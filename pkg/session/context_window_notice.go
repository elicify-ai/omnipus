package session

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ContextWindowNoticeFrame preserves the durable diagnostic's identity on both
// live delivery and replay. Invalid diagnostics never become ordinary messages.
func (entry TranscriptEntry) ContextWindowNoticeFrame(sessionID string) (generated.ContextWindowNoticeFrame, error) {
	if entry.Type != EntryTypeContextWindowNotice {
		return generated.ContextWindowNoticeFrame{}, fmt.Errorf("context_window_notice: unexpected entry type %q", entry.Type)
	}
	if entry.ContextWindowNotice == nil {
		return generated.ContextWindowNoticeFrame{}, fmt.Errorf("context_window_notice: entry %q requires a classified payload", entry.ID)
	}
	notice := entry.ContextWindowNotice
	if !notice.Kind.Valid() {
		return generated.ContextWindowNoticeFrame{}, fmt.Errorf("context_window_notice: entry %q has invalid kind %q", entry.ID, notice.Kind)
	}
	if n := utf8.RuneCountInString(notice.Message); n < 1 || n > 2048 {
		return generated.ContextWindowNoticeFrame{}, fmt.Errorf("context_window_notice: entry %q message must contain 1 to 2048 characters", entry.ID)
	}
	for _, field := range []struct{ name, value string }{
		{"session_id", sessionID},
		{"entry_id", entry.ID},
		{"turn_id", entry.TurnID},
		{"agent_id", entry.AgentID},
	} {
		if n := utf8.RuneCountInString(field.value); n < 1 || n > 128 {
			return generated.ContextWindowNoticeFrame{}, fmt.Errorf("context_window_notice: entry %q %s must contain 1 to 128 characters", entry.ID, field.name)
		}
	}
	if entry.Timestamp.IsZero() {
		return generated.ContextWindowNoticeFrame{}, fmt.Errorf("context_window_notice: entry %q requires its original timestamp", entry.ID)
	}

	return generated.ContextWindowNoticeFrame{
		Type:      string(generated.WsFrameTypeContextWindowNotice),
		SessionId: sessionID,
		EntryId:   entry.ID,
		TurnId:    entry.TurnID,
		AgentId:   entry.AgentID,
		Timestamp: entry.Timestamp.Format(time.RFC3339Nano),
		Notice: generated.ContextWindowNoticeFrameNotice{
			Kind:    string(notice.Kind),
			Message: notice.Message,
		},
	}, nil
}

func validateContextWindowNotice(sessionID string, entry TranscriptEntry) error {
	if entry.Type != EntryTypeContextWindowNotice {
		return nil
	}
	_, err := entry.ContextWindowNoticeFrame(sessionID)
	return err
}

// isContextWindowNoticeLine reads only the discriminant when decoding the full
// entry failed, so invalid typed payloads are returned as errors, not skipped.
func isContextWindowNoticeLine(line []byte) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(line, &fields); err != nil {
		return false
	}
	var kind EntryType
	return json.Unmarshal(fields["type"], &kind) == nil && kind == EntryTypeContextWindowNotice
}
