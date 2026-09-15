package agent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestToolCallProgress_ReasoningOnlyStreamAdvancesTheTimestamp drives the REAL
// OpenAI-compatible streaming provider against a server that sends nothing but
// reasoning, feeding the turn's own recordToolCallProgress exactly as loop.go
// does. It pins the founder decision of 2026-09-14 (UAT E-15c): a model that is
// only thinking is working, so the turn's progress timestamp — the one the
// goal keeper's work check and `delegate status` both read — must move while
// only reasoning arrives, and the snapshot must describe thinking rather than a
// tool call.
//
// The server pauses between chunks until the test has observed the previous
// one, so the assertions are ordered by construction rather than by sleeps.
func TestToolCallProgress_ReasoningOnlyStreamAdvancesTheTimestamp(t *testing.T) {
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 128)
	step := make(chan struct{})
	// stop releases a handler still parked on step when the test fails early;
	// without it the deferred server.Close() waits for that handler forever and
	// a failing assertion turns into a hung test binary.
	stop := make(chan struct{})
	wait := func() bool {
		select {
		case <-step:
			return true
		case <-stop:
			return false
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		send := func(s string) {
			if _, err := w.Write([]byte(s)); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		send(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning\":%q}}]}\n\n", first))
		if !wait() {
			return
		}
		send(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning\":%q}}]}\n\n", second))
		if !wait() {
			return
		}
		send(`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n")
		send("data: [DONE]\n\n")
	}))
	defer server.Close()
	defer close(stop) // runs before server.Close (LIFO)

	provider, err := providers.NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
		"test-key", server.URL, "", "", 0, nil)
	if err != nil {
		t.Fatalf("NewHTTPProvider: %v", err)
	}

	ts := &turnState{}
	done := make(chan error, 1)
	go func() {
		_, streamErr := provider.ChatStream(t.Context(), []providers.Message{{Role: "user", Content: "hi"}},
			nil, "test-model", nil, nil, ts.recordToolCallProgress)
		done <- streamErr
	}()

	waitFor := func(what string, cond func(snap tools.ToolCallProgressSnapshot) bool) tools.ToolCallProgressSnapshot {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			snap := ts.ToolCallProgress()
			if cond(snap) {
				return snap
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s; last snapshot %+v", what, ts.ToolCallProgress())
		return tools.ToolCallProgressSnapshot{}
	}

	snap1 := waitFor("the first reasoning delta to be recorded", func(s tools.ToolCallProgressSnapshot) bool {
		return s.ReasoningBytes == len(first)
	})
	if snap1.LastActivity.IsZero() {
		t.Fatal("reasoning arrived but the progress timestamp was never stamped")
	}
	if snap1.Name != "" || snap1.ArgsBytes != 0 {
		t.Fatalf("a reasoning-only stream must not read as a tool call: %+v", snap1)
	}

	// Guarantee a distinguishable clock reading before the next delta.
	time.Sleep(2 * time.Millisecond)
	step <- struct{}{}

	snap2 := waitFor("the second reasoning delta to be recorded", func(s tools.ToolCallProgressSnapshot) bool {
		return s.ReasoningBytes == len(first)+len(second)
	})
	if !snap2.LastActivity.After(snap1.LastActivity) {
		t.Fatalf("progress timestamp did not advance while only reasoning arrived: first=%v second=%v",
			snap1.LastActivity, snap2.LastActivity)
	}

	step <- struct{}{}
	if streamErr := <-done; streamErr != nil {
		t.Fatalf("ChatStream() error = %v", streamErr)
	}
}

// TestToolCallProgress_ReasoningAfterToolCallDescribesThinking pins that the
// snapshot always describes the most recent kind of delta. A tool call that
// streamed and was followed by reasoning (interleaved thinking) must not keep
// claiming the old tool call is being generated.
func TestToolCallProgress_ReasoningAfterToolCallDescribesThinking(t *testing.T) {
	ts := &turnState{}
	ts.recordToolCallProgress(protocoltypes.ToolCallProgress{
		Index: 0, Name: "bash", ArgsBytes: 300, TotalArgsBytes: 300,
	})
	ts.recordToolCallProgress(protocoltypes.ToolCallProgress{
		Index: protocoltypes.ReasoningProgressIndex, TotalArgsBytes: 300, ReasoningBytes: 512,
	})

	snap := ts.ToolCallProgress()
	if snap.Name != "" || snap.ArgsBytes != 0 {
		t.Fatalf("after a reasoning delta the snapshot still names the earlier tool call: %+v", snap)
	}
	if snap.ReasoningBytes != 512 || snap.TotalArgsBytes != 300 {
		t.Fatalf("reasoning event fields did not round-trip: %+v", snap)
	}
}
