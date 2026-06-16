// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package boardtask_test

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/boardtask"
)

// withStatus returns a copy of task t with the given status applied.
func withStatus(t boardtask.Task, s boardtask.Status) boardtask.Task {
	t.Status = s
	return t
}

// TestAdvanceBlockedDependents_SingleDepSatisfied verifies that completing the
// only blocker of a waiting dependent advances it waiting→next (FR-6.5).
func TestAdvanceBlockedDependents_SingleDepSatisfied(t *testing.T) {
	// B is waiting, blocked_by [A]. A is done.
	a := withStatus(makeTask("A"), boardtask.StatusDone)
	b := withStatus(makeTask("B", "A"), boardtask.StatusWaiting)
	dir := writeTasks(t, []boardtask.Task{a, b})

	advanced, err := boardtask.AdvanceBlockedDependents(dir, "A")
	if err != nil {
		t.Fatalf("AdvanceBlockedDependents: %v", err)
	}
	if len(advanced) != 1 || advanced[0] != "B" {
		t.Fatalf("expected [B] advanced, got %v", advanced)
	}
	if got := readTask(t, dir, "B").Status; got != boardtask.StatusNext {
		t.Fatalf("expected B to be next, got %q", got)
	}
}

// TestAdvanceBlockedDependents_UnsatisfiedDepStaysWaiting verifies that a
// dependent with a still-incomplete second blocker is NOT advanced.
func TestAdvanceBlockedDependents_UnsatisfiedDepStaysWaiting(t *testing.T) {
	// C is waiting, blocked_by [A, X]. A is done but X is still inbox.
	a := withStatus(makeTask("A"), boardtask.StatusDone)
	x := withStatus(makeTask("X"), boardtask.StatusInbox)
	c := withStatus(makeTask("C", "A", "X"), boardtask.StatusWaiting)
	dir := writeTasks(t, []boardtask.Task{a, x, c})

	advanced, err := boardtask.AdvanceBlockedDependents(dir, "A")
	if err != nil {
		t.Fatalf("AdvanceBlockedDependents: %v", err)
	}
	if len(advanced) != 0 {
		t.Fatalf("expected no advance, got %v", advanced)
	}
	if got := readTask(t, dir, "C").Status; got != boardtask.StatusWaiting {
		t.Fatalf("expected C to stay waiting, got %q", got)
	}
}

// TestAdvanceBlockedDependents_NonWaitingDependentUntouched verifies idempotency
// and GTD-semantics: a dependent that is already past "waiting" (e.g. inbox or
// next) is never auto-moved.
func TestAdvanceBlockedDependents_NonWaitingDependentUntouched(t *testing.T) {
	a := withStatus(makeTask("A"), boardtask.StatusDone)
	// D depends on A but is in inbox (user hasn't gated it as waiting).
	d := withStatus(makeTask("D", "A"), boardtask.StatusInbox)
	dir := writeTasks(t, []boardtask.Task{a, d})

	advanced, err := boardtask.AdvanceBlockedDependents(dir, "A")
	if err != nil {
		t.Fatalf("AdvanceBlockedDependents: %v", err)
	}
	if len(advanced) != 0 {
		t.Fatalf("expected no advance for non-waiting dependent, got %v", advanced)
	}
	if got := readTask(t, dir, "D").Status; got != boardtask.StatusInbox {
		t.Fatalf("expected D to stay inbox, got %q", got)
	}
}

// TestAdvanceBlockedDependents_Idempotent verifies re-running the advance on the
// same completed id is a no-op once the dependent has already been advanced.
func TestAdvanceBlockedDependents_Idempotent(t *testing.T) {
	a := withStatus(makeTask("A"), boardtask.StatusDone)
	b := withStatus(makeTask("B", "A"), boardtask.StatusWaiting)
	dir := writeTasks(t, []boardtask.Task{a, b})

	if _, err := boardtask.AdvanceBlockedDependents(dir, "A"); err != nil {
		t.Fatalf("first advance: %v", err)
	}
	// Second run: B is now "next", not "waiting", so it must not advance again.
	advanced, err := boardtask.AdvanceBlockedDependents(dir, "A")
	if err != nil {
		t.Fatalf("second advance: %v", err)
	}
	if len(advanced) != 0 {
		t.Fatalf("expected idempotent no-op on second run, got %v", advanced)
	}
}

// TestAdvanceBlockedDependents_MultiDepAllDone verifies a dependent gated on two
// blockers advances only once BOTH are done.
func TestAdvanceBlockedDependents_MultiDepAllDone(t *testing.T) {
	a := withStatus(makeTask("A"), boardtask.StatusDone)
	bDone := withStatus(makeTask("B"), boardtask.StatusDone)
	c := withStatus(makeTask("C", "A", "B"), boardtask.StatusWaiting)
	dir := writeTasks(t, []boardtask.Task{a, bDone, c})

	// Completing the second blocker (B) should now un-gate C.
	advanced, err := boardtask.AdvanceBlockedDependents(dir, "B")
	if err != nil {
		t.Fatalf("AdvanceBlockedDependents: %v", err)
	}
	if len(advanced) != 1 || advanced[0] != "C" {
		t.Fatalf("expected [C] advanced, got %v", advanced)
	}
	if got := readTask(t, dir, "C").Status; got != boardtask.StatusNext {
		t.Fatalf("expected C to be next, got %q", got)
	}
}

// TestAdvanceBlockedDependents_NoDependents verifies a clean no-op when nothing
// depends on the completed task.
func TestAdvanceBlockedDependents_NoDependents(t *testing.T) {
	a := withStatus(makeTask("A"), boardtask.StatusDone)
	b := withStatus(makeTask("B"), boardtask.StatusInbox)
	dir := writeTasks(t, []boardtask.Task{a, b})

	advanced, err := boardtask.AdvanceBlockedDependents(dir, "A")
	if err != nil {
		t.Fatalf("AdvanceBlockedDependents: %v", err)
	}
	if len(advanced) != 0 {
		t.Fatalf("expected no advance, got %v", advanced)
	}
}

// TestAdvanceBlockedDependents_MissingDir verifies a missing tasks dir is a
// graceful no-op (not an error).
func TestAdvanceBlockedDependents_MissingDir(t *testing.T) {
	advanced, err := boardtask.AdvanceBlockedDependents(t.TempDir()+"/does-not-exist", "A")
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if len(advanced) != 0 {
		t.Fatalf("expected no advance, got %v", advanced)
	}
}
