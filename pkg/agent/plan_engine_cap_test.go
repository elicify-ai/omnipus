// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_engine_cap_test.go is E6's oracle for GOAL-FR-049 (the "config half"
// of the active-loop cap): "Task-owned goals MUST NOT consume the 'goal'
// admission slot registered via pkg/agent/plan_engine.go::RegisterActiveCounter
// and bounded by config.DefaultGlobalActiveLoopCap (16). Chat goals MUST
// remain bounded by it unchanged."
//
// The exemption itself is enforced by WHICHEVER function is registered for
// "goal" — E11's gateway.go closure, per its own row's documented predicate:
// "the 'goal' counter returns the number of goal records with
// owner_kind == session and state == active, read from pkg/goal ... and
// task-owned goals are excluded by owner_kind" (R-22). plan_engine.go itself
// (PlanEngine.Admit / computeActiveLocked / RegisterActiveCounter) has and
// must have ZERO owner-kind awareness of its own — it trusts the registered
// counter's return value verbatim. This file proves that CONTRACT: it
// registers the real, documented predicate — pkg/goal's own
// ListActiveByOwnerKind(GoalOwnerKindSession), exercised against a real
// goal.Store with real fixtures, not a hand-waved fake — and shows the two
// scenarios the goal spec names (S-42, S-43) both hold as a direct
// consequence of that registration.
//
// If a future change to plan_engine.go's cap mechanism ever starts counting
// something other than what the registered ActiveCounterFunc reports (e.g.
// scanning goal records itself, or hardcoding an owner-kind assumption),
// this test goes red: TestTaskGoalsExemptFromActiveLoopCap/S-42 would see
// task-owned goals start consuming the slot.
package agent

import (
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newCapTestGoalStore builds a real *goal.Store rooted at a fresh temp
// directory — the same constructor pkg/gateway boot uses
// (goal.NewStore($OMNIPUS_HOME)), just pointed at a scratch dir.
func newCapTestGoalStore(t *testing.T) *goal.Store {
	t.Helper()
	return goal.NewStore(t.TempDir())
}

// capTestDoD is the minimal valid DoD list Goal.Validate requires
// (D11/D15's minItems: 1), built from plan_engine_test.go's own
// planProseCriterion helper (same package) so the Author field this
// package's validation requires is populated exactly the way the rest of
// this test file already does.
func capTestDoD() []task.AcceptanceCriterion {
	return []task.AcceptanceCriterion{planProseCriterion("done")}
}

// mustCreateCapTestGoal creates a goal record for ownerKind/ownerID and, when
// active is true, transitions it through the real Goal.Activate state
// machine (not a hand-set State field) so the fixture is exactly what a
// genuine activation produces — an ActiveSessionID and StartedAt included.
func mustCreateCapTestGoal(t *testing.T, s *goal.Store, ownerKind generated.GoalOwnerKind, ownerID string, active bool) *goal.Goal {
	t.Helper()
	now := time.Now()
	g, err := goal.New(ownerKind, ownerID, generated.ChatCompiled, "prompt for "+ownerID, "", nil, capTestDoD(), 20, now)
	if err != nil {
		t.Fatalf("goal.New(%s/%s): %v", ownerKind, ownerID, err)
	}
	if createErr := s.Create(g); createErr != nil {
		t.Fatalf("goal store Create(%s/%s): %v", ownerKind, ownerID, createErr)
	}
	if !active {
		return g
	}
	sessionID := "sess-" + ownerID
	updated, err := s.Update(g.GoalID, func(gg *goal.Goal) error {
		return gg.Activate(sessionID, time.Now())
	})
	if err != nil {
		t.Fatalf("goal store Update/Activate(%s/%s): %v", ownerKind, ownerID, err)
	}
	return updated
}

// registerRealGoalCounter registers "goal" against pe using the SAME
// predicate E11's gateway.go closure is documented to implement (R-22):
// count of active, session-owned goal records.
func registerRealGoalCounter(pe *PlanEngine, s *goal.Store) {
	pe.RegisterActiveCounter("goal", func() (int, error) {
		active, err := s.ListActiveByOwnerKind(generated.GoalOwnerKindSession)
		if err != nil {
			return 0, err
		}
		return len(active), nil
	})
}

// TestTaskGoalsExemptFromActiveLoopCap is the goal spec's row-49 oracle
// (`TestTaskGoalsExemptFromActiveLoopCap`, FR-049, S-42, S-43).
func TestTaskGoalsExemptFromActiveLoopCap(t *testing.T) {
	// S-42 (HP) — "Given more concurrently running tasks than the goal
	// admission cap, when the operator sets a chat goal, then it is
	// admitted." Task-owned goals must never inflate the counted total.
	t.Run("S-42 task goals do not starve chat goals", func(t *testing.T) {
		h := newTestPlanEngine(t)
		s := newCapTestGoalStore(t)
		registerRealGoalCounter(h.pe, s)

		// Deliberately MORE active task-owned goals than
		// config.DefaultGlobalActiveLoopCap (16) — if these consumed the
		// slot the cap would already be blown before any chat goal exists.
		const taskGoalCount = 20
		if taskGoalCount <= config.DefaultGlobalActiveLoopCap {
			t.Fatalf("test setup: taskGoalCount=%d must exceed DefaultGlobalActiveLoopCap=%d to be a meaningful S-42 fixture",
				taskGoalCount, config.DefaultGlobalActiveLoopCap)
		}
		for i := 0; i < taskGoalCount; i++ {
			mustCreateCapTestGoal(t, s, generated.GoalOwnerKindTask, taskOwnerID(i), true /* active */)
		}

		// No chat goal exists yet — the operator is about to set the FIRST
		// one. It must be admitted despite 20 task-owned goals being active.
		ok, active, capOut := h.pe.Admit("goal")
		if !ok {
			t.Fatalf("Admit(\"goal\") = false with %d task-owned active goals and 0 chat-owned ones; want true (task-owned goals are exempt, GOAL-FR-049)", taskGoalCount)
		}
		if active != 0 {
			t.Fatalf("Admit(\"goal\") active count = %d; want 0 — task-owned goals must not be counted at all (only owner_kind=session, state=active is counted)", active)
		}
		if capOut != config.DefaultGlobalActiveLoopCap {
			t.Fatalf("Admit(\"goal\") cap = %d; want the unmodified default %d", capOut, config.DefaultGlobalActiveLoopCap)
		}
	})

	// S-43 (AP) — "Given the goal admission cap reached by chat goals alone,
	// when another chat goal is set, then it is bounded exactly as before."
	t.Run("S-43 chat goals remain capped", func(t *testing.T) {
		h := newTestPlanEngine(t)
		s := newCapTestGoalStore(t)
		registerRealGoalCounter(h.pe, s)

		// Reach the cap with chat (session-owned) goals ALONE — plus a few
		// task-owned goals thrown in to prove, again, that they contribute
		// nothing to the count even when mixed in with the chat ones.
		for i := 0; i < config.DefaultGlobalActiveLoopCap; i++ {
			mustCreateCapTestGoal(t, s, generated.GoalOwnerKindSession, "chat-owner-"+taskOwnerID(i), true)
		}
		for i := 0; i < 5; i++ {
			mustCreateCapTestGoal(t, s, generated.GoalOwnerKindTask, "extra-task-"+taskOwnerID(i), true)
		}

		ok, active, capOut := h.pe.Admit("goal")
		if ok {
			t.Fatalf("Admit(\"goal\") = true with %d chat-owned active goals already at the cap; want false (unchanged cap behaviour, GOAL-FR-049)", config.DefaultGlobalActiveLoopCap)
		}
		if active != config.DefaultGlobalActiveLoopCap {
			t.Fatalf("Admit(\"goal\") active count = %d; want exactly the chat-owned count %d (the 5 extra task-owned goals must not appear in this number)",
				active, config.DefaultGlobalActiveLoopCap)
		}
		if capOut != config.DefaultGlobalActiveLoopCap {
			t.Fatalf("Admit(\"goal\") cap = %d; want the unmodified default %d", capOut, config.DefaultGlobalActiveLoopCap)
		}

		// One fewer chat goal must free exactly one admission — proving the
		// boundary is real, not a permanently-stuck false.
		firstActive, err := s.ListActiveByOwnerKind(generated.GoalOwnerKindSession)
		if err != nil {
			t.Fatalf("ListActiveByOwnerKind: %v", err)
		}
		if len(firstActive) == 0 {
			t.Fatal("test setup: expected at least one active session-owned goal to terminate")
		}
		if _, err := s.Update(firstActive[0].GoalID, func(gg *goal.Goal) error {
			return gg.Terminate(generated.GoalStateCleared, "test cleanup", time.Now())
		}); err != nil {
			t.Fatalf("Terminate one chat goal to free a slot: %v", err)
		}
		ok2, active2, _ := h.pe.Admit("goal")
		if !ok2 {
			t.Fatalf("Admit(\"goal\") = false after freeing one of %d chat-owned slots; want true", config.DefaultGlobalActiveLoopCap)
		}
		if active2 != config.DefaultGlobalActiveLoopCap-1 {
			t.Fatalf("Admit(\"goal\") active count after freeing one slot = %d; want %d", active2, config.DefaultGlobalActiveLoopCap-1)
		}
	})
}

// taskOwnerID renders a short, distinct owner id per fixture index.
func taskOwnerID(i int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if i < len(digits) {
		return string(digits[i])
	}
	return string(rune('a'+i%26)) + string(rune('0'+i/26))
}
