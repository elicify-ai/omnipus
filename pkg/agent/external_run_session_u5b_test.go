// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// U5b security re-verification regression pack (N1, N2, N3, N4, N5, N6, N7,
// F4-task, S1).
//
// Each test's doc comment states the oracle (the required property), the real
// code boundary it exercises, and the instrument. No sleep is used as an
// oracle; synchronization is by channels and explicit hooks.
package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func u5bLoop(t *testing.T) *AgentLoop {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})
	// Wire the durable lifecycle + inbox stores the delivery/binding paths read.
	al.SetSessionMessagingStores(
		session.NewMessageInboxStore(t.TempDir()),
		session.NewLifecycleStore(t.TempDir()),
	)
	t.Cleanup(func() { al.Close() })
	return al
}

func u5bSeedSteered(t *testing.T, al *AgentLoop, sessionID, agentID string, is3P bool) {
	t.Helper()
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID:      sessionID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1",
		AgentID:        agentID,
		Is3P:           is3P,
	}))
}

// ---------------------------------------------------------------------------
// N5 / N7 — resume-or-refuse on later steered entries; no fresh fallback.
// ---------------------------------------------------------------------------

// Oracle: FR-043 — an explicit resume (or any post-launch entry of a steered
// session) with no retained driver must refuse, never start a fresh one.
// Real: beginExternalRun + driverForRun. Instrument: a counting fake factory.
func TestU5b_N5N7_LaterEntryWithNoRetainedDriverRefuses(t *testing.T) {
	al := u5bLoop(t)
	sessKey := "sess-n5"
	ts := &turnState{transcriptSessionID: sessKey}

	// First launch (never started): fresh Run is permitted; driverForRun makes
	// the driver and records `started`.
	sess, resume, err := al.beginExternalRun(sessKey, ts, func() {}, true)
	require.NoError(t, err)
	require.False(t, resume, "a first launch must Run, not Resume")
	require.Same(t, sess, al.externalRunSession(sessKey))
	_, derr := sess.driverForRun("claude-code", nil, false)
	require.NoError(t, derr)
	al.finishExternalRun(sess, sessKey)

	// A later entry (started, driver retained) resumes.
	sess2, resume2, err2 := al.beginExternalRun(sessKey, ts, func() {}, true)
	require.NoError(t, err2)
	require.True(t, resume2, "a later steered entry must Resume the retained conversation")
	require.Same(t, sess, sess2)
	al.finishExternalRun(sess, sessKey)

	// Release the driver (N6). A later entry now MUST refuse — no fresh fallback.
	sess.mu.Lock()
	sess.releaseDriverLocked()
	sess.mu.Unlock()
	_, _, err3 := al.beginExternalRun(sessKey, ts, func() {}, true)
	require.ErrorIs(t, err3, errExternalResumeUnavailable)
}

// Oracle: an explicit continuation (ts.opts.ExternalCLIResume) with no retained
// driver refuses — N5's "missing driver visibly refuses; no fresh fallback".
func TestU5b_N5_ExplicitResumeWithNoDriverRefuses(t *testing.T) {
	al := u5bLoop(t)
	sessKey := "sess-n5b"
	ts := &turnState{transcriptSessionID: sessKey, opts: processOptions{ExternalCLIResume: true}}

	_, _, err := al.beginExternalRun(sessKey, ts, func() {}, true)
	require.ErrorIs(t, err, errExternalResumeUnavailable)
}

// Oracle: a non-steered session (resumeOnly=false) keeps today's fresh-Run
// behaviour even when the holder already ran — the N7 rule is scoped to
// steered delegated sessions.
func TestU5b_N7_NonSteeredSessionStillRunsFresh(t *testing.T) {
	al := u5bLoop(t)
	sessKey := "sess-n7"
	ts := &turnState{transcriptSessionID: sessKey}

	_, resume, err := al.beginExternalRun(sessKey, ts, func() {}, false)
	require.NoError(t, err)
	require.False(t, resume)
	sess := al.externalRunSession(sessKey)
	_, derr := sess.driverForRun("claude-code", nil, false)
	require.NoError(t, derr)
	al.finishExternalRun(sess, sessKey)

	_, resume2, err2 := al.beginExternalRun(sessKey, ts, func() {}, false)
	require.NoError(t, err2)
	require.False(t, resume2, "a non-steered session is not subject to the resume-only rule")
}

// ---------------------------------------------------------------------------
// N2 — delivery bound to the selected execution; supersession refused.
// ---------------------------------------------------------------------------

func u5bSeedLiveHolder(t *testing.T, al *AgentLoop, sessKey string, gen int) *externalCLIRunSession {
	t.Helper()
	sess := al.externalRunSession(sessKey)
	fr := runner.NewFakeRunner()
	prev := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) { return fr, nil }
	t.Cleanup(func() { newExternalDriver = prev })
	sess.mu.Lock()
	sess.started = true
	sess.running = true
	sess.driver = fr
	sess.claim = executionClaim{SessionID: sessKey, Generation: gen, RunID: "run-1", BootSeq: 1}
	sess.cancelRun = func() {}
	sess.mu.Unlock()
	return sess
}

// Oracle: a delivery whose expected claim no longer matches the in-flight run
// is refused as superseded before it reaches the queue (BDD-05.6 "no stale
// delivery"). Real: deliverExternalCLIInstruction's binding check.
func TestU5b_N2_SupersededGenerationDeliveryRefused(t *testing.T) {
	al := u5bLoop(t)
	sessKey := "sess-n2"
	u5bSeedSteered(t, al, sessKey, "worker", true)
	u5bSeedLiveHolder(t, al, sessKey, 2) // the live run is generation 2

	// A delivery bound to the OLD generation 1 is refused.
	_, err := al.deliverExternalCLIInstruction(context.Background(), sessKey, "worker",
		providers.Message{Role: "user", Content: "stale S"}, "",
		executionClaim{SessionID: sessKey, Generation: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "superseded")
}

// Oracle: with no live conversation the delivery refuses visibly (never a
// silent success).
func TestU5b_N2_NoLiveConversationRefused(t *testing.T) {
	al := u5bLoop(t)
	_, err := al.DeliverExternalCLIInstruction(context.Background(), "sess-none", "worker",
		providers.Message{Role: "user", Content: "S"}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no live external CLI conversation")
}

// ---------------------------------------------------------------------------
// N4 — a task-origin external run ACCEPTS a live instruction (FR-032).
// ---------------------------------------------------------------------------

// Oracle: FR-032 — authorized human input in an existing task chat steers the
// live external run. The delivery is therefore accepted (no refusal), the
// holder is marked (steerInterrupt) so the task loop resumes the SAME native
// conversation, and the selected CLI run is interrupted via its own cancel func.
// Real: deliverExternalCLIInstruction's task-origin branch.
//
// History: this test formerly asserted an interim refusal ("the task side has no
// continuation consumer", FR-043/U5b). U10b supplied that consumer
// (task_executor_run.go::processTaskDirectExternalCLI), so the same spec element
// now overturns the refusal into an acceptance. The end-to-end acceptance —
// resume of the same native conversation with no second Run — is covered by
// task_external_steer_u10b_test.go::TestU10b_FR032_LiveExternalTaskRun_SteerResumesSameConversation;
// this test pins the delivery seam itself.
func TestU5b_N4_TaskOriginDeliveryAccepted(t *testing.T) {
	al := u5bLoop(t)
	sessKey := "sess-n4-task"
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID:      sessKey,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-1"},
		WorkspaceID:    "ws-1",
		AgentID:        "worker",
		Is3P:           true,
	}))
	sess := u5bSeedLiveHolder(t, al, sessKey, 1)
	var canceled int
	sess.mu.Lock()
	sess.cancelRun = func() { canceled++ }
	sess.mu.Unlock()

	resolved, err := al.DeliverExternalCLIInstruction(context.Background(), sessKey, "worker",
		providers.Message{Role: "user", Content: "S"}, "")
	require.NoError(t, err, "FR-032: a live external task run must accept the instruction")
	require.NotEmpty(t, resolved, "the accepted delivery returns the resolved correlation id")
	require.Equal(t, 1, canceled, "the selected CLI run is interrupted via its own cancel func")
	require.True(t, al.takeExternalSteerInterrupt(sessKey),
		"the holder is marked so the task loop resumes the same native conversation")
}

// ---------------------------------------------------------------------------
// N6 — the driver (and its snapshot) is released once the episode ends.
// ---------------------------------------------------------------------------

// Oracle: releaseExternalRunIfIdle drops the retained driver when the run is
// not running and no continuation is queued; ForgetExternalRunSession drops it
// unconditionally (session deletion).
func TestU5b_N6_DriverReleasedWhenIdle(t *testing.T) {
	al := u5bLoop(t)
	sessKey := "sess-n6"
	u5bSeedLiveHolder(t, al, sessKey, 1)
	sess := al.externalRunSession(sessKey)
	sess.mu.Lock()
	sess.running = false
	sess.mu.Unlock()

	al.releaseExternalRunIfIdle(sessKey)
	sess.mu.Lock()
	require.Nil(t, sess.driver, "an idle episode must release the retained driver")
	require.True(t, sess.started, "the started marker must survive the release (N5/N7)")
	sess.mu.Unlock()

	// ForgetExternalRunSession removes the holder entirely.
	al.ForgetExternalRunSession(sessKey)
	require.Nil(t, al.externalRunSessionIfPresent(sessKey))
}

// ---------------------------------------------------------------------------
// N1 — a continuation must run in, and lock, the workspace it started in.
// ---------------------------------------------------------------------------

// Oracle: a continuation whose re-resolved workspace differs from the preserved
// one refuses — it never authorizes one workspace while the retained driver
// executes in another. Real: runExternalCLISubTurn's resume re-resolution.
func TestU5b_N1_ContinuationToDifferentWorkspaceRefused(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	d, restore := withRecordingDriver(t)
	defer restore()

	// First run records the real workspace.
	if _, err := runExternalCLISubTurn(context.Background(), al, ts, "first", 30*time.Second); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}

	// Corrupt the preserved workspace to a path the re-resolution cannot return.
	sess := al.externalRunSession(externalRunSessionKey(ts))
	sess.mu.Lock()
	sess.workDir = "/nonexistent/preserved/elsewhere"
	sess.workspaceID = "ws-not-the-real-one"
	sess.mu.Unlock()

	ts.opts.ExternalCLIResume = true
	res, err := runExternalCLISubTurn(context.Background(), al, ts, "continuation", 30*time.Second)
	require.Error(t, err)
	require.ErrorIs(t, err, errExternalWorkspaceChanged, "the refusal must name the workspace mismatch")
	require.Nil(t, res, "a refused continuation starts no process")
	runs, resumes := d.snapshot()
	require.Equal(t, 1, runs, "no fresh Run may start on a refused continuation")
	require.Empty(t, resumes, "no Resume may reach the driver on a refused continuation")
}

// ---------------------------------------------------------------------------
// F4-task — task-executor runs honor the durable Is3P classification.
// ---------------------------------------------------------------------------

// Oracle: a task run whose live executor disagrees with its durable record
// refuses before any driver/provider call. Real: enforceTaskRunRuntimeInvariant.
func TestU5b_F4Task_TaskRunRuntimeInvariant(t *testing.T) {
	al := u5bLoop(t)
	sessKey := "sess-task-run"
	taskCtx := tools.WithRunningTaskID(context.Background(), "task-1")

	// Not a task-executor run → no-op.
	require.NoError(t, al.enforceTaskRunRuntimeInvariant(context.Background(), sessKey, true))

	// A task run with no durable record refuses.
	err := al.enforceTaskRunRuntimeInvariant(taskCtx, sessKey, true)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTaskRunNotDispatched)
	require.ErrorIs(t, err, errUnpersistedRuntimeClassification)

	// A durable native record vs a live external executor refuses.
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: sessKey, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-1"},
		WorkspaceID:    "ws-1", AgentID: "worker", Is3P: false,
	}))
	err = al.enforceTaskRunRuntimeInvariant(taskCtx, sessKey, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Rerun the task")

	// Matching classification passes.
	require.NoError(t, al.enforceTaskRunRuntimeInvariant(taskCtx, sessKey, false))
}

// ---------------------------------------------------------------------------
// S1 — a second StartTaskNow after a failed setup refuses, starts nothing.
// ---------------------------------------------------------------------------

// Oracle: a bound session is returned only when its durable record exists and
// the task is not Failed. Real: boundSessionRetryable.
func TestU5b_S1_BoundSessionRetryGate(t *testing.T) {
	te, store, _, _ := newStartTaskNowWithRegistry(t)
	ls := session.NewLifecycleStore(t.TempDir())
	te.SetLifecycleStore(ls)

	tk := &task.Task{Title: "t", Prompt: "p", AgentID: "test-agent", Priority: 3,
		Action: task.ActionLLM, Status: task.StatusNext, SessionID: "sess-s1", WorkspaceID: "ws-1"}
	require.NoError(t, store.Create(tk))

	// No record for the bound session → refuse.
	err := te.boundSessionRetryable(tk)
	require.Error(t, err)
	require.Contains(t, err.Error(), "never persisted")

	// Persist the record; task not Failed → allowed.
	require.NoError(t, ls.Persist(&session.LifecycleRecord{
		SessionID: "sess-s1", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "test-agent",
	}))
	require.NoError(t, te.boundSessionRetryable(tk))

	// Failed → refuse, naming the recorded failure.
	failed := task.StatusFailed
	tk2, uerr := store.Update(tk.ID, task.Patch{Status: &failed, Result: strptr("persist boom")})
	require.NoError(t, uerr)
	err = te.boundSessionRetryable(tk2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Failed")
	require.Contains(t, err.Error(), "persist boom")
}

// ---------------------------------------------------------------------------
// N2 barrier — a canceled run's process must have exited before the handler
// returns, so the follow-up Resume cannot race "already active".
// ---------------------------------------------------------------------------

// gatedExitDriver models the real CLI drivers: Run/Resume refuse a second
// concurrent run, and the event stream closes only once the "process" has
// exited (the test controls that through exitGate).
type gatedExitDriver struct {
	mu       sync.Mutex
	active   bool
	started  chan struct{}
	exitGate chan struct{}
	runCalls int32
}

func newGatedExitDriver() *gatedExitDriver {
	return &gatedExitDriver{started: make(chan struct{}), exitGate: make(chan struct{})}
}

func (d *gatedExitDriver) start(ctx context.Context) (<-chan runner.RunEvent, error) {
	d.mu.Lock()
	if d.active {
		d.mu.Unlock()
		return nil, errors.New("Run called while a run is already active")
	}
	d.active = true
	atomic.AddInt32(&d.runCalls, 1)
	events := make(chan runner.RunEvent)
	d.mu.Unlock()
	select {
	case <-d.started:
	default:
		close(d.started)
	}
	_ = ctx
	go func() {
		// The "process" exits when the test opens the gate (modelling a real
		// process's exit after an interrupt), and only then does the event stream
		// close — the N2 barrier waits on exactly this.
		<-d.exitGate
		d.mu.Lock()
		d.active = false
		d.mu.Unlock()
		close(events)
	}()
	return events, nil
}

func (d *gatedExitDriver) Run(ctx context.Context, _ runner.RunOptions) (<-chan runner.RunEvent, error) {
	return d.start(ctx)
}
func (d *gatedExitDriver) Resume(ctx context.Context, _ string, _ ...string) (<-chan runner.RunEvent, error) {
	return d.start(ctx)
}
func (d *gatedExitDriver) Decide(runner.PermissionDecision) {}
func (d *gatedExitDriver) Cancel()                          {}
func (d *gatedExitDriver) Input(string) error               { return nil }
func (d *gatedExitDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{OK: true}
}

// Oracle: runExternalCLISubTurn must not return until the driver's event stream
// has closed (process exit), so a continuation that follows cannot hit "Run
// called while a run is already active". Real: the N2 barrier.
func TestU5b_N2_ProcessExitBarrier(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	d := newGatedExitDriver()
	prev := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) { return d, nil }
	defer func() { newExternalDriver = prev }()

	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := runExternalCLISubTurn(ctx, al, ts, "first", 30*time.Second)
		firstDone <- err
	}()

	<-d.started // the run is live
	cancel()    // interrupt it; the driver has not exited yet

	// The barrier holds the return open until the driver's stream closes.
	select {
	case <-firstDone:
		t.Fatal("runExternalCLISubTurn returned before the driver's stream closed (no process-exit barrier)")
	case <-time.After(100 * time.Millisecond): // harness bounded wait (not an oracle)
	}
	close(d.exitGate) // the process exits; the stream closes
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("runExternalCLISubTurn did not return after the driver exited")
	}

	// The continuation now resumes with no "already active" error.
	ts.opts.ExternalCLIResume = true
	if _, err := runExternalCLISubTurn(context.Background(), al, ts, "continuation", 30*time.Second); err != nil {
		t.Fatalf("continuation after interrupt failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// F4-task / S1 / N7 REAL-CALL-SITE coverage (U5b CHECK F-02).
//
// The helper tests above assert the helper functions directly. These drive the
// PRODUCTION entry points (processTaskDirect, StartTaskNow, runExternalCLISubTurn)
// so removing the production invocation of a guard no longer leaves them green.
// ---------------------------------------------------------------------------

// Oracle (FR-016 / C-DELEGATE Property B, F4 task path): processTaskDirect — the
// REAL task-dispatch entry point — must refuse a task run whose live executor
// disagrees with its durable record, BEFORE any driver or provider call.
// Real: the enforceTaskRunRuntimeInvariant invocation inside processTaskDirect.
func TestU5b_F4Task_ProcessTaskDirectRefusesRuntimeChange_RealCallSite(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(t.TempDir()))
	fr, restore := withFakeDriver(t)
	defer restore()

	const taskChat = "task-u5b-f4-real"
	// Durable record: launched NATIVE (Is3P=false). The live "ext-agent" of this
	// harness resolves EXTERNAL-CLI, so the two disagree.
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: taskChat, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-1"},
		WorkspaceID:    "ws-1", AgentID: "ext-agent", Is3P: false,
	}))

	ctx := tools.WithRunningTaskID(context.Background(), "task-1")
	_, err := al.processTaskDirect(ctx, "ext-agent", "do the task", "task-key", taskChat)
	require.Error(t, err, "the REAL processTaskDirect call site must refuse a runtime change (F4 task path)")
	require.ErrorIs(t, err, ErrTaskRunNotDispatched)
	require.Contains(t, err.Error(), "Rerun the task")
	require.Empty(t, fr.RecordedRunOpts(), "no external run may start on the refused real call site")
	require.Zero(t, provider.calls, "no native provider call on the refused real call site")
}

// Oracle (S1): StartTaskNow — the REAL entry point — must refuse to hand back a
// bound session whose durable classification was never persisted, or whose task
// already Failed; it must never return (sessionID, nil) for an unretryable
// binding. Real: the boundSessionRetryable invocation inside StartTaskNow.
func TestU5b_S1_StartTaskNowRefusesUnretryableBinding_RealCallSite(t *testing.T) {
	te, store, _, _ := newStartTaskNowWithRegistry(t)
	te.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))

	// Case 1: bound session, NO durable record → refuse, return no id.
	noRecord := &task.Task{Title: "t1", Prompt: "p", AgentID: "test-agent", Priority: 3,
		Action: task.ActionLLM, Status: task.StatusNext, SessionID: "sess-s1-norec", WorkspaceID: "ws-1"}
	require.NoError(t, store.Create(noRecord))
	sessID, err := te.StartTaskNow(context.Background(), noRecord.ID)
	require.Error(t, err, "StartTaskNow must refuse a bound session with no durable record")
	require.Empty(t, sessID, "no session id may be returned for an unretryable binding")
	require.Contains(t, err.Error(), "never persisted")

	// Case 2: durable record exists, but the task already Failed → refuse,
	// naming the recorded failure.
	ls := te.getLifecycleStore()
	require.NotNil(t, ls)
	require.NoError(t, ls.Persist(&session.LifecycleRecord{
		SessionID: "sess-s1-failed", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "test-agent",
	}))
	failed := task.StatusFailed
	failedTask := &task.Task{Title: "t2", Prompt: "p", AgentID: "test-agent", Priority: 3,
		Action: task.ActionLLM, Status: failed, Result: "persist boom",
		SessionID: "sess-s1-failed", WorkspaceID: "ws-1"}
	require.NoError(t, store.Create(failedTask))
	sessID2, err2 := te.StartTaskNow(context.Background(), failedTask.ID)
	require.Error(t, err2, "StartTaskNow must refuse a Failed task's binding")
	require.Empty(t, sessID2, "no session id may be returned for a Failed task's binding")
	require.Contains(t, err2.Error(), "persist boom")
}

// Oracle (N5/N7, FR-043): a later entry of a STEERED session must resume-or-refuse
// — never start a fresh conversation. Real: the production resume-only
// determination (externalRunResumeOnly) that runExternalCLISubTurn feeds to
// beginExternalRun. Supplying resumeOnly=true directly (the helper test above)
// cannot observe that production computation; this drives the real body.
func TestU5b_N7_SteeredSessionLaterEntryRefusesWithoutDriver_RealDispatch(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	// The durable lifecycle store the resume-only determination and the seed
	// below both read (newExternalTestLoop does not wire it).
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(t.TempDir()))

	sessKey := externalRunSessionKey(ts)
	// A steered, NON-task durable record → the production determination is true.
	u5bSeedSteered(t, al, sessKey, ts.agentID, false)
	require.True(t, al.externalRunResumeOnly(sessKey),
		"precondition: a steered non-task session must determine resume-only")

	// A factory handing out one DISTINCT driver per construction: its Run/Resume
	// return an ALREADY-CLOSED stream (so drainExternalRun never blocks) and its
	// construction count is the observable that a continuation built a second
	// driver.
	factory := withRecordingDriverFactory(t)

	// First entry: a genuine first launch Runs fresh.
	if _, err := runExternalCLISubTurn(context.Background(), al, ts, "first", 30*time.Second); err != nil {
		t.Fatalf("first steered dispatch: %v", err)
	}
	if got := factory.count(); got != 1 {
		t.Fatalf("the first steered entry must build exactly one driver; built=%d, want 1", got)
	}
	// Drop the retained driver so a later entry has nothing to resume (N6).
	sess := al.externalRunSession(sessKey)
	sess.mu.Lock()
	sess.releaseDriverLocked()
	sess.mu.Unlock()

	// A later entry of a steered session must REFUSE — never a fresh Run. If the
	// production resume-only computation is dropped (the CHECK's
	// n7-production-classification-unwired mutant), this call would start a
	// second fresh Run, build a second driver and return nil error.
	_, err := runExternalCLISubTurn(context.Background(), al, ts, "second", 30*time.Second)
	require.ErrorIs(t, err, errExternalResumeUnavailable,
		"a later steered entry with no retained driver must refuse, never start fresh")
	require.Equal(t, 1, factory.count(),
		"the continuation must not build a second driver (a fresh Run would)")
}

// ---------------------------------------------------------------------------
// N3 — a human message into a live external worker's chat delivers via the
// external-CLI path, not the silent native queue.
// ---------------------------------------------------------------------------

// Oracle: a message typed into a running external worker's own chat is DELIVERED
// by interrupt + external conversation continuation (BDD-05.5's entry), not
// merely queued. Real: deliverHumanHelperInput -> enqueueHelperSteeringFromMessage
// -> DeliverExternalCLIInstruction.
func TestU5b_N3_HumanMessageIntoExternalWorkerDelivers(t *testing.T) {
	provider := &delegateDispatchProvider{}
	al := newDelegateDispatchLoop(t, provider)
	u5bSeedSteered(t, al, "sess-n3", delegateExtCLIAgentID, true)

	fr := runner.NewFakeRunner()
	prev := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) { return fr, nil }
	defer func() { newExternalDriver = prev }()

	var canceled atomic.Int32
	sess := al.externalRunSession("sess-n3")
	sess.mu.Lock()
	sess.started = true
	sess.running = true
	sess.driver = fr
	sess.cancelRun = func() { canceled.Add(1) }
	sess.mu.Unlock()

	before := al.pendingSteeringCountForScope("sess-n3")
	handled := al.deliverHumanHelperInput(bus.InboundMessage{
		Channel: "webchat", ChatID: "parent-1", SessionID: "sess-n3", Content: "stop that, do this instead",
	})
	require.True(t, handled, "a message to a steered helper session must be handled by the helper path")
	require.Equal(t, before+1, al.pendingSteeringCountForScope("sess-n3"),
		"the message must be queued for the external CLI conversation (via the delivery path)")
	require.EqualValues(t, 1, canceled.Load(),
		"the running external CLI run must have been interrupted to deliver the message")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func strptr(s string) *string { return &s }
