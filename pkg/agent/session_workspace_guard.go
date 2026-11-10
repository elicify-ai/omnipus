package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// validateInboundWorkspace refuses a foreign scope instead of silently running
// the message in the session's old workspace. No metadata is changed here.
func (al *AgentLoop) validateInboundWorkspace(msg bus.InboundMessage) error {
	if msg.SessionID == "" || msg.Channel == "system" {
		return nil
	}
	requested := inboundMetadata(msg, "workspace_id")
	if requested == "" && msg.Channel != "webchat" {
		if cfg := al.GetConfig(); cfg != nil {
			if instance, ok := cfg.Channels[inboundInstanceID(msg)]; ok {
				requested = instance.WorkspaceID
			}
		}
	}
	if requested == "" {
		return nil
	}
	store := al.GetSessionStore()
	if store == nil {
		return fmt.Errorf("session store unavailable while checking workspace")
	}
	meta, err := store.GetMeta(msg.SessionID)
	if err != nil {
		return fmt.Errorf("check conversation workspace: %w", err)
	}
	if scopeErr := session.ValidateWorkspaceReuse(meta, requested); scopeErr != nil {
		return fmt.Errorf("%w: %w", scopeErr, &curatedTurnError{text: session.ErrWorkspaceImmutable.Error()})
	}
	return nil
}
