package agent

// U1 RED — plan-restart leg (coordinator-approved scope, 2026-10-03; qa-lead
// owns NEW plan-restart test files only — W6's question files and W2's
// same-generation resume files are disjoint).
//
// Authority: sub-agent control-plane ADR @ cd20cf8b
// (omnipus-investigations/a-u1-runtime-contracts-20261002/assets/
// ADR-20260928-sub-agent-control-plane@cd20cf8b.md):
//   - D8.10 (MAJ-008 / F0929-R2-Q3=A): an explicit plan restart (POST
//     /plans/{id}/restart; the engine-level entry is
//     plan_engine_play.go::PlayPlan — the same failed(stopped_by_user)->
//     approved gate the REST handler uses) preserves done members but MUST
//     retain each non-done step's Task.SessionID, conversation, task-owned
//     goal (ID, active state, original session binding) and direct-parent
//     edge. Readmission then RESUMEs the SAME stopped member session and
//     history — never a replacement session; only a step that never had a
//     session may create one; an already-working step is not dispatched a
//     second time; restart and member admission emit NO NEW stop notice
//     unless that session stops again (D6 notices stay in the inbox as
//     history); boot alone never restarts a plan or resumes a member (D8.5).
//   - T17: stop → process restart → task-goal store inspection → explicit
//     restart → crash before one member dispatch; both original task-goal
//     IDs remain active across Stop/boot/restart; both original
//     Task.SessionIDs / transcript histories are reused; the done step
//     remains untouched; no duplicate dispatch.
//   - MIN-006: Plan/Task keep the failed(stopped_by_user) reason pairs
//     across the stop and across the boot.
//   - MAJ-003: the task-owned goal is preserved separately from any
//     session-owned goal; no stop and no member readmission clears or
//     replaces it.
//   - ADR-093 D6: a member whose steering edge was dropped at task start
//     (SteeredBy == nil, ordinary root) has NO D6 notice duty — but the
//     coordinator's dispatch for this pack (founded on F0929-R2-Q3=A) still
//     requires it stopped with its history preserved across the restart.
//
// Oracle note (elicify-test-writing): every expected value below is fixed by
// the FIXTURE — session ids minted by the test's own session-store calls,
// goal ids returned by the pack's u1ActivateTaskGoal, transcript marker
// strings authored here — or quoted from the ADR text, never from observed
// production output. The known disagreement this pack pins: today
// pkg/task/store.go::RestartReset (called by PlayPlan for every non-done
// member) clears Task.SessionID to "", so the D8.10 retention requirement is
// unmet; that behavioral RED is this file's target.
//
// Reported gap, not fabricated as an assertion: D8.10's "a step/plan state
// change and session dispatch need recoverable intent + a receipt naming the
// failed member" has no persisted seam on this revision (PlayPlan's
// PlayResult is in-memory only). The crash-cut test pins the durable facts
// that must survive the cut (bindings, exactly-once dispatch); the missing
// durable-intent seam is reported to the coordinator for W4/W2 to consume.
//
// Fixture mechanism note (measured on the pre-change tree, 2026-10-03, red
// runs 1-6): StopPlan's session fan-out lands NO D2 member stop on this
// revision, for ANY member shape. The fan-out's generic RequestCancel path
// cannot claim steered turns — fired=0 even with live parked turns
// (installParkedProvider + launchSteeredChild + Dispatch) — and turn-less
// members are never claimed at all (RequestCancel's claimOrArm returns before
// interruptGracefully, the TransitionSession(LifecycleStopped) writer, ever
// runs). This is the pre-existing "member stops written OUTSIDE the D2 path"
// gap the u1 pack header records — the pending W4 leg — so the member-stop
// premise assertions in this pack are RED against that missing
// implementation (kept loud, never skipped), exactly like the existing u1
// pack's own RED. Two consequences are deliberate:
//   - the D6 direct-parent notice count is asserted RELATIVELY (unchanged
//     across the restart) — the absolute notice duty for the stop leg is the
//     existing W6 pack's RED target, not this file's;
//   - the nil-edge ordinary-root member's "still stopped" assertion (test 3)
//     fails for the same missing-leg reason; it is kept as the loud
//     coordinator-ruled expectation (F0929-R2-Q3=A / ADR-093 D6's nil-safe
//     rule) rather than weakened to match today's gap.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// u1prHarness builds a PlanEngine over EXPLICIT plan/task store dirs (so a
// later phase can REOPEN the same stores as a new process would) and wires
// the given real AgentLoop as its canceller — the same production wiring
// u1PlanStopHarness uses. Collaborator fakes (judge/notifier/clock) and the
// dispatch-recording fakePlanDispatcher stay as newTestPlanEngine builds
// them: this pack drives the stop and the restart state machine, not
// adjudication.
func u1prHarness(t *testing.T, al *AgentLoop, baseDir string) *planEngineHarness {
	t.Helper()
	ps := plan.New(filepath.Join(baseDir, "plans"))
	ts := task.New(filepath.Join(baseDir, "tasks"))
	fj := &fakePlanJudge{}
	fd := &fakePlanDispatcher{store: ts}
	fn := &fakePlanNotifier{}
	fc := &fakePlanClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	pe := &PlanEngine{
		planStore:        ps,
		taskStore:        ts,
		dispatcher:       fd,
		judge:            fj,
		notifier:         fn,
		clock:            fc,
		tickInterval:     defaultPlanEngineTickInterval,
		activeCounters:   make(map[string]ActiveCounterFunc),
		verifierRegistry: NewVerifierSessionRegistry(),
		judgeSema:        newDispatchSemaphore(defaultPlanJudgeConcurrency),
	}
	pe.canceller = al
	pe.agentLoop = al
	return &planEngineHarness{pe: pe, plans: ps, tasks: ts, judge: fj, disp: fd, notif: fn, clock: fc}
}

// u1prFixture holds every expected value the FIXTURE minted, captured BEFORE
// any production call runs — the pack's oracle.
type u1prFixture struct {
	planID         string
	origin         string
	sessM1, sessM2 string
	sessDone       string
	t1, t2, t3     *task.Task
	goalM1, goalM2 string
	markerM1       string
	markerM2       string
}

// u1prStartPlanWithMembers builds T17's plan shape: TWO in-progress steps,
// each a REAL transcript-backed member session carrying the existing u1
// pack's steered record shape (direct-parent edge to origin) and a REAL
// task-owned active goal — plus one done step.
//
// Measured on the pre-change tree (red-run-4/6): StopPlan's session fan-out
// lands NO D2 member stop on this revision for ANY member shape — the
// generic RequestCancel path cannot claim steered turns (fired=0 even with
// live parked turns) and turn-less members are never claimed at all — so the
// D2 member-stop premise assertions below stay RED against the pending W4
// stop leg while the restart-leg REDs (this pack's primary target) stand
// independently. Deliberately the same member shape as the existing u1 pack
// so CHECK compares like with like.
func u1prStartPlanWithMembers(t *testing.T, al *AgentLoop, h *planEngineHarness, planID string) *u1prFixture {
	t.Helper()
	fx := &u1prFixture{planID: planID}

	wireSteerCompletionDeps(t, al)
	fx.origin = newTestSteeringSession(t, al, "ws-u1-plan-restart")

	fx.sessM1 = newTestSteeringSession(t, al, "ws-u1-plan-restart")
	fx.sessM2 = newTestSteeringSession(t, al, "ws-u1-plan-restart")
	u1PersistSteered(t, al, fx.sessM1, fx.origin, session.LifecycleRunning)
	u1PersistSteered(t, al, fx.sessM2, fx.origin, session.LifecycleRunning)

	fx.t1 = mustCreateTask(t, h.tasks, &task.Task{
		Title: "restart-member-1", WorkspaceID: "ws", PlanID: planID,
		Status: task.StatusInProgress, SessionID: fx.sessM1,
	})
	fx.t2 = mustCreateTask(t, h.tasks, &task.Task{
		Title: "restart-member-2", WorkspaceID: "ws", PlanID: planID,
		Status: task.StatusInProgress, SessionID: fx.sessM2,
	})
	fx.goalM1 = u1ActivateTaskGoal(t, fx.t1.ID, fx.sessM1)
	fx.goalM2 = u1ActivateTaskGoal(t, fx.t2.ID, fx.sessM2)

	fx.markerM1 = "u1-plan-restart history marker M1 " + fx.t1.ID
	fx.markerM2 = "u1-plan-restart history marker M2 " + fx.t2.ID
	u1prAppendTranscript(t, al, fx.sessM1, fx.markerM1)
	u1prAppendTranscript(t, al, fx.sessM2, fx.markerM2)

	// The done step: real session binding; the restart must leave it untouched.
	fx.sessDone = newTestSteeringSession(t, al, "ws-u1-plan-restart")
	fx.t3 = mustCreateTask(t, h.tasks, &task.Task{
		Title: "restart-member-done", WorkspaceID: "ws", PlanID: planID,
		Status: task.StatusDone, SessionID: fx.sessDone,
	})

	mustCreateRunningPlan(t, h.plans, planID, "owner")
	return fx
}

func u1prAppendTranscript(t *testing.T, al *AgentLoop, sessID, content string) {
	t.Helper()
	if al.GetSessionStore() == nil {
		t.Fatal("AppendTranscript: al.GetSessionStore() is nil — the shared session store must be initialized")
	}
	if err := al.GetSessionStore().AppendTranscript(sessID, session.TranscriptEntry{
		ID:        "u1pr-" + sessID,
		Role:      "user",
		Content:   content,
		Timestamp: time.Now().UTC(),
		AgentID:   testDefaultAgentID,
	}); err != nil {
		t.Fatalf("AppendTranscript(%s): %v", sessID, err)
	}
}

// u1prAssertTranscriptKept pins D8.10's "conversation" half: the ORIGINAL
// member session's transcript still carries the pre-restart marker entry —
// exact content equality, read back through a freshly reopened store.
func u1prAssertTranscriptKept(t *testing.T, al *AgentLoop, sessID, marker string) {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sessID)
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", sessID, err)
	}
	for _, e := range entries {
		if e.Content == marker {
			return
		}
	}
	t.Errorf("transcript of %s lost its pre-restart history entry %q (read %d entries) — "+
		"D8.10: the explicit restart retains the step's conversation under the same session id",
		sessID, marker, len(entries))
}

type u1prGoalRef struct{ name, goalID, sessID string }

// u1prAssertGoalsActive pins MAJ-003/D8.10 across any phase: the ORIGINAL
// task-goal records are still ACTIVE with their original session binding —
// no GoalStateCleared, no replacement goal, no re-binding.
func u1prAssertGoalsActive(t *testing.T, phase string, refs ...u1prGoalRef) {
	t.Helper()
	gs := goal.NewStore(config.OmnipusHomeDir())
	for _, ref := range refs {
		g, err := gs.Get(ref.goalID)
		if err != nil {
			t.Fatalf("%s: Get(task goal %s): %v", phase, ref.name, err)
		}
		if g.State != generated.GoalStateActive {
			t.Errorf("%s: task goal %s state = %q, want active — D8.10/MAJ-003: the original "+
				"task-goal ID and active state carry across stop, boot and plan restart; "+
				"no GoalStateCleared and no replacement goal", phase, ref.goalID, g.State)
		}
		if g.ActiveSessionID != ref.sessID {
			t.Errorf("%s: task goal %s session binding = %q, want original %q — D8.10: the "+
				"original session binding carries across the restart", phase, ref.goalID,
				g.ActiveSessionID, ref.sessID)
		}
	}
}

// u1prAssertStoppedPremise pins the T17 state that must hold after the stop
// AND across every later boot: the Plan/Task failed(stopped_by_user) reason
// pairs (MIN-006), both original task-goal IDs still active, both member
// sessions D2-stopped with their stop note and direct-parent edge intact,
// and the done member untouched.
func u1prAssertStoppedPremise(t *testing.T, al *AgentLoop, h *planEngineHarness, fx *u1prFixture, phase string) {
	t.Helper()

	p, err := h.plans.Get(fx.planID)
	if err != nil {
		t.Fatalf("%s: Get(plan): %v", phase, err)
	}
	if p.State != plan.StateFailed || p.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("%s: plan state=%q reason=%q, want failed/stopped_by_user (D8.10/MIN-006)",
			phase, p.State, p.FailedReason)
	}

	gotT1, err := h.tasks.Get(fx.t1.ID)
	if err != nil {
		t.Fatalf("%s: Get(t1): %v", phase, err)
	}
	gotT2, err := h.tasks.Get(fx.t2.ID)
	if err != nil {
		t.Fatalf("%s: Get(t2): %v", phase, err)
	}
	for _, tc := range []struct {
		name string
		got  *task.Task
	}{{"M1", gotT1}, {"M2", gotT2}} {
		if tc.got.Status != task.StatusFailed || tc.got.CancelReason != task.CancelReasonStoppedByUser {
			t.Fatalf("%s: member %s task status=%q cancel_reason=%q, want failed/stopped_by_user (MIN-006)",
				phase, tc.name, tc.got.Status, tc.got.CancelReason)
		}
	}

	gotT3, err := h.tasks.Get(fx.t3.ID)
	if err != nil {
		t.Fatalf("%s: Get(t3): %v", phase, err)
	}
	if gotT3.Status != task.StatusDone || gotT3.SessionID != fx.sessDone {
		t.Fatalf("%s: done member status=%q session=%q, want done/%q untouched",
			phase, gotT3.Status, gotT3.SessionID, fx.sessDone)
	}

	u1prAssertGoalsActive(t, phase,
		u1prGoalRef{name: "M1", goalID: fx.goalM1, sessID: fx.sessM1},
		u1prGoalRef{name: "M2", goalID: fx.goalM2, sessID: fx.sessM2})

	for _, mc := range []struct{ name, sess string }{{"M1", fx.sessM1}, {"M2", fx.sessM2}} {
		rec, err := al.GetSessionLifecycleStore().Load(mc.sess)
		if err != nil {
			t.Fatalf("%s: Load(member %s): %v", phase, mc.name, err)
		}
		if rec.State != session.LifecycleStopped {
			t.Errorf("%s: member %s lifecycle=%q, want D2-stopped", phase, mc.name, rec.State)
		}
		if rec.StopNote == nil {
			t.Errorf("%s: member %s carries no stop note — D2: a landed stopped record keeps its note", phase, mc.name)
		}
		if rec.SteeredBy == nil || rec.SteeredBy.SteeringSessionID != fx.origin {
			t.Errorf("%s: member %s direct-parent edge = %+v, want SteeredBy→%s — D8.10 retains the edge",
				phase, mc.name, rec.SteeredBy, fx.origin)
		}
	}
}

// u1prCountStoppedNotices counts the D6 stopped-child notices about childID
// in observerID's inbox — the same detection shape assertU1NoStoppedNoticeFor
// uses (session_id match + a body offering resume/redirect). Used to pin
// D8.10's "no NEW stop notice on restart" (count unchanged) and ADR-093 D6's
// nil-edge no-notice duty (count zero).
func u1prCountStoppedNotices(t *testing.T, al *AgentLoop, observerID, childID string) int {
	t.Helper()
	entries, err := al.GetMessageInboxStore().Entries(observerID)
	if err != nil {
		t.Fatalf("Entries(%s): %v", observerID, err)
	}
	n := 0
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		raw, merr := entry.Message.MarshalJSON()
		if merr != nil {
			t.Fatalf("MarshalJSON(observer message): %v", merr)
		}
		fields := u1DecodeMessageFields(t, raw)
		if fields["session_id"] != childID {
			continue
		}
		if body := u1NoticeBody(fields); body != "" && u1ContainsAll(body, "resume", "redirect") {
			n++
		}
	}
	return n
}

func u1prCountDispatch(calls []string, taskID string) int {
	n := 0
	for _, c := range calls {
		if c == taskID {
			n++
		}
	}
	return n
}

// TestU1PlanRestartReusesOriginalMemberSessionsAndGoals pins D8.10/T17's
// restart half end to end: StopPlan lands the failed(stopped_by_user) reason
// pairs and keeps both task-owned goals; a process restart (fresh AgentLoop +
// freshly reopened stores over the same dirs) leaves that state intact; the
// explicit restart (PlayPlan — the POST /plans/{id}/restart engine entry)
// must retain each non-done step's ORIGINAL Task.SessionID and conversation,
// keep the same goal records active with unchanged IDs and bindings, leave
// the done step untouched, resume nothing by itself, emit no NEW stop
// notice, and dispatch nothing.
//
// RED on this revision: pkg/task/store.go::RestartReset — PlayPlan's reset
// primitive — clears Task.SessionID to "", so the SessionID assertions fail
// behaviorally (a compile failure or a stubbed skip would NOT be this pack's
// red).
func TestU1PlanRestartReusesOriginalMemberSessionsAndGoals(t *testing.T) {
	baseDir := t.TempDir()
	al1, cleanup1 := newSteerAL(t)
	defer cleanup1()
	h1 := u1prHarness(t, al1, baseDir)
	fx := u1prStartPlanWithMembers(t, al1, h1, "p-u1-restart")

	if _, err := h1.pe.StopPlan(context.Background(), fx.planID, "tester", "web"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}
	u1prAssertStoppedPremise(t, al1, h1, fx, "after StopPlan")

	// The D6 notice baseline is captured, not required >=1: the absolute
	// notice duty for the plan-stop leg is the existing W6 RED pack's target
	// (its writer is still pending there); THIS pack owns only D8.10's
	// "restart emits no NEW stop notice" rule, which the relative
	// (count-unchanged) assertions below pin regardless of the baseline.
	noticesM1 := u1prCountStoppedNotices(t, al1, fx.origin, fx.sessM1)
	noticesM2 := u1prCountStoppedNotices(t, al1, fx.origin, fx.sessM2)

	// --- process restart: fresh AgentLoop over the SAME home, freshly
	// reopened plan/task stores over the SAME dirs ---
	home1 := al1.GetConfig().Agents.Defaults.Home
	al2, cleanup2 := newSteerALWithSkills(t, home1, nil)
	defer cleanup2()
	h2 := u1prHarness(t, al2, baseDir)
	u1prAssertStoppedPremise(t, al2, h2, fx, "after process restart")

	// --- the explicit restart: the production entry D8.10 names ---
	if _, err := h2.pe.PlayPlan(context.Background(), fx.planID); err != nil {
		t.Fatalf("PlayPlan (explicit restart): %v", err)
	}

	gotT1, err := h2.tasks.Get(fx.t1.ID)
	if err != nil {
		t.Fatalf("Get(t1) after restart: %v", err)
	}
	gotT2, err := h2.tasks.Get(fx.t2.ID)
	if err != nil {
		t.Fatalf("Get(t2) after restart: %v", err)
	}
	// D8.10: "it must retain each non-done step's Task.SessionID". Expected
	// values are the fixture's own session ids, captured before any
	// production call.
	if gotT1.SessionID != fx.sessM1 {
		t.Errorf("restart member M1: Task.SessionID = %q, want original %q — D8.10: the explicit "+
			"restart retains each non-done step's Task.SessionID (today pkg/task/store.go::"+
			"RestartReset, PlayPlan's reset primitive, clears it)", gotT1.SessionID, fx.sessM1)
	}
	if gotT2.SessionID != fx.sessM2 {
		t.Errorf("restart member M2: Task.SessionID = %q, want original %q — D8.10: the explicit "+
			"restart retains each non-done step's Task.SessionID (today pkg/task/store.go::"+
			"RestartReset, PlayPlan's reset primitive, clears it)", gotT2.SessionID, fx.sessM2)
	}
	if task.IsTerminal(gotT1.Status) || task.IsTerminal(gotT2.Status) {
		t.Errorf("restart members' statuses after restart = %q/%q, want a dispatchable (non-terminal) "+
			"reset — D8.10 readmission requires the steps redispatchable", gotT1.Status, gotT2.Status)
	}

	// Conversation history stays addressable under the ORIGINAL session ids.
	u1prAssertTranscriptKept(t, al2, fx.sessM1, fx.markerM1)
	u1prAssertTranscriptKept(t, al2, fx.sessM2, fx.markerM2)

	// Same goal record IDs, active state and session binding across restart.
	u1prAssertGoalsActive(t, "after PlayPlan",
		u1prGoalRef{name: "M1", goalID: fx.goalM1, sessID: fx.sessM1},
		u1prGoalRef{name: "M2", goalID: fx.goalM2, sessID: fx.sessM2})

	// Done step remains untouched (T17).
	gotT3, err := h2.tasks.Get(fx.t3.ID)
	if err != nil {
		t.Fatalf("Get(t3) after restart: %v", err)
	}
	if gotT3.Status != task.StatusDone || gotT3.SessionID != fx.sessDone {
		t.Errorf("done member after restart: status=%q session=%q, want untouched done/%q — "+
			"D8.10: the restart preserves done members", gotT3.Status, gotT3.SessionID, fx.sessDone)
	}

	// The restart itself must not resume the stopped member sessions (D8.5:
	// nothing resumes automatically; readmission is the separate explicit
	// step) and must retain the direct-parent edge (D8.10).
	for _, mc := range []struct{ name, sess string }{{"M1", fx.sessM1}, {"M2", fx.sessM2}} {
		rec, err := al2.GetSessionLifecycleStore().Load(mc.sess)
		if err != nil {
			t.Fatalf("Load(member %s) after restart: %v", mc.name, err)
		}
		if rec.State != session.LifecycleStopped {
			t.Errorf("member %s lifecycle after restart = %q, want still stopped — the restart "+
				"itself must not resume the member (D8.5; readmission is a separate explicit step)",
				mc.name, rec.State)
		}
		if rec.SteeredBy == nil || rec.SteeredBy.SteeringSessionID != fx.origin {
			t.Errorf("member %s direct-parent edge after restart = %+v, want retained SteeredBy→%s — "+
				"D8.10 retains the edge", mc.name, rec.SteeredBy, fx.origin)
		}
	}

	// No NEW stop notice: the D6 notice counts are unchanged (D8.10: neither
	// plan restart nor member admission emits a new stop notice unless the
	// session stops again).
	if got := u1prCountStoppedNotices(t, al2, fx.origin, fx.sessM1); got != noticesM1 {
		t.Errorf("M1 stopped-child notices in origin inbox after restart = %d, want unchanged %d — "+
			"D8.10: restart emits no new stop notice (the original stays as history)", got, noticesM1)
	}
	if got := u1prCountStoppedNotices(t, al2, fx.origin, fx.sessM2); got != noticesM2 {
		t.Errorf("M2 stopped-child notices in origin inbox after restart = %d, want unchanged %d — "+
			"D8.10: restart emits no new stop notice (the original stays as history)", got, noticesM2)
	}

	// The restart dispatches nothing itself (readmission/dispatch is the
	// separate, later step — D8.10); the fake dispatcher must be silent.
	if got := len(h2.disp.callList()); got != 0 {
		t.Errorf("dispatcher calls during PlayPlan = %d, want 0 — the restart must not dispatch "+
			"members itself (calls: %v)", got, h2.disp.callList())
	}
}

// TestU1PlanRestartCrashBeforeMemberDispatchKeepsBindingsAndDispatchesOnce
// pins T17's crash cut: after the explicit restart's state write but BEFORE
// any member dispatch, a process crash and reopen must leave every original
// binding intact (session id, conversation, task-goal), must NOT have
// resumed anything (D8.5), and the pending plan must then dispatch each
// stopped member EXACTLY ONCE — a further engine pass must not dispatch an
// already-working step a second time (D8.10).
//
// RED on this revision: the binding assertions fail behaviorally (RestartReset
// cleared the session ids). Reported gap, not asserted: D8.10's durable
// recoverable-intent record naming a failed member has no persisted seam on
// this revision.
func TestU1PlanRestartCrashBeforeMemberDispatchKeepsBindingsAndDispatchesOnce(t *testing.T) {
	baseDir := t.TempDir()
	al1, cleanup1 := newSteerAL(t)
	defer cleanup1()
	h1 := u1prHarness(t, al1, baseDir)
	fx := u1prStartPlanWithMembers(t, al1, h1, "p-u1-restart-crash")

	if _, err := h1.pe.StopPlan(context.Background(), fx.planID, "tester", "web"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}
	u1prAssertStoppedPremise(t, al1, h1, fx, "after StopPlan")

	home1 := al1.GetConfig().Agents.Defaults.Home

	// boot2: the explicit restart's state write — and NOTHING else.
	al2, cleanup2 := newSteerALWithSkills(t, home1, nil)
	defer cleanup2()
	h2 := u1prHarness(t, al2, baseDir)
	u1prAssertStoppedPremise(t, al2, h2, fx, "boot2 before restart")
	if _, err := h2.pe.PlayPlan(context.Background(), fx.planID); err != nil {
		t.Fatalf("PlayPlan (restart state write): %v", err)
	}

	// --- crash before member dispatch: boot3 reopens the same stores ---
	al3, cleanup3 := newSteerALWithSkills(t, home1, nil)
	defer cleanup3()
	h3 := u1prHarness(t, al3, baseDir)

	// The bindings survive the crash cut (D8.10/T17). Expected values are the
	// fixture's own.
	gotT1, err := h3.tasks.Get(fx.t1.ID)
	if err != nil {
		t.Fatalf("boot3: Get(t1): %v", err)
	}
	gotT2, err := h3.tasks.Get(fx.t2.ID)
	if err != nil {
		t.Fatalf("boot3: Get(t2): %v", err)
	}
	if gotT1.SessionID != fx.sessM1 {
		t.Errorf("boot3 (crash before dispatch): member M1 Task.SessionID = %q, want original %q — "+
			"D8.10: the original session binding survives the restart state write and the crash cut "+
			"(today pkg/task/store.go::RestartReset cleared it at the restart write)",
			gotT1.SessionID, fx.sessM1)
	}
	if gotT2.SessionID != fx.sessM2 {
		t.Errorf("boot3 (crash before dispatch): member M2 Task.SessionID = %q, want original %q — "+
			"D8.10: the original session binding survives the restart state write and the crash cut "+
			"(today pkg/task/store.go::RestartReset cleared it at the restart write)",
			gotT2.SessionID, fx.sessM2)
	}
	u1prAssertTranscriptKept(t, al3, fx.sessM1, fx.markerM1)
	u1prAssertTranscriptKept(t, al3, fx.sessM2, fx.markerM2)
	u1prAssertGoalsActive(t, "boot3 (crash before dispatch)",
		u1prGoalRef{name: "M1", goalID: fx.goalM1, sessID: fx.sessM1},
		u1prGoalRef{name: "M2", goalID: fx.goalM2, sessID: fx.sessM2})

	// Boot alone must not have resumed anything (D8.5).
	for _, mc := range []struct{ name, sess string }{{"M1", fx.sessM1}, {"M2", fx.sessM2}} {
		rec, err := al3.GetSessionLifecycleStore().Load(mc.sess)
		if err != nil {
			t.Fatalf("boot3: Load(member %s): %v", mc.name, err)
		}
		if rec.State != session.LifecycleStopped {
			t.Errorf("boot3: member %s lifecycle = %q, want still stopped — boot alone never "+
				"performs a plan restart or member resume (D8.5)", mc.name, rec.State)
		}
	}

	// The pending plan admits and dispatches each stopped member EXACTLY ONCE.
	h3.pe.tryStartApprovedPlan(context.Background(), fx.planID)
	firstWave := h3.disp.callList()
	for _, tc := range []struct {
		name string
		id   string
	}{{"M1", fx.t1.ID}, {"M2", fx.t2.ID}} {
		if got := u1prCountDispatch(firstWave, tc.id); got != 1 {
			t.Errorf("post-crash dispatch wave: member %s (%s) dispatched %d times, want exactly 1 "+
				"(wave: %v) — D8.10: no duplicate dispatch across the crash cut", tc.name, tc.id, got, firstWave)
		}
	}

	// A further engine pass must not dispatch an already-working step again.
	h3.pe.processPlan(context.Background(), fx.planID)
	secondPass := h3.disp.callList()
	for _, tc := range []struct {
		name string
		id   string
	}{{"M1", fx.t1.ID}, {"M2", fx.t2.ID}} {
		if got := u1prCountDispatch(secondPass, tc.id); got != 1 {
			t.Errorf("after the further engine pass: member %s (%s) dispatched %d times total, want "+
				"still exactly 1 (passes: %v) — D8.10: an already-working step is not dispatched a "+
				"second time", tc.name, tc.id, got, secondPass)
		}
	}
}

// TestU1PlanRestartNilEdgeMemberStoppedNoNoticeHistoryKept pins ADR-093 D6's
// consequence alongside D8.10's restart retention: a member whose steering
// edge was dropped at task start (SteeredBy == nil, ordinary root) receives
// the plan stop (it is an in-progress member session in StopPlan's fan-out —
// the coordinator's dispatch for this pack, founded on F0929-R2-Q3=A),
// keeps its conversation history and task-owned goal, gets NO D6 notice
// anywhere (no parent exists), and — after the explicit restart — retains
// its ORIGINAL Task.SessionID.
func TestU1PlanRestartNilEdgeMemberStoppedNoNoticeHistoryKept(t *testing.T) {
	baseDir := t.TempDir()
	al1, cleanup1 := newSteerAL(t)
	defer cleanup1()
	h1 := u1prHarness(t, al1, baseDir)
	wireSteerCompletionDeps(t, al1)

	// The only plausible observer in this fixture; it must receive NOTHING
	// for the nil-edge member.
	origin := newTestSteeringSession(t, al1, "ws-u1-plan-restart-nil")

	sessM := newTestSteeringSession(t, al1, "ws-u1-plan-restart-nil")
	// A nil-edge member: a running session record with NO SteeredBy —
	// ADR-093 D6's ordinary-root shape.
	nilEdge := &session.LifecycleRecord{
		SessionID: sessM, Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-u1-pr-nil"},
	}
	persistLifecycle(t, al1.GetSessionLifecycleStore(), nilEdge)

	t1 := mustCreateTask(t, h1.tasks, &task.Task{
		Title: "restart-nil-edge-member", WorkspaceID: "ws", PlanID: "p-u1-restart-nil",
		Status: task.StatusInProgress, SessionID: sessM,
	})
	goalM := u1ActivateTaskGoal(t, t1.ID, sessM)
	marker := "u1-plan-restart history marker nil-edge " + t1.ID
	u1prAppendTranscript(t, al1, sessM, marker)

	mustCreateRunningPlan(t, h1.plans, "p-u1-restart-nil", "owner")

	if _, err := h1.pe.StopPlan(context.Background(), "p-u1-restart-nil", "tester", "web"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}

	p, err := h1.plans.Get("p-u1-restart-nil")
	if err != nil {
		t.Fatalf("Get(plan): %v", err)
	}
	if p.State != plan.StateFailed || p.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("premise: plan state=%q reason=%q, want failed/stopped_by_user", p.State, p.FailedReason)
	}
	gotT1, err := h1.tasks.Get(t1.ID)
	if err != nil {
		t.Fatalf("Get(t1): %v", err)
	}
	if gotT1.Status != task.StatusFailed || gotT1.CancelReason != task.CancelReasonStoppedByUser {
		t.Fatalf("member task status=%q cancel_reason=%q, want failed/stopped_by_user",
			gotT1.Status, gotT1.CancelReason)
	}
	rec, err := al1.GetSessionLifecycleStore().Load(sessM)
	if err != nil {
		t.Fatalf("Load(nil-edge member): %v", err)
	}
	if rec.State != session.LifecycleStopped {
		t.Errorf("nil-edge member lifecycle after the plan stop = %q, want stopped — the stop "+
			"fan-out covers every in-progress member session, edge or no edge", rec.State)
	}
	if got := u1prCountStoppedNotices(t, al1, origin, sessM); got != 0 {
		t.Errorf("nil-edge member: observer %s received %d stopped-child notice(s) — ADR-093 D6: "+
			"a member with no steering edge has no direct parent and no D6 notice duty", origin, got)
	}

	// --- process restart + explicit restart ---
	home1 := al1.GetConfig().Agents.Defaults.Home
	al2, cleanup2 := newSteerALWithSkills(t, home1, nil)
	defer cleanup2()
	h2 := u1prHarness(t, al2, baseDir)

	if _, err := h2.pe.PlayPlan(context.Background(), "p-u1-restart-nil"); err != nil {
		t.Fatalf("PlayPlan (explicit restart): %v", err)
	}

	gotT1b, err := h2.tasks.Get(t1.ID)
	if err != nil {
		t.Fatalf("Get(t1) after restart: %v", err)
	}
	if gotT1b.SessionID != sessM {
		t.Errorf("nil-edge member after restart: Task.SessionID = %q, want original %q — D8.10: the "+
			"explicit restart retains each non-done step's Task.SessionID (today pkg/task/store.go::"+
			"RestartReset clears it)", gotT1b.SessionID, sessM)
	}
	u1prAssertTranscriptKept(t, al2, sessM, marker)
	u1prAssertGoalsActive(t, "after PlayPlan (nil-edge)",
		u1prGoalRef{name: "nil-edge M", goalID: goalM, sessID: sessM})
	rec2, err := al2.GetSessionLifecycleStore().Load(sessM)
	if err != nil {
		t.Fatalf("Load(nil-edge member) after restart: %v", err)
	}
	if rec2.State != session.LifecycleStopped {
		t.Errorf("nil-edge member lifecycle after restart = %q, want still stopped — the restart "+
			"itself must not resume the member (D8.5)", rec2.State)
	}
	if got := u1prCountStoppedNotices(t, al2, origin, sessM); got != 0 {
		t.Errorf("nil-edge member: observer received %d stopped-child notice(s) after restart, want 0", got)
	}
}
