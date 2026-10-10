// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FR-024 (session-core): Stop discards the person's web/channel input that was
// admitted but not yet committed into the agent's model input. The archived
// message stays; it is labelled "discarded / stopped_before_delivery" in a
// read-only sidecar so REST and replay can show it, and the caller of the Stop
// learns which messages were discarded so a live client can be told at once.
package agent

import (
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// DiscardedInput names one undelivered person's message a Stop discarded.
type DiscardedInput struct {
	// SessionID is the session whose queue held the message.
	SessionID string
	// MessageID is the original archive entry id of the message.
	MessageID string
}

// discardUndeliveredHumanInput removes sessionID's queued human input (items
// without a delegate steer receipt that are not upward wakes) and labels each
// archived original discarded. Input already committed into a running turn is
// not in the queue and is untouched. A failure to label one message is returned
// (joined) - the message is still discarded, never left to be consumed.
func (al *AgentLoop) discardUndeliveredHumanInput(sessionID string) ([]DiscardedInput, error) {
	if al == nil || al.steering == nil {
		return nil, nil
	}
	scopes := []string{sessionID}
	if ts := al.getActiveTurnState(sessionID); ts != nil && ts.sessionKey != "" && ts.sessionKey != sessionID {
		scopes = append(scopes, ts.sessionKey)
	}
	var out []DiscardedInput
	var errs []error
	for _, scope := range scopes {
		for _, item := range al.steering.takeHumanItemsScope(scope) {
			if item.transcriptEntryID == "" {
				continue // never archived (no entry to label)
			}
			store := al.ResolveSessionStore(sessionID)
			if store == nil {
				errs = append(errs, fmt.Errorf("label discarded input %q: no session store owns %q", item.transcriptEntryID, sessionID))
				continue
			}
			if err := store.RecordInputDiscarded(sessionID, item.transcriptEntryID, ""); err != nil {
				errs = append(errs, fmt.Errorf("label discarded input %q: %w", item.transcriptEntryID, err))
				continue
			}
			out = append(out, DiscardedInput{SessionID: sessionID, MessageID: item.transcriptEntryID})
		}
	}
	err := errors.Join(errs...)
	if err != nil {
		logger.ErrorCF("agent", "stop: discarded input could not be labelled",
			map[string]any{"session_id": sessionID, "error": err.Error()})
	}
	return out, err
}
