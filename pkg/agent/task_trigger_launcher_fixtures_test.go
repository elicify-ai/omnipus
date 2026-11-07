package agent

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Addendum 5: exercise executeMainTask, not the isolated executor branch.
// The gateway's unexported eligibility callback is the one injected seam:
// U6 supplies its decision for a real chat-target workspace member and a real
// persisted main. The launcher, admission and all task execution stay real.
// Gateway REST/eligibility wiring is not covered by these component tests.
type u2bLauncherProcess struct {
	*u2bAsyncProcess
	mainID    string
	closeOnce sync.Once
	closeDone chan struct{}
}

func newU2bLauncherProcess(t *testing.T, home string, clk *triggerFakeClock, worker *u2bAsyncWorker) *u2bLauncherProcess {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	agentHome := filepath.Join(home, "native-agent")
	require.NoError(t, os.MkdirAll(agentHome, 0o700))
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{
				{ID: "native-agent", Name: "Native Agent", Type: config.AgentTypeCustom, Home: agentHome},
				judgeAgentConfigForTaskTests(t),
			},
		},
	}
	cfg.Performance.MaxParallelAgents = 1 // Saturatable real admission boundary, not a fake queued Dispatch.
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	require.True(t, cfg.Agents.List[0].IsChatTarget(), "instrument: workers cannot own an eligible main")
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: "default", Name: "U2b launcher workspace", Status: "active", CoreTeam: []string{"native-agent"},
	}))
	wsID, member := workspace.FindForAgentPreferring(home, "native-agent", "default")
	require.True(t, member)
	require.Equal(t, "default", wsID, "instrument: the assignee is actually a workspace member")

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), worker)
	bindMetSoftTierJudge(t, al)
	installTestSessionLauncher(t, al)
	require.IsType(t, &SteerLauncher{}, al.taskExecutor.launcher, "instrument: no capturing/fake launcher")
	main, err := al.GetSessionStore().GetOrCreateMainSessionOwned("default", "native-agent", "uat-person")
	require.NoError(t, err)
	fresh, err := session.NewUnifiedStore(al.GetSessionStore().BaseDir())
	require.NoError(t, err)
	persisted, err := fresh.GetMeta(main.ID)
	require.NoError(t, err)
	require.Equal(t, session.SessionTypeMain, persisted.Type)
	require.Equal(t, "native-agent", persisted.AgentID)
	require.Equal(t, "default", persisted.WorkspaceID)
	require.Equal(t, "uat-person", persisted.Owner)
	al.taskExecutor.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{
		{"default", "native-agent"}: main.ID,
	}})
	sched := NewTaskTriggerScheduler(cron.NewCronService(filepath.Join(home, "tasks_triggers", "jobs.json")), al.taskStore, al.taskExecutor)
	sched.SetClock(clk)
	al.SetTaskTriggerScheduler(sched)
	p := &u2bLauncherProcess{
		u2bAsyncProcess: &u2bAsyncProcess{home: home, loop: al, scheduler: sched, clock: clk},
		mainID:          main.ID,
		closeDone:       make(chan struct{}),
	}
	t.Cleanup(p.stop)
	return p
}

func (p *u2bLauncherProcess) requireMainChild(t *testing.T, taskID, childID string) {
	t.Helper()
	tk, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	require.False(t, tk.RunIsolated, "instrument: isolation must not silently bypass executeMainTask")
	mainID, eligible, err := p.loop.taskExecutor.assigneeMain(tk)
	require.NoError(t, err)
	require.True(t, eligible)
	require.Equal(t, p.mainID, mainID)
	require.Equal(t, taskRunMain, deriveTaskRunMode(tk, eligible))
	rec, err := p.loop.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	require.NotEqual(t, mainID, childID, "MAIN execution is a fresh child, not a turn on the main itself")
	require.Equal(t, mainID, rec.SteeringSessionID(), "instrument: the REAL launcher parented this child on the assignee main")
	require.NotNil(t, rec.Origin)
	require.Equal(t, session.OriginKindTask, rec.Origin.Kind)
	require.Equal(t, taskID, rec.Origin.TaskID)
	require.Equal(t, tk.AgentID, rec.AgentID)
	require.Equal(t, tk.WorkspaceID, rec.WorkspaceID)
}

func (p *u2bLauncherProcess) fireStarted(t *testing.T, worker *u2bAsyncWorker) u2bAsyncFire {
	t.Helper()
	fire := p.fireWhileProviderHeld(t, worker)
	p.requireMainChild(t, fire.taskID, fire.run.SessionID)
	require.True(t, p.loop.taskExecutor.holdsRun(fire.taskID), "instrument: launcher dispatch returned while actual execution still owns the task")
	rec, err := p.loop.GetSessionLifecycleStore().Load(fire.run.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, rec.State)
	require.Equal(t, task.RunKindScheduled, fire.run.Kind)
	require.Nil(t, fire.run.OccurrenceMs)
	return fire
}

func bootU2bLauncherProcess(t *testing.T, home string, clk *triggerFakeClock) *u2bLauncherProcess {
	t.Helper()
	worker := newU2bAsyncWorker()
	worker.complete() // An erroneous replay must run visibly, not hide behind another held provider.
	p := newU2bLauncherProcess(t, home, clk, worker)
	p.physicalBoot(t)
	// dispatchLaunchedTask calls enterDispatch before launching the MAIN task
	// goroutine; runTaskFromInProgress calls wg.Done after its settlement tail.
	p.runDueAndJoin(t)
	return p
}
