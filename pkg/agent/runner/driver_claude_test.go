// driver_claude_test.go — regression coverage for two live-verified
// driver_claude.go bugs:
//
//   - A1: buildArgs must NOT append a trailing "-" positional argument. Live
//     testing against the real claude binary (v2.1.199) proved "-" is read as
//     a literal one-character prompt string, not a stdin sentinel
//     (`claude -p - --output-format json < /dev/null` succeeds and proceeds
//     past the "input required" check). The correct, documented pattern is to
//     omit the positional prompt entirely and let `claude -p` consume all of
//     stdin automatically.
//   - A2: parseResultEvent must check the "is_error" field, not just
//     "subtype". A live authentication failure produces
//     {"type":"result","subtype":"success","is_error":true,
//     "result":"Not logged in · Please run /login",...} — subtype says
//     success while is_error is true. This must surface as a fatal
//     EventKindError, not silently succeed with no output (or worse, treated
//     as normal output text).

package runner

import "testing"

// TestClaudeDriver_BuildArgs_NoTrailingDashSentinel is the A1 regression test.
func TestClaudeDriver_BuildArgs_NoTrailingDashSentinel(t *testing.T) {
	d := NewClaudeDriver(nil)
	args := d.buildArgs(RunOptions{Input: "task"})
	if len(args) == 0 {
		t.Fatal("expected non-empty args")
	}
	if last := args[len(args)-1]; last == "-" {
		t.Fatalf(
			`buildArgs must not end with a trailing "-" positional argument (not a real stdin sentinel for claude); args=%v`,
			args,
		)
	}
	for _, a := range args {
		if a == "-" {
			t.Fatalf(`buildArgs must not contain a bare "-" token anywhere; args=%v`, args)
		}
	}
}

// TestClaudeDriver_BuildArgs_NoTrailingDashSentinel_WithCLIArgs proves the
// fix holds even when operator cli_args are present — kept cli_args must be
// the last tokens in argv, with nothing appended after them.
func TestClaudeDriver_BuildArgs_NoTrailingDashSentinel_WithCLIArgs(t *testing.T) {
	d := NewClaudeDriver(nil)
	args := d.buildArgs(RunOptions{Input: "task", CLIArgs: []string{"--add-dir", "/tmp/x"}})
	if last := args[len(args)-1]; last == "-" {
		t.Fatalf(`buildArgs must not append "-" after operator cli_args; args=%v`, args)
	}
	if !containsSeq(args, "--add-dir", "/tmp/x") {
		t.Fatalf("expected operator cli_args to be kept in argv; args=%v", args)
	}
}

// TestClaudeDriver_ParseResultEvent_SuccessSubtypeWithIsErrorTrue is the A2
// regression test: subtype "success" + is_error true must be treated as a
// fatal error, using Result as the message text (that is where the real
// error text lives in this observed shape).
func TestClaudeDriver_ParseResultEvent_SuccessSubtypeWithIsErrorTrue(t *testing.T) {
	d := NewClaudeDriver(nil)
	raw := []byte(
		`{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login","session_id":"abc123"}`,
	)

	ev, ok := d.parseResultEvent(raw, "run-1")
	if !ok {
		t.Fatal("expected an event to be emitted")
	}
	if ev.Kind != EventKindError {
		t.Fatalf("expected EventKindError for is_error:true, got Kind=%q ev=%+v", ev.Kind, ev)
	}
	if ev.Err == nil {
		t.Fatal("expected a non-nil Err payload")
	}
	if !ev.Err.Fatal {
		t.Fatal("is_error:true must be reported as a FATAL error")
	}
	if ev.Err.Message != "Not logged in · Please run /login" {
		t.Fatalf("expected Err.Message to be the result text; got %q", ev.Err.Message)
	}
}

// TestClaudeDriver_ParseResultEvent_SuccessIsErrorTrue_EmptyResultFallsBack
// proves an empty Result with is_error:true still produces a fatal error
// event with a generic (non-empty) message, never a silent success.
func TestClaudeDriver_ParseResultEvent_SuccessIsErrorTrue_EmptyResultFallsBack(t *testing.T) {
	d := NewClaudeDriver(nil)
	raw := []byte(`{"type":"result","subtype":"success","is_error":true,"result":"","session_id":"abc123"}`)

	ev, ok := d.parseResultEvent(raw, "run-1")
	if !ok {
		t.Fatal("expected an event to be emitted")
	}
	if ev.Kind != EventKindError {
		t.Fatalf("expected EventKindError; got Kind=%q", ev.Kind)
	}
	if ev.Err == nil || ev.Err.Message == "" {
		t.Fatalf("expected a non-empty fallback error message; got %+v", ev.Err)
	}
	if !ev.Err.Fatal {
		t.Fatal("expected Fatal=true")
	}
}

// TestClaudeDriver_ParseResultEvent_SuccessIsErrorFalse_StillOutputsResult
// proves the fix does not regress the ordinary success path: subtype
// "success" with is_error absent (defaults false) must still emit output.
func TestClaudeDriver_ParseResultEvent_SuccessIsErrorFalse_StillOutputsResult(t *testing.T) {
	d := NewClaudeDriver(nil)
	raw := []byte(`{"type":"result","subtype":"success","result":"all done","session_id":"abc123"}`)

	ev, ok := d.parseResultEvent(raw, "run-1")
	if !ok {
		t.Fatal("expected an event to be emitted")
	}
	if ev.Kind != EventKindOutput {
		t.Fatalf("expected EventKindOutput when is_error is absent/false; got Kind=%q ev=%+v", ev.Kind, ev)
	}
	if ev.Output == nil || ev.Output.Text != "all done" {
		t.Fatalf("expected Output.Text=%q; got %+v", "all done", ev.Output)
	}
}

// TestClaudeDriver_ParseResultEvent_SuccessIsErrorFalse_NoResultEmitsEnd
// proves the "success, no result text" path still emits a plain End event
// (not an error) when is_error is explicitly false.
func TestClaudeDriver_ParseResultEvent_SuccessIsErrorFalse_NoResultEmitsEnd(t *testing.T) {
	d := NewClaudeDriver(nil)
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"","session_id":"abc123"}`)

	ev, ok := d.parseResultEvent(raw, "run-1")
	if !ok {
		t.Fatal("expected an event to be emitted")
	}
	if ev.Kind != EventKindEnd {
		t.Fatalf("expected EventKindEnd; got Kind=%q ev=%+v", ev.Kind, ev)
	}
}
