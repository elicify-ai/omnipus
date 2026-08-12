// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMountFixture builds a home with one real workspace, plus a target folder
// outside it that an agent might ask for.
func newMountFixture(t *testing.T) (home, wsID, target string) {
	t.Helper()
	home = t.TempDir()
	wsID = "ws-request-mount"
	// A real workspace RECORD, not just a directory: CreateMount loads the
	// workspace before it will grant anything, so a bare mkdir produces a
	// "file does not exist" that has nothing to do with what is under test.
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces", wsID, "work"), 0o700))
	now := time.Now().UTC().Format(time.RFC3339)
	rec := map[string]any{
		"id": wsID, "name": "Request Mount Test", "status": "active",
		"created_at": now, "updated_at": now,
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", wsID+".json"), data, 0o600))
	target = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "existing.txt"), []byte("real"), 0o600))
	return home, wsID, target
}

// TestRequestMount_RefusesTheOmnipusDataDirectory is the one hard boundary
// (FR-7.5). An agent that could mount $OMNIPUS_HOME would make config.json and
// master.key writable and could then disable its own sandbox — so the tool that
// exists to ASK for access must never be the way around it.
func TestRequestMount_RefusesTheOmnipusDataDirectory(t *testing.T) {
	home, wsID, _ := newMountFixture(t)
	tool := NewRequestMountTool(home, wsID)

	res := tool.Execute(context.Background(), map[string]any{
		"host_path": home,
		"reason":    "I would like my own keys, please",
	})
	require.True(t, res.IsError, "mounting the data directory must fail")
	assert.Contains(t, strings.ToLower(res.ForLLM), "omnipus data directory")

	// And a path INSIDE it, not just the directory itself.
	inside := filepath.Join(home, "workspaces")
	res = tool.Execute(context.Background(), map[string]any{
		"host_path": inside,
		"reason":    "just a subfolder",
	})
	assert.True(t, res.IsError, "a path inside the data directory must be refused too")
}

// TestRequestMount_RefusesTheSameTargetViaSymlink covers the form anyone would
// actually reach for. A refusal that only matches the literal path is defeated
// by pointing a link at it.
func TestRequestMount_RefusesTheSameTargetViaSymlink(t *testing.T) {
	home, wsID, _ := newMountFixture(t)
	link := filepath.Join(t.TempDir(), "looks-innocent")
	require.NoError(t, os.Symlink(home, link))

	tool := NewRequestMountTool(home, wsID)
	res := tool.Execute(context.Background(), map[string]any{
		"host_path": link,
		"reason":    "a perfectly ordinary folder",
	})
	require.True(t, res.IsError, "a symlink to the data directory must be refused")
	assert.Contains(t, strings.ToLower(res.ForLLM), "omnipus data directory")
}

// TestRequestMount_GrantsAnOrdinaryFolder proves the tool actually works once
// approved — a refusal-only tool would be a control that never does its job.
func TestRequestMount_GrantsAnOrdinaryFolder(t *testing.T) {
	home, wsID, target := newMountFixture(t)
	tool := NewRequestMountTool(home, wsID)

	res := tool.Execute(context.Background(), map[string]any{
		"host_path": target,
		"reason":    "to run the build",
	})
	require.False(t, res.IsError, "an ordinary folder must be mountable: %s", res.ForLLM)

	mounts, ok := workspace.LoadMounts(home, wsID)
	require.True(t, ok)
	require.Len(t, mounts, 1)
	assert.Equal(t, filepath.Base(target), mounts[0].Name)

	resolvedTarget, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	assert.Equal(t, resolvedTarget, mounts[0].HostPath,
		"the stored path must be symlink-resolved so it matches what enforcement sees")
}

// TestRequestMount_RejectsRelativeAndMissingInput pins the input contract. A
// relative path would resolve against the gateway's working directory — a
// location neither the agent nor the operator is thinking about.
func TestRequestMount_RejectsRelativeAndMissingInput(t *testing.T) {
	home, wsID, _ := newMountFixture(t)
	tool := NewRequestMountTool(home, wsID)

	res := tool.Execute(context.Background(), map[string]any{"reason": "no path given"})
	assert.True(t, res.IsError)

	res = tool.Execute(context.Background(), map[string]any{
		"host_path": "relative/path",
		"reason":    "relative",
	})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "absolute")
}

// TestRequestMount_NoWorkspaceSaysSoRatherThanGuessing: a turn with no
// workspace has no target, and picking one would grant access somewhere the
// operator never looked.
func TestRequestMount_NoWorkspaceSaysSoRatherThanGuessing(t *testing.T) {
	home, _, target := newMountFixture(t)
	tool := NewRequestMountTool(home, "")

	res := tool.Execute(context.Background(), map[string]any{
		"host_path": target,
		"reason":    "anywhere will do",
	})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "no workspace")
}

// TestRequestMount_BroadTargetWarnsInsteadOfRefusing pins the operator's own
// decision: everything except the Omnipus directory warns and proceeds
// (FR-7.6). The warning must reach the agent's result too, so it cannot report
// a broad grant back as an unremarkable success.
func TestRequestMount_BroadTargetWarnsInsteadOfRefusing(t *testing.T) {
	home, wsID, _ := newMountFixture(t)
	// $HOME is the canonical broad target and exists on every platform.
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this host")
	}
	if _, statErr := os.Stat(userHome); statErr != nil {
		t.Skip("home directory unreadable on this host")
	}

	tool := NewRequestMountTool(home, wsID)
	res := tool.Execute(context.Background(), map[string]any{
		"host_path": userHome,
		"reason":    "everything",
	})
	require.False(t, res.IsError, "a broad target is allowed, not refused: %s", res.ForLLM)
	assert.Contains(t, strings.ToLower(res.ForLLM), "note:",
		"a broad grant must not be reported back as an unremarkable success")
}

// TestMountNameFromHostPath keeps the derived name a single, usable segment.
// The agent does not choose it: it approved a PATH, and a self-chosen label
// could misrepresent what the folder is.
func TestMountNameFromHostPath(t *testing.T) {
	assert.Equal(t, "api", mountNameFromHostPath("/Users/dana/projects/api"))
	assert.Equal(t, "api", mountNameFromHostPath("/Users/dana/projects/api/"))
	assert.Equal(t, "my-project", mountNameFromHostPath("/tmp/my project"))
	assert.Equal(t, "hidden", mountNameFromHostPath("/tmp/.hidden"))
	assert.Equal(t, "", mountNameFromHostPath("/"))

	// No separator can survive, or the "name" would be a path.
	assert.NotContains(t, mountNameFromHostPath("/tmp/a/b"), "/")
}
