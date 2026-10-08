// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// UAT W-03 (build aec77f9a3): a person pressed Stop on a running task-origin
// run and was refused with "Stop: selected execution has no owned disposition
// barrier"; the run went on and the session stayed `working`. Every task run
// (a Calendar tick or Run-now through ExecuteTask, and the launcher front
// StartTaskNow) must own an execution disposition, so the one Stop can find the
// barrier, interrupt the live turn and land `stopped`.
//
// Only the external model provider is controlled. Launch/Dispatch, admission,
// the task executor, StopSession and the lifecycle store are all real.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const taskStopWorkerID = "native-agent"

type taskStopFront string

const (
	// taskFrontExecute is the Calendar tick / Run-now / queue-drain front:
	// ExecuteTask -> runTask, no steering launcher involved.
	taskFrontExecute taskStopFront = "execute_task"
	// taskFrontLauncher is StartTaskNow through the real SteerLauncher:
	// Launch + Dispatch -> dispatchSteeredSessionWithReservation's task branch.
	taskFrontLauncher taskStopFront = "start_task_now_launcher"
)

type taskStopFixture struct {
	al       *AgentLoop
	te       *TaskExecutor
	provider *r1CompletionProvider
	task     *task.Task
	parent   string
	// sessionID is the task run's own session (set by start).
	sessionID string
}

// newTaskStopFixture builds a real AgentLoop with a real TaskExecutor and one
// task whose worker turn blocks in the provider until released or cancelled.
func newTaskStopFixture(t *testing.T, front taskStopFront, withParent bool, answers ...string) *taskStopFixture {
	t.Helper()
	return newTaskStopFixtureWith(t, front, withParent, nil, answers...)
}

// newTaskStopFixtureWith is newTaskStopFixture with a config mutation applied
// before the loop is built (the external-CLI worker case).
func newTaskStopFixtureWith(t *testing.T, front taskStopFront, withParent bool, mutateCfg func(*config.Config), answers ...string) *taskStopFixture {
	t.Helper()
	provider := &r1CompletionProvider{answers: answers, entered: make(chan int, len(answers)+4)}
	for range answers {
		provider.release = append(provider.release, make(chan struct{}))
	}
	t.Cleanup(provider.openAll)
	// A loop whose config seeds goal_claim "allow" (a task worker can only
	// finish by calling it), then the same steered-admission wiring newSteerAL
	// gives its loop: lifecycle + inbox stores and one genuinely minted boot
	// epoch.
	al, _ := newGoalLoopTestLoop(t, provider, mutateCfg)
	home := al.GetConfig().Agents.Defaults.Home
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), lifecycle)
	epochDir := filepath.Join(home, "boot_epoch")
	require.NoError(t, os.MkdirAll(epochDir, 0o700))
	boot := session.NewBootEpochStore(epochDir)
	epoch, err := boot.Mint()
	require.NoError(t, err)
	require.NotZero(t, epoch)
	al.SetBootEpochStore(boot)
	wireSteerCompletionDeps(t, al)

	te := &TaskExecutor{
		agentLoop: al, store: al.taskStore,
		running: make(map[string]*taskSlot), dispatchSema: newDispatchSemaphore(4),
	}
	te.SetLifecycleStore(al.GetSessionLifecycleStore())
	if front == taskFrontLauncher {
		te.SetSessionLauncher(NewSteerLauncher(al))
	}
	al.taskExecutor = te
	t.Cleanup(func() { te.Drain(10 * time.Second) })
	// Registered after the Drain above so it runs first: a run still blocked in
	// the provider is released before the executor waits for it.
	t.Cleanup(provider.openAll)

	f := &taskStopFixture{al: al, te: te, provider: provider}
	tk := &task.Task{
		Title: "stop barrier task", Prompt: "write something long", Action: task.ActionLLM,
		AgentID: taskStopWorkerID, WorkspaceID: "ws-task-stop", Owner: "owner",
		Status: task.StatusNext,
	}
	if withParent {
		f.parent = newTestSteeringSession(t, al, "ws-task-stop")
		tk.OriginSessionID = f.parent
	}
	require.NoError(t, al.taskStore.Create(tk), "create the real task record")
	f.task = tk
	return f
}

// start launches the task through the front under test and waits until the
// worker's first provider call is in flight (the run is genuinely live).
func (f *taskStopFixture) start(t *testing.T, front taskStopFront) {
	t.Helper()
	switch front {
	case taskFrontExecute:
		require.NoError(t, f.te.ExecuteTask(context.Background(), f.task.ID, nil))
	case taskFrontLauncher:
		inProgress := task.StatusInProgress
		_, err := f.al.taskStore.Update(f.task.ID, task.Patch{Status: &inProgress})
		require.NoError(t, err)
		_, err = f.te.StartTaskNow(context.Background(), f.task.ID)
		require.NoError(t, err)
	default:
		t.Fatalf("unknown front %q", front)
	}
	r1AwaitProvider(t, f.provider, 0)
	got, err := f.al.taskStore.Get(f.task.ID)
	require.NoError(t, err)
	require.NotEmpty(t, got.SessionID, "the task run must have bound its session")
	f.sessionID = got.SessionID
}

func (f *taskStopFixture) pressStop(t *testing.T) StopResult {
	t.Helper()
	res, err := f.al.StopSession(context.Background(), StopRequest{
		SessionID: f.sessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "person"},
		HooksFor: func(string) CancelHooks { return CancelHooks{} },
	})
	require.NoError(t, err, "a person's Stop on a running task must start")
	return res
}

// awaitRunJoined waits for the task executor's goroutines (and so the run's
// whole tail, including the release callback) to finish.
func (f *taskStopFixture) awaitRunJoined(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	var once sync.Once
	go func() { f.te.wg.Wait(); once.Do(func() { close(done) }) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the task run did not finish after the Stop (turn not interrupted or tail stuck)")
	}
	joinGoalFixtureRuns(t, f.al)
}

func (f *taskStopFixture) requireLifecycleStopped(t *testing.T) *session.LifecycleRecord {
	t.Helper()
	rec := rootReopenedRecord(t, f.al, f.sessionID)
	require.Equal(t, session.LifecycleStopped, rec.State,
		"the person's Stop must end the run `stopped`, not leave it %q (fence=%v failed_reason=%q)", rec.State, rec.Stop, rec.FailedReason)
	require.Nil(t, rec.Stop, "a landed Stop clears its fence")
	require.NotNil(t, rec.StopNote, "a landed Stop carries its note")
	require.Equal(t, session.StopCauseStop, rec.StopNote.Cause)
	return rec
}

// requireTaskEndedStoppedNotRestarted pins the task executor's own half of a
// person's Stop: the task ends `failed` with its "Stopped: ..." reason (a stop
// is not a broken run), spends no attempt, and is never restarted into a second
// session.
func (f *taskStopFixture) requireTaskEndedStoppedNotRestarted(t *testing.T) {
	t.Helper()
	got, err := f.al.taskStore.Get(f.task.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, got.Status, "a stopped run ends the task failed, never done or running (result=%q)", got.Result)
	require.True(t, strings.HasPrefix(got.Result, "Stopped"), "the task's reason must say it was stopped, got %q", got.Result)
	records, err := f.al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	require.NoError(t, err)
	runs := 0
	for _, rec := range records {
		if rec.Origin != nil && rec.Origin.TaskID == f.task.ID {
			runs++
		}
	}
	require.Equal(t, 1, runs, "the Stop must not restart the task into another session")
}

func requireStopReachedOnly(t *testing.T, res StopResult, sessionID string) {
	t.Helper()
	require.Empty(t, res.RootErr, "Stop on the task run must not be refused")
	require.Empty(t, res.Report.Unreachable, "nothing may be unreachable")
	require.Equal(t, []string{sessionID}, res.Report.Reached, "the task run is reached exactly once")
	requireNoSessionTwice(t, res.Report)
}

// requireNoSessionTwice pins the summary's arithmetic input: one session is
// either reached or unreachable, never both, and never listed twice.
func requireNoSessionTwice(t *testing.T, report steer.CancelReport) {
	t.Helper()
	seen := map[string]string{}
	for _, id := range report.Reached {
		require.NotContains(t, seen, id, "session %q listed twice", id)
		seen[id] = "reached"
	}
	for _, u := range report.Unreachable {
		require.NotContains(t, seen, u.ID, "session %q counted both reached and unreachable (or twice)", u.ID)
		seen[u.ID] = "unreachable"
	}
}

func TestTaskRunStop_LandsStopped_EveryFront(t *testing.T) {
	for _, tc := range []struct {
		name       string
		front      taskStopFront
		withParent bool
	}{
		{"calendar_tick_execute_task", taskFrontExecute, false},
		{"start_task_now_launcher_ordinary_root", taskFrontLauncher, false},
		{"start_task_now_launcher_steered_by_chat", taskFrontLauncher, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTaskStopFixture(t, tc.front, tc.withParent, "a long answer that never arrives")
			f.start(t, tc.front)

			res := f.pressStop(t)

			requireStopReachedOnly(t, res, f.sessionID)
			f.awaitRunJoined(t)
			f.requireLifecycleStopped(t)
			f.requireTaskEndedStoppedNotRestarted(t)
			require.Len(t, f.provider.Requests(), 1, "the interrupted turn must not be restarted or retried after the Stop")
		})
	}
}

// TestTaskRunStop_GoalDrivenRun_IsNotRestartedByTheGoalLoop: a task is a goal.
// Its first turn ends without a claim, so the run loop re-prompts it inside
// the same run (one goal try); the person's Stop lands on that second turn.
// The goal loop must not run a third turn or redispatch the task.
func TestTaskRunStop_GoalDrivenRun_IsNotRestartedByTheGoalLoop(t *testing.T) {
	for _, front := range []taskStopFront{taskFrontExecute, taskFrontLauncher} {
		t.Run(string(front), func(t *testing.T) {
			f := newTaskStopFixture(t, front, false, "progress so far, no claim yet", "second try that never arrives", "a third turn must never run")
			f.start(t, front)
			f.provider.open(0)
			r1AwaitProvider(t, f.provider, 1)

			res := f.pressStop(t)

			requireStopReachedOnly(t, res, f.sessionID)
			f.awaitRunJoined(t)
			f.requireLifecycleStopped(t)
			f.requireTaskEndedStoppedNotRestarted(t)
			require.Len(t, f.provider.Requests(), 2, "the goal loop must not run another turn after the Stop")
		})
	}
}

// TestTaskRunDispatchFailure_ReleasesItsSlotAndOwner: a task whose dispatch is
// refused after the launcher reserved its slot and attached the run's owner
// (here: the executor already holds a run for that task) must give both back.
// The steered cap is one, so a leaked slot would queue the next task forever;
// the proof is that a second task launched afterwards actually runs.
func TestTaskRunDispatchFailure_ReleasesItsSlotAndOwner(t *testing.T) {
	f := newTaskStopFixture(t, taskFrontLauncher, false, "the second task's turn")
	inProgress := task.StatusInProgress
	_, err := f.al.taskStore.Update(f.task.ID, task.Patch{Status: &inProgress})
	require.NoError(t, err)
	f.te.mu.Lock()
	f.te.running[f.task.ID] = &taskSlot{}
	f.te.mu.Unlock()

	_, err = f.te.StartTaskNow(context.Background(), f.task.ID)
	require.Error(t, err, "the refused dispatch must be reported to the caller")
	require.Contains(t, err.Error(), "already running")

	got, err := f.al.taskStore.Get(f.task.ID)
	require.NoError(t, err)
	require.NotEmpty(t, got.SessionID, "the launch happened before the refusal")
	rec := rootReopenedRecord(t, f.al, got.SessionID)
	claim := f.al.executionClaimFor(rec)
	require.NotEmpty(t, claim.RunID, "the refused admission was stamped")
	gate := f.al.steerAdmission()
	gate.mu.Lock()
	activeSlots := len(gate.active)
	gate.mu.Unlock()
	require.Zero(t, activeSlots, "the refused task must not keep its steered slot")
	require.Nil(t, f.al.executionDispositionFor(claim), "the refused task must not keep its owner")

	second := &task.Task{
		Title: "second task", Prompt: "runs once the slot is free", Action: task.ActionLLM,
		AgentID: taskStopWorkerID, WorkspaceID: "ws-task-stop", Owner: "owner", Status: task.StatusNext,
	}
	require.NoError(t, f.al.taskStore.Create(second))
	_, err = f.al.taskStore.Update(second.ID, task.Patch{Status: &inProgress})
	require.NoError(t, err)
	_, err = f.te.StartTaskNow(context.Background(), second.ID)
	require.NoError(t, err)
	r1AwaitProvider(t, f.provider, 0)
}

// blockingExternalCLIDriver blocks inside Run until its context is cancelled,
// like a real external CLI process that only a kill ends.
type blockingExternalCLIDriver struct {
	*runner.FakeRunner
	entered chan struct{}
	once    sync.Once
}

func (d *blockingExternalCLIDriver) Run(ctx context.Context, _ runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.once.Do(func() { close(d.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestTaskRunStop_ExternalCLIWorker_LandsStopped: a task assigned to an
// external-CLI worker runs through processTaskDirectExternalCLI, not the native
// loop. Its turn must own the same barrier, or a person's Stop is refused for
// it exactly as in UAT W-03.
func TestTaskRunStop_ExternalCLIWorker_LandsStopped(t *testing.T) {
	driver := &blockingExternalCLIDriver{FakeRunner: runner.NewFakeRunner(), entered: make(chan struct{})}
	previous := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) { return driver, nil }
	t.Cleanup(func() { newExternalDriver = previous })

	f := newTaskStopFixtureWith(t, taskFrontExecute, false, func(cfg *config.Config) {
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == taskStopWorkerID {
				cfg.Agents.List[i].Subagents = &config.SubagentsConfig{
					Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
				}
			}
		}
	})
	require.NoError(t, f.te.ExecuteTask(context.Background(), f.task.ID, nil))
	select {
	case <-driver.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the external-CLI worker never started")
	}
	got, err := f.al.taskStore.Get(f.task.ID)
	require.NoError(t, err)
	f.sessionID = got.SessionID
	require.NotEmpty(t, f.sessionID)

	res := f.pressStop(t)

	requireStopReachedOnly(t, res, f.sessionID)
	f.awaitRunJoined(t)
	f.requireLifecycleStopped(t)
	f.requireTaskEndedStoppedNotRestarted(t)
}

// failingWorkerProvider blocks its one call until released (or cancelled) and
// then fails it, so a task run ends on its own - not through a Stop.
type failingWorkerProvider struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *failingWorkerProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return nil, errors.New("upstream model failed")
	}
}

func (*failingWorkerProvider) GetDefaultModel() string { return "failing-worker" }

// startSelfEndingRun starts a one-attempt task whose worker fails once
// released, and waits until that call is in flight.
func startSelfEndingRun(t *testing.T, front taskStopFront) (*taskStopFixture, *failingWorkerProvider) {
	t.Helper()
	f := newTaskStopFixture(t, front, false, "unused")
	inst, ok := f.al.GetRegistry().GetAgent(taskStopWorkerID)
	require.True(t, ok)
	p := &failingWorkerProvider{entered: make(chan struct{}), release: make(chan struct{})}
	inst.Provider = p
	t.Cleanup(func() {
		p.once.Do(func() {})
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	})
	one := 1
	oneP := &one
	_, err := f.al.taskStore.Update(f.task.ID, task.Patch{MaxAttempts: &oneP})
	require.NoError(t, err)
	if front == taskFrontLauncher {
		inProgress := task.StatusInProgress
		_, err = f.al.taskStore.Update(f.task.ID, task.Patch{Status: &inProgress})
		require.NoError(t, err)
		_, err = f.te.StartTaskNow(context.Background(), f.task.ID)
	} else {
		err = f.te.ExecuteTask(context.Background(), f.task.ID, nil)
	}
	require.NoError(t, err)
	select {
	case <-p.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker call never started")
	}
	got, err := f.al.taskStore.Get(f.task.ID)
	require.NoError(t, err)
	f.sessionID = got.SessionID
	return f, p
}

// TestTaskRunNeverEndsStuckWorking: a run's own ending (its terminal lifecycle
// write) and an accepted Stop race. Whichever wins, the session settles: a
// Stop accepted first is landed `stopped` by the run's owner after the
// rejected terminal write, a run that ended first stays terminal and the late
// Stop is skipped. It is never left `running` with a pending fence.
func TestTaskRunNeverEndsStuckWorking(t *testing.T) {
	for _, front := range []taskStopFront{taskFrontExecute, taskFrontLauncher} {
		t.Run(string(front)+"/stop_accepted_first", func(t *testing.T) {
			f, p := startSelfEndingRun(t, front)
			// Accept the Stop (fence + barrier bound) WITHOUT interrupting the
			// in-flight call, then let the run end on its own: its terminal
			// write meets the pending fence and is rejected.
			report, err := f.al.steerCanceller().StopTurnsWithCause(context.Background(), f.sessionID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "person"}, false, session.StopCauseStop,
				func(ctx context.Context, id string, generation int) (GenerationCancelResult, error) {
					_, _, retainErr := f.al.retainSelectedStop(ctx, id, generation, nil)
					return GenerationCancelResult{Found: true}, retainErr
				})
			require.NoError(t, err)
			require.Equal(t, []string{f.sessionID}, report.Reached)
			require.Empty(t, report.Unreachable)
			close(p.release)

			f.awaitRunJoined(t)
			f.requireLifecycleStopped(t)
		})
		t.Run(string(front)+"/run_ended_first", func(t *testing.T) {
			f, p := startSelfEndingRun(t, front)
			close(p.release)
			f.awaitRunJoined(t)
			ended := rootReopenedRecord(t, f.al, f.sessionID)
			require.True(t, ended.Terminal(), "the run ended on its own, so its record is terminal, got %q", ended.State)

			res, err := f.al.StopSession(context.Background(), StopRequest{
				SessionID: f.sessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "person"},
				HooksFor: func(string) CancelHooks { return CancelHooks{} },
			})
			require.NoError(t, err)
			require.Empty(t, res.Report.Unreachable)
			after := rootReopenedRecord(t, f.al, f.sessionID)
			require.Equal(t, ended.State, after.State, "a Stop after the run ended must not change its terminal record")
			require.Nil(t, after.Stop, "no fence may be left on a terminal record")
		})
	}
}

// TestBoardTaskRunStop_IsNotRefused: ExecuteBoardTask runs a board task's turn
// through processTaskDirect with no task-run owner. Its session has no
// lifecycle record (nothing mints one), so the one Stop takes the plain
// no-record path; a person's Stop must still interrupt the live turn and not be
// refused with the barrier error.
func TestBoardTaskRunStop_IsNotRefused(t *testing.T) {
	f := newTaskStopFixture(t, taskFrontExecute, false, "a board answer that never arrives")
	meta, err := f.al.GetAgentStore(taskStopWorkerID).NewSession(session.SessionTypeTask, "system", taskStopWorkerID)
	require.NoError(t, err)
	done := make(chan error, 1)
	f.al.ExecuteBoardTask(taskStopWorkerID, "board-task-1", meta.ID, "do the board work", func(_ string, runErr error) { done <- runErr })
	r1AwaitProvider(t, f.provider, 0)
	f.sessionID = meta.ID

	res, err := f.al.StopSession(context.Background(), StopRequest{
		SessionID: meta.ID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "person"},
		HooksFor: func(string) CancelHooks { return CancelHooks{} },
	})
	require.NoError(t, err)
	require.NoError(t, res.RootErr, "a Stop on a board-task run must not be refused")
	require.True(t, res.Fired, "the live turn must be interrupted")
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the board task's turn was not interrupted by the Stop")
	}
}
