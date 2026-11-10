// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 fix-round-3 RED pack — N3 from the Opus security-lead review of the
// A2 fix (commit 88740dc75).
//
// ORACLE (N3): sameFileCheck's OWN doc comment (grep_scope.go) states the
// contract in plain words: a Stat error inside sameFileCheck "is treated as
// 'check did not run' by the callers, never as a green (a missing check must
// not silently pass)." The two call sites in absoluteGrepRoot read:
//
//	if same, sErr := sameFileCheck(mr, m.HostPath); sErr == nil && !same {
//		return lost(...), 0, nil
//	}
//
// When sErr != nil, this condition is false, so the code falls straight
// through to the ordinary success path — the exact "silently pass" the
// doc comment forbids. The identity check could not be answered, and the
// caller treated that as "fine, proceed" instead of "could not verify,
// refuse" (fail open on an unanswerable check).
//
// Drive: the A2 seam (grepAbsolutePostOpenHook) fires AFTER os.OpenRoot(...)
// has bound its fd and BEFORE sameFileCheck. This test's hook renames the
// mount's host directory to a sibling path (no symlink involved — the
// directory itself moves) at that exact moment: the ALREADY-OPEN os.Root fd
// keeps working (fd-relative operations don't depend on the path that
// created them), so the walk can still reach the file through it, while
// os.Stat(anchor) on the ORIGINAL path now fails with a genuine, real
// os.ErrNotExist — sameFileCheck's own os.Stat(anchor) call errors, which is
// precisely the "check did not run" case its doc comment names. The correct
// behaviour is to treat that as unverified and refuse (root_lost); the
// current code has no such branch at all.
package tools

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestReadBoundary_GrepAbsoluteMountSameFileCheckFailsClosedOnStatError is
// N3's RED test: when sameFileCheck's own os.Stat(anchor) errors (the
// identity check could not be answered), absoluteGrepRoot must treat the
// root as lost — never proceed as if the check had passed.
func TestReadBoundary_GrepAbsoluteMountSameFileCheckFailsClosedOnStatError(t *testing.T) {
	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.mnt, "src", "b.md"), "needle\n")

	movedAway := f.mnt + "-relocated"
	var once sync.Once
	restore := setGrepAbsolutePostOpenHook(func(anchor string) {
		once.Do(func() {
			// Move the mount's own host directory itself out from under its
			// original path — the open *os.Root's fd stays bound and
			// functional (fd-relative ops do not re-resolve the path that
			// created them); os.Stat(anchor) on the vacated original path
			// now fails with a real, non-fabricated os.ErrNotExist.
			if err := os.Rename(f.mnt, movedAway); err != nil {
				t.Errorf("relocate mount dir %q -> %q: %v", f.mnt, movedAway, err)
			}
		})
	})
	t.Cleanup(func() {
		restore()
		// Best-effort: put it back so any later cleanup in f (t.TempDir
		// removal) can still find/remove everything under the original
		// TempDir tree; a leftover relocated copy is harmless either way.
		_ = os.Rename(movedAway, f.mnt)
	})

	res := f.grep.Execute(f.ctx, map[string]any{
		"pattern": "needle",
		"path":    filepath.Join(f.mnt, "src", "b.md"),
	})

	// N3 oracle: an unanswerable identity check must fail CLOSED — the
	// search must not report a plain, unqualified success as though the
	// check had passed. Acceptable "failed closed" shapes: a hard error, or
	// a truncated result whose reason is root_lost. Anything else —
	// especially a clean, non-truncated success carrying the "needle" hit —
	// means the code fell through the "sErr == nil && !same" guard exactly
	// as N3 describes.
	if res.IsError {
		return // refused outright: fails closed, acceptable.
	}
	if reason := rbTruncation(res.ForLLM); reason == "root_lost" {
		return // truncated as a lost root: fails closed, acceptable.
	}
	t.Fatalf("N3: sameFileCheck's own Stat error must fail closed (refused or root_lost) per its own doc "+
		"comment (\"a missing check must not silently pass\") — got a result that neither errored nor "+
		"reported root_lost:\n%s", res.ForLLM)
}
