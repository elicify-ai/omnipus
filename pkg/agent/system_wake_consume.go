// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// consumeOrdinarySystemWake preserves the existing inbox-backed wake boundary.
// Call only after admission: a refused wake must leave its input pending.
// Secret-only direct-bus wakes instead use ClaimResume and never call this.
func (al *AgentLoop) consumeOrdinarySystemWake(store *session.UnifiedStore, sessionID, messageID, agentID string) error {
	if err := store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID: "consumed-" + messageID, Type: session.EntryTypeSystem, Role: "system",
		Content: "consumed " + messageID, AgentID: agentID, Timestamp: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("steer: wake: append consumed marker: %w", err)
	}
	if inbox := al.GetMessageInboxStore(); inbox != nil {
		if err := inbox.Ack(sessionID, []string{messageID}); err != nil {
			return fmt.Errorf("steer: wake: acknowledge %q: %w", messageID, err)
		}
	}
	return nil
}
