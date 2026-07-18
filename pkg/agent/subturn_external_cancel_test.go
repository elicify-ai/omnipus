// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FIX 1 regression coverage: delegated EXTERNAL-CLI sub-turns are cancelable.
//
// Root cause (see external_dispatch.go's FIX 1 comment): the session-wide
// cancel cascade (InterruptSession / InterruptSessionHard, steering.go) fires
// a matching turnState's OWN providerCancel/turnCancel context.CancelFuncs
// directly — those two fields are populated ONLY inside al.runTurn (loop.go,
// via ts.setTurnCancel/ts.setProviderCancel). runExternalCLISubTurn (the
// dispatch path for external-CLI delegation) never called al.runTurn, so
// those fields stayed nil and the cascade's field-fire was a silent no-op:
// an external-CLI sub-turn could never be canceled. A SYNCHRONOUS delegate
// (`delegate(async=false)`) would deadlock the parent for up to the full run
// timeout.
//
// The tests below drive the REAL cancel path (InterruptSession /
// InterruptSessionHard against a childTS registered in al.activeTurnStates,
// exactly as spawnSubTurn registers it) against a live external-cli child
// backed by a fake driver — NOT parentTS.Finish directly, which is what the
// pre-existing cancel tests (subturn_cancel_status_test.go et al.) exercise
// and why this gap went unnoticed: those tests only cover native + async
// dispatch, never external-cli.

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
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// blockingExternalDriver is a fake runner.ExternalAgentRunner whose Run does
// not complete until the ctx passed to it is Done. This mirrors the real CLI
// drivers (claude/codex/opencode), which all bind the child OS process via
// exec.CommandContext(runCtx, ...): the process — and therefore the run —
// only ends when runCtx is canceled (or the process exits on its own).
// Records whether the ctx it received was ever canceled, so tests can assert
// the "subprocess" really would have been killed.
type blockingExternalDriver struct {
	startOnce sync.Once
	started   chan struct{} // closed once Run has been called

	ctxCanceled atomic.Bool
}

func newBlockingExternalDriver() *blockingExternalDriver {
	return &blockingExternalDriver{started: make(chan struct{})}
}

func (d *blockingExternalDriver) Run(ctx context.Context, _ runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.startOnce.Do(func() { close(d.started) })
	ch := make(chan runner.RunEvent)
	go func() {
		<-ctx.Done() // blocks exactly as exec.CommandContext(runCtx, ...) would
		d.ctxCanceled.Store(true)
		close(ch)
	}()
	return ch, nil
}

func (d *blockingExternalDriver) Decide(runner.PermissionDecision) {}

func (d *blockingExternalDriver) Cancel() {}

func (d *blockingExternalDriver) Input(string) error { return nil }

func (d *blockingExternalDriver) Resume(ctx context.Context, _ string) (<-chan runner.RunEvent, error) {
	return d.Run(ctx, runner.RunOptions{})
}

func (d *blockingExternalDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{OK: true}
}

var _ runner.ExternalAgentRunner = (*blockingExternalDriver)(nil)

// withBlockingDriver swaps the external driver factory for the test, handing
// out a blockingExternalDriver instance, and returns a restore func.
func withBlockingDriver(t *testing.T) (*blockingExternalDriver, func()) {
	t.Helper()
	d := newBlockingExternalDriver()
	prev := newExternalDriver
	newExternalDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return d, nil
	}
	return d, func() { newExternalDriver = prev }
}

// TestExternalCLISubTurn_CancelPropagates_Async proves the session-wide
// GRACEFUL cancel cascade (InterruptSession, PHASE A of RequestCancel) now
// reaches a live external-cli child — modeling `delegate(async=true)`
// background dispatch, where the parent's own turn is never blocked on this
// call and the cancel must reach the detached child via the
// activeTurnStates/transcriptSessionID cascade alone.
func TestExternalCLISubTurn_CancelPropagates_Async(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, childTS := newExternalTestLoop(t, "claude-code", "")
	const sessionID = "session_ext_cancel_async"
	childTS.transcriptSessionID = sessionID
	const turnKey = "child-async-1"
	childTS.turnID = turnKey
	// MED (7-reviewer gate follow-up): genuinely mirror what
	// DelegateTool.executeAsync's spawnSubTurn actually constructs for an
	// async delegate — Critical:true, depth>0, non-empty parentTurnID —
	// rather than a synthetic root-shaped (depth 0, no parent) turnState.
	// Without this the test's own claim to "model `delegate(async=true)`"
	// was not actually true: a real async child is never a root turn.
	childTS.critical = true
	childTS.depth = 1
	childTS.parentTurnID = "root-1"

	driver, restore := withBlockingDriver(t)
	defer restore()

	// Register childTS in al.activeTurnStates exactly as spawnSubTurn does
	// (subturn.go: al.activeTurnStates.Store(childID, childTS)) so the
	// session-wide cancel cascade can find it via transcriptSessionID match.
	al.activeTurnStates.Store(turnKey, childTS)
	defer al.activeTurnStates.Delete(turnKey)

	resultCh := make(chan *tools.ToolResult, 1)
	go func() {
		res, _ := runExternalCLISubTurn(context.Background(), al, childTS, "background task", 30*time.Second)
		resultCh <- res
	}()

	// Wait for the driver to actually start before firing the cancel, so the
	// test isn't racing goroutine scheduling.
	select {
	case <-driver.started:
	case <-time.After(2 * time.Second):
		t.Fatal("driver.Run was never called")
	}

	// Drive the REAL graceful cancel cascade — NOT parentTS.Finish directly.
	// Before the fix, childTS.providerCancel is nil and InterruptSession's
	// field-fire is a silent no-op.
	if _, err := al.InterruptSession(sessionID, "test cancel (async)"); err != nil {
		t.Fatalf("InterruptSession: %v", err)
	}

	select {
	case res := <-resultCh:
		if res == nil || res.Err == nil {
			t.Fatalf("expected a canceled ToolResult with Err set, got %+v", res)
		}
		if !errors.Is(res.Err, context.Canceled) {
			t.Errorf("result.Err = %v, want context.Canceled", res.Err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runExternalCLISubTurn did not return after InterruptSession — " +
			"cancel did not propagate to the external-cli child (async/background delegation)")
	}

	if !driver.ctxCanceled.Load() {
		t.Error("fake driver's run ctx was never canceled — the external CLI subprocess would not have been killed")
	}
}

// TestExternalCLISubTurn_CancelPropagates_Sync proves the HARD-abort cascade
// (InterruptSessionHard, PHASE B of RequestCancel) reaches a live
// external-cli child AND unblocks the calling goroutine — modeling
// `delegate(async=false)` SYNCHRONOUS dispatch, where the parent's own
// runTurn goroutine is blocked directly inside runExternalCLISubTurn's
// return. Before the fix this deadlocked for up to the full run timeout,
// since childTS.providerCancel/turnCancel were nil and nothing could ever
// cancel runCtx (childCtx is deliberately detached from the parent's ctx
// tree — context.Background() in spawnSubTurn).
func TestExternalCLISubTurn_CancelPropagates_Sync(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, childTS := newExternalTestLoop(t, "codex", "")
	const sessionID = "session_ext_cancel_sync"
	childTS.transcriptSessionID = sessionID
	const turnKey = "child-sync-1"
	childTS.turnID = turnKey

	driver, restore := withBlockingDriver(t)
	defer restore()

	al.activeTurnStates.Store(turnKey, childTS)
	defer al.activeTurnStates.Delete(turnKey)

	// Fire the hard-abort cascade from a separate goroutine once the driver
	// has started — mirroring RequestCancel's PHASE B timer firing while the
	// PARENT is still synchronously blocked inside this exact call.
	go func() {
		select {
		case <-driver.started:
		case <-time.After(2 * time.Second):
			return
		}
		if _, err := al.InterruptSessionHard(sessionID, "test cancel (sync)"); err != nil {
			t.Errorf("InterruptSessionHard: %v", err)
		}
	}()

	// This call blocks the CALLING goroutine directly, exactly like a
	// synchronous delegate blocks the parent's own runTurn goroutine. Bounded
	// with a select+timeout below (rather than letting it run to the full
	// 30s timeout) so a regression fails fast instead of hanging the suite.
	doneCh := make(chan struct{})
	var res *tools.ToolResult
	go func() {
		res, _ = runExternalCLISubTurn(context.Background(), al, childTS, "sync task", 30*time.Second)
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("runExternalCLISubTurn did not return after InterruptSessionHard — " +
			"synchronous (await) external-cli delegation would deadlock the parent turn")
	}

	if res == nil || res.Err == nil || !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("expected a canceled ToolResult with Err=context.Canceled, got %+v", res)
	}
	if !driver.ctxCanceled.Load() {
		t.Error("fake driver's run ctx was never canceled — the external CLI subprocess would not have been killed")
	}
}

// TestExternalCLISubTurn_CancelDuringWorkspaceLockWait is the BLOCK-finding
// regression test (7-reviewer gate on FIX 1): a session-wide cancel firing
// WHILE a second external-cli sub-turn is QUEUED behind a first, still-running
// sub-turn that shares its workDir (external_dispatch.go's workspaceRunLocks)
// must still cancel the queued one. This is exactly the contended-lock
// scenario cancel_stress_test.go deliberately avoids (see that file's note on
// its distinct-per-goroutine workDirs) — covered explicitly here instead.
//
// Before the fix: runExternalCLISubTurn acquired the (then-plain-sync.Mutex)
// workspace lock BEFORE creating runCtx/registering childTS's cancel funcs,
// so a cancel firing while queued on the lock found childTS.providerCancel/
// turnCancel still nil — a silent, PERMANENT no-op, because requestHardAbort
// latches hardAbort=true on its very first call and never re-fires once the
// nil cancel funcs are later replaced with real ones after the lock frees.
// The queued sub-turn would then run to completion, fully uncancelable.
//
// After the fix: runCtx/cancel are created and registered on childTS BEFORE
// the (now cancel-aware, channel-based) lock acquire, so a cancel firing
// while queued cancels runCtx directly, unblocking
// acquireWorkspaceRunLockCtx's select immediately — the queued run is
// skipped entirely and never reaches newExternalDriver/driver.Run at all.
func TestExternalCLISubTurn_CancelDuringWorkspaceLockWait(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	sharedWorkDir := t.TempDir()

	al, firstTS := newExternalTestLoop(t, "claude-code", sharedWorkDir)
	firstTS.transcriptSessionID = "session_lock_first"
	firstTS.turnID = "lock-first-1"

	// A second turnState on an agent with the SAME workspace directory as
	// firstTS — constructed directly (not via a second newExternalTestLoop
	// call, which would spin up an unrelated second *AgentLoop) so both
	// sub-turns share the same al.activeTurnStates and the same
	// external_dispatch.go package-level workspaceRunLocks entry for
	// sharedWorkDir.
	secondAgent := &AgentInstance{
		ID:   "ext-agent-lock-2",
		Name: "External Agent 2",
		Home: sharedWorkDir,
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
		},
	}
	secondTS := &turnState{
		agent:               secondAgent,
		agentID:             secondAgent.ID,
		turnID:              "lock-second-1",
		transcriptSessionID: "session_lock_second",
	}

	// Track driver-factory calls so we can prove the SECOND (queued) run
	// never even instantiated a driver — i.e. it never got past the lock
	// acquire to start the CLI at all, not merely that it was canceled after
	// starting.
	d := newBlockingExternalDriver()
	var driverCalls atomic.Int32
	prevFactory := newExternalDriver
	newExternalDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		driverCalls.Add(1)
		return d, nil
	}
	defer func() { newExternalDriver = prevFactory }()

	al.activeTurnStates.Store(firstTS.turnID, firstTS)
	defer al.activeTurnStates.Delete(firstTS.turnID)
	al.activeTurnStates.Store(secondTS.turnID, secondTS)
	defer al.activeTurnStates.Delete(secondTS.turnID)

	// Start the FIRST run — it acquires the sharedWorkDir lock and blocks
	// inside blockingExternalDriver.Run until its ctx is canceled.
	firstResultCh := make(chan *tools.ToolResult, 1)
	go func() {
		res, _ := runExternalCLISubTurn(context.Background(), al, firstTS, "first task", 30*time.Second)
		firstResultCh <- res
	}()
	select {
	case <-d.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first driver.Run was never called")
	}
	require.EqualValues(t, 1, driverCalls.Load(), "the first run must have instantiated exactly one driver")

	// Start the SECOND run on the SAME workDir — it must block behind the
	// first on acquireWorkspaceRunLockCtx (same-path lock contention).
	secondResultCh := make(chan *tools.ToolResult, 1)
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		res, _ := runExternalCLISubTurn(context.Background(), al, secondTS, "second task", 30*time.Second)
		secondResultCh <- res
	}()
	<-secondStarted
	// Give the second goroutine a moment to actually reach (and start
	// blocking on) the lock acquire before firing the cancel — otherwise the
	// cancel could win the race and land before the second goroutine even
	// starts waiting, which would prove nothing about the QUEUED case
	// specifically.
	time.Sleep(200 * time.Millisecond)

	// Fire the REAL hard-abort cascade against the SECOND session while it is
	// queued behind the first (still-running, still-locked) same-workspace
	// run.
	if _, err := al.InterruptSessionHard(secondTS.transcriptSessionID, "test cancel while queued"); err != nil {
		t.Fatalf("InterruptSessionHard (second, queued): %v", err)
	}

	// The second run must return a canceled result WITHOUT ever running to
	// completion — bounded so a lost-cancel regression under lock contention
	// fails fast instead of hanging for up to the full first-run timeout.
	select {
	case res := <-secondResultCh:
		if res == nil || res.Err == nil || !errors.Is(res.Err, context.Canceled) {
			t.Fatalf("expected the queued second run to return a canceled ToolResult, got %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the second (queued) external-cli sub-turn was not canceled while waiting for the workspace lock — " +
			"lost-cancel regression under lock contention")
	}

	// The queued run must never have reached newExternalDriver — i.e. it was
	// skipped entirely rather than started-then-killed.
	require.EqualValues(t, 1, driverCalls.Load(),
		"the queued second run must never have instantiated a driver — it should be canceled before starting")

	// Clean up: cancel the FIRST run too so the test doesn't leak a blocked
	// goroutine/driver.
	if _, err := al.InterruptSessionHard(firstTS.transcriptSessionID, "test cleanup"); err != nil {
		t.Fatalf("InterruptSessionHard (first, cleanup): %v", err)
	}
	select {
	case res := <-firstResultCh:
		if res == nil || res.Err == nil || !errors.Is(res.Err, context.Canceled) {
			t.Fatalf("expected the first run to return a canceled ToolResult, got %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the first external-cli sub-turn did not cancel during cleanup")
	}
	if !d.ctxCanceled.Load() {
		t.Error("fake driver's run ctx was never canceled — the external CLI subprocess would not have been killed")
	}
}

// TestExternalCLISubTurn_RequestCancelEndToEnd_ExternalCLIOnlySession drives
// the REAL public cancel entrypoint — RequestCancel, used by every cancel
// surface (web SPA, Tier A /cancel, Tier B text-parsing channels, CLI) —
// against a session whose ONLY live turn is an external-cli childTS, with NO
// root turnState registered at all. This models a Critical async delegate
// (`delegate(async=true)`) whose PARENT root turn has already finished and
// been cleared from al.activeTurnStates (ADR-045) while the delegate itself
// is still running — the case RequestCancel's own
// GetActiveTurnHookForSession/ClaimCancel/SetOnCancelFinish machinery must
// resolve, claim, and cancel directly, never exercised by the
// InterruptSession/InterruptSessionHard-only tests above (those call the
// lower-level cascade functions directly, bypassing ClaimCancel/RequestCancel
// entirely).
func TestExternalCLISubTurn_RequestCancelEndToEnd_ExternalCLIOnlySession(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, childTS := newExternalTestLoop(t, "codex", "")

	// Wire a REAL session + transcript store so the "turn_canceled" transcript
	// check below is a genuine best-effort read, not a guaranteed no-op.
	store := al.GetSessionStore()
	require.NotNil(t, store, "agent loop shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "ext-agent")
	require.NoError(t, err)
	sessionID := meta.ID
	require.NotEmpty(t, sessionID)

	childTS.transcriptSessionID = sessionID
	childTS.transcriptStore = store
	const turnKey = "child-e2e-1"
	childTS.turnID = turnKey
	// Model a genuine async delegate (MED finding, mirrored from the Async
	// test above): Critical:true, depth>0, non-empty parentTurnID — never a
	// synthetic root-shaped turnState.
	childTS.critical = true
	childTS.depth = 1
	childTS.parentTurnID = "root-1"

	driver, restore := withBlockingDriver(t)
	defer restore()

	al.activeTurnStates.Store(turnKey, childTS)
	defer al.activeTurnStates.Delete(turnKey)
	// Deliberately no root turnState is registered for this session at all —
	// the parent is genuinely gone, exactly as it would be once a Critical
	// async delegate's parent root has already finished and been cleared via
	// clearActiveTurn (loop.go).

	resultCh := make(chan *tools.ToolResult, 1)
	go func() {
		res, _ := runExternalCLISubTurn(context.Background(), al, childTS, "background task", 30*time.Second)
		resultCh <- res
	}()

	select {
	case <-driver.started:
	case <-time.After(2 * time.Second):
		t.Fatal("driver.Run was never called")
	}

	outcome, cancelErr := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "test-channel"},
		CancelHooks{},
	)
	require.NoError(t, cancelErr)
	require.True(t, outcome.Fired,
		"RequestCancel must claim the session's only live turn (the external-cli child) via ClaimCancel")
	require.Equal(t, turnKey, outcome.TurnID)
	require.Contains(t, outcome.Descendants, turnKey)

	select {
	case res := <-resultCh:
		if res == nil || res.Err == nil || !errors.Is(res.Err, context.Canceled) {
			t.Fatalf("expected a canceled ToolResult with Err=context.Canceled, got %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runExternalCLISubTurn did not return after RequestCancel — " +
			"the real public cancel entrypoint did not reach the external-cli child")
	}
	if !driver.ctxCanceled.Load() {
		t.Error("fake driver's run ctx was never canceled via the real RequestCancel entrypoint")
	}

	// Best-effort (per this fix's scope): the turn_canceled transcript entry
	// is written by RequestCancel's onCancelFinish callback, which only fires
	// from turnState.Finish(). runExternalCLISubTurn never calls
	// childTS.Finish() for the external-cli dispatch path (unlike the native
	// path's al.runTurn, which always does via `defer ts.Finish(false)`) — a
	// separate, pre-existing gap outside this BLOCK fix's scope. So the entry
	// is checked opportunistically here (logged, not failed, if absent) to
	// document the gap honestly rather than assert coverage that doesn't
	// exist; the cancellation of the driver's ctx above (the actual subject
	// of this fix) is unconditionally required.
	entries, readErr := store.ReadTranscript(sessionID)
	if readErr != nil {
		t.Logf("ReadTranscript: %v (non-fatal — best-effort check)", readErr)
		return
	}
	found := false
	for _, e := range entries {
		if e.Type == session.EntryTypeTurnCancelled {
			found = true
			break
		}
	}
	if !found {
		t.Log("turn_canceled transcript entry not found — expected, since runExternalCLISubTurn never calls " +
			"childTS.Finish() so RequestCancel's onCancelFinish callback never fires for this path; " +
			"not a failure of this test (documents a separate, pre-existing gap)")
	}
}
