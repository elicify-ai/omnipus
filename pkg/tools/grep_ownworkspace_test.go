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
)

// seedGrepWorkspace creates a minimal, valid workspace record (CoreTeam =
// [agentID]) and returns its work directory — the same shape
// TestResolveTurnFSPolicy_CoreTeamMemberGetsMountsWithoutExplicitWorkspaceID
// (pkg/tools/mount_coreteam_turn_test.go) seeds, reused here so grep's own
// workspace resolution is exercised against the identical fixture shape
// every other mount-consuming tool test uses.
func seedGrepWorkspace(t *testing.T, home, wsID, agentID string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := workspace.SaveRecord(home, workspace.Workspace{
		ID: wsID, Name: "test", Status: "active", CreatedAt: now, UpdatedAt: now,
		CoreTeam: []string{agentID},
	}); err != nil {
		t.Fatalf("seed workspace %s: %v", wsID, err)
	}
	work, err := workspace.EnsureWorkDir(home, wsID)
	if err != nil {
		t.Fatalf("ensure work dir for %s: %v", wsID, err)
	}
	return work
}

// TestGrepTool_OwnWorkspaceOnly is US-3 AS-7 / FR-020's own proof: two
// distinct workspaces, each with its own agent, own root content, and own
// mount. Every combination of caller x scope must reach ONLY that caller's
// own workspace root and its own mounts — never the other's, via any
// argument shape (default full search, an explicit `path` naming the other
// side's mount, or a `path` attempting to walk out with "..").
func TestGrepTool_OwnWorkspaceOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	// --- Workspace A: its own root secret + its own mount ------------------
	const wsA, agentA = "ws-a", "agent-a"
	workA := seedGrepWorkspace(t, home, wsA, agentA)
	if err := os.WriteFile(filepath.Join(workA, "secretA.txt"), []byte("alpha-only-token\n"), 0o600); err != nil {
		t.Fatalf("seed workA secret: %v", err)
	}
	mountATarget := t.TempDir()
	if err := os.WriteFile(filepath.Join(mountATarget, "vault.txt"), []byte("alpha-mount-token\n"), 0o600); err != nil {
		t.Fatalf("seed mountA target: %v", err)
	}
	if _, warn, err := workspace.CreateMount(home, wsA, "extra", mountATarget); err != nil {
		t.Fatalf("create mount for A: %v", err)
	} else if warn != "" {
		t.Logf("mount A warning (expected empty): %s", warn)
	}

	// --- Workspace B: its own, DIFFERENT root secret, no mounts -------------
	const wsB, agentB = "ws-b", "agent-b"
	workB := seedGrepWorkspace(t, home, wsB, agentB)
	if err := os.WriteFile(filepath.Join(workB, "secretB.txt"), []byte("bravo-only-token\n"), 0o600); err != nil {
		t.Fatalf("seed workB secret: %v", err)
	}

	toolA := NewGrepTool(workA, true)
	ctxA := WithTurnWorkspaceDir(WithAgentID(context.Background(), agentA), workA)

	toolB := NewGrepTool(workB, true)
	ctxB := WithTurnWorkspaceDir(WithAgentID(context.Background(), agentB), workB)

	mustZeroHits := func(t *testing.T, tool *GrepTool, ctx context.Context, args map[string]any) {
		t.Helper()
		res := tool.Execute(ctx, args)
		if res.IsError {
			// An error (e.g. "path not found") is an ACCEPTABLE way to
			// refuse crossover — the only unacceptable outcome is a real
			// hit from the other workspace. Log and move on.
			t.Logf("refused with error (acceptable): %s", res.ForLLM)
			return
		}
		if !strings.Contains(res.ForLLM, "0 match(es)") {
			t.Fatalf("expected zero matches (no crossover), got:\n%s", res.ForLLM)
		}
	}

	t.Run("A cannot see B's root secret", func(t *testing.T) {
		mustZeroHits(t, toolA, ctxA, map[string]any{"pattern": "bravo-only-token"})
	})
	t.Run("B cannot see A's root secret", func(t *testing.T) {
		mustZeroHits(t, toolB, ctxB, map[string]any{"pattern": "alpha-only-token"})
	})
	t.Run("B cannot see A's mount content by default scope", func(t *testing.T) {
		mustZeroHits(t, toolB, ctxB, map[string]any{"pattern": "alpha-mount-token"})
	})
	t.Run("A CAN see its own mount content (control case)", func(t *testing.T) {
		res := toolA.Execute(ctxA, map[string]any{"pattern": "alpha-mount-token"})
		if res.IsError {
			t.Fatalf("A must be able to search its own mount, got error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "extra/vault.txt:1:") {
			t.Fatalf("expected a hit under A's own mount (\"extra/\"), got:\n%s", res.ForLLM)
		}
	})
	t.Run("B naming A's mount by name finds nothing (B has no such mount)", func(t *testing.T) {
		res := toolB.Execute(ctxB, map[string]any{"pattern": "alpha", "path": "extra"})
		if !res.IsError {
			t.Fatalf("B has no mount or subdirectory named \"extra\" — expected a not-found error, got success:\n%s", res.ForLLM)
		}
	})
	t.Run("path traversal out of B's root is rejected before any I/O", func(t *testing.T) {
		res := toolB.Execute(ctxB, map[string]any{"pattern": "alpha", "path": "../" + wsA + "/work"})
		if !res.IsError {
			t.Fatalf("a \"..\"-bearing path must be rejected outright, got success:\n%s", res.ForLLM)
		}
	})
	t.Run("an absolute path naming A's mount target directly is rejected", func(t *testing.T) {
		res := toolB.Execute(ctxB, map[string]any{"pattern": "alpha", "path": mountATarget})
		if !res.IsError {
			t.Fatalf("an absolute `path` must be rejected outright, got success:\n%s", res.ForLLM)
		}
	})
	t.Run("include_globs cannot reach outside B's own confined roots", func(t *testing.T) {
		// A glob is matched against the REPORTED (already root-relative)
		// path inside filegrep — it has no host-filesystem meaning at all,
		// so there is no glob spelling that reaches outside the roots this
		// call was already confined to.
		mustZeroHits(t, toolB, ctxB, map[string]any{
			"pattern":       "alpha",
			"include_globs": []any{"**", "../**", "/**"},
		})
	})
}
