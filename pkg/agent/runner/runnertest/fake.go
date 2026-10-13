// Package runnertest holds FakeRunner, a test-only runner.ExternalAgentRunner.
// It lives in its own package, imported only by _test.go files, so it never
// compiles into the production binary (it used to sit in package runner as an
// ordinary file).
package runnertest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
)

// FakeRunner is a test-only implementation of runner.ExternalAgentRunner.
// It allows tests to inject events, simulate permission prompts, and verify
// that the bidirectional interface contract is satisfied.
//
// Usage:
//
//	fr := runnertest.NewFakeRunner()
//	ch, err := fr.Run(ctx, runner.RunOptions{Input: "do something"})
//	fr.InjectEvent(runner.RunEvent{Kind: EventKindOutput, Output: &OutputEvent{Text: "hello"}})
//	fr.InjectEvent(runner.RunEvent{Kind: EventKindEnd})
//	for ev := range ch { ... }
type FakeRunner struct {
	mu      sync.Mutex
	eventCh chan runner.RunEvent
	// done is closed by Cancel BEFORE the event channel is closed, so an
	// InjectEvent blocked on a full buffer is released and returns an error
	// instead of sending on a closed channel; sendMu makes Cancel wait for
	// in-flight senders to leave before it closes eventCh.
	done       chan struct{}
	sendMu     sync.RWMutex
	decisions  []runner.PermissionDecision
	canceled   bool
	inputsSent []string
	testResult runner.ConnectionTestResult
	runOpts    []runner.RunOptions
	resumeIDs  []string
	// resumeInputs records the instruction delivered by each Resume call (empty
	// for a bare continuation), so a delivery-path test can assert the new
	// instruction S actually reached the runner (FR-043).
	resumeInputs []string
}

// NewFakeRunner returns a FakeRunner with a buffered event channel.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		eventCh:    make(chan runner.RunEvent, 64),
		done:       make(chan struct{}),
		testResult: runner.ConnectionTestResult{OK: true, Message: "fake runner: OK", CLIVersion: "0.0.0-fake"},
	}
}

// Run records the options and returns the internal event channel.
//
// Contract (mirrors the real drivers): cancelling the Run context ends the run
// and CLOSES the stream. The real drivers derive a child context from ctx
// (`exec.CommandContext(runCtx, …)`), so a cancel kills the CLI process,
// cmd.Wait() returns and the parser goroutine closes the event channel. A fake
// that left its stream open on ctx cancel let a cancelled caller block forever
// waiting for a close that never came (see the processTaskDirect external-CLI
// timeout test); it now honours the same contract.
func (f *FakeRunner) Run(ctx context.Context, opts runner.RunOptions) (<-chan runner.RunEvent, error) {
	f.mu.Lock()
	f.runOpts = append(f.runOpts, opts)
	f.mu.Unlock()
	f.watchCancel(ctx)
	return f.eventCh, nil
}

// watchCancel ends the fake run when ctx is cancelled. A nil Done channel
// (context.Background/TODO) has nothing to watch — no goroutine is started, so
// a background-context run cannot leak a watcher goroutine blocked on <-nil.
func (f *FakeRunner) watchCancel(ctx context.Context) {
	done := ctx.Done()
	if done == nil {
		return
	}
	go func() {
		<-done
		f.Cancel()
	}()
}

// Decide records the decision (tests can verify it was received).
//
// On a DENY (!Allow) it also cancels the run, mirroring the real-driver contract:
// external CLIs run non-interactively with only post-hoc consent, so a denial
// cannot veto the individual call — it can only kill the whole run (the real
// drivers call Cancel() on !Allow; see consent.go's POST-HOC CONSENT note). This
// makes deny-cancels assertions load-bearing rather than relying on a test
// goroutine to cancel separately.
func (f *FakeRunner) Decide(decision runner.PermissionDecision) {
	f.mu.Lock()
	f.decisions = append(f.decisions, decision)
	f.mu.Unlock()
	if !decision.Allow {
		f.Cancel()
	}
}

// Cancel marks the runner as canceled and closes the event channel.
// Safe to call multiple times.
func (f *FakeRunner) Cancel() {
	f.mu.Lock()
	if f.canceled {
		f.mu.Unlock()
		return
	}
	f.canceled = true
	close(f.done)
	f.mu.Unlock()
	// Wait for every in-flight InjectEvent to leave before closing the stream.
	f.sendMu.Lock()
	close(f.eventCh)
	f.sendMu.Unlock()
}

// Input records the text sent to the runner.
func (f *FakeRunner) Input(text string) error {
	f.mu.Lock()
	f.inputsSent = append(f.inputsSent, text)
	f.mu.Unlock()
	return nil
}

// Resume records the runID (and any delivered instruction) and returns the
// same event channel. Like Run, it honours the ctx-cancel-ends-the-stream
// contract (the real drivers' Resume re-enters the same Run implementation).
func (f *FakeRunner) Resume(ctx context.Context, runID string, instruction ...string) (<-chan runner.RunEvent, error) {
	f.mu.Lock()
	f.resumeIDs = append(f.resumeIDs, runID)
	f.resumeInputs = append(f.resumeInputs, firstInstruction(instruction))
	f.mu.Unlock()
	f.watchCancel(ctx)
	return f.eventCh, nil
}

// firstInstruction is the instruction a Resume call delivered: the first
// argument, or "" for a bare continuation.
func firstInstruction(instruction []string) string {
	if len(instruction) == 0 {
		return ""
	}
	return instruction[0]
}

// ReceivedResumeInputs returns the instruction delivered by every Resume call
// (snapshot), parallel to ReceivedResumeIDs.
func (f *FakeRunner) ReceivedResumeInputs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.resumeInputs))
	copy(out, f.resumeInputs)
	return out
}

// Test returns the configured test result (default: OK=true).
func (f *FakeRunner) Test(ctx context.Context) runner.ConnectionTestResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.testResult
}

// SetTestResult configures the result returned by Test.
func (f *FakeRunner) SetTestResult(r runner.ConnectionTestResult) {
	f.mu.Lock()
	f.testResult = r
	f.mu.Unlock()
}

// ErrFakeRunnerCancelled is returned by InjectEvent after Cancel: the event
// stream is closed, so the event is dropped rather than sent on a closed channel.
var ErrFakeRunnerCancelled = errors.New("runnertest: event dropped, the fake runner was cancelled")

// InjectEvent sends a runner.RunEvent into the event channel.
// The event Timestamp is set to now if zero. After Cancel (or if Cancel lands
// while the buffer is full) the event is dropped and ErrFakeRunnerCancelled is
// returned - never a send on a closed channel, never a panic.
func (f *FakeRunner) InjectEvent(ev runner.RunEvent) error {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	f.sendMu.RLock()
	defer f.sendMu.RUnlock()
	select {
	case <-f.done:
		return ErrFakeRunnerCancelled
	default:
	}
	select {
	case f.eventCh <- ev:
		return nil
	case <-f.done:
		return ErrFakeRunnerCancelled
	}
}

// ReceivedDecisions returns all decisions received via Decide (snapshot).
func (f *FakeRunner) ReceivedDecisions() []runner.PermissionDecision {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runner.PermissionDecision, len(f.decisions))
	copy(out, f.decisions)
	return out
}

// ReceivedInputs returns all text received via Input (snapshot).
func (f *FakeRunner) ReceivedInputs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.inputsSent))
	copy(out, f.inputsSent)
	return out
}

// RecordedRunOpts returns all runner.RunOptions passed to Run (snapshot).
func (f *FakeRunner) RecordedRunOpts() []runner.RunOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runner.RunOptions, len(f.runOpts))
	copy(out, f.runOpts)
	return out
}

// IsCancelled returns whether Cancel has been called.
func (f *FakeRunner) IsCancelled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canceled
}

// compile-time interface assertion
var _ runner.ExternalAgentRunner = (*FakeRunner)(nil)
