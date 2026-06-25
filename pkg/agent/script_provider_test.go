// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// script_provider_test.go — generic ScriptResponses provider harness (§8/§9.8).
//
// newScriptedProvider returns a providers.LLMProvider that replays a fixed
// sequence of *providers.LLMResponse values, one per Chat call.  Tests that
// need to drive a multi-turn tool-call sequence without rolling a bespoke mock
// struct can use this helper instead of inline closures.
//
// The design is intentionally distinct from the pkg/agent/testutil.ScenarioProvider
// (which is a full builder with text/tool-call/error DSL and is gated by the
// gateway harness).  newScriptedProvider is a lightweight alternative for
// pkg/agent-internal tests that only need a raw response sequence.
//
// Self-test: TestScriptedProvider_ReturnsResponsesInOrder below.
//
// Traces to: Plan §8 — "Generic ScriptResponses provider"
//            (docs/internal/plan referenced from issue #440)

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// errNoMoreScriptedResponses is returned when Chat is called after all scripted
// responses have been consumed.  Tests that want to verify exhaustion behaviour
// can check for this sentinel.
var errNoMoreScriptedResponses = errors.New("sequenceProvider: no more responses")

// sequenceProvider is a providers.LLMProvider that returns a pre-scripted
// sequence of *providers.LLMResponse values (one per Chat call).
//
// Named "sequence" to avoid collision with the local scriptedProvider type in
// session_end_behavioral_test.go (same package, different file).
type sequenceProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	idx       int
	// callCount is the total number of Chat calls received (even exhausted ones).
	callCount int
}

// newScriptedProvider returns a sequenceProvider that replays the given
// responses in order.  When all responses have been consumed, subsequent calls
// return errNoMoreScriptedResponses.
//
// Usage:
//
//	p := newScriptedProvider(
//	    &providers.LLMResponse{Content: "step 1"},
//	    &providers.LLMResponse{ToolCalls: []providers.ToolCall{{Name: "bash", ...}}},
//	    &providers.LLMResponse{Content: "done"},
//	)
func newScriptedProvider(responses ...*providers.LLMResponse) *sequenceProvider {
	return &sequenceProvider{responses: responses}
}

// Chat implements providers.LLMProvider.
func (p *sequenceProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	// Respect context cancellation.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	if p.idx >= len(p.responses) {
		return nil, errNoMoreScriptedResponses
	}
	resp := p.responses[p.idx]
	p.idx++
	return resp, nil
}

// GetDefaultModel implements providers.LLMProvider.
func (p *sequenceProvider) GetDefaultModel() string { return "scripted-test-model" }

// CallCount returns how many times Chat has been invoked (thread-safe).
func (p *sequenceProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

// Remaining returns how many scripted responses have not yet been consumed.
func (p *sequenceProvider) Remaining() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.responses) - p.idx
}

// ---- self-test ----

// TestScriptedProvider_ReturnsResponsesInOrder verifies:
//  1. Chat returns each scripted response in the declared order (not hardcoded).
//  2. Two different providers return different content (differentiation test).
//  3. Exhausted provider returns errNoMoreScriptedResponses.
//  4. callCount and Remaining track correctly.
//
// Traces to: Plan §8 — "Generic ScriptResponses provider"
func TestScriptedProvider_ReturnsResponsesInOrder(t *testing.T) {
	resp1 := &providers.LLMResponse{Content: "first response"}
	resp2 := &providers.LLMResponse{Content: "second response"}
	resp3 := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{
			{ID: "call-a", Name: "mock_tool"},
		},
	}

	p := newScriptedProvider(resp1, resp2, resp3)
	ctx := context.Background()

	if p.Remaining() != 3 {
		t.Fatalf("Remaining() = %d, want 3", p.Remaining())
	}

	// --- call 1 ---
	got1, err := p.Chat(ctx, nil, nil, "", nil)
	if err != nil {
		t.Fatalf("Chat[1]: unexpected error: %v", err)
	}
	if got1.Content != "first response" {
		t.Errorf("Chat[1].Content = %q, want %q", got1.Content, "first response")
	}
	if p.Remaining() != 2 {
		t.Errorf("Remaining after 1 call = %d, want 2", p.Remaining())
	}
	if p.CallCount() != 1 {
		t.Errorf("CallCount after 1 call = %d, want 1", p.CallCount())
	}

	// --- call 2 ---
	got2, err := p.Chat(ctx, nil, nil, "", nil)
	if err != nil {
		t.Fatalf("Chat[2]: unexpected error: %v", err)
	}
	if got2.Content != "second response" {
		t.Errorf("Chat[2].Content = %q, want %q", got2.Content, "second response")
	}
	// Differentiation: responses must differ
	if got1.Content == got2.Content {
		t.Errorf("differentiation FAIL: Chat[1] and Chat[2] returned same content %q", got1.Content)
	}

	// --- call 3 (tool call response) ---
	got3, err := p.Chat(ctx, nil, nil, "", nil)
	if err != nil {
		t.Fatalf("Chat[3]: unexpected error: %v", err)
	}
	if len(got3.ToolCalls) != 1 {
		t.Errorf("Chat[3]: expected 1 tool call, got %d", len(got3.ToolCalls))
	} else if got3.ToolCalls[0].Name != "mock_tool" {
		t.Errorf("Chat[3].ToolCalls[0].Name = %q, want %q", got3.ToolCalls[0].Name, "mock_tool")
	}

	// --- call 4: exhausted ---
	_, err = p.Chat(ctx, nil, nil, "", nil)
	if !errors.Is(err, errNoMoreScriptedResponses) {
		t.Errorf("Chat[4] exhausted: got err %v, want errNoMoreScriptedResponses", err)
	}
	if p.CallCount() != 4 {
		t.Errorf("CallCount after 4 calls = %d, want 4", p.CallCount())
	}
	if p.Remaining() != 0 {
		t.Errorf("Remaining after exhaustion = %d, want 0", p.Remaining())
	}
}

// TestScriptedProvider_ContextCancellation verifies that a cancelled context is
// respected and returns ctx.Err() without consuming the next response.
func TestScriptedProvider_ContextCancellation(t *testing.T) {
	p := newScriptedProvider(&providers.LLMResponse{Content: "unreachable"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := p.Chat(ctx, nil, nil, "", nil)
	if err == nil {
		t.Fatal("expected an error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	// The unreachable response must still be available.
	if p.Remaining() != 1 {
		t.Errorf("Remaining after cancelled call = %d, want 1 (response was not consumed)", p.Remaining())
	}
}

// TestScriptedProvider_DifferentiationVsTwoInstances verifies that two separate
// scriptedProvider instances with different responses return different content.
// This is the classic "not hardcoded" guard for the helper itself.
func TestScriptedProvider_DifferentiationVsTwoInstances(t *testing.T) {
	pA := newScriptedProvider(&providers.LLMResponse{Content: "alpha"})
	pB := newScriptedProvider(&providers.LLMResponse{Content: "beta"})
	ctx := context.Background()

	gotA, err := pA.Chat(ctx, nil, nil, "", nil)
	if err != nil {
		t.Fatalf("pA.Chat: %v", err)
	}
	gotB, err := pB.Chat(ctx, nil, nil, "", nil)
	if err != nil {
		t.Fatalf("pB.Chat: %v", err)
	}
	if gotA.Content == gotB.Content {
		t.Errorf("differentiation FAIL: both providers returned %q", gotA.Content)
	}
	if gotA.Content != "alpha" {
		t.Errorf("pA: want %q, got %q", "alpha", gotA.Content)
	}
	if gotB.Content != "beta" {
		t.Errorf("pB: want %q, got %q", "beta", gotB.Content)
	}
}
