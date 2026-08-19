// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestResolveTurnFSPolicy_CoreTeamMemberGetsMountsWithoutExplicitWorkspaceID
// is the regression test for the bug traced in resolvepath.go's
// ResolveTurnFSPolicy: a CoreTeam member's turn that carries NO explicit
// workspace_id (a CLI/ProcessDirect turn, or a scheduled/heartbeat turn)
// still gets its work dir re-rooted into the workspace by
// pkg/agent/workspace_reroot.go's resolveTurnWorkDirOrRefuse, via
// workspace.FindForAgentPreferring keyed on CoreTeam membership. Before the
// fix, ResolveTurnFSPolicy populated AllowedRoots ONLY when
// ToolWorkspaceID(ctx) was non-empty — so this exact shape (work dir
// re-rooted, but no tools.WithWorkspaceID set, matching
// resolveTurnWorkDirOrRefuse's documented "does NOT touch
// tools.WithWorkspaceID" contract) got the workspace's work dir but none of
// its mounts, and a write into a mounted folder was refused with "no mount
// covers it" despite the operator having granted it via
// POST /workspaces/{id}/mounts.
//
// This mirrors TestResolveTurnFSPolicy_MountsBecomeWriteRoots's seeding and
// write-admit assertion, but deliberately omits WithWorkspaceID and instead
// seeds a CoreTeam membership so FindForAgentPreferring's fallback path is
// what supplies the workspace id — the exact gap the fix closes.
func TestResolveTurnFSPolicy_CoreTeamMemberGetsMountsWithoutExplicitWorkspaceID(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	const wsID = "w-coreteam"
	const agentID = "agent-coreteam-member"

	now := time.Now().UTC().Format(time.RFC3339)
	if err := workspace.SaveRecord(home, workspace.Workspace{
		ID:        wsID,
		Name:      "test",
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
		CoreTeam:  []string{agentID},
	}); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	work, err := workspace.EnsureWorkDir(home, wsID)
	if err != nil {
		t.Fatalf("ensure work dir: %v", err)
	}

	// A folder outside the workspace entirely — the case mounts exist for.
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	if _, warn, mountErr := workspace.CreateMount(home, wsID, "repo", target); mountErr != nil {
		t.Fatalf("create mount: %v", mountErr)
	} else if warn != "" {
		t.Logf("mount warning (expected empty for an ordinary folder): %s", warn)
	}

	// The exact CLI/ProcessDirect turn shape: agent id + re-rooted work dir,
	// but deliberately NO WithWorkspaceID — resolveTurnWorkDirOrRefuse never
	// sets it (workspace_reroot.go: "does NOT touch
	// tools.WithWorkspaceID/memory-room routing (FR-030)").
	ctx := WithAgentID(context.Background(), agentID)
	ctx = WithTurnWorkspaceDir(ctx, work)

	if got := ToolWorkspaceID(ctx); got != "" {
		t.Fatalf("test setup: expected no turn-carried workspace id, got %q", got)
	}

	policy, err := ResolveTurnFSPolicy(ctx, work, true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(policy.AllowedRoots) == 0 {
		t.Fatal("CoreTeam member's turn has no explicit workspace_id but IS re-rooted into " +
			"the workspace's work dir; AllowedRoots must still carry the workspace's mounts " +
			"(mounts must follow the work dir), got none")
	}
	// CheckMountTarget resolves symlinks (macOS's /var/folders is itself a
	// symlink to /private/var/folders), so compare against the resolved
	// target rather than the raw t.TempDir() string.
	wantTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target symlinks: %v", err)
	}
	found := false
	for _, root := range policy.AllowedRoots {
		if root == target || root == wantTarget {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("AllowedRoots %v does not contain the mount's host path %q (resolved %q)", policy.AllowedRoots, target, wantTarget)
	}

	if _, err := ResolvePath(ctx, policy, "test", "", FSOpWrite, filepath.Join(target, "new.txt")); err != nil {
		t.Errorf("write into the mounted folder must succeed for a CoreTeam member with no "+
			"explicit workspace_id, got %v", err)
	}
}
