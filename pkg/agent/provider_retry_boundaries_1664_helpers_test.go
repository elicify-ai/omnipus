package agent

// Round 5 helpers keep the response stage, fallback chain and event tap real.
// Only the outgoing provider is scripted, as in the existing #1664 harness.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func issue1664R5Response(t *testing.T, provider providers.LLMProvider, withChain bool) (*wu22Harness, *agentLoopRunTurnResponse) {
	t.Helper()
	h := wu22NewHarness(t, provider)
	h.agent.Model = "m"
	h.agent.Candidates = nil
	h.al.fallback = nil
	if withChain {
		h.agent.Candidates = []providers.FallbackCandidate{{Provider: "p", Model: "m"}}
		h.agent.StoreProviderPool(map[string]providers.LLMProvider{"p": provider})
		h.al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker()).
			WithRetryWait(func(ctx context.Context, _ time.Duration) error { return ctx.Err() })
	}
	rr := h.responseStage()
	rt := rr.rq.ri.rf.rt
	rt.activeCandidates, rt.activeProvider, rt.llmModel = h.agent.Candidates, provider, "m"
	rr.rq.ri.rf.callMessages = []providers.Message{{Role: "user", Content: "hello"}}
	rr.rq.ri.rf.providerToolDefs = nil
	return h, rr
}

// A connection-drop spelling wrapping DeadlineExceeded reaches the real
// streaming-reset branch after a two-candidate exhaustion. A plain transport
// error is instead retried in place by #1280 and cannot test this boundary.
var errIssue1664R5Reset = fmt.Errorf("streaming read error: http2: response body closed: %w", context.DeadlineExceeded)

func issue1664R5ResetResponse(t *testing.T, p, q providers.LLMProvider) (*wu22Harness, *agentLoopRunTurnResponse) {
	t.Helper()
	h, rr := issue1664R5Response(t, p, true)
	candidates := []providers.FallbackCandidate{{Provider: "p", Model: "m"}, {Provider: "q", Model: "m2"}}
	h.agent.Candidates = candidates
	h.agent.StoreProviderPool(map[string]providers.LLMProvider{"p": p, "q": q})
	rr.rq.ri.rf.rt.activeCandidates = candidates
	return h, rr
}

func issue1664R5EventCount(h *wu22Harness, kind EventKind) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, event := range h.events {
		if event.Kind == kind {
			n++
		}
	}
	return n
}

func issue1664R5TimeoutProvider() *issue1478Provider {
	return &issue1478Provider{script: func(context.Context, int) (*providers.LLMResponse, error) {
		return nil, context.DeadlineExceeded
	}}
}

// The synchronous tap is an observation boundary, not a replacement retry
// implementation: it records every event, then delivers the real user-Stop
// cascade at the requested event. No scheduling race chooses the Stop's phase.
func issue1664R5StopAt(h *wu22Harness, at EventKind) *atomic.Int32 {
	var fired atomic.Int32
	h.al.SetEventSyncTap(func(event Event) {
		h.mu.Lock()
		h.events = append(h.events, event)
		h.mu.Unlock()
		if event.Kind == at && fired.CompareAndSwap(0, 1) {
			h.ts.claimCancel(true)
			h.ts.requestGracefulInterrupt("")
		}
	})
	return &fired
}
