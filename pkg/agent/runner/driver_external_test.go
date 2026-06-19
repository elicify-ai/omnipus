package runner

import (
	"os/exec"
	"runtime"
	"testing"
)

// TestExternalEventParser_MockProcess exercises the generic external-event
// parser against a real child process. The mock emits the canonical JSON Lines
// protocol on stdout; ParseExternalStream consumes it. This mirrors how a real
// external CLI driver would read a process stream.
func TestExternalEventParser_MockProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}

	stub := writeStubScript(t, `#!/bin/sh
printf '%s\n' '{"type":"message","message":{"role":"assistant","text":"hello from mock"}}'
printf '%s\n' '{"type":"tool_use","tool_use":{"id":"t-1","name":"bash","input":{"cmd":"ls"}}}'
printf '%s\n' '{"type":"tool_result","tool_result":{"id":"t-1","output":{"status":"ok"}}}'
printf '%s\n' '{"type":"permission_request","permission_request":{"id":"p-1","tool_name":"bash","description":"run rm"}}'
printf '%s\n' '{"type":"error","error":{"message":"mock fatal"}}'
printf '%s\n' '{"type":"done"}'
exit 0
`)

	out, err := exec.Command(stub).Output()
	if err != nil {
		t.Fatalf("mock process failed: %v", err)
	}

	events := ParseExternalStream(out, "run-mock")

	want := []EventKind{
		EventKindOutput,
		EventKindToolCall,
		EventKindToolResult,
		EventKindPermissionRequest,
		EventKindError,
		EventKindEnd,
	}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %v", len(events), len(want), events)
	}
	for i, kind := range want {
		if events[i].Kind != kind {
			t.Errorf("events[%d].Kind = %q, want %q", i, events[i].Kind, kind)
		}
	}

	if events[0].Output == nil || events[0].Output.Text != "hello from mock" {
		t.Errorf("first event text = %v, want 'hello from mock'", events[0].Output)
	}
	if events[1].ToolCall == nil || events[1].ToolCall.CallID != "t-1" {
		t.Errorf("tool-call event = %+v", events[1].ToolCall)
	}
	if events[2].ToolResult == nil || string(events[2].ToolResult.Output) != `{"status":"ok"}` {
		t.Errorf("tool-result event = %+v", events[2].ToolResult)
	}
	if events[3].PermissionRequest == nil || events[3].PermissionRequest.RequestID != "p-1" {
		t.Errorf("permission-request event = %+v", events[3].PermissionRequest)
	}
}
