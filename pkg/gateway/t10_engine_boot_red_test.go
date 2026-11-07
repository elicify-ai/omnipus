package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixture invokes the real gateway scheduler boot stage, not a test copy of
// its ordering. No engine is started here. Callers seed stores before invoking it.
func t10BootStage(t *testing.T) *setupAndStartServicesState {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096}, List: []config.AgentConfig{{ID: "mia"}}}}
	mb := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, mb, &restMockProvider{})
	st := &setupAndStartServicesState{ctx: context.Background(), cfg: cfg, agentLoop: al, msgBus: mb, homePath: home}
	t.Cleanup(func() {
		if st.runningServices != nil {
			stopAndCleanupServices(st.runningServices, 5*time.Second, false)
		}
		al.Close()
		mb.Close()
	})
	return st
}

func t10SeedRecurringTask(t *testing.T, st *setupAndStartServicesState) {
	t.Helper()
	rule, tz := "FREQ=DAILY;COUNT=5", "UTC"
	at := time.Now().Add(48 * time.Hour).UnixMilli()
	require.NoError(t, agent.GetTaskStore(st.agentLoop).Create(&task.Task{ID: "t10-boot-task", Title: "T10 trigger", AgentID: "mia", WorkspaceID: "default", Status: task.StatusDone, Trigger: &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &at, Tz: &tz}}}))
}

// Traces to: schedules-t10-spec-r6.md lines 580, 617; TDD 5/6/7, FR-01/04/22.
func TestT10_GatewayBootUsesExactlyOneEngineAndSharedStore(t *testing.T) {
	st := t10BootStage(t)
	t10SeedRecurringTask(t, st)
	_, stop, err := st.startSchedulers()
	require.NoError(t, err)
	require.False(t, stop)
	svc := st.runningServices
	require.NotNil(t, svc.CronService)
	require.NotNil(t, svc.TaskTrigger)
	require.NotNil(t, svc.LoopScheduler)
	// Inspect identity only; never call unexported methods using reflection.
	// A future equivalent injected interface may update this structural binding,
	// but the SAME engine and actual shared-job assertions must remain.
	for name, owner := range map[string]any{"task": svc.TaskTrigger, "loop": svc.LoopScheduler} {
		v := reflect.ValueOf(owner).Elem().FieldByName("cs")
		require.True(t, v.IsValid(), "RED binding: hosted %s scheduler's injected engine field must be bound", name)
		assert.Equal(t, reflect.ValueOf(svc.CronService).Pointer(), v.Pointer(), "%s owns a private engine", name)
	}
	_, err = svc.LoopScheduler.AddInterval("mia", "t10-loop-session", 3600000)
	require.NoError(t, err)
	got := map[string]int{}
	for _, j := range svc.CronService.ListJobs(true) {
		got[j.Payload.Kind]++
	}
	assert.Equal(t, 1, got["task"], "real task producer must write into the shared live engine")
	assert.Equal(t, 1, got["loop"], "real loop producer must write into the shared live engine")
	data, err := os.ReadFile(filepath.Join(st.homePath, "cron", "jobs.json"))
	require.NoError(t, err)
	var disk cron.CronStore
	require.NoError(t, json.Unmarshal(data, &disk))
	assert.Equal(t, svc.CronService.ListJobs(true), disk.Jobs, "shared persistence must contain actual producer jobs")
}

// Traces to: schedules-t10-spec-r6.md lines 631–636; TDD 9b/18 (T8 r17 18.39).
func TestT10_GatewayBootCheckedLoadPrecedesAllWrites(t *testing.T) {
	st := t10BootStage(t)
	t10SeedRecurringTask(t, st)
	path := filepath.Join(st.homePath, "cron", "jobs.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	damaged := []byte(`{"version":1,"jobs":[{"id":"damaged-t10","enabled":"invalid","payload":{"kind":"heartbeat"}}]}`)
	require.NoError(t, os.WriteFile(path, damaged, 0600))
	_, stop, err := st.startSchedulers()
	require.True(t, stop, "checked load error must reach the production boot caller")
	var original *json.UnmarshalTypeError
	require.ErrorAs(t, err, &original)
	assert.Contains(t, original.Field, "enabled")
	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, damaged, after, "no registration may mask the original checked-load error")
	if st.runningServices != nil {
		assert.Nil(t, st.runningServices.TaskTrigger, "task reconciliation/activation must not start after failed restoration")
		assert.Nil(t, st.runningServices.LoopScheduler, "loop recovery/activation must not start after failed restoration")
	}
}

type t10GatewayClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *t10GatewayClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *t10GatewayClock) advance(d time.Duration) { c.mu.Lock(); c.at = c.at.Add(d); c.mu.Unlock() }

type t10GatewayRunner struct {
	calls   atomic.Int32
	entered chan context.Context
	release chan struct{}
	fail    bool
}

func (r *t10GatewayRunner) RunScheduled(ctx context.Context, j *cron.CronJob) (string, error) {
	n := r.calls.Add(1)
	if n == 1 && r.entered != nil {
		r.entered <- ctx
		<-r.release
	}
	if r.fail {
		return "", &cron.TransientError{Err: errors.New("T10 retry diagnostic")}
	}
	return "completed-" + j.ID, nil
}
func t10GatewayKind(t *testing.T, s *cron.JobSpec, kind string) {
	t.Helper()
	v := reflect.ValueOf(s).Elem().FieldByName("Kind")
	if !v.IsValid() {
		t.Fatal("BLOCKED: JobSpec.Kind missing — S-C.3/S-D.5 cannot use a retired agent_turn producer")
	}
	v.SetString(kind)
}

// Traces to: schedules-t10-spec-r6.md lines 708–713; TDD 10, FR-26.
func TestReload_DoesNotRestartEngineOrStrandRunning(t *testing.T) {
	st := t10BootStage(t)
	cs, err := setupCronTool(st.agentLoop, st.msgBus, st.homePath, st.cfg, nil)
	require.NoError(t, err)
	clk := &t10GatewayClock{at: time.Now().Add(24 * time.Hour)}
	cs.SetClock(clk)
	work := &t10GatewayRunner{entered: make(chan context.Context, 1), release: make(chan struct{})}
	cs.SetRunner(work)
	var once sync.Once
	release := func() { once.Do(func() { close(work.release) }) }
	defer release()
	cs.SetStopDrainTimeout(time.Millisecond)
	require.NoError(t, cs.StartAfterPhysicalBoot())
	svc := &services{homePath: st.homePath, CronService: cs}
	st.runningServices = svc
	defer func() { release(); cs.Stop(); cs.WaitForLane() }()
	interval := int64(60000)
	spec := cron.JobSpec{Kind: cron.KindHeartbeat, Name: "reload-held", AgentID: "mia", Message: "held-heartbeat", Schedule: cron.CronSchedule{Kind: "every", EveryMS: &interval}}
	// Seed a typed persisted fixture on the pre-T10 engine. Kind-at-creation is
	// checked independently; it must not prevent exercising the real reload bug.
	j, err := cs.AddJobFull(spec)
	require.NoError(t, err)
	j.Payload.Kind = "heartbeat"
	require.NoError(t, cs.UpdateJob(j))
	clk.advance(time.Minute)
	cs.RunDueJobs(clk.Now())
	var runctx context.Context
	select {
	case runctx = <-work.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real handler did not enter")
	}
	running, ok := cs.GetJob(j.ID)
	require.True(t, ok)
	require.True(t, running.State.Running)
	// Real stop and restart scheduler phases of configuration reload; do NOT
	// pre-stop the engine in the test and thereby hide the prohibited cancellation.
	stopAndCleanupServices(svc, time.Second, true)
	assert.NoError(t, runctx.Err(), "reload must not cancel the admitted callback")
	rs := &restartServicesState{al: st.agentLoop, msgBus: st.msgBus, runningServices: svc, cfg: st.cfg}
	reloadErr, stop := rs.restartCron()
	require.NoError(t, reloadErr)
	require.False(t, stop)
	require.Same(t, cs, svc.CronService, "one process, one engine; retained Running on a successor is not settlement")
	release()
	cs.WaitForLane()
	settled, ok := cs.GetJob(j.ID)
	require.True(t, ok)
	assert.False(t, settled.State.Running)
	assert.Equal(t, "ok", settled.State.LastStatus)
	require.NotNil(t, settled.State.NextRunAtMS)
	clk.advance(time.Minute)
	cs.RunDueJobs(clk.Now())
	cs.WaitForLane()
	assert.Equal(t, int32(2), work.calls.Load(), "same recurring job must run at its next boundary, exactly once")
}

// Traces to: schedules-t10-spec-r6.md lines 638–643; TDD 9b. Binding gap, not coverage.
func TestT10_BootReconciliationsFinishBeforeFirstCollection(t *testing.T) {
	st := t10BootStage(t)
	t10SeedRecurringTask(t, st)
	var mu sync.Mutex
	events := []string{}
	collectKinds := map[string]int{}
	collected := make(chan struct{})
	var first sync.Once
	// Missing hook is a binding prerequisite only. The callback observes the
	// REAL gateway boot, never supplies a replacement boot implementation.
	observer, ok := any(st).(interface {
		SetSchedulerBootObserverForTest(func(string, *cron.CronService))
	})
	if !ok {
		t.Fatal("BLOCKED: pre-activation boot observer not implemented — S-B.4/FR-03; bind equivalent real stage/first-collection hooks")
	}
	observer.SetSchedulerBootObserverForTest(func(stage string, cs *cron.CronService) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, stage)
		if stage == "first_collection" {
			first.Do(func() {
				for _, j := range cs.ListJobs(true) {
					collectKinds[j.Payload.Kind]++
				}
				close(collected)
			})
		}
	})
	defer observer.SetSchedulerBootObserverForTest(nil)
	_, stop, err := st.startSchedulers()
	require.NoError(t, err)
	require.False(t, stop)
	select {
	case <-collected:
	case <-time.After(5 * time.Second):
		t.Fatal("real first collection was not observed after successful boot")
	}
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, events, 7, "every real boot phase once; no-op observer cannot pass")
	assert.Equal(t, []string{"checked_load", "clear_running"}, events[:2])
	assert.ElementsMatch(t, []string{"task_reconciled", "loop_reconciled", "heartbeat_reconciled"}, events[2:5], "spec requires all reconcilers before activation, not a relative order between them")
	assert.Equal(t, []string{"activate", "first_collection"}, events[5:])
	assert.Equal(t, 1, collectKinds["task"], "first collection sees real reconciled task, not an empty boot")
	assert.NotNil(t, st.runningServices.TaskTrigger)
	assert.NotNil(t, st.runningServices.LoopScheduler)
}

// t10SeedIntervalLoop creates a real chat session that carries persisted
// interval-loop state but NO scheduled step (the state a crash between the
// state write and the job write leaves). runCount/maxRuns select whether the
// loop still owes a step (0/10) or has exhausted its limit (10/10).
func t10SeedIntervalLoop(t *testing.T, st *setupAndStartServicesState, runCount, maxRuns int) string {
	t.Helper()
	store := st.agentLoop.GetSessionStore()
	require.NotNil(t, store, "SETUP: the shared session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	mode, every, prompt := "interval", int64(15*time.Minute/time.Millisecond), "boot recovery probe"
	require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{
		LoopMode: &mode, LoopPrompt: &prompt, LoopRunCount: &runCount, LoopMaxRuns: &maxRuns, LoopIntervalMS: &every,
	}))
	return meta.ID
}

// Traces to: schedules-t10-spec-r6.md FR-03/FR-05; CHECK2 F4. The observer
// label alone cannot prove recovery ran: the loop's own outcome must exist
// after the REAL gateway boot, and be visible to the first collection.
func TestT10_BootRecoversActiveLoopThroughGatewayStage(t *testing.T) {
	st := t10BootStage(t)
	sid := t10SeedIntervalLoop(t, st, 0, 10)
	var mu sync.Mutex
	loopsAtCollection := 0
	collected := make(chan struct{})
	var first sync.Once
	observer, ok := any(st).(interface {
		SetSchedulerBootObserverForTest(func(string, *cron.CronService))
	})
	require.True(t, ok, "BLOCKED: pre-activation boot observer not implemented")
	observer.SetSchedulerBootObserverForTest(func(stage string, cs *cron.CronService) {
		if stage != "first_collection" {
			return
		}
		first.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			for _, j := range cs.ListJobs(true) {
				if j.Payload.Kind == cron.KindLoop && j.Payload.Message == sid {
					loopsAtCollection++
				}
			}
			close(collected)
		})
	})
	defer observer.SetSchedulerBootObserverForTest(nil)

	before := time.Now().UnixMilli()
	_, stop, err := st.startSchedulers()
	require.NoError(t, err)
	require.False(t, stop)

	var loops []cron.CronJob
	for _, j := range st.runningServices.CronService.ListJobs(true) {
		if j.Payload.Kind == cron.KindLoop && j.Payload.Message == sid {
			loops = append(loops, j)
		}
	}
	require.Len(t, loops, 1, "boot recovery must re-arm the active loop with exactly one step")
	step := loops[0]
	assert.Equal(t, "every", step.Schedule.Kind)
	require.NotNil(t, step.Schedule.EveryMS)
	assert.Equal(t, int64(900000), *step.Schedule.EveryMS, "the re-armed step uses the persisted interval")
	assert.Equal(t, "mia", step.AgentID)
	require.NotNil(t, step.State.NextRunAtMS)
	assert.Greater(t, *step.State.NextRunAtMS, before, "the repaired step is in the future")
	meta, err := st.agentLoop.GetSessionStore().GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, step.ID, meta.LoopJobID, "the recovered step is recorded in the session's loop state")
	assert.Equal(t, "interval", meta.LoopMode, "an active loop stays active")

	select {
	case <-collected:
	case <-time.After(5 * time.Second):
		t.Fatal("real first collection was not observed after successful boot")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, loopsAtCollection, "the first collection sees the recovered loop step, not an unrecovered store")
}

// Traces to: schedules-t10-spec-r6.md FR-05(h); CHECK2 F4. A recovery write
// that fails must abort the REAL gateway boot, name the session, and leave the
// engine unactivated.
func TestT10_BootAbortsWhenLoopRecoveryCannotPersist(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind root; the injected write failure needs a non-root user")
	}
	st := t10BootStage(t)
	sid := t10SeedIntervalLoop(t, st, 10, 10) // limit reached: recovery must persist the stopped state
	var sessionDirs []string
	require.NoError(t, filepath.WalkDir(st.agentLoop.GetSessionStore().BaseDir(), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == sid {
			sessionDirs = append(sessionDirs, p)
		}
		return nil
	}))
	require.Len(t, sessionDirs, 1, "SETUP: exactly one session directory for the seeded loop")
	require.NoError(t, os.Chmod(sessionDirs[0], 0o555))
	t.Cleanup(func() { _ = os.Chmod(sessionDirs[0], 0o755) })

	var mu sync.Mutex
	var events []string
	observer, ok := any(st).(interface {
		SetSchedulerBootObserverForTest(func(string, *cron.CronService))
	})
	require.True(t, ok, "BLOCKED: pre-activation boot observer not implemented")
	observer.SetSchedulerBootObserverForTest(func(stage string, _ *cron.CronService) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, stage)
	})
	defer observer.SetSchedulerBootObserverForTest(nil)

	_, stop, err := st.startSchedulers()
	require.Error(t, err, "a loop recovery write failure must fail the boot")
	assert.True(t, stop)
	assert.Contains(t, err.Error(), "error recovering /loop schedules")
	assert.Contains(t, err.Error(), sid, "the failure names the session")
	mu.Lock()
	defer mu.Unlock()
	assert.NotContains(t, events, "loop_reconciled")
	assert.NotContains(t, events, "activate", "nothing may be activated after a failed recovery")
}
