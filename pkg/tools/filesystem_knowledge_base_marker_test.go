// Omnipus — KB-2b (defect-list-knowledge-base-ux-2026-09-08.md,
// founder-ratified 2026-09-08): list_directory marks a knowledge base
// distinctly from an ordinary folder.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestListDirTool_MarksKnowledgeBaseWithKBPrefix is the primary case:
// a confined (root != nil) listing of the workspace root, where one entry
// is a knowledge base (an .omnipus-vault marker directory at its root), one
// is an ordinary folder, and one is a plain file. Only the knowledge base
// must render as "KB:" — the other two keep their existing "DIR:"/"FILE:"
// labels unchanged.
func TestListDirTool_MarksKnowledgeBaseWithKBPrefix(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "MyNotes", ".omnipus-vault"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "PlainFolder"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "todo.txt"), []byte("x"), 0o644))

	tool := NewListDirTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{"path": "."})
	require.False(t, result.IsError, result.ForLLM)

	assert.Contains(t, result.ForLLM, "KB:   MyNotes",
		"a folder marked with .omnipus-vault must render as KB:, not DIR:")
	assert.NotContains(t, result.ForLLM, "DIR:  MyNotes",
		"a knowledge base must not ALSO render as an ordinary DIR: entry")
	assert.Contains(t, result.ForLLM, "DIR:  PlainFolder",
		"an ordinary folder with no marker must still render as DIR:")
	assert.Contains(t, result.ForLLM, "FILE: todo.txt")
}

// TestListDirTool_ObsidianMarkerAloneIsAlsoAKnowledgeBase pins the OR rule
// (knowledge.Detection.IsKnowledgeBase: HasOmnipusMarker || HasObsidianMarker)
// — reused here, not re-derived, so a knowledge base opened first in
// Obsidian (before Omnipus ever wrote its own marker) is still recognised.
func TestListDirTool_ObsidianMarkerAloneIsAlsoAKnowledgeBase(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "ObsidianVault", ".obsidian"), 0o700))

	tool := NewListDirTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{"path": "."})
	require.False(t, result.IsError, result.ForLLM)

	assert.Contains(t, result.ForLLM, "KB:   ObsidianVault")
}

// TestListDirTool_MarkerFileIsNotAMarkerDirectory proves the check is
// FR-020's "a real DIRECTORY" rule, not a bare name match: a REGULAR FILE
// named ".omnipus-vault" must not be mistaken for the marker.
func TestListDirTool_MarkerFileIsNotAMarkerDirectory(t *testing.T) {
	ws := t.TempDir()
	folder := filepath.Join(ws, "NotAVault")
	require.NoError(t, os.MkdirAll(folder, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(folder, ".omnipus-vault"), []byte("not a dir"), 0o644))

	tool := NewListDirTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{"path": "."})
	require.False(t, result.IsError, result.ForLLM)

	assert.Contains(t, result.ForLLM, "DIR:  NotAVault")
	assert.NotContains(t, result.ForLLM, "KB:   NotAVault")
}

// TestListDirTool_MarksKnowledgeBaseReachedThroughAMount is KB-2b's explicit
// requirement: "a knowledge base reached through a mount is detected today
// and must stay detected." Listing INTO a workspace mount (a path outside
// the workspace's own work dir but inside an allowed mount root) resolves a
// HOST-MODE PathHandle (resolvepath.go's read branch for a path outside
// policy.WorkDir), exercising hasSubdir's other branch from the confined-mode
// tests above.
func TestListDirTool_MarksKnowledgeBaseReachedThroughAMount(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	const wsID, agentID = "kb2b-ws", "kb2b-agent"
	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: wsID, Name: "test", Status: "active", CreatedAt: now, UpdatedAt: now,
		CoreTeam: []string{agentID},
	}))
	workDir, err := workspace.EnsureWorkDir(home, wsID)
	require.NoError(t, err)

	mountTarget := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(mountTarget, "MountedVault", ".omnipus-vault"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(mountTarget, "MountedPlain"), 0o755))
	_, warn, err := workspace.CreateMount(home, wsID, "extra", mountTarget)
	require.NoError(t, err)
	require.Empty(t, warn)

	tool := NewListDirTool(workDir, true)
	ctx := WithTurnWorkspaceDir(WithAgentID(context.Background(), agentID), workDir)
	result := tool.Execute(ctx, map[string]any{"path": mountTarget})
	require.False(t, result.IsError, result.ForLLM)

	assert.Contains(t, result.ForLLM, "KB:   MountedVault",
		"a knowledge base inside a mounted folder must still be marked KB: when listed through the mount")
	assert.Contains(t, result.ForLLM, "DIR:  MountedPlain")
}

// TestListDirTool_UnreadableMarkerCheckDegradesToOrdinaryDir proves a stat
// failure while checking for a marker (here: the parent itself is not
// readable) never turns into a tool error — list_directory still returns
// the page, simply without the KB: fact for that one entry, matching
// pkg/gateway/rest_library.go's own per-entry tolerance for the identical
// question. This does not attempt to reproduce every unreadable-marker
// path (permissions are unreliable to assert on across CI/test platforms);
// it proves the FUNCTION composition (isKnowledgeBaseEntry returning false
// on error rather than propagating one) using a directory entry that has no
// marker at all, which exercises the exact same "stat error or absent both
// mean false" code path hasSubdir documents.
func TestListDirTool_UnreadableMarkerCheckDegradesToOrdinaryDir(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "Ordinary"), 0o755))

	tool := NewListDirTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{"path": "."})
	require.False(t, result.IsError, result.ForLLM)
	assert.Contains(t, result.ForLLM, "DIR:  Ordinary")
	assert.False(t, strings.Contains(result.ForLLM, "KB:   Ordinary"))
}
