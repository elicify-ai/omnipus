// Omnipus — askuserquestion-tool-spec v3 §0.7 (M-R2-5): a parked turn never
// advances the goal round (spec Test 3's no-goal-round-advance half).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestGoalLoop_ParkedTurnNeverAdvancesRound: TurnEndStatusParked is NOT a
// natural turn stop for checkGoalLoopAfterTurn — a parked clarification turn
// (AskUserQuestion, message_parent(question:true)) must not consume a round,
// invoke the Judge, or re-dispatch a follow-up, even when the parked turn's
// final content happens to carry a completion claim. The gate lives at the
// function's entry.
func TestGoalLoop_ParkedTurnNeverAdvancesRound(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	// ADR-074 D4a (wave-1 A6, merged AFTER this A5 test was written): a
	// prose `/goal` now compiles to a PENDING goal awaiting the user's
	// confirming reply — activate it explicitly so the scenario under test
	// (a parked turn on an ACTIVE goal) is actually established.
	activatePendingGoal(t, al, agentInst, &opts)

	// A judge that would return unmet IF it were (wrongly) invoked.
	judgeInst.Provider = unmetJudgeProvider("3 tests still failing")

	result := &turnResult{
		status:       TurnEndStatusParked,
		finalContent: "[goal:evidence] all tests green\nGOAL_STATUS: met",
	}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalCondition == "" {
		t.Fatal("the goal must remain active — a parked turn is not an adjudication boundary")
	}
	if after.GoalRoundsUsed != 0 {
		t.Fatalf("rounds_used = %d, want 0 — a parked turn must not consume a goal round", after.GoalRoundsUsed)
	}
	if len(result.followUps) != 0 {
		t.Fatalf("a parked turn must not schedule a follow-up round, got %d", len(result.followUps))
	}
	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Type == session.EntryTypeJudgeVerdict {
			t.Fatal("the Judge must never be invoked for a parked turn")
		}
	}
}
