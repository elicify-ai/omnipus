// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// budget_callsite_test.go closes a gap the existing D12 regression suite
// left open: TestTokenBudget_SoleBrake_Core (loop_wave4_test.go) proves the
// TokenBudget TYPE has no core-agent exemption — but TokenBudget.Debit takes
// no agentType parameter at all, so that test can never observe a regression
// introduced at the ACTUAL call site (loop.go's runTurn, around the
// "TokenBudget is the sole app-level spend brake" comment) if a future edit
// wrapped that call in an `if !security.IsPrivilegedAgent(...)` guard. This
// test drives a REAL turn for a "core"-type agent through the same
// processTaskDirect -> runAgentLoop -> runTurn path production uses, and
// asserts the shared pool was actually debited — issue #540 / ADR-053 D12.

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestTokenBudget_DebitedAtRealCallSite_NoCoreExemption(t *testing.T) {
	const wantTokens = 400

	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: "done",
			Usage: &providers.UsageInfo{
				PromptTokens:     300,
				CompletionTokens: 100,
				TotalTokens:      wantTokens,
			},
		}, nil
	}}

	al, _ := newGoalLoopTestLoop(t, fake, func(cfg *config.Config) {
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
			ID:   "core-agent-real-call",
			Name: "Core Agent",
			Type: config.AgentTypeCore,
			Home: t.TempDir(),
		})
	})
	// Override the loop's own budget field (not a standalone TokenBudget) so
	// this test exercises the exact field runTurn's debit line reads.
	al.tokenBudget = NewTokenBudget(1_000_000, nil)

	if _, err := al.processTaskDirect(
		context.Background(), "core-agent-real-call", "hello", "sess-core-real-call", "chat-core-real-call",
	); err != nil {
		t.Fatalf("processTaskDirect: %v", err)
	}

	if got := al.TokenBudget().Consumed(); got != wantTokens {
		t.Fatalf("core-agent real turn: TokenBudget().Consumed() = %d, want %d — "+
			"a core-agent exemption has crept back into the REAL debit call site "+
			"in loop.go (runTurn), even though TokenBudget.Debit itself takes no "+
			"agentType and cannot special-case anyone (ADR-053 D12, #540)", got, wantTokens)
	}
}
