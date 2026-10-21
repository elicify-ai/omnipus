// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package agent provides the MemoryStoreAdapter that bridges the agent-side
// MemoryStore to the tools.MemoryAccess / tools.RoomMemoryAccess interfaces (FR-7.3).
//
// Room routing (MIN-002 / FR-7.3): every room-aware operation receives the
// current tool call's workspace_id as a value and constructs a MemoryScope.
// No current-room state is stored on the adapter or MemoryStore.
//
// String-based room scope ("private" | "shared" | "both") is used at the interface
// boundary to prevent a pkg/tools → pkg/memrooms import that would create a cycle.

package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/memrooms"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
// With no turn workspace supplied, writes only to the agent's private room.
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
// With no turn workspace supplied, searches only the agent's private room.
func (a *MemoryStoreAdapter) SearchEntries(query string, limit int) ([]tools.MemoryEntry, error) {
	return a.SearchEntriesInRoom(query, limit, "private", "")
}

// --- tools.RoomMemoryWriter implementation ----------------------------------

// AppendLongTermToRoom implements tools.RoomMemoryWriter.
// scope is "private" | "shared" | "both" (both→ private for writes).
func (a *MemoryStoreAdapter) AppendLongTermToRoom(content, category, scope, workspaceID string) error {
	identity := a.ms.scopeForWorkspace(workspaceID)
	rs := stringToRoomScope(scope)
	// "both" is not valid for writes; treat as the default scope.
	if rs == memrooms.RoomScopeBoth {
		rs = a.ms.rooms(identity).DefaultRoomScope()
	}
	return a.ms.AppendLongTermToScope(content, category, rs, identity)
}

// --- tools.RoomMemorySearcher implementation --------------------------------

// SearchEntriesInRoom implements tools.RoomMemorySearcher.
// scope is "private" | "shared" | "both".
func (a *MemoryStoreAdapter) SearchEntriesInRoom(query string, limit int, scope, workspaceID string) ([]tools.MemoryEntry, error) {
	agentEntries, err := a.ms.SearchEntriesInScope(query, limit, stringToRoomScope(scope), a.ms.scopeForWorkspace(workspaceID))
	result := make([]tools.MemoryEntry, len(agentEntries))
	for i, e := range agentEntries {
		result[i] = tools.MemoryEntry{
			Timestamp: e.Timestamp,
			Category:  string(e.Category),
			Content:   e.Content,
			ID:        e.ID,
			Title:     e.Title,
		}
	}
	return result, err
}

// --- tools.RetroSearcher implementation -------------------------------------

// SearchRetros implements tools.RetroSearcher. It returns retrospectives from the
// private room whose content (recap + went-well + needs-improvement) matches the
// query, ranked by BM25 relevance (same k1/b parameters bleve uses for long-term
// recall), capped at limit. This lets recall_memory span past reflections with
// consistent lexical ranking, not just long-term memories.
func (a *MemoryStoreAdapter) SearchRetros(query string, limit int) ([]tools.MemoryEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	retros, err := a.ms.ReadRetros(365)
	if err != nil {
		return nil, err
	}
	ranked := rankRetrosBM25(retros, query, limit)
	out := make([]tools.MemoryEntry, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, tools.MemoryEntry{
			Timestamp: r.Timestamp,
			Category:  "retrospective",
			Content:   retroSearchText(r),
			Title:     "Retrospective",
		})
	}
	return out, nil
}

// retroSearchText renders a Retro into a single searchable / human-readable
// block (recap, then went-well and needs-improvement bullets).
func retroSearchText(r Retro) string {
	var sb strings.Builder
	if r.Recap != "" {
		sb.WriteString(r.Recap)
	}
	if len(r.WentWell) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("Went well: ")
		sb.WriteString(strings.Join(r.WentWell, "; "))
	}
	if len(r.NeedsImprovement) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("Needs improvement: ")
		sb.WriteString(strings.Join(r.NeedsImprovement, "; "))
	}
	return strings.TrimSpace(sb.String())
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
