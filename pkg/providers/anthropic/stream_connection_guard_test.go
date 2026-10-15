package anthropicprovider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestChatStream_GuardsAConnectionThatNeverSendsHeaders: with no turn
// timeout (issue #1306), a streaming call to an Anthropic endpoint that
// accepts the request but never sends response headers must end with a
// timeout error after the connection guard — not hang until Stop.
func TestChatStream_GuardsAConnectionThatNeverSendsHeaders(t *testing.T) {
	var reached atomic.Bool
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	p := NewProviderWithBaseURL("test-token", srv.URL).WithConnectionGuardTimeout(200 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
			nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 64}, nil, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ChatStream against an endpoint that never sends headers returned no error")
		}
		if !reached.Load() {
			t.Fatalf("the request never reached the server; the error is unrelated: %v", err)
		}
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("error = %v, want a network timeout from the connection guard", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ChatStream hung waiting for response headers: no connection guard")
	}
}

// TestChatStream_SlowStreamIsNotCutByTheConnectionGuard: once headers have
// arrived, a stream that keeps delivering — longer in total than the guard —
// completes normally (the guard is not a whole-stream deadline).
func TestChatStream_SlowStreamIsNotCutByTheConnectionGuard(t *testing.T) {
	srv := anthropicDripServer(t, 150*time.Millisecond, 4) // ~600 ms of pings after headers
	p := NewProviderWithBaseURL("test-token", srv.URL).WithConnectionGuardTimeout(200 * time.Millisecond)
	resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 64}, nil, nil)
	if err != nil {
		t.Fatalf("a slow but flowing stream was cut by the connection guard: %v", err)
	}
	if resp.Content != "Done." {
		t.Errorf("resp.Content = %q, want %q", resp.Content, "Done.")
	}
}
