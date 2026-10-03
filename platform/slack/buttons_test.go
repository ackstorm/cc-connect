package slack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/chenhg5/cc-connect/core"
)

func TestSendWithButtons_PostsBlockKitButtonsInThread(t *testing.T) {
	posted := make(chan map[string]string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form := map[string]string{"path": r.URL.Path}
		for k := range r.PostForm {
			form[k] = r.PostFormValue(k)
		}
		posted <- form
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"ts":"2.0"}`)
	}))
	defer srv.Close()

	p := &Platform{client: slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/api/"))}
	buttons := [][]core.ButtonOption{
		{{Text: "Allow", Data: "perm:allow"}, {Text: "Deny", Data: "perm:deny"}},
		{{Text: "Allow all", Data: "perm:allow_all"}},
	}
	if err := p.SendWithButtons(t.Context(), replyContext{channel: "D1", timestamp: "1.0"}, "May I?", buttons); err != nil {
		t.Fatalf("SendWithButtons: %v", err)
	}
	form := <-posted
	if form["path"] != "/api/chat.postMessage" || form["channel"] != "D1" || form["thread_ts"] != "1.0" {
		t.Fatalf("unexpected post: %v", form)
	}
	if form["text"] != "May I?" {
		t.Errorf("fallback text = %q", form["text"])
	}
	var blocks []struct {
		Type     string `json:"type"`
		Expand   bool   `json:"expand"`
		Elements []struct {
			Value string `json:"value"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(form["blocks"]), &blocks); err != nil {
		t.Fatalf("blocks: %v (%s)", err, form["blocks"])
	}
	if len(blocks) != 2 || blocks[0].Type != "section" || !blocks[0].Expand || blocks[1].Type != "actions" {
		t.Fatalf("want an expanded section and one actions row, got %s", form["blocks"])
	}
	var values []string
	for _, el := range blocks[1].Elements {
		values = append(values, el.Value)
	}
	if got := strings.Join(values, ","); got != "perm:allow,perm:deny,perm:allow_all" {
		t.Errorf("button values = %q", got)
	}
}

func TestHandleEvent_PermissionButtonClick(t *testing.T) {
	updates := make(chan map[string]string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		updates <- map[string]string{"path": r.URL.Path, "ts": r.PostFormValue("ts"), "text": r.PostFormValue("text"), "blocks": r.PostFormValue("blocks")}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	var got []*core.Message
	p := &Platform{allowFrom: "*", sessionScope: "thread", channelNameCache: map[string]string{"D1": "dm"},
		client: slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/api/"))}
	p.userNameCache.Store("U1", "Jim")
	p.handler = func(_ core.Platform, m *core.Message) { got = append(got, m) }

	// Decoded from the JSON Slack sends, so the test covers slack-go's
	// block_actions parsing (it needs block_id to file the action under BlockActions).
	click := func(value string) socketmode.Event {
		var cb slack.InteractionCallback
		raw := fmt.Sprintf(`{"type":"block_actions","user":{"id":"U1"},"channel":{"id":"D1"},
			"container":{"type":"message","channel_id":"D1","message_ts":"2.0","thread_ts":"1.0"},
			"message":{"ts":"2.0","text":"May I read /etc/hosts?"},
			"actions":[{"type":"button","block_id":"buttons_0","action_id":%q,"value":%q}]}`, value, value)
		if err := json.Unmarshal([]byte(raw), &cb); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return socketmode.Event{Type: socketmode.EventTypeInteractive, Data: cb}
	}
	p.handleEvent(click("perm:allow_all"))
	p.handleEvent(click("cmd:/new"))
	upd := <-updates
	if upd["path"] != "/api/chat.update" || upd["ts"] != "2.0" || upd["text"] != "May I read /etc/hosts?" ||
		strings.Contains(upd["blocks"], "actions") || !strings.Contains(upd["blocks"], "May I read") {
		t.Errorf("prompt must be kept without buttons, got %v", upd)
	}
	if len(got) != 1 {
		t.Fatalf("dispatched %d messages, want 1 (non-perm actions are ignored)", len(got))
	}
	m := got[0]
	if m.Content != "allow all" || !m.IsPermissionResponse {
		t.Errorf("content=%q permission=%v", m.Content, m.IsPermissionResponse)
	}
	if m.SessionKey != "slack:D1:t:1.0" {
		t.Errorf("session key = %q, want the thread's", m.SessionKey)
	}
	if rc := m.ReplyCtx.(replyContext); rc.channel != "D1" || rc.timestamp != "1.0" {
		t.Errorf("reply ctx = %+v", rc)
	}
}
