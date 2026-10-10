// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FR-043 production wiring: runExternalCLISubTurn Runs a first dispatch but
// RESUMES the session's existing native CLI conversation for a continuation
// (the post-turn drain delivering a queued follow-up instruction), and a failed
// resume is a VISIBLE failure — never a silent fresh-run fallback.

package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// recordingExternalDriver records whether the dispatch Ran or Resumed, with the
// delivered instruction, and can be told to fail the resume.
type recordingExternalDriver struct {
	mu          sync.Mutex
	runCount    int
	hasRun      bool
	resumeInput []string
	resumeErr   error
}

func closedExternalChan() <-chan runner.RunEvent {
	ch := make(chan runner.RunEvent)
	close(ch)
	return ch
}

func (d *recordingExternalDriver) Run(context.Context, runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.mu.Lock()
	d.runCount++
	d.hasRun = true
	d.mu.Unlock()
	return closedExternalChan(), nil
}

func (d *recordingExternalDriver) Resume(_ context.Context, _ string, instruction ...string) (<-chan runner.RunEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// A real fresh driver has no captured native conversation; only a driver that
	// actually Ran can Resume. This makes a re-created driver VISIBLE: a
	// continuation that constructs a new instance can never silently succeed.
	if !d.hasRun {
		return nil, errors.New("recordingExternalDriver: Resume on a driver that never Ran (a fresh driver has no native conversation to continue)")
	}
	if d.resumeErr != nil {
		return nil, d.resumeErr
	}
	in := ""
	if len(instruction) > 0 {
		in = instruction[0]
	}
	d.resumeInput = append(d.resumeInput, in)
	return closedExternalChan(), nil
}

func (d *recordingExternalDriver) Decide(runner.PermissionDecision) {}
func (d *recordingExternalDriver) Cancel()                          {}
func (d *recordingExternalDriver) Input(string) error               { return nil }
func (d *recordingExternalDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{OK: true}
}

var _ runner.ExternalAgentRunner = (*recordingExternalDriver)(nil)

func withRecordingDriver(t *testing.T) (*recordingExternalDriver, func()) {
	t.Helper()
	d := &recordingExternalDriver{}
	prev := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return d, nil
	}
	return d, func() { newExternalDriver = prev }
}

// recordingDriverFactory hands out a DISTINCT recordingExternalDriver per
// newExternalDriver call and keeps them, so a test can prove a continuation
// REUSED the driver the first Run built rather than constructing a fresh one —
// which the single-fake withRecordingDriver above cannot observe.
type recordingDriverFactory struct {
	mu      sync.Mutex
	drivers []*recordingExternalDriver
}

func (f *recordingDriverFactory) make(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := &recordingExternalDriver{}
	f.drivers = append(f.drivers, d)
	return d, nil
}

func (f *recordingDriverFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.drivers)
}

func (f *recordingDriverFactory) only(t *testing.T) *recordingExternalDriver {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.drivers) != 1 {
		t.Fatalf("expected exactly one constructed driver (the first Run's, reused by the continuation), got %d", len(f.drivers))
	}
	return f.drivers[0]
}

// withRecordingDriverFactory installs a distinct-driver-per-call factory.
func withRecordingDriverFactory(t *testing.T) *recordingDriverFactory {
	t.Helper()
	f := &recordingDriverFactory{}
	prev := newExternalDriver
	newExternalDriver = f.make
	t.Cleanup(func() { newExternalDriver = prev })
	return f
}

func (d *recordingExternalDriver) snapshot() (runs int, resumeInput []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runCount, append([]string(nil), d.resumeInput...)
}

// FR-043: a first dispatch Runs; a continuation (ts.opts.ExternalCLIResume, the
// marker the post-turn drain sets) RESUMES the SAME native conversation and
// delivers the new instruction S to the runner.
func TestExternalRunFollowup_ContinuationResumesNativeConversation(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	// A factory that hands out a DISTINCT driver per construction: a continuation
	// that re-creates the driver is then observable (the shared single fake used
	// before could not see it — the F-03 gap), and a re-created driver's Resume
	// refuses outright because it never Ran.
	factory := withRecordingDriverFactory(t)

	if _, err := runExternalCLISubTurn(context.Background(), al, ts, "first instruction", 30*time.Second); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if got := factory.count(); got != 1 {
		t.Fatalf("a first dispatch must construct exactly one driver; constructed=%d, want 1", got)
	}
	drv := factory.only(t)
	if runs, _ := drv.snapshot(); runs != 1 {
		t.Fatalf("a first dispatch must Run; runCount=%d, want 1", runs)
	}

	// The post-turn drain (steer_turn_drain.go::continueSteeredTurn) marks the
	// reconstructed turn with ExternalCLIResume before re-entering the body.
	ts.opts.ExternalCLIResume = true
	if _, err := runExternalCLISubTurn(context.Background(), al, ts, "follow-up instruction S", 30*time.Second); err != nil {
		t.Fatalf("FR-043 continuation: %v", err)
	}

	// FR-043: the continuation must reuse the SAME driver instance — a fresh
	// driver has no captured native conversation. Exactly one construction proves
	// the driver was retained, not recreated.
	if got := factory.count(); got != 1 {
		t.Fatalf("FR-043: the continuation must REUSE the first Run's driver; constructed=%d, want 1 (a re-created driver loses the native conversation)", got)
	}
	runs, resumeInput := drv.snapshot()
	if runs != 1 {
		t.Fatalf("FR-043: a continuation must NOT start a fresh Run; runCount=%d, want 1", runs)
	}
	if len(resumeInput) != 1 || resumeInput[0] != "follow-up instruction S" {
		t.Fatalf("FR-043: the runner resumed with instruction %#v, want exactly [\"follow-up instruction S\"]", resumeInput)
	}
}

// FR-043 / BDD-05.6: when the resume cannot reach the native conversation (no
// captured id, rejected/crashed resume), the dispatch fails VISIBLY and does NOT
// fall back to a fresh Run (no silent fresh conversation).
func TestExternalRunFollowup_ResumeFailureIsVisibleNotAFreshRun(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	drv, restore := withRecordingDriver(t)
	defer restore()

	if _, err := runExternalCLISubTurn(context.Background(), al, ts, "first instruction", 30*time.Second); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}

	drv.mu.Lock()
	drv.resumeErr = errors.New("no native conversation id was captured from the prior run")
	drv.mu.Unlock()

	ts.opts.ExternalCLIResume = true
	res, err := runExternalCLISubTurn(context.Background(), al, ts, "follow-up instruction S", 30*time.Second)
	if err == nil {
		t.Fatalf("BDD-05.6: a failed resume must be a visible failure, got success (result=%v)", res)
	}
	if !strings.Contains(err.Error(), "resume native conversation") {
		t.Fatalf("the failure must name the resume, got: %v", err)
	}
	if runs, _ := drv.snapshot(); runs != 1 {
		t.Fatalf("BDD-05.6: a failed resume must NOT fall back to a fresh Run; runCount=%d, want 1", runs)
	}
}
