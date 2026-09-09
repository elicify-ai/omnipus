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

	"github.com/elicify-ai/omnipus/pkg/config"
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
	tool := NewRequestMountTool(home)

	res := tool.Execute(WithWorkspaceID(context.Background(), wsID), map[string]any{
		"host_path": home,
		"reason":    "I would like my own keys, please",
	})
	require.True(t, res.IsError, "mounting the data directory must fail")
	assert.Contains(t, strings.ToLower(res.ForLLM), "omnipus data directory")

	// And a path INSIDE it, not just the directory itself.
	inside := filepath.Join(home, "workspaces")
	res = tool.Execute(WithWorkspaceID(context.Background(), wsID), map[string]any{
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

	tool := NewRequestMountTool(home)
	res := tool.Execute(WithWorkspaceID(context.Background(), wsID), map[string]any{
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
	tool := NewRequestMountTool(home)

	res := tool.Execute(WithWorkspaceID(context.Background(), wsID), map[string]any{
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
	tool := NewRequestMountTool(home)

	res := tool.Execute(WithWorkspaceID(context.Background(), wsID), map[string]any{"reason": "no path given"})
	assert.True(t, res.IsError)

	res = tool.Execute(WithWorkspaceID(context.Background(), wsID), map[string]any{
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
	tool := NewRequestMountTool(home)

	// A bare context is the real "no workspace" turn: the tool now resolves
	// its target from the turn, so absence lives on the context, not on a
	// constructor argument.
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

	tool := NewRequestMountTool(home)
	res := tool.Execute(WithWorkspaceID(context.Background(), wsID), map[string]any{
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

// TestRequestMount_CoreTeamMemberWithNoExplicitWorkspaceIDCanMount is the
// regression test for the UAT-reported CRITICAL finding S6
// (docs/internal/qa/uat-report-full-tool-catalog-batch1-2026-09-02.md §2.1):
// "request_mount cannot ever succeed" — it always failed with "this turn has
// no workspace to mount into" even after the operator approved the request,
// while the SAME turn's list_mounts calls immediately before and after
// correctly resolved the workspace via
// "workspace_resolved_from":"agent_membership".
//
// Root cause: request_mount only ever consulted ToolWorkspaceID(ctx), unlike
// its own read-side counterpart list_mounts.go (see that Execute's doc
// comment) and ResolveTurnFSPolicy (resolvepath.go, and see
// TestResolveTurnFSPolicy_CoreTeamMemberGetsMountsWithoutExplicitWorkspaceID
// above in this package for the identical fix on the write-grant side) —
// both of which fall back to workspace.FindForAgentPreferring keyed on
// CoreTeam membership when the turn carries no explicit workspace_id. An
// ordinary chat turn for an agent that belongs to a workspace's CoreTeam, but
// whose channel binding never set ts.opts.WorkspaceID, is exactly this shape
// — and it is the shape the live UAT repro hit. Without the fallback,
// request_mount was UNRECOVERABLY broken for that entire class of turn: no
// REST mount record was ever created, and it silently made write_file to the
// mount's intended alias name land in an ordinary in-workspace directory of
// the same name instead of the real external folder.
//
// This mirrors ResolveTurnFSPolicy's CoreTeam regression test's seeding
// exactly (workspace.SaveRecord with CoreTeam=[agentID], WithAgentID only —
// deliberately NO WithWorkspaceID) and asserts the mount actually succeeds,
// end to end: a real workspace mount record is created AND a subsequent
// write through the mount's alias reaches the real external folder — closing
// both halves of the reported defect in one proof.
func TestRequestMount_CoreTeamMemberWithNoExplicitWorkspaceIDCanMount(t *testing.T) {
	home := t.TempDir()
	// ResolveTurnFSPolicy's own mount lookup (the write-side half of this
	// test) reads config.OmnipusHomeDir(), not an agentHome parameter — it
	// must be pointed at this test's isolated home too, exactly like
	// TestResolveTurnFSPolicy_CoreTeamMemberGetsMountsWithoutExplicitWorkspaceID
	// above does.
	t.Setenv(config.EnvHome, home)

	const wsID = "ws-coreteam-mount"
	const agentID = "agent-coreteam-mount-member"

	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID:        wsID,
		Name:      "CoreTeam Mount Test",
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
		CoreTeam:  []string{agentID},
	}))
	_, err := workspace.EnsureWorkDir(home, wsID)
	require.NoError(t, err)

	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "existing.txt"), []byte("real"), 0o600))

	tool := NewRequestMountTool(home)

	// The exact shape the live repro hit: agent identity only, no
	// WithWorkspaceID — resolved purely via CoreTeam membership.
	ctx := WithAgentID(context.Background(), agentID)
	require.Empty(t, ToolWorkspaceID(ctx), "test setup: no turn-carried workspace id")

	res := tool.Execute(ctx, map[string]any{
		"host_path": target,
		"reason":    "regression coverage for the agent-membership fallback",
	})
	require.False(t, res.IsError,
		"a CoreTeam member with no explicit workspace_id must still be able to mount: %s", res.ForLLM)

	// Half 1 of the reported defect: a real workspace mount record must exist.
	mounts, ok := workspace.LoadMounts(home, wsID)
	require.True(t, ok)
	require.Len(t, mounts, 1)
	assert.Equal(t, filepath.Base(target), mounts[0].Name)

	// Half 2: a write through the mount's alias must reach the REAL external
	// folder, not silently land in an ordinary in-workspace directory of the
	// same name (the exact silent-mislead the UAT report flagged as "worse
	// than a clean failure").
	work, err := workspace.EnsureWorkDir(home, wsID)
	require.NoError(t, err)
	policy, err := ResolveTurnFSPolicy(ctx, work, true)
	require.NoError(t, err)
	handle, err := ResolvePath(ctx, policy, "test", "",
		FSOpWrite, filepath.Join(work, mounts[0].Name, "probe_after_grant.txt"))
	require.NoError(t, err, "write through the mount alias must be permitted")
	require.NoError(t, handle.WriteFile([]byte("via-mount")))
	require.NoError(t, handle.Close())

	resolvedTarget, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(resolvedTarget, "probe_after_grant.txt"))
	require.NoError(t, err, "the write must have actually reached the real external folder")
	assert.Equal(t, "via-mount", string(got))
}

func TestRequestMount_AcceptsPathAlias(t *testing.T) {
	home, wsID, target := newMountFixture(t)
	tool := NewRequestMountTool(home)
	args := map[string]any{
		"path":   target,
		"reason": "model used path instead of host_path",
	}
	require.NoError(t, validateToolArgs(tool.Parameters(), args),
		"schema validation must accept path as the folder field")
	res := tool.Execute(WithWorkspaceID(context.Background(), wsID), args)
	require.False(t, res.IsError, "path must be accepted as an alias of host_path: %s", res.ForLLM)
}
