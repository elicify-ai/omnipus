// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// persistAsyncToolFeedback records the async tool feedback that was just
// published to the chat (for example a background bash completion) as a
// system entry of the originating transcript, so the chat after a reload
// shows what was shown live (#1960 S-72, D-9). content is the same text that
// was published: the child's output was masked at its source (programs.Attach)
// and the sensitive-data filter ran on the published copy, so nothing else is
// stored. A failed result is stored with status "error", like the suppressed-
// feedback error notice (persistAsyncToolErrorNotice, S-73). It is written
// even when the live publish failed, so the result is not lost. A write
// failure is counted and logged.
func persistAsyncToolFeedback(ts *turnState, toolCallID, content string, isError bool) {
	if ts == nil || ts.transcriptStore == nil || ts.transcriptSessionID == "" || content == "" {
		return
	}
	now := time.Now().UTC()
	status := ""
	if isError {
		status = "error"
	}
	entry := session.TranscriptEntry{
		ID:        fmt.Sprintf("async-tool-result-%s-%d", toolCallID, now.UnixNano()),
		Type:      session.EntryTypeSystem,
		Role:      "system",
		AgentID:   ts.resolveActiveAgentID(),
		Content:   programs.MaskTokens(content), // B3 (FR-020)
		Timestamp: now,
		Status:    status,
		TurnID:    ts.turnID,
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
		logger.WarnCF("agent", "could not record async tool feedback to transcript",
			map[string]any{
				"session_id":   ts.transcriptSessionID,
				"tool_call_id": toolCallID,
				"error":        err.Error(),
			})
	}
}
