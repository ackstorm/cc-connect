package slack

import (
	"testing"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/chenhg5/cc-connect/core"
)

func TestNormalizeSessionScope(t *testing.T) {
	cases := []struct {
		raw   any
		share bool
		want  string
	}{
		{nil, false, "user"},
		{nil, true, "channel"},
		{"", false, "user"},
		{"", true, "channel"},
		{"user", true, "user"},
		{"channel", false, "channel"},
		{"thread", false, "thread"},
		{"Thread", false, "thread"},     // case-insensitive
		{" channel ", false, "channel"}, // trimmed
		{"bogus", false, "user"},        // unknown -> share-derived default
		{"bogus", true, "channel"},
	}
	for _, c := range cases {
		if got := normalizeSessionScope(c.raw, c.share); got != c.want {
			t.Errorf("normalizeSessionScope(%v, share=%v) = %q, want %q", c.raw, c.share, got, c.want)
		}
	}
}

func TestBuildSessionKey(t *testing.T) {
	const (
		ch     = "C123"
		user   = "U456"
		thread = "1717000000.000100"
	)
	cases := []struct {
		scope    string
		threadTS string
		want     string
	}{
		{"user", thread, "slack:C123:U456"}, // thread ignored in user scope
		{"channel", thread, "slack:C123"},   // user/thread ignored in channel scope
		{"thread", thread, "slack:C123:t:1717000000.000100"},
		{"thread", "", "slack:C123:U456"}, // no thread context -> falls back to user
		{"", thread, "slack:C123:U456"},   // empty scope behaves as user
	}
	for _, c := range cases {
		p := &Platform{sessionScope: c.scope}
		if got := p.buildSessionKey(ch, user, c.threadTS); got != c.want {
			t.Errorf("scope=%q threadTS=%q: buildSessionKey = %q, want %q", c.scope, c.threadTS, got, c.want)
		}
	}
}

func TestReconstructReplyCtx(t *testing.T) {
	p := &Platform{}
	cases := []struct {
		key       string
		channel   string
		timestamp string // thread_ts, "" when none
	}{
		{"slack:C123:U456", "C123", ""},                                 // user scope -> no thread
		{"slack:C123", "C123", ""},                                      // channel scope
		{"slack:C123:t:1717000000.000100", "C123", "1717000000.000100"}, // thread scope -> thread ts
	}
	for _, c := range cases {
		got, err := p.ReconstructReplyCtx(c.key)
		if err != nil {
			t.Fatalf("ReconstructReplyCtx(%q) error: %v", c.key, err)
		}
		rc := got.(replyContext)
		if rc.channel != c.channel || rc.timestamp != c.timestamp {
			t.Errorf("ReconstructReplyCtx(%q) = {channel:%q timestamp:%q}, want {channel:%q timestamp:%q}",
				c.key, rc.channel, rc.timestamp, c.channel, c.timestamp)
		}
	}
	if _, err := p.ReconstructReplyCtx("telegram:123"); err == nil {
		t.Error("ReconstructReplyCtx should reject non-slack keys")
	}
}

// With session_scope=thread a top-level DM starts its own thread, like a
// channel message does: its session key and reply thread are the message ts,
// so the follow-ups in that thread land in the same session. Other scopes keep
// replying top-level in DMs.
func TestHandleEvent_DMThreadScope(t *testing.T) {
	dm := func(ts, threadTS string) socketmode.Event {
		return socketmode.Event{
			Type: socketmode.EventTypeEventsAPI,
			Data: slackevents.EventsAPIEvent{
				Type: slackevents.CallbackEvent,
				InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.MessageEvent{
					User: "U1", Channel: "D1", ChannelType: "im", TimeStamp: ts, ThreadTimeStamp: threadTS, Text: "hi",
				}},
			},
		}
	}
	root, reply := freshTS(1), freshTS(2)
	cases := []struct {
		scope            string
		wantKey, wantRTS string // for the top-level message; the reply must match
	}{
		{"thread", "slack:D1:t:" + root, root},
		{"user", "slack:D1:U1", ""},
	}
	for _, c := range cases {
		var got []*core.Message
		p := &Platform{allowFrom: "*", sessionScope: c.scope, channelNameCache: map[string]string{"D1": "dm"}}
		p.userNameCache.Store("U1", "Jim")
		p.handler = func(_ core.Platform, m *core.Message) { got = append(got, m) }

		p.handleEvent(dm(root, ""))
		p.handleEvent(dm(reply, root))
		if len(got) != 2 {
			t.Fatalf("scope=%s: dispatched %d, want 2", c.scope, len(got))
		}
		if got[0].SessionKey != c.wantKey {
			t.Errorf("scope=%s: top-level key = %q, want %q", c.scope, got[0].SessionKey, c.wantKey)
		}
		if rts := got[0].ReplyCtx.(replyContext).timestamp; rts != c.wantRTS {
			t.Errorf("scope=%s: top-level reply thread = %q, want %q", c.scope, rts, c.wantRTS)
		}
		if c.scope == "thread" && got[1].SessionKey != got[0].SessionKey {
			t.Errorf("scope=thread: thread reply key = %q, want %q", got[1].SessionKey, got[0].SessionKey)
		}
	}
}
