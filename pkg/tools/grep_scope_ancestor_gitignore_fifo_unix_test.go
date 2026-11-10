// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 fix-round-3 RED pack — the second half of T2 (A3/F3 + N2, Opus
// security-lead review): an ancestor `.gitignore` reached only through
// filegrep.LoadAncestorIgnore (grep_scope.go::regularOnlyFS, used for the
// preloaded ignore chain ABOVE a scoped root — never covered by grepGateFS)
// that is itself an ABSOLUTE symlink pointing to a FIFO OUTSIDE the searched
// root must be refused/skipped without ever hanging the walk. regularOnlyFS
// constructs its host path by joining r.root with the walk-relative name
// (filepath.Join(r.root, filepath.FromSlash(name))) and hands that STRING to
// refuseNonRegularViaOpen, which opens it via a raw os.OpenFile — a raw
// host-path open follows a symlink wherever it points, including outside
// r.root's own directory tree, so this is the one ignore-file code path
// that is not confined by any os.Root at all.
//
//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReadBoundary_GrepAncestorGitignoreAbsoluteSymlinkToOutsideFIFO is T2's
// second scenario: an ancestor `.gitignore`, reached while scoping a search
// to a sub-folder, is an absolute symlink to a FIFO living OUTSIDE the
// workspace. The call must finish within the watchdog and must not surface
// anything from beyond the workspace boundary.
func TestReadBoundary_GrepAncestorGitignoreAbsoluteSymlinkToOutsideFIFO(t *testing.T) {
	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.ws, "sub", "deep", "b.md"), "needle\n")

	outsideFIFO := filepath.Join(f.ext, "outside.fifo")
	if err := os.MkdirAll(filepath.Dir(outsideFIFO), 0o755); err != nil {
		t.Fatalf("mkdir for outside FIFO: %v", err)
	}
	if err := syscall.Mkfifo(outsideFIFO, 0o600); err != nil {
		t.Fatalf("mkfifo %q: %v", outsideFIFO, err)
	}
	// <WS>/.gitignore is an ABSOLUTE symlink to a FIFO OUTSIDE <WS> — the
	// one ancestor level LoadAncestorIgnore("sub/deep") preloads at dir="".
	if err := os.Symlink(outsideFIFO, filepath.Join(f.ws, ".gitignore")); err != nil {
		t.Fatalf("symlink workspace .gitignore -> outside FIFO: %v", err)
	}

	done := make(chan *ToolResult, 1)
	go func() {
		done <- f.grep.Execute(f.ctx, map[string]any{
			"pattern": "needle",
			"path":    filepath.Join(f.ws, "sub", "deep"),
		})
	}()

	var res *ToolResult
	select {
	case res = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("T2/D13: grep hung reading an ancestor .gitignore that is an absolute symlink to an outside FIFO")
	}

	// Oracle: whatever the outcome (a plain result with the file's own
	// .gitignore marked unreadable, or a hard error), the workspace's own
	// "needle" content must still be reachable/reported honestly — a hang
	// or a crash is the only forbidden outcome for this scenario (D13: a
	// non-regular entry anywhere in the ignore chain must never be opened
	// for a blocking read). A leak-shaped assertion does not apply here —
	// a FIFO carries no on-disk "content" to leak; the risk this closes is
	// exclusively the hang / open-outside-the-root side effect.
	if res == nil {
		t.Fatal("no result received before the watchdog fired")
	}
	if !res.IsError && !strings.Contains(res.ForLLM, "needle") {
		t.Fatalf("T2: a non-error result must still report the workspace's own needle hit (the ancestor .gitignore being unreachable must degrade gracefully, not silently drop legitimate matches):\n%s", res.ForLLM)
	}
}
