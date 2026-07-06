// external_event.go — generic JSON Lines event parser for external CLI runs.
//
// Defines a unified, CLI-agnostic external-event wire format used when Omnipus
// drives an external agent process that emits line-delimited JSON events. This
// parser is intended to be consumed by:
//
//   - test stubs that mock an external CLI (driver_external_test.go);
//   - future A2A/ACP bridges that want to speak one canonical event shape;
//   - per-CLI drivers that translate their native stream into this shape.
//
// Canonical event types (one JSON object per line):
//
//	{"type":"message","message":{"role":"assistant","text":"..."}}
//	{"type":"text","text":{"text":"..."}}
//	{"type":"tool_use","tool_use":{"id":"...","name":"...","input":{...}}}
//	{"type":"tool_call","tool_call":{"id":"...","name":"...","input":{...}}}
//	{"type":"tool_result","tool_result":{"id":"...","name":"...","output":...,"is_error":false}}
//	{"type":"permission_request","permission_request":{"id":"...","tool_name":"...","description":"...","input":{...}}}
//	{"type":"error","error":{"message":"...","code":"..."}}
//	{"type":"done"}
//
// Mapping to Omnipus runner events:
//
//	message/text          -> EventKindOutput
//	tool_use / tool_call  -> EventKindToolCall  (pending tool call)
//	tool_result           -> EventKindToolResult (tool result completion)
//	permission_request    -> EventKindPermissionRequest (routed to consent layer)
//	error                 -> EventKindError
//	done                  -> EventKindEnd
//
// TODO(#488): permission_request is routed post-hoc — a DENY only cancels the
// whole run via driver.Cancel(), never a per-call veto (see consent.go's
// POST-HOC CONSENT LIMITATION box). A bidirectional stdin control channel for
// mid-call allow/deny wouldn't fix the real gap: claude-code and opencode
// workers currently run with no enforced sandbox of their own (only codex
// still has one). The actual fix is giving Omnipus its own confiner for
// external-CLI children — see issue #488 for that evaluation.

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// ExternalEvent is a single line-delimited JSON event emitted by an external
// agent process. Exactly one of the pointer fields is populated for a valid
// event; the Type discriminator tells you which.
type ExternalEvent struct {
	Type string `json:"type"`

	Message *struct {
		Role string `json:"role"`
		Text string `json:"text"`
	} `json:"message,omitempty"`

	Text *struct {
		Text string `json:"text"`
	} `json:"text,omitempty"`

	ToolUse *struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"tool_use,omitempty"`

	ToolCall *struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"tool_call,omitempty"`

	ToolResult *struct {
		ID      string          `json:"id"`
		Name    string          `json:"name,omitempty"`
		Output  json.RawMessage `json:"output,omitempty"`
		IsError bool            `json:"is_error,omitempty"`
	} `json:"tool_result,omitempty"`

	PermissionRequest *struct {
		ID          string          `json:"id"`
		ToolName    string          `json:"tool_name"`
		Description string          `json:"description"`
		Input       json.RawMessage `json:"input,omitempty"`
	} `json:"permission_request,omitempty"`

	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code,omitempty"`
	} `json:"error,omitempty"`

	Done *struct{} `json:"done,omitempty"`
}

// ParseExternalEvent unmarshals one JSON line into an ExternalEvent. It returns
// an error only for syntactically invalid JSON; unknown type strings are
// preserved in ExternalEvent.Type and can be handled by callers.
func ParseExternalEvent(raw []byte) (ExternalEvent, error) {
	var ev ExternalEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return ExternalEvent{}, fmt.Errorf("unmarshal external event: %w", err)
	}
	return ev, nil
}

// ExternalEventToRunEvent maps a parsed ExternalEvent to the canonical RunEvent
// used inside Omnipus. It returns (event, true) when the external event should
// be emitted; (zero, false) when the event is not recognized or has no payload.
func ExternalEventToRunEvent(ev ExternalEvent, runID string) (RunEvent, bool) {
	now := time.Now().UTC()
	switch ev.Type {
	case "message":
		if ev.Message == nil || ev.Message.Text == "" {
			return RunEvent{}, false
		}
		return RunEvent{
			Kind:      EventKindOutput,
			RunID:     runID,
			Timestamp: now,
			Output:    &OutputEvent{Text: ev.Message.Text},
		}, true

	case "text":
		if ev.Text == nil || ev.Text.Text == "" {
			return RunEvent{}, false
		}
		return RunEvent{
			Kind:      EventKindOutput,
			RunID:     runID,
			Timestamp: now,
			Output:    &OutputEvent{Text: ev.Text.Text},
		}, true

	case "tool_use":
		if ev.ToolUse == nil {
			return RunEvent{}, false
		}
		return RunEvent{
			Kind:      EventKindToolCall,
			RunID:     runID,
			Timestamp: now,
			ToolCall: &ToolCallEvent{
				CallID:    ev.ToolUse.ID,
				ToolName:  ev.ToolUse.Name,
				ToolInput: safeCopyRaw(ev.ToolUse.Input),
			},
		}, true

	case "tool_call":
		if ev.ToolCall == nil {
			return RunEvent{}, false
		}
		return RunEvent{
			Kind:      EventKindToolCall,
			RunID:     runID,
			Timestamp: now,
			ToolCall: &ToolCallEvent{
				CallID:    ev.ToolCall.ID,
				ToolName:  ev.ToolCall.Name,
				ToolInput: safeCopyRaw(ev.ToolCall.Input),
			},
		}, true

	case "tool_result":
		if ev.ToolResult == nil {
			return RunEvent{}, false
		}
		return RunEvent{
			Kind:      EventKindToolResult,
			RunID:     runID,
			Timestamp: now,
			ToolResult: &ToolResultEvent{
				CallID:   ev.ToolResult.ID,
				ToolName: ev.ToolResult.Name,
				Output:   safeCopyRaw(ev.ToolResult.Output),
				IsError:  ev.ToolResult.IsError,
			},
		}, true

	case "permission_request":
		if ev.PermissionRequest == nil {
			return RunEvent{}, false
		}
		return RunEvent{
			Kind:      EventKindPermissionRequest,
			RunID:     runID,
			Timestamp: now,
			PermissionRequest: &PermissionRequestEvent{
				RequestID:   ev.PermissionRequest.ID,
				ToolName:    ev.PermissionRequest.ToolName,
				Description: ev.PermissionRequest.Description,
				RawInput:    safeCopyRaw(ev.PermissionRequest.Input),
			},
		}, true

	case "error":
		if ev.Error == nil || ev.Error.Message == "" {
			return RunEvent{}, false
		}
		return RunEvent{
			Kind:      EventKindError,
			RunID:     runID,
			Timestamp: now,
			Err:       &ErrorEvent{Message: ev.Error.Message, Fatal: true},
		}, true

	case "done":
		return RunEvent{Kind: EventKindEnd, RunID: runID, Timestamp: now}, true

	default:
		slog.Debug("external event: unknown type, skipping", "type", ev.Type, "runID", runID)
		return RunEvent{}, false
	}
}

// safeCopyRaw returns a defensive copy of a json.RawMessage, or nil when empty.
func safeCopyRaw(r json.RawMessage) []byte {
	if len(r) == 0 {
		return nil
	}
	out := make([]byte, len(r))
	copy(out, r)
	return out
}

// parseExternalLine is the streamParser callback for the canonical external
// event protocol. It turns one NDJSON line into a RunEvent, emitting a non-fatal
// ErrorEvent for malformed JSON while allowing unknown types to be skipped
// gracefully.
func parseExternalLine(raw []byte, runID string) (RunEvent, bool) {
	ev, err := ParseExternalEvent(raw)
	if err != nil {
		return RunEvent{
			Kind:      EventKindError,
			RunID:     runID,
			Timestamp: time.Now().UTC(),
			Err:       &ErrorEvent{Message: fmt.Sprintf("malformed JSON: %v", err), Fatal: false},
		}, true
	}
	return ExternalEventToRunEvent(ev, runID)
}

// ParseExternalStream parses a complete NDJSON byte slice in the canonical
// external-event format and returns the emitted RunEvents. It is the
// fixture-based test entry point for external_event.go.
func ParseExternalStream(data []byte, runID string) []RunEvent {
	var events []RunEvent
	ctx := context.Background()
	out := make(chan RunEvent, 256)
	streamParser(ctx, bytes.NewReader(data), runID, func(raw []byte) (RunEvent, bool) {
		return parseExternalLine(raw, runID)
	}, out)
	close(out)
	for ev := range out {
		events = append(events, ev)
	}
	return events
}
