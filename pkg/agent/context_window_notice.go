package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// recordContextWindowNotice persists the classified diagnostic before returning
// its live frame. It does not add the diagnostic to the model's history.
func (ts *turnState) recordContextWindowNotice(notice generated.ContextWindowNotice) (*generated.ContextWindowNoticeFrame, error) {
	// Transcript-less turns have no durable session to address. Recording is
	// intentionally disabled there, as it is for other turn transcript writers.
	if ts.transcriptStore == nil && ts.transcriptSessionID == "" {
		return nil, nil
	}
	if ts.abandoned.Load() {
		return nil, context.Canceled
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return nil, fmt.Errorf("context_window_notice: transcript store and session ID are both required")
	}

	entry := session.TranscriptEntry{
		ID:                  uuid.NewString(),
		Type:                session.EntryTypeContextWindowNotice,
		Role:                "system",
		Timestamp:           time.Now().UTC(),
		AgentID:             ts.resolveActiveAgentID(),
		TurnID:              ts.turnID,
		ContextWindowNotice: &notice,
	}
	frame, err := entry.ContextWindowNoticeFrame(ts.transcriptSessionID)
	if err != nil {
		return nil, err
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		return nil, fmt.Errorf("context_window_notice: persist diagnostic: %w", err)
	}
	return &frame, nil
}

// transcriptModelHistoryRole keeps classified UI diagnostics out of hydration,
// even when a stored entry also has an ordinary role or Content.
func transcriptModelHistoryRole(entry *session.TranscriptEntry) string {
	if entry.Type == session.EntryTypeContextWindowNotice {
		return ""
	}
	return entry.Role
}
