package slack

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// token_command supplies the bot and app token: every Web API and
// apps.connections.open call carries the command's current output, re-read once
// the cached value is older than the refresh interval.
func TestTokenCommand_RefreshesTokenOnRequests(t *testing.T) {
	seen := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		tok := r.PostFormValue("token")
		if h := r.Header.Get("Authorization"); h != "" {
			tok = strings.TrimPrefix(h, "Bearer ")
		}
		seen <- r.URL.Path + " " + tok
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"url":"ws://127.0.0.1:1/socket"}`)
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(file, []byte("tok-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pl, err := New(map[string]any{"token_command": "cat " + file, "base_url": srv.URL + "/api/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p := pl.(*Platform)
	p.tokens.ttl = 0 // re-run the command on every request
	p.client = p.newClient()

	if _, err := p.client.AuthTest(); err != nil {
		t.Fatalf("AuthTest: %v", err)
	}
	if got := <-seen; got != "/api/auth.test tok-1" {
		t.Errorf("got %q", got)
	}
	if err := os.WriteFile(file, []byte("tok-2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.client.StartSocketModeContext(t.Context()); err != nil {
		t.Fatalf("apps.connections.open: %v", err)
	}
	if got := <-seen; got != "/api/apps.connections.open tok-2" {
		t.Errorf("got %q", got)
	}
}

func TestTokenCommand_ExclusiveWithStaticTokens(t *testing.T) {
	if _, err := New(map[string]any{"token_command": "true", "bot_token": "x", "app_token": "y"}); err == nil {
		t.Fatal("expected an error when token_command and bot_token/app_token are both set")
	}
	if _, err := New(map[string]any{}); err == nil {
		t.Fatal("expected an error when no token source is set")
	}
}
