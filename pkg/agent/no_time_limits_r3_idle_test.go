package agent

// PR #1311 / RED dispatch G1 (R2-1): clocks must not expire running or
// queued work. Launch, Dispatch, reconstruction, admission, completion and
// the idle sweep are real. Only the paid-provider edge is gated. All sweep
// timestamps are supplied explicitly; no sleep advances the idle clock.
// CHECK mutations: lose own-session lookup; ignore the durable queue;
// substitute the routing root and incorrectly protect an unrelated sibling.

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type r3IdleHarness struct {
	al      *AgentLoop
	launch  *SteerLauncher
	model   *gatedFinalProvider
	release func()
	base    time.Time
}

func newR3IdleHarness(t *testing.T) *r3IdleHarness {
	t.Helper()
	p := newGatedFinalProvider()
	al, _ := newGoalLoopTestLoop(t, p, func(cfg *config.Config) {
		cfg.Performance.MaxParallelAgents = 1 // exactly one slot, so the second dispatch queues
	})
	home := config.OmnipusHomeDir()
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "r3-inbox")),
		session.NewLifecycleStore(filepath.Join(home, "r3-lifecycle")))
	if al.bootEpochFor() == 0 {
		mintGenuineBootEpochForLoop(t, al)
	}
	wireSteerCompletionDeps(t, al)
	release := sync.OnceFunc(func() { close(p.release) })
	// Release then join every launched turn BEFORE the harness closes/removes stores.
	t.Cleanup(func() { release(); al.steerAdmission().turns.Wait() })
	return &r3IdleHarness{al: al, launch: NewSteerLauncher(al), model: p, release: release,
		base: time.Now().UTC()}
}

func (h *r3IdleHarness) newSession(t *testing.T, parent, label string, withGoal bool) steer.LaunchResult {
	t.Helper()
	kind := steer.OriginKindChat
	if parent != "" {
		kind = steer.OriginKindDelegate
	}
	req := steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: "native-agent",
		Task: label, Origin: steer.Origin{Kind: kind, CallID: "r3-" + label}}
	if withGoal {
		req.Goal = &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the requested work is complete"}},
			DoD:      []steer.Criterion{{Text: "the result contains evidence of completion"}},
		}
	}
	res, err := h.launch.Launch(context.Background(), req)
	if err != nil {
		t.Fatalf("SETUP Launch(%s): %v", label, err)
	}
	return res
}

func (h *r3IdleHarness) dispatch(t *testing.T, res steer.LaunchResult, want steer.DispatchState) {
	t.Helper()
	got, err := h.launch.Dispatch(context.Background(), res.SessionID, res.Generation)
	if err != nil || got.State != want {
		t.Fatalf("SETUP Dispatch = %+v, err=%v; want %s", got, err, want)
	}
	if want == steer.DispatchQueued && (got.QueuePosition != 1 || got.ConcurrencyLimit != 1) {
		t.Fatalf("SETUP queued disposition = %+v, want position=1 limit=1", got)
	}
}

func (h *r3IdleHarness) ageGoal(t *testing.T, sid string) string {
	t.Helper()
	g := goalRecordForSession(t, sid)
	if g.OwnerKind != generated.GoalOwnerKindSession || g.OwnerID != sid || g.ActiveSessionID != sid {
		t.Fatalf("SETUP goal must belong to the helper's OWN session: %+v", g)
	}
	if _, err := resolveGoalRecordStore().Update(g.GoalID, func(cur *goal.Goal) error {
		cur.LastActivityAt = h.base
		return nil
	}); err != nil {
		t.Fatalf("SETUP age goal: %v", err)
	}
	return g.GoalID
}

func (h *r3IdleHarness) sweep() {
	// G1 requests a sweep beyond the idle bound. One day is the configured
	// bound; one nanosecond past it makes the strict-boundary intent explicit.
	h.al.goalIdleExpirySweep(config.PlanningConfig{IdleExpiryDays: 1}, h.base.Add(24*time.Hour+time.Nanosecond))
}

func r3GoalState(t *testing.T, gid string, want generated.GoalState, why string) {
	t.Helper()
	if got := mustGoalRecord(t, gid); got.State != want {
		t.Errorf("%s: goal state=%q, want %q", why, got.State, want)
	}
}

func (h *r3IdleHarness) assertOwnRunningIdentity(t *testing.T, sid, root string) {
	t.Helper()
	waitClosed(t, h.model.entered, "the real helper provider")
	v, ok := h.al.activeTurnStates.Load(sid)
	if !ok {
		t.Fatal("SETUP real Dispatch registered no helper turn")
	}
	ts := v.(*turnState)
	if ts.transcriptSessionID != sid || string(ts.routingSessionID) != root || sid == root || !ts.IsAlive() {
		t.Fatalf("SETUP helper identity/live = own:%q routing:%q alive:%v, want own=%q root=%q (distinct)",
			ts.transcriptSessionID, ts.routingSessionID, ts.IsAlive(), sid, root)
	}
}

func TestR3IdleExpiry_RealRunningHelperKeepsItsOwnGoalActive(t *testing.T) {
	h := newR3IdleHarness(t)
	root := h.newSession(t, "", "root", false)
	child := h.newSession(t, root.SessionID, "working-helper", true)
	h.dispatch(t, child, steer.DispatchRunning)
	h.assertOwnRunningIdentity(t, child.SessionID, root.SessionID)
	gid := h.ageGoal(t, child.SessionID)

	h.sweep()
	r3GoalState(t, gid, generated.GoalStateActive, "G1 running helper was expired while its real provider was still working")

	// Control: ending the real work (not deleting a synthetic registry entry)
	// removes its protection. Re-age after completion's legitimate activity bump.
	h.release()
	h.al.steerAdmission().turns.Wait()
	if _, live := h.al.activeTurnStates.Load(child.SessionID); live || h.al.steerAdmission().activeCount() != 0 {
		t.Fatal("CONTROL the helper's real turn/slot did not settle")
	}
	if mustGoalRecord(t, gid).State == generated.GoalStateActive {
		h.ageGoal(t, child.SessionID)
	}
	h.sweep()
	r3GoalState(t, gid, generated.GoalStateExpired, "G1 idle control after the helper's work ended")
}

func TestR3IdleExpiry_RealQueuedHelperAndParentStayActiveUntilQueueIsGone(t *testing.T) {
	h := newR3IdleHarness(t)
	// The slot is occupied by a different tree: it must not masquerade as
	// this parent's own running work. Its ONLY work is the queued descendant.
	busyRoot := h.newSession(t, "", "busy-root", false)
	busy := h.newSession(t, busyRoot.SessionID, "slot-holder", false)
	h.dispatch(t, busy, steer.DispatchRunning)
	h.assertOwnRunningIdentity(t, busy.SessionID, busyRoot.SessionID)
	parent := h.newSession(t, "", "waiting-parent", true)
	queued := h.newSession(t, parent.SessionID, "queued-helper", true)
	h.dispatch(t, queued, steer.DispatchQueued)
	// Settle the parent's OWN undispatched launch. Stop is session-only, so
	// the queued child survives: now descendants are genuinely its only work.
	if _, err := h.al.StopSession(context.Background(), StopRequest{SessionID: parent.SessionID,
		By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}, Channel: "webchat"}); err != nil {
		t.Fatalf("SETUP settle parent's own admission: %v", err)
	}
	if rec, err := h.al.GetSessionLifecycleStore().Load(parent.SessionID); err != nil || rec.State != session.LifecycleStopped {
		t.Fatalf("SETUP parent must have no own queued admission: record=%+v err=%v", rec, err)
	}
	childGoal := h.ageGoal(t, queued.SessionID)
	parentGoal := h.ageGoal(t, parent.SessionID)
	if _, live := h.al.activeTurnStates.Load(queued.SessionID); live {
		t.Fatal("SETUP queued helper must have no registered turn")
	}
	if rec, err := h.al.GetSessionLifecycleStore().Load(queued.SessionID); err != nil || rec.State != session.LifecycleQueued {
		t.Fatalf("SETUP durable queue = %+v, err=%v", rec, err)
	}

	h.sweep()
	r3GoalState(t, childGoal, generated.GoalStateActive, "G1 queued helper was expired while DispatchQueued still held its admission")
	r3GoalState(t, parentGoal, generated.GoalStateActive, "G1 parent with only queued descendants was expired")

	// Control: let the slot-holder finish; real FIFO promotion then runs and
	// settles the queued helper. Join completion rather than sample a timestamp.
	h.release()
	h.al.steerAdmission().turns.Wait()
	if h.al.steerAdmission().queueLen() != 0 || h.al.steerAdmission().activeCount() != 0 {
		t.Fatalf("CONTROL real work did not settle: queued=%d active=%d",
			h.al.steerAdmission().queueLen(), h.al.steerAdmission().activeCount())
	}
	for _, pair := range [][2]string{{childGoal, queued.SessionID}, {parentGoal, parent.SessionID}} {
		if mustGoalRecord(t, pair[0]).State == generated.GoalStateActive {
			h.ageGoal(t, pair[1]) // completion legitimately bumps the activity clock
		}
	}
	h.sweep()
	r3GoalState(t, childGoal, generated.GoalStateExpired, "G1 idle helper after its queue and work were gone")
	r3GoalState(t, parentGoal, generated.GoalStateExpired, "G1 idle parent after its only queued descendant finished")
	t.Run("cascade_stopped_descendants_do_not_protect_parent", r3IdleCascadeStoppedDescendantControl)
}

func r3IdleCascadeStoppedDescendantControl(t *testing.T) {
	h := newR3IdleHarness(t)
	busyRoot := h.newSession(t, "", "cascade-busy-root", false)
	busy := h.newSession(t, busyRoot.SessionID, "cascade-slot-holder", false)
	h.dispatch(t, busy, steer.DispatchRunning)
	h.assertOwnRunningIdentity(t, busy.SessionID, busyRoot.SessionID)
	parent := h.newSession(t, "", "cascade-parent", true)
	child := h.newSession(t, parent.SessionID, "cascade-queued-child", true)
	h.dispatch(t, child, steer.DispatchQueued)
	if _, err := h.al.StopSession(context.Background(), StopRequest{SessionID: parent.SessionID, Tree: true,
		By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}, Channel: "webchat"}); err != nil {
		t.Fatalf("CONTROL Stop whole tree: %v", err)
	}
	for _, sid := range []string{parent.SessionID, child.SessionID} {
		if rec, err := h.al.GetSessionLifecycleStore().Load(sid); err != nil || rec.State != session.LifecycleStopped {
			t.Fatalf("CONTROL cascade target must be stopped: record=%+v err=%v", rec, err)
		}
	}
	if h.al.steerAdmission().queueLen() != 0 {
		t.Fatal("CONTROL cascade did not remove the child's queued admission")
	}
	gid := h.ageGoal(t, parent.SessionID)
	h.sweep()
	r3GoalState(t, gid, generated.GoalStateExpired, "G1 cascade-stopped descendants must not protect the idle parent")
}

func TestR3IdleExpiry_SiblingWorkDoesNotProtectTheWrongHelperGoal(t *testing.T) {
	h := newR3IdleHarness(t)
	root := h.newSession(t, "", "shared-root", false)
	busy := h.newSession(t, root.SessionID, "busy-sibling", true)
	idle := h.newSession(t, root.SessionID, "idle-sibling", true)
	h.dispatch(t, busy, steer.DispatchRunning)
	h.assertOwnRunningIdentity(t, busy.SessionID, root.SessionID)
	// Settle the sibling's admission through the real Stop path, not merely
	// leave an undispatched Launch (whose persisted queued state is work).
	if _, err := h.al.StopSession(context.Background(), StopRequest{SessionID: idle.SessionID,
		By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}, Channel: "webchat"}); err != nil {
		t.Fatalf("SETUP stop idle sibling: %v", err)
	}
	busyGoal := h.ageGoal(t, busy.SessionID)
	idleGoal := h.ageGoal(t, idle.SessionID)

	h.sweep()
	r3GoalState(t, idleGoal, generated.GoalStateExpired, "G1 sibling isolation: unrelated work must not protect the stopped helper")
	r3GoalState(t, busyGoal, generated.GoalStateActive, "G1 sibling isolation control: the actual working sibling must remain Active")
}
