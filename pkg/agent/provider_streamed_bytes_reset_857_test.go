// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// provider_streamed_bytes_reset_857_test.go: issue #857 round-2 review —
// deleting callProvider removed the per-call reset of providerCallStreamedBytes.
// The §7.4 chain consults that counter (C-10) to refuse an in-place retry once
// bytes of THIS attempt were streamed. A value left over from an earlier
// provider round must never suppress the retry of a later, unrelated 429 —
// including on the multi-candidate path, whose closure does not reset it.

package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// rateLimitOnceProvider fails the FIRST call per model with an HTTP 429
// (Retry-After 1s) and answers every later call.
type rateLimitOnceProvider struct {
	mu    sync.Mutex
	calls map[string]int
}

func (p *rateLimitOnceProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[model]++
	if p.calls[model] == 1 {
		return nil, &common.ProviderError{Status: 429, Body: "rate limited", RetryAfterSeconds: 1}
	}
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (p *rateLimitOnceProvider) GetDefaultModel() string { return "unused" }

func (p *rateLimitOnceProvider) count(model string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[model]
}

func TestCallProviderOnce_MultiCandidate_StaleStreamedBytesDoNotSuppressRateLimitRetry(t *testing.T) {
	provider := &rateLimitOnceProvider{calls: map[string]int{}}
	agent := &AgentInstance{ID: "mia", Provider: provider}
	agent.StoreProviderPool(map[string]providers.LLMProvider{"302ai": provider, "zai": provider})
	al := &AgentLoop{fallback: providers.NewFallbackChain(providers.NewCooldownTracker())}
	rt := &agentLoopRunTurn{
		al: al, ts: newTurnState(agent, processOptions{}, turnEventScope{}), turnCtx: context.Background(), activeProvider: provider,
		activeCandidates: []providers.FallbackCandidate{{Provider: "302ai", Model: "primary-model"}, {Provider: "zai", Model: "fallback-model"}},
		llmModel:         "primary-model",
	}
	// Left over from an earlier provider round of the same turn.
	rt.providerCallStreamedBytes.Store(5)

	if _, err := rt.callProviderOnce([]providers.Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("callProviderOnce: %v", err)
	}
	if got := provider.count("primary-model"); got != 2 {
		t.Fatalf("primary candidate calls = %d, want 2 (one 429 + one in-place retry) — a stale streamed-bytes counter must not disable the retry", got)
	}
	if got := provider.count("fallback-model"); got != 0 {
		t.Fatalf("fallback candidate was called %d times, want 0 — the primary's in-place retry should have answered", got)
	}
}
