package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/chenhg5/cc-connect/core"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// SendWithButtons posts content with all buttons on one Block Kit actions row
// (Slack wraps it on narrow screens). Each button's action_id and value carry
// ButtonOption.Data (e.g. "perm:allow"). Implements core.InlineButtonSender.
func (p *Platform) SendWithButtons(ctx context.Context, rctx any, content string, buttons [][]core.ButtonOption) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("slack: invalid reply context type %T", rctx)
	}
	text := core.MarkdownToSlackMrkdwn(content)
	var elems []slack.BlockElement
	for _, row := range buttons {
		for _, b := range row {
			btn := slack.NewButtonBlockElement(b.Data, b.Data, slack.NewTextBlockObject(slack.PlainTextType, b.Text, false, false))
			if strings.HasSuffix(b.Data, ":allow") {
				btn.Style = slack.StylePrimary
			} else if strings.HasSuffix(b.Data, ":deny") {
				btn.Style = slack.StyleDanger
			}
			elems = append(elems, btn)
		}
	}
	blocks := append(promptBlocks(text), slack.NewActionBlock("buttons", elems...))
	opts := []slack.MsgOption{slack.MsgOptionText(text, false), slack.MsgOptionBlocks(blocks...)}
	if rc.timestamp != "" {
		opts = append(opts, slack.MsgOptionPostMessageParameters(slack.PostMessageParameters{ThreadTimestamp: rc.timestamp}))
	}
	if _, _, err := p.client.PostMessageContext(ctx, rc.channel, opts...); err != nil {
		return fmt.Errorf("slack: send buttons: %w", err)
	}
	return nil
}

// promptBlocks renders the prompt text fully expanded (no "Show more").
func promptBlocks(text string) []slack.Block {
	return []slack.Block{slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, text, false, false), nil, nil,
		slack.SectionBlockOptionExpand(true),
	)}
}

// permissionReplies maps a permission button's data to the reply the engine
// understands; the engine then confirms the choice in the thread.
var permissionReplies = map[string]string{
	"perm:allow":     "allow",
	"perm:deny":      "deny",
	"perm:allow_all": "allow all",
}

// handleInteractive turns a click on a permission button into the same
// permission reply a user would type in the thread.
func (p *Platform) handleInteractive(evt socketmode.Event) {
	cb, ok := evt.Data.(slack.InteractionCallback)
	if !ok {
		return
	}
	if evt.Request != nil && p.socket != nil {
		p.socket.Ack(*evt.Request)
	}
	if cb.Type != slack.InteractionTypeBlockActions || len(cb.ActionCallback.BlockActions) == 0 {
		return
	}
	reply, ok := permissionReplies[cb.ActionCallback.BlockActions[0].Value]
	if !ok {
		return
	}
	if !core.AllowList(p.allowFrom, cb.User.ID) {
		slog.Debug("slack: button click from unauthorized user", "user", cb.User.ID)
		return
	}
	channel := cb.Container.ChannelID
	if channel == "" {
		channel = cb.Channel.ID
	}
	threadTS := cb.Container.ThreadTs

	// Keep the prompt but drop its buttons so it can't be answered twice.
	if p.client != nil && cb.Container.MessageTs != "" && cb.Message.Text != "" {
		if _, _, _, err := p.client.UpdateMessage(channel, cb.Container.MessageTs,
			slack.MsgOptionText(cb.Message.Text, false),
			slack.MsgOptionBlocks(promptBlocks(cb.Message.Text)...),
		); err != nil {
			slog.Debug("slack: permission prompt update failed", "error", err)
		}
	}

	p.handler(p, &core.Message{
		SessionKey:           p.buildSessionKey(channel, cb.User.ID, threadTS),
		Platform:             "slack",
		UserID:               cb.User.ID,
		UserName:             p.resolveUserName(cb.User.ID),
		ChatName:             p.resolveChannelNameForMsg(channel),
		Content:              reply,
		MessageID:            cb.Container.MessageTs,
		ReplyCtx:             replyContext{channel: channel, timestamp: threadTS},
		IsPermissionResponse: true,
	})
}

var _ core.InlineButtonSender = (*Platform)(nil)
