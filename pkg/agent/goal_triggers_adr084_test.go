// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_triggers_adr084_test.go carries ADR-084 revision 9 D13/JUDGE-FR-095's
// two required oracles (wave E13, joint delivery plan §3): the claimless
// idle-adjudication path is retired — a `met` claim (goal_claim tool or the
// prose GOAL_STATUS marker) is the sole trigger for the Judge from here on,
// and runGoalAdjudication itself refuses an empty claimText rather than
// silently adjudicating against nothing. Both tests fail against the
// pre-D13 shape (a quiet-but-non-empty goal used to adjudicate at idle; an
// empty claimText used to be the documented claimless-idle contract).
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestQuietWindow_MakesZeroJudgeCalls proves JUDGE-FR-095: a quiet
// goal-bearing session with a record and PRIOR output — exactly the shape
// that used to make the old (retired) FR-014b zero-output triple evaluate
// false and adjudicate — produces ZERO Judge dispatches at idle, no matter
// how much real output exists, because idle settlement no longer consults
// output at all; it only ever re-posts (FR-097).
func TestQuietWindow_MakesZeroJudgeCalls(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRecordArmed(t, store, sid, "goal with real prior output", 0, time.Now().Add(-1*time.Hour))
	if err := store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "real-work", Type: session.EntryTypeToolCall,
		ToolCalls: []session.ToolCall{{ID: "tc1", Tool: "bash", Status: "success"}},
		Timestamp: time.Now().Add(-30 * time.Minute), AgentID: agentInst.ID,
	}); err != nil {
		t.Fatal(err)
	}

	cp := unmetJudgeProvider("must never be called — JUDGE-FR-095 retires claimless idle adjudication")
	judgeInst.Provider = cp

	before := goalRecordForSession(t, sid)
	al.goalQuietWindowSettle(time.Now())
	after := goalRecordForSession(t, sid)
	if cp.callCount() != 0 {
		t.Fatalf("JUDGE-FR-095: idle settlement Judge calls = %d, want 0", cp.callCount())
	}
	if after.Round != before.Round {
		t.Fatalf("JUDGE-FR-095: goal record Round changed (%d -> %d) — idle settlement must never consume a round",
			before.Round, after.Round)
	}
}

// TestRunGoalAdjudication_RejectsEmptyClaimText proves JUDGE-FR-095's second
// half: the claimless contract is gone — runGoalAdjudication itself now
// refuses an empty claimText outright (no Judge call, no round consumed, no
// steer delivered) rather than silently adjudicating against nothing, so a
// claimless call site reintroduced by a future merge fails loudly instead of
// quietly resuming the retired behaviour.
func TestRunGoalAdjudication_RejectsEmptyClaimText(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setGoalRecordArmed(t, store, sid, "goal empty claim", 0, time.Now().Add(-1*time.Hour))
	meta := goalRecordForSession(t, sid)

	cp := unmetJudgeProvider("must never be called — an empty claimText must be refused before dispatch")
	judgeInst.Provider = cp

	steered := false
	met := al.runGoalAdjudication(context.Background(), agentInst, "", sid, store, meta, "",
		func(string) { steered = true })
	if met {
		t.Fatal("an empty claimText must never report met")
	}
	if steered {
		t.Fatal("an empty claimText must never deliver a steer")
	}
	if cp.callCount() != 0 {
		t.Fatalf("an empty claimText must never invoke the Judge; got %d calls", cp.callCount())
	}
	after := goalRecordForSession(t, sid)
	if after.Round != 0 {
		t.Fatalf("an empty claimText must never consume a round; rounds_used = %d, want 0", after.Round)
	}
}
