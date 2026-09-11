// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_keeper_task_session_reach_test.go closes the second, INDEPENDENT cause
// of "a task goal is never watched at all" (DD-* keeper defect, wave E14).
//
// The first cause was routing (the keeper had no channel to reach the agent
// on) and is fixed elsewhere. This file covers the other one: the SESSION
// STORE the keeper reads.
//
//   - A real task run mints its session through
//     `pkg/agent/task_executor.go::createTaskSessionSync`, which calls
//     `al.GetAgentStore(t.AgentID)` — the PER-AGENT UnifiedStore rooted at
//     `<agent home>/sessions`.
//   - `pkg/agent/goal_triggers.go::goalQuietWindowSettle` resolves every
//     active goal record's `ActiveSessionID` through `al.GetSessionStore()`
//     — the SHARED UnifiedStore rooted at `$OMNIPUS_HOME/sessions`, and
//     `continue`s past any record whose `GetMeta` misses.
//
// Two different directories on disk, so the meta read misses and the goal is
// skipped before a single keeper precondition is ever evaluated. GOAL-FR-015
// ("both drivers MUST apply to both owner kinds") and ADR-086's headline
// promise — a chat goal and a task goal behave IDENTICALLY — both fail there.
//
// WHY THE EXISTING TASK-KEEPER SUITE DOES NOT COVER THIS (false green worth
// recording): `goal_triggers_task_test.go`'s `newTaskGoalTestSession` arranges
// its "task run's session" by calling `al.GetSessionStore().NewSession(...)`
// by hand — i.e. it mints the session in the store the keeper happens to
// read, never through the production minting path. Every test in that file,
// `TestKeeperSweepsTaskOwnedGoals` included, therefore passes on a build where
// no real task session is reachable by the keeper at all. The session type and
// the owner kind are real in that suite; the session's LOCATION is not.
//
// The oracle below is deliberately NOT "the session is filed in store X" — a
// location assertion would pass while the keeper still ignored the goal, and
// it would also hard-code one of the two legitimate fix shapes (move the
// mint / resolve the store per session). The oracle is the keeper's own
// observable OUTPUT: the bounded continue-push it dispatches (JUDGE-FR-097)
// for a quiet, recorded, task-owned goal, captured off the async notifier.
//
// THE FIX SHAPE THIS TEST EXPECTS (verified against this same fixture before
// the test was committed): goalQuietWindowSettle must resolve EACH record's
// session through `al.ResolveSessionStore(rec.ActiveSessionID)` (loop.go —
// shared store first, then a scan of the per-agent stores) instead of the
// single `al.GetSessionStore()`, and pass that store to maybeSettleGoalIdle.
// Chat goals resolve to the shared store exactly as today, so chat behaviour
// is unchanged; task sessions become reachable.
//
// The opposite fix — minting the task session in the SHARED store — was
// examined and rejected: five production readers resolve a TASK session
// exclusively through al.GetAgentStore(agentID) (behavior_scan.go's
// runBehaviorScan, judge_evidence_tiers.go's resolveBehaviorScanEntries,
// pkg/gateway/rest_tasks.go's task-verdicts handler, boot_sweep.go's
// reconcileUnifiedMetaStatus, plus task_executor.go's own ten call sites), so
// moving the mint would blind the Judge to the very work ADR-084 requires it
// to read.
package agent

import (
	"strings"
	"testing"
	"time"
)

// TestKeeperReachesSessionMintedByARealTaskRun is the end-to-end proof: start
// a task the way production starts one, let its goal go quiet, tick the
// keeper, and assert the keeper ACTED on it.
//
// Given a task whose goal record was authored in the defining phase
//
//	(rest_tasks.go's syncTaskGoalRecord equivalent — seedDefiningTaskGoal)
//
// When the task is dispatched (createTaskSessionSync mints its session and
//
//	activateTaskGoal binds the record to it) and the goal then goes quiet
//	for longer than the quiet window
//
// Then the engine-tick keeper dispatches exactly one bounded continue-push
//
//	to that task run's own session — identical to the chat-goal behaviour.
//
// Traces to: GOAL-FR-015, goal-entity-spec.md S-06, JUDGE-FR-097, ADR-086.
func TestKeeperReachesSessionMintedByARealTaskRun(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}

	// Arrange exactly as production does: a task with a defining-phase goal
	// record, then the REAL dispatch-path session mint. Nothing here reaches
	// into a session store by hand.
	tk, _ := seedDefiningTaskGoal(t, al, "t-keeper-reach-1", agentInst.ID)
	sid, err := al.taskExecutor.createTaskSessionSync(tk)
	if err != nil {
		t.Fatalf("createTaskSessionSync: %v", err)
	}
	if sid == "" {
		t.Fatal("createTaskSessionSync returned no session id — the task run has no session at all")
	}
	rec := goalRecordForSession(t, sid)
	gid := rec.GoalID

	// The goal has been quiet for an hour: the state S-06 describes as "a
	// running task whose goal has been quiet longer than the quiet window".
	rewindGoalActivityTimeOnly(t, nil, sid)

	// Routing is the OTHER, already-fixed cause of the same symptom; record it
	// explicitly so this test can only ever fail for the store reason.
	al.recordGoalRouting(sid, gid, "webchat", "task:"+tk.ID, "", agentInst.ID)

	// An UNMET canned verdict: if some implementation wrongly adjudicated at
	// idle (JUDGE-FR-095 retires that path) it must not accidentally satisfy
	// the "still active" reading by completing the goal.
	judgeInst.Provider = unmetJudgeProvider("the keeper must never adjudicate — JUDGE-FR-095")
	dispatch := recordGoalDispatches(al)

	al.goalQuietWindowSettle(time.Now())

	evts := dispatch.all()
	if len(evts) != 1 {
		sharedReach := "yes"
		if shared := al.GetSessionStore(); shared == nil {
			sharedReach = "no shared store at all"
		} else if _, merr := shared.GetMeta(sid); merr != nil {
			sharedReach = "NO — " + merr.Error()
		}
		t.Fatalf("GOAL-FR-015/ADR-086: the keeper dispatched %d follow-ups for the quiet goal of a REAL task run, want exactly 1.\n"+
			"The task's session was minted by task_executor.go::createTaskSessionSync; goal_triggers.go::goalQuietWindowSettle\n"+
			"must be able to read that session's meta to evaluate the goal at all.\n"+
			"  session id                            : %s\n"+
			"  readable from al.GetSessionStore()    : %s\n"+
			"  readable from al.GetAgentStore(agent) : %s\n"+
			"  dispatched contents                   : %q\n"+
			"FIX: resolve each record's store with al.ResolveSessionStore(rec.ActiveSessionID) inside\n"+
			"goalQuietWindowSettle, and pass it to maybeSettleGoalIdle — see this file's header.",
			len(evts), sid, sharedReach, agentStoreReach(t, al, agentInst.ID, sid), dispatch.contents())
	}

	got := evts[0]
	if want := goalContinuePushPrompt(rec.Prompt); got.Content != want {
		t.Fatalf("keeper dispatched %q, want the bounded continue-push %q", got.Content, want)
	}
	if got.SenderCanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("keeper dispatch sender = %q, want %q — checkGoalLoopAfterTurn's origin gate drops anything else",
			got.SenderCanonicalID, goalLoopFollowUpSenderID)
	}
	if got.TranscriptSessionID != sid {
		t.Fatalf("keeper dispatch session = %q, want the task run's own session %q", got.TranscriptSessionID, sid)
	}
	if !strings.Contains(got.Content, goalClaimInstructionLine) {
		t.Fatalf("keeper dispatch must carry D-A's claim instruction line; content = %q", got.Content)
	}

	// Read the counters BACK from pkg/goal's store (C-09): the push was
	// counted, and no round was burned (the keeper never adjudicates).
	after := mustGoalRecord(t, gid)
	if after.ZeroOutputPushes != 1 {
		t.Fatalf("goal record ZeroOutputPushes = %d, want 1 — the keeper's push must be counted on the record",
			after.ZeroOutputPushes)
	}
	if after.Round != 0 {
		t.Fatalf("goal record Round = %d, want 0 — the keeper pushes, it never adjudicates (JUDGE-FR-095/FR-097)",
			after.Round)
	}
	if judgeInst.Provider.(*fakeJudgeProvider).callCount() != 0 {
		t.Fatalf("the Judge was called %d times from the idle keeper, want 0",
			judgeInst.Provider.(*fakeJudgeProvider).callCount())
	}
}

// agentStoreReach reports whether sid is readable from the per-agent store —
// diagnostic only, used to make the failure message above name the real cause
// instead of leaving a reader to guess.
func agentStoreReach(t *testing.T, al *AgentLoop, agentID, sid string) string {
	t.Helper()
	store := al.GetAgentStore(agentID)
	if store == nil {
		return "no per-agent store"
	}
	if _, err := store.GetMeta(sid); err != nil {
		return "NO — " + err.Error()
	}
	return "yes"
}
