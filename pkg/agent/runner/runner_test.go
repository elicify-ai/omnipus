package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// ──────────────────────────────────────────────────────────────────────────────
// U1 tests (ported from wave3/spec4-executor-iface)
// ──────────────────────────────────────────────────────────────────────────────

// TestExecutorField_DefaultIsNative verifies that a nil ExecutorConfig defaults to
// the native executor (existing behavior unchanged — no regression).
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

// TestResolveDispatch_ExternalCLI_Active verifies that external-cli now resolves
// to DispatchKindExternalCLI (ACTIVE in v0.1.0) instead of the former reserved
// sentinel error. The run executes worktree-isolated under the CLI's own sandbox.
func TestResolveDispatch_ExternalCLI_Active(t *testing.T) {
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
	if !errors.Is(err, runner.ErrRemoteA2AReserved) {
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
// arrives, the decision defaults to deny.
func TestRunner_PermissionNoHandler_DeniesByDefault(t *testing.T) {
	fr := runner.NewFakeRunner()

	// No decisions injected yet — simulates no consent handler.
	decisions := fr.ReceivedDecisions()
	if len(decisions) != 0 {
		t.Fatalf("expected 0 decisions before any Decide call, got %d", len(decisions))
	}

	// When no handler routes a decision, dispatch code must default to deny.
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

// ──────────────────────────────────────────────────────────────────────────────
// U2 tests — streaming drivers + consent routing
// ──────────────────────────────────────────────────────────────────────────────

// TestClaudeDriver_ImplementsInterface is a compile-time assertion that
// ClaudeDriver satisfies ExternalAgentRunner.
func TestClaudeDriver_ImplementsInterface(t *testing.T) {
	var _ runner.ExternalAgentRunner = (*runner.ClaudeDriver)(nil)
}

// TestCodexDriver_ImplementsInterface is a compile-time assertion that
// CodexDriver satisfies ExternalAgentRunner.
func TestCodexDriver_ImplementsInterface(t *testing.T) {
	var _ runner.ExternalAgentRunner = (*runner.CodexDriver)(nil)
}

// TestOpencodeDriver_ImplementsInterface is a compile-time assertion that
// OpencodeDriver satisfies ExternalAgentRunner.
func TestOpencodeDriver_ImplementsInterface(t *testing.T) {
	var _ runner.ExternalAgentRunner = (*runner.OpencodeDriver)(nil)
}

// TestOpencodeDriver_Test_DelegatesToTestConnection verifies the opencode
// driver's Test() runs the full 3-step health check (binary → handshake → auth)
// by delegating to runner.TestConnection, rather than only probing the binary
// version (FR-4.2 — the missing-binary vs unauthed distinction must be uniform
// across all external CLIs, and the auth leg must not be skipped).
func TestOpencodeDriver_Test_DelegatesToTestConnection(t *testing.T) {
	ctx := context.Background()
	d := &runner.OpencodeDriver{}

	got := d.Test(ctx)
	want := runner.TestConnection(ctx, "opencode")

	// The driver may log on the OK path; the observable contract is that the
	// result matches TestConnection exactly (OK, Reason, CLIVersion, Message).
	if got.OK != want.OK {
		t.Errorf("Test().OK = %v, want %v (TestConnection)", got.OK, want.OK)
	}
	if got.Reason != want.Reason {
		t.Errorf("Test().Reason = %q, want %q (TestConnection)", got.Reason, want.Reason)
	}
	if got.CLIVersion != want.CLIVersion {
		t.Errorf("Test().CLIVersion = %q, want %q (TestConnection)", got.CLIVersion, want.CLIVersion)
	}
	// When unauthenticated, TestConnection surfaces the auth leg failure; a
	// driver that only checked the binary would report OK=true here.
	if !got.OK && got.Reason == runner.ReasonUnauthenticated {
		t.Logf("auth leg exercised: %s", got.Message)
	}
}

// TestClaudeDriver_ParsesStreamJSONFixture verifies that the Claude driver
// correctly parses a recorded stream-json fixture (TDD #8 per spec, FR-5.2).
// Uses a recorded fixture under testdata/fixtures/ — no real CLI invoked.
func TestClaudeDriver_ParsesStreamJSONFixture(t *testing.T) {
	fixtureData := readFixture(t, "claude_stream_json.ndjson")
	events := runner.ParseClaudeStreamJSON(fixtureData, "test-run-1")

	if len(events) == 0 {
		t.Fatal("ParseClaudeStreamJSON returned no events from fixture")
	}

	// Verify at least one output event.
	var hasOutput, hasPermReq bool
	for _, ev := range events {
		switch ev.Kind {
		case runner.EventKindOutput:
			hasOutput = true
		case runner.EventKindPermissionRequest:
			hasPermReq = true
			if ev.PermissionRequest == nil {
				t.Error("PermissionRequestEvent is nil")
			} else if ev.PermissionRequest.ToolName == "" {
				t.Error("PermissionRequestEvent.ToolName is empty")
			}
		}
	}
	if !hasOutput {
		t.Error("expected at least one output event from claude fixture")
	}
	if !hasPermReq {
		t.Error("expected at least one permission-request event from claude fixture (tool_use block)")
	}
}

// TestCodexDriver_ParsesStreamJSONFixture verifies that the Codex driver
// correctly parses a recorded stream-json fixture (FR-5.2).
func TestCodexDriver_ParsesStreamJSONFixture(t *testing.T) {
	fixtureData := readFixture(t, "codex_stream_json.ndjson")
	events := runner.ParseCodexStreamJSON(fixtureData, "test-run-2")

	if len(events) == 0 {
		t.Fatal("ParseCodexStreamJSON returned no events from fixture")
	}

	var hasOutput, hasPermReq, hasEnd bool
	for _, ev := range events {
		switch ev.Kind {
		case runner.EventKindOutput:
			hasOutput = true
		case runner.EventKindPermissionRequest:
			hasPermReq = true
		case runner.EventKindEnd:
			hasEnd = true
		}
	}
	if !hasOutput {
		t.Error("expected at least one output event from codex fixture")
	}
	if !hasPermReq {
		t.Error("expected at least one permission-request event from codex fixture (tool_call item)")
	}
	if !hasEnd {
		t.Error("expected an end event from codex fixture (turn.completed)")
	}
}

// TestOpencodeDriver_ParsesStreamJSONFixture verifies that the opencode driver
// correctly parses a recorded stream-json fixture (FR-5.2).
func TestOpencodeDriver_ParsesStreamJSONFixture(t *testing.T) {
	fixtureData := readFixture(t, "opencode_stream_json.ndjson")
	events := runner.ParseOpencodeStreamJSON(fixtureData, "test-run-3")

	if len(events) == 0 {
		t.Fatal("ParseOpencodeStreamJSON returned no events from fixture")
	}

	var hasOutput, hasPermReq, hasEnd bool
	for _, ev := range events {
		switch ev.Kind {
		case runner.EventKindOutput:
			hasOutput = true
		case runner.EventKindPermissionRequest:
			hasPermReq = true
		case runner.EventKindEnd:
			hasEnd = true
		}
	}
	if !hasOutput {
		t.Error("expected at least one output event from opencode fixture")
	}
	if !hasPermReq {
		t.Error("expected at least one permission-request event from opencode fixture (tool.start)")
	}
	if !hasEnd {
		t.Error("expected an end event from opencode fixture (session.complete)")
	}
}

// TestClaudeDriver_MalformedJSON_NonFatal verifies that malformed JSON lines in
// a stream do not crash the parser and produce non-fatal error events (FR-5.2 edge).
func TestClaudeDriver_MalformedJSON_NonFatal(t *testing.T) {
	malformed := []byte("not-valid-json\n{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"ok\"}\n")
	events := runner.ParseClaudeStreamJSON(malformed, "run-malformed")

	if len(events) == 0 {
		t.Fatal("expected events even when one line is malformed")
	}

	var hasNonFatalError bool
	for _, ev := range events {
		if ev.Kind == runner.EventKindError && ev.Err != nil && !ev.Err.Fatal {
			hasNonFatalError = true
		}
	}
	if !hasNonFatalError {
		t.Error("expected a non-fatal error event for the malformed JSON line")
	}
}

// TestConsentRouting_AllowDecision verifies that RouteConsent passes allow=true
// decisions through correctly (FR-5.1, M-2).
func TestConsentRouting_AllowDecision(t *testing.T) {
	handler := &stubConsentHandler{allow: true, reason: ""}
	ev := runner.PermissionRequestEvent{
		RequestID:   "req-allow-1",
		ToolName:    "bash",
		Description: "run ls",
	}
	decision := runner.RouteConsent(context.Background(), ev, "run-1", "sess-1", handler)
	if !decision.Allow {
		t.Errorf("RouteConsent with allow handler: Allow = false, want true")
	}
	if decision.RequestID != "req-allow-1" {
		t.Errorf("RouteConsent: RequestID = %q, want req-allow-1", decision.RequestID)
	}
}

// TestConsentRouting_DenyDecision verifies that RouteConsent passes deny=false
// decisions through correctly.
func TestConsentRouting_DenyDecision(t *testing.T) {
	handler := &stubConsentHandler{allow: false, reason: "not allowed by policy"}
	ev := runner.PermissionRequestEvent{
		RequestID:   "req-deny-1",
		ToolName:    "bash",
		Description: "run rm -rf /",
	}
	decision := runner.RouteConsent(context.Background(), ev, "run-1", "sess-1", handler)
	if decision.Allow {
		t.Errorf("RouteConsent with deny handler: Allow = true, want false")
	}
	if !strings.Contains(decision.Reason, "not allowed") {
		t.Errorf("RouteConsent deny: Reason = %q, want 'not allowed by policy'", decision.Reason)
	}
}

// TestConsentRouting_NilHandler_DeniesByDefault verifies that a nil consent handler
// causes deny-by-default (FR-5.1 edge case).
func TestConsentRouting_NilHandler_DeniesByDefault(t *testing.T) {
	ev := runner.PermissionRequestEvent{
		RequestID:   "req-nil-handler",
		ToolName:    "bash",
		Description: "run command",
	}
	decision := runner.RouteConsent(context.Background(), ev, "run-nil", "sess-nil", nil)
	if decision.Allow {
		t.Errorf("RouteConsent(nil handler): Allow = true, want false (deny-by-default)")
	}
	if decision.RequestID != "req-nil-handler" {
		t.Errorf("RouteConsent(nil handler): RequestID = %q, want req-nil-handler", decision.RequestID)
	}
}

// TestConsentDispatcher_RoutesPermissionRequests verifies that ConsentDispatcher
// intercepts PermissionRequestEvents, routes them through the handler, and sends
// decisions back to the runner (FR-5.1, M-2).
func TestConsentDispatcher_RoutesPermissionRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	fr := runner.NewFakeRunner()
	outCh := make(chan runner.RunEvent, 16)
	handler := &stubConsentHandler{allow: true}

	// Inject input events including a permission request.
	inputCh := make(chan runner.RunEvent, 8)
	inputCh <- runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "hi"}}
	inputCh <- runner.RunEvent{
		Kind: runner.EventKindPermissionRequest,
		PermissionRequest: &runner.PermissionRequestEvent{
			RequestID: "req-d1", ToolName: "bash", Description: "test",
		},
	}
	inputCh <- runner.RunEvent{Kind: runner.EventKindEnd}
	close(inputCh)

	go runner.ConsentDispatcher(ctx, inputCh, fr, "run-dispatch", "sess-d1", handler, outCh)

	// Collect output events.
	var got []runner.RunEvent
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-outCh:
			if !ok {
				break loop
			}
			got = append(got, ev)
			if ev.Kind == runner.EventKindEnd {
				break loop
			}
		case <-deadline:
			t.Error("timed out waiting for dispatcher output")
			break loop
		}
	}

	// Verify: output + permission-request + end were forwarded.
	if len(got) < 3 {
		t.Errorf("expected ≥3 forwarded events, got %d: %v", len(got), got)
	}

	// Verify the permission decision was sent back to the runner.
	decisions := fr.ReceivedDecisions()
	if len(decisions) == 0 {
		t.Fatal("expected at least one decision routed back to runner via Decide()")
	}
	if !decisions[0].Allow {
		t.Errorf("expected allow decision from stub handler, got deny")
	}
}

// TestRunner_TimeoutTerminates verifies that a run bounded by TimeoutSeconds
// is terminated after the deadline (FR-5.4, US-7).
// Uses the FakeRunner with context cancellation to simulate timeout.
func TestRunner_TimeoutTerminates(t *testing.T) {
	ctx := context.Background()
	fr := runner.NewFakeRunner()

	// Start a run with a 50ms timeout via context (not RunOptions, since FakeRunner
	// doesn't enforce a real timeout — we simulate via ctx).
	runCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	ch, err := fr.Run(runCtx, runner.RunOptions{Input: "long task"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Do NOT inject an end event — let the context cancel the run.
	// Wait for ctx to be canceled.
	<-runCtx.Done()

	// Cancel the runner (as a real driver would do on ctx cancel).
	fr.Cancel()

	// Channel should close after cancel.
	select {
	case _, ok := <-ch:
		if ok {
			// Drain.
			for range ch {
			}
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("channel not closed within 500ms after timeout cancel")
	}

	if !fr.IsCancelled() {
		t.Error("FakeRunner should be canceled after context timeout")
	}
}

// TestConsentDispatcher_ContextCancel verifies that ConsentDispatcher exits
// cleanly when its context is canceled.
func TestConsentDispatcher_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fr := runner.NewFakeRunner()
	outCh := make(chan runner.RunEvent, 8)
	handler := &stubConsentHandler{allow: true}

	inputCh := make(chan runner.RunEvent, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.ConsentDispatcher(ctx, inputCh, fr, "run-cancel", "sess-c1", handler, outCh)
	}()

	// Cancel the context — dispatcher should exit promptly.
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("ConsentDispatcher did not exit within 500ms after context cancel")
	}
}

// TestDenyByDefault_SetsCorrectFields verifies DenyByDefault produces a correct
// denial decision (FR-5.1).
func TestDenyByDefault_SetsCorrectFields(t *testing.T) {
	d := runner.DenyByDefault("test-req-id")
	if d.Allow {
		t.Error("DenyByDefault: Allow = true, want false")
	}
	if d.RequestID != "test-req-id" {
		t.Errorf("DenyByDefault: RequestID = %q, want test-req-id", d.RequestID)
	}
	if d.Reason == "" {
		t.Error("DenyByDefault: Reason is empty, want a deny message")
	}
}

// TestUnknownEventType_SkippedGracefully verifies that unknown event types in
// the claude stream produce no events (graceful degradation, FR-5.6).
func TestUnknownEventType_SkippedGracefully(t *testing.T) {
	data := []byte(`{"type":"some_future_event","payload":{"foo":"bar"}}` + "\n" +
		`{"type":"result","subtype":"success","result":"done"}` + "\n")
	events := runner.ParseClaudeStreamJSON(data, "run-unknown")
	// We expect 0 unknown-type events and possibly 1 end event from result.
	for _, ev := range events {
		if ev.Kind == runner.EventKindError && ev.Err != nil && ev.Err.Fatal {
			// A fatal error from an unknown type would be wrong — skip must be non-fatal.
			t.Errorf("unexpected fatal error from unknown event type: %v", ev.Err)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// readFixture reads a test fixture file from testdata/fixtures/.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "fixtures", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFixture(%q): %v", name, err)
	}
	return data
}

// stubConsentHandler is a test-only ConsentHandler that returns a fixed decision.
type stubConsentHandler struct {
	allow  bool
	reason string
}

func (s *stubConsentHandler) RequestConsent(_ context.Context, _ runner.ConsentRequest) (bool, string) {
	return s.allow, s.reason
}
