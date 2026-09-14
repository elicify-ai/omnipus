package anthropicprovider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// The Anthropic half of the silence check (founder decision 2026-09-14).
// Byte-level arming matters here specifically: the SDK swallows Anthropic's
// `ping` keep-alives inside Stream.Next (they never surface as events), so a
// ping-only stream must still count as alive.

// anthropicDripServer serves a Messages SSE stream: message_start, then `n`
// pings every interval, then one text delta and message_stop. n < 0 sends
// message_start and then holds the connection in total silence.
func anthropicDripServer(t *testing.T, interval time.Duration, n int) *httptest.Server {
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
		if n < 0 {
			<-release
			return
		}
		for i := 0; i < n; i++ {
			select {
			case <-release:
				return
			case <-time.After(interval):
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
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv
}

// A stream whose only traffic is SDK-swallowed pings is NOT silent: the
// byte-level arming keeps it alive and it completes on its own terms.
func TestChatStream_PingOnlyStreamIsNotAborted(t *testing.T) {
	srv := anthropicDripServer(t, 150*time.Millisecond, 6)
	p := NewProviderWithBaseURL("test-token", srv.URL).WithStreamStallTimeout(400 * time.Millisecond)

	start := time.Now()
	resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 256}, nil, nil)
	if err != nil {
		t.Fatalf("a ping-only stream must not be aborted as silent (ran %s): %v", time.Since(start), err)
	}
	if elapsed := time.Since(start); elapsed < 700*time.Millisecond {
		t.Fatalf("call finished suspiciously fast (%s) — the pings did not actually drip", elapsed)
	}
	if resp.Content != "Done." {
		t.Errorf("resp.Content = %q, want %q", resp.Content, "Done.")
	}
}

// A stream that goes fully mute past the limit is aborted with the typed
// stall error.
func TestChatStream_SilentAnthropicStreamIsAbortedAsAStall(t *testing.T) {
	srv := anthropicDripServer(t, 0, -1)
	p := NewProviderWithBaseURL("test-token", srv.URL).WithStreamStallTimeout(400 * time.Millisecond)

	start := time.Now()
	_, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		nil, "claude-sonnet-4-6", map[string]any{"max_tokens": 256}, nil, nil)
	if err == nil {
		t.Fatal("a fully silent stream must be aborted")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stall abort took %s; the monitor did not fire promptly", elapsed)
	}
	if !errors.Is(err, common.ErrStreamStalled) {
		t.Fatalf("want errors.Is(err, common.ErrStreamStalled); got %v", err)
	}
}

// The silence limit defaults to the shipped five minutes when unset.
func TestStreamStallTimeout_DefaultsToShippedLimit(t *testing.T) {
	p := NewProvider("test-token")
	if got := p.effectiveStreamStallTimeout(); got != common.DefaultStreamStallTimeout {
		t.Fatalf("unset stall timeout must resolve to %s; got %s", common.DefaultStreamStallTimeout, got)
	}
}
