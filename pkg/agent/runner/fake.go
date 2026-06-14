package runner

import (
	"context"
	"sync"
	"time"
)

// FakeRunner is a test-only implementation of ExternalAgentRunner.
// It allows tests to inject events, simulate permission prompts, and verify
// that the bidirectional interface contract is satisfied.
//
// Usage:
//
//	fr := NewFakeRunner()
//	ch, err := fr.Run(ctx, RunOptions{Input: "do something"})
//	fr.InjectEvent(RunEvent{Kind: EventKindOutput, Output: &OutputEvent{Text: "hello"}})
//	fr.InjectEvent(RunEvent{Kind: EventKindEnd})
//	for ev := range ch { ... }
type FakeRunner struct {
	mu         sync.Mutex
	eventCh    chan RunEvent
	decisions  []PermissionDecision
	cancelled  bool
	inputsSent []string
	testResult ConnectionTestResult
	runOpts    []RunOptions
	resumeIDs  []string
}

// NewFakeRunner returns a FakeRunner with a buffered event channel.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		eventCh:    make(chan RunEvent, 64),
		testResult: ConnectionTestResult{OK: true, Message: "fake runner: OK", CLIVersion: "0.0.0-fake"},
	}
}

// Run records the options and returns the internal event channel.
func (f *FakeRunner) Run(ctx context.Context, opts RunOptions) (<-chan RunEvent, error) {
	f.mu.Lock()
	f.runOpts = append(f.runOpts, opts)
	f.mu.Unlock()
	return f.eventCh, nil
}

// Decide records the decision (tests can verify it was received).
func (f *FakeRunner) Decide(decision PermissionDecision) {
	f.mu.Lock()
	f.decisions = append(f.decisions, decision)
	f.mu.Unlock()
}

// Cancel marks the runner as cancelled and closes the event channel.
// Safe to call multiple times.
func (f *FakeRunner) Cancel() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.cancelled {
		f.cancelled = true
		close(f.eventCh)
	}
}

// Input records the text sent to the runner.
func (f *FakeRunner) Input(text string) error {
	f.mu.Lock()
	f.inputsSent = append(f.inputsSent, text)
	f.mu.Unlock()
	return nil
}

// Resume records the runID and returns the same event channel.
func (f *FakeRunner) Resume(ctx context.Context, runID string) (<-chan RunEvent, error) {
	f.mu.Lock()
	f.resumeIDs = append(f.resumeIDs, runID)
	f.mu.Unlock()
	return f.eventCh, nil
}

// Test returns the configured test result (default: OK=true).
func (f *FakeRunner) Test(ctx context.Context) ConnectionTestResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.testResult
}

// SetTestResult configures the result returned by Test.
func (f *FakeRunner) SetTestResult(r ConnectionTestResult) {
	f.mu.Lock()
	f.testResult = r
	f.mu.Unlock()
}

// InjectEvent sends a RunEvent into the event channel.
// The event Timestamp is set to now if zero.
func (f *FakeRunner) InjectEvent(ev RunEvent) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	f.eventCh <- ev
}

// ReceivedDecisions returns all decisions received via Decide (snapshot).
func (f *FakeRunner) ReceivedDecisions() []PermissionDecision {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]PermissionDecision, len(f.decisions))
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

// RunOptions returns all RunOptions passed to Run (snapshot).
func (f *FakeRunner) RecordedRunOpts() []RunOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RunOptions, len(f.runOpts))
	copy(out, f.runOpts)
	return out
}

// IsCancelled returns whether Cancel has been called.
func (f *FakeRunner) IsCancelled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled
}

// compile-time interface assertion
var _ ExternalAgentRunner = (*FakeRunner)(nil)
