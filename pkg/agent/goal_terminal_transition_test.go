// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_terminal_transition_test.go is wave E8's own regression suite for
// ADR-086 GOAL-FR-027/FR-028 (the terminal transition) and GOAL-FR-049's
// idle-expiry predicate half — the goal spec's own goal_terminal_test.go
// (docs/internal/specs/goal-entity-spec.md §12 W4, test rows 27/28),
// renamed per the joint delivery plan §3's E8 row ("this is the goal spec's
// goal_terminal_test.go; ONE file, this name").
//
// Every test here seeds a REAL pkg/goal.Store record (seedActiveGoalRecord,
// goal_record_wiring_test.go, wave E4 — same package, reused here rather
// than duplicated) ALONGSIDE the session-meta activation every existing
// goal_loop_test.go test already drives, then asserts on the RECORD itself
// after a terminal transition: this is the one thing no pre-existing test
// in this package checks, because before this wave nothing ever wrote one.
//
// Traces to: docs/internal/specs/goal-entity-spec.md S-13..S-18.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// terminalTestCriterion is this file's own copy of goal_record_wiring_test.go's
// seedActiveGoalRecord helper's implicit criterion shape — kept local so
// this file does not depend on unexported details of a sibling test file
// beyond the seedActiveGoalRecord call itself.
func terminalTestCriterion(text string) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		// ADR-086: the id is recordedGoalCriterionID (goal_loop_test.go) so
		// the canned met/unmet judge providers answer for it. Verdicts are
		// matched to criteria BY ID (verifier_adjudication.go), and this
		// file's criteria now reach the Judge from the RECORD rather than
		// through compiledGoalCriteriaFor's "goal-condition" back-compat
		// synthesis — an unanswered id would resolve criterion_unjudgeable
		// (met=false) and silently invert every met-verdict assertion here.
		ID:   recordedGoalCriterionID,
		Kind: task.KindProse, Judgment: task.JudgmentBoolean,
		Text: text, Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "daniel"},
	}
}

// seedCriteriaOntoActiveGoal writes criteria (plus the built-in floor DoD)
// onto the goal record the preceding `/goal` drive already activated for sid,
// and returns it.
//
// ADR-086: this replaces seedActiveGoalRecord for the tests in this file that
// ALSO drive a real `/goal <intent>` activation. Pre-S6 those two steps were
// independent — the `/goal` wrote session meta, the seed wrote the record —
// so doing both was the dual write this file's own doc comment describes as
// "ALONGSIDE". Now `/goal` IS the record creation, so a second
// seedActiveGoalRecord would leave the session carrying TWO active records:
// an upstream invariant violation that activeGoalForSession reports at Warn
// and resolves in favour of the OLDER one — i.e. the `/goal` record, not the
// seeded one the assertions then read. Updating the existing record keeps one
// record per session and keeps every assertion pointed at the record
// production actually operates on.
func seedCriteriaOntoActiveGoal(t *testing.T, sid, prompt string, criteria []task.AcceptanceCriterion) *goal.Goal {
	t.Helper()
	g := goalRecordForSession(t, sid)
	updated, err := goal.NewStore(config.OmnipusHomeDir()).Update(g.GoalID, func(cur *goal.Goal) error {
		cur.Prompt = prompt
		if serr := cur.SetCriteria(criteria, time.Now().UTC()); serr != nil {
			return serr
		}
		// The floor DoD's two fixed "goal-dod-floor-*" sentinels are ids the
		// canned judge providers answer for — required, because the judged
		// set is criteria UNION DoD (ADR-080 D-DOD).
		return cur.SetDoD(newFloorDoD(), time.Now().UTC())
	})
	if err != nil {
		t.Fatalf("seedCriteriaOntoActiveGoal(%q): %v", sid, err)
	}
	return updated
}

// setGoalRecordMaxRounds pins a goal record's budget ceiling — the ADR-086
// replacement for the retired `SetMeta(sid, MetaPatch{GoalMaxRounds: &n})`
// arrange step (GOAL-FR-024: MaxRounds lives on the record).
func setGoalRecordMaxRounds(t *testing.T, goalID string, n int) {
	t.Helper()
	if _, err := goal.NewStore(config.OmnipusHomeDir()).Update(goalID, func(cur *goal.Goal) error {
		cur.MaxRounds = n
		return nil
	}); err != nil {
		t.Fatalf("setGoalRecordMaxRounds(%q, %d): %v", goalID, n, err)
	}
}

// readGoalRecord is this file's shared re-read helper: a fresh
// goal.NewStore(config.OmnipusHomeDir()) pointed at the SAME OMNIPUS_HOME
// newGoalLoopTestLoop's t.Setenv(config.EnvHome, …) established, mirroring
// resolveGoalRecordStore's own construction.
func readGoalRecord(t *testing.T, goalID string) *goal.Goal {
	t.Helper()
	g, err := goal.NewStore(config.OmnipusHomeDir()).Get(goalID)
	if err != nil {
		t.Fatalf("readGoalRecord: Get(%q): %v", goalID, err)
	}
	return g
}

// --- S-13 (HP): a met goal keeps its record --------------------------------

func TestTerminalGoalRetainsRecord_Met(t *testing.T) {
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

	g := seedCriteriaOntoActiveGoal(t, sid, "make the tests pass",
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")})

	judgeInst.Provider = metJudgeProvider("tests pass")

	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	result := &turnResult{finalContent: "[goal:evidence] all tests green\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
	// wave R7C (Group 2): JUDGE-FR-098 (landed by E13) made adjudication
	// deferred — checkGoalLoopAfterTurn now only RECORDS the work on
	// result.goalDeferredAdjudication and returns; production dispatches it
	// from runAgentLoop's own goroutine, strictly after PublishOutbound.
	// The test drives that dispatch synchronously and inline, the same
	// pattern E13 applied throughout its own write-set.
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)
	cleanup()

	after := readGoalRecord(t, g.GoalID)
	if after.State != generated.GoalStateMet {
		t.Fatalf("record state = %q, want %q (FR-027/FR-028: a met verdict transitions the record, S-13)",
			after.State, generated.GoalStateMet)
	}
	if len(after.Criteria) == 0 {
		t.Fatal("record.Criteria must survive the terminal transition (FR-027: \"the record MUST survive with its criteria\", S-13)")
	}
	if after.Criteria[0].Text != "the tests pass" {
		t.Fatalf("record.Criteria[0].Text = %q, want the seeded criterion text — the record must not be a fresh/blank one", after.Criteria[0].Text)
	}
	if after.TerminalReason == "" {
		t.Fatal("record.TerminalReason must be set on a terminal transition")
	}

	payloads := goalStatusPayloadsFor(c, sid)
	terminal := terminalPayloads(payloads)
	if len(terminal) != 1 {
		t.Fatalf("terminal GoalStatusFrames emitted = %d, want exactly 1 (S-17: \"exactly one is emitted\")", len(terminal))
	}
	if terminal[0].State != goalPillDone {
		t.Fatalf("terminal frame state = %q, want %q", terminal[0].State, goalPillDone)
	}
}

// --- S-14 (AP): an exhausted goal keeps its record --------------------------

func TestTerminalGoalRetainsRecord_Exhausted(t *testing.T) {
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

	g := seedCriteriaOntoActiveGoal(t, sid, "make the tests pass",
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")})

	// Last permitted attempt: max_rounds = 1, rounds_used = 0 (this WILL be
	// attempt 1 of 1). ADR-086: the budget ceiling lives on the goal record.
	setGoalRecordMaxRounds(t, g.GoalID, 1)
	judgeInst.Provider = unmetJudgeProvider("still failing")

	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	result := &turnResult{finalContent: "[goal:evidence] ran the suite\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
	// wave R7C (Group 2): see TestTerminalGoalRetainsRecord_Met's identical
	// comment — JUDGE-FR-098's deferred adjudication must be driven
	// explicitly before the record reflects a terminal transition.
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)
	cleanup()

	after := readGoalRecord(t, g.GoalID)
	if after.State != generated.GoalStateExhausted {
		t.Fatalf("record state = %q, want %q (FR-027/FR-028: round exhaustion transitions the record, S-14)",
			after.State, generated.GoalStateExhausted)
	}
	if !strings.Contains(after.TerminalReason, "round bound reached") {
		t.Fatalf("record.TerminalReason = %q, want a round-bound-reached reason/handover", after.TerminalReason)
	}
	if len(after.Criteria) == 0 {
		t.Fatal("record.Criteria must survive the terminal transition (S-14)")
	}

	payloads := goalStatusPayloadsFor(c, sid)
	terminal := terminalPayloads(payloads)
	if len(terminal) != 1 {
		t.Fatalf("terminal GoalStatusFrames emitted = %d, want exactly 1", len(terminal))
	}
	if terminal[0].State != goalPillFailed {
		t.Fatalf("terminal frame state = %q, want %q (a genuine exhaustion, not a user choice)", terminal[0].State, goalPillFailed)
	}
}

// --- S-15 (AP): an operator-cleared goal is distinguishable -----------------

func TestTerminalGoalRetainsRecord_OperatorCleared(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)

	// wave "taskedit" (this fix): this test was the last one in the file still
	// calling seedActiveGoalRecord AFTER a real `/goal` drive. Under ADR-086
	// the `/goal` drive IS the record creation, so the seed minted a SECOND
	// active record on the same session — the upstream invariant violation
	// activeGoalForSession reports at Warn and resolves in favour of the
	// OLDER record. `/goal clear` therefore correctly cleared the `/goal`
	// record while the assertions below read the SEEDED one, which nothing
	// had ended. Switched to seedCriteriaOntoActiveGoal, exactly as
	// TestTerminalGoalRetainsRecord_Met and _Exhausted above already do, so
	// there is one record per session and the assertions point at the record
	// production actually operates on. Nothing asserted below changed.
	g := seedCriteriaOntoActiveGoal(t, sid, "make the tests pass",
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")})

	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("clear: matched=%v handled=%v, want both true", matched, handled)
	}
	cleanup()

	after := readGoalRecord(t, g.GoalID)
	if after.State != generated.GoalStateCleared {
		t.Fatalf("record state = %q, want %q (S-15)", after.State, generated.GoalStateCleared)
	}
	if after.State == generated.GoalStateMet || after.State == generated.GoalStateExhausted {
		t.Fatalf("an operator clear's terminal state (%q) must be distinct from met/exhausted", after.State)
	}
	if len(after.Criteria) == 0 {
		t.Fatal("record.Criteria must survive an operator clear too (FR-027 applies to every ending kind)")
	}

	payloads := goalStatusPayloadsFor(c, sid)
	terminal := terminalPayloads(payloads)
	if len(terminal) != 1 {
		t.Fatalf("terminal GoalStatusFrames emitted = %d, want exactly 1", len(terminal))
	}
	if terminal[0].State != goalPillCleared {
		t.Fatalf("terminal frame state = %q, want %q", terminal[0].State, goalPillCleared)
	}
}

// --- S-16 (EC): idle expiry is a fourth distinct terminal state ------------

func TestTerminalGoalRetainsRecord_IdleExpired(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("shared session store not available")
	}

	now := time.Now().UTC()
	expiredAt := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339) // exactly 7d idle

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentInst.ID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sid := meta.ID
	condition := "make the tests pass"

	// ADR-086: no `/goal` drive here, so seedActiveGoalRecord IS the
	// activation — there is no second record to collide with, and the
	// retired GoalCondition/GoalMaxRounds/GoalStartedAt/GoalLastActivityAt
	// meta patch that used to sit alongside it is gone with the fields.
	g := seedActiveGoalRecord(t, sid, condition,
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")}, nil)
	// The record's OWN last_activity_at (R-06: "idle expiry uses the goal
	// record's own last_activity_at") is what drives this — set it directly
	// via Store.Update rather than through New's now-at-creation default.
	expiredTime, _ := time.Parse(time.RFC3339, expiredAt)
	if _, err := goal.NewStore(config.OmnipusHomeDir()).Update(g.GoalID, func(cur *goal.Goal) error {
		cur.LastActivityAt = expiredTime
		return nil
	}); err != nil {
		t.Fatalf("seeding record LastActivityAt: %v", err)
	}

	c, cleanup := newEventCollector(t, al)
	defer cleanup()
	al.goalIdleExpirySweep(config.PlanningConfig{}, now)
	cleanup()

	after := readGoalRecord(t, g.GoalID)
	if after.State != generated.GoalStateExpired {
		t.Fatalf("record state = %q, want %q (S-16: idle expiry is its own distinct terminal state)",
			after.State, generated.GoalStateExpired)
	}
	for _, other := range []generated.GoalState{generated.GoalStateMet, generated.GoalStateExhausted, generated.GoalStateCleared} {
		if after.State == other {
			t.Fatalf("idle-expiry's terminal state must be distinct from %q", other)
		}
	}

	payloads := goalStatusPayloadsFor(c, sid)
	terminal := terminalPayloads(payloads)
	if len(terminal) != 1 {
		t.Fatalf("terminal GoalStatusFrames emitted = %d, want exactly 1 (S-17)", len(terminal))
	}
	if terminal[0].State != goalPillExpired {
		t.Fatalf("terminal frame state = %q, want %q — distinct from %q (a genuine round/budget exhaustion)",
			terminal[0].State, goalPillExpired, goalPillFailed)
	}
	if terminal[0].State == goalPillFailed {
		t.Fatal("idle expiry must NOT collapse into the generic failed pill (FR-028)")
	}
}

// --- S-18 (EC): the Judge unavailable at a boundary is not terminal --------

func TestTerminalGoalRetainsRecord_JudgeUnavailableAtBoundary_NotTerminal(t *testing.T) {
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

	g := seedCriteriaOntoActiveGoal(t, sid, "make the tests pass",
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")})

	// Last permitted attempt, exactly like S-14 — this is the boundary
	// where an exhaustion would otherwise fire.
	setGoalRecordMaxRounds(t, g.GoalID, 1)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, context.DeadlineExceeded
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 1)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	result := &turnResult{finalContent: "[goal:evidence] ran the suite\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(ctx, agentInst, opts, result)
	// wave R7C (Group 2): the deferred dispatch runs on its OWN fresh
	// context internally (dispatchDeferredGoalAdjudication's own doc
	// comment: "never the turn-time snapshot"), so this test's deliberately
	// expired `ctx` above no longer reaches the Judge call at all — the
	// fakeJudgeProvider below always errors regardless, which is what
	// actually drives the unavailable-boundary outcome this test proves.
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)

	after := readGoalRecord(t, g.GoalID)
	if after.State != generated.GoalStateActive {
		t.Fatalf("record state = %q, want %q — a Judge-unavailable boundary must NOT transition the record (S-18)",
			after.State, generated.GoalStateActive)
	}
	if after.Round != 0 {
		t.Fatalf("record.Round = %d, want 0 — no round is consumed when the Judge is unavailable (S-18)", after.Round)
	}

	// ADR-086: there is no session-meta mirror left to cross-check — the
	// record IS the state, and it is asserted above. What remains worth
	// asserting here is that the session still resolves to an ACTIVE goal
	// through the predicate production uses.
	metaAfter := goalRecordForSessionOrNil(sid)
	if metaAfter == nil {
		t.Fatal("goal must remain active (re-armed) when the judge is unavailable, not terminal")
	}
	if metaAfter.Round != 0 {
		t.Fatalf("rounds_used = %d, want 0", metaAfter.Round)
	}
}

// terminalPayloads filters payloads (emission order) down to those carrying
// one of the four terminal pill states this wave's clearGoal now
// distinguishes — the judging/active/waiting_on_user pills a normal round
// also emits are excluded, so "exactly one" (S-17) means exactly one
// TERMINAL frame, not exactly one frame overall.
func terminalPayloads(payloads []GoalStatusChangedPayload) []GoalStatusChangedPayload {
	var out []GoalStatusChangedPayload
	for _, p := range payloads {
		switch p.State {
		case goalPillDone, goalPillFailed, goalPillCleared, goalPillExpired:
			out = append(out, p)
		}
	}
	return out
}
