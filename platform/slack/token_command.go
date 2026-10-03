package slack

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// tokenPlaceholder is the token slack-go is built with when token_command is
// set; tokenTransport swaps it for the command's current output on the wire.
const tokenPlaceholder = "cc-connect-token-command"

// commandToken runs token_command to obtain the token used as both bot and app
// token (e.g. a short-lived credential printed by a CLI), caching it for ttl.
type commandToken struct {
	command string
	ttl     time.Duration

	mu    sync.Mutex
	value string
	at    time.Time
}

func (c *commandToken) get(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != "" && time.Since(c.at) < c.ttl {
		return c.value, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", c.command).Output()
	if err != nil {
		return "", fmt.Errorf("slack: token_command: %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", fmt.Errorf("slack: token_command printed no token")
	}
	c.value, c.at = tok, time.Now()
	return tok, nil
}

// tokenTransport replaces tokenPlaceholder in the Authorization header and in
// form bodies, the two places slack-go puts the token.
type tokenTransport struct {
	tokens *commandToken
	next   http.RoundTripper
}

func (t *tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tok, err := t.tokens.get(req.Context())
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	if h := req.Header.Get("Authorization"); strings.Contains(h, tokenPlaceholder) {
		req.Header.Set("Authorization", strings.ReplaceAll(h, tokenPlaceholder, tok))
	}
	if req.Body != nil && strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = bytes.ReplaceAll(body, []byte(tokenPlaceholder), []byte(tok))
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	return t.next.RoundTrip(req)
}
