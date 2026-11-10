package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Issue #1306 (CR F1): the multi-day idle-expiry sweeps end only workflows
// with nothing in flight. A plan, goal or /loop whose agent is still working
// is never failed or stopped by the calendar, however old its last recorded
// activity is; once the work ends, the sweep applies as before.

func TestPlanIdleExpiry_NeverEndsAPlanWithWorkInFlight(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, h *planEngineHarness) (release func())
	}{
		{"member task in progress", func(t *testing.T, h *planEngineHarness) func() {
			m := mustCreateTask(t, h.tasks, &task.Task{Title: "long member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusInProgress})
			return func() {
				done := task.StatusDone
				if _, err := h.tasks.Update(m.ID, task.Patch{Status: &done}); err != nil {
					t.Fatalf("finish member: %v", err)
				}
			}
		}},
		{"owner or supervisor turn running", func(t *testing.T, h *planEngineHarness) func() {
			h.pe.notePlanTurnStarted("p1")
			return func() { h.pe.notePlanTurnSettled("p1") }
		}},
		{"plan judge round running", func(t *testing.T, h *planEngineHarness) func() {
			if err := h.pe.registry().Register(verifierUnitForPlan("p1"), "judge-session"); err != nil {
				t.Fatalf("hold judge claim: %v", err)
			}
			return func() { h.pe.registry().Unregister(verifierUnitForPlan("p1")) }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestPlanEngine(t)
			mustCreateRunningPlan(t, h.plans, "p1", "owner")
			h.pe.touchActivity("p1")
			release := tc.arrange(t, h)

			h.clock.Set(h.clock.Now().Add(30 * 24 * time.Hour)) // far past the 7-day bound
			h.pe.idleExpirySweep()
			got, err := h.plans.Get("p1")
			if err != nil {
				t.Fatal(err)
			}
			if got.State != plan.StateRunning {
				t.Fatalf("plan %s (%s) while %s — a clock ended work (issue #1306)", got.State, got.FailedReason, tc.name)
			}

			release()
			h.pe.idleExpirySweep()
			if got, _ = h.plans.Get("p1"); got.State != plan.StateFailed || got.FailedReason != plan.FailedReasonIdleExpired {
				t.Fatalf("after the work ended the idle plan = %s (%s), want failed(idle_expired)", got.State, got.FailedReason)
			}
		})
	}
}

func TestLoopIdleExpiry_NeverStopsALoopWhoseRunIsStillWorking(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store := al.GetSessionStore()
	ls, _ := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	now := time.Now().UTC()
	longAgo := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentInst.ID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sid := meta.ID
	jobID, err := ls.AddInterval(agentInst.ID, sid, 5*60*1000)
	if err != nil {
		t.Fatalf("AddInterval: %v", err)
	}
	mode := loopModeInterval
	everyMS := int64(5 * 60 * 1000)
	if err := store.SetMeta(sid, session.MetaPatch{
		LoopMode: &mode, LoopPrompt: strPtrForTest("ping"), LoopMaxRuns: intPtr(config.DefaultLoopMaxRuns),
		LoopIntervalMS: &everyMS, LoopJobID: &jobID, LoopStartedAt: &longAgo, LoopLastActivityAt: &longAgo,
	}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	registerWedgedTurn(t, al, sid, "turn-long-loop-run")

	ls.IdleExpirySweep(config.PlanningConfig{}, now)
	if got, _ := store.GetMeta(sid); got.LoopMode == "" {
		t.Fatal("the loop was stopped while its run was still working — a clock ended work (issue #1306)")
	}

	al.activeTurnStates.Delete(sid)
	ls.IdleExpirySweep(config.PlanningConfig{}, now)
	if got, _ := store.GetMeta(sid); got.LoopMode != "" {
		t.Fatal("after the run ended the idle loop must expire as before")
	}
}
