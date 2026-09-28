// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
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

// TestGrepTool_OwnWorkspaceOnly — rewritten for #920
// (read-boundary-consistency-spec.md, Regression Test Requirements,
// "TestGrepTool_OwnWorkspaceOnly rewrite (round-1 unasked question 3)").
//
// What still holds (US-3 AS-7's cross-workspace half, tightened): another
// workspace's files are unreachable by every argument shape, and a refusal
// now carries the reason read_file records for the same path — carve_out,
// DS-1 row 12 — in exactly one path.access_denied row written through a
// REAL registry-wired audit logger (FR-020). The old version asserted no
// reason at all (any error counted as a refusal), and its ".." subtest used
// "../ws-a/work", which lands at workspaces/ws-b/ws-a/work — not at
// workspace A.
//
// What flips (D1): an absolute path naming A's mount TARGET — a host
// folder outside $OMNIPUS_HOME that read_file already admits for B — is now
// admitted and searched. Keeping it refused would pin the superseded
// FR-020 confinement.
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
	ctxB := WithTurnWorkspaceDir(WithAgentID(context.Background(), agentB), workB)

	// newToolB registers a fresh grep for B in a real registry with a real
	// audit logger (FR-020 — never a logger set by hand on the tool) and
	// returns a reader for the rows that call sequence wrote.
	newToolB := func(t *testing.T) (*GrepTool, func() []auditRow) {
		t.Helper()
		dir := t.TempDir()
		logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
		if err != nil {
			t.Fatalf("audit.NewLogger: %v", err)
		}
		reg := NewToolRegistry()
		reg.SetAuditLogger(logger)
		tool := NewGrepTool(workB, true)
		reg.Register(tool)
		return tool, func() []auditRow {
			if _, statErr := os.Stat(filepath.Join(dir, "audit.jsonl")); errors.Is(statErr, os.ErrNotExist) {
				_ = logger.Close()
				return nil
			}
			return readAuditRows(t, logger, dir)
		}
	}

	mustZeroHits := func(t *testing.T, tool *GrepTool, ctx context.Context, args map[string]any) {
		t.Helper()
		res := tool.Execute(ctx, args)
		if res.IsError {
			t.Fatalf("a default-scope search must run and find nothing of the other side, got error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "0 match(es)") {
			t.Fatalf("expected zero matches (no crossover), got:\n%s", res.ForLLM)
		}
	}

	t.Run("A cannot see B's root secret", func(t *testing.T) {
		mustZeroHits(t, toolA, ctxA, map[string]any{"pattern": "bravo-only-token"})
	})
	t.Run("B cannot see A's root secret", func(t *testing.T) {
		toolB, _ := newToolB(t)
		mustZeroHits(t, toolB, ctxB, map[string]any{"pattern": "alpha-only-token"})
	})
	t.Run("B cannot see A's mount content by default scope", func(t *testing.T) {
		toolB, _ := newToolB(t)
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
	t.Run("B naming extra (not B's mount) is a workspace-relative not-found error, not a refusal", func(t *testing.T) {
		toolB, rows := newToolB(t)
		res := toolB.Execute(ctxB, map[string]any{"pattern": "alpha", "path": "extra"})
		if !res.IsError {
			t.Fatalf("B has no mount or subdirectory named \"extra\" — expected a not-found error, got success:\n%s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "extra") {
			t.Fatalf("FR-010: the error must name the path \"extra\", got: %s", res.ForLLM)
		}
		if d := rbRowsFor(rows(), rbAccessDeniedEvent, ""); len(d) != 0 {
			t.Fatalf("a not-found path is not a refusal: want no path.access_denied row, got %+v", d)
		}
	})
	for _, reach := range []struct{ name, path string }{
		{"relative ../../ws-a/work", "../../" + wsA + "/work"},
		{"absolute $OMNIPUS_HOME/workspaces/ws-a/work", filepath.Join(home, "workspaces", wsA, "work")},
	} {
		t.Run("B reaching A's workspace by "+reach.name+" is refused as carve_out", func(t *testing.T) {
			toolB, rows := newToolB(t)
			res := toolB.Execute(ctxB, map[string]any{"pattern": "alpha", "path": reach.path})
			if !res.IsError {
				t.Fatalf("DS-1 row 12: another workspace must be refused, got success:\n%s", res.ForLLM)
			}
			if strings.Contains(res.ForLLM, "alpha-only-token") {
				t.Fatalf("the refusal leaked A's content: %s", res.ForLLM)
			}
			d := rbRowsFor(rows(), rbAccessDeniedEvent, "grep")
			if len(d) != 1 {
				t.Fatalf("want exactly one grep path.access_denied row, got %d: %+v", len(d), d)
			}
			if got := d[0].detail("reason"); got != ReasonCarveOut {
				t.Fatalf("refusal reason = %q, want %q (DS-1 row 12)", got, ReasonCarveOut)
			}
		})
	}
	t.Run("an absolute path naming A's mount target is admitted like read_file admits it (D1)", func(t *testing.T) {
		toolB, _ := newToolB(t)
		res := toolB.Execute(ctxB, map[string]any{"pattern": "alpha", "path": mountATarget})
		if res.IsError {
			t.Fatalf("D1: a host folder outside $OMNIPUS_HOME is admitted for B exactly as read_file admits it, got error: %s", res.ForLLM)
		}
		realTarget, err := filepath.EvalSymlinks(mountATarget)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.ToSlash(filepath.Join(realTarget, "vault.txt")) + ":1:"
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("expected the absolute hit %q, got:\n%s", want, res.ForLLM)
		}
	})
	t.Run("include_globs cannot reach outside B's own confined roots", func(t *testing.T) {
		// A glob is matched against the REPORTED (already root-relative)
		// path inside filegrep — it has no host-filesystem meaning at all,
		// so there is no glob spelling that reaches outside the roots this
		// call was already confined to.
		// Unchanged from before #920 (spec: "keep the include_globs subtest
		// unchanged"), including its tolerance of an error result.
		toolB, _ := newToolB(t)
		res := toolB.Execute(ctxB, map[string]any{
			"pattern":       "alpha",
			"include_globs": []any{"**", "../**", "/**"},
		})
		if res.IsError {
			t.Logf("refused with error (acceptable): %s", res.ForLLM)
			return
		}
		if !strings.Contains(res.ForLLM, "0 match(es)") {
			t.Fatalf("expected zero matches (no crossover), got:\n%s", res.ForLLM)
		}
	})
}
