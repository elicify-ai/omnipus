// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// attention_secret_cards.go: pending secret cards as an attention source
// (omnipus-system-agent FR-091, BDD-10.2). A pending card lights the main of
// the chat that raised it: the chat itself when it is a main, otherwise the
// first main reached through ParentSessionID, otherwise — the chain's root is
// a non-main chat — the root agent's main in the root's workspace. No main
// there means no cue (the card still shows in its own chat). A source or a
// main that cannot be read makes the answer unknown, never a guessed false.

package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// withPendingSecretCards adds the mains lit by pending secret cards to the
// approval-derived set the session lists stamp needs_attention from.
func (a *restAPI) withPendingSecretCards(in pendingApprovalMains) pendingApprovalMains {
	if in.mains == nil {
		in.mains = map[string]struct{}{}
	}
	reg := a.agentLoop.SecretRequests()
	if reg == nil {
		slog.Warn("rest: attention: the secret request registry is not wired; needs_attention is left unset on mains")
		attentionReadFailures.Add(1)
		in.unknown = true
		return in
	}
	ids, err := reg.PendingSessionIDs()
	if err != nil {
		slog.Warn("rest: attention: pending secret cards could not be read", "error", err)
		attentionReadFailures.Add(1)
		in.unknown = true
		return in
	}
	getMeta := func(id string) (*session.UnifiedMeta, error) {
		store := a.resolveSessionStore(id)
		if store == nil {
			return nil, fmt.Errorf("session %q: no store: %w", id, os.ErrNotExist)
		}
		return store.GetMeta(id)
	}
	for _, id := range ids {
		mainID, unknown := secretCardMain(id, getMeta)
		if unknown {
			attentionReadFailures.Add(1)
			in.unknown = true
		}
		if mainID != "" {
			in.mains[mainID] = struct{}{}
		}
	}
	return in
}

// secretCardMain resolves the main a pending card in sessionID lights. It
// returns "" with unknown=false when there is honestly no main to light.
func secretCardMain(sessionID string, getMeta func(string) (*session.UnifiedMeta, error)) (string, bool) {
	id := sessionID
	for depth := 0; depth < maxAttentionParentWalk; depth++ {
		meta, err := getMeta(id)
		if err != nil || meta == nil {
			slog.Warn("rest: attention: could not read a session while assigning a pending secret card to its main",
				"session_id", id, "error", err)
			return "", true
		}
		if meta.Type == session.SessionTypeMain {
			return meta.ID, false
		}
		if meta.ParentSessionID == "" {
			return rootAgentMain(meta, getMeta)
		}
		id = meta.ParentSessionID
	}
	slog.Warn("rest: attention: a pending secret card's parent chain is deeper than the walk bound", "session_id", sessionID)
	return "", true
}

// rootAgentMain returns the main of a non-main root's agent in the root's
// workspace, "" when that agent has no main there.
func rootAgentMain(root *session.UnifiedMeta, getMeta func(string) (*session.UnifiedMeta, error)) (string, bool) {
	if root.WorkspaceID == "" || root.AgentID == "" {
		return "", false
	}
	mainID, err := session.MainSessionID(root.WorkspaceID, root.AgentID)
	if err != nil {
		return "", false
	}
	meta, err := getMeta(mainID)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil || meta == nil {
		slog.Warn("rest: attention: could not read the root agent's main while assigning a pending secret card",
			"session_id", mainID, "error", err)
		return "", true
	}
	if meta.Type != session.SessionTypeMain {
		return "", false
	}
	return meta.ID, false
}
