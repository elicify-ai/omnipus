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

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
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
