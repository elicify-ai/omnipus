package agent

// U1 RED — plan-member stop leg (coordinator-approved scope, 2026-10-02).
// Authority: sub-agent control-plane ADR @ cd20cf8b365e7bc8a010c731a8f6830e14397c23
// — D6 (plan stop is a notice-producing stop kind), D8.10/MAJ-008 (the stop
// fan-out lands member sessions D2-`stopped` with goals kept and each direct
// parent gets D6's deduplicated notice), MAJ-009 (direct-only routing: the
// root does not act in a member's parent's place), T15/T17/T20; ADR-093 D6
// (a member whose steering edge was dropped at task start runs as an
// ordinary root — `SteeredBy == nil` — and has NO notice duty, the same
// nil-safe rule as the settled helper-identity ruling). The architect's
// §3.3 assessment records that the edge itself already exists in the data
// model (task members launch steered via StartTaskNow → SteeredBy) and that
// today's plan stop writes member stops OUTSIDE the D2 path
// (plan_engine.go::cancelMemberLocked: task `failed(stopped_by_user)` +
// terminateTaskGoalRecord, the MAJ-003 removal target) — that missing
// routing IS this RED pack's target, not a scope drop.
//
// Cause note: D2's stop-note cause vocabulary is closed
// (stop/redirect_pause/cascade/restart/timeout) and a plan-swept member is
// not the stop's direct target — `cascade` is the sweep value the
// CancelSubtree path already stamps for swept descendants. The tests pin
// `cascade`; if backend-lead's implementation assigns a different member of
// the closed vocabulary, that is a spec-level correction to settle, not a
// test weakening.

import (
	"context"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// u1PlanStopHarness wires a PlanEngine built on real plan/task stores to
// this test's real AgentLoop: the loop IS the sessionCanceller (the
// production wiring at plan_engine.go's constructor) and receives the
// task-status events. Collaborator fakes (judge/dispatcher/notifier/clock)
// stay as newTestPlanEngine built them — this pack drives the stop fan-out,
// not adjudication.
func u1PlanStopHarness(t *testing.T, al *AgentLoop) *planEngineHarness {
	t.Helper()
	h := newTestPlanEngine(t)
	h.pe.canceller = al
	h.pe.agentLoop = al
	return h
}

// u1ActivateTaskGoal creates and activates the task-paired goal record a
// task run mints (GoalOwnerKindTask/taskID, bound to the session the task
// run holds) — the fixture terminateTaskGoalRecord's GetByOwner lookup
// finds. Returns the minted goal id.
func u1ActivateTaskGoal(t *testing.T, taskID, boundSessionID string) string {
	t.Helper()
	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.GoalSourceTaskExplicit,
		"plan member work "+taskID, "", nil, newFloorDoD(), config.DefaultGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("u1ActivateTaskGoal: goal.New: %v", err)
	}
	gs := goal.NewStore(config.OmnipusHomeDir())
	if cerr := gs.Create(g); cerr != nil {
		t.Fatalf("u1ActivateTaskGoal: Create: %v", cerr)
	}
	if _, uerr := gs.Update(g.GoalID, func(cur *goal.Goal) error {
		return cur.Activate(boundSessionID, time.Now().UTC())
	}); uerr != nil {
		t.Fatalf("u1ActivateTaskGoal: Activate: %v", uerr)
	}
	return g.GoalID
}

// TestU1PlanStopSteeredMemberNoticeLandsForDirectParentOnly pins D8.10's
// notice half for a steered plan member: a plan stop lands the member's
// session D2-`stopped` (non-terminal, stop note carried) and persists
// exactly one deduplicated D6 notice to the member's DIRECT parent — the
// origin session the member's SteeredBy names — waking the working parent
// once. Routing is direct-only (MAJ-009): the origin's own parent (the
// fixture's indirect ancestor) receives nothing for this member.
func TestU1PlanStopSteeredMemberNoticeLandsForDirectParentOnly(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	h := u1PlanStopHarness(t, al)

	root := newTestSteeringSession(t, al, "ws-u1-plan-stop")
	origin := newTestSteeringSession(t, al, "ws-u1-plan-stop")
	// The origin is itself steered by root (its lifecycle record carries
	// SteeredBy → root), making root the member's INDIRECT ancestor — the
	// MAJ-009 negative observer.
	u1PersistSteered(t, al, origin, root, session.LifecycleRunning)

	u1PersistSteered(t, al, "sess-u1-plan-m1", origin, session.LifecycleRunning)
	member, err := al.GetSessionLifecycleStore().Load("sess-u1-plan-m1")
	if err != nil {
		t.Fatalf("Load(member): %v", err)
	}
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "m1", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusInProgress, SessionID: "sess-u1-plan-m1",
	})
	wakes := observeU1ParentNoticeWakes(t, al, origin)

	if _, err := h.pe.StopPlan(context.Background(), "p1", "tester", "web"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}

	// Premise (D8.10 keeps it): the plan itself lands failed/stopped_by_user.
	p, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("Get(plan): %v", err)
	}
	if p.State != plan.StateFailed || p.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("premise: plan state=%q reason=%q, want failed/stopped_by_user (D8.10)", p.State, p.FailedReason)
	}

	// The member's session outcome: D2-stopped (the helper asserts stopped
	// non-terminal, unchanged generation, the carried stop note, exactly one
	// deduplicated notice to the DIRECT parent conveying cause+actor and the
	// D6 action offers, and no legacy terminal/fatal completion beside it).
	// Sweep cause: the member did not press Stop itself — the plan's stop
	// swept it (see the file header's cause note).
	noticeID, _ := assertU1StoppedChildNotice(t, al, origin, member, string(session.StopCauseCascade), "tester")
	if got := wakes(noticeID); got != 1 {
		t.Errorf("working direct-parent wakes for the member's stopped notice = %d, want exactly 1 (D6)", got)
	}

	// MAJ-009: the indirect ancestor receives nothing for this member.
	assertU1NoStoppedNoticeFor(t, al, root, "sess-u1-plan-m1")
}

// TestU1PlanStopKeepsMemberGoalsActive pins D8.10's goals half (T17/T20):
// a plan stop ends NO goal record. Both of a member's goal records stay
// active — the task-paired goal (which today plan_engine.go::
// cancelMemberLocked ends via terminateTaskGoalRecord, the MAJ-003 removal
// target) and the member session's own session-owned goal (D6 Goal row:
// only clear_goal ends a goal).
func TestU1PlanStopKeepsMemberGoalsActive(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	h := u1PlanStopHarness(t, al)

	origin := newTestSteeringSession(t, al, "ws-u1-plan-goals")
	u1PersistSteered(t, al, "sess-u1-plan-g1", origin, session.LifecycleRunning)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	m1 := mustCreateTask(t, h.tasks, &task.Task{
		Title: "m1", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusInProgress, SessionID: "sess-u1-plan-g1",
	})
	taskGoal := u1ActivateTaskGoal(t, m1.ID, "sess-u1-plan-g1")
	sessGoal := activateTestGoalRecord(t, "sess-u1-plan-g1", "member session goal stays open")

	if _, err := h.pe.StopPlan(context.Background(), "p1", "tester", "web"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}

	gs := goal.NewStore(config.OmnipusHomeDir())
	tg, err := gs.Get(taskGoal)
	if err != nil {
		t.Fatalf("Get(task goal): %v", err)
	}
	if tg.State != generated.GoalStateActive {
		t.Errorf("task-paired goal after the plan stop = %q, want active — D8.10/T17: "+
			"the stop fan-out keeps goals (cancelMemberLocked's terminateTaskGoalRecord "+
			"is the MAJ-003 removal target)", tg.State)
	}
	sg, err := gs.Get(sessGoal)
	if err != nil {
		t.Fatalf("Get(session goal): %v", err)
	}
	if sg.State != generated.GoalStateActive {
		t.Errorf("member session goal after the plan stop = %q, want active — D6 Goal row: "+
			"no stop of any kind ends a goal", sg.State)
	}
}

// TestU1PlanStopNilEdgeOrdinaryRootMemberGetsNoNotice pins ADR-093 D6's
// consequence (architect §3.3): a member whose steering edge was dropped at
// task start runs as an ordinary root (`SteeredBy == nil`) and has NO D6
// notice duty — no parent inbox may receive a stopped-child notice naming
// it. The decided part ends there: this test deliberately does NOT assert
// the nil-edge member's own session state (the ADR's stop fan-out names the
// sessions, but no decided row assigns a nil-edge member's post-stop
// lifecycle state — flagged as an open question, never invented here).
func TestU1PlanStopNilEdgeOrdinaryRootMemberGetsNoNotice(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	h := u1PlanStopHarness(t, al)

	origin := newTestSteeringSession(t, al, "ws-u1-plan-nil")
	// A nil-edge member: a running session record with NO SteeredBy —
	// ADR-093 D6's ordinary-root shape.
	nilEdge := &session.LifecycleRecord{
		SessionID: "sess-u1-plan-nil-m", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-u1-plan-nil"},
	}
	persistLifecycle(t, al.GetSessionLifecycleStore(), nilEdge)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "nil-edge member", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusInProgress, SessionID: "sess-u1-plan-nil-m",
	})

	if _, err := h.pe.StopPlan(context.Background(), "p1", "tester", "web"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}

	// Premise (D8.10 keeps it): the plan itself is stopped-by-user regardless.
	p, err := h.plans.Get("p1")
	if err != nil {
		t.Fatalf("Get(plan): %v", err)
	}
	if p.State != plan.StateFailed || p.FailedReason != plan.FailedReasonStoppedByUser {
		t.Fatalf("premise: plan state=%q reason=%q, want failed/stopped_by_user", p.State, p.FailedReason)
	}

	// No parent inbox (the only plausible parents in this fixture) may hold
	// a stopped-child notice for the nil-edge member.
	for _, observer := range []string{origin} {
		entries, ierr := al.GetMessageInboxStore().Entries(observer)
		if ierr != nil {
			t.Fatalf("Entries(%s): %v", observer, ierr)
		}
		for _, entry := range entries {
			if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
				continue
			}
			raw, merr := entry.Message.MarshalJSON()
			if merr != nil {
				t.Fatalf("MarshalJSON: %v", merr)
			}
			fields := u1DecodeMessageFields(t, raw)
			if fields["session_id"] == "sess-u1-plan-nil-m" {
				t.Errorf("observer %s received a message for the nil-edge ordinary-root member: %s — "+
					"ADR-093 D6: a member with no steering edge has no direct parent and no D6 notice duty",
					observer, raw)
			}
		}
	}
}
