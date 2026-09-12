// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_triggers_test.go covers ADR-053 Phase-2 §1/§2 claim-or-idle triggering,
// waiting_on_user pause, bounce economics, and the idle settlement invariants
// (FR-101..FR-105/FR-107/FR-109; acceptance G-1..G-5; folded findings
// F5/N-3/N-11/N-12/N-13). Mirrors goal_loop_test.go's newGoalLoopTestLoop
// harness (a fake Judge LLM provider swapped onto the seeded Judge agent).
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// withShortIdleWindow swaps goalIdleQuietWindow to a small value for the test
// and restores it on cleanup. Must not be combined with t.Parallel (the var
// is package-scoped); all tests here run non-parallel under -p 1.
func withShortIdleWindow(t *testing.T, d time.Duration) {
	t.Helper()
	prev := goalIdleQuietWindow
	goalIdleQuietWindow = d
	t.Cleanup(func() { goalIdleQuietWindow = prev })
}

// countingProvider is a fake Judge LLM provider that returns a fixed canned
// response and counts how many times it was invoked — so a test can assert the
// Judge fired EXACTLY once (G-1/INV-1). Reuses judge_test.go's
// fakeJudgeProvider (which already carries a calls counter + callCount()) via
// the metJudgeProvider/unmetJudgeProvider constructors defined there.

// armedGoalMaxRounds is the budget ceiling every arm-a-goal helper below
// stamps — the value the retired `GoalMaxRounds: &maxRounds` session-meta
// patch used to write.
const armedGoalMaxRounds = 5

// armGoalRecord is the ADR-086 arrange primitive shared by every
// "put an ACTIVE goal on this session" helper in the suite. It replaces the
// `store.SetMeta(sid, session.MetaPatch{GoalCondition: …, GoalCriteriaJSON:
// …, GoalRoundsUsed: …, GoalMaxRounds: …, GoalLastActivityAt: …,
// GoalStartedAt: …})` block those helpers used to run: wave S6 deleted all
// six fields, and production now reads the goal off its OWN pkg/goal record
// (activeGoalForSession).
//
// criteria may be nil — that is the RECORDLESS goal (ADR-081 D3's transient
// state), which routes idle settlement to the D6c nudge ladder rather than
// the Judge. DoD is always newFloorDoD(): Goal.Validate requires a non-empty
// DoD on every persisted record, so a record with "no DoD" is not a state
// this store can represent.
//
// lastActivity is written to BOTH LastActivityAt and StartedAt, matching the
// retired patch, which set GoalLastActivityAt and GoalStartedAt to the same
// `past` string. Returns the goal id.
func armGoalRecord(
	t *testing.T, sid, condition string, criteria []task.AcceptanceCriterion,
	roundsUsed int, lastActivity time.Time,
) string {
	t.Helper()
	past := lastActivity.UTC()
	gs := goal.NewStore(config.OmnipusHomeDir())

	var gid string
	if existing := activeGoalForSession(sid); existing != nil {
		gid = existing.GoalID
	} else {
		g, err := goal.New(generated.GoalOwnerKindSession, sid, generated.ChatCompiled,
			condition, "", criteria, newFloorDoD(), armedGoalMaxRounds, time.Now().UTC())
		if err != nil {
			t.Fatalf("armGoalRecord: goal.New: %v", err)
		}
		g.GoalID = newGoalID()
		if cerr := gs.Create(g); cerr != nil {
			t.Fatalf("armGoalRecord: Create: %v", cerr)
		}
		gid = g.GoalID
		if _, uerr := gs.Update(gid, func(cur *goal.Goal) error {
			return cur.Activate(sid, time.Now().UTC())
		}); uerr != nil {
			t.Fatalf("armGoalRecord: Activate: %v", uerr)
		}
	}

	if _, uerr := gs.Update(gid, func(cur *goal.Goal) error {
		cur.Prompt = condition
		cur.MaxRounds = armedGoalMaxRounds
		cur.Round = roundsUsed
		if serr := cur.SetCriteria(criteria, past); serr != nil {
			return serr
		}
		// SetCriteria stamps LastActivityAt with `past` already; StartedAt is
		// stamped here because Activate set it to "now", not to the past
		// timestamp the idle-window precondition needs.
		started := past
		cur.StartedAt = &started
		cur.LastActivityAt = past
		return nil
	}); uerr != nil {
		t.Fatalf("armGoalRecord: arm: %v", uerr)
	}
	return gid
}

// recordedGoalCriteria is the single prose criterion a RECORDED test goal
// carries — the counterpart of the "goal-condition"-id criterion the canned
// judge providers used to be the only match for. Its id is
// recordedGoalCriterionID (goal_loop_test.go) rather than "goal-condition"
// because the latter is one of GOAL-FR-007's reserved, never-persisted ids
// and pkg/goal refuses to store it; the canned providers answer for both.
func recordedGoalCriteria(condition string) []task.AcceptanceCriterion {
	return []task.AcceptanceCriterion{{
		ID: recordedGoalCriterionID, Kind: task.KindProse, Judgment: task.JudgmentBoolean,
		Text: condition, Status: task.CritPending,
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
	}}
}

// setGoalRoundsArmed sets a goal active on sid with the round count and an
// activity timestamp in the past (so the idle quiet window is already elapsed)
// — the idle-settlement precondition. Uses the back-compat single-prose
// criterion path (an EMPTY criteria ladder on the goal record) so the canned
// judge providers match.
//
// ADR-081 D3 caveat: an empty criteria ladder on an ACTIVE goal is now the
// recordless/D6c-nudge transient state (goal_triggers.go's
// maybeSettleGoalIdle routes it to the nudge ladder, never the Judge, at
// idle) — this helper's old "empty criteria still reaches the Judge via
// compiledGoalCriteriaFor's back-compat synthesis" property therefore no
// longer holds AT IDLE TIME (it still holds on the CLAIM path, which never
// consulted the ladder's emptiness). A test that specifically wants to
// exercise the recordless nudge ladder (or a claim-path scenario) still uses
// this helper as before; a test that wants IDLE-PATH JUDGE ADJUDICATION uses
// setGoalRoundsArmedRecorded below instead.
// Returns the goal id, so a caller that needs to key an in-memory marker on
// it (idleSettling / waitingOnUser are both goal-id keyed, DD-7(b)/
// GOAL-FR-052) can use the record's REAL id instead of inventing one.
func setGoalRoundsArmed(t *testing.T, _ *session.UnifiedStore, sid, condition string, roundsUsed int, lastActivity time.Time) string {
	t.Helper()
	return armGoalRecord(t, sid, condition, nil, roundsUsed, lastActivity)
}

// setGoalRoundsArmedRecorded is setGoalRoundsArmed's ADR-081 counterpart for
// tests that exercise IDLE-PATH JUDGE ADJUDICATION specifically: a single
// prose criterion the canned judge providers answer for
// (unmetJudgeProvider/metJudgeProvider), persisted EXPLICITLY onto the goal
// record rather than left empty — a RECORDED goal, per D3, so
// maybeSettleGoalIdle evaluates the normal adjudication path instead of
// routing to the D6c nudge ladder.
// Returns the goal id, same as setGoalRoundsArmed.
func setGoalRoundsArmedRecorded(t *testing.T, _ *session.UnifiedStore, sid, condition string, roundsUsed int, lastActivity time.Time) string {
	t.Helper()
	return armGoalRecord(t, sid, condition, recordedGoalCriteria(condition), roundsUsed, lastActivity)
}

// primeGoalZeroOutputTripleFalse seeds a prior ADJUDICABLE transcript entry
// (a tool call) for sid. KEPT — despite the FR-014b zero-output triple it
// used to prime being retired outright (JUDGE-FR-095/FR-097, this wave:
// goalZeroOutputTripleHolds and its own watermark consultation are
// deleted) — because wave E12's pkg/agent/goal_flow_integration_test.go
// (outside this wave's write-set) still calls it; deleting the symbol
// entirely breaks that file's compilation and, transitively, every test in
// the whole package. The watermark write is dropped (the map it seeded,
// goalTriggerState.outputWatermarks, has no reader left); the transcript
// append survives unchanged, since it is real, harmless setup data on its
// own. Reported: the caller's own idle-adjudication assertion
// (goal_flow_integration_test.go, "cycle 1: Judge calls = ... want 1") now
// asserts retired behavior and needs wave E12's own attention — this
// function's job is only to keep the package compiling.
func primeGoalZeroOutputTripleFalse(t *testing.T, store *session.UnifiedStore, sid, agentID string) {
	t.Helper()
	if err := store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID:        fmt.Sprintf("prime-%s-%d", sid, time.Now().UnixNano()),
		Type:      session.EntryTypeToolCall,
		ToolCalls: []session.ToolCall{{ID: session.ToolCallID(fmt.Sprintf("prime-tc-%d", time.Now().UnixNano())), Tool: "bash", Status: "success"}},
		Timestamp: time.Now().Add(-90 * time.Minute), AgentID: agentID,
	}); err != nil {
		t.Fatal(err)
	}
}

// =========================== Parser (S6 family) ===========================

func TestGoalStatusMarkerParser_Family(t *testing.T) {
	cases := []struct {
		name        string
		output      string
		wantPresent bool
		wantStatus  string
		wantEv      bool
	}{
		{
			name:        "met with evidence immediately before",
			output:      "[goal:evidence] all tests green\nGOAL_STATUS: met",
			wantPresent: true, wantStatus: goalStatusMet, wantEv: true,
		},
		{
			name:        "met bare (no evidence line) — bounce case",
			output:      "I'm done.\nGOAL_STATUS: met",
			wantPresent: true, wantStatus: goalStatusMet, wantEv: false,
		},
		{
			name:        "waiting_on_user typed pause",
			output:      "I need to ask the user a question.\nGOAL_STATUS: waiting_on_user",
			wantPresent: true, wantStatus: goalStatusWaitingOnUser, wantEv: false,
		},
		{
			name:        "no marker — deterministic not-waiting/not-claim fallback",
			output:      "Still working on the task, about 60% done.",
			wantPresent: false, wantStatus: "", wantEv: false,
		},
		{
			name:        "fenced marker is not a real signal",
			output:      "```\nGOAL_STATUS: met\n```",
			wantPresent: false,
		},
		{
			name:        "evidence line fenced does not count (bare)",
			output:      "```\n[goal:evidence] x\n```\nGOAL_STATUS: met",
			wantPresent: true, wantStatus: goalStatusMet, wantEv: false,
		},
		{
			name:        "blank line between evidence and marker tolerated",
			output:      "[goal:evidence] verified\n\nGOAL_STATUS: met",
			wantPresent: true, wantStatus: goalStatusMet, wantEv: true,
		},
		{
			name:        "markdown-emphasis-wrapped marker matches",
			output:      "[goal:evidence] ok\n**GOAL_STATUS**: met",
			wantPresent: true, wantStatus: goalStatusMet, wantEv: true,
		},
		{
			name:        "unrecognized value — present but empty status",
			output:      "GOAL_STATUS: thinking",
			wantPresent: true, wantStatus: "",
		},
		{
			name:        "empty evidence text is bare",
			output:      "[goal:evidence]\nGOAL_STATUS: met",
			wantPresent: true, wantStatus: goalStatusMet, wantEv: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseGoalStatusMarker(tc.output)
			if got.Present != tc.wantPresent {
				t.Fatalf("Present = %v, want %v", got.Present, tc.wantPresent)
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.HasEvidence != tc.wantEv {
				t.Fatalf("HasEvidence = %v, want %v (evidence=%q)", got.HasEvidence, tc.wantEv, got.EvidenceText)
			}
		})
	}
}

// =========================== G-1: claim → Judge once =====================

// TestClaim_AdjudicatesExactlyOnce_G1 proves FR-101(a)/INV-1: a completion
// claim ([goal:evidence] + GOAL_STATUS: met) invokes the Judge EXACTLY once.
func TestClaim_AdjudicatesExactlyOnce_G1(t *testing.T) {
	resetGoalTriggerStateForTest()
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
	clearGoalRecordCriteria(t, sid)

	cp := metJudgeProvider("tests pass")
	judgeInst.Provider = cp

	result := &turnResult{finalContent: "[goal:evidence] all green\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	// JUDGE-FR-098 (D13, this wave): the Judge is no longer invoked
	// synchronously inside checkGoalLoopAfterTurn — the claim is recorded as
	// deferred work instead, to be dispatched AFTER delivery (runAgentLoop).
	if cp.callCount() != 0 {
		t.Fatalf("Judge invoked %d times synchronously inside checkGoalLoopAfterTurn, want 0 (D13: dispatch is deferred)", cp.callCount())
	}
	if result.goalDeferredAdjudication == nil {
		t.Fatal("a met+evidence claim must record deferred adjudication work (JUDGE-FR-098)")
	}
	if result.goalDeferredAdjudication.claimText != result.finalContent {
		t.Fatalf("deferred work claimText = %q, want the marker path's whole finalContent %q",
			result.goalDeferredAdjudication.claimText, result.finalContent)
	}

	// Simulate runAgentLoop's own post-delivery dispatch (the goroutine call)
	// to prove the underlying "exactly once" guarantee (G-1/INV-1) still
	// holds end-to-end once the deferred work actually runs.
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)
	if cp.callCount() != 1 {
		t.Fatalf("Judge invoked %d times after the deferred dispatch, want exactly 1 (G-1/INV-1)", cp.callCount())
	}
	if after := goalRecordForSessionOrNil(sid); after != nil {
		t.Fatalf("met claim must clear the goal, still ACTIVE: %q", after.Prompt)
	}
}

// TestNoClaim_NoAdjudication_FR101 proves the ADR-053 supersession: a turn
// with NO claim marker does NOT adjudicate (the ADR-052 after-every-turn
// defect is gone). FR-101 / Explicit Non-Behavior.
func TestNoClaim_NoAdjudication_FR101(t *testing.T) {
	resetGoalTriggerStateForTest()
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
	clearGoalRecordCriteria(t, sid)

	cp := metJudgeProvider("tests pass")
	judgeInst.Provider = cp

	// A turn that does NOT end in a claim marker — must NOT reach the Judge.
	result := &turnResult{finalContent: "Still working — about 60% done, no claim yet."}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	if cp.callCount() != 0 {
		t.Fatalf("Judge invoked %d times on a non-claim turn, want 0 (FR-101: never after every worker turn)", cp.callCount())
	}
	after := goalRecordForSessionOrNil(sid)
	if after == nil {
		t.Fatal("goal must remain active on a non-claim turn")
	}
	if after.Round != 0 {
		t.Fatalf("non-claim turn consumed a round (%d), want 0", after.Round)
	}
}

// =========================== G-2/G-3: idle settlement (D13 retirement) ====
//
// JUDGE-FR-095/FR-097 (ADR-084 revision 9 D13, wave E13): the claimless
// idle-adjudication path this section used to exercise is RETIRED — a `met`
// claim is now the sole adjudication trigger, and the idle tick only ever
// re-posts (a bounded continue-push), never judges. The two tests that used
// to live here (TestIdleSettlement_FiresOnceConsumesRoundRearms_G2,
// TestIdleSettlement_ClaimlessPassesEmptyClaimText_G3 — a claimless call is
// now REFUSED by runGoalAdjudication's own FR-095 guard, not merely a
// different code path) are superseded by the push-ladder tests below and by
// TestQuietWindow_MakesZeroJudgeCalls / TestRunGoalAdjudication_RejectsEmptyClaimText
// in goal_triggers_adr084_test.go — the FR-095 oracles this delivery
// requires by name.

// TestIdlePush_FiresOnceConsumesNoRoundRearms_G2 is G2's push-ladder
// successor: idle settlement fires exactly one continue-push per quiet
// spell, consumes NO round (D13: nothing is judged), and re-arms only on
// new activity.
func TestIdlePush_FiresOnceConsumesNoRoundRearms_G2(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRoundsArmedRecorded(t, store, sid, "goal A", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must never be called — D13 retires idle adjudication")
	judgeInst.Provider = cp

	// Fire 1: the quiet window elapsed (activity was 1h ago) → one push.
	al.goalQuietWindowSettle(time.Now())
	if cp.callCount() != 0 {
		t.Fatalf("after first settle: Judge calls = %d, want 0 (D13: idle never judges)", cp.callCount())
	}
	after := goalRecordForSession(t, sid)
	if after.Round != 0 {
		t.Fatalf("after first settle: rounds_used = %d, want 0 (a push consumes no round)", after.Round)
	}
	if after.ZeroOutputPushes != 1 {
		t.Fatalf("after first settle: pushes = %d, want 1", after.ZeroOutputPushes)
	}

	// Fire 2 immediately: the push bumped the goal record's LastActivityAt to
	// ~now, so the quiet window has NOT elapsed again → no second push (re-arm).
	al.goalQuietWindowSettle(time.Now())
	if after2 := goalRecordForSession(t, sid); after2.ZeroOutputPushes != 1 {
		t.Fatalf("after second immediate settle: pushes = %d, want still 1 (re-arm only on new activity)", after2.ZeroOutputPushes)
	}
}

// =========================== G-4: bounce economics ======================

// TestBareClaim_FirstFreeSecondCosts_G4 proves FR/G-4: the 1st bare
// GOAL_STATUS: met (no [goal:evidence]) is bounced free (teaching steer, no
// round); the 2nd consumes an attempt/round.
func TestBareClaim_FirstFreeSecondCosts_G4(t *testing.T) {
	resetGoalTriggerStateForTest()
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
	clearGoalRecordCriteria(t, sid)
	cp := metJudgeProvider("x") // should NEVER be called on a bare claim
	judgeInst.Provider = cp

	// 1st bare claim: free — bounced with a teaching steer, no round, no Judge.
	r1 := &turnResult{finalContent: "GOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r1)
	if cp.callCount() != 0 {
		t.Fatalf("1st bare claim: Judge calls = %d, want 0 (bounced before Judge)", cp.callCount())
	}
	if after1 := goalRecordForSession(t, sid); after1.Round != 0 {
		t.Fatalf("1st bare claim: rounds_used = %d, want 0 (free steer)", after1.Round)
	}
	if len(r1.followUps) != 1 || !strings.Contains(r1.followUps[0].Content, "evidence") {
		t.Fatalf("1st bare claim: want a teaching steer followUp, got %d followUps", len(r1.followUps))
	}

	// 2nd bare claim: costs an attempt/round (still no Judge — nothing to judge).
	r2 := &turnResult{finalContent: "GOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r2)
	if cp.callCount() != 0 {
		t.Fatalf("2nd bare claim: Judge calls = %d, want 0 (no evidence to judge)", cp.callCount())
	}
	if after2 := goalRecordForSession(t, sid); after2.Round != 1 {
		t.Fatalf("2nd bare claim: rounds_used = %d, want 1 (consumes an attempt/round, G-4)", after2.Round)
	}
}

// =========================== G-5: waiting_on_user pause ==================

// TestWaitingOnUser_PauseNoRoundNoVerdictIdleSuppressed_G5 proves FR-104/G-5:
// a GOAL_STATUS: waiting_on_user turn pauses with no verdict and no round, and
// idle settlement is suppressed while the pause holds.
func TestWaitingOnUser_PauseNoRoundNoVerdictIdleSuppressed_G5(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
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
	clearGoalRecordCriteria(t, sid)
	cp := metJudgeProvider("x")
	judgeInst.Provider = cp

	// Turn ends in a waiting_on_user marker → pause, no verdict, no round.
	r := &turnResult{finalContent: "Which DB should I target?\nGOAL_STATUS: waiting_on_user"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r)
	if cp.callCount() != 0 {
		t.Fatalf("waiting_on_user turn: Judge calls = %d, want 0 (no verdict)", cp.callCount())
	}
	after := goalRecordForSession(t, sid)
	if after.Round != 0 {
		t.Fatalf("waiting_on_user turn: rounds_used = %d, want 0 (no round consumed)", after.Round)
	}
	// waitingOnUser is keyed by GOAL ID (GOAL-FR-052), not session id.
	if !al.goalIsWaitingOnUser(after.GoalID) {
		t.Fatal("goal must be in waiting_on_user pause after the marker (G-5)")
	}

	// Idle settlement must be SUPPRESSED while waiting — even with the quiet
	// window long elapsed.
	setGoalRoundsArmed(t, store, sid, after.Prompt, 0, time.Now().Add(-1*time.Hour))
	// Re-assert the pause flag. ADR-086: armGoalRecord re-arms the session's
	// EXISTING active record rather than minting a second one, so the goal id
	// the flag is keyed on survives on its own — the retired
	// `SetMeta(GoalID: &goalID)` restore this test used to need is gone with
	// the field.
	goalID := after.GoalID
	al.goalSetWaitingOnUser(goalID, true)
	al.goalQuietWindowSettle(time.Now())
	if cp.callCount() != 0 {
		t.Fatalf("idle settle while waiting_on_user: Judge calls = %d, want 0 (idle suppressed, G-5)", cp.callCount())
	}

	// User replies in normal chat → pause clears, goal resumes (re-arms idle).
	// opts already carries UserInitiated: true; a plain (no-marker) reply turn
	// hits the resume branch and then the default (active) branch.
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{
		finalContent: "Use postgres, thanks.",
	})
	if al.goalIsWaitingOnUser(goalID) {
		t.Fatal("user reply must clear the waiting_on_user pause (G-5 resume)")
	}
}

// =========================== N-12: stray claim after clear ===============

// TestStrayClaimAfterClear_Inert_N12 proves FR-114/N-12: a GOAL_STATUS: met
// emitted after /goal clear does nothing (no active goal to adjudicate).
func TestStrayClaimAfterClear_Inert_N12(t *testing.T) {
	resetGoalTriggerStateForTest()
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
	al.clearGoal(sid, store, "cleared by user")
	cp := metJudgeProvider("x")
	judgeInst.Provider = cp

	// A later stray claim on the same session — inert (no active goal).
	r := &turnResult{finalContent: "[goal:evidence] done\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r)
	if cp.callCount() != 0 {
		t.Fatalf("stray claim after clear: Judge calls = %d, want 0 (N-12: inert)", cp.callCount())
	}
}

// =========================== F5: verifier turn = activity =================

// TestVerifierInFlight_SuppressesIdlePush_NoSelfRace_F5 proves architect F5 /
// FR-109's push-ladder successor (D13): a running (claim-triggered)
// adjudication suppresses the idle tick's continue-push too — the verifier's
// own turn IS activity, and the idle path must not act (push OR, were it
// still possible, judge) against a goal whose adjudication is already
// in flight. Simulated by registering an in-flight verifier via the
// PlanEngine's registry (goalAdjudicationInFlight's own real production
// consult, F1's key-consistency contract, unaffected by D13).
func TestVerifierInFlight_SuppressesIdlePush_NoSelfRace_F5(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRoundsArmedRecorded(t, store, sid, "goal F5", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("must never be called — D13 retires idle adjudication")
	judgeInst.Provider = cp

	// Install a PlanEngine so goalAdjudicationInFlight consults its registry.
	pe := NewPlanEngine(al, plan.New(t.TempDir()), nil, nil)
	al.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)
	// Simulate a verifier turn currently in flight for this goal.
	pe.VerifierRegistry().Register(verifierUnitForGoal(sid), "fake-verifier-session")

	al.goalQuietWindowSettle(time.Now())
	if after := goalRecordForSession(t, sid); after.ZeroOutputPushes != 0 {
		t.Fatalf("idle settle with in-flight verifier: pushes = %d, want 0 (F5: no self-race)", after.ZeroOutputPushes)
	}
	if cp.callCount() != 0 {
		t.Fatalf("idle settle with in-flight verifier: Judge calls = %d, want 0", cp.callCount())
	}

	// Once the verifier turn completes (unregistered), the guard opens — but
	// the activity clock must still gate the next fire (re-arm only on activity).
	pe.VerifierRegistry().Unregister(verifierUnitForGoal(sid))
	al.goalQuietWindowSettle(time.Now())
	if after := goalRecordForSession(t, sid); after.ZeroOutputPushes != 1 {
		t.Fatalf("after verifier completes: pushes = %d, want 1 (guard opens)", after.ZeroOutputPushes)
	}
}

// =========================== FR-107: per-goal-id independence ============

// TestPerGoalId_TwoSessionsIndependent_FR107 proves FR-107/R§8.11's
// push-ladder successor (D13): two goal-bearing sessions run independent
// settlements — each fires its own continue-push, keyed by goal-id.
func TestPerGoalId_TwoSessionsIndependent_FR107(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sidA := newGoalTestSession(t, al, agentInst.ID)
	_, sidB := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sidA, "", "webchat", "c1", "sk1", agentInst.ID)
	al.recordGoalRouting(sidB, "", "webchat", "c2", "sk2", agentInst.ID)
	// DD-7(b)/GOAL-FR-052 (E13): idleSettling is keyed by GOAL ID, not session
	// ID, so this test's premise (two goals track INDEPENDENTLY) needs two
	// distinct goal ids. ADR-086 supplies them for free — each session gets
	// its OWN pkg/goal record with its own minted id — so the two synthetic
	// `SetMeta(GoalID: …)` patches this test used to need are gone with the
	// field; the helper now hands back the record's REAL id.
	goalIDA := setGoalRoundsArmedRecorded(t, store, sidA, "goal A", 0, time.Now().Add(-1*time.Hour))
	// Goal B: activity is RECENT — its quiet window has NOT elapsed.
	goalIDB := setGoalRoundsArmedRecorded(t, store, sidB, "goal B", 0, time.Now())
	if goalIDA == goalIDB {
		t.Fatalf("two sessions must carry two DISTINCT goal records, both got %q", goalIDA)
	}

	cpA := unmetJudgeProvider("must never be called — D13 retires idle adjudication")
	judgeInst.Provider = cpA

	al.goalQuietWindowSettle(time.Now())
	if cpA.calls != 0 {
		t.Fatalf("Judge calls = %d, want 0 (D13: idle never judges)", cpA.calls)
	}
	if afterA := goalRecordForSession(t, sidA); afterA.ZeroOutputPushes != 1 {
		t.Fatalf("goal A (window elapsed) pushes = %d, want 1", afterA.ZeroOutputPushes)
	}
	if afterB := goalRecordForSession(t, sidB); afterB.ZeroOutputPushes != 0 {
		t.Fatalf("goal B (window not elapsed): pushes = %d, want 0 (independent timer)", afterB.ZeroOutputPushes)
	}

	// A's push must NOT mark B as settled (per-goal-id markers, DD-7(b)).
	if idleA, idleB := al.goalIsIdleSettling(goalIDA), al.goalIsIdleSettling(goalIDB); idleA == idleB {
		t.Fatalf("goal A and goal B idle-settling states must be distinct (per-goal-id): got A=%v B=%v", idleA, idleB)
	}
	if al.goalIsIdleSettling(goalIDB) {
		t.Fatal("goal B must not be marked idle-settling by goal A's push (per-goal-id)")
	}
}

// TestGoalAdjudication_RoundAdvancePersistFailure_Aborts_M3 proves the
// silent-M3 fix: when the GoalRoundsUsed round-advance persist fails, the
// adjudication must NOT silently continue with an un-persisted counter. The
// prior code WARN-logged and proceeded — emitting an advanced round frame and
// delivering the steer — so the goal re-dispatched while the durable counter
// stayed stale, and the attempt>=maxRounds gate could never fire (unbounded
// rounds). The fix aborts the round-advance: no steer is delivered, no advance
// is emitted, and the durable counter stays unchanged (gate stays honest); the
// failure is surfaced via ERROR + a system transcript note.
//
// The store is rigged by making the session dir read-only AFTER arming:
// writeMetaLocked's atomic temp+rename needs dir write, so SetMeta fails while
// GetMeta reads (the meta file already exists with read perms).
func TestGoalAdjudication_RoundAdvancePersistFailure_Aborts_M3(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "goal A", 0, time.Now().Add(-1*time.Hour))

	judgeInst.Provider = unmetJudgeProvider("not yet")

	// ADR-086: the round counter is persisted by pkg/goal.Store.Update onto
	// the GOAL record, not by session.SetMeta onto session meta — so the rig
	// blocks the GOAL entity dir, not the session dir. Reads (Store.Get)
	// still work: blockSessionDirWrites blocks only new-file creation
	// (the atomic temp+rename's CreateTemp), and every file this test's
	// record needs already exists by now.
	gstore := goal.NewStore(config.OmnipusHomeDir())
	rec := goalRecordForSession(t, sid)

	restore := blockSessionDirWrites(t, gstore.Dir())
	t.Cleanup(restore)

	// Sanity: confirm the goal-record write now actually fails (otherwise the
	// test cannot prove the persist-failure path).
	if _, perr := gstore.Update(rec.GoalID, func(cur *goal.Goal) error {
		cur.LatestReason = "probe"
		return nil
	}); perr == nil {
		t.Fatalf("setup invariant: the goal-record write unexpectedly succeeded on the read-only store; cannot prove the M3 path")
	}

	steerDelivered := false
	met := al.runGoalAdjudication(context.Background(), agentInst, "", sid, store, rec, "[goal:evidence] done, please verify",
		func(string) { steerDelivered = true })

	if met {
		t.Fatal("return = true (met); want false on an unmet round-advance persist failure")
	}
	if steerDelivered {
		t.Fatal("deliverSteer was called after a persist failure; the fix must abort BEFORE delivering the steer (silent-M3)")
	}

	// The durable round counter must NOT have advanced (the round was not
	// counted). pkg/entity.Store holds no in-memory cache, so a plain Get is
	// already durable truth (disk) — the fresh-store re-open the session-meta
	// version of this assertion needed has no counterpart here.
	after, aerr := gstore.Get(rec.GoalID)
	if aerr != nil {
		t.Fatalf("re-read goal record: %v", aerr)
	}
	if after.Round != 0 {
		t.Fatalf("durable goal record Round = %d, want 0 (round-advance must not persist on failure; gate stays honest)", after.Round)
	}

	// Control: with the dir writable again, the SAME adjudication advances the
	// counter to 1 and delivers the steer (proves the rig — not the unmet judge
	// — was the only blocker, and that the happy path still works after the fix).
	restore() // make the goal entity dir writable again for the control (happy-path) call
	// Reload the record so the control call sees the same starting state the
	// rig did.
	controlRec := goalRecordForSession(t, sid)
	steer2 := false
	al.runGoalAdjudication(context.Background(), agentInst, "", sid, store, controlRec, "[goal:evidence] done, please verify",
		func(string) { steer2 = true })
	if !steer2 {
		t.Fatal("control: with a writable store the unmet adjudication must deliver the steer (round advanced normally)")
	}
	after2, a2err := gstore.Get(rec.GoalID)
	if a2err != nil {
		t.Fatalf("re-read goal record (control): %v", a2err)
	}
	if after2.Round != 1 {
		t.Fatalf("control: durable goal record Round = %d, want 1 after a successful round-advance", after2.Round)
	}
}

// ===================== corr-MAJOR-2: idleSettling wedge ====================

// TestIdleSettle_JudgeUnavailable_ClearsIdleSettling_Refires_corrMAJOR2
// proves corr-MAJOR-2: when runGoalAdjudication hits the Judge-Unavailable
// branch, it must CLEAR the idleSettling marker (set by maybeSettleGoalIdle
// before dispatch) so the next quiet-window re-fires once the Judge recovers.
// Without the fix the marker stays set and every subsequent tick early-returns
// on goalIsIdleSettling → the goal is wedged in judge_unavailable forever.
//
// RETIRED (JUDGE-FR-095/D13, this wave, deleted outright rather than
// skipped): the scenario this test guarded against — the IDLE tick
// triggering a real Judge call that then goes Unavailable — no longer
// exists, because the idle tick never triggers the Judge at all any more
// (FR-097: it only ever re-posts). runGoalAdjudication's own Unavailable
// branch (including its idleSettling marker-clear) is still reachable and
// still exercised — now only via the claim path's deferred dispatch — by
// TestClaim_AdjudicatesExactlyOnce_G1's direct dispatchDeferredGoalAdjudication
// call above and by TestRunGoalAdjudication_RejectsEmptyClaimText in
// goal_triggers_adr084_test.go.

// ===================== corr-MAJOR-1: idle-path budget brake ================

// TestIdleSettle_BudgetExhausted_Brakes_corrMAJOR1 proves corr-MAJOR-1: the
// IDLE adjudication path must consult TokenBudget().Exhausted() (the CLAIM
// path already did). When exhausted, the idle settle must NOT fire the Judge
// (no round burned past the cap); instead the goal brakes honestly — cleared
// with failed_reason=budget_exhausted and a handover transcript, mirroring the
// claim path's brake. No double-debit: the Judge never runs.
func TestIdleSettle_BudgetExhausted_Brakes_corrMAJOR1(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	// Exhaust the overall token budget (cap 100, debited 100 → consumed >= cap).
	al.tokenBudget = NewTokenBudget(100, nil)
	al.tokenBudget.Debit(100)
	if !al.TokenBudget().Exhausted() {
		t.Fatal("setup invariant: budget must be exhausted")
	}
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	goalID := setGoalRoundsArmed(t, store, sid, "goal brake", 0, time.Now().Add(-1*time.Hour))

	cp := unmetJudgeProvider("x")
	judgeInst.Provider = cp

	al.goalQuietWindowSettle(time.Now())
	if cp.callCount() != 0 {
		t.Fatalf("corr-MAJOR-1: idle settle with exhausted budget invoked Judge %d times, want 0 (idle must brake on Exhausted)", cp.callCount())
	}
	if after := goalRecordForSessionOrNil(sid); after != nil {
		t.Fatalf("corr-MAJOR-1: goal must be cleared (budget_exhausted) on the idle brake, still ACTIVE: %q", after.Prompt)
	}
	// idleSettling must NOT be set: the brake returns before the mark, so no
	// wedge can follow a budget-exhausted settle. Keyed by GOAL ID (DD-7(b)) —
	// ADR-086 gives the armed goal a REAL record id, so check THAT key rather
	// than the empty string the retired session-meta fixture left behind.
	if al.goalIsIdleSettling(goalID) {
		t.Fatal("corr-MAJOR-1: idleSettling must not be set when the budget brake fires")
	}
}

// =============== corr-MAJOR-3: concurrent deferred claims → one Judge ======

// TestConcurrentDeferredClaims_OneJudge_corrMAJOR3 is corr-MAJOR-3's D13
// successor (G-1 "exactly once"): under D13 the idle tick can no longer
// race a Judge call at all (FR-095/FR-097 — it only ever re-posts), so the
// race this test now proves is the one D13 actually introduces: TWO turns
// that both produce a `met` claim on the SAME session, each independently
// recording its OWN deferred adjudication work (checkGoalLoopAfterTurn's
// synchronous goalAdjudicationInFlight check cannot see either turn's
// adjudication yet — neither has been DISPATCHED, only recorded, so both
// legitimately pass it), then BOTH dispatched concurrently
// (dispatchDeferredGoalAdjudication, simulating runAgentLoop's own
// goroutines). The verifier-registry's CAS Register (corr-MAJOR-3b,
// unaffected by D13 — pkg/agent/verifier_adjudication.go, outside this
// wave's write-set) is what still enforces "exactly once" here. Run with
// -race so any unsynchronized map access on the registry is caught.
func TestConcurrentDeferredClaims_OneJudge_corrMAJOR3(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	// Install a PlanEngine so goalAdjudicationInFlight/the verifier registry
	// consult the real production CAS Register.
	pe := NewPlanEngine(al, plan.New(t.TempDir()), nil, nil)
	al.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "goal race", 0, time.Now().Add(-1*time.Hour))

	var judgeCalls int32
	// chatFn counts invocations and sleeps briefly to WIDEN the race window —
	// the loser (whichever dispatch reaches runVerifierAdjudication second)
	// is still in-flight when the winner's verifier session is registered,
	// so the CAS Register must reject it.
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		atomic.AddInt32(&judgeCalls, 1)
		time.Sleep(50 * time.Millisecond)
		return &providers.LLMResponse{
			Content: `{"met": false, "criteria": [{"id":"goal-condition","met":false,"reason":"nope"}]}`,
		}, nil
	}}

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	// Two turns, each producing its OWN claim — checkGoalLoopAfterTurn's
	// synchronous in-flight check cannot see either turn's own adjudication
	// yet (nothing has dispatched), so BOTH legitimately record deferred
	// work here — this is the shape D13 introduces.
	r1 := &turnResult{finalContent: "[goal:evidence] first done\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r1)
	r2 := &turnResult{finalContent: "[goal:evidence] second done\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r2)
	if r1.goalDeferredAdjudication == nil || r2.goalDeferredAdjudication == nil {
		t.Fatal("both turns must have recorded deferred adjudication work")
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		al.dispatchDeferredGoalAdjudication(r1.goalDeferredAdjudication)
	}()
	go func() {
		defer wg.Done()
		al.dispatchDeferredGoalAdjudication(r2.goalDeferredAdjudication)
	}()
	wg.Wait()

	if got := atomic.LoadInt32(&judgeCalls); got != 1 {
		t.Fatalf("corr-MAJOR-3: two concurrently dispatched deferred claims invoked the Judge %d times, want exactly 1 (G-1 exactly-once)", got)
	}
}

// ============ bare-claim M3 sibling: persist-failure aborts ================

// TestBareClaim_RoundAdvancePersistFailure_Aborts_M3Sibling proves the
// bare-claim M3 sibling (gate honesty): handleBareGoalClaim's 2nd-bare-claim
// round-advance persist must NOT silently WARN-and-continue (the same defect
// Fix-1's silent-M3 fixed in runGoalAdjudication). On a SetMeta failure the
// round is NOT counted (durable counter stays unchanged), no follow-up steer
// is dispatched, and the failure is surfaced via ERROR + a system transcript.
func TestBareClaim_RoundAdvancePersistFailure_Aborts_M3Sibling(t *testing.T) {
	resetGoalTriggerStateForTest()
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
	clearGoalRecordCriteria(t, sid)
	cp := metJudgeProvider("x") // bare claims never invoke the Judge
	judgeInst.Provider = cp

	// 1st bare claim: free teaching steer (streak 1 < threshold 2, no round).
	r1 := &turnResult{finalContent: "GOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r1)
	if cp.callCount() != 0 {
		t.Fatalf("1st bare claim: Judge calls = %d, want 0", cp.callCount())
	}

	// Rig the store read-only AFTER the 1st claim so the 2nd's round-advance
	// write fails. ADR-086: the round counter persists onto the GOAL record
	// (pkg/goal.Store.Update), so the GOAL entity dir is what must block, not
	// the session dir. Reads (Store.Get) still work; blockSessionDirWrites
	// blocks the atomic temp+rename (new-file creation) for both unprivileged
	// (chmod 0o500) and root (FS_IMMUTABLE_FL) — the latter is what the
	// ci-omnipus worker needs, where chmod alone is a no-op under
	// CAP_DAC_OVERRIDE.
	gstore := goal.NewStore(config.OmnipusHomeDir())
	gid := goalRecordForSession(t, sid).GoalID
	restore := blockSessionDirWrites(t, gstore.Dir())
	t.Cleanup(restore)
	if _, perr := gstore.Update(gid, func(cur *goal.Goal) error {
		cur.LatestReason = "probe"
		return nil
	}); perr == nil {
		t.Fatalf("setup invariant: the goal-record write unexpectedly succeeded on the read-only store; cannot prove the M3 path")
	}

	// 2nd bare claim: persist fails → must abort (round NOT consumed, no follow-up).
	r2 := &turnResult{finalContent: "GOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r2)
	if cp.callCount() != 0 {
		t.Fatalf("2nd bare claim persist-failure: Judge calls = %d, want 0 (bare claims never judge)", cp.callCount())
	}
	// The durable round counter must NOT have advanced. pkg/entity.Store holds
	// no in-memory cache, so a plain Get is already durable truth (disk).
	after, aerr := gstore.Get(gid)
	if aerr != nil {
		t.Fatalf("re-read goal record: %v", aerr)
	}
	if after.Round != 0 {
		t.Fatalf("bare-claim M3: durable goal record Round = %d, want 0 (round must not persist on failure; gate stays honest)", after.Round)
	}
	// No follow-up steer: the abort returns BEFORE appending, so the worker is
	// not re-prompted on a counter the store can't durably advance.
	if len(r2.followUps) != 0 {
		t.Fatalf("bare-claim M3: followUps = %d, want 0 (abort must not re-prompt on a failed persist)", len(r2.followUps))
	}
}

// ==================== GOAL-FR-052 / S-47: trigger state by goal id ========

// TestTriggerStateKeyedByGoalID proves GOAL-FR-052 and S-47: the keeper's
// in-memory trigger state is addressed by the GOAL's own id, not by the id of
// the session the goal happens to be bound to.
//
// Given a goal whose id is not its session's id
// When the keeper's trigger state is consulted
// Then it resolves by goal id and does not require a session.
//
// Why this matters beyond tidiness, in the spec's own framing (goal-entity-
// spec.md §5 row 1 and FR-052): before ADR-086 "session id *is* the goal id"
// was a true statement, so every one of these maps could be keyed either way
// and nobody could tell. A task-owned goal breaks it — its owner is a task,
// its session is minted later by the task run, and a goal in the definition
// phase has no session at all. A map still keyed by session id is then either
// unreachable or, worse, reachable under the WRONG key.
//
// Each subtest below is deliberately a BEHAVIOURAL differentiator rather than
// a read of the map: the marker is first installed under the SESSION id and
// the keeper must act anyway (proving that key is not the one consulted), then
// installed under the GOAL id and the keeper must be suppressed. A test that
// only asserted the second half would pass on a map keyed by anything at all.
//
// Traces to: goal-entity-spec.md line 577 (FR-052), line 892 (S-47), line 1162
// (A-11); adr-084-086-joint-delivery-plan.md line 1747 (R-29).
func TestTriggerStateKeyedByGoalID(t *testing.T) {
	// armForKeying builds one quiet, recorded chat goal and returns everything
	// a subtest needs. gid is a freshly minted uuid and is therefore never
	// equal to sid — the precondition the whole test rests on.
	armForKeying := func(t *testing.T) (al *AgentLoop, sid, gid string, dispatches *goalDispatchRecorder) {
		t.Helper()
		resetGoalTriggerStateForTest()
		withShortIdleWindow(t, 2*time.Second)
		al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, ok := al.GetRegistry().GetAgent("native-agent")
		if !ok {
			t.Fatal("native-agent not registered")
		}
		_, sid = newGoalTestSession(t, al, agentInst.ID)
		gid = armGoalRecord(t, sid, "keying goal", recordedGoalCriteria("keying goal"), 0, time.Now().Add(-1*time.Hour))
		if gid == sid {
			t.Fatal("precondition: the goal id must differ from the session id, or this test proves nothing")
		}
		al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)
		judgeInst.Provider = unmetJudgeProvider("the keeper never adjudicates — JUDGE-FR-095")
		return al, sid, gid, recordGoalDispatches(al)
	}

	t.Run("the waiting_on_user marker is keyed by goal id", func(t *testing.T) {
		al, sid, gid, dispatches := armForKeying(t)

		// Under the SESSION id: not the key the keeper consults.
		al.goalSetWaitingOnUser(sid, true)
		al.goalQuietWindowSettle(time.Now())
		if got := len(dispatches.all()); got != 1 {
			t.Fatalf("GOAL-FR-052: a waiting_on_user marker installed under the SESSION id suppressed the keeper "+
				"(dispatches = %d, want 1) — the map is still session-keyed", got)
		}
		rewindGoalActivity(t, al, al.GetSessionStore(), sid)

		// Under the GOAL id: this is the key.
		al.goalSetWaitingOnUser(gid, true)
		al.goalQuietWindowSettle(time.Now())
		if got := len(dispatches.all()); got != 1 {
			t.Fatalf("GOAL-FR-052: a waiting_on_user marker installed under the GOAL id did not suppress the keeper "+
				"(cumulative dispatches = %d, want still 1)", got)
		}
	})

	t.Run("the blocked marker is keyed by goal id", func(t *testing.T) {
		al, sid, gid, dispatches := armForKeying(t)

		al.goalSetBlocked(sid, true)
		al.goalQuietWindowSettle(time.Now())
		if got := len(dispatches.all()); got != 1 {
			t.Fatalf("GOAL-FR-052: a blocked marker installed under the SESSION id suppressed the keeper "+
				"(dispatches = %d, want 1) — the map is still session-keyed", got)
		}
		rewindGoalActivity(t, al, al.GetSessionStore(), sid)

		al.goalSetBlocked(gid, true)
		al.goalQuietWindowSettle(time.Now())
		if got := len(dispatches.all()); got != 1 {
			t.Fatalf("GOAL-FR-052: a blocked marker installed under the GOAL id did not suppress the keeper "+
				"(cumulative dispatches = %d, want still 1)", got)
		}
	})

	t.Run("the idle re-arm marker is keyed by goal id", func(t *testing.T) {
		al, sid, gid, dispatches := armForKeying(t)

		al.goalMarkIdleSettling(sid, true)
		al.goalQuietWindowSettle(time.Now())
		if got := len(dispatches.all()); got != 1 {
			t.Fatalf("GOAL-FR-052: an idle re-arm marker installed under the SESSION id suppressed the keeper "+
				"(dispatches = %d, want 1) — the map is still session-keyed", got)
		}
		// Remove the probe this subtest installed, so the remaining
		// assertions observe only what the KEEPER itself wrote.
		al.goalMarkIdleSettling(sid, false)

		// The keeper's own dispatch must have installed the marker under the
		// GOAL id, which is the positive half of the same property.
		if !al.goalIsIdleSettling(gid) {
			t.Fatalf("GOAL-FR-052: after acting, the keeper must mark the re-arm under the GOAL id %q", gid)
		}
		if al.goalIsIdleSettling(sid) {
			t.Fatalf("GOAL-FR-052: the keeper must NOT mark the re-arm under the SESSION id %q", sid)
		}
	})

	t.Run("the bare-claim streak is keyed by goal id", func(t *testing.T) {
		al, sid, gid, _ := armForKeying(t)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		store := al.GetSessionStore()

		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
		}
		// A BARE claim (no [goal:evidence]) is the streak's only writer.
		al.checkGoalLoopAfterTurn(context.Background(), agentInst,
			opts, &turnResult{finalContent: "GOAL_STATUS: met"})

		// The production write landed under the GOAL id: the next bump there
		// returns 2, while the session id is an untouched, independent key.
		if got := al.bumpGoalBareClaimStreak(gid); got != 2 {
			t.Fatalf("GOAL-FR-052: the bare-claim streak must be written under the GOAL id; a bump there returned %d, want 2", got)
		}
		if got := al.bumpGoalBareClaimStreak(sid); got != 1 {
			t.Fatalf("GOAL-FR-052: the bare-claim streak must NOT be written under the SESSION id; a bump there returned %d, want 1", got)
		}
	})

	t.Run("the claim-scan watermark is keyed by goal id", func(t *testing.T) {
		al, sid, gid, _ := armForKeying(t)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		store := al.GetSessionStore()

		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
		}
		al.checkGoalLoopAfterTurn(context.Background(), agentInst,
			opts, &turnResult{finalContent: "still working on it"})

		if _, ok := al.goalClaimScanWatermark(gid); !ok {
			t.Fatalf("GOAL-FR-052: the claim-scan watermark must be recorded under the GOAL id %q", gid)
		}
		if _, ok := al.goalClaimScanWatermark(sid); ok {
			t.Fatalf("GOAL-FR-052: the claim-scan watermark must NOT be recorded under the SESSION id %q", sid)
		}
	})

	t.Run("the keeper's routing follows the goal, not the session", func(t *testing.T) {
		// FR-052 names `routing` among the six maps that must be re-keyed off
		// the session. The behaviour that depends on it: after a restart the
		// in-memory entry is gone, and the keeper must still be able to reach
		// the agent by reading the route persisted on the goal's OWN record
		// (GOAL-FR-032/FR-033). This probes routeFor DIRECTLY rather than
		// through a keeper tick, so the result cannot be confounded by
		// whether the tick reached the goal at all.
		probe := func(t *testing.T, al *AgentLoop, sid string) goalRoute {
			t.Helper()
			gs := goalTriggers()
			gs.mu.Lock()
			delete(gs.routing, sid)
			gs.mu.Unlock()
			return goalTriggers().routeFor(sid)
		}

		// Reference side: a CHAT-owned goal's route rehydrates from its
		// record. Without this half, the task assertion below would also
		// "pass" on an engine whose persisted-route fallback was broken for
		// everything.
		alChat, sidChat, _, _ := armForKeying(t)
		if got := probe(t, alChat, sidChat); got.channel != "webchat" || got.chatID != "c1" {
			t.Fatalf("baseline: a chat-owned goal's route must rehydrate from its record; got channel=%q chat_id=%q",
				got.channel, got.chatID)
		}

		// A TASK-owned goal must rehydrate identically — its routing state
		// belongs to the GOAL, and a task-owned goal's session is simply the
		// one its run minted.
		h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))
		persisted := h.record(t)
		if persisted.RouteChannel == "" || persisted.RouteChatID == "" {
			t.Fatalf("precondition: the route must be persisted on the goal record (GOAL-FR-033); "+
				"route_channel = %q, route_chat_id = %q", persisted.RouteChannel, persisted.RouteChatID)
		}
		if got := probe(t, h.al, h.sid); got.channel != persisted.RouteChannel || got.chatID != persisted.RouteChatID {
			t.Fatalf("GOAL-FR-052/FR-032: a TASK-owned goal's route did not rehydrate from its record; "+
				"got channel=%q chat_id=%q, want channel=%q chat_id=%q. Routing state must follow the GOAL, not "+
				"the session: pkg/agent/goal_triggers.go::routeFor's persisted fallback resolves the record via "+
				"GetActiveByOwner(GoalOwnerKindSession, sessionID), which can never find a task-owned record "+
				"(its owner id is the TASK's id, GOAL-FR-002). Without it a task goal's keeper cannot reach the "+
				"agent after a restart.",
				got.channel, got.chatID, persisted.RouteChannel, persisted.RouteChatID)
		}
	})

	t.Run("a definition-phase task goal reaches no keeper driver", func(t *testing.T) {
		// R-29's required oracle. GOAL-FR-052's last sentence asks for the
		// behaviour of trigger state for a goal with no session bound to be
		// specified; A-11 answers it: entries are created lazily at
		// activation, so a definition-phase goal has none — and the keeper
		// drivers must not be reachable for such a goal at all.
		resetGoalTriggerStateForTest()
		withShortIdleWindow(t, 2*time.Second)
		al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		if _, ok := al.GetRegistry().GetAgent("native-agent"); !ok {
			t.Fatal("native-agent not registered")
		}
		judgeInst.Provider = unmetJudgeProvider("nothing may adjudicate a goal that has not started")
		dispatches := recordGoalDispatches(al)

		// No session: the goal sits in the DEFINITION phase, exactly as
		// GOAL-FR-009 requires before its task starts. Its activity clock is
		// deliberately an hour in the past, so the quiet window IS elapsed and
		// the test cannot pass merely because nothing looked old enough yet.
		gid := armTaskGoalRecord(t, "task-unstarted", "", "an unstarted task's goal",
			recordedGoalCriteria("an unstarted task's goal"), 0, time.Now().Add(-1*time.Hour))

		al.goalQuietWindowSettle(time.Now())

		if got := len(dispatches.all()); got != 0 {
			t.Fatalf("R-29/A-11: the keeper acted on a DEFINITION-phase goal (%d dispatches: %q) — a goal that has "+
				"not started must not be reachable by any keeper driver", got, dispatches.contents())
		}
		rec := mustGoalRecord(t, gid)
		if rec.State != generated.GoalStateDefining {
			t.Fatalf("R-29: the keeper changed a definition-phase goal's state to %q", rec.State)
		}
		if rec.ZeroOutputPushes != 0 {
			t.Fatalf("R-29: the keeper advanced a definition-phase goal's push counter to %d, want 0", rec.ZeroOutputPushes)
		}
		if al.goalIsIdleSettling(gid) {
			t.Fatal("R-29/A-11: trigger-state entries are created lazily AT ACTIVATION; a definition-phase goal " +
				"must have none")
		}
		if _, ok := al.goalClaimScanWatermark(gid); ok {
			t.Fatal("R-29/A-11: a definition-phase goal must carry no claim-scan watermark")
		}
	})
}
