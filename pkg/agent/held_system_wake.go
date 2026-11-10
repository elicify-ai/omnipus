package agent

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// logStoppedSystemWake distinguishes a durable pending report from a synthetic
// wake whose body has no inbox entry. It neither consumes nor acknowledges input.
func (al *AgentLoop) logStoppedSystemWake(msg bus.InboundMessage) error {
	sessionID := msg.AsyncTranscriptSessionID
	messageID := inboundMetadata(msg, "steer_message_id")
	kind := strings.TrimSpace(msg.Sender.CanonicalID)
	if kind == "" {
		kind = "system"
	}
	fields := map[string]any{"session_id": sessionID, "message_id": messageID, "kind": kind}
	if inbox := al.GetMessageInboxStore(); inbox != nil {
		entries, err := inbox.Entries(sessionID)
		if err != nil {
			return fmt.Errorf("steer: held wake: inspect inbox for session %q message %q: %w", sessionID, messageID, err)
		}
		for _, entry := range entries {
			if entry.Kind == session.InboxEntryMessage && entry.Message != nil && messageIDOf(*entry.Message) == messageID {
				logger.InfoCF("agent", "steer: wake held; retained pending", fields)
				return nil
			}
		}
	}
	logger.WarnCF("agent", "steer: wake held; content is not retained", fields)
	return nil
}
