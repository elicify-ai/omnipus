// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package agent provides the MemoryStoreAdapter that bridges the agent-side
// MemoryStore to the tools.MemoryAccess / tools.RoomMemoryAccess interfaces (FR-7.3).
//
// Room routing (MIN-002 / FR-7.3): the adapter reads workspace_id from the
// tool execution context. Default scope:
//   - Workspace session (workspace_id set):  shared
//   - Direct/private session (no workspace): private
//
// String-based room scope ("private" | "shared" | "both") is used at the interface
// boundary to prevent a pkg/tools → pkg/memrooms import that would create a cycle.
package agent

import (
	"github.com/dapicom-ai/omnipus/pkg/memrooms"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// MemoryStoreAdapter wraps *MemoryStore to implement tools.MemoryAccess +
// tools.RoomMemoryWriter + tools.RoomMemorySearcher.
// The adapter converts between the agent-side types (LongTermEntry, Retro)
// and the tools-side mirror types (tools.MemoryEntry, tools.MemoryRetro),
// keeping pkg/agent and pkg/tools import-cycle-free.
type MemoryStoreAdapter struct {
	ms *MemoryStore
}

// NewMemoryStoreAdapter wraps ms in a tools.MemoryAccess implementation.
func NewMemoryStoreAdapter(ms *MemoryStore) *MemoryStoreAdapter {
	return &MemoryStoreAdapter{ms: ms}
}

// --- tools.MemoryWriter implementation --------------------------------------

// AppendLongTerm implements tools.MemoryWriter.
// Delegates to the default scope (shared when workspace session, private otherwise).
func (a *MemoryStoreAdapter) AppendLongTerm(content, category string) error {
	return a.ms.AppendLongTerm(content, category)
}

// AppendRetro converts tools.MemoryRetro to agent.Retro and delegates.
func (a *MemoryStoreAdapter) AppendRetro(sessionID string, r tools.MemoryRetro) error {
	return a.ms.AppendRetro(sessionID, Retro{
		Timestamp:        r.Timestamp,
		Trigger:          RecapTrigger(r.Trigger),
		Fallback:         r.Fallback,
		FallbackReason:   r.FallbackReason,
		Recap:            r.Recap,
		WentWell:         r.WentWell,
		NeedsImprovement: r.NeedsImprovement,
	})
}

// --- tools.MemorySearcher implementation ------------------------------------

// SearchEntries implements tools.MemorySearcher.
// Searches both rooms when a shared room is active.
func (a *MemoryStoreAdapter) SearchEntries(query string, limit int) ([]tools.MemoryEntry, error) {
	return a.SearchEntriesInRoom(query, limit, "both")
}

// --- tools.RoomMemoryWriter implementation ----------------------------------

// AppendLongTermToRoom implements tools.RoomMemoryWriter.
// scope is "private" | "shared" | "both" (both→ private for writes).
func (a *MemoryStoreAdapter) AppendLongTermToRoom(content, category, scope string) error {
	rs := stringToRoomScope(scope)
	// "both" is not valid for writes; treat as the default scope.
	if rs == memrooms.RoomScopeBoth {
		rs = a.ms.rooms().DefaultRoomScope()
	}
	return a.ms.AppendLongTermToScope(content, category, rs)
}

// SetWorkspaceID implements tools.RoomMemoryWriter.
// Wires the shared room for the current turn; called before each tool invocation.
func (a *MemoryStoreAdapter) SetWorkspaceID(workspaceID string) {
	a.ms.SetWorkspaceID(workspaceID)
}

// --- tools.RoomMemorySearcher implementation --------------------------------

// SearchEntriesInRoom implements tools.RoomMemorySearcher.
// scope is "private" | "shared" | "both".
func (a *MemoryStoreAdapter) SearchEntriesInRoom(query string, limit int, scope string) ([]tools.MemoryEntry, error) {
	agentEntries, err := a.ms.SearchEntriesInScope(query, limit, stringToRoomScope(scope))
	if err != nil {
		return nil, err
	}
	result := make([]tools.MemoryEntry, len(agentEntries))
	for i, e := range agentEntries {
		result[i] = tools.MemoryEntry{
			Timestamp: e.Timestamp,
			Category:  string(e.Category),
			Content:   e.Content,
		}
	}
	return result, nil
}

// stringToRoomScope converts a string scope to a memrooms.RoomScope.
// Unknown values default to RoomScopePrivate.
func stringToRoomScope(s string) memrooms.RoomScope {
	switch s {
	case "shared":
		return memrooms.RoomScopeShared
	case "both":
		return memrooms.RoomScopeBoth
	default:
		return memrooms.RoomScopePrivate
	}
}
