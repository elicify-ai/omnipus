package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Issue #1490 (scheduler-consolidation-check.md, gap G1). The normal process
// shutdown, omnipusGracefulShutdown, must stop EVERY timer service before it
// drains active turns: the user-schedules CronService, the task-trigger copy
// and the /loop copy. Today it stops only the first, so a task trigger or a
// /loop that comes due during the drain can still start work.
//
// Oracle (the requirement, not the code): once shutdown has begun, a due timed
// job of either copy must not start. A job "started" is observed in the copy's
// persisted store: a stopped engine leaves its store untouched when it is
// offered due jobs, a running one clears the next-run time, runs the job and
// records the outcome.

const timerTestLoopSessionID = "timer-step0-loop-session"

// timerStoreSnapshot is the part of a cron store the tests compare.
type timerStoreSnapshot struct {
	Jobs []cron.CronJob
}

func readTimerStore(t *testing.T, path string) timerStoreSnapshot {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "the timer store must exist: %s", path)
	var s cron.CronStore
	require.NoError(t, json.Unmarshal(data, &s))
	return timerStoreSnapshot{Jobs: s.Jobs}
}

// bootTimerServices boots the real scheduler stage (the production
// startSchedulers) over a fresh home that holds one future recurring task and,
// after boot, one /loop interval job. Returns the stage, the agent loop and
// the two store paths.
func bootTimerServices(t *testing.T, provider providers.LLMProvider) (
	stage *setupAndStartServicesState, al *agent.AgentLoop, cfg *config.Config, taskStore, loopStore string,
) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tasks := task.New(filepath.Join(home, "tasks"))
	rule, tz := "FREQ=DAILY;COUNT=5", "UTC"
	due := time.Now().Add(48 * time.Hour).Truncate(time.Second).UnixMilli()
	require.NoError(t, tasks.Create(&task.Task{
		ID: "timer-step0-task", Title: "timer-step0-task", AgentID: "mia", WorkspaceID: "default",
		Status: task.StatusDone, // done + recurring: owns future occurrences, no queue drain
		Trigger: &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{
			Rrule: &rule, DtstartMs: &due, Tz: &tz,
		}},
	}))

	cfg = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "workspace"),
				DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al = mustAgentLoop(t, cfg, msgBus, provider)
	stage = &setupAndStartServicesState{
		ctx: context.Background(), cfg: cfg, agentLoop: al, msgBus: msgBus, homePath: home,
	}
	_, stop, err := stage.startSchedulers()
	if stage.runningServices != nil {
		t.Cleanup(func() { stopAndCleanupServices(stage.runningServices, 5*time.Second, false) })
	}
	require.NoError(t, err)
	require.False(t, stop)
	require.NotNil(t, stage.runningServices.TaskTrigger, "SETUP: task trigger copy must be running")
	require.NotNil(t, stage.runningServices.LoopScheduler, "SETUP: loop copy must be running")

	_, err = stage.runningServices.LoopScheduler.AddInterval("mia", timerTestLoopSessionID, int64(time.Hour/time.Millisecond))
	require.NoError(t, err)

	taskStore = filepath.Join(home, "tasks_triggers", "jobs.json")
	loopStore = filepath.Join(home, "loops", "jobs.json")
	require.Len(t, readTimerStore(t, taskStore).Jobs, 1, "SETUP: the task trigger copy holds the task's job")
	require.Len(t, readTimerStore(t, loopStore).Jobs, 1, "SETUP: the loop copy holds the loop job")
	return stage, al, cfg, taskStore, loopStore
}

// offerDueJobs offers both copies a moment when everything is due (a week
// ahead) and reports, per copy, whether the persisted store changed, i.e.
// whether a job was started.
func offerDueJobs(t *testing.T, rs *services, taskStore, loopStore string) (taskFired, loopFired bool) {
	t.Helper()
	beforeTask, beforeLoop := readTimerStore(t, taskStore), readTimerStore(t, loopStore)
	future := time.Now().Add(7 * 24 * time.Hour)
	rs.TaskTrigger.RunDueJobs(future)
	rs.LoopScheduler.RunDueJobs(future)
	rs.TaskTrigger.WaitForLane()
	rs.LoopScheduler.WaitForLane()
	afterTask, afterLoop := readTimerStore(t, taskStore), readTimerStore(t, loopStore)
	return !jobsEqual(beforeTask, afterTask), !jobsEqual(beforeLoop, afterLoop)
}

func jobsEqual(a, b timerStoreSnapshot) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// TestGracefulShutdown_StopsTaskTriggerAndLoopTimers: after the normal
// shutdown returns, a due task-trigger job and a due /loop job must not start.
func TestGracefulShutdown_StopsTaskTriggerAndLoopTimers(t *testing.T) {
	stage, al, cfg, taskStore, loopStore := bootTimerServices(t, &restMockProvider{})

	omnipusGracefulShutdown(stage.runningServices, al, nil, cfg)

	taskFired, loopFired := offerDueJobs(t, stage.runningServices, taskStore, loopStore)
	assert.False(t, taskFired,
		"#1490: after shutdown a due task-trigger job must not start; the task-trigger scheduler was left running")
	assert.False(t, loopFired,
		"#1490: after shutdown a due /loop job must not start; the loop scheduler was left running")
}

// holdingProvider blocks every call whose first user message is "HOLD" until
// release is closed, ignoring cancellation on purpose so the held turn keeps
// the shutdown drain waiting. Every other call answers at once.
type holdingProvider struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *holdingProvider) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	for _, m := range msgs {
		if m.Role == "user" && m.Content == "HOLD" {
			p.once.Do(func() { close(p.entered) })
			<-p.release
			break
		}
	}
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (p *holdingProvider) GetDefaultModel() string { return "test-model" }

// TestGracefulShutdown_StopsTimerCopiesBeforeTheTurnDrain: both extra timer
// copies must already be stopped by the time shutdown starts waiting for active
// turns. A held turn keeps the drain waiting; the test waits until the drain
// has closed turn intake (a fresh turn is refused), then offers due jobs.
func TestGracefulShutdown_StopsTimerCopiesBeforeTheTurnDrain(t *testing.T) {
	prov := &holdingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(prov.release) }) }
	t.Cleanup(release)

	stage, al, cfg, taskStore, loopStore := bootTimerServices(t, prov)

	heldDone := make(chan error, 1)
	go func() {
		_, err := al.ProcessDirectAs(context.Background(), "mia", "HOLD", "timer-held-session")
		heldDone <- err
	}()
	select {
	case <-prov.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: the held turn never reached the provider")
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		omnipusGracefulShutdown(stage.runningServices, al, nil, cfg)
	}()

	// Turn intake closes when shutdown reaches the drain (AgentLoop.Stop).
	require.Eventually(t, func() bool {
		_, err := al.ProcessDirectAs(context.Background(), "mia", "probe", "timer-probe-session")
		return err != nil
	}, 30*time.Second, 25*time.Millisecond, "SETUP: shutdown never closed turn intake")

	select {
	case <-shutdownDone:
		t.Fatal("SETUP: shutdown finished while a turn was held; the drain did not wait")
	default:
	}

	taskFired, loopFired := offerDueJobs(t, stage.runningServices, taskStore, loopStore)
	release()
	<-shutdownDone
	<-heldDone

	assert.False(t, taskFired,
		"#1490: the task-trigger copy must be stopped before the turn drain; a due job started while the drain waited")
	assert.False(t, loopFired,
		"#1490: the /loop copy must be stopped before the turn drain; a due job started while the drain waited")
}
