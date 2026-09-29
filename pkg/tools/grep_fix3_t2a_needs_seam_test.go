// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 fix-round-3 RED pack — T2's first scenario (A3/F3 + N2, Opus
// security-lead review): needs a NEW production test seam that does not
// exist on 5a87aa6bd. Isolated behind this build tag so the default build
// (`-tags goolm,stdjson`, no `fix3seam`) stays green; this file does NOT
// compile until the seam below is added to grep_scope.go.
//
// # fix-round-3 RED-pack correction (team-lead decision, option A)
//
// This test's ORACLE was wrong as originally written and is corrected here.
// It used to require Open to return an ERROR after a swap landing between
// the kind check and grepGateFS.Open's own separate reopen. That encodes
// the WRONG target behaviour: the required fix is a SINGLE non-blocking
// open through os.Root whose already-verified file is RETURNED directly —
// mirroring pkg/tools/resolvepath.go::PathHandle.OpenRegularNonBlocking,
// which opens once (O_NONBLOCK on unix), stats the ALREADY-OPEN fd to
// check its kind, and returns that same fd when it is regular rather than
// closing it and opening a second, fresh handle. Under that shape a swap
// landing after the verified open is INVISIBLE, not merely refused: the
// caller's read goes through the fd the kind check already pinned, so
// whatever now occupies the NAME on disk (a FIFO or anything else) is
// simply never consulted again. The old oracle (an error is required)
// would have FAILED the correct fix and only accepted the exact
// discard-and-reopen shape that is the actual bug (N2, below).
//
// # Needed production seam: grepGatePreReopenHook (N2 — the reopen gap in
// the A3 fix)
//
// Finding N2: grepGateFS.Open (grep_scope.go) calls
// refuseNonRegularViaOpen(g.abs(name), name) to verify the entry's kind —
// which itself performs a real os.OpenFile + Stat + Close on the entry —
// and then, on success, calls g.fsys.Open(name) AGAIN: a SEPARATE, fresh
// open through the guarded fsys. The file the kind check just verified is
// discarded; the file the caller actually reads is a brand-new open that
// never inherited the verified kind. A swap landing in the gap between the
// two opens (regular -> FIFO) faces the guarded fsys's OWN Open, which has
// no O_NONBLOCK protection of its own — reintroducing the exact hang A3 was
// written to remove, just moved one open later.
//
// Needed signature (same pattern as the three existing seams in
// grep_scope.go — atomic.Pointer, nil in production, one-shot t.Cleanup
// restore in tests):
//
//	var grepGatePreReopenHook atomic.Pointer[func(name string)]
//	func setGrepGatePreReopenHook(fn func(name string)) (restore func())
//	func runGrepGatePreReopenHook(name string)
//
// # Whether this seam still has a live call site under the corrected fix
//
// The correct fix (single open, verified file returned) removes the
// SEPARATE g.fsys.Open(name) reopen this seam was originally meant to fire
// "immediately before" — so that exact call site may no longer exist at
// all once the fix lands. Both outcomes below are treated as PASSING by
// this test's rewritten oracle, and neither is preferred over the other:
//
//   - the developer keeps a call site for this hook (e.g. for future test
//     seams, or because some intermediate shape still benefits from it),
//     but no swap it drives ever results in a second, unverified open of
//     the swapped name reaching the guarded fsys; or
//   - the developer's fix has no reopen left to hook at all, so
//     runGrepGatePreReopenHook is simply never called in production and
//     the hook stays permanently dormant — which is itself the direct
//     consequence of having closed the window N2 names, not a gap in this
//     test.
//
// The developer changes this file's build tag from `fix3seam && unix` to
// plain `unix` when the seam symbols above are added to grep_scope.go
// (the `sync`/`syscall` swap machinery below is unix-only; `unix` alone,
// not the bare removal of all tags, is the correct target — this file does
// not become portable just because the seam exists). Folding this test
// into a normal `*_test.go` file is also acceptable once the seam is in
// place and the tag is no longer needed to keep the default build green.
//
//go:build unix

package tools

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// fix3T2aReopenSpy wraps the guarded fsys grepGateFS.Open hands its
// verified reads to (carveOutFS in production) and is the direct,
// non-inferred proof of N2's "no second open of the swapped name": once
// swapped reports true (the pre-reopen hook has already performed the
// regular-file -> FIFO swap), an Open call reaching THIS spy for the same
// name is exactly the discard-and-reopen bug N2 names, whether or not the
// corrected fix still calls the pre-reopen hook at all.
//
// It deliberately does NOT forward that one call to the real (now-FIFO)
// path — doing so would block this test's own reader goroutine on the
// FIFO, which is not needed to prove the property (the watchdog in the
// test below independently catches a hang reached by any OTHER route to
// the same bug); recording the attempt and refusing it is enough.
type fix3T2aReopenSpy struct {
	fs.FS
	name            string
	swapped         *atomic.Bool
	reopenedSwapped *atomic.Bool
}

func (s *fix3T2aReopenSpy) Open(name string) (fs.File, error) {
	if name == s.name && s.swapped.Load() {
		s.reopenedSwapped.Store(true)
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return s.FS.Open(name)
}

// TestFix3RedT2a_GrepGateFS_Open_NoReopen_FIFOSwapAfterKindCheck is T2's
// first scenario (A3/F3 + N2): an entry that IS regular when the kind
// check verifies it, but becomes a FIFO in the gap before grepGateFS.Open
// would otherwise perform a SEPARATE reopen through the guarded fsys.
//
// ORACLE (corrected — see the file header's "fix-round-3 RED-pack
// correction" note): the required fix returns the ALREADY-VERIFIED file
// directly, so the swap must be invisible to a successful read (the
// caller's fd was pinned before the swap landed) rather than surfaced as
// an error. Open must:
//   - return promptly either way — the 2s watchdog below fails the test on
//     a hang, since a swapped-in FIFO must never be opened (or reopened)
//     blocking;
//   - on success, return the ORIGINAL verified regular file's content —
//     never a wrong-content success, which would mean some route other
//     than the pinned, pre-swap fd supplied the bytes; a clean
//     (non-hanging) error is an acceptable outcome too, just not required;
//   - never let a second, unverified open of the (now-swapped) name reach
//     the guarded fsys — asserted directly via fix3T2aReopenSpy above, not
//     inferred from timing.
func TestFix3RedT2a_GrepGateFS_Open_NoReopen_FIFOSwapAfterKindCheck(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "entry.txt")
	original := []byte("needle\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	var swapped atomic.Bool
	var reopenedSwapped atomic.Bool
	var once sync.Once
	restore := setGrepGatePreReopenHook(func(name string) {
		once.Do(func() {
			if err := os.Remove(target); err != nil {
				t.Errorf("grepGatePreReopenHook: remove %q: %v", target, err)
				return
			}
			if err := syscall.Mkfifo(target, 0o600); err != nil {
				t.Errorf("grepGatePreReopenHook: mkfifo %q: %v", target, err)
				return
			}
			swapped.Store(true)
		})
	})
	t.Cleanup(restore)

	rawFsys := os.DirFS(dir)
	policy := fspolicy.FSPolicy{}
	spy := &fix3T2aReopenSpy{
		FS:              carveOutFS{fsys: rawFsys, root: dir, policy: policy},
		name:            "entry.txt",
		swapped:         &swapped,
		reopenedSwapped: &reopenedSwapped,
	}
	g := grepGateFS{
		fsys:   spy,
		raw:    rawFsys,
		root:   dir,
		policy: policy,
	}

	type openOutcome struct {
		content []byte
		err     error
	}
	done := make(chan openOutcome, 1)
	go func() {
		f, err := g.Open("entry.txt")
		if err != nil {
			done <- openOutcome{err: err}
			return
		}
		defer f.Close()
		content, readErr := io.ReadAll(f)
		done <- openOutcome{content: content, err: readErr}
	}()

	select {
	case res := <-done:
		if reopenedSwapped.Load() {
			t.Fatal("N2: after the kind check verified a regular file, grepGateFS.Open reopened the (now-swapped) name a SECOND time through the guarded fsys — the verified file was discarded in favor of an unverified reopen")
		}
		if res.err == nil && !bytes.Equal(res.content, original) {
			t.Fatalf("N2: Open succeeded but returned %q, want the ORIGINAL verified file's content %q — a discard-and-reopen shape reads whatever now occupies the name instead of the file the kind check actually verified", res.content, original)
		}
		// A clean, non-hanging error (e.g. a refusal because the on-disk
		// kind changed under an implementation that still re-checks it) is
		// an acceptable outcome too — only a hang or a silently
		// wrong-content success is the defect N2 names.
	case <-time.After(2 * time.Second):
		t.Fatal("N2: gate Open hung — a swapped-in FIFO must never be opened or blocked on, whether directly or via a discarded-and-reopened handle")
	}
}
