package runner

import (
	"testing"
)

func TestExternalEventParser_MessageToOutput(t *testing.T) {
	raw := []byte(`{"type":"message","message":{"role":"assistant","text":"hello"}}`)
	ev, err := ParseExternalEvent(raw)
	if err != nil {
		t.Fatalf("ParseExternalEvent error: %v", err)
	}
	if ev.Type != "message" {
		t.Errorf("Type = %q, want message", ev.Type)
	}
	if ev.Message == nil || ev.Message.Text != "hello" {
		t.Errorf("Message.Text = %v, want hello", ev.Message)
	}

	runEv, ok := ExternalEventToRunEvent(ev, "run-1")
	if !ok {
		t.Fatal("ExternalEventToRunEvent returned false")
	}
	if runEv.Kind != EventKindOutput {
		t.Errorf("Kind = %q, want output", runEv.Kind)
	}
	if runEv.Output == nil || runEv.Output.Text != "hello" {
		t.Errorf("Output = %v, want hello", runEv.Output)
	}
	if runEv.RunID != "run-1" {
		t.Errorf("RunID = %q, want run-1", runEv.RunID)
	}
}

func TestExternalEventParser_TextToOutput(t *testing.T) {
	raw := []byte(`{"type":"text","text":{"text":"world"}}`)
	ev, err := ParseExternalEvent(raw)
	if err != nil {
		t.Fatalf("ParseExternalEvent error: %v", err)
	}
	runEv, ok := ExternalEventToRunEvent(ev, "run-t")
	if !ok || runEv.Kind != EventKindOutput || runEv.Output.Text != "world" {
		t.Errorf("unexpected event: %+v", runEv)
	}
}

func TestExternalEventParser_ToolUseToToolCall(t *testing.T) {
	raw := []byte(`{"type":"tool_use","tool_use":{"id":"tu-1","name":"bash","input":{"cmd":"ls"}}}`)
	ev, err := ParseExternalEvent(raw)
	if err != nil {
		t.Fatalf("ParseExternalEvent error: %v", err)
	}
	runEv, ok := ExternalEventToRunEvent(ev, "run-tu")
	if !ok {
		t.Fatal("ExternalEventToRunEvent returned false")
	}
	if runEv.Kind != EventKindToolCall {
		t.Errorf("Kind = %q, want tool-call", runEv.Kind)
	}
	if runEv.ToolCall == nil {
		t.Fatal("ToolCall is nil")
	}
	if runEv.ToolCall.CallID != "tu-1" || runEv.ToolCall.ToolName != "bash" {
		t.Errorf("ToolCall = %+v, want tu-1/bash", runEv.ToolCall)
	}
	if string(runEv.ToolCall.ToolInput) != `{"cmd":"ls"}` {
		t.Errorf("ToolInput = %s, want {\"cmd\":\"ls\"}", runEv.ToolCall.ToolInput)
	}
}

func TestExternalEventParser_ToolCallAliasToToolCall(t *testing.T) {
	raw := []byte(`{"type":"tool_call","tool_call":{"id":"tc-1","name":"python","input":{"code":"1+1"}}}`)
	ev, err := ParseExternalEvent(raw)
	if err != nil {
		t.Fatalf("ParseExternalEvent error: %v", err)
	}
	runEv, ok := ExternalEventToRunEvent(ev, "run-tc")
	if !ok || runEv.Kind != EventKindToolCall {
		t.Fatalf("unexpected event: %+v", runEv)
	}
	if runEv.ToolCall.ToolName != "python" {
		t.Errorf("ToolName = %q, want python", runEv.ToolCall.ToolName)
	}
}

func TestExternalEventParser_ToolResultToToolResult(t *testing.T) {
	raw := []byte(`{"type":"tool_result","tool_result":{"id":"tr-1","name":"bash","output":{"status":"ok"},"is_error":false}}`)
	ev, err := ParseExternalEvent(raw)
	if err != nil {
		t.Fatalf("ParseExternalEvent error: %v", err)
	}
	runEv, ok := ExternalEventToRunEvent(ev, "run-tr")
	if !ok {
		t.Fatal("ExternalEventToRunEvent returned false")
	}
	if runEv.Kind != EventKindToolResult {
		t.Errorf("Kind = %q, want tool-result", runEv.Kind)
	}
	if runEv.ToolResult == nil {
		t.Fatal("ToolResult is nil")
	}
	if runEv.ToolResult.CallID != "tr-1" || runEv.ToolResult.ToolName != "bash" {
		t.Errorf("ToolResult = %+v", runEv.ToolResult)
	}
	if string(runEv.ToolResult.Output) != `{"status":"ok"}` {
		t.Errorf("Output = %s, want {\"status\":\"ok\"}", runEv.ToolResult.Output)
	}
	if runEv.ToolResult.IsError {
		t.Error("IsError = true, want false")
	}
}

func TestExternalEventParser_PermissionRequestToPause(t *testing.T) {
	raw := []byte(`{"type":"permission_request","permission_request":{"id":"pr-1","tool_name":"bash","description":"run ls","input":{"cmd":"ls"}}}`)
	ev, err := ParseExternalEvent(raw)
	if err != nil {
		t.Fatalf("ParseExternalEvent error: %v", err)
	}
	runEv, ok := ExternalEventToRunEvent(ev, "run-pr")
	if !ok {
		t.Fatal("ExternalEventToRunEvent returned false")
	}
	if runEv.Kind != EventKindPermissionRequest {
		t.Errorf("Kind = %q, want permission-request", runEv.Kind)
	}
	if runEv.PermissionRequest == nil {
		t.Fatal("PermissionRequest is nil")
	}
	if runEv.PermissionRequest.RequestID != "pr-1" || runEv.PermissionRequest.ToolName != "bash" {
		t.Errorf("PermissionRequest = %+v", runEv.PermissionRequest)
	}
}

func TestExternalEventParser_ErrorToError(t *testing.T) {
	raw := []byte(`{"type":"error","error":{"message":"it broke","code":"E1"}}`)
	ev, err := ParseExternalEvent(raw)
	if err != nil {
		t.Fatalf("ParseExternalEvent error: %v", err)
	}
	runEv, ok := ExternalEventToRunEvent(ev, "run-err")
	if !ok {
		t.Fatal("ExternalEventToRunEvent returned false")
	}
	if runEv.Kind != EventKindError {
		t.Errorf("Kind = %q, want error", runEv.Kind)
	}
	if runEv.Err == nil || runEv.Err.Message != "it broke" || !runEv.Err.Fatal {
		t.Errorf("Err = %+v, want fatal 'it broke'", runEv.Err)
	}
}

func TestExternalEventParser_DoneToEnd(t *testing.T) {
	raw := []byte(`{"type":"done"}`)
	ev, err := ParseExternalEvent(raw)
	if err != nil {
		t.Fatalf("ParseExternalEvent error: %v", err)
	}
	runEv, ok := ExternalEventToRunEvent(ev, "run-end")
	if !ok || runEv.Kind != EventKindEnd {
		t.Fatalf("unexpected event: %+v", runEv)
	}
}

func TestExternalEventParser_MalformedJSON(t *testing.T) {
	_, err := ParseExternalEvent([]byte(`not-json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseExternalStream_ProcessesMixedEvents(t *testing.T) {
	data := []byte(`
{"type":"message","message":{"role":"assistant","text":"hello"}}
{"type":"tool_use","tool_use":{"id":"t1","name":"bash","input":{"cmd":"ls"}}}
{"type":"tool_result","tool_result":{"id":"t1","output":{"status":"ok"}}}
{"type":"permission_request","permission_request":{"id":"p1","tool_name":"bash","description":"run rm"}}
{"type":"error","error":{"message":"boom"}}
{"type":"done"}
`)
	events := ParseExternalStream(data, "run-stream")

	wantKinds := []EventKind{
		EventKindOutput,
		EventKindToolCall,
		EventKindToolResult,
		EventKindPermissionRequest,
		EventKindError,
		EventKindEnd,
	}
	if len(events) != len(wantKinds) {
		t.Fatalf("got %d events, want %d: %v", len(events), len(wantKinds), events)
	}
	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Errorf("events[%d].Kind = %q, want %q", i, events[i].Kind, want)
		}
	}
}

func TestParseExternalStream_UnknownTypeSkipped(t *testing.T) {
	data := []byte(`{"type":"future_event","payload":1}
{"type":"done"}
`)
	events := ParseExternalStream(data, "run-future")
	if len(events) != 1 || events[0].Kind != EventKindEnd {
		t.Fatalf("expected only end event, got %v", events)
	}
}

func TestParseExternalStream_MalformedJSON_NonFatal(t *testing.T) {
	data := []byte(`not-json
{"type":"done"}
`)
	events := ParseExternalStream(data, "run-mal")
	var hasNonFatal bool
	for _, ev := range events {
		if ev.Kind == EventKindError && ev.Err != nil && !ev.Err.Fatal {
			hasNonFatal = true
		}
	}
	if !hasNonFatal {
		t.Fatal("expected a non-fatal error event for malformed JSON line")
	}
}
