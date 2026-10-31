package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const u2bAsyncEvidence = "finished the scheduled work and verified its result"

// Only the external provider is held. Real ExecuteTask/SpawnTriggeredRun,
// RunScheduled and the cron lane all return while this real task run is open.
// Unlike crashRunRecorder, this does not block inside the dispatch function.
// Cancellation is deliberately real, so an orderly Stop can unwind the run.
type u2bAsyncWorker struct {
	providers.LLMProvider
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func newU2bAsyncWorker() *u2bAsyncWorker {
	return &u2bAsyncWorker{
		LLMProvider: newClaimingWorker(turnClaimMet(u2bAsyncEvidence)),
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (w *u2bAsyncWorker) Chat(
	ctx context.Context, msgs []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]any,
) (*providers.LLMResponse, error) {
	w.enteredOnce.Do(func() { close(w.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.release:
		return w.LLMProvider.Chat(ctx, msgs, tools, model, opts)
	}
}

func (w *u2bAsyncWorker) complete() {
	w.releaseOnce.Do(func() { close(w.release) })
}

type u2bAsyncProcess struct {
	home      string
	loop      *AgentLoop
	scheduler *TaskTriggerScheduler
	clock     *triggerFakeClock
	stopOnce  sync.Once
}

// Reuses the real native-completion fixtures, but roots BOTH tasks and paired
// goals under one explicit durable home so fresh loops can reboot that home.
func newU2bAsyncProcess(t *testing.T, home string, clk *triggerFakeClock, worker *u2bAsyncWorker) *u2bAsyncProcess {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	workerHome := filepath.Join(home, "native-agent")
	require.NoError(t, os.MkdirAll(workerHome, 0o700))
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{
				{ID: "native-agent", Name: "Native Agent", Type: config.AgentTypeWorker, Home: workerHome},
				judgeAgentConfigForTaskTests(t),
			},
		},
	}
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), worker)
	bindMetSoftTierJudge(t, al)
	installTestSessionLauncher(t, al)
	sched := NewTaskTriggerScheduler(filepath.Join(home, "tasks_triggers", "jobs.json"), al.taskStore, al.taskExecutor)
	sched.SetClock(clk)
	al.SetTaskTriggerScheduler(sched)
	p := &u2bAsyncProcess{home: home, loop: al, scheduler: sched, clock: clk}
	t.Cleanup(p.stop)
	return p
}

// Same relevant component order as gateway/shutdown.go::runGracefulShutdown:
// stop the task-trigger scheduler first, then Stop/Close the loop. The real
// provider observes cancellation. Never close an old process over a later boot.
func (p *u2bAsyncProcess) stop() {
	p.stopOnce.Do(func() {
		p.scheduler.Stop()
		p.loop.Stop()
		p.loop.Close()
		p.loop.taskExecutor.wg.Wait()
	})
}

func (p *u2bAsyncProcess) physicalBoot(t *testing.T) {
	t.Helper()
	ReconcileInterruptedTasks(p.loop.taskStore)
	require.NoError(t, p.scheduler.PrepareAfterPhysicalBoot())
	require.NoError(t, p.scheduler.StartAfterPhysicalBoot())
	require.NoError(t, p.scheduler.Reconcile())
}

// A watchdog diagnoses a stuck fixture; it never drives task ordering. All
// successful paths synchronize on real completion, not elapsed time or sleeps.
func u2bAsyncJoin(t *testing.T, label string, join func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		join()
		close(done)
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatalf("fixture: %s did not return; dispatch must not wait for provider completion", label)
	}
}

func (p *u2bAsyncProcess) runDueAndJoin(t *testing.T) {
	t.Helper()
	p.scheduler.RunDueJobs(p.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane", p.scheduler.WaitForLane)
	u2bAsyncJoin(t, "real task execution", p.loop.taskExecutor.wg.Wait)
}

type u2bAsyncFire struct {
	taskID string
	jobID  string
	run    task.TaskRun
}

func (p *u2bAsyncProcess) fireWhileProviderHeld(t *testing.T, worker *u2bAsyncWorker) u2bAsyncFire {
	t.Helper()
	require.NoError(t, p.scheduler.Start())
	tk := newCompletionContractTask(t, p.loop, "native-agent", "U2b asynchronous once task")
	fireAt := p.clock.Now().Add(time.Minute).UnixMilli()
	trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &fireAt}}
	stored, err := p.loop.taskStore.Update(tk.ID, task.Patch{Trigger: &trigger})
	require.NoError(t, err)
	p.loop.NotifyTaskUpserted(stored)
	// Test the disk reader before trusting an empty result after settlement.
	jobs := p.storedJobs(t, tk.ID)
	require.Len(t, jobs, 1, "instrument: the once job is durably armed before firing")

	p.clock.Advance(2 * time.Minute)
	p.scheduler.RunDueJobs(p.clock.Now())
	u2bAsyncJoin(t, "cron dispatch lane while the provider is held", p.scheduler.WaitForLane)
	u2bAsyncJoin(t, "the real task's first provider call", func() { <-worker.entered })
	active, err := p.loop.taskStore.Get(tk.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusInProgress, active.Status, "instrument: dispatch returned but the task is still active")
	runs := p.runs(t, tk.ID)
	require.Len(t, runs, 1, "instrument: actual execution opened exactly one TaskRun")
	require.Equal(t, task.StatusInProgress, runs[0].Status)
	require.Nil(t, runs[0].EndedAt)
	require.NotEmpty(t, runs[0].SessionID, "instrument: this is a real session-backed task run, not a dispatch recorder")
	return u2bAsyncFire{taskID: tk.ID, jobID: jobs[0].ID, run: runs[0]}
}

func (p *u2bAsyncProcess) runs(t *testing.T, taskID string) []task.TaskRun {
	t.Helper()
	runs, err := p.loop.taskStore.ListRuns(taskID)
	require.NoError(t, err)
	return runs
}

func (p *u2bAsyncProcess) storedJobs(t *testing.T, taskID string) []cron.CronJob {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.home, "tasks_triggers", "jobs.json"))
	require.NoError(t, err)
	var store cron.CronStore
	require.NoError(t, json.Unmarshal(data, &store))
	var jobs []cron.CronJob
	for _, job := range store.Jobs {
		if job.Payload.Message == taskID {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// Capture bytes exactly as the live run left them. No fabricated Running bit,
// manual TaskRun, reset task status, or repaired job is inserted into the image.
func u2bAsyncSnapshot(t *testing.T, home string) map[string][]byte {
	t.Helper()
	image := make(map[string][]byte)
	err := fs.WalkDir(os.DirFS(home), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("fixture: cannot snapshot non-regular entry %s", path)
		}
		data, readErr := os.ReadFile(filepath.Join(home, path))
		if readErr != nil {
			return readErr
		}
		image[path] = data
		return nil
	})
	require.NoError(t, err)
	return image
}

// Called only after the old scheduler, executor and loop have all been joined.
func u2bAsyncRestore(t *testing.T, home string, image map[string][]byte) {
	t.Helper()
	require.NoError(t, os.RemoveAll(home))
	require.NoError(t, os.MkdirAll(home, 0o700))
	for path, data := range image {
		dest := filepath.Join(home, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o700))
		require.NoError(t, os.WriteFile(dest, data, 0o600))
	}
}
