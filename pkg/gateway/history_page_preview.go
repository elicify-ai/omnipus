package gateway

import (
	"encoding/json"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// historyPreviewMarker maps the session reader's private display record to
// the generated HistoryResultPreview wire type (chat-history paging spec,
// "Amendment 2026-10-27 (founder, #1452 D2)", A2.1/A2.2).
func historyPreviewMarker(d session.HistoryResultDisplay) generated.HistoryResultPreview {
	return generated.HistoryResultPreview{HistoryPreview: true, OriginalSizeBytes: d.OriginalSizeBytes, Preview: d.Preview}
}

// historyPreviewMarkerObject is the same generated marker as the REST
// ToolCall.result object, whose generated field is an untyped object map.
func historyPreviewMarkerObject(d session.HistoryResultDisplay) (map[string]any, error) {
	raw, err := json.Marshal(historyPreviewMarker(d))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(raw, &out)
	return out, err
}

// historyPreviewBytesValid is the A2.2 byte rule the schema cannot express:
// an object carrying `_history_preview` needs a string preview whose UTF-8
// length is at most session.ChatHistoryResultPreviewBytes. Objects without
// that key are unaffected; the shape itself is checked by the schema.
func historyPreviewBytesValid(result any) bool {
	obj, ok := result.(map[string]any)
	if !ok {
		return true
	}
	if _, marker := obj["_history_preview"]; !marker {
		return true
	}
	preview, ok := obj["preview"].(string)
	return ok && len(preview) <= session.ChatHistoryResultPreviewBytes
}

// historyMessageMarkersValid applies the byte rule to every tool result of a
// projected Message.
func historyMessageMarkersValid(message generated.Message) bool {
	if message.ToolCalls == nil {
		return true
	}
	for _, call := range *message.ToolCalls {
		if call.Result != nil && !historyPreviewBytesValid(*call.Result) {
			return false
		}
	}
	return true
}

var errHistoryPreviewMarker = errors.New("invalid history preview marker")

// historyStoredMarkerInvalid reports a saved (not reader-made) tool result
// that carries the reserved `_history_preview` key but is not a valid
// HistoryResultPreview under both the generated schema and the byte rule.
// Such an item is malformed on every carrier (A2.2): it becomes the D4
// placeholder, never a sent marker and never a plain result.
func historyStoredMarkerInvalid(e session.TranscriptEntry) (bool, error) {
	for _, tc := range e.ToolCalls {
		if _, preview := tc.HistoryDisplayPreview(); preview {
			continue
		}
		if _, marker := tc.Result["_history_preview"]; !marker {
			continue
		}
		if !historyPreviewBytesValid(tc.Result) {
			return true, nil
		}
		raw, err := json.Marshal(tc.Result)
		if err != nil {
			return false, err
		}
		msg, serverErr := validateBodyAgainstSchema("HistoryResultPreview", raw)
		if serverErr {
			return false, errors.New("history preview schema unavailable")
		}
		if msg != "" {
			return true, nil
		}
	}
	return false, nil
}

// historySavedMarkerPresent reports a saved (not reader-made) tool result
// that carries the reserved `_history_preview` key. A reader preview replaces
// its result with nil, so any such key is saved data. An ordinary result must
// not carry it (A2.2): on every carrier, valid marker shape or not, the item
// becomes the D4 placeholder and the key is never sent.
func historySavedMarkerPresent(e session.TranscriptEntry) bool {
	for _, tc := range e.ToolCalls {
		if _, preview := tc.HistoryDisplayPreview(); preview {
			continue
		}
		if _, marker := tc.Result["_history_preview"]; marker {
			return true
		}
	}
	return false
}

// historyPlaceholderSource is the replay source for an item that degraded to
// the D4 placeholder: identity and the placeholder text only, no saved body.
func historyPlaceholderSource(e session.TranscriptEntry) session.TranscriptEntry {
	return session.TranscriptEntry{ID: e.ID, AgentID: e.AgentID, Timestamp: e.Timestamp, Role: "system", Content: historyPlaceholderText}
}

// historyFrameMarkerValid applies the byte rule to a prepared history-origin
// tool_call_result frame.
func historyFrameMarkerValid(raw []byte) error {
	var f generated.ToolCallResultFrame
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	if !historyPreviewBytesValid(f.Result) {
		return errHistoryPreviewMarker
	}
	return nil
}

// historyToolCallMedia is the "media" member the history projection consumes:
// from the full value when the page shows a preview, else from the result.
func historyToolCallMedia(tc session.ToolCall) (any, bool) {
	if d, ok := tc.HistoryDisplayPreview(); ok {
		return d.Media, d.HasMedia
	}
	raw, ok := tc.Result["media"]
	return raw, ok
}

// historyToolCallChildSessionID is the full value's top-level "session_id"
// string, from the preserved fact when the page shows a preview.
func historyToolCallChildSessionID(tc session.ToolCall) string {
	if d, ok := tc.HistoryDisplayPreview(); ok {
		return d.ChildSessionID
	}
	sid, _ := tc.Result["session_id"].(string)
	return sid
}
