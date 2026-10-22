// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import "fmt"

// InteractionContext is the server-derived owning chat of an interaction card.
// It is internal routing data, not a wire type.
type InteractionContext struct {
	WorkspaceID   string
	RootSessionID string
	RootAgentID   string
	Owner         string
	RootType      UnifiedSessionType
}

// ResolveInteractionContext is the bounded, cycle-guarded ancestry walk shared
// by secret cards and approvals. Unknown ancestry is an error, never authority.
func (us *UnifiedStore) ResolveInteractionContext(sessionID string) (InteractionContext, error) {
	out := InteractionContext{}
	seen := make(map[string]bool)
	for hop := 0; hop < 16 && sessionID != ""; hop++ {
		if seen[sessionID] {
			return out, fmt.Errorf("session ancestry contains a cycle")
		}
		seen[sessionID] = true
		meta, err := us.GetMeta(sessionID)
		if err != nil {
			return out, err
		}
		if out.WorkspaceID == "" {
			out.WorkspaceID = meta.WorkspaceID
		}
		if meta.ParentSessionID == "" {
			out.RootSessionID, out.RootAgentID, out.Owner, out.RootType = meta.ID, meta.AgentID, meta.Owner, meta.Type
			return out, nil
		}
		sessionID = meta.ParentSessionID
	}
	return out, fmt.Errorf("session ancestry could not be resolved")
}
