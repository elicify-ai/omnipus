// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 fix-round-3 RED pack — N5 from the Opus security-lead review: an
// absolute `path` EQUAL to a mount's own host root is the one case
// absoluteGrepRoot's mount branch never special-cased the way
// workspaceScopeRoot special-cases the analogous "path is the workspace
// root itself" case (grep_scope.go::workspaceScopeRoot: "if subPath == \".\"
// { ... the workspace root alone ... }").
//
// ORACLES, both derived from code that ALREADY EXISTS and is trusted for a
// different root kind — never from running absoluteGrepRoot itself:
//
//  1. Reported root name. joinGrepName(prefix, ".") == prefix + "/" + "."
//     (grep_scope.go::joinGrepName: only skips the separator when prefix is
//     empty or already ends in "/", neither true for a mount's absolute
//     host path) — the exact defect workspaceScopeRoot's own "." special
//     case exists to avoid for the workspace-root case (US-1 AS-6 / FR-004:
//     absolute match paths are plain, clean absolute paths — never carrying
//     a literal "/." segment nobody asked for).
//  2. Ancestor-ignore double count. filegrep.LoadAncestorIgnore's OWN doc
//     comment (pkg/filegrep/ignore.go) states its contract explicitly: it
//     reads every .gitignore/.ignore file "from trueRoot's own root down
//     to (but NOT INCLUDING) scope itself." Tracing LoadAncestorIgnore(ancestorFS, ".")
//     by that same code (ignore.go's LoadAncestorIgnore body: scope="."
//     does not match its own scope=="" early return, so the loop's first
//     iteration runs readIgnoreFiles at dir="" — trueRoot's own root, which
//     for the mount branch IS scope itself) — the function's own documented
//     "not including scope itself" promise is violated for this one input,
//     and the mount's OWN .gitignore is loaded a SECOND time as an
//     "ancestor" layer on top of the walk's own unconditional root-level
//     load (filegrep.go: `loadIgnoreLayer(root.FS, "")`, run for every
//     Root regardless of caller). An unreadable root .gitignore is
//     therefore counted as "unreadable" TWICE for the identical file,
//     while workspaceScopeRoot's "." special case (which skips
//     resolveScopedRoot/LoadAncestorIgnore entirely for this input) counts
//     it exactly ONCE.
package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// rbIgnoreUnreadableCount extracts the "N .gitignore/.ignore file(s)
// unreadable" count from the grepStatsLine "stats:" line (grep.go's own
// rendering, `", %d .gitignore/.ignore file(s) unreadable (their rules were
// NOT applied)"`), 0 when the clause is absent (grep.go gates it on > 0).
func rbIgnoreUnreadableCount(t *testing.T, out string) int {
	t.Helper()
	const marker = " .gitignore/.ignore file(s) unreadable"
	idx := strings.Index(out, marker)
	if idx < 0 {
		return 0
	}
	// Walk backwards from idx over the digits grep.go's Fprintf("%d ...")
	// wrote immediately before marker.
	end := idx
	start := end
	for start > 0 && out[start-1] >= '0' && out[start-1] <= '9' {
		start--
	}
	if start == end {
		t.Fatalf("marker %q found but no digits precede it in:\n%s", marker, out)
	}
	n, err := strconv.Atoi(out[start:end])
	if err != nil {
		t.Fatalf("parse ignore-unreadable count from %q: %v", out[start:end], err)
	}
	return n
}

// TestReadBoundary_GrepAbsoluteMountRootReportsPlainName is N5's first
// oracle: an absolute `path` equal to the mount's own host root must report
// its hit under the plain mount host path, never with a trailing "/.".
func TestReadBoundary_GrepAbsoluteMountRootReportsPlainName(t *testing.T) {
	// A UNIQUE sentinel, not the fixture's own "needle": newRBFixture already
	// seeds <MNT>/src/b.md with "needle\n" (readboundary_fixture_test.go's
	// Background), so searching the mount root for "needle" correctly
	// returns TWO hits (this test's own file plus the fixture's) — that was
	// a wrong oracle in an earlier version of this test, not a defect in
	// absoluteGrepRoot. A sentinel absent from the shared fixture keeps this
	// test's one-hit assertion sound without touching that shared fixture.
	const sentinel = "n5-mountroot-plainname-sentinel"
	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.mnt, "b.md"), sentinel+"\n")

	res := f.grep.Execute(f.ctx, map[string]any{
		"pattern": sentinel,
		"path":    f.mnt,
	})
	if res.IsError {
		t.Fatalf("precondition: grep of the mount root itself must be admitted, got: %s", res.ForLLM)
	}

	wantPath := rbSlash(filepath.Join(f.mnt, "b.md"))
	hits := rbHitPaths(res.ForLLM)
	if len(hits) == 0 {
		t.Fatalf("no hits at all — precondition (the %s sentinel file under the mount root) was not found:\n%s", sentinel, res.ForLLM)
	}
	for _, p := range hits {
		if strings.Contains(p, "/./") || strings.HasSuffix(p, "/.") {
			t.Fatalf("N5: match path %q carries a literal \"/.\" segment — the mount-root case must report the plain mount name, got full result:\n%s", p, res.ForLLM)
		}
	}
	if !rbSameSet(hits, []string{wantPath}) {
		t.Fatalf("N5: match paths = %v, want exactly [%q] (the plain, clean absolute mount path)", hits, wantPath)
	}
}

// TestReadBoundary_GrepAbsoluteMountRootDoesNotDoubleCountOwnIgnoreFile is
// N5's second oracle: the mount root's own unreadable .gitignore must be
// counted once (via the walk's unconditional root-level load), never a
// second time as an "ancestor" layer LoadAncestorIgnore's own doc comment
// says scope itself is excluded from.
func TestReadBoundary_GrepAbsoluteMountRootDoesNotDoubleCountOwnIgnoreFile(t *testing.T) {
	// Mount-root case: an absolute `path` equal to the mount's own host
	// root, with the mount's OWN .gitignore unreadable. This is the only
	// unreadable ignore file in this fixture, so any double-count is
	// visible without a second root's own file muddying the number.
	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.mnt, "b.md"), "needle\n")
	giPath := filepath.Join(f.mnt, ".gitignore")
	rbWrite(t, giPath, "*.tmp\n")
	if err := os.Chmod(giPath, 0o000); err != nil {
		t.Fatalf("chmod 0000 %q: %v", giPath, err)
	}
	t.Cleanup(func() { _ = os.Chmod(giPath, 0o644) }) // let t.TempDir clean up

	res := f.grep.Execute(f.ctx, map[string]any{
		"pattern": "needle",
		"path":    f.mnt,
	})
	if res.IsError {
		t.Fatalf("precondition: grep of the mount root itself must be admitted, got: %s", res.ForLLM)
	}

	// Control: the IDENTICAL fixture shape (one unreadable root .gitignore,
	// nothing else) via a FRESH fixture, using `path` equal to the WORKSPACE
	// root itself — workspaceScopeRoot's own "." special case (which this
	// test is not questioning: it skips resolveScopedRoot/LoadAncestorIgnore
	// entirely for that input) must count the file exactly once. A separate
	// fixture avoids the mount case's own unreadable file contaminating this
	// count.
	ctrl := newRBFixture(t)
	rbWrite(t, filepath.Join(ctrl.ws, "notes", "extra.md"), "needle\n")
	ctrlGI := filepath.Join(ctrl.ws, ".gitignore")
	rbWrite(t, ctrlGI, "*.tmp\n")
	if err := os.Chmod(ctrlGI, 0o000); err != nil {
		t.Fatalf("chmod 0000 %q: %v", ctrlGI, err)
	}
	t.Cleanup(func() { _ = os.Chmod(ctrlGI, 0o644) })
	wsRes := ctrl.grep.Execute(ctrl.ctx, map[string]any{"pattern": "needle", "path": ctrl.ws})
	if wsRes.IsError {
		t.Fatalf("control precondition: grep of the workspace root itself must be admitted, got: %s", wsRes.ForLLM)
	}
	wantCount := rbIgnoreUnreadableCount(t, wsRes.ForLLM)
	if wantCount != 1 {
		t.Fatalf("control precondition: the workspace-root unreadable .gitignore must count exactly once via the same fixture shape, got %d in:\n%s", wantCount, wsRes.ForLLM)
	}

	gotCount := rbIgnoreUnreadableCount(t, res.ForLLM)
	if gotCount != wantCount {
		t.Fatalf("N5: an absolute `path` equal to the mount root double-counts its own unreadable .gitignore as an ancestor layer on top of the walk's own root-level load — got %d unreadable file(s), want %d (the same single count the workspace-root control gets for the identical fixture shape):\n%s",
			gotCount, wantCount, res.ForLLM)
	}
}
