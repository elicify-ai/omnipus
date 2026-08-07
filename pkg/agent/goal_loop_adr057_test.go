// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_loop_adr057_test.go (ADR-057 U26/W3d) pins the strict-transcript
// conversion of goal_loop.go's 2 AppendTranscript call sites
// (writeGoalVerdictTranscript, writeGoalSystemTranscript): each now calls
// UnifiedStore.AppendTranscriptStrict and increments the SAME package-level
// taskGoalTranscriptWriteFailures counter task_executor_adr057_test.go
// exercises — this unit's counter is scoped to the unit (task_executor.go +
// goal_loop.go), not to a single file, matching turn.go's (U3) and
// handoff.go's (U22) one-counter-per-unit precedent. See
// task_executor_adr057_test.go's doc comment for this file's shared
// red/green note (U5 already made the base primitive strict before this
// unit started; there is no lenient sibling left to contrast against).
package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestU26_WriteGoalSystemTranscript_NonexistentSession_CountsAndWarns
// exercises goal_loop.go's writeGoalSystemTranscript (the idle-expiry
// handover writer, goalIdleExpirySweep's call site) against a session id
// with no backing store entry.
func TestU26_WriteGoalSystemTranscript_NonexistentSession_CountsAndWarns(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}

	before := TaskGoalTranscriptWriteFailures()
	al.writeGoalSystemTranscript(store, u26NonexistentSessionID, "native-agent", "idle-expired handover")
	after := TaskGoalTranscriptWriteFailures()

	if after-before != 1 {
		t.Fatalf("taskGoalTranscriptWriteFailures delta = %d, want 1", after-before)
	}
	u26AssertNoSessionDir(t, store, u26NonexistentSessionID)
}

// TestU26_WriteGoalSystemTranscript_RealSession_PersistsAndDoesNotCount is
// the Rule-4 positive lower bound: a real session accepts the handover entry
// and the counter does not move.
func TestU26_WriteGoalSystemTranscript_RealSession_PersistsAndDoesNotCount(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	sessionID := u26FreshTaskSession(t, store, "native-agent")

	before := TaskGoalTranscriptWriteFailures()
	al.writeGoalSystemTranscript(store, sessionID, "native-agent", "idle-expired handover text")
	after := TaskGoalTranscriptWriteFailures()

	if after != before {
		t.Fatalf("taskGoalTranscriptWriteFailures moved on a real session (before=%d after=%d)", before, after)
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Content == "idle-expired handover text" && e.Type == session.EntryTypeSystem {
			found = true
		}
	}
	if !found {
		t.Fatalf("handover entry not found in real transcript on disk, got %d entries", len(entries))
	}
}

// TestU26_WriteGoalVerdictTranscript_NonexistentSession_CountsAndWarns
// exercises goal_loop.go's writeGoalVerdictTranscript against a session id
// with no backing store entry.
func TestU26_WriteGoalVerdictTranscript_NonexistentSession_CountsAndWarns(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	verdict := &task.JudgeVerdict{
		Round: 1, Met: false, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: false, Reason: "unmet"}},
	}

	before := TaskGoalTranscriptWriteFailures()
	al.writeGoalVerdictTranscript(store, u26NonexistentSessionID, verdict)
	after := TaskGoalTranscriptWriteFailures()

	if after-before != 1 {
		t.Fatalf("taskGoalTranscriptWriteFailures delta = %d, want 1", after-before)
	}
	u26AssertNoSessionDir(t, store, u26NonexistentSessionID)
}

// TestU26_WriteGoalVerdictTranscript_RealSession_PersistsAndDoesNotCount is
// the Rule-4 positive lower bound: a real session accepts the goal verdict
// entry and the counter does not move.
func TestU26_WriteGoalVerdictTranscript_RealSession_PersistsAndDoesNotCount(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	sessionID := u26FreshTaskSession(t, store, "native-agent")
	verdict := &task.JudgeVerdict{
		Round: 3, Met: true, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: true, Reason: "evidenced"}},
	}

	before := TaskGoalTranscriptWriteFailures()
	al.writeGoalVerdictTranscript(store, sessionID, verdict)
	after := TaskGoalTranscriptWriteFailures()

	if after != before {
		t.Fatalf("taskGoalTranscriptWriteFailures moved on a real session (before=%d after=%d)", before, after)
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Type == session.EntryTypeJudgeVerdict && e.AgentID == "judge" {
			found = true
		}
	}
	if !found {
		t.Fatalf("goal verdict entry not found in real transcript on disk, got %d entries", len(entries))
	}
}

// TestU26_GoalIdleExpirySweep_ListSessions_IsUnpaginatedZeroArgForm is a
// W16j documentation-as-test: it pins that goalIdleExpirySweep (this unit's
// only ListSessions call site alongside goal_triggers.go's
// goalQuietWindowSettle) still calls the store's zero-arg, unpaginated
// ListSessions() — the form ADR-057 U6 deliberately left unchanged
// (unified.go's SessionListPage doc comment: "ListSessions() itself keeps
// its existing zero-arg, 'return everything' signature so every caller
// outside this migration's own session-list pipeline ... keeps compiling
// unchanged"). This sweep must see every goal-bearing session in one pass
// (it is a periodic full-store scan, not a UI-facing paginated listing), so
// converting it to ListSessionsPage would be a regression, not an upgrade —
// this test exists so a future change that swaps in pagination here fails
// loudly instead of silently under-sweeping.
func TestU26_GoalIdleExpirySweep_ListSessions_IsUnpaginatedZeroArgForm(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	// Create more sessions than any plausible single UI page size, all
	// carrying an active goal, and confirm the idle sweep still considers
	// every one of them (proving it is not windowed).
	const n = 12
	for i := 0; i < n; i++ {
		meta, err := store.NewSession(session.SessionTypeChat, "", "native-agent")
		if err != nil {
			t.Fatalf("NewSession[%d]: %v", i, err)
		}
		goalCond := "condition"
		if err := store.SetMeta(meta.ID, session.MetaPatch{
			GoalCondition: &goalCond,
		}); err != nil {
			t.Fatalf("SetMeta[%d]: %v", i, err)
		}
	}
	sessions, err := store.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	goalBearing := 0
	for _, s := range sessions {
		if s.GoalCondition != "" {
			goalBearing++
		}
	}
	if goalBearing != n {
		t.Fatalf("ListSessions returned %d goal-bearing sessions, want all %d in one unpaginated call", goalBearing, n)
	}
}
