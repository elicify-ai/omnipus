// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_triggers_task_test.go is wave T2's TASK-OWNED keeper suite (joint
// delivery plan §3): the quiet-window idle keeper, the bounded continue-push
// ladder and the six keeper suppressions, all exercised against a goal whose
// owner is a TASK rather than a chat session.
//
// What it proves, and why it exists as its own file: ADR-086 row 2
// (goal-entity-spec.md §5) resolves the adjudication-driver contradiction
// "toward chat" — BOTH drivers apply to BOTH owner kinds. GOAL-FR-015 states
// it in one sentence: *"Both the after-turn claim driver and the
// quiet-window idle keeper MUST apply to both owner kinds … and
// `pkg/agent/goal_triggers.go::goalQuietWindowSettle`'s goal-bearing test
// MUST select task-owned goals."* The operator's own reason, quoted in the
// spec: *"tasks DO get the idle keeper — it is the most important piece."*
// A task with only a claim driver is a task that stops silently and is never
// noticed.
//
// ARRANGE FIDELITY (re-pointed 2026-09-12, keeper defect): the harness now
// mints its task session through task_executor.go::createTaskSessionSync —
// the production path — instead of calling al.GetSessionStore().NewSession
// by hand. Until that re-point every test in this file passed against a
// session filed in the ONE store the keeper happens to read, so none of them
// could fail on the real defect (goalQuietWindowSettle reads only the shared
// store; a real task session lives in the per-agent one). See mintTaskRunGoal
// for the full account.
//
// Every oracle here is stated as an OBSERVABLE: a dispatched follow-up
// captured off the async notifier (content, sender stamp and target session),
// the goal record's own counters read BACK from pkg/goal's store (never a
// cached in-memory value — C-09), and the Judge provider's call count. None
// of them passes against a keeper that does nothing.
//
// Spec precedence applied here, recorded so a reader does not think it was
// missed: goal-entity-spec.md's S-10 says a task past its push bound "runs a
// normal adjudication and consumes a round". That sentence is SUPERSEDED by
// JUDGE-FR-097 (ADR-084 revision 9 D13), which is explicit that the ladder
// "MUST NOT fall through to an adjudication when the budget is spent", and by
// operator decision D-A, which leaves the seven-day idle-expiry sweep as the
// sole terminator. The joint delivery plan's precedence rule 2 gives ADR-084
// the last word on what triggers an adjudication, and its C-49 row rewrites
// FR-019's test into the three phases TestInvisibleProgressTaskIsPushedThenJudged
// implements below.
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- arrange primitives -------------------------------------------------

// mintTaskRunGoal arranges a task-owned goal THROUGH THE PRODUCTION PATH:
// the task record, its defining-phase goal record, and then
// `task_executor.go::createTaskSessionSync` — the function every real
// dispatch funnels through (ExecuteTask calls it; StartTaskNow runs the same
// block inline). createTaskSessionSync is what mints the session, persists
// SessionID on the task, writes the initial prompt entry and calls
// activateTaskGoal to bind the goal record to that session.
//
// WHY THIS REPLACED A HAND-MINTED SESSION, and it is the whole reason this
// helper exists: the previous arrange called
// `al.GetSessionStore().NewSession(...)` under a comment claiming it "mints
// the session a TASK RUN would mint". It did not. A real task session is
// minted in the PER-AGENT store (`al.GetAgentStore(t.AgentID)`, rooted at
// `<agent home>/sessions`); the shared store is a different directory. Since
// `goal_triggers.go::goalQuietWindowSettle` resolves every active goal
// record's session through `al.GetSessionStore()` and skips any record whose
// `GetMeta` misses, a REAL task goal was invisible to the keeper — while
// every test in this file passed, because the arrange had put the session in
// the one store the keeper reads. The suite asserted fidelity it did not
// have. See pkg/agent/goal_keeper_task_session_reach_test.go for the
// end-to-end proof of the defect itself.
//
// WHAT THIS DOES AND DOES NOT REPRODUCE, stated plainly so no future reader
// has to trust a comment again: it runs the real session mint, the real
// SessionID persistence and the real goal activation. It does NOT run
// ExecuteTask's claim/dispatch machinery, the runTask goroutine or any LLM
// turn — the keeper never depends on those, and a suite that started real
// turns could not assert "the Judge was called zero times".
//
// criteria is deliberately a parameter with no default: a task-owned goal
// carries a non-empty criteria ladder by construction (GOAL-FR-047 makes at
// least one acceptance criterion and one definition-of-done item mandatory at
// creation AND at edit, D-C), and TestNudgeLadderUnreachableForTaskGoal
// depends on that being an arrange-time fact rather than a helper default.
//
// Returns the store that OWNS the minted session (not a store chosen by the
// test), the session id and the goal id.
func mintTaskRunGoal(
	t *testing.T, al *AgentLoop, agentID, taskID, condition string,
	criteria []task.AcceptanceCriterion, roundsUsed int, lastActivity time.Time,
) (*session.UnifiedStore, string, string) {
	t.Helper()
	past := lastActivity.UTC()

	ts := GetTaskStore(al)
	if ts == nil {
		t.Fatal("mintTaskRunGoal: no task store wired")
	}
	tk := &task.Task{
		ID: taskID, AgentID: agentID, WorkspaceID: "test-ws",
		Title: condition, Status: task.StatusNext,
	}
	if err := ts.Create(tk); err != nil {
		t.Fatalf("mintTaskRunGoal: create task: %v", err)
	}

	// The defining-phase record rest_tasks.go's syncTaskGoalRecord authors at
	// task creation (GOAL-FR-009): criteria and DoD fixed up front (D-C).
	gs := goal.NewStore(config.OmnipusHomeDir())
	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.TaskExplicit,
		condition, "", criteria, newFloorDoD(), armedGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("mintTaskRunGoal: goal.New: %v", err)
	}
	if cerr := gs.Create(g); cerr != nil {
		t.Fatalf("mintTaskRunGoal: goal Create: %v", cerr)
	}
	if g.State != generated.GoalStateDefining {
		t.Fatalf("mintTaskRunGoal: seeded goal state = %q, want defining", g.State)
	}

	// The production mint. This is the line the old helper faked.
	sid, serr := al.taskExecutor.createTaskSessionSync(tk)
	if serr != nil {
		t.Fatalf("mintTaskRunGoal: createTaskSessionSync: %v", serr)
	}
	if sid == "" {
		t.Fatal("mintTaskRunGoal: createTaskSessionSync minted no session — the task run would have no session at all")
	}
	store := al.GetAgentStore(agentID)
	if store == nil {
		t.Fatalf("mintTaskRunGoal: no session store owns agent %q's sessions", agentID)
	}
	if _, merr := store.GetMeta(sid); merr != nil {
		t.Fatalf("mintTaskRunGoal: the minted session is not readable from the store that owns it: %v", merr)
	}
	rec := goalRecordForSession(t, sid)

	// Arm the counters: quiet for longer than the window, with the round
	// budget the test asked for.
	if _, uerr := gs.Update(rec.GoalID, func(cur *goal.Goal) error {
		cur.Round = roundsUsed
		if serr := cur.SetCriteria(criteria, past); serr != nil {
			return serr
		}
		started := past
		cur.StartedAt = &started
		cur.LastActivityAt = past
		return nil
	}); uerr != nil {
		t.Fatalf("mintTaskRunGoal: arm: %v", uerr)
	}
	return store, sid, rec.GoalID
}

// armTaskGoalRecord creates a task-owned goal RECORD ONLY — no task, no
// session, no dispatch. It is the DEFINING-PHASE arrange (GOAL-FR-009: a
// task's goal sits in the definition phase until its run mints a session),
// and that is the only thing it reproduces.
//
// It takes an sid parameter and will Activate against it, but passing a
// hand-minted session id is exactly the fidelity failure described on
// mintTaskRunGoal above: a session this helper is handed did not come from
// the production mint and may not live where a real task session lives. Use
// mintTaskRunGoal for anything that exercises a running task; use this one
// with sid == "" for the unstarted-task case.
func armTaskGoalRecord(
	t *testing.T, taskID, sid, condition string, criteria []task.AcceptanceCriterion,
	roundsUsed int, lastActivity time.Time,
) string {
	t.Helper()
	past := lastActivity.UTC()
	gs := goal.NewStore(config.OmnipusHomeDir())

	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.TaskExplicit,
		condition, "", criteria, newFloorDoD(), armedGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("armTaskGoalRecord: goal.New: %v", err)
	}
	g.GoalID = newGoalID()
	if cerr := gs.Create(g); cerr != nil {
		t.Fatalf("armTaskGoalRecord: Create: %v", cerr)
	}
	gid := g.GoalID

	if sid != "" {
		if _, uerr := gs.Update(gid, func(cur *goal.Goal) error {
			return cur.Activate(sid, time.Now().UTC())
		}); uerr != nil {
			t.Fatalf("armTaskGoalRecord: Activate: %v", uerr)
		}
	}

	if _, uerr := gs.Update(gid, func(cur *goal.Goal) error {
		cur.Round = roundsUsed
		if serr := cur.SetCriteria(criteria, past); serr != nil {
			return serr
		}
		started := past
		cur.StartedAt = &started
		cur.LastActivityAt = past
		return nil
	}); uerr != nil {
		t.Fatalf("armTaskGoalRecord: arm: %v", uerr)
	}
	return gid
}

// goalDispatchRecorder captures every follow-up turn the keeper dispatches
// through dispatchGoalAsyncFollowUp. This is the POSITIVE half of every
// oracle in this file: "the goal record's push counter moved" alone would
// pass on an implementation that increments a counter and never reaches the
// agent, and "zero Judge calls" alone passes on an implementation that does
// nothing at all (false-green-patterns.md, the guard-with-the-feature-deleted
// pattern). The dispatched event's content, sender stamp and target session
// are what prove a real re-post happened.
type goalDispatchRecorder struct {
	mu     sync.Mutex
	events []AsyncNotifyEvent
}

func (r *goalDispatchRecorder) all() []AsyncNotifyEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AsyncNotifyEvent, len(r.events))
	copy(out, r.events)
	return out
}

func (r *goalDispatchRecorder) contents() []string {
	evts := r.all()
	out := make([]string, 0, len(evts))
	for _, e := range evts {
		out = append(out, e.Content)
	}
	return out
}

func recordGoalDispatches(al *AgentLoop) *goalDispatchRecorder {
	rec := &goalDispatchRecorder{}
	al.asyncNotifier.registerObserver(func(evt AsyncNotifyEvent) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.events = append(rec.events, evt)
	})
	return rec
}

// taskKeeperHarness is the shared arrange for this file: one AgentLoop, one
// task-type session, one ACTIVE task-owned goal record bound to it, routing
// recorded, a counting Judge provider installed and a dispatch recorder
// attached.
type taskKeeperHarness struct {
	al        *AgentLoop
	agentInst *AgentInstance
	judgeInst *AgentInstance
	judge     *fakeJudgeProvider
	store     *session.UnifiedStore
	sid       string
	gid       string
	taskID    string
	dispatch  *goalDispatchRecorder
	armedAt   time.Time
}

// swapJudgeProvider replaces the harness's canned Judge provider and returns
// the new one, for the one test that needs a MET verdict in its last phase
// while still counting calls across every earlier phase with one instance.
func (h *taskKeeperHarness) swapJudgeProvider(cp *fakeJudgeProvider) *fakeJudgeProvider {
	h.judgeInst.Provider = cp
	h.judge = cp
	return cp
}

// newTaskKeeperHarness arms a quiet, ACTIVE, task-owned goal whose last
// activity is an hour in the past — S-06's "a running task whose goal has
// been quiet longer than the quiet window". The session comes from the
// production mint (mintTaskRunGoal), so every assertion below is made against
// a task session filed where a real task run files one.
func newTaskKeeperHarness(t *testing.T, condition string, criteria []task.AcceptanceCriterion) *taskKeeperHarness {
	t.Helper()
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	taskID := "task-keeper-1"
	armedAt := time.Now().Add(-1 * time.Hour)
	store, sid, gid := mintTaskRunGoal(t, al, agentInst.ID, taskID, condition, criteria, 0, armedAt)
	// A task run is dispatched on its own session's chat id, not a chat's.
	al.recordGoalRouting(sid, gid, "webchat", "task:"+taskID, "sk1", agentInst.ID)

	// The Judge must never be reached from the keeper (JUDGE-FR-095/FR-097).
	// An UNMET canned response is used so that an implementation which DID
	// wrongly adjudicate would not accidentally satisfy a later "the goal is
	// still active" assertion by completing the goal.
	cp := unmetJudgeProvider("the keeper must never adjudicate — JUDGE-FR-095 retires the claimless path")
	judgeInst.Provider = cp

	return &taskKeeperHarness{
		al: al, agentInst: agentInst, judgeInst: judgeInst, judge: cp, store: store,
		sid: sid, gid: gid, taskID: taskID,
		dispatch: recordGoalDispatches(al), armedAt: armedAt,
	}
}

// record reads the goal record BACK from pkg/goal's store (C-09's oracle
// shape: never a cached in-memory value), regardless of its state, so an
// assertion about a goal that reached a terminal state still has an
// observable.
func (h *taskKeeperHarness) record(t *testing.T) *goal.Goal {
	t.Helper()
	return mustGoalRecord(t, h.gid)
}

// taskGoalCondition is the goal text every test in this file uses, so a
// dispatched prompt can be matched on the goal statement it must carry
// (E8/S-38: the keeper sources the statement from the DURABLE record, never
// from the transcript).
const taskGoalCondition = "ship the CSV exporter behind a flag"

// continuePushFor is the exact re-post text FR-097 names, rendered from the
// production builder so the assertion cannot drift from the implementation's
// wording while still being derived from the requirement (the prompt builder
// IS the requirement's surface — D-A).
func continuePushFor(condition string) string { return goalContinuePushPrompt(condition) }

// ===================== FR-015 / S-06: the keeper sweeps tasks =============

// TestKeeperSweepsTaskOwnedGoals proves GOAL-FR-015 and S-06: a running task
// whose goal has been quiet longer than the quiet window is ACTED ON by the
// engine-tick keeper — exactly as a chat goal is.
//
// Given a running task whose goal has been quiet longer than the quiet window
// When the engine tick sweeps
// Then the keeper acts on that task's goal.
//
// The action, post-D13, is the bounded continue-push (JUDGE-FR-097): one
// dispatched follow-up turn carrying goalContinuePushPrompt's text, stamped
// with the goal loop's own sender id so checkGoalLoopAfterTurn's origin gate
// accepts it, zero Judge calls and zero rounds consumed.
//
// Traces to: goal-entity-spec.md line 428 (FR-015), line 632 (S-06);
// adr-084-086-joint-delivery-plan.md line 254 (C-24), line 384 (wave T2).
func TestKeeperSweepsTaskOwnedGoals(t *testing.T) {
	h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))

	h.al.goalQuietWindowSettle(time.Now())

	evts := h.dispatch.all()
	if len(evts) != 1 {
		t.Fatalf("GOAL-FR-015/S-06: the keeper dispatched %d follow-ups for a quiet TASK-owned goal, want exactly 1.\n"+
			"The quiet-window keeper MUST sweep task-owned goals, not only chat-owned ones — see\n"+
			"pkg/agent/goal_triggers.go::goalQuietWindowSettle, whose goal-bearing selector must include\n"+
			"owner kind `task` (GOAL-FR-015: \"goalQuietWindowSettle's goal-bearing test MUST select\n"+
			"task-owned goals\"; joint delivery plan C-24). Dispatched contents: %q",
			len(evts), h.dispatch.contents())
	}
	got := evts[0]
	wantContent := continuePushFor(taskGoalCondition)
	if got.Content != wantContent {
		t.Fatalf("GOAL-FR-015: dispatched content = %q, want the bounded continue-push %q", got.Content, wantContent)
	}
	if got.SenderCanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("GOAL-FR-015: dispatched sender = %q, want %q — checkGoalLoopAfterTurn's origin gate drops anything else, "+
			"so a differently-stamped re-post reaches the agent and then advances nothing",
			got.SenderCanonicalID, goalLoopFollowUpSenderID)
	}
	if got.TranscriptSessionID != h.sid {
		t.Fatalf("GOAL-FR-015: dispatched session = %q, want the task run's own session %q", got.TranscriptSessionID, h.sid)
	}

	after := h.record(t)
	if after.ZeroOutputPushes != 1 {
		t.Fatalf("GOAL-FR-015: goal record ZeroOutputPushes = %d, want 1 (read back from pkg/goal's store)", after.ZeroOutputPushes)
	}
	if after.Round != 0 {
		t.Fatalf("JUDGE-FR-097: a keeper re-post must consume NO round; rounds_used = %d, want 0", after.Round)
	}
	if after.State != generated.GoalStateActive {
		t.Fatalf("D-A: a re-posted goal stays ACTIVE; state = %q", after.State)
	}
	if h.judge.callCount() != 0 {
		t.Fatalf("JUDGE-FR-095: the keeper invoked the Judge %d times, want 0 — a claim is the sole adjudication trigger",
			h.judge.callCount())
	}
}

// ================ FR-016 + JUDGE-FR-096: the union suppression matrix =====

// TestKeeperSuppressionsApplyToTasks is the UNION table the joint delivery
// plan's wave T2 row requires: goal-entity-spec.md FR-016's six keeper
// suppressions merged with judge-active-reviewer-spec.md FR-096's eight
// surviving keeper behaviours. They overlap on six rows and are not
// identical — FR-096 adds the two ACTION rows (the recordless nudge ladder
// and the zero-output continue-push), which are not suppressions at all but
// belong in the same table because they are what must still happen once no
// suppression holds.
//
// Every row runs against a TASK-OWNED goal, which is the whole point:
// FR-016's wording is "All six existing keeper suppressions MUST apply
// UNCHANGED to a task-owned goal".
//
// Given a running task with <the row's condition>
// When the engine tick sweeps
// Then the keeper takes <the row's expected action>.
//
// Traces to: goal-entity-spec.md line 429 (FR-016), lines 638/644 (S-07,
// S-08); judge-active-reviewer-spec.md line 2497 (FR-096's eight-row table);
// adr-084-086-joint-delivery-plan.md line 384 (wave T2's "one table-driven
// test whose rows are the union").
func TestKeeperSuppressionsApplyToTasks(t *testing.T) {
	type row struct {
		name string
		// matrix records which specification row(s) this case covers, so a
		// reader can check the union is complete without re-deriving it.
		matrix string
		// arrange installs the row's precondition. It runs after the goal is
		// armed and before the sweep. It returns the closure that REMOVES that
		// precondition again, or nil when the row has no suppression to lift.
		//
		// The lift is what keeps a suppression row honest. "The keeper
		// dispatched nothing" is satisfied just as well by a keeper that never
		// reached this goal at all, so every suppression row runs a second
		// sweep with the suppression removed and requires the keeper to act —
		// the differentiation half without which four of these rows would be
		// vacuous green (false-green-patterns.md).
		arrange func(t *testing.T, h *taskKeeperHarness) (lift func())
		// wantDispatches is how many follow-up turns the keeper must dispatch.
		wantDispatches int
		// wantPushes is the goal record's ZeroOutputPushes after the sweep.
		wantPushes int
		// wantContent, when non-empty, must be the dispatched content.
		wantContent string
		// wantStillActive is false only for the token-budget brake, which is
		// the one row whose keeper action is a TERMINAL transition.
		wantStillActive bool
		// wantActivityRearmed asserts the keeper pushed the activity clock
		// forward without acting (the live-turn row's second half, S-07's
		// "and the activity clock is re-armed").
		wantActivityRearmed bool
	}

	rows := []row{
		{
			name:   "parked question card suppresses the keeper",
			matrix: "GOAL-FR-016 #1 / JUDGE-FR-096 #1 (goalHasParkedCard) / S-08",
			arrange: func(_ *testing.T, h *taskKeeperHarness) func() {
				reg := &fakeParkedCardRegistry{pending: map[string]bool{h.sid: true}}
				h.al.SetAskUserRegistry(reg)
				return func() { reg.pending[h.sid] = false }
			},
			wantDispatches: 0, wantPushes: 0, wantStillActive: true,
		},
		{
			name:   "waiting_on_user marker suppresses the keeper",
			matrix: "GOAL-FR-016 #2 / JUDGE-FR-096 #2 (goalIsWaitingOnUser)",
			arrange: func(_ *testing.T, h *taskKeeperHarness) func() {
				h.al.goalSetWaitingOnUser(h.gid, true)
				return func() { h.al.goalSetWaitingOnUser(h.gid, false) }
			},
			wantDispatches: 0, wantPushes: 0, wantStillActive: true,
		},
		{
			name:   "the fire-once re-arm marker suppresses a second action in one quiet spell",
			matrix: "GOAL-FR-016 #3 / JUDGE-FR-096 #3 (goalIsIdleSettling / markGoalIdleFired) / S-12",
			arrange: func(_ *testing.T, h *taskKeeperHarness) func() {
				h.al.goalMarkIdleSettling(h.gid, true)
				return func() { h.al.goalMarkIdleSettling(h.gid, false) }
			},
			wantDispatches: 0, wantPushes: 0, wantStillActive: true,
		},
		{
			name:   "an in-flight adjudication suppresses the keeper",
			matrix: "GOAL-FR-016 #4 / JUDGE-FR-096 #4 (goalAdjudicationInFlight)",
			arrange: func(t *testing.T, h *taskKeeperHarness) func() {
				pe := NewPlanEngine(h.al, plan.New(t.TempDir()), nil, nil)
				h.al.SetPlanEngine(pe)
				t.Cleanup(pe.Stop)
				pe.VerifierRegistry().Register(verifierUnitForGoal(h.sid), "fake-verifier-session")
				return func() { pe.VerifierRegistry().Unregister(verifierUnitForGoal(h.sid)) }
			},
			wantDispatches: 0, wantPushes: 0, wantStillActive: true,
		},
		{
			name:   "a live turn suppresses the keeper and re-arms the activity clock",
			matrix: "GOAL-FR-016 #5 / JUDGE-FR-096 #5 (goalHasLiveTurn) / S-07",
			arrange: func(t *testing.T, h *taskKeeperHarness) func() {
				ts := &turnState{
					turnID:              "turn-task-live",
					transcriptSessionID: h.sid,
					routingSessionID:    session.RoutingSessionID(h.sid),
					finishedChan:        make(chan struct{}),
				}
				h.al.activeTurnStates.Store(h.sid, ts)
				t.Cleanup(func() { h.al.activeTurnStates.Delete(h.sid) })
				return func() { h.al.activeTurnStates.Delete(h.sid) }
			},
			wantDispatches: 0, wantPushes: 0, wantStillActive: true, wantActivityRearmed: true,
		},
		{
			name:   "an exhausted token budget brakes the goal instead of pushing it",
			matrix: "GOAL-FR-016 #6 / JUDGE-FR-096 #6 (TokenBudget().Exhausted())",
			// No lift: this row's keeper action IS the terminal brake, which
			// is a state change and therefore already non-vacuous.
			arrange: func(t *testing.T, h *taskKeeperHarness) func() {
				h.al.tokenBudget = NewTokenBudget(100, nil)
				h.al.tokenBudget.Debit(100)
				if !h.al.TokenBudget().Exhausted() {
					t.Fatal("setup invariant: the token budget must be exhausted")
				}
				return nil
			},
			wantDispatches: 0, wantPushes: 0, wantStillActive: false,
		},
		{
			name:   "the recordless nudge ladder is not entered — a task goal always has criteria",
			matrix: "JUDGE-FR-096 #7 (settleRecordlessGoal / goalNudgePrompt) / GOAL-FR-020 / S-11",
			// No suppression: the point is which BRANCH the keeper takes.
			arrange:        func(_ *testing.T, _ *taskKeeperHarness) func() { return nil },
			wantDispatches: 1, wantPushes: 1, wantContent: continuePushFor(taskGoalCondition), wantStillActive: true,
		},
		{
			name:           "with nothing suppressing it the keeper dispatches the bounded continue-push",
			matrix:         "JUDGE-FR-096 #8 (settleZeroOutputRecordedGoal / goalContinuePushPrompt) / GOAL-FR-017",
			arrange:        func(_ *testing.T, _ *taskKeeperHarness) func() { return nil },
			wantDispatches: 1, wantPushes: 1, wantContent: continuePushFor(taskGoalCondition), wantStillActive: true,
		},
	}

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))
			lift := tc.arrange(t, h)

			now := time.Now()
			h.al.goalQuietWindowSettle(now)

			evts := h.dispatch.all()
			if len(evts) != tc.wantDispatches {
				t.Fatalf("[%s] keeper dispatches = %d, want %d. Contents: %q",
					tc.matrix, len(evts), tc.wantDispatches, h.dispatch.contents())
			}
			if tc.wantContent != "" {
				if evts[0].Content != tc.wantContent {
					t.Fatalf("[%s] dispatched content = %q, want %q", tc.matrix, evts[0].Content, tc.wantContent)
				}
				if strings.Contains(evts[0].Content, "Call set_goal now") {
					t.Fatalf("[%s] GOAL-FR-020/S-11: the recordless nudge ladder must be UNREACHABLE for a task-owned "+
						"goal (its criteria are mandatory at creation, GOAL-FR-047); dispatched a nudge instead: %q",
						tc.matrix, evts[0].Content)
				}
			}

			after := h.record(t)
			if after.ZeroOutputPushes != tc.wantPushes {
				t.Fatalf("[%s] goal record ZeroOutputPushes = %d, want %d", tc.matrix, after.ZeroOutputPushes, tc.wantPushes)
			}
			if after.Round != 0 {
				t.Fatalf("[%s] JUDGE-FR-097: no keeper path may consume a round; rounds_used = %d, want 0",
					tc.matrix, after.Round)
			}
			isActive := after.State == generated.GoalStateActive
			if isActive != tc.wantStillActive {
				t.Fatalf("[%s] goal state = %q (active=%v), want active=%v",
					tc.matrix, after.State, isActive, tc.wantStillActive)
			}
			if tc.wantActivityRearmed && !after.LastActivityAt.After(h.armedAt.UTC()) {
				t.Fatalf("[%s] S-07: a suppressing live turn must RE-ARM the activity clock; last_activity_at = %s, "+
					"armed at %s — without the re-arm the keeper re-tests on every tick",
					tc.matrix, after.LastActivityAt, h.armedAt.UTC())
			}
			if h.judge.callCount() != 0 {
				t.Fatalf("[%s] JUDGE-FR-095: the keeper invoked the Judge %d times, want 0",
					tc.matrix, h.judge.callCount())
			}

			// The differentiation half: with the suppression removed and the
			// quiet window elapsed again, the keeper MUST act. Without this,
			// a suppression row passes on an engine whose keeper never reaches
			// a task-owned goal at all.
			if lift == nil {
				return
			}
			lift()
			rewindGoalActivityTimeOnly(t, h.store, h.sid)
			h.al.goalQuietWindowSettle(time.Now())

			lifted := h.dispatch.all()
			if len(lifted) != tc.wantDispatches+1 {
				t.Fatalf("[%s] once the suppression is lifted the keeper MUST act on the task-owned goal; "+
					"cumulative dispatches = %d, want %d. Until this passes, the suppression assertion above is "+
					"vacuous — a keeper that never reaches a task-owned goal satisfies it too (GOAL-FR-015). "+
					"Contents: %q",
					tc.matrix, len(lifted), tc.wantDispatches+1, h.dispatch.contents())
			}
			if got := lifted[len(lifted)-1].Content; got != continuePushFor(taskGoalCondition) {
				t.Fatalf("[%s] post-lift dispatch = %q, want the bounded continue-push", tc.matrix, got)
			}
			if got := h.record(t).Round; got != 0 {
				t.Fatalf("[%s] JUDGE-FR-097: the post-lift action must still consume no round; rounds_used = %d", tc.matrix, got)
			}
		})
	}
}

// =================== FR-017 / S-09, S-10: the push ladder on tasks ========

// TestZeroOutputPushAppliesToTasks proves GOAL-FR-017 and S-09: the bounded
// zero-output continue-push applies to a TASK-owned goal, and it is genuinely
// bounded.
//
// Given a running task producing no transcript output, no scoped diff and no
//
//	tool evidence
//
// When the keeper sweeps twice within the push bound
// Then it dispatches a continue-push each time and consumes no round.
//
// And the third sweep, past the bound: the ladder stops. It does NOT fall
// through to an adjudication (JUDGE-FR-097 is explicit that the
// `if pushes >= max { settleGoalNormally(…) }` fall-through is deleted, not
// merely reached less often) and it does NOT end the goal (D-A: the ladder
// gets no terminator of its own).
//
// Traces to: goal-entity-spec.md line 430 (FR-017), line 650 (S-09), line 656
// (S-10); judge-active-reviewer-spec.md line 2517 (FR-097).
func TestZeroOutputPushAppliesToTasks(t *testing.T) {
	h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))
	wantContent := continuePushFor(taskGoalCondition)

	for cycle := 1; cycle <= goalZeroOutputPushMax; cycle++ {
		h.al.goalQuietWindowSettle(time.Now())

		evts := h.dispatch.all()
		if len(evts) != cycle {
			t.Fatalf("GOAL-FR-017/S-09 cycle %d: cumulative keeper dispatches = %d, want %d. Contents: %q",
				cycle, len(evts), cycle, h.dispatch.contents())
		}
		if evts[cycle-1].Content != wantContent {
			t.Fatalf("cycle %d: dispatched content = %q, want the bounded continue-push %q",
				cycle, evts[cycle-1].Content, wantContent)
		}
		after := h.record(t)
		if after.ZeroOutputPushes != cycle {
			t.Fatalf("cycle %d: goal record ZeroOutputPushes = %d, want %d", cycle, after.ZeroOutputPushes, cycle)
		}
		if after.Round != 0 {
			t.Fatalf("cycle %d: JUDGE-FR-097: a push consumes NO round; rounds_used = %d, want 0", cycle, after.Round)
		}
		if h.judge.callCount() != 0 {
			t.Fatalf("cycle %d: JUDGE-FR-095: Judge calls = %d, want 0", cycle, h.judge.callCount())
		}
		// Re-arm exactly the way a completed dispatched turn would: push the
		// activity clock back and clear the fire-once marker.
		rewindGoalActivity(t, h.al, h.store, h.sid)
	}

	// Past the bound.
	h.al.goalQuietWindowSettle(time.Now())

	if got := len(h.dispatch.all()); got != goalZeroOutputPushMax {
		t.Fatalf("S-10/JUDGE-FR-097: past the push bound the ladder must stop; cumulative dispatches = %d, want %d. Contents: %q",
			got, goalZeroOutputPushMax, h.dispatch.contents())
	}
	after := h.record(t)
	if after.ZeroOutputPushes != goalZeroOutputPushMax {
		t.Fatalf("past the bound: ZeroOutputPushes = %d, want it capped at %d", after.ZeroOutputPushes, goalZeroOutputPushMax)
	}
	if after.Round != 0 {
		t.Fatalf("JUDGE-FR-097: past the push bound the ladder MUST NOT fall through to an adjudication; "+
			"rounds_used = %d, want 0 (the `if pushes >= max { settleGoalNormally(…) }` fall-through is deleted)", after.Round)
	}
	if h.judge.callCount() != 0 {
		t.Fatalf("JUDGE-FR-095/FR-097: past the push bound the Judge was invoked %d times, want 0", h.judge.callCount())
	}
	if after.State != generated.GoalStateActive {
		t.Fatalf("D-A: past the push bound the goal stays ACTIVE (the seven-day idle-expiry sweep is the sole "+
			"terminator); state = %q", after.State)
	}
}

// ==================== FR-018 / S-12: one action per quiet spell ===========

// TestQuietWindowReArmAppliesToTasks proves GOAL-FR-018 and S-12: the
// quiet-window re-arm marker applies to a task-owned goal, so exactly one
// keeper action fires per quiet spell and it re-arms only on genuine external
// activity.
//
// Given a quiet task goal that has already had one keeper action
// When the next tick arrives with no new external activity
// Then no second action fires.
//
// The second sweep deliberately pushes the activity clock back into the past
// WITHOUT clearing the fire-once marker (rewindGoalActivityTimeOnly). That
// isolates the marker as the thing doing the work: if the implementation
// relied on the activity clock alone, this sweep would fire a second action
// and the test would catch it.
//
// Traces to: goal-entity-spec.md line 431 (FR-018), line 668 (S-12).
func TestQuietWindowReArmAppliesToTasks(t *testing.T) {
	h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))

	h.al.goalQuietWindowSettle(time.Now())
	if got := len(h.dispatch.all()); got != 1 {
		t.Fatalf("GOAL-FR-018: first sweep dispatched %d follow-ups, want 1. Contents: %q", got, h.dispatch.contents())
	}
	if !h.al.goalIsIdleSettling(h.gid) {
		t.Fatalf("GOAL-FR-018: the keeper must set the fire-once re-arm marker under the GOAL id %q before dispatching; "+
			"without it every subsequent tick inside the same quiet spell fires again", h.gid)
	}

	// The quiet window is elapsed again, but nothing external happened — the
	// marker must still hold.
	rewindGoalActivityTimeOnly(t, h.store, h.sid)
	h.al.goalQuietWindowSettle(time.Now())
	if got := len(h.dispatch.all()); got != 1 {
		t.Fatalf("GOAL-FR-018/S-12: a second tick inside the SAME quiet spell dispatched again; cumulative dispatches = %d, "+
			"want still 1. Contents: %q", got, h.dispatch.contents())
	}
	if got := h.record(t).ZeroOutputPushes; got != 1 {
		t.Fatalf("GOAL-FR-018/S-12: ZeroOutputPushes = %d, want still 1 inside one quiet spell", got)
	}

	// Genuine external activity: a dispatched turn completed and cleared the
	// marker (bumpGoalActivityOnTurn's job on the real path).
	rewindGoalActivity(t, h.al, h.store, h.sid)
	h.al.goalQuietWindowSettle(time.Now())
	if got := len(h.dispatch.all()); got != 2 {
		t.Fatalf("GOAL-FR-018: after genuine activity re-armed the spell the keeper must act again; "+
			"cumulative dispatches = %d, want 2. Contents: %q", got, h.dispatch.contents())
	}
	if h.judge.callCount() != 0 {
		t.Fatalf("JUDGE-FR-095: Judge calls = %d, want 0 across the whole re-arm sequence", h.judge.callCount())
	}
}

// ============ FR-019 / S-10: invisible progress, pushed then judged =======

// TestInvisibleProgressTaskIsPushedThenJudged proves GOAL-FR-019 in the shape
// the joint delivery plan's C-49 row decides, because the goal spec's own
// wording ("pushed … and MUST then be judged normally") and JUDGE-FR-095
// ("a `met` claim MUST be the sole trigger for an adjudication") cannot both
// be implemented literally. ADR-084 wins on what triggers an adjudication, so
// the test becomes three phases in ONE function:
//
//  1. the task-owned goal goes quiet and receives the push, with zero Judge
//     calls and zero rounds consumed;
//  2. the pushed worker emits a completion claim;
//  3. that claim triggers exactly one adjudication.
//
// The positive assertion (the re-post was actually dispatched and correctly
// stamped) sits in the same function as the zero-Judge-calls negative, so an
// implementation that does nothing cannot pass phase 1 and then coast.
// Counts are asserted, never elapsed wall-clock.
//
// Traces to: goal-entity-spec.md line 432 (FR-019), line 656 (S-10);
// adr-084-086-joint-delivery-plan.md line 279 (C-49).
func TestInvisibleProgressTaskIsPushedThenJudged(t *testing.T) {
	h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))
	// Phase 3 needs a MET verdict, so swap in the met provider before phase 1
	// and count across all three phases with one instance.
	cp := h.swapJudgeProvider(metJudgeProvider("the exporter is shipped"))

	// --- Phase 1: a task producing nothing observable is PUSHED, not judged.
	h.al.goalQuietWindowSettle(time.Now())

	evts := h.dispatch.all()
	if len(evts) != 1 {
		t.Fatalf("GOAL-FR-019 phase 1: a silent task must receive the bounded push; dispatches = %d, want 1. Contents: %q",
			len(evts), h.dispatch.contents())
	}
	if evts[0].Content != continuePushFor(taskGoalCondition) {
		t.Fatalf("phase 1: dispatched content = %q, want the continue-push", evts[0].Content)
	}
	if evts[0].SenderCanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("phase 1: dispatched sender = %q, want %q", evts[0].SenderCanonicalID, goalLoopFollowUpSenderID)
	}
	if cp.callCount() != 0 {
		t.Fatalf("phase 1: JUDGE-FR-095: Judge calls = %d, want 0 — the push happens BEFORE any round is spent", cp.callCount())
	}
	if got := h.record(t).Round; got != 0 {
		t.Fatalf("phase 1: rounds_used = %d, want 0 — the push must precede any round being spent", got)
	}

	// --- Phase 2: the pushed worker answers with a completion claim.
	opts := processOptions{
		TranscriptStore: h.store, TranscriptSessionID: h.sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1",
		IsTaskRun: true,
	}
	result := &turnResult{finalContent: "[goal:evidence] exporter merged behind FLAG_CSV\nGOAL_STATUS: met"}
	h.al.checkGoalLoopAfterTurn(context.Background(), h.agentInst, opts, result)

	if result.goalDeferredAdjudication == nil {
		t.Fatal("GOAL-FR-019 phase 2: a met+evidence claim on a TASK run must record deferred adjudication work " +
			"(JUDGE-FR-098) — the unified claim driver must serve a task-owned goal exactly as it serves a chat goal " +
			"(GOAL-FR-013/FR-015)")
	}
	if cp.callCount() != 0 {
		t.Fatalf("phase 2: JUDGE-FR-098: the Judge must not run inside checkGoalLoopAfterTurn; calls = %d, want 0", cp.callCount())
	}

	// --- Phase 3: the claim triggers EXACTLY ONE adjudication.
	h.al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)

	if cp.callCount() != 1 {
		t.Fatalf("GOAL-FR-019 phase 3: the claim must trigger EXACTLY ONE adjudication; Judge calls = %d, want 1",
			cp.callCount())
	}
	final := h.record(t)
	if final.State != generated.GoalStateMet {
		t.Fatalf("phase 3: a met verdict must move the task-owned goal to the `met` terminal state; state = %q", final.State)
	}
	// GOAL-FR-027: the terminal transition is a STATUS CHANGE on a RETAINED
	// record, never field-zeroing erasure — the criteria that were judged must
	// still be there afterwards.
	if len(final.Criteria) == 0 {
		t.Fatal("phase 3: GOAL-FR-027: a terminal goal keeps its record, including the criteria it was judged against")
	}
	// Deliberately NOT asserted here: the value of the record's round counter
	// after a MET verdict. runGoalAdjudication's met branch terminates the goal
	// before writing the round back, so a met outcome records zero rounds —
	// observed, and left to wave T3, which owns attempt-and-round accounting
	// (joint delivery plan §3, wave T3). This test's obligation under C-49 is
	// "the claim triggers EXACTLY ONE adjudication", asserted on the Judge call
	// count above.
}

// ==================== FR-020 / S-11: the ladder is unreachable ============

// TestNudgeLadderUnreachableForTaskGoal proves GOAL-FR-020 and S-11: the
// recordless nudge ladder remains ONE code path serving both owner kinds, and
// for a task-owned goal it is unreachable BY CONSTRUCTION — GOAL-FR-047 makes
// at least one acceptance criterion mandatory before the task can exist, so
// the record is never empty and `len(rec.Criteria) == 0` is never true.
//
// Given a task created under the mandatory-criteria rule
// When its goal runs to any keeper action
// Then the recordless nudge ladder is never entered.
//
// The second subtest is the differentiation half, and it is what makes the
// first subtest mean something: the SAME driver, given a RECORDLESS chat goal,
// does enter the ladder. Without it, "no nudge was dispatched" would pass just
// as well on a keeper that dispatches nothing at all.
//
// Traces to: goal-entity-spec.md line 433 (FR-020), line 662 (S-11), line 331
// (row 3's "unreachable by construction is stronger than branched-around").
func TestNudgeLadderUnreachableForTaskGoal(t *testing.T) {
	t.Run("a task-owned goal never enters the nudge ladder", func(t *testing.T) {
		h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))

		// Run well past the ladder's own bound: were the ladder reachable,
		// nudge 1 and 2 would have fired and cycle 3 would have run the
		// engine fallback compile.
		for cycle := 0; cycle < goalZeroOutputPushMax+2; cycle++ {
			h.al.goalQuietWindowSettle(time.Now())
			rewindGoalActivity(t, h.al, h.store, h.sid)
		}

		for i, content := range h.dispatch.contents() {
			if strings.Contains(content, "Call set_goal now") {
				t.Fatalf("GOAL-FR-020/S-11: dispatch %d entered the recordless nudge ladder for a TASK-owned goal: %q",
					i, content)
			}
			if content != continuePushFor(taskGoalCondition) {
				t.Fatalf("GOAL-FR-020/S-11: dispatch %d = %q, want the continue-push — a task-owned goal's only "+
					"keeper action is the bounded re-post", i, content)
			}
		}
		if got := len(h.dispatch.contents()); got == 0 {
			t.Fatalf("GOAL-FR-015: the keeper never acted on the task-owned goal at all (%d dispatches) — "+
				"\"the ladder was not entered\" is vacuously true when nothing happens; "+
				"goalQuietWindowSettle must select task-owned goals", got)
		}
		final := h.record(t)
		if len(final.Criteria) == 0 {
			t.Fatal("GOAL-FR-047: a task-owned goal's criteria must never become empty — that is what makes the " +
				"ladder unreachable by construction rather than branched-around")
		}
	})

	t.Run("a recordless chat goal DOES enter the nudge ladder", func(t *testing.T) {
		resetGoalTriggerStateForTest()
		withShortIdleWindow(t, 2*time.Second)
		al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		agentInst, _ := al.GetRegistry().GetAgent("native-agent")
		_, sid := newGoalTestSession(t, al, agentInst.ID)
		gid := armGoalRecord(t, sid, "write the release notes", nil, 0, time.Now().Add(-1*time.Hour))
		al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)
		judgeInst.Provider = unmetJudgeProvider("must not fire")
		rec := recordGoalDispatches(al)

		al.goalQuietWindowSettle(time.Now())

		evts := rec.contents()
		if len(evts) != 1 {
			t.Fatalf("GOAL-FR-020: the ladder is ONE code path and a recordless goal must reach it; dispatches = %d, want 1: %q",
				len(evts), evts)
		}
		if !strings.Contains(evts[0], "Call set_goal now") {
			t.Fatalf("GOAL-FR-020: a recordless goal must receive the registration NUDGE, got %q", evts[0])
		}
	})
}
