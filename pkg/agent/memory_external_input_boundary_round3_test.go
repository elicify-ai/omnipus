package agent

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Only the external process edge is substituted. Run receives the real runtime
// context and never emits output. Canceling that context does not close the
// process-exit stream until releaseExit: the production exit barrier therefore
// holds the real owner, queue consumer and continuation behind our assertions.
type memoryExternalInputRound3Driver struct {
	factories atomic.Int64
	runs      atomic.Int64
	resumes   atomic.Int64
	cancels   atomic.Int64
	inputs    atomic.Int64
	decisions atomic.Int64
	mu        sync.Mutex
	runCtx    context.Context
	workDir   string
	input     string
	started   chan struct{}
	startOnce sync.Once
	exit      chan struct{}
	exitOnce  sync.Once
}

type memoryExternalInputRound3Counts struct {
	factories int64
	runs      int64
	resumes   int64
	cancels   int64
	inputs    int64
	decisions int64
}

func (d *memoryExternalInputRound3Driver) Run(ctx context.Context, opts runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.runs.Add(1)
	d.mu.Lock()
	if d.runCtx == nil {
		d.runCtx = ctx
		d.workDir = opts.WorkDir
		d.input = opts.Input
	}
	d.mu.Unlock()
	stream := d.heldStream()
	d.startOnce.Do(func() { close(d.started) })
	return stream, nil
}

func (d *memoryExternalInputRound3Driver) Resume(context.Context, string, ...string) (<-chan runner.RunEvent, error) {
	d.resumes.Add(1)
	return d.heldStream(), nil
}

func (d *memoryExternalInputRound3Driver) heldStream() <-chan runner.RunEvent {
	stream := make(chan runner.RunEvent)
	go func() {
		<-d.exit
		close(stream)
	}()
	return stream
}

func (d *memoryExternalInputRound3Driver) releaseExit() {
	d.exitOnce.Do(func() { close(d.exit) })
}

func (d *memoryExternalInputRound3Driver) Cancel() {
	d.cancels.Add(1)
}

func (d *memoryExternalInputRound3Driver) Input(string) error {
	d.inputs.Add(1)
	return nil
}

func (d *memoryExternalInputRound3Driver) Decide(runner.PermissionDecision) {
	d.decisions.Add(1)
}

func (d *memoryExternalInputRound3Driver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{OK: true}
}

func (d *memoryExternalInputRound3Driver) counts() memoryExternalInputRound3Counts {
	return memoryExternalInputRound3Counts{
		factories: d.factories.Load(),
		runs:      d.runs.Load(),
		resumes:   d.resumes.Load(),
		cancels:   d.cancels.Load(),
		inputs:    d.inputs.Load(),
		decisions: d.decisions.Load(),
	}
}

func (d *memoryExternalInputRound3Driver) firstRun() (context.Context, string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runCtx, d.workDir, d.input
}

var _ runner.ExternalAgentRunner = (*memoryExternalInputRound3Driver)(nil)

type memoryExternalInputRound3Fixture struct {
	al        *AgentLoop
	provider  *memoryInputRound3Provider
	driver    *memoryExternalInputRound3Driver
	sessionID string
	parentID  string
	taskID    string
	agentID   string
}

func memoryExternalInputRound3Install(
	t *testing.T, al *AgentLoop, provider *memoryInputRound3Provider,
) *memoryExternalInputRound3Fixture {
	t.Helper()
	driver := &memoryExternalInputRound3Driver{started: make(chan struct{}), exit: make(chan struct{})}
	fixture := &memoryExternalInputRound3Fixture{al: al, provider: provider, driver: driver}
	previous := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		driver.factories.Add(1)
		return driver, nil
	}
	// Registered before Launch/StartTaskNow and their startup waits. Fixture
	// cleanup from the existing harness was registered earlier, so runs later.
	t.Cleanup(func() {
		fixture.stopAndJoin(t)
		newExternalDriver = previous
	})
	return fixture
}

func (f *memoryExternalInputRound3Fixture) stopAndJoin(t *testing.T) {
	t.Helper()
	if f.sessionID != "" {
		memoryInputRound3Stop(t, f.al, f.sessionID)
	}
	// Stop targets the selected execution. Cancel the genuine task parent's
	// context too during teardown, so a failing assertion cannot spend attempts
	// or redispatch while the process gate is being released. No path under
	// test is replaced: this is the real running task slot's cancellation.
	if f.taskID != "" {
		f.al.taskExecutor.mu.Lock()
		slot := f.al.taskExecutor.running[f.taskID]
		var cancel context.CancelFunc
		if slot != nil {
			cancel = slot.cancel
		}
		f.al.taskExecutor.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	f.driver.releaseExit()
	// Unlike a sampled Finished()/stopped flag or bounded Close warning,
	// these joins prove the whole task/steered owner's output tail has left
	// the stores before factory restoration and fixture directory cleanup.
	f.al.taskExecutor.wg.Wait()
	f.al.steerAdmission().turns.Wait()
	if f.sessionID != "" {
		rec, err := f.al.GetSessionLifecycleStore().Load(f.sessionID)
		if err != nil {
			t.Errorf("cleanup: load joined external session: %v", err)
		} else if rec == nil || rec.State != session.LifecycleStopped || rec.Stop != nil || rec.StopNote == nil {
			t.Errorf("cleanup: joined external owner must have landed its real Stop: %+v", rec)
		}
	}
}

func memoryExternalInputRound3HelperFixture(t *testing.T) *memoryExternalInputRound3Fixture {
	t.Helper()
	provider := &memoryInputRound3Provider{}
	al := newDelegateDispatchLoop(t, provider)
	memoryInputRound3SeedWorkspaces(t, delegateExtCLIAgentID)
	fixture := memoryExternalInputRound3Install(t, al, provider)
	fixture.agentID = delegateExtCLIAgentID
	parentID, child := memoryInputRound3LaunchHelper(t, al, fixture.agentID)
	fixture.parentID, fixture.sessionID = parentID, child.SessionID
	require.True(t, child.Is3P, "SETUP: Launch classifies the registered external target")
	dispatch, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err, "SETUP: actual external helper Dispatch")
	require.Equal(t, steer.DispatchRunning, dispatch.State)
	require.Equal(t, child.Generation, dispatch.Generation)
	fixture.awaitRealRun(t)
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.NotNil(t, rec)
	require.NotNil(t, rec.SteeredBy)
	require.Equal(t, parentID, rec.SteeredBy.SteeringSessionID)
	_, _, input := fixture.driver.firstRun()
	require.Contains(t, input, memoryInputRound3History, "SETUP: the real external launch instruction reached Run")
	memoryInputRound3AssertSetupOutbound(t, al)
	return fixture
}

func memoryExternalInputRound3TaskFixture(t *testing.T) *memoryExternalInputRound3Fixture {
	t.Helper()
	provider := &memoryInputRound3Provider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)
	memoryInputRound3SeedWorkspaces(t, "ext-agent")
	fixture := memoryExternalInputRound3Install(t, al, provider)
	fixture.agentID = "ext-agent"
	tk := createInProgressTask(t, al.taskStore, fixture.agentID, memoryInputRound3A)
	fixture.taskID = tk.ID
	sessionID, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
	fixture.sessionID = sessionID
	require.NoError(t, err, "SETUP: real StartTaskNow creates and dispatches the external task session")
	require.NotEmpty(t, sessionID)
	fixture.awaitRealRun(t)
	rec, err := al.GetSessionLifecycleStore().Load(sessionID)
	require.NoError(t, err)
	require.NotNil(t, rec)
	require.Nil(t, rec.SteeredBy, "SETUP: this must exercise the task branch, not a steered-helper branch")
	require.NotNil(t, rec.Origin)
	require.Equal(t, session.OriginKindTask, rec.Origin.Kind)
	require.Equal(t, tk.ID, rec.Origin.TaskID)
	require.Nil(t, al.pinnedSteeredRecord(sessionID))
	live := al.liveExternalTaskRecord(sessionID)
	require.NotNil(t, live, "SETUP: the distinct liveExternalTaskRecord branch is reachable")
	require.Equal(t, sessionID, live.SessionID)
	stored, err := al.taskStore.Get(tk.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, sessionID, stored.SessionID, "SETUP: genuine durable task/session binding")
	require.Equal(t, memoryInputRound3A, stored.WorkspaceID)
	require.Equal(t, task.StatusInProgress, stored.Status)
	memoryInputRound3AssertSetupOutbound(t, al)
	return fixture
}

func (f *memoryExternalInputRound3Fixture) awaitRealRun(t *testing.T) {
	t.Helper()
	select {
	case <-f.driver.started:
	case <-time.After(asyncTaskPollTimeout):
		t.Fatal("SETUP: genuine external dispatch never reached the held process edge")
	}
	ctx, workDir, input := f.driver.firstRun()
	require.NotNil(t, ctx)
	require.NoError(t, ctx.Err(), "SETUP: the selected Run is genuinely live")
	require.NotEmpty(t, workDir)
	require.NotEmpty(t, input)
	require.Equal(t, memoryExternalInputRound3Counts{factories: 1, runs: 1}, f.driver.counts(),
		"SETUP: one genuine initial Run, no resume/input/interrupt")
	rec, err := f.al.GetSessionLifecycleStore().Load(f.sessionID)
	require.NoError(t, err)
	require.NotNil(t, rec)
	require.Equal(t, session.LifecycleRunning, rec.State)
	require.True(t, rec.Is3P)
	require.Equal(t, f.agentID, rec.AgentID)
	require.Equal(t, memoryInputRound3A, rec.WorkspaceID)
	require.NotNil(t, rec.ExecutionID, "SETUP: real admission minted the execution, not the test")
	require.NotEmpty(t, rec.ExecutionID.RunID)
	require.Equal(t, f.al.bootEpochFor(), rec.ExecutionID.BootSeq)
	holder := memoryExternalInputRound3Holder(t, f.al, f.sessionID)
	require.True(t, holder.claim.matches(rec), "SETUP: the real selected Run carries the durable admission identity")
	require.True(t, holder.running)
	require.True(t, holder.started)
	require.True(t, holder.hasCancel)
	require.False(t, holder.steerInterrupt)
	require.Same(t, f.driver, holder.driver)
	require.Equal(t, memoryInputRound3A, holder.workspaceID)
	require.Equal(t, workDir, holder.workDir)
	require.Zero(t, f.provider.calls.Load(), "SETUP: no native-provider fallback while starting an external run")
}

// This is a read-only snapshot of a holder populated by genuine dispatch. No
// running flag, driver, cancellation function or execution claim is installed
// by the fixture. Reading steerInterrupt here does not consume the task mark.
type memoryExternalInputRound3HolderSnapshot struct {
	driver         runner.ExternalAgentRunner
	started        bool
	running        bool
	claim          executionClaim
	hasCancel      bool
	steerInterrupt bool
	workspaceID    string
	workDir        string
	reservations   int
}

func memoryExternalInputRound3Holder(t *testing.T, al *AgentLoop, sessionID string) memoryExternalInputRound3HolderSnapshot {
	t.Helper()
	holder := al.externalRunSessionIfPresent(sessionID)
	require.NotNil(t, holder, "SETUP: actual dispatch owns a live external holder")
	holder.mu.Lock()
	defer holder.mu.Unlock()
	return memoryExternalInputRound3HolderSnapshot{
		driver:         holder.driver,
		started:        holder.started,
		running:        holder.running,
		claim:          holder.claim,
		hasCancel:      holder.cancelRun != nil,
		steerInterrupt: holder.steerInterrupt,
		workspaceID:    holder.workspaceID,
		workDir:        holder.workDir,
		reservations:   len(holder.reservations),
	}
}

func (f *memoryExternalInputRound3Fixture) assertUninterrupted(
	t *testing.T, counts memoryExternalInputRound3Counts, holder memoryExternalInputRound3HolderSnapshot,
) {
	t.Helper()
	ctx, _, _ := f.driver.firstRun()
	require.NotNil(t, ctx)
	require.NoError(t, ctx.Err(), "F02: foreign input must not cancel the selected genuine Run context")
	require.Equal(t, counts, f.driver.counts(), "F02: zero driver construction/Run/Resume/Input/Cancel/Decide effects")
	require.Equal(t, holder, memoryExternalInputRound3Holder(t, f.al, f.sessionID),
		"F02: retained driver, execution binding, task mark and holder state remain unchanged")
}

func (f *memoryExternalInputRound3Fixture) assertAccepted(t *testing.T, taskOrigin bool) {
	t.Helper()
	ctx, _, _ := f.driver.firstRun()
	require.NotNil(t, ctx)
	require.ErrorIs(t, ctx.Err(), context.Canceled, "same-A control must cancel the actual selected Run, not merely queue text")
	counts := f.driver.counts()
	require.EqualValues(t, 1, counts.factories, "the already-retained driver must be reused")
	require.EqualValues(t, 1, counts.runs, "no second fresh external dispatch")
	require.Zero(t, counts.resumes, "process-exit gate must hold Resume until after the measured interval")
	require.Zero(t, counts.inputs, "live steering uses selected-run interrupt, not a mid-run stdin injection")
	require.Zero(t, counts.decisions, "no fake permission event was emitted")
	// ConsentDispatcher may observe the canceled context asynchronously and
	// call Cancel. Context.Err above is the synchronous interrupt instrument;
	// do not use a scheduling-dependent exact Cancel count for accepted input.
	holder := memoryExternalInputRound3Holder(t, f.al, f.sessionID)
	require.True(t, holder.running, "real exit barrier still owns the selected Run")
	require.Equal(t, taskOrigin, holder.steerInterrupt, "only a task-origin run gets the task continuation marker")
	require.Equal(t, memoryInputRound3A, holder.workspaceID)
	require.Zero(t, f.provider.calls.Load(), "external steering must not fall back to a native provider")
}

func memoryExternalInputRound3Helpers(t *testing.T) {
	for _, entry := range []string{"Direct", "Public"} {
		for _, scope := range []struct {
			name      string
			workspace string
			foreign   bool
		}{
			{name: "ForeignRefusesBeforeSelectedRunInterrupt", workspace: memoryInputRound3B, foreign: true},
			{name: "SameWorkspaceQueuesAndInterruptsSelectedRun", workspace: memoryInputRound3A},
		} {
			t.Run(entry+"/"+scope.name, func(t *testing.T) {
				f := memoryExternalInputRound3HelperFixture(t)
				msg := memoryInputRound3HelperMessage(f.sessionID, scope.workspace)
				before := memoryInputRound3Capture(t, f.al, f.provider, f.sessionID, agentSessionKey(f.agentID, msg))
				parentBefore := memoryInputRound3Capture(t, f.al, f.provider, f.parentID)
				counts := f.driver.counts()
				holder := memoryExternalInputRound3Holder(t, f.al, f.sessionID)
				if entry == "Public" {
					require.True(t, f.al.deliverHumanHelperInput(msg), "public helper input must be handled even on refusal")
				} else {
					err := f.al.enqueueHelperSteeringFromMessage(msg, f.agentID, true)
					if scope.foreign {
						require.ErrorIs(t, err, session.ErrWorkspaceImmutable, "F02: external helper's actual enqueue door refuses B")
						require.Equal(t, memoryInputRound3Refusal, userVisibleTurnError(err), "typed direct error remains actionable")
					} else {
						require.NoError(t, err)
					}
				}
				if scope.foreign {
					if entry == "Public" {
						memoryInputRound3AssertRefusal(t, f.al, msg)
					} else {
						require.Empty(t, memoryInputRound3Outbound(t, f.al))
					}
					f.assertUninterrupted(t, counts, holder)
					memoryInputRound3AssertUnchanged(t, before, memoryInputRound3Capture(t, f.al, f.provider, f.sessionID, agentSessionKey(f.agentID, msg)))
					memoryInputRound3AssertUnchanged(t, parentBefore, memoryInputRound3Capture(t, f.al, f.provider, f.parentID))
				} else {
					memoryInputRound3AssertQueued(t, f.al, msg)
					f.assertAccepted(t, false)
					require.Empty(t, memoryInputRound3Outbound(t, f.al), "accepted same-A helper input has no refusal")
				}
			})
		}
	}
}

func memoryExternalInputRound3TaskSnapshot(t *testing.T, f *memoryExternalInputRound3Fixture) []byte {
	t.Helper()
	stored, err := f.al.taskStore.Get(f.taskID)
	require.NoError(t, err, "read whole task record including binding/status/attempt counters")
	require.NotNil(t, stored)
	data, err := json.Marshal(stored)
	require.NoError(t, err, "freeze the complete task value independently of any live store pointer")
	return data
}

func memoryExternalInputRound3Tasks(t *testing.T) {
	for _, scope := range []struct {
		name      string
		workspace string
		foreign   bool
	}{
		{name: "ForeignPublicRefusesBeforeTaskSteer", workspace: memoryInputRound3B, foreign: true},
		{name: "SameWorkspacePublicQueuesInterruptsAndMarksTask", workspace: memoryInputRound3A},
	} {
		t.Run(scope.name, func(t *testing.T) {
			f := memoryExternalInputRound3TaskFixture(t)
			msg := memoryInputRound3HelperMessage(f.sessionID, scope.workspace)
			msg.ChatID = "f02-live-task-own-view"
			before := memoryInputRound3Capture(t, f.al, f.provider, f.sessionID, agentSessionKey(f.agentID, msg))
			taskBefore := memoryExternalInputRound3TaskSnapshot(t, f)
			counts := f.driver.counts()
			holder := memoryExternalInputRound3Holder(t, f.al, f.sessionID)
			require.True(t, f.al.deliverHumanHelperInput(msg), "real live external task branch must handle the input")
			if scope.foreign {
				memoryInputRound3AssertRefusal(t, f.al, msg)
				f.assertUninterrupted(t, counts, holder)
				memoryInputRound3AssertUnchanged(t, before, memoryInputRound3Capture(t, f.al, f.provider, f.sessionID, agentSessionKey(f.agentID, msg)))
			} else {
				memoryInputRound3AssertQueued(t, f.al, msg)
				f.assertAccepted(t, true)
				require.Empty(t, memoryInputRound3Outbound(t, f.al), "same-A live task input must not be refused")
			}
			require.Equal(t, taskBefore, memoryExternalInputRound3TaskSnapshot(t, f),
				"held task input cannot rebind/restart the task or spend attempts before process exit")
		})
	}
}
