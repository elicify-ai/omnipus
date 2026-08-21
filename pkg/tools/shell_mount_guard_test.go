// Omnipus - Ultra-lightweight personal AI agent
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

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// TestGuardCommand_MountedFolderPathIsWritableInBash is the regression test for
// the bug traced in shell.go's guardCommand/checkPathSegment: the bash
// command-TEXT scan gated absolute paths against ONLY safePaths, the operator
// allowlist, and cwd-containment — never the workspace mounts — yet its own
// rejection message said "and no mount covers it". So when an operator mounted
// a host folder into a workspace (POST /workspaces/{id}/mounts), the kernel
// policy and the app-layer path resolver (write_file) both granted write to it,
// but a plain `bash` command that named that folder by its absolute HOST path
// was blocked pre-flight — the agents in the workspace did not automatically
// get write access through bash.
//
// Oracle (from the spec, not the code): a folder an operator has mounted into
// the workspace IS part of that turn's writable filesystem. The bash guard must
// therefore ALLOW an absolute path under the mount root (return ""), matching
// the kernel policy it is supposed to mirror. A path outside the workspace that
// is NOT under any mount must still be BLOCKED — the fix aligns the guard with
// mounts, it does not open the guard.
func TestGuardCommand_MountedFolderPathIsWritableInBash(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	const wsID = "w-mount-guard"
	const agentID = "agent-in-ws"

	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID:        wsID,
		Name:      "test",
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
		CoreTeam:  []string{agentID},
	}), "seed workspace")

	work, err := workspace.EnsureWorkDir(home, wsID)
	require.NoError(t, err, "ensure work dir")
	// The guard compares filepath.Abs(cwd); on macOS t.TempDir() lives under a
	// /var -> /private/var symlink, so resolve it the same way guardFixture does.
	workResolved, err := filepath.EvalSymlinks(work)
	require.NoError(t, err)

	// A host folder OUTSIDE the workspace entirely — the case mounts exist for.
	hostDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "existing.txt"), []byte("x"), 0o600), "seed host file")

	// CreateMount realpath-resolves and stores the host path; AllowedMountRoots
	// (and thus the guard's mountRoots) return that realpath, so reference the
	// resolved form in the command.
	_, warn, mountErr := workspace.CreateMount(home, wsID, "repo", hostDir)
	require.NoError(t, mountErr, "create mount")
	require.Empty(t, warn, "mount of an ordinary folder should not warn")
	hostRoot, err := filepath.EvalSymlinks(hostDir)
	require.NoError(t, err)
	mountedFile := filepath.Join(hostRoot, "existing.txt")

	tool, err := NewExecTool(workResolved, true)
	require.NoError(t, err)

	// The turn is the workspace's own agent (ToolWorkspaceID drives the mount
	// lookup in ResolveTurnFSPolicy).
	ctx := WithAgentID(context.Background(), agentID)
	ctx = WithWorkspaceID(ctx, wsID)

	// POSITIVE: a command naming the mounted folder by its absolute host path
	// must be allowed — the kernel grants write there, so the guard must too.
	got := tool.guardCommand(ctx, "cat "+mountedFile, workResolved)
	require.Empty(t, got,
		"bash command referencing a mounted folder's host path must be allowed; guard said: %q", got)

	// NEGATIVE (no widening): a sibling of the mount that is NOT itself mounted
	// must still be blocked with the mount message.
	unmounted := filepath.Join(filepath.Dir(hostRoot), "definitely-not-mounted-"+filepath.Base(hostRoot), "secret.txt")
	blocked := tool.guardCommand(ctx, "cat "+unmounted, workResolved)
	require.NotEmpty(t, blocked, "a non-mounted outside path must still be blocked")
	require.Contains(t, blocked, "no mount covers it",
		"the negative case should be rejected by the mount-aware working-dir guard")
}

// TestGuardCommand_MountResolutionErrorStaysStrict pins the fail-closed
// direction: when no workspace/mount context is present (ResolveTurnFSPolicy
// yields no roots), an absolute path outside the working dir is still blocked —
// the mount exemption never widens the guard when mounts can't be resolved.
func TestGuardCommand_MountResolutionErrorStaysStrict(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	ws := t.TempDir()
	wsResolved, err := filepath.EvalSymlinks(ws)
	require.NoError(t, err)
	tool, err := NewExecTool(wsResolved, true)
	require.NoError(t, err)

	// No WithWorkspaceID / no mounts on disk -> no mount roots.
	outside := filepath.Join(t.TempDir(), "etc", "shadow")
	got := tool.guardCommand(context.Background(), "cat "+strings.TrimSpace(outside), wsResolved)
	require.NotEmpty(t, got, "with no resolvable mounts, an outside path must stay blocked")
	require.Contains(t, got, "no mount covers it")
}
