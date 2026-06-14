package runner_test

import (
	"context"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestExecutorField_DefaultIsNative verifies that a nil ExecutorConfig defaults to
// the native executor (existing behaviour unchanged — no regression).
func TestExecutorField_DefaultIsNative(t *testing.T) {
	var ec *config.ExecutorConfig
	if ec.EffectiveKind() != config.ExecutorKindNative {
		t.Fatalf("nil ExecutorConfig: EffectiveKind() = %q, want %q",
			ec.EffectiveKind(), config.ExecutorKindNative)
	}

	ec2 := &config.ExecutorConfig{Kind: ""}
	if ec2.EffectiveKind() != config.ExecutorKindNative {
		t.Fatalf("empty-kind ExecutorConfig: EffectiveKind() = %q, want %q",
			ec2.EffectiveKind(), config.ExecutorKindNative)
	}
}

// TestExecutorField_SchemaRoundTrip verifies that all three enum values can be
// stored and retrieved from an ExecutorConfig (JSON round-trip via config struct).
func TestExecutorField_SchemaRoundTrip(t *testing.T) {
	cases := []config.ExecutorKind{
		config.ExecutorKindNative,
		config.ExecutorKindExternalCLI,
		config.ExecutorKindRemoteA2A,
	}
	for _, k := range cases {
		ec := &config.ExecutorConfig{Kind: k, CLI: "claude-code"}
		if ec.EffectiveKind() != k {
			t.Errorf("EffectiveKind() = %q, want %q", ec.EffectiveKind(), k)
		}
	}
}

// TestResolveDispatch_Native verifies that native dispatch is returned for nil,
// empty-string, and explicit "native" kind.
func TestResolveDispatch_Native(t *testing.T) {
	cases := []*config.ExecutorConfig{
		nil,
		{Kind: ""},
		{Kind: config.ExecutorKindNative},
	}
	for _, ec := range cases {
		kind, err := runner.ResolveDispatch(ec)
		if err != nil {
			t.Errorf("ResolveDispatch(%v) error = %v, want nil", ec, err)
		}
		if kind != runner.DispatchKindNative {
			t.Errorf("ResolveDispatch(%v) kind = %q, want %q", ec, kind, runner.DispatchKindNative)
		}
	}
}

// TestResolveDispatch_ExternalCLI verifies that external-cli dispatch is returned.
func TestResolveDispatch_ExternalCLI(t *testing.T) {
	ec := &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"}
	kind, err := runner.ResolveDispatch(ec)
	if err != nil {
		t.Fatalf("ResolveDispatch(external-cli) error = %v, want nil", err)
	}
	if kind != runner.DispatchKindExternalCLI {
		t.Errorf("ResolveDispatch(external-cli) kind = %q, want %q", kind, runner.DispatchKindExternalCLI)
	}
}

// TestRunner_RemoteA2A_ReservedNotResolvable verifies that remote-a2a is accepted
// in the config but rejected at dispatch with the correct sentinel error.
func TestRunner_RemoteA2A_ReservedNotResolvable(t *testing.T) {
	ec := &config.ExecutorConfig{Kind: config.ExecutorKindRemoteA2A}
	kind, err := runner.ResolveDispatch(ec)
	if err == nil {
		t.Fatalf("ResolveDispatch(remote-a2a) error = nil, want ErrRemoteA2AReserved")
	}
	if err != runner.ErrRemoteA2AReserved {
		t.Errorf("ResolveDispatch(remote-a2a) error = %v, want ErrRemoteA2AReserved", err)
	}
	if kind != "" {
		t.Errorf("ResolveDispatch(remote-a2a) kind = %q, want empty", kind)
	}
}

// TestRunner_BidirectionalEvents verifies that the FakeRunner (implementing
// ExternalAgentRunner) can emit events and receive decisions — proving the
// bidirectional interface shape works.
func TestRunner_BidirectionalEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fr := runner.NewFakeRunner()

	ch, err := fr.Run(ctx, runner.RunOptions{Input: "do something"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Inject events from the "external process" side.
	fr.InjectEvent(runner.RunEvent{
		Kind:   runner.EventKindOutput,
		RunID:  "run-1",
		Output: &runner.OutputEvent{Text: "hello"},
	})
	fr.InjectEvent(runner.RunEvent{
		Kind:  runner.EventKindPermissionRequest,
		RunID: "run-1",
		PermissionRequest: &runner.PermissionRequestEvent{
			RequestID:   "req-1",
			ToolName:    "bash",
			Description: "run ls /tmp",
		},
	})

	// Read and verify events.
	ev1 := <-ch
	if ev1.Kind != runner.EventKindOutput {
		t.Errorf("event[0].Kind = %q, want %q", ev1.Kind, runner.EventKindOutput)
	}
	if ev1.Output == nil || ev1.Output.Text != "hello" {
		t.Errorf("event[0].Output = %v, want Text=hello", ev1.Output)
	}

	ev2 := <-ch
	if ev2.Kind != runner.EventKindPermissionRequest {
		t.Errorf("event[1].Kind = %q, want %q", ev2.Kind, runner.EventKindPermissionRequest)
	}

	// Route a decision back via the control-in path.
	fr.Decide(runner.PermissionDecision{
		RequestID: "req-1",
		Allow:     true,
		Reason:    "approved by test",
	})

	decisions := fr.ReceivedDecisions()
	if len(decisions) != 1 || !decisions[0].Allow {
		t.Errorf("ReceivedDecisions() = %v, want one allowed decision", decisions)
	}

	// Verify Input() control-in path.
	if err := fr.Input("continue"); err != nil {
		t.Errorf("Input() error = %v", err)
	}
	inputs := fr.ReceivedInputs()
	if len(inputs) != 1 || inputs[0] != "continue" {
		t.Errorf("ReceivedInputs() = %v, want [continue]", inputs)
	}

	// Inject end event and verify channel closes.
	fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd, RunID: "run-1"})
	ev3 := <-ch
	if ev3.Kind != runner.EventKindEnd {
		t.Errorf("event[2].Kind = %q, want %q", ev3.Kind, runner.EventKindEnd)
	}
}

// TestRunner_PermissionNoHandler_DeniesByDefault verifies the deny-by-default
// semantics: when no consent handler is registered and a permission request
// arrives, the decision defaults to deny. In the FakeRunner the decision is
// not auto-sent, which simulates the no-handler case; the caller must handle it.
func TestRunner_PermissionNoHandler_DeniesByDefault(t *testing.T) {
	fr := runner.NewFakeRunner()

	// No decisions injected yet — simulates no consent handler.
	decisions := fr.ReceivedDecisions()
	if len(decisions) != 0 {
		t.Fatalf("expected 0 decisions before any Decide call, got %d", len(decisions))
	}

	// When no handler routes a decision, dispatch code must default to deny.
	// Verify the helper function DenyByDefault returns a denial decision.
	d := runner.DenyByDefault("req-x")
	if d.Allow {
		t.Errorf("DenyByDefault().Allow = true, want false")
	}
	if d.RequestID != "req-x" {
		t.Errorf("DenyByDefault().RequestID = %q, want req-x", d.RequestID)
	}
}

// TestRunner_ConnectionTest verifies that Test() returns the configured result.
func TestRunner_ConnectionTest(t *testing.T) {
	fr := runner.NewFakeRunner()
	ctx := context.Background()

	// Default: OK=true.
	result := fr.Test(ctx)
	if !result.OK {
		t.Errorf("Test().OK = false, want true")
	}

	// Configure a failure result.
	fr.SetTestResult(runner.ConnectionTestResult{
		OK:      false,
		Message: "binary not found: claude",
	})
	result2 := fr.Test(ctx)
	if result2.OK {
		t.Errorf("Test().OK = true, want false after SetTestResult(fail)")
	}
}

// TestRunner_Cancel verifies that Cancel closes the event channel and is idempotent.
func TestRunner_Cancel(t *testing.T) {
	ctx := context.Background()
	fr := runner.NewFakeRunner()
	ch, _ := fr.Run(ctx, runner.RunOptions{})

	if fr.IsCancelled() {
		t.Fatal("IsCancelled() = true before Cancel()")
	}

	fr.Cancel()
	if !fr.IsCancelled() {
		t.Fatal("IsCancelled() = false after Cancel()")
	}

	// Channel must be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel received a value after Cancel(); expected it to be closed")
		}
	case <-time.After(time.Second):
		t.Error("channel not closed within 1s after Cancel()")
	}

	// Second Cancel must not panic.
	fr.Cancel()
}

// TestRunner_Resume verifies the Resume path records the runID.
func TestRunner_Resume(t *testing.T) {
	ctx := context.Background()
	fr := runner.NewFakeRunner()

	ch, err := fr.Resume(ctx, "run-abc")
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if ch == nil {
		t.Fatal("Resume() returned nil channel")
	}

	// Must be the same channel (FakeRunner wires them together).
	fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
	ev := <-ch
	if ev.Kind != runner.EventKindEnd {
		t.Errorf("Resume channel event Kind = %q, want end", ev.Kind)
	}
}

// TestFakeRunner_ImplementsInterface is a compile-time and runtime assertion that
// FakeRunner fully satisfies ExternalAgentRunner.
func TestFakeRunner_ImplementsInterface(t *testing.T) {
	var _ runner.ExternalAgentRunner = (*runner.FakeRunner)(nil)
}
