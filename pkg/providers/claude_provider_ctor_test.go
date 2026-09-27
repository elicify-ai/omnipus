// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// WP-E item 3 (issue #750): the outer constructor the factory switch needs.
//
// PINNED CONTRACT for backend-lead — the test does not compile until this
// exact symbol exists (the compile failure is the RED evidence by design):
//
//	func NewClaudeProviderWithTimeout(token, apiBase string, timeout time.Duration) *ClaudeProvider
//
// Semantics pinned, all inherited from the inner adapter's documented
// contracts (pkg/providers/anthropic/provider.go):
//   - apiBase is used as the request base — the SDK default only when empty
//     (NewProviderWithBaseURL + normalizeBaseURL);
//   - timeout is the STREAM-SILENCE limit (WithStreamStallTimeout): a stream
//     delivering nothing for this long aborts with common.ErrStreamStalled; a
//     stream that keeps delivering is never cut; non-positive resolves to the
//     shipped default, never to an instant abort.
func TestNewClaudeProviderWithTimeout(t *testing.T) {
	t.Run("uses the given api_base verbatim", func(t *testing.T) {
		// Deliberately /v1-free so the assertion stays on "the given base is
		// used" and does not depend on the adapter's /v1 normalization.
		cp := NewClaudeProviderWithTimeout("test-token", "https://relay.example", 30*time.Second)
		if got := cp.delegate.BaseURL(); got != "https://relay.example" {
			t.Fatalf("base URL = %q, want the given api_base %q", got, "https://relay.example")
		}
	})

	t.Run("empty api_base falls back to the adapter default base", func(t *testing.T) {
		// "https://api.anthropic.com" is the adapter's documented default
		// (defaultBaseURL, pkg/providers/anthropic/provider.go).
		cp := NewClaudeProviderWithTimeout("test-token", "", 30*time.Second)
		if got := cp.delegate.BaseURL(); got != "https://api.anthropic.com" {
			t.Fatalf("base URL = %q, want the adapter default %q", got, "https://api.anthropic.com")
		}
	})

	t.Run("applies the timeout as the stream-silence limit", func(t *testing.T) {
		srv := wpeStreamServer(t, "silent")
		cp := NewClaudeProviderWithTimeout("test-token", srv.URL, 400*time.Millisecond)

		// The context deadline only bounds the test: with the silence limit
		// correctly wired, the stall fires long before it, and a botched
		// wiring fails here as a clean DeadlineExceeded instead of a hang.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		start := time.Now()
		_, err := cp.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}},
			nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 256}, nil, nil)
		if err == nil {
			t.Fatal("a fully silent stream must be aborted")
		}
		if !errors.Is(err, common.ErrStreamStalled) {
			t.Fatalf("want errors.Is(err, common.ErrStreamStalled); got %v", err)
		}
		if elapsed := time.Since(start); elapsed > 4*time.Second {
			t.Errorf("stall abort took %s; the silence limit did not fire promptly", elapsed)
		}
	})

	t.Run("a delivering stream is never cut (silence limit, not wall-clock)", func(t *testing.T) {
		srv := wpeStreamServer(t, "pings")
		cp := NewClaudeProviderWithTimeout("test-token", srv.URL, 400*time.Millisecond)

		start := time.Now()
		resp, err := cp.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
			nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 256}, nil, nil)
		if err != nil {
			t.Fatalf("a stream that keeps delivering must not be cut: %v", err)
		}
		if elapsed := time.Since(start); elapsed < 700*time.Millisecond {
			t.Fatalf("call finished suspiciously fast (%s) — the pings did not actually drip", elapsed)
		}
		if resp.Content != "Done." {
			t.Errorf("resp.Content = %q, want %q", resp.Content, "Done.")
		}
	})

	t.Run("a zero timeout is the shipped default, not an instant abort", func(t *testing.T) {
		srv := wpeStreamServer(t, "silent")
		cp := NewClaudeProviderWithTimeout("test-token", srv.URL, 0)

		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()

		_, err := cp.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}},
			nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 256}, nil, nil)
		if err == nil {
			t.Fatal("the 1.5s context must end the call")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want the context deadline to end the call; got %v", err)
		}
		if errors.Is(err, common.ErrStreamStalled) {
			t.Fatalf("a zero timeout must resolve to the shipped default, not abort instantly; got %v", err)
		}
	})
}

// wpeStreamServer serves an Anthropic Messages SSE stream in one of two
// shapes, copied from the adapter's own stall fixtures
// (pkg/providers/anthropic/stream_stall_test.go):
//   - "silent": message_start, then total silence until the test ends;
//   - "pings": message_start, six SDK-swallowed pings 150ms apart (a
//     ping-only stream counts as alive), then one text delta and stop.
func wpeStreamServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		send := func(s string) bool {
			if _, err := w.Write([]byte(s)); err != nil {
				return false
			}
			if flusher != nil {
				flusher.Flush()
			}
			return true
		}
		if !send("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"," +
			"\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\"," +
			"\"stop_reason\":null,\"usage\":{\"input_tokens\":9,\"output_tokens\":0}}}\n\n") {
			return
		}
		switch mode {
		case "silent":
			<-release
			return
		case "pings":
			for i := 0; i < 6; i++ {
				select {
				case <-release:
					return
				case <-time.After(150 * time.Millisecond):
					if !send("event: ping\ndata: {\"type\":\"ping\"}\n\n") {
						return
					}
				}
			}
			send("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0," +
				"\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
			send("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0," +
				"\"delta\":{\"type\":\"text_delta\",\"text\":\"Done.\"}}\n\n")
			send("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
			send("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}," +
				"\"usage\":{\"output_tokens\":7}}\n\n")
			send("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv
}
