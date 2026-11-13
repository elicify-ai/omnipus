package gateway

// Oracle: issue #1760 and the precedent in plan_engine_reload_1725_test.go /
// t8_scheduler_lifecycle_test.go (TK-30, 18.40). A configuration reload must
// keep the live cron service and the live /loop scheduler: stopping them
// cancels every running heartbeat, scheduled-task and /loop turn and drops the
// jobs queued behind a full lane. Real shutdown still stops them.
//
// Real gateway boot and reload functions; the only fake is the cron runner, so
// a run's context can be observed.

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// gate1760 blocks each run until released; it records which jobs started and
// the context error each run saw when it ended.
type gate1760 struct {
	mu       sync.Mutex
	started  map[string]bool
	ctxErrs  map[string]error
	release  chan struct{}
	relOnce  sync.Once
	finished map[string]bool
}

func newGate1760() *gate1760 {
	return &gate1760{
		started:  map[string]bool{},
		ctxErrs:  map[string]error{},
		finished: map[string]bool{},
		release:  make(chan struct{}),
	}
}

func (g *gate1760) open() { g.relOnce.Do(func() { close(g.release) }) }

func (g *gate1760) RunScheduled(ctx context.Context, job *cron.CronJob) (string, error) {
	g.mu.Lock()
	g.started[job.ID] = true
	g.mu.Unlock()
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	g.mu.Lock()
	g.ctxErrs[job.ID] = ctx.Err()
	g.finished[job.ID] = true
	g.mu.Unlock()
	return "", nil
}

func (g *gate1760) startedCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.started)
}

func (g *gate1760) ctxErr(id string) (error, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.ctxErrs[id]
	return e, ok
}

// stopSchedulersKeptByReload finishes a fixture whose final cleanup used the
// reload mode of stopAndCleanupServices: reload mode deliberately leaves the
// cron service and the loop scheduler running (#1760), so a test that ends with
// it must stop them itself or they outlive the test.
func stopSchedulersKeptByReload(rs *services) {
	if rs == nil {
		return
	}
	if rs.CronService != nil {
		rs.CronService.Stop()
	}
	if rs.LoopScheduler != nil {
		rs.LoopScheduler.Stop()
	}
}

func wait1760(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type fixture1760 struct {
	home    string
	path    string
	cfg     *config.Config
	msgBus  *bus.MessageBus
	rs      *services
	al      *agent.AgentLoop
	gate    *gate1760
	jobs    []string
	stopped bool // set once the test has run the real shutdown itself
}

// boot1760 boots real services with the lane capacity given, swaps the cron
// runner for the gate and adds nJobs jobs that are due on every tick.
func boot1760(t *testing.T, laneCap, nJobs int) *fixture1760 {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	path := filepath.Join(home, "config.json")
	seed := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents:  config.AgentsConfig{Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"), MaxTokens: 4096}},
	}
	seed.Schedules.MaxConcurrentRuns = laneCap
	require.NoError(t, config.SaveConfig(path, seed))
	creds := newUnlockedStore(t, home)
	cfg, err := config.LoadConfigWithStore(path, creds)
	require.NoError(t, err)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	rs, err := setupAndStartServices(context.Background(), cfg, credentials.SecretBundle{}, al, msgBus, home, creds, &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
	require.NoError(t, err)
	f := &fixture1760{al: al, home: home, path: path, cfg: cfg, msgBus: msgBus, rs: rs, gate: newGate1760()}
	t.Cleanup(func() {
		f.gate.open()
		if !f.stopped {
			stopAndCleanupServices(f.rs, 5*time.Second, false)
		}
	})
	rs.loadConfigForSwap = newReloadConfigLoader(path, home, creds, rs)

	require.NotNil(t, rs.CronService, "instrument: boot must start a cron service")
	rs.CronService.SetRunner(f.gate)
	every := int64(20)
	for i := 0; i < nJobs; i++ {
		j, addErr := rs.CronService.AddJobFull(cron.JobSpec{
			Name:     "reload-1760",
			Schedule: cron.CronSchedule{Kind: "every", EveryMS: &every},
			Message:  "m",
			AgentID:  "u6-test-owner",
		})
		require.NoError(t, addErr)
		f.jobs = append(f.jobs, j.ID)
	}
	return f
}

// A running scheduled job must keep its live context across a reload.
func TestReload_KeepsCronService_RunningJobIsNotCancelled_1760(t *testing.T) {
	f := boot1760(t, 8, 1)
	original := f.rs.CronService
	wait1760(t, "the scheduled job to start", func() bool { return f.gate.startedCount() == 1 })

	provider := providers.LLMProvider(&restMockProvider{})
	al := f.al
	require.NoError(t, handleConfigReload(context.Background(), al, f.cfg, &provider, f.rs, f.msgBus, true))

	assert.Same(t, original, f.rs.CronService, "#1760: reload must keep the one live cron service (no Stop/rebuild/Start)")
	if ra := f.rs.restAPIRef; ra != nil {
		assert.Same(t, original, ra.cronService.Load(), "the REST handle must still point at the live service")
	}
	time.Sleep(200 * time.Millisecond) // let a (buggy) cancel land
	_, finished := f.gate.ctxErr(f.jobs[0])
	assert.False(t, finished, "#1760: the running job ended during a reload (its context was cancelled)")
	f.gate.open()
	wait1760(t, "the run to end", func() bool { _, ok := f.gate.ctxErr(f.jobs[0]); return ok })
	err, _ := f.gate.ctxErr(f.jobs[0])
	assert.NoError(t, err, "#1760: the running job's context was cancelled by the reload")
}

// With the lane full (cap 1) a second due job waits for a slot. A reload must
// neither drop it nor cancel the run holding the slot.
func TestReload_KeepsCronService_QueuedJobSurvives_1760(t *testing.T) {
	f := boot1760(t, 1, 2)
	wait1760(t, "one job to run and the other to queue", func() bool { return f.gate.startedCount() == 1 })

	provider := providers.LLMProvider(&restMockProvider{})
	al := f.al
	require.NoError(t, handleConfigReload(context.Background(), al, f.cfg, &provider, f.rs, f.msgBus, true))

	f.gate.open() // the first run ends, the queued one takes the slot
	wait1760(t, "the queued job to start on the SAME live service after the reload", func() bool {
		return f.gate.startedCount() == 2
	})
}

// The /loop scheduler is not rebuilt by a reload either.
func TestReload_KeepsLoopScheduler_1760(t *testing.T) {
	f := boot1760(t, 8, 0)
	original := f.rs.LoopScheduler
	require.NotNil(t, original, "instrument: boot must start a loop scheduler")
	al := f.al

	provider := providers.LLMProvider(&restMockProvider{})
	require.NoError(t, handleConfigReload(context.Background(), al, f.cfg, &provider, f.rs, f.msgBus, true))

	assert.Same(t, original, f.rs.LoopScheduler, "#1760: reload must keep the one live loop scheduler")
	assert.Same(t, original, al.LoopScheduler(), "the agent loop must still hold the live loop scheduler")

	// Identity is not liveness (CHECK round 1, finding 1): a stopped scheduler
	// keeps its pointers. Drive a REAL dispatch through the kept instance. The
	// job names a session that does not exist, so a running scheduler fires it,
	// finds no session and removes the job; a stopped one ignores RunDueJobs and
	// the job stays.
	_, addErr := original.AddInterval("u6-test-owner", "ghost-session-1760", int64(time.Hour/time.Millisecond))
	require.NoError(t, addErr)
	require.Len(t, original.ListEnabledJobs(), 1, "instrument: the probe job is registered")
	original.RunDueJobs(time.Now().Add(2 * time.Hour))
	original.WaitForLane()
	assert.Empty(t, original.ListEnabledJobs(),
		"#1760: the loop scheduler kept across a reload must still be running and dispatch due jobs")
}

// The kept loop scheduler's own background run loop (not only a manual
// RunDueJobs) keeps firing due jobs after a reload: a due one-shot for a session
// that does not exist is picked up by the scheduler's timer and removed.
func TestReload_LoopSchedulerTimerKeepsFiring_1760(t *testing.T) {
	f := boot1760(t, 8, 0)
	ls := f.rs.LoopScheduler
	require.NotNil(t, ls)
	provider := providers.LLMProvider(&restMockProvider{})
	require.NoError(t, handleConfigReload(context.Background(), f.al, f.cfg, &provider, f.rs, f.msgBus, true))
	assert.Same(t, ls, f.al.LoopScheduler())
	_, err := ls.AddOneShot("u6-test-owner", "ghost-session-1760b", 300)
	require.NoError(t, err)
	wait1760(t, "the live loop scheduler to fire the due one-shot by itself", func() bool {
		return len(ls.ListEnabledJobs()) == 0
	})
}

// Guard (green before and after the fix): a real shutdown still cancels a
// running scheduled job and returns.
func TestShutdown_StillStopsCronService_AndCancelsRunningJob_1760(t *testing.T) {
	f := boot1760(t, 8, 1)
	wait1760(t, "the scheduled job to start", func() bool { return f.gate.startedCount() == 1 })

	f.stopped = true
	done := make(chan struct{})
	go func() { stopAndCleanupServices(f.rs, 5*time.Second, false); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("shutdown did not return")
	}
	err, finished := f.gate.ctxErr(f.jobs[0])
	require.True(t, finished, "shutdown must end the running job")
	assert.ErrorIs(t, err, context.Canceled, "shutdown must cancel the running job's context")
}
