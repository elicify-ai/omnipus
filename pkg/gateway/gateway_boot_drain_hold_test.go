// gateway_boot_drain_hold_test.go — G1 (team-lead addendum 12): the gateway
// must register the session-plane hold (ExpectSessionPlane) at boot.
//
// pkg/agent's U2b drain rows call ExpectSessionPlane themselves through the
// sessionPlaneExpecter seam, so a mutant that deletes the gateway's call
// (pkg/gateway/gateway_boot.go::setupAndStartServices, right before
// startSchedulers — CHECK mutant M13) survives every pkg/agent row: no
// pkg/agent test runs the real gateway boot, and this package's rows would.
//
// The row boots the REAL setupAndStartServices on a minimal no-network config
// (exactly as TestSetupAndStartServices_TaskExecutorLifecycleStoreWiring
// builds it) with a queued `next` task already in the task store, and uses
// the developer's test-only seam SetAfterStartSchedulersHookForTest — nil in
// production, called by setupAndStartServices right after startSchedulers
// returns and before setupMediaAndChannels — to run ONE real drain tick
// inside the boot window: CheckQueuedTasks on the real executor, the exact
// entry pkg/heartbeat's TaskDrainService calls on its checker. The row never
// calls ExpectSessionPlane itself: the point is that the gateway must.
//
// Oracles: the early tick dispatches nothing (a run created inside the window
// would run isolated and unclassified — the W1 defect); after the boot
// completes, one later tick through the REAL production drain
// (rs.TaskDrain.FireOnce) dispatches exactly one run, and that run's session
// has a persisted runtime classification.
//
// BLOCKED until the developer's seam lands: G1 fails at compile time on trees
// without SetAfterStartSchedulersHookForTest (the whole package's tests are
// blocked with it — the seam is one function, and this row is its consumer).
package gateway

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestSetupAndStartServices_DrainTickBeforePlaneWired_HoldsThenRunsOnce(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway:  config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Planning: config.PlanningConfig{GoalMaxRounds: 1, TaskMaxAttempts: 1},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// The queued task exists BEFORE the boot, so the drain's early tick has
	// something to wrongly dispatch (the W1 production scenario: a task
	// queued before the boot's schedulers start).
	tStore := agent.GetTaskStore(al)
	require.NotNil(t, tStore, "the agent loop owns the task store before boot")
	tsk := &task.Task{
		Title:       "gateway-drain-hold-g1",
		AgentID:     "mia",
		Status:      task.StatusNext,
		WorkspaceID: "gateway-drain-hold-g1-ws",
	}
	require.NoError(t, tStore.Create(tsk))
	taskID := tsk.ID

	// One real drain tick inside the boot window, through the developer's
	// test-only seam (nil in production). Run records are minted
	// synchronously at dispatch, so the count captured here is final for the
	// window no matter how long the window's turns run afterwards.
	earlyRuns := -1
	SetAfterStartSchedulersHookForTest(func(hookAL *agent.AgentLoop) {
		agent.GetTaskExecutor(hookAL).CheckQueuedTasks(context.Background())
		runs, listErr := agent.GetTaskStore(hookAL).ListRuns(taskID)
		if listErr != nil {
			earlyRuns = -2
			return
		}
		earlyRuns = len(runs)
	})
	t.Cleanup(func() { SetAfterStartSchedulersHookForTest(nil) })

	rs, err := setupAndStartServices(
		context.Background(),
		cfg,
		credentials.SecretBundle{},
		al,
		msgBus,
		tmpDir,
		newUnlockedStore(t, tmpDir),
		&SandboxApplyResult{},
		tools.NewBuiltinRegistry(),
		tools.NewMCPRegistry(),
		false, // allowGodMode
	)
	require.NoError(t, err, "setupAndStartServices must boot cleanly on a minimal config")
	t.Cleanup(func() { stopAndCleanupServices(rs, 5*time.Second, false) })

	require.GreaterOrEqual(t, earlyRuns, 0, "G1: the seam's tick must be able to read the run history")
	require.Equal(t, 0, earlyRuns,
		"G1: the drain tick inside the boot window must dispatch nothing — a run created "+
			"there went isolated and without its runtime classification, the W1 defect the "+
			"gateway's ExpectSessionPlane call prevents")

	// After the boot completes, one later tick through the REAL production
	// drain dispatches exactly one run.
	rs.TaskDrain.FireOnce(context.Background())
	require.Eventually(t, func() bool {
		runs, listErr := tStore.ListRuns(taskID)
		return listErr == nil && len(runs) == 1
	}, 10*time.Second, 20*time.Millisecond,
		"G1: the post-boot drain tick dispatches exactly one run for the queued task")
	runs, listErr := tStore.ListRuns(taskID)
	require.NoError(t, listErr)
	require.Len(t, runs, 1)

	// The dispatched run's session carries the persisted runtime
	// classification, read through a FRESH store over the directory the boot
	// wired (the lifecycle-wiring test's observable-on-disk pattern).
	lifecycleStore := session.NewLifecycleStore(filepath.Join(tmpDir, "session_lifecycle"))
	rec, loadErr := lifecycleStore.Load(runs[0].SessionID)
	require.NoError(t, loadErr,
		"G1: the drained task's session must have a persisted runtime classification "+
			"(session.ErrLifecycleNotFound means the run dispatched without its classification)")
	require.NotNil(t, rec)

	// Teardown race guard (the lifecycle-wiring test's pattern): the
	// dispatched turn keeps writing after FireOnce returns; the mock provider
	// finishes quickly, so this clears well inside teardown.
	t.Cleanup(func() {
		require.Eventually(t, func() bool {
			fresh, getErr := tStore.Get(taskID)
			return getErr == nil && (fresh.Status == task.StatusDone || fresh.Status == task.StatusFailed)
		}, 10*time.Second, 20*time.Millisecond,
			"task goroutine must reach a terminal state before test teardown")
	})
}
