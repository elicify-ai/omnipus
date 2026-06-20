// Package memrooms implements the two-room memory topology for Omnipus.
//
// FR-7.1: Two rooms —
//   - Private per-agent room:  agents/<id>/.omnipus/   (agent-global, workspace-independent)
//   - Shared workspace room:   workspaces/<id>/.omnipus/ (keyed to the Spec-1 Workspace)
//
// Directory layout (under $OMNIPUS_HOME):
//
//	workspaces/<workspace_id>/.omnipus/
//	    memories/          ← per-memory .md files (FR-7.2 frontmatter)
//	    counters.jsonl     ← frozen access/cited log (FR-7.5)
//	    (no last-session.md — D19: continuity is the agent's, not the workspace's)
//
//	agents/<agent_id>/.omnipus/
//	    memories/
//	    counters.jsonl
//	    last-session.md    ← last session summary (private room only, D19)
//
// The ".index/" subtree (bleve, edges.jsonl, tags.json, minhash.jsonl) is a
// DERIVED artifact — it is NOT included here and is rebuilt from the .md
// sources by the search engine (separate unit per the brief).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package memrooms

import (
	"os"
	"path/filepath"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

const (
	// OmnipusRoomDir is the hidden directory name used for both rooms.
	OmnipusRoomDir = ".omnipus"

	// MemoriesSubdir is the sub-directory within a room that holds per-memory .md files.
	MemoriesSubdir = "memories"

	// CountersFile is the append-only frozen log of memory access/cited events (FR-7.5).
	CountersFile = "counters.jsonl"

	// LastSessionFile holds a summary of the most recent session.
	LastSessionFile = "last-session.md"

	// WorkspacesDirName is the top-level directory under $OMNIPUS_HOME for workspace rooms.
	WorkspacesDirName = "workspaces"
)

// Room holds the resolved filesystem paths for a single memory room.
type Room struct {
	// Root is the .omnipus/ directory itself.
	Root string
	// MemoriesDir is Root/memories/ — where per-memory .md files are stored.
	MemoriesDir string
	// CountersPath is Root/counters.jsonl.
	CountersPath string
}

// Rooms pairs the two rooms for one agent turn.
// Only Private is guaranteed non-empty; Shared may be empty when no workspace
// is associated with the session (e.g., a direct REST API call with no workspace_id).
type Rooms struct {
	Private Room
	Shared  *Room // nil when no workspace_id is present
}

// DefaultRoomScope returns the room scope appropriate for this Rooms:
// "shared" when a shared room is available, "private" otherwise.
// Used by tools that want to default to the shared room in a workspace session.
func (r Rooms) DefaultRoomScope() RoomScope {
	if r.Shared != nil {
		return RoomScopeShared
	}
	return RoomScopePrivate
}

// RoomScope selects which room a memory operation targets.
type RoomScope string

const (
	// RoomScopePrivate targets the per-agent private room.
	RoomScopePrivate RoomScope = "private"
	// RoomScopeShared targets the per-workspace shared room.
	// Falls back to private when no workspace room is available.
	RoomScopeShared RoomScope = "shared"
	// RoomScopeBoth reads from both rooms (recall only).
	RoomScopeBoth RoomScope = "both"
)

// ParseRoomScope parses a string into a RoomScope.
// Accepts "private", "shared", "both"; returns an error on anything else.
func ParseRoomScope(s string) (RoomScope, error) {
	switch RoomScope(s) {
	case RoomScopePrivate, RoomScopeShared, RoomScopeBoth:
		return RoomScope(s), nil
	case "":
		return RoomScopePrivate, nil
	}
	return "", &InvalidRoomScopeError{s}
}

// InvalidRoomScopeError is returned when an unrecognised room scope string is supplied.
type InvalidRoomScopeError struct {
	Got string
}

func (e *InvalidRoomScopeError) Error() string {
	return "invalid room scope " + e.Got + "; expected one of: private, shared, both"
}

// ResolveAgentPrivateRoom returns the Room for an agent's private room given the
// agent's workspace directory (i.e., $OMNIPUS_HOME/agents/<id>/).
//
// Path: <agentWorkspace>/.omnipus/
func ResolveAgentPrivateRoom(agentWorkspace string) Room {
	return buildRoom(filepath.Join(agentWorkspace, OmnipusRoomDir))
}

// ResolveWorkspaceSharedRoom returns the Room for the per-workspace shared room.
//
// Path: $OMNIPUS_HOME/workspaces/<workspaceID>/.omnipus/
//
// workspaceID must be a validated entity ID (no path components, no "..").
// omnipusHome is the resolved $OMNIPUS_HOME directory.
func ResolveWorkspaceSharedRoom(omnipusHome, workspaceID string) Room {
	wsDir := filepath.Join(omnipusHome, WorkspacesDirName, workspaceID)
	return buildRoom(filepath.Join(wsDir, OmnipusRoomDir))
}

// buildRoom constructs a Room from the given root path.
func buildRoom(root string) Room {
	return Room{
		Root:         root,
		MemoriesDir:  filepath.Join(root, MemoriesSubdir),
		CountersPath: filepath.Join(root, CountersFile),
	}
}

// EnsureRoom creates the room directories if they do not exist.
// Returns the Room so callers can chain: EnsureRoom(ResolveAgentPrivateRoom(...)).
func EnsureRoom(room Room) (Room, error) {
	if err := os.MkdirAll(room.MemoriesDir, 0o700); err != nil {
		return room, err
	}
	return room, nil
}

// MustEnsureRoom creates the room directories and logs WARN on failure (non-fatal).
// The agent can still operate; writes will fail with clear errors.
func MustEnsureRoom(room Room, tag string) Room {
	if _, err := EnsureRoom(room); err != nil {
		logger.WarnCF("memrooms", "Failed to create room directory",
			map[string]any{"room": room.Root, "tag": tag, "error": err.Error()})
	}
	return room
}
