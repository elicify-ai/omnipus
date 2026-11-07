package agent

// Oracle: r17 TK-30 / 18.19, 18.27–18.29. Real plan engine, task/plan
// stores, task-trigger scheduler, executor and session creation. Fake clock
// and fake provider only. Interleaving/barrier cases are separate blockers.
import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t8PlanWaitFixture(t *testing.T) (*AgentLoop, *TaskTriggerScheduler, *triggerFakeClock, *PlanEngine, *plan.Plan) {
	t.Helper()
	al, sched, clock := t8NativeSchedulingFixture(t)
	ps := plan.New(filepath.Join(config.OmnipusHomeDir(), "plans"))
	al.SetPlanStore(ps)
	al.taskExecutor.SetPlanStore(ps)
	pe := NewPlanEngine(al, ps, al.taskStore, al.taskExecutor)
	// Even an unstarted engine can launch an owner/supervisor wake. Stop
	// joins those producers before the native fixture or its temp root closes.
	t.Cleanup(pe.Stop)
	pe.clock = clock
	al.SetPlanEngine(pe)
	p := &plan.Plan{Title: "T8 waiting plan", WorkspaceID: "default", OwnerAgentID: "native-agent", State: plan.StateRunning}
	require.NoError(t, ps.Create(p))
	stamp := clock.Now().UTC().Format(time.RFC3339)
	var err error
	p, err = ps.Update(p.ID, plan.Patch{LastActivityAt: &stamp})
	require.NoError(t, err)
	return al, sched, clock, pe, p
}
func t8PlanMember(t *testing.T, al *AgentLoop, p *plan.Plan, title string, trigger *task.Trigger, blockedBy []string) *task.Task {
	t.Helper()
	status := task.StatusInbox
	if len(blockedBy) > 0 {
		status = task.StatusBlocked
	}
	tk := &task.Task{Title: title, Prompt: "verify " + title, Action: task.ActionLLM, AgentID: "native-agent", CreatedByAgentID: "native-agent", CreatedBy: "native-agent", WorkspaceID: "default", PlanID: p.ID, Status: status, Trigger: trigger, BlockedBy: blockedBy, Criteria: []task.AcceptanceCriterion{planProseCriterion("the scheduled work is verified")}}
	require.NoError(t, al.taskStore.Create(tk))
	pairTaskGoalRecordUsingTaskCriteria(t, al, tk.ID)
	got, err := al.taskStore.Get(tk.ID)
	require.NoError(t, err)
	return got
}
func t8AssertNoFalseWaitEffects(t *testing.T, al *AgentLoop, pe *PlanEngine, p *plan.Plan, beforeSessions int) {
	t.Helper()
	got, err := pe.planStore.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.StateRunning, got.State, "expected waiting does not end a running plan")
	assert.NotEqual(t, plan.PhaseStalled, got.PlanPhase, "expected wait creates no stalled phase")
	assert.Empty(t, got.HandoverText, "no stall note")
	if got.Supervision != nil {
		assert.Zero(t, got.Supervision.Attempts, "no supervision attempt spent")
		assert.Empty(t, got.Supervision.SessionID, "no supervisor session")
	}
	assert.Equal(t, p.LastActivityAt, got.LastActivityAt, "no invented waiting activity")
	assert.Equal(t, beforeSessions, t8SessionCount(t, al), "no task or supervisor session while legitimately waiting")
}
func TestTaskTrigger_PlanWaitAndDependencyChains_ProductionWiring(t *testing.T) {
	for _, length := range []int{1, 2, 3} {
		t.Run(map[int]string{1: "18.19 own scheduled wait", 2: "18.27 two member chain", 3: "18.27 three member chain"}[length], func(t *testing.T) {
			al, sched, clock, pe, p := t8PlanWaitFixture(t)
			at := clock.Now().Add(24 * time.Hour).UnixMilli()
			first := t8PlanMember(t, al, p, "A scheduled root", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}, nil)
			members := []*task.Task{first}
			for i := 1; i < length; i++ {
				members = append(members, t8PlanMember(t, al, p, string(rune('A'+i))+" dependent", nil, []string{members[i-1].ID}))
			}
			sched.OnTaskUpserted(first)
			require.Len(t, sched.cs.ListJobs(true), 1, "legitimate armed-wait control")
			before := t8SessionCount(t, al)
			for range 3 {
				pe.processPlan(context.Background(), p.ID)
				al.taskExecutor.CheckQueuedTasks(context.Background())
				al.taskExecutor.wg.Wait()
				t8AssertNoFalseWaitEffects(t, al, pe, p, before)
				for i, member := range members {
					got, err := al.taskStore.Get(member.ID)
					require.NoError(t, err)
					want := task.StatusBlocked
					if i == 0 {
						want = task.StatusInbox
					}
					require.Equal(t, want, got.Status, "scheduled chain holds before the occurrence")
					runs, err := al.taskStore.ListRuns(member.ID)
					require.NoError(t, err)
					require.Empty(t, runs, "zero early dispatches")
				}
				clock.Advance(time.Hour)
			}
			clock.Advance(21 * time.Hour)
			sched.RunDueJobs(clock.Now())
			sched.WaitForLane()
			al.taskExecutor.wg.Wait()
			for range length {
				pe.processPlan(context.Background(), p.ID)
				al.taskExecutor.CheckQueuedTasks(context.Background())
				al.taskExecutor.wg.Wait()
			}
			for _, member := range members {
				runs, err := al.taskStore.ListRuns(member.ID)
				require.NoError(t, err)
				assert.Len(t, runs, 1, "each chain member dispatches once after scheduled release")
			}
		})
	}
}
func TestTaskTrigger_PlanExpectedWaitDoesNotHideIndependentStall(t *testing.T) {
	al, sched, clock, pe, p := t8PlanWaitFixture(t)
	at := clock.Now().Add(24 * time.Hour).UnixMilli()
	a := t8PlanMember(t, al, p, "A scheduled root", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}, nil)
	_ = t8PlanMember(t, al, p, "B dependent", nil, []string{a.ID})
	blocker := &task.Task{Title: "outside unmet predecessor", Status: task.StatusInbox, AgentID: "native-agent", WorkspaceID: "default", Action: task.ActionLLM}
	require.NoError(t, al.taskStore.Create(blocker))
	d := t8PlanMember(t, al, p, "D genuinely stalled", nil, []string{blocker.ID})
	sched.OnTaskUpserted(a)
	members, err := al.taskStore.List(task.Filter{PlanID: p.ID})
	require.NoError(t, err)
	reason := pe.planStallReason(members, clock.Now())
	assert.Contains(t, reason, d.ID, "18.28 diagnoses independent genuine stall")
	assert.NotContains(t, reason, a.ID, "expected scheduled root not diagnosed")
	for _, member := range members {
		if member.ID != d.ID {
			assert.NotContains(t, reason, member.ID, "scheduled chain contributes no stall reason")
		}
	}
	// Use the real persistence/supervision surface, not just the diagnosis string.
	pe.planDecisionMu.Lock()
	pe.surfaceStallIfAny(p, members)
	pe.planDecisionMu.Unlock()
	got, err := pe.planStore.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.PhaseStalled, got.PlanPhase)
	assert.Contains(t, got.HandoverText, d.ID)
}
func TestTaskTrigger_PlanIdleExpiryRespectsExpectedWait_ProductionWiring(t *testing.T) {
	for _, scheduled := range []bool{true, false} {
		name := "18.29 genuine stalled expiry control"
		if scheduled {
			name = "18.29 scheduled waiting exemption"
		}
		t.Run(name, func(t *testing.T) {
			al, sched, clock, pe, p := t8PlanWaitFixture(t)
			base := clock.Now()
			at := base.Add(14 * 24 * time.Hour).UnixMilli()
			if scheduled {
				member := t8PlanMember(t, al, p, "waiting beyond idle bound", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}, nil)
				sched.OnTaskUpserted(member)
			} else {
				blocker := &task.Task{Title: "unmet external predecessor", Status: task.StatusInbox, AgentID: "native-agent", WorkspaceID: "default", Action: task.ActionLLM}
				require.NoError(t, al.taskStore.Create(blocker))
				t8PlanMember(t, al, p, "genuinely blocked", nil, []string{blocker.ID})
			}
			before := t8SessionCount(t, al)
			for _, delta := range []time.Duration{7*24*time.Hour - time.Millisecond, time.Millisecond, 3 * 24 * time.Hour} {
				clock.Advance(delta)
				pe.idleExpirySweep()
				got, err := pe.planStore.Get(p.ID)
				require.NoError(t, err)
				if scheduled {
					t8AssertNoFalseWaitEffects(t, al, pe, p, before)
				} else if clock.Now().Sub(base) < 7*24*time.Hour {
					assert.Equal(t, plan.StateRunning, got.State)
				} else {
					assert.Equal(t, plan.StateFailed, got.State)
					assert.Equal(t, plan.FailedReasonIdleExpired, got.FailedReason)
				}
			}
		})
	}
}
