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
//
// ADR-068 (2026-08-23) RETARGETED BOTH PROBES from reads to writes. Both used
// to be `cat <path>`, which stopped testing anything about mounts the moment
// the founder ruled (§2.1 option A) that reads outside the working directory
// are allowed unconditionally: the positive would pass with the mount deleted,
// and the negative asserted a block that is no longer correct. The tests are
// not weakened by the change — they are strengthened, because a mount now
// grants exactly one thing (write, per AllowedMountRoots' own contract in
// pkg/workspace/mount.go) and that is precisely what these two probes now
// exercise. `printf x > <path>` is used rather than `cat <path>` for that
// reason and no other.
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

	// POSITIVE: a command WRITING to the mounted folder by its absolute host
	// path must be allowed — the kernel grants write there, so the guard must too.
	got := tool.guardCommand(ctx, "printf x > "+mountedFile, workResolved)
	require.Empty(t, got,
		"bash command writing to a mounted folder's host path must be allowed; guard said: %q", got)

	// NEGATIVE (no widening): a sibling of the mount that is NOT itself mounted
	// must still be blocked for writes, with the mount message.
	unmounted := filepath.Join(filepath.Dir(hostRoot), "definitely-not-mounted-"+filepath.Base(hostRoot), "secret.txt")
	blocked := tool.guardCommand(ctx, "printf x > "+unmounted, workResolved)
	require.NotEmpty(t, blocked, "a write to a non-mounted outside path must still be blocked")
	require.Contains(t, blocked, "no mount covers it",
		"the negative case should be rejected by the mount-aware working-dir guard")

	// ADR-068 §2.1(A): the same non-mounted path is READABLE. This is the
	// behaviour change the ADR ruled on, asserted here beside the write block so
	// the two dispositions are visibly different rather than accidentally equal.
	readable := tool.guardCommand(ctx, "cat "+unmounted, workResolved)
	require.Empty(t, readable,
		"reads outside the working directory are allowed under ADR-068 even with no mount; guard said: %q", readable)
}

// TestGuardCommand_MountMatchesThroughASymlinkedPath is the regression test for
// ADR-068 §2.3, the latent fragility that ADR marked [INFERRED] and this test
// now settles by execution.
//
// The mismatch: workspace.CreateMount realpath-resolves and stores the host
// path (pkg/workspace/mount.go), while the bash guard resolved its candidate
// with filepath.Abs — lexical, symlinks not followed — and matchedAllowedRoot
// compares by string prefix. So the moment an agent names a mounted folder
// through a symlink (which on macOS is the DEFAULT for anything under /tmp or
// /var), the two strings disagree and the write is refused with "no mount
// covers it" while the mount plainly exists.
//
// Oracle (from ADR-068 §2.3, not the code): a write under an approved mount is
// allowed no matter which of the path's aliases the agent happens to type. The
// operator granted access to a directory, not to a spelling.
//
// Two cases, because the resolver has two branches: an existing file (plain
// EvalSymlinks) and a not-yet-created one (the deepest-existing-ancestor
// fallback, which is the case that matters for `>` redirects creating a file).
func TestGuardCommand_MountMatchesThroughASymlinkedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	const wsID = "w-mount-symlink"
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
	workResolved, err := filepath.EvalSymlinks(work)
	require.NoError(t, err)

	// The mounted folder, and a symlink pointing at it from somewhere else
	// entirely. Both live outside the workspace.
	outside := t.TempDir()
	realDir := filepath.Join(outside, "real")
	require.NoError(t, os.MkdirAll(realDir, 0o700), "create mount target")
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "existing.txt"), []byte("x"), 0o600), "seed file")

	linkDir := filepath.Join(outside, "link")
	require.NoError(t, os.Symlink(realDir, linkDir), "create symlink alias")

	_, warn, mountErr := workspace.CreateMount(home, wsID, "repo", realDir)
	require.NoError(t, mountErr, "create mount")
	require.Empty(t, warn, "mount of an ordinary folder should not warn")

	tool, err := NewExecTool(workResolved, true)
	require.NoError(t, err)

	ctx := WithAgentID(context.Background(), agentID)
	ctx = WithWorkspaceID(ctx, wsID)

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "existing file reached through the symlink", path: filepath.Join(linkDir, "existing.txt")},
		{name: "new file created through the symlink", path: filepath.Join(linkDir, "brand-new.txt")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.guardCommand(ctx, "printf x > "+tc.path, workResolved)
			require.Empty(t, got,
				"a write under an approved mount must be allowed through any alias of the path; guard said: %q", got)
		})
	}

	// The fix must not turn into a general escape: a symlink that points
	// somewhere NOT covered by the mount is still refused.
	decoyTarget := filepath.Join(outside, "decoy")
	require.NoError(t, os.MkdirAll(decoyTarget, 0o700), "create decoy target")
	decoyLink := filepath.Join(outside, "decoy-link")
	require.NoError(t, os.Symlink(decoyTarget, decoyLink), "create decoy symlink")

	blocked := tool.guardCommand(ctx, "printf x > "+filepath.Join(decoyLink, "x.txt"), workResolved)
	require.NotEmpty(t, blocked, "a symlink resolving outside every mount must stay blocked")
	require.Contains(t, blocked, "no mount covers it")
}

// TestGuardCommand_MountResolutionErrorStaysStrict pins the fail-closed
// direction: when no workspace/mount context is present (ResolveTurnFSPolicy
// yields no roots), a WRITE to an absolute path outside the working dir is
// still blocked — the mount exemption never widens the guard when mounts can't
// be resolved.
//
// ADR-068 retargeted this probe from `cat <outside>` to a write, for the reason
// given on the test above: after the read/write split, a read probe would pass
// here whether or not the mount exemption behaved, so it would have stopped
// testing the property named in this function's own title.
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
	got := tool.guardCommand(context.Background(), "printf x > "+strings.TrimSpace(outside), wsResolved)
	require.NotEmpty(t, got, "with no resolvable mounts, a write to an outside path must stay blocked")
	require.Contains(t, got, "no mount covers it")
}
