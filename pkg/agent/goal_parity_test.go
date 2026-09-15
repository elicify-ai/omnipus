// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_parity_test.go is wave T2's GOAL-FR-014 differential suite and the
// home of operator decision D-A's two required oracles (joint delivery plan
// §3, wave T2; §8's D-A entry).
//
// GOAL-FR-014, in its own words: *"A test MUST exist that fails if any
// post-activation behavioural difference between a chat-owned and a
// task-owned goal appears. It MUST compare observable behaviour, not
// implementation structure."* SC-001 states the same property as a success
// criterion: *"A chat goal and a task goal built from identical text produce
// identical observable loop behaviour after activation."*
//
// The shape that requirement forces, and the reason this file is not just a
// pair of assertions: the test runs ONE fixed script of stimuli against each
// owner kind, records a struct of OBSERVATIONS from each run, and then
// compares the two structs. It never inspects owner kind inside the
// assertions, so a behavioural difference cannot be written into the test's
// own expectations — which is exactly the failure mode a hand-written
// "assert chat does X, assert task does X" pair invites.
//
// The deliberate asymmetries, stated so they are not mistaken for gaps. Both
// are turn PROVENANCE, not goal behaviour, and every observation compared below
// is downstream of them:
//
//   - how a turn is ADMITTED: a chat turn as `UserInitiated`, a task run's own
//     dispatched turn as `IsTaskRun` (goal_loop.go's origin gate, GOAL-FR-015);
//   - which DRIVER resolves a claim (founder decision 2026-09-14, issue #710:
//     one claim mechanism, one Judge pipeline). Both kinds claim through the
//     same goal_claim tool call and are adjudicated by the same Judge
//     verification; a chat claim is picked up by the after-turn hook and
//     judged after the reply is delivered, a task run's claim by the task
//     executor's run loop once the turn has ended.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// parityGoalText is the identical text both goals are built from — SC-001's
// "built from identical text". Using one constant is load-bearing: the
// keeper's dispatched prompt embeds the goal statement, so identical text is
// what makes the dispatched content directly comparable between the runs.
const parityGoalText = "migrate the billing importer to the new schema"

// goalBehaviourObservation is the comparable surface. Every exported field is
// something an operator or an agent could observe — a dispatched turn, a
// counter read back from pkg/goal's store, a terminal state, a Judge call
// count. No exported field records an implementation detail.
type goalBehaviourObservation struct {
	// Phase A — a suppression that must hold identically for both kinds.
	KeeperActedWhileWaitingOnUser bool

	// Phase B — the quiet-window keeper.
	KeeperDispatches      int
	KeeperDispatchContent string
	KeeperDispatchSender  string
	PushesAfterKeeper     int
	RoundsAfterKeeper     int
	JudgeCallsAfterKeeper int

	// Phase C — a met claim made through the real goal_claim tool, and the
	// adjudication it triggers.
	JudgeCallsAfterClaim  int
	TerminalState         string
	RoundsAfterVerdict    int
	CriteriaCountOnRecord int

	// The claim as the goal record keeps it (GOAL-FR-013's one code path for
	// the claim): its status, the worker's own evidence, and whether it was
	// stamped with a time. The time itself differs run to run and is not
	// compared.
	ClaimStatusOnRecord   string
	ClaimEvidenceOnRecord string
	ClaimTimeOnRecord     bool

	// Chat-driver facts (JUDGE-FR-098's deferral), checked on the chat run's
	// baseline and never compared: a task run's claim is resolved after its turn
	// has already ended, so it has no in-turn window to defer out of.
	chatClaimDeferred        bool
	chatJudgeCallsInsideHook int
}

const parityTaskID = "task-parity"

// parityClaimEvidence is the worker's one-line evidence in the scripted met
// claim — what both goal records must keep as that claim's evidence.
const parityClaimEvidence = "importer migrated, backfill verified"

// observeGoalBehaviour runs the fixed script against one owner kind and
// returns what was observed. Each call builds its OWN AgentLoop and its own
// OMNIPUS_HOME (newGoalLoopTestLoop's t.Setenv), so the two runs cannot see
// each other's goal records — a shared store would let one run's active goal
// be swept by the other's keeper tick and quietly corrupt the comparison.
func observeGoalBehaviour(t *testing.T, ownerKind generated.GoalOwnerKind) goalBehaviourObservation {
	t.Helper()
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}

	armedAt := time.Now().Add(-1 * time.Hour)
	var (
		sid, gid string
		store    *session.UnifiedStore
	)
	switch ownerKind {
	case generated.GoalOwnerKindSession:
		store, sid = newGoalTestSession(t, al, agentInst.ID)
		gid = armGoalRecord(t, sid, parityGoalText, recordedGoalCriteria(parityGoalText), 0, armedAt)
	case generated.GoalOwnerKindTask:
		// The PRODUCTION mint (task_executor.go::createTaskSessionSync), not a
		// hand-made session: a task session is filed in the per-agent store,
		// and arranging it in the shared store instead is what made this
		// parity comparison unable to fail on the keeper's store defect. Each
		// branch now carries the store that actually OWNS its session, which
		// is also what a real turn would use as its TranscriptStore.
		store, sid, gid = mintTaskRunGoal(t, al, agentInst.ID, parityTaskID, parityGoalText,
			recordedGoalCriteria(parityGoalText), 0, armedAt)
	default:
		t.Fatalf("observeGoalBehaviour: unsupported owner kind %q", ownerKind)
	}
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)

	cp := metJudgeProvider("the importer runs against the new schema")
	judgeInst.Provider = cp
	dispatches := recordGoalDispatches(al)

	obs := goalBehaviourObservation{}

	// --- Phase A: the waiting_on_user suppression.
	al.goalSetWaitingOnUser(gid, true)
	al.goalQuietWindowSettle(time.Now())
	obs.KeeperActedWhileWaitingOnUser = len(dispatches.all()) > 0
	al.goalSetWaitingOnUser(gid, false)

	// --- Phase B: the quiet-window keeper.
	al.goalQuietWindowSettle(time.Now())
	evts := dispatches.all()
	obs.KeeperDispatches = len(evts)
	if len(evts) > 0 {
		obs.KeeperDispatchContent = evts[0].Content
		obs.KeeperDispatchSender = evts[0].SenderCanonicalID
	}
	afterKeeper := mustGoalRecord(t, gid)
	obs.PushesAfterKeeper = afterKeeper.ZeroOutputPushes
	obs.RoundsAfterKeeper = afterKeeper.Round
	obs.JudgeCallsAfterKeeper = cp.callCount()

	// --- Phase C: a met claim through the REAL goal_claim tool, resolved by the
	// driver that owns this kind's turns.
	claimTool, ok := agentInst.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatal("goal_claim is not registered on the working agent")
	}
	claimedFrom := time.Now().UTC().Add(-time.Second)
	b6ClaimMetAndPersist(t, claimTool, store, sid, "parity-claim", parityClaimEvidence)
	const reply = "The importer is migrated and the backfill is verified."
	switch ownerKind {
	case generated.GoalOwnerKindSession:
		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
		}
		result := &turnResult{finalContent: reply}
		al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
		obs.chatClaimDeferred = result.goalDeferredAdjudication != nil
		obs.chatJudgeCallsInsideHook = cp.callCount()
		if result.goalDeferredAdjudication != nil {
			al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)
		}
	case generated.GoalOwnerKindTask:
		ts := GetTaskStore(al)
		if _, err := ts.Update(parityTaskID, task.Patch{Status: ptrStatus(task.StatusInProgress)}); err != nil {
			t.Fatalf("observeGoalBehaviour: move the task to in_progress: %v", err)
		}
		running, err := ts.Get(parityTaskID)
		if err != nil {
			t.Fatalf("observeGoalBehaviour: read the running task: %v", err)
		}
		al.taskExecutor.finishRunTurn(context.Background(), running, sid, reply, nil, "", nil,
			&taskRunState{claimWatermark: claimedFrom})
	}
	obs.JudgeCallsAfterClaim = cp.callCount()
	final := mustGoalRecord(t, gid)
	obs.TerminalState = string(final.State)
	obs.RoundsAfterVerdict = final.Round
	obs.CriteriaCountOnRecord = len(final.Criteria)
	if final.LatestClaim != nil {
		obs.ClaimStatusOnRecord = string(final.LatestClaim.Status)
		obs.ClaimEvidenceOnRecord = final.LatestClaim.Evidence
		obs.ClaimTimeOnRecord = !final.LatestClaim.ClaimedAt.IsZero()
	}

	return obs
}

// TestChatAndTaskGoalsBehaveIdentically proves GOAL-FR-014 and SC-001.
//
// Given a chat goal and a task goal built from identical text
// When each is driven through the same script — a suppression, a
//
//	quiet-window keeper tick, and a completion claim made through the real
//	goal_claim tool together with the adjudication that claim triggers
//
// Then every observation is identical.
//
// Traces to: goal-entity-spec.md line 424 (FR-014), line 581 (SC-001),
// line 600 (S-01), line 612 (S-03); adr-084-086-joint-delivery-plan.md
// line 384 (wave T2).
func TestChatAndTaskGoalsBehaveIdentically(t *testing.T) {
	chat := observeGoalBehaviour(t, generated.GoalOwnerKindSession)
	task := observeGoalBehaviour(t, generated.GoalOwnerKindTask)

	// A sanity gate on the CHAT run first. Without it, a total regression that
	// broke BOTH kinds identically would satisfy the comparison below and
	// report parity where there is only shared breakage.
	//
	// This block doubles as JUDGE-FR-097's own oracle, stated in the four
	// parts the judge specification insists on together: *"goalContinuePushPrompt's
	// text is dispatched, stamped goalLoopFollowUpSenderID, with zero Judge
	// calls and no round consumed. All four, because 'no verdict was written'
	// alone passes on an implementation that does nothing and wedges every
	// quiet goal."*
	if chat.KeeperActedWhileWaitingOnUser {
		t.Fatal("baseline (JUDGE-FR-096 #2): the keeper must not act while a waiting_on_user pause holds")
	}
	if chat.KeeperDispatches != 1 {
		t.Fatalf("baseline (JUDGE-FR-097): the chat-owned run must produce exactly 1 keeper dispatch, got %d — "+
			"the differential comparison is meaningless until the reference side works", chat.KeeperDispatches)
	}
	if chat.KeeperDispatchContent != goalContinuePushPrompt(parityGoalText) {
		t.Fatalf("baseline (JUDGE-FR-097): the quiet goal must be RE-POSTED with goalContinuePushPrompt's text; got %q",
			chat.KeeperDispatchContent)
	}
	if chat.KeeperDispatchSender != goalLoopFollowUpSenderID {
		t.Fatalf("baseline (JUDGE-FR-097): the re-post must be stamped %q; got %q",
			goalLoopFollowUpSenderID, chat.KeeperDispatchSender)
	}
	if chat.JudgeCallsAfterKeeper != 0 {
		t.Fatalf("baseline (JUDGE-FR-095/FR-097): a quiet goal is re-posted, NEVER judged; Judge calls = %d, want 0",
			chat.JudgeCallsAfterKeeper)
	}
	if chat.RoundsAfterKeeper != 0 {
		t.Fatalf("baseline (JUDGE-FR-097): the re-post consumes no round; rounds_used = %d, want 0",
			chat.RoundsAfterKeeper)
	}
	if !chat.chatClaimDeferred || chat.chatJudgeCallsInsideHook != 0 {
		t.Fatalf("baseline (JUDGE-FR-098): a chat goal_claim(met) must record deferred work and the Judge must not "+
			"run inside checkGoalLoopAfterTurn; deferred = %v, calls inside the hook = %d",
			chat.chatClaimDeferred, chat.chatJudgeCallsInsideHook)
	}
	if chat.JudgeCallsAfterClaim != 1 || chat.TerminalState != string(generated.GoalStateMet) {
		t.Fatalf("baseline (JUDGE-FR-095): a `met` claim is the SOLE adjudication trigger and must fire exactly once; "+
			"judge calls = %d, state = %q", chat.JudgeCallsAfterClaim, chat.TerminalState)
	}

	type field struct {
		name      string
		chat, tsk any
		why       string
	}
	fields := []field{
		{"KeeperActedWhileWaitingOnUser", chat.KeeperActedWhileWaitingOnUser, task.KeeperActedWhileWaitingOnUser,
			"GOAL-FR-016: all six keeper suppressions apply unchanged to a task-owned goal"},
		{"KeeperDispatches", chat.KeeperDispatches, task.KeeperDispatches,
			"GOAL-FR-015: the quiet-window idle keeper applies to BOTH owner kinds — goalQuietWindowSettle's " +
				"goal-bearing selector must include owner kind `task`"},
		{"KeeperDispatchContent", chat.KeeperDispatchContent, task.KeeperDispatchContent,
			"JUDGE-FR-097: one unified push ladder, one prompt, owner-agnostic"},
		{"KeeperDispatchSender", chat.KeeperDispatchSender, task.KeeperDispatchSender,
			"the re-post must be stamped with the goal loop's own sender id for both kinds, or the origin gate drops it"},
		{"PushesAfterKeeper", chat.PushesAfterKeeper, task.PushesAfterKeeper,
			"GOAL-FR-017: the bounded zero-output continue-push applies to a task-owned goal"},
		{"RoundsAfterKeeper", chat.RoundsAfterKeeper, task.RoundsAfterKeeper,
			"JUDGE-FR-097: a keeper re-post consumes no round, for either kind"},
		{"JudgeCallsAfterKeeper", chat.JudgeCallsAfterKeeper, task.JudgeCallsAfterKeeper,
			"JUDGE-FR-095: the keeper never adjudicates, for either kind"},
		{"JudgeCallsAfterClaim", chat.JudgeCallsAfterClaim, task.JudgeCallsAfterClaim,
			"issue #710: one claim mechanism and one Judge pipeline — a goal_claim(met) triggers exactly one " +
				"adjudication, for either kind"},
		{"TerminalState", chat.TerminalState, task.TerminalState,
			"GOAL-FR-027: a met verdict is a status transition on a retained record, identical for both kinds"},
		{"RoundsAfterVerdict", chat.RoundsAfterVerdict, task.RoundsAfterVerdict,
			"one adjudication consumes exactly one round, for either kind"},
		{"CriteriaCountOnRecord", chat.CriteriaCountOnRecord, task.CriteriaCountOnRecord,
			"GOAL-FR-027: the terminal record keeps its criteria for both kinds"},
		{"ClaimStatusOnRecord", chat.ClaimStatusOnRecord, task.ClaimStatusOnRecord,
			"GOAL-FR-013: one code path for the claim — a goal_claim(met) is recorded on the goal record for either kind"},
		{"ClaimEvidenceOnRecord", chat.ClaimEvidenceOnRecord, task.ClaimEvidenceOnRecord,
			"GOAL-FR-013: the record keeps the worker's own evidence, identically for both kinds"},
		{"ClaimTimeOnRecord", chat.ClaimTimeOnRecord, task.ClaimTimeOnRecord,
			"GOAL-FR-013: the recorded claim is stamped with when it was made, for either kind"},
	}
	for _, f := range fields {
		if f.chat != f.tsk {
			t.Errorf("GOAL-FR-014/SC-001: post-activation behaviour differs between owner kinds.\n"+
				"  observation : %s\n  chat-owned  : %v\n  task-owned  : %v\n  why it matters: %s",
				f.name, f.chat, f.tsk, f.why)
		}
	}

	// Checked AFTER the comparison, so a claim recorded by only one kind is
	// reported above as the parity difference it is. This catches the shared
	// breakage the comparison cannot see: neither kind recording the claim.
	if chat.ClaimStatusOnRecord != string(generated.GoalLatestClaimStatusMet) ||
		chat.ClaimEvidenceOnRecord != parityClaimEvidence || !chat.ClaimTimeOnRecord {
		t.Errorf("baseline (GOAL-FR-013): the chat goal's record must carry the met claim with the worker's evidence %q; "+
			"got status=%q evidence=%q stamped=%v", parityClaimEvidence,
			chat.ClaimStatusOnRecord, chat.ClaimEvidenceOnRecord, chat.ClaimTimeOnRecord)
	}
}

// TestChatGoalWaitingOnUserClaimIsRecordedLikeATaskRun: a chat goal's
// goal_claim(waiting_on_user) is recorded on its goal record — the status, the
// worker's own question and when — exactly as a task run records the same
// claim (task_run_loop_test.go::TestTaskRun_WaitingOnUserEndsFailedWithOneOutcomeLine),
// and the chat goal still parks with no adjudication and no round used. The
// two kinds diverge after the claim by design (a task run has no reply channel
// and ends), which is why this is asserted here and not in the shared script.
//
// Traces to: goal-entity-spec.md FR-013/FR-014; ADR-084's task-run amendment
// ("the chat path records that reason on the goal record too").
func TestChatGoalWaitingOnUserClaimIsRecordedLikeATaskRun(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, parityGoalText, recordedGoalCriteria(parityGoalText), 0, time.Now())
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "unused"}
	judgeInst.Provider = judge

	claimTool, ok := agentInst.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatal("goal_claim is not registered on the working agent")
	}
	const question = "which of the two supplier price lists is current?"
	res := claimTool.Execute(tools.WithTranscriptSessionID(context.Background(), sid),
		map[string]any{"status": tools.GoalClaimStatusWaitingOnUser, "evidence": question})
	if res == nil || res.IsError {
		t.Fatalf("goal_claim(waiting_on_user) was refused on a session with an active goal: %+v", res)
	}
	if err := store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "parity-waiting-claim", Type: session.EntryTypeToolCall, Role: "assistant", Timestamp: time.Now().UTC(),
		ToolCalls: []session.ToolCall{{
			ID: session.ToolCallID("parity-waiting-claim"), Tool: tools.GoalClaimToolName, Status: "success",
			Result: map[string]any{"text": res.ForLLM},
		}},
	}); err != nil {
		t.Fatalf("persist the goal_claim call: %v", err)
	}

	result := &turnResult{finalContent: "I need an answer before I can go on."}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}, result)

	rec := mustGoalRecord(t, gid)
	if rec.LatestClaim == nil || rec.LatestClaim.Status != generated.GoalLatestClaimStatusWaitingOnUser ||
		rec.LatestClaim.Evidence != question || rec.LatestClaim.ClaimedAt.IsZero() {
		t.Errorf("GOAL-FR-013: latest claim on the chat goal's record = %+v, want waiting_on_user carrying the worker's "+
			"question %q, stamped with a time", rec.LatestClaim, question)
	}
	if !al.goalIsWaitingOnUser(gid) {
		t.Error("the chat goal must park on waiting_on_user")
	}
	if rec.State != generated.GoalStateActive || rec.Round != 0 || judge.callCount() != 0 || result.goalDeferredAdjudication != nil {
		t.Errorf("a waiting_on_user claim must not adjudicate or use a round: state=%q round=%d Judge calls=%d deferred=%v",
			rec.State, rec.Round, judge.callCount(), result.goalDeferredAdjudication != nil)
	}
}

// ==================== D-A oracle 1: the prompts name the tool =============

// TestKeeperPromptsInstructTheAgentToClaim is the first of operator decision
// D-A's two required T2 oracles: *"the keeper's re-post text and the nudge
// text must each tell the agent that if it believes the work is done it must
// mark it complete by calling the claim tool."*
//
// D-A's reasoning, recorded because it governs what this test may and may not
// assert: bounding the nudges treats the symptom. The cause is that the agent
// does not know it is supposed to claim. So the fix is in the WORDS, and the
// words are therefore a requirement with a test, not a comment.
//
// Both halves are asserted twice over — once on the builder's output, and once
// on the text that was actually DISPATCHED to the agent. The builder assertion
// alone would pass on an implementation that builds the right string and
// dispatches a different one.
//
// Traces to: adr-084-086-joint-delivery-plan.md line 1201 (D-A: "T2 asserts
// both halves: the prompts name the tool, and the ladder has no terminator of
// its own"), line 378 (wave E13's prompt-text obligation).
func TestKeeperPromptsInstructTheAgentToClaim(t *testing.T) {
	// The tool an agent must call to say the work is done. D-A requires it to
	// be named, not merely alluded to.
	const claimToolName = "goal_claim"

	t.Run("goalContinuePushPrompt names the claim tool", func(t *testing.T) {
		got := goalContinuePushPrompt(parityGoalText)
		if !strings.Contains(got, claimToolName) {
			t.Fatalf("D-A: the keeper re-post must tell the agent to mark the work complete by calling %s; got %q",
				claimToolName, got)
		}
		if !strings.Contains(got, parityGoalText) {
			t.Fatalf("E8/S-38: the re-post must carry the goal statement from the DURABLE record; got %q", got)
		}
	})

	t.Run("goalNudgePrompt names the claim tool", func(t *testing.T) {
		got := goalNudgePrompt(parityGoalText, 1)
		if !strings.Contains(got, claimToolName) {
			t.Fatalf("D-A: the recordless nudge must ALSO tell the agent to mark the work complete by calling %s — "+
				"a recordless goal can already be finished; nothing about the missing record implies the work is not; got %q",
				claimToolName, got)
		}
	})

	t.Run("the dispatched re-post carries the instruction", func(t *testing.T) {
		resetGoalTriggerStateForTest()
		withShortIdleWindow(t, 2*time.Second)
		al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		_, sid := newGoalTestSession(t, al, agentInst.ID)
		gid := armGoalRecord(t, sid, parityGoalText, recordedGoalCriteria(parityGoalText), 0, time.Now().Add(-1*time.Hour))
		al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)
		judgeInst.Provider = unmetJudgeProvider("must not fire")
		dispatches := recordGoalDispatches(al)

		al.goalQuietWindowSettle(time.Now())

		got := dispatches.contents()
		if len(got) != 1 {
			t.Fatalf("expected exactly one dispatched re-post, got %d: %q", len(got), got)
		}
		if !strings.Contains(got[0], claimToolName) {
			t.Fatalf("D-A: the DISPATCHED re-post must name %s; got %q", claimToolName, got[0])
		}
	})

	t.Run("the dispatched nudge carries the instruction", func(t *testing.T) {
		resetGoalTriggerStateForTest()
		withShortIdleWindow(t, 2*time.Second)
		al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		_, sid := newGoalTestSession(t, al, agentInst.ID)
		gid := armGoalRecord(t, sid, parityGoalText, nil, 0, time.Now().Add(-1*time.Hour))
		al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)
		judgeInst.Provider = unmetJudgeProvider("must not fire")
		dispatches := recordGoalDispatches(al)

		al.goalQuietWindowSettle(time.Now())

		got := dispatches.contents()
		if len(got) != 1 {
			t.Fatalf("expected exactly one dispatched nudge, got %d: %q", len(got), got)
		}
		if !strings.Contains(got[0], "Call set_goal now") {
			t.Fatalf("precondition: this must be the recordless NUDGE, got %q", got[0])
		}
		if !strings.Contains(got[0], claimToolName) {
			t.Fatalf("D-A: the DISPATCHED nudge must name %s; got %q", claimToolName, got[0])
		}
	})
}

// ============== D-A oracle 2: the ladder has no terminator ================

// TestNudgeLadderHasNoTerminatorOfItsOwn is the second of D-A's required T2
// oracles: *"a goal that keeps working and never claims stays active until
// the seven-day idle-expiry sweep reaches it, and no bounded push count ends
// it."*
//
// D-A is explicit about the prohibition, not only the behaviour: *"The nudge
// ladder does not get an ending of its own — do not bound it, do not add a
// counter, do not add a stop reason."* So this test drives many more quiet
// cycles than the push budget allows and asserts that nothing about running
// out of pushes ends the goal, consumes a round, or invokes the Judge. Then it
// ages the record past the seven-day brake and asserts that THAT — and by
// elimination only that — is what ends it.
//
// The second half is what makes the first half meaningful. "The goal is still
// active after twelve ticks" would also pass on an engine in which nothing can
// ever end a goal at all; demonstrating the one surviving terminator still
// works rules that out.
//
// Traces to: adr-084-086-joint-delivery-plan.md line 1201 and line 1798 (D-A /
// OQ-A: "Let it sit. Seven days stays, the nudge ladder gets no bound");
// judge-active-reviewer-spec.md line 2517 (FR-097's retained push budget).
func TestNudgeLadderHasNoTerminatorOfItsOwn(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, parityGoalText, recordedGoalCriteria(parityGoalText), 0, time.Now().Add(-1*time.Hour))
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)
	cp := unmetJudgeProvider("no adjudication may ever run on the keeper path")
	judgeInst.Provider = cp

	const cycles = 12 // six times the push budget
	for cycle := 1; cycle <= cycles; cycle++ {
		al.goalQuietWindowSettle(time.Now())
		rewindGoalActivity(t, al, store, sid)

		rec := mustGoalRecord(t, gid)
		if rec.State != generated.GoalStateActive {
			t.Fatalf("D-A: after %d quiet cycles the goal reached state %q (reason %q). The nudge/push ladder must "+
				"have NO terminator of its own — no bounded push count, no counter, no stop reason may end a goal "+
				"whose agent keeps working and never claims; only the seven-day idle-expiry sweep may",
				cycle, rec.State, rec.TerminalReason)
		}
		if rec.Round != 0 {
			t.Fatalf("D-A/JUDGE-FR-097: after %d quiet cycles rounds_used = %d, want 0 — the ladder must never "+
				"fall through to an adjudication when its push budget is spent", cycle, rec.Round)
		}
		if rec.ZeroOutputPushes > goalZeroOutputPushMax {
			t.Fatalf("JUDGE-FR-097: the push budget must still BOUND the pushes themselves; ZeroOutputPushes = %d, "+
				"want at most %d", rec.ZeroOutputPushes, goalZeroOutputPushMax)
		}
	}
	if cp.callCount() != 0 {
		t.Fatalf("JUDGE-FR-095: %d quiet cycles produced %d Judge calls, want 0", cycles, cp.callCount())
	}

	// The one surviving terminator, proven to still work: age the record past
	// the seven-day calendar brake and sweep.
	now := time.Now().UTC()
	expiredAt := now.Add(-time.Duration(config.DefaultIdleExpiryDays) * 24 * time.Hour)
	if _, err := resolveGoalRecordStore().Update(gid, func(cur *goal.Goal) error {
		cur.LastActivityAt = expiredAt
		started := expiredAt
		cur.StartedAt = &started
		return nil
	}); err != nil {
		t.Fatalf("aging the goal record past the idle-expiry brake: %v", err)
	}

	al.goalIdleExpirySweep(config.PlanningConfig{}, now)

	final := mustGoalRecord(t, gid)
	if final.State == generated.GoalStateActive {
		t.Fatalf("D-A: the seven-day idle-expiry sweep is the SOLE terminator for a goal that never claims, and it "+
			"did not end this one; state = %q after %d days idle", final.State, config.DefaultIdleExpiryDays)
	}
}
