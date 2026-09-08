// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// DEFECT-G1 (field report): pointing `path` at a single FILE instead of a
// directory returned `path "uat-grep/sample.txt" not found in your
// workspace` — with an underlying errno of "not a directory" — for a file
// the tester had just confirmed existed with `ls`. Root cause:
// (*os.Root).OpenRoot only ever opens a directory, and grep's old scope
// resolution called it on `path` unconditionally, wrapping ANY failure
// (including ENOTDIR from an existing file) as one blanket "not found"
// sentence.
//
// Two things had to become true (see resolveScopedRoot's doc comment in
// grep.go for the fix): (a) scoping to a single existing file WORKS — the
// natural thing to try, and the tester tried it three times before working
// out the constraint; (b) whatever cases remain unsupported state what is
// ACTUALLY true (not-exist / permission-denied / wrong-kind), never a
// blanket lie.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// --- (a) scoping to a single file works -------------------------------------

func TestGrepTool_ScopeSingleFile_TopLevel(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "sample.txt"), "needle line one\nirrelevant\n")
	mustWriteFile(t, filepath.Join(dir, "other.txt"), "needle should not be reported from here\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    "sample.txt",
	})
	if res.IsError {
		t.Fatalf("scoping to a top-level file must work, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "sample.txt:1:") {
		t.Fatalf("expected a hit rendered with the full workspace-relative path, got:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "other.txt") {
		t.Fatalf("a single-file scope must not report a hit from any other file, got:\n%s", res.ForLLM)
	}
}

func TestGrepTool_ScopeSingleFile_Nested(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "uat-grep"), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "uat-grep", "sample.txt"), "needle line one\nsome other line\n")
	mustWriteFile(t, filepath.Join(dir, "uat-grep", "sibling.txt"), "needle should not surface from the sibling\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    "uat-grep/sample.txt",
	})
	if res.IsError {
		t.Fatalf("scoping to an existing nested file must work, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "uat-grep/sample.txt:1:") {
		t.Fatalf("expected a hit rendered with the full workspace-relative path (matching the `path` argument), got:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "sibling.txt") {
		t.Fatalf("a single-file scope must not reach the sibling file, got:\n%s", res.ForLLM)
	}
}

// TestGrepTool_ScopeSingleFile_NameMatch proves a NAME match (not just
// content) still renders with the full path when scoped to a single file.
func TestGrepTool_ScopeSingleFile_NameMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "docs", "needle-report.txt"), "unrelated content\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "needle-report",
		"path":    "docs/needle-report.txt",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "docs/needle-report.txt  (name match)") {
		t.Fatalf("expected a name-match hit with the full path, got:\n%s", res.ForLLM)
	}
}

// TestGrepTool_ScopeSingleFile_InMount proves file-scoping composes with the
// mount-resolution half of `path` (a mount name, optionally with a
// subpath) exactly as directory-scoping already does.
func TestGrepTool_ScopeSingleFile_InMount(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	const wsID, agentID = "ws-sf-mount", "agent-sf-mount"
	work := seedGrepWorkspace(t, home, wsID, agentID)

	mountTarget := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mountTarget, "sub"), 0o700); err != nil {
		t.Fatalf("seed mount subdir: %v", err)
	}
	mustWriteFile(t, filepath.Join(mountTarget, "top.txt"), "needle at mount top level\n")
	mustWriteFile(t, filepath.Join(mountTarget, "sub", "deep.txt"), "needle nested in the mount\n")
	mustWriteFile(t, filepath.Join(mountTarget, "sub", "other.txt"), "needle must not leak from here\n")
	if _, _, err := workspace.CreateMount(home, wsID, "extra", mountTarget); err != nil {
		t.Fatalf("create mount: %v", err)
	}

	tool := NewGrepTool(work, true)
	ctx := WithTurnWorkspaceDir(WithAgentID(context.Background(), agentID), work)

	t.Run("file directly at the mount root", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"pattern": "needle", "path": "extra/top.txt"})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "extra/top.txt:1:") {
			t.Fatalf("expected a hit at extra/top.txt, got:\n%s", res.ForLLM)
		}
	})

	t.Run("file nested inside the mount", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"pattern": "needle", "path": "extra/sub/deep.txt"})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "extra/sub/deep.txt:1:") {
			t.Fatalf("expected a hit at extra/sub/deep.txt, got:\n%s", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "other.txt") {
			t.Fatalf("a single-file scope inside a mount must not reach a sibling, got:\n%s", res.ForLLM)
		}
	})
}

// --- include_globs/exclude_globs compose with a single-file scope (AND) ----

// TestGrepTool_ScopeSingleFile_IncludeGlobsAND documents the chosen
// semantics (grepRoots' doc comment / resolveScopedRoot's doc comment): a
// single-file scope is a directory walk narrowed to exactly one entry, so
// include_globs/exclude_globs still apply to that one entry exactly as they
// would during an ordinary walk — an explicit file scope ANDs with the
// globs rather than overriding them.
func TestGrepTool_ScopeSingleFile_IncludeGlobsAND(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "sample.txt"), "needle line\n")
	tool := newTestGrepTool(t, dir)

	t.Run("a matching include_glob still finds the file", func(t *testing.T) {
		res := tool.Execute(context.Background(), map[string]any{
			"pattern":       "needle",
			"path":          "sample.txt",
			"include_globs": []any{"*.txt"},
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "sample.txt:1:") {
			t.Fatalf("expected a hit, got:\n%s", res.ForLLM)
		}
	})

	t.Run("a non-matching include_glob ANDs the file scope down to zero hits", func(t *testing.T) {
		res := tool.Execute(context.Background(), map[string]any{
			"pattern":       "needle",
			"path":          "sample.txt",
			"include_globs": []any{"*.go"},
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "0 match(es)") {
			t.Fatalf("expected zero hits when include_globs excludes the explicitly-scoped file, got:\n%s", res.ForLLM)
		}
		// OBS-G1: the exclusion must be OBSERVABLE, not a silent zero.
		if !strings.Contains(res.ForLLM, "excluded by include_globs/exclude_globs") {
			t.Fatalf("expected the glob-filtered-everything explanation, got:\n%s", res.ForLLM)
		}
	})

	t.Run("exclude_globs still refuses the explicitly-scoped file", func(t *testing.T) {
		res := tool.Execute(context.Background(), map[string]any{
			"pattern":       "needle",
			"path":          "sample.txt",
			"exclude_globs": []any{"sample.txt"},
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "0 match(es)") {
			t.Fatalf("expected zero hits when exclude_globs excludes the explicitly-scoped file, got:\n%s", res.ForLLM)
		}
	})
}

// --- ancestor .gitignore parity with directory scoping ----------------------

// TestGrepTool_ScopeSingleFile_AncestorIgnoreStillApplies documents (mirrors
// TestGrepTool_ScopedSearchHonorsAncestorIgnore's directory-scope case) that
// a single-file scope is treated as a directory walk narrowed to one entry
// for EVERY per-entry rule, .gitignore/.ignore included — not only globs.
// Naming an ignored file directly does not exempt it from the rule that
// would have pruned it during an ordinary walk.
func TestGrepTool_ScopeSingleFile_AncestorIgnoreStillApplies(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "build"), 0o700); err != nil {
		t.Fatalf("seed dirs: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, ".gitignore"), "build/\n")
	mustWriteFile(t, filepath.Join(dir, "src", "build", "generated.go"), "needle in generated output\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    "src/build/generated.go",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "0 match(es)") {
		t.Fatalf("a file scope naming an ignored path must still honor the ancestor .gitignore, got:\n%s", res.ForLLM)
	}
}

// --- (b) honest errors for what remains unsupported -------------------------

func TestGrepTool_ScopeSingleFile_NonExistentPathIsHonest(t *testing.T) {
	dir := t.TempDir()
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    "does/not/exist.txt",
	})
	if !res.IsError {
		t.Fatalf("a nonexistent `path` must be refused, got success:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "does not exist") {
		t.Fatalf("expected an honest \"does not exist\" message, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "not a directory") {
		t.Fatalf("must never surface the raw, self-contradicting OpenRoot errno text, got: %s", res.ForLLM)
	}
}

// TestGrepTool_ScopeSingleFile_NoLyingErrnoOnAnyRefusal is DEFECT-G1's core
// regression pin: for any scope outcome grep refuses, the message must never
// simultaneously claim "not found" AND surface an underlying "not a
// directory" errno — that self-contradiction is exactly what misled the
// field tester three times running.
func TestGrepTool_ScopeSingleFile_NoLyingErrnoOnAnyRefusal(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "sample.txt"), "needle\n")
	tool := newTestGrepTool(t, dir)

	// A path that walks THROUGH an existing file as if it were a directory
	// ("sample.txt/nested") is the one shape that can still surface a raw
	// "not a directory" errno (verified against the live os.Root behavior,
	// not assumed) — assert it is at least never paired with "not found".
	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    "sample.txt/nested",
	})
	if !res.IsError {
		t.Fatalf("expected a refusal, got success:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "not found") && strings.Contains(res.ForLLM, "not a directory") {
		t.Fatalf("message both claims \"not found\" and surfaces a contradicting \"not a directory\" errno: %s", res.ForLLM)
	}
}
