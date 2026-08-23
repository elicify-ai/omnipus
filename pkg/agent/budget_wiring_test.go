// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// budget_wiring_test.go — ADR-053 D12 / #540 end-to-end regression: proves the
// ONE app-level token-budget debit that lives at loop.go's runTurn (the
// al.tokenBudget.Debit(...) call, guarded by NOTHING agent-type-related) is
// actually reached by a REAL turn for both the owner/core scope and the
// verifier/Judge scope, not merely by unit tests that hand-construct a
// TokenBudget and call Debit()/Exhausted() directly (loop_wave4_test.go,
// goal_compile_test.go) or that pre-exhaust the pool before asserting a
// boundary gate (goal_triggers_test.go, plan_engine_test.go).
//
// Together with those existing tests this closes G-14 across all four D12
// scopes (owner, member, verifier, Judge): member/plan-task turns dispatch
// through the exact same al.processTaskDirect -> runAgentLoop -> runTurn path
// exercised here by TestRunTurn_DebitsTokenBudget_CoreAgentNoExemption
// (task_executor.go's dispatchReadyMembers calls processTaskDirect directly,
// see task_executor.go:468/1896), so a separate member-scope copy of this test
// would exercise byte-identical wiring.
//
// These two tests are also the mutation-test anchor for #540: commenting out
// (or re-gating with a security.IsPrivilegedAgent check) the
// al.tokenBudget.Debit call in loop.go's runTurn makes both fail closed
// (Consumed stays 0) instead of silently passing.

// usageStubProvider is a providers.LLMProvider double that returns a fixed
// Content + Usage with no tool calls, so runTurn completes in one round trip
// without needing any tool policy wired up.
type usageStubProvider struct {
	totalTokens int
}

func (p *usageStubProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:   "ok",
		ToolCalls: []providers.ToolCall{},
		Usage: &providers.UsageInfo{
			PromptTokens:     p.totalTokens / 2,
			CompletionTokens: p.totalTokens - p.totalTokens/2,
			TotalTokens:      p.totalTokens,
		},
	}, nil
}

func (p *usageStubProvider) GetDefaultModel() string { return "usage-stub-model" }

// TestRunTurn_DebitsTokenBudget_CoreAgentNoExemption is the end-to-end
// regression for the central #540 claim: a REAL turn for a "core"-type agent
// (the type the retired SEC-26 USD cap exempted via IsPrivilegedAgent) must
// debit the ONE shared token pool exactly like any other agent, because
// runTurn's debit call takes no agentType parameter at all (ADR-053 D12,
// FR-172).
func TestRunTurn_DebitsTokenBudget_CoreAgentNoExemption(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{ID: "core-agent", Name: "Core", Type: config.AgentTypeCore, Home: t.TempDir()},
			},
		},
	}
	provider := &usageStubProvider{totalTokens: 777}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(func() { al.Close() })

	tb := al.TokenBudget()
	if tb == nil {
		t.Fatal("TokenBudget() must not be nil after NewAgentLoop boot wiring (D12/R§8.3)")
	}
	if got := tb.Consumed(); got != 0 {
		t.Fatalf("setup: Consumed() = %d before any turn, want 0", got)
	}

	if _, err := al.processTaskDirect(context.Background(), "core-agent", "hello", "sess-core", "chat-core"); err != nil {
		t.Fatalf("processTaskDirect: %v", err)
	}

	if got := tb.Consumed(); got != 777 {
		t.Errorf("Consumed() after one core-agent turn = %d, want 777 — "+
			"a core-agent turn must debit the shared pool with NO "+
			"IsPrivilegedAgent exemption (ADR-053 D12, #540)", got)
	}
}

// TestJudgeCriteria_DebitsTokenBudget_VerifierScope is the end-to-end
// regression proving the Judge's own out-of-turn adjudication call — which
// dispatches through the exact same al.processTaskDirect (verifier_adjudication.go:981)
// as any other agent turn — also debits the ONE shared token pool. This is
// the "verifier/Judge" leg of G-14 that the pre-exhausted-pool boundary-gate
// tests (goal_triggers_test.go's TestIdleSettle_BudgetExhausted_Brakes_corrMAJOR1,
// plan_engine_test.go's TestPlanEngine_BudgetExhausted_FailsPlan_M1) do not
// cover, since both hand-construct an already-exhausted TokenBudget rather
// than observing a real debit from a genuine Judge LLM call.
func TestJudgeCriteria_DebitsTokenBudget_VerifierScope(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	tb := al.TokenBudget()
	if tb == nil {
		t.Fatal("TokenBudget() must not be nil after NewAgentLoop boot wiring (D12/R§8.3)")
	}
	if got := tb.Consumed(); got != 0 {
		t.Fatalf("setup: Consumed() = %d before any Judge call, want 0", got)
	}

	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
			Usage:   &providers.UsageInfo{PromptTokens: 200, CompletionTokens: 121, TotalTokens: 321},
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-budget-wiring",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if result.Unavailable || !result.Verdict.Met {
		t.Fatalf("unexpected result: %+v", result)
	}

	if got := tb.Consumed(); got != 321 {
		t.Errorf("Consumed() after one Judge adjudication call = %d, want 321 — "+
			"the Judge's own LLM call must debit the shared pool exactly like "+
			"any other agent turn (ADR-053 D12, #540 verifier scope)", got)
	}
}
