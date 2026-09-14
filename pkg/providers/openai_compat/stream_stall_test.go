package openai_compat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Founder decision 2026-09-14 (UAT E-15c): a streaming call receiving NO
// bytes of any kind for the silence limit is aborted as a provider stall;
// a call that keeps streaming — however slowly — is never cut. These tests
// use a deliberately short limit (hundreds of milliseconds) standing in for
// the shipped 5-minute default, and a drip interval standing in for a model
// reasoning for ~21 minutes: the ratio, not the absolute times, is the
// behaviour under test.

// dripServer serves an SSE stream: an opening delta immediately, then a line
// rendered by drip every interval. drips >= 0 ends the stream with [DONE]
// after that many drips (a stream that terminates on its own); drips < 0
// holds the connection open after the opening delta — TOTAL silence, never
// sending [DONE] — until the returned release channel is closed, modelling a
// provider that went mute mid-call.
func dripServer(t *testing.T, interval time.Duration, drips int, drip func(i int) string) (*httptest.Server, chan struct{}) {
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
		if !send(`data: {"choices":[{"delta":{"content":""}}]}` + "\n\n") {
			return
		}
		if drips < 0 {
			<-release
			return
		}
		for i := 0; i < drips; i++ {
			select {
			case <-release:
				return
			case <-time.After(interval):
				if !send(drip(i)) {
					return
				}
			}
		}
		send("data: [DONE]\n\n")
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv, release
}

func stallTestProvider(t *testing.T, url string, stall time.Duration) *Provider {
	t.Helper()
	p, err := NewProvider("test-key", url, "", WithStreamStallTimeout(stall))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

// A stream that keeps sending reasoning deltas slower than the overall call
// length but faster than the silence limit must complete. This is the scaled
// stand-in for "reasoning every 30 s for 20 minutes with a 5-minute limit".
func TestChatStream_SlowButStreamingReasoningIsNotAborted(t *testing.T) {
	// 6 drips × 250 ms ≈ 1.5 s of streaming against a 600 ms silence limit:
	// each drip re-arms the clock, so the call outlives the limit 2.5× over
	// and still completes on its own terms.
	srv, _ := dripServer(t, 250*time.Millisecond, 6, func(i int) string {
		return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning\":\"chunk %02d\"}}]}\n\n", i)
	})
	p := stallTestProvider(t, srv.URL, 600*time.Millisecond)

	start := time.Now()
	resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		nil, "test-model", nil, nil, nil)
	if err != nil {
		t.Fatalf("a slow-but-streaming call must not be aborted; took %s; err=%v", time.Since(start), err)
	}
	if elapsed := time.Since(start); elapsed < 1200*time.Millisecond {
		t.Fatalf("call finished suspiciously fast (%s) — it did not actually drip slowly", elapsed)
	}
	if resp.Content != "" {
		t.Errorf("reasoning leaked into content: %q", resp.Content)
	}
}

// A provider keep-alive (an SSE comment line, no JSON payload) is bytes too:
// it must re-arm the silence clock exactly like a reasoning delta.
func TestChatStream_KeepAliveCommentsCountAsActivity(t *testing.T) {
	srv, _ := dripServer(t, 200*time.Millisecond, 6, func(int) string { return ": keep-alive\n\n" })
	p := stallTestProvider(t, srv.URL, 500*time.Millisecond)

	resp, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		nil, "test-model", nil, nil, nil)
	if err != nil {
		t.Fatalf("a keep-alive-only stream must not be aborted as silent: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response with nil error")
	}
}

// The founder's scenario: the provider accepts the request, sends one delta,
// then goes fully mute. The call must end with the typed stall error — not
// hang forever, and not masquerade as a transient stream reset.
func TestChatStream_TotallySilentStreamIsAbortedAsAStall(t *testing.T) {
	srv, _ := dripServer(t, 0, -1, nil) // opening delta, then total silence
	p := stallTestProvider(t, srv.URL, 400*time.Millisecond)

	start := time.Now()
	_, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		nil, "test-model", nil, nil, nil)
	if err == nil {
		t.Fatal("a fully silent stream must be aborted")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stall abort took %s; the monitor did not fire promptly", elapsed)
	}
	if !errors.Is(err, common.ErrStreamStalled) {
		t.Fatalf("want errors.Is(err, common.ErrStreamStalled); got %v", err)
	}
	var se *common.StallError
	if !errors.As(err, &se) || se.SilentFor != 400*time.Millisecond {
		t.Fatalf("stall error must carry the configured limit (400ms); got %+v", se)
	}
	if strings.Contains(err.Error(), "streaming read error") {
		t.Fatalf("a stall must not be reported as a transient streaming read error: %v", err)
	}
}

// The same body-closed read error means two different things depending on
// who closed the body. Fired() is the discriminator: without it (or with a
// watch that never fired) the read keeps its historical streaming-read-error
// classification — a genuine server-side drop stays retryable as before; with
// it, the same bytes are the typed stall.
func TestParseStreamResponse_BodyClosedClassificationDependsOnFired(t *testing.T) {
	bodyClosed := errors.New("http: read on closed response body")

	t.Run("no watch: historical streaming read error", func(t *testing.T) {
		_, err := parseStreamResponse(context.Background(), &errReader{err: bodyClosed}, nil, nil, nil)
		if err == nil {
			t.Fatal("expected an error")
		}
		if errors.Is(err, common.ErrStreamStalled) {
			t.Fatalf("without a stall watch a body-closed read must stay a streaming read error, got %v", err)
		}
		if !strings.Contains(err.Error(), "streaming read error") {
			t.Fatalf("want streaming read error; got %v", err)
		}
	})

	t.Run("fired watch: typed stall error", func(t *testing.T) {
		closed := make(chan struct{})
		watch := common.WatchStreamStall(context.Background(), func() { close(closed) }, 50*time.Millisecond)
		defer watch.Stop()
		// Wait for the monitor to fire (it closed our dummy body), then feed
		// the same read error the close produces in a real stream.
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Fatal("monitor did not fire")
		}
		err := func() error {
			_, err := parseStreamResponse(context.Background(), &errReader{err: bodyClosed}, nil, nil, watch)
			return err
		}()
		if !errors.Is(err, common.ErrStreamStalled) {
			t.Fatalf("a fired watch plus a body-closed read must classify as a stall; got %v", err)
		}
		var se *common.StallError
		if !errors.As(err, &se) || se.SilentFor != 50*time.Millisecond {
			t.Fatalf("stall error must carry the configured limit; got %+v", se)
		}
	})
}

// The silence limit is a per-provider configuration, defaulting to the
// shipped five minutes.
func TestStreamStallTimeout_DefaultAndOverride(t *testing.T) {
	if common.DefaultStreamStallTimeout != 5*time.Minute {
		t.Fatalf("shipped default changed: %s", common.DefaultStreamStallTimeout)
	}
	p, err := NewProvider("k", "http://127.0.0.1:1", "")
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if got := p.effectiveStreamStallTimeout(); got != 5*time.Minute {
		t.Fatalf("unset stall timeout must resolve to the default; got %s", got)
	}
	p2, err := NewProvider("k", "http://127.0.0.1:1", "", WithStreamStallTimeout(90*time.Second))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if got := p2.effectiveStreamStallTimeout(); got != 90*time.Second {
		t.Fatalf("explicit stall timeout must win; got %s", got)
	}
}
