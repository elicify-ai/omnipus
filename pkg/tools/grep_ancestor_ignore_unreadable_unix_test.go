//go:build !windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Unix-only per grep_singlefile_unix_test.go's own precedent: this test
// relies on Unix file-mode bits (chmod 0o000) to simulate a real
// permission-denied .gitignore, and is skipped when running as root since
// Unix permission bits do not block root's own access.
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGrepTool_ScopedSearch_UnreadableAncestorIgnoreIsCounted pins finding
// L11: resolveScopedRoot calls filegrep.LoadAncestorIgnore to seed a scoped
// search's inherited .gitignore/.ignore rules before the walk starts, and
// discarded the "unreadable" count that call returns (grep.go's own former
// comment: "this preload runs before a filegrep.state exists to fold it
// into Stats.IgnoreFilesUnreadable"). Stats.IgnoreFilesUnreadable (finding
// F-E) only ever covered ignore files read DURING the walk itself, so on a
// scoped search (path=subdir) a permission-denied ANCESTOR .gitignore
// silently applied fewer rules than intended with NO trace anywhere in the
// response — the identical class of gap F-E closed for the within-walk
// case. (Note: rest_library_files_search.go has the identical gap at its
// own LoadAncestorIgnore call sites — owned by a different agent, not fixed
// here.)
func TestGrepTool_ScopedSearch_UnreadableAncestorIgnoreIsCounted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: Unix permission bits do not block access")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "build"), 0o700); err != nil {
		t.Fatalf("seed dirs: %v", err)
	}
	// An ANCESTOR .gitignore — strictly above the "src" scope being
	// searched below — that exists but cannot be read. LoadAncestorIgnore
	// reads exactly this file when resolveScopedRoot seeds "src"'s inherited
	// rules; the within-walk search of "src" itself never touches it.
	giPath := filepath.Join(dir, ".gitignore")
	mustWriteFile(t, giPath, "build/\n")
	if err := os.Chmod(giPath, 0o000); err != nil {
		t.Fatalf("chmod ancestor .gitignore: %v", err)
	}
	defer func() {
		if err := os.Chmod(giPath, 0o600); err != nil {
			t.Fatalf("restore ancestor .gitignore perms: %v", err)
		}
	}()
	mustWriteFile(t, filepath.Join(dir, "src", "build", "generated.go"), "needle in generated output\n")

	tool := newTestGrepTool(t, dir)
	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    "src",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	// Degraded behavior (rules from the unreadable ancestor file do not
	// apply) is unchanged and still exercised here — build/generated.go is
	// still walked and matched because the exclusion rule that would have
	// pruned it could not be read. The fix under test is OBSERVABILITY,
	// not a behavior change.
	if !strings.Contains(res.ForLLM, "build/generated.go") {
		t.Fatalf("precondition failed: the unreadable ancestor .gitignore's rule must not apply "+
			"(degraded behavior unchanged), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "1 .gitignore/.ignore file(s) unreadable") {
		t.Fatalf("expected the unreadable ancestor .gitignore to be counted and surfaced in the "+
			"stats line, got:\n%s", res.ForLLM)
	}
}
