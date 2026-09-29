// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 fix-round-3 RED pack — T1 (A2/F2, Opus security-lead review): needs
// a NEW production test seam that does not exist on 5a87aa6bd. Isolated
// behind this build tag so the default build (`-tags goolm,stdjson`, no
// `fix3seam`) stays green; this file does NOT compile until the seam below
// is added to grep_scope.go. The backend developer adds the seam and
// removes this tag (or folds these tests into a normal *_test.go file once
// the seam exists).
//
// # Needed production seam: grepPreOpenRootHook (F2 — the pre-open gap the
// A2 fix does not cover)
//
// Finding F2 (Opus security-lead review of the A2 fix, commit 88740dc75):
// the A2 SameFile check (grep_scope.go::sameFileCheck) only detects a swap
// that lands AFTER os.OpenRoot has already bound its fd — it compares
// root.Stat(".") (via the bound fd) against os.Stat(anchor) (a fresh path
// lookup) AFTER both calls, so when the swap happened BEFORE the open both
// sides observe the SAME post-swap reality and agree ("same") even though
// the walk is now rooted at the wrong directory. A swap landing BEFORE any
// of the following RAW, unanchored os.OpenRoot calls is invisible to A2 and
// to every seam that exists today:
//
//   - absoluteGrepRoot's mount-anchored branch: os.OpenRoot(m.HostPath)
//   - absoluteGrepRoot's unanchored branch:     os.OpenRoot(parent)
//   - workspaceScopeRoot:                        os.OpenRoot(policy.WorkDir)
//   - mountScopeRoot:                            os.OpenRoot(m.HostPath)
//
// None of the three existing seams (grepScopeStatOpenHook,
// grepAbsolutePostOpenHook, grepGateFIFOSwapHook) fires in this window:
// grepScopeStatOpenHook fires INSIDE resolveScopedRoot, which for all four
// call sites above only runs AFTER the relevant os.OpenRoot has already
// succeeded (confirmed by reading grep_scope.go/grep.go on 5a87aa6bd: in
// every case the *os.Root this file calls "container"/"mr"/"wr" is already
// bound before resolveScopedRoot is ever invoked) — a swap landing after
// that seam's own stat is protected by os.Root's own symlink-escape
// containment (the mechanism the A1 fix relies on, empirically confirmed:
// see this branch's fix3-red-T3-pre-a1.log/fix3-red-T3-post.log receipts —
// the SAME hook location produces no leak on EITHER side of the A1 fix, for
// a single-path-component target). grepAbsolutePostOpenHook fires strictly
// AFTER the same os.OpenRoot calls, which is A2's own already-tested
// window.
//
// Needed signature (same pattern as the three existing seams in
// grep_scope.go — atomic.Pointer, nil in production, one-shot t.Cleanup
// restore in tests):
//
//	var grepPreOpenRootHook atomic.Pointer[func(hostPath string)]
//	func setGrepPreOpenRootHook(fn func(hostPath string)) (restore func())
//	func runGrepPreOpenRootHook(hostPath string)
//
// Call runGrepPreOpenRootHook(hostPath) immediately before each of the four
// os.OpenRoot(hostPath) calls named above, passing the exact string about
// to be opened.
//
// # Tag guidance (fix-round-3 RED-pack correction, item 4)
//
// This file's tag is plain `fix3seam` (no `unix`) because its swap
// machinery (installFix3PreOpenSwapHook, os.Symlink) and its two test
// functions are not unix-specific — os.Symlink and the fixtures both of
// them use (newRBFixture, rbWrite, rbTruncation) run on every platform this
// repo supports (Hard Constraint: Linux, macOS, Windows). The developer
// removes the `fix3seam` term entirely (this file becomes an ordinary,
// always-built *_test.go with no build-tag line at all) once
// setGrepPreOpenRootHook/runGrepPreOpenRootHook are added to grep_scope.go
// — there is no narrower platform tag to fall back to the way T2a's
// FIFO-swap file falls back to `unix`, because nothing else in this file
// is platform-restricted. Folding it into an existing file rather than
// keeping it standalone is also acceptable once the seam exists.
//
package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// installFix3PreOpenSwapHook mirrors installGrepScopeAncestorSwapHook's
// one-shot swap shape, driving the NEW grepPreOpenRootHook seam instead of
// the existing grepScopeStatOpenHook.
func installFix3PreOpenSwapHook(t *testing.T, dir, swapTo string) {
	t.Helper()
	var once sync.Once
	restore := setGrepPreOpenRootHook(func(hostPath string) {
		once.Do(func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Errorf("grepPreOpenRootHook: remove %q before swap: %v", dir, err)
				return
			}
			if err := os.Symlink(swapTo, dir); err != nil {
				t.Errorf("grepPreOpenRootHook: symlink %q -> %q: %v", dir, swapTo, err)
			}
		})
	})
	t.Cleanup(restore)
}

// TestFix3RedT1_GrepAbsoluteUnanchoredPreOpenSwap_OrdinaryTurn is T1's
// absolute-path scenario (A2/F2): an ORDINARY (not read-confined) grep with
// an absolute path OUTSIDE any mount and the workspace — so absoluteGrepRoot
// takes the unanchored `os.OpenRoot(parent)` branch, which the A1 fix never
// touches — whose PARENT is swapped for a symlink to a folder holding a
// distinct sentinel, BEFORE os.OpenRoot(parent) runs.
//
// ORACLE: no match line's content is the swapped-in folder's sentinel, and
// the result reports the root as lost/refused — never a silent, unflagged
// success reading the swapped-in folder's content (F2: the A2 SameFile
// check cannot observe this, since the swap lands before the open it
// checks around).
func TestFix3RedT1_GrepAbsoluteUnanchoredPreOpenSwap_OrdinaryTurn(t *testing.T) {
	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.ext, "anc", "hit.md"), "needle\n")
	swapTo := f.ws // a real, acyclic sibling directory
	rbWrite(t, filepath.Join(swapTo, "swapped.md"), "OUTSIDE-PREOPEN-SWAPPED\n")

	installFix3PreOpenSwapHook(t, filepath.Join(f.ext, "anc"), swapTo)

	res := f.grep.Execute(f.ctx, map[string]any{
		"pattern": "OUTSIDE-PREOPEN-SWAPPED",
		"path":    filepath.Join(f.ext, "anc", "hit.md"),
	})
	for _, line := range strings.Split(res.ForLLM, "\n") {
		m := rbContentHitLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if strings.Contains(line[len(m[0]):], "OUTSIDE-PREOPEN-SWAPPED") {
			t.Fatalf("F2: a pre-open ancestor swap leaked the swapped-in folder's content:\n%s", res.ForLLM)
		}
	}
	if !res.IsError && rbTruncation(res.ForLLM) != "root_lost" {
		t.Fatalf("F2: a pre-open ancestor swap must fail closed (refused or root_lost), got an unflagged result:\n%s", res.ForLLM)
	}
}

// TestFix3RedT1_GrepMountShorthandPreOpenSwap_OrdinaryTurn is T1's
// "ordinary-turn root kind" scenario: an ORDINARY grep using the mount-name
// SHORTHAND (mountScopeRoot, grep.go) with no sub-path, whose mount host
// root is swapped for a symlink to an outside folder BEFORE
// os.OpenRoot(m.HostPath) runs. mountScopeRoot carries NO SameFile check at
// all (only absoluteGrepRoot got the A2 fix), so this window is unguarded
// by both timing AND by any post-open verification.
//
// ORACLE: same as above — no content leak, and the result must not be an
// unflagged success.
func TestFix3RedT1_GrepMountShorthandPreOpenSwap_OrdinaryTurn(t *testing.T) {
	f := newRBFixture(t)
	swapTo := filepath.Join(f.ext, "mountswap")
	rbWrite(t, filepath.Join(swapTo, "s.md"), "OUTSIDE-MOUNT-SWAP\n")

	installFix3PreOpenSwapHook(t, f.mnt, swapTo)

	res := f.grep.Execute(f.ctx, map[string]any{
		"pattern": "OUTSIDE-MOUNT-SWAP",
		"path":    rbMountName,
	})
	for _, line := range strings.Split(res.ForLLM, "\n") {
		m := rbContentHitLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if strings.Contains(line[len(m[0]):], "OUTSIDE-MOUNT-SWAP") {
			t.Fatalf("T1 (mountScopeRoot): a pre-open mount-root swap leaked the swapped-in folder's content:\n%s", res.ForLLM)
		}
	}
	if !res.IsError && rbTruncation(res.ForLLM) != "root_lost" {
		t.Fatalf("T1 (mountScopeRoot): a pre-open mount-root swap must fail closed (refused or root_lost), got an unflagged result:\n%s", res.ForLLM)
	}
}
