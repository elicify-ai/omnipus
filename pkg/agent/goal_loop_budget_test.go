// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_loop_budget_test.go closes a G-14 coverage gap in the ADR-053 D12
// token-budget suite: TestIdleSettle_BudgetExhausted_Brakes_corrMAJOR1
// (goal_triggers_test.go) proves the IDLE adjudication path brakes on
// TokenBudget().Exhausted(), and its own doc comment notes "the CLAIM path
// already did" (goal_loop.go's checkGoalLoopAfterTurn, ~line 574) — but no
// test exercised that CLAIM-path boundary check directly. This test does,
// mirroring the idle test's assertions (Judge not invoked, goal cleared with
// the budget-exhausted brake) against the claim path instead.

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestGoalLoop_ClaimPath_BudgetExhausted_Brakes proves the CLAIM adjudication
// path (checkGoalLoopAfterTurn, goal_loop.go) consults TokenBudget().Exhausted()
// BEFORE dispatching the Judge — mirroring the idle path's brake
// (corr-MAJOR-1) and closing out issue #540 / ADR-053 D12 / G-14 for the
// claim trigger specifically. When exhausted: no Judge round fires (no
// double-spend past the cap) and the goal is cleared with the honest
// budget_exhausted terminal instead of silently over-spending.
func TestGoalLoop_ClaimPath_BudgetExhausted_Brakes(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	emptyCriteria := ""
	if err := store.SetMeta(sid, session.MetaPatch{GoalCriteriaJSON: &emptyCriteria}); err != nil {
		t.Fatal(err)
	}

	// Exhaust the overall token budget (cap 100, debited 100 -> consumed >= cap).
	al.tokenBudget = NewTokenBudget(100, nil)
	al.tokenBudget.Debit(100)
	if !al.TokenBudget().Exhausted() {
		t.Fatal("setup invariant: budget must be exhausted")
	}

	cp := unmetJudgeProvider("should never be reached")
	judgeInst.Provider = cp

	// A real completion claim ([goal:evidence] + GOAL_STATUS: met) — the exact
	// shape TestGoalLoop_UnmetVerdict_AdvancesRoundAndFeedsForward uses when the
	// budget is NOT exhausted (where it DOES reach the Judge).
	result := &turnResult{finalContent: "[goal:evidence] all green\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	if cp.callCount() != 0 {
		t.Fatalf("claim path with exhausted budget invoked Judge %d times, want 0 "+
			"(the claim path must brake on TokenBudget().Exhausted() before dispatching, "+
			"same as the idle path — corr-MAJOR-1 / #540)", cp.callCount())
	}
	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalCondition != "" {
		t.Fatalf("goal must be cleared (budget_exhausted) on the claim-path brake, still: %q", after.GoalCondition)
	}
}
