package openai_compat

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestProvider_ChatStreamGuardsAConnectionThatNeverSendsHeaders pins the
// connection guard issue #1306 keeps: with no work time limit, a streaming
// call to a provider that accepts the request and never sends response
// headers must still end with a retryable error after the request timeout —
// it must not hang the turn forever (the stall watcher only starts once
// headers arrive).
func TestProvider_ChatStreamGuardsAConnectionThatNeverSendsHeaders(t *testing.T) {
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

	p, err := NewProvider("key", srv.URL, "", WithRequestTimeout(200*time.Millisecond))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, streamErr := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil, nil, nil)
		done <- streamErr
	}()
	select {
	case streamErr := <-done:
		if streamErr == nil {
			t.Fatal("ChatStream against a provider that never sends headers returned no error")
		}
		if !reached.Load() {
			t.Fatalf("the request never reached the server; the error is unrelated: %v", streamErr)
		}
		if errors.Is(streamErr, context.Canceled) {
			t.Fatalf("connection guard surfaced as a cancel (%v); it must read as a provider timeout", streamErr)
		}
		var ne net.Error
		if !errors.As(streamErr, &ne) || !ne.Timeout() {
			t.Fatalf("error = %v, want a network timeout from the connection guard", streamErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ChatStream hung waiting for response headers: no connection guard (issue #1306 kept guard)")
	}
}

// TestProvider_ChatStreamSlowBodyIsNotCutByTheConnectionGuard: the guard
// bounds only the wait for response headers. A stream whose headers arrive
// promptly and whose body keeps flowing slowly — longer than the request
// timeout in total — must finish normally (no whole-stream deadline).
func TestProvider_ChatStreamSlowBodyIsNotCutByTheConnectionGuard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		flusher.Flush()
		for i := 0; i < 4; i++ {
			time.Sleep(150 * time.Millisecond)
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"part%d \"}}]}\n\n", i)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p, err := NewProvider("key", srv.URL, "", WithRequestTimeout(200*time.Millisecond))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil, nil, nil)
	if err != nil {
		t.Fatalf("a slow but flowing stream (600 ms total, 200 ms request timeout) was cut: %v", err)
	}
	if resp == nil || resp.Content != "part0 part1 part2 part3 " {
		t.Fatalf("streamed content = %+v, want all four parts", resp)
	}
}
