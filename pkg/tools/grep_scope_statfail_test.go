// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// 8-reviewer gate finding F1/S3 (silent-failure-hunter, security-lead):
// refuseNonRegular (grep_scope.go) treated ANY fs.Stat error — not just
// fs.ErrNotExist — as "nothing to refuse", so a Stat failure caused by
// anything other than the target being genuinely absent (a permission
// error on an ancestor, a transient I/O error the fs.FS implementation
// surfaces, a symlink race) silently ALLOWED the entry through to Open,
// exactly the fail-open shape a fs.FS gate must never have.
//
// Oracle: the fix instruction itself (not the pre-fix implementation):
// fs.ErrNotExist keeps today's behaviour (return nil; Open reports the
// absence truthfully); every OTHER Stat error must refuse — fail closed —
// with an *fs.PathError wrapping fs.ErrPermission, and the entry must never
// reach Open/content-read.
//
// # fix-round-3 RED-pack correction (team-lead decision, option A)
//
// Two things were wrong with this file's oracle as originally written, both
// corrected here rather than left for CHECK to find:
//
//  1. TestRefuseNonRegular_FailClosedOnOpaqueStatError used to call
//     refuseNonRegular directly. refuseNonRegular has ZERO production
//     callers as of 3d33a7950 (`grep -rn "refuseNonRegular(" pkg/tools/*.go`
//     outside _test.go files returns only its own definition) — it is dead
//     code, kept only for its own doc comment's account of the OLD
//     Stat-then-Open race window the A3 fix (refuseNonRegularViaOpen)
//     replaced. A green test that calls dead code directly proves nothing
//     about the shipped path; it was DELETED rather than retargeted,
//     because TestGrepGateFS_Open_StatErrorRefusesWithoutOpening below
//     (F1/S3's "T4") already asserts the identical property — refuse with
//     an *fs.PathError wrapping fs.ErrPermission, entry never opened —
//     through the LIVE call path (grepGateFS.Open ->
//     refuseNonRegularViaOpen) and with a REAL, deterministic OS-level
//     opaque error (a symlink loop, syscall.ELOOP) rather than a synthetic
//     fs.FS stub error against unreachable code. Retargeting would only
//     have reproduced T4's own assertions under a new name. Its sibling,
//     TestRefuseNonRegular_NotExistLeftToOpen (below), calls the same dead
//     helper and has the identical redundancy problem (the two
//     Open-level "StillOpens" tests already cover fs.ErrNotExist through
//     the live path) — it was OUT OF SCOPE for this pass and is left as a
//     reported finding, not fixed here.
//  2. TestGrepGateFS_Open_ELOOPDiscriminatingControl and
//     TestGrepGateFS_Open_StatNotExistStillOpens used to assert
//     `fsys.opened` / an `opened` spy flag directly — i.e. that the
//     guarded fsys's OWN Open call was reached, which encodes ONE
//     particular internal implementation (a Stat/verify step followed by a
//     SEPARATE reopen) rather than the property #920 actually specifies:
//     a genuinely-missing entry's absence surfaces to the CALLER
//     truthfully. The required fix for the related N2 finding
//     (grep_fix3_t2a_needs_seam_test.go) may remove that separate reopen
//     entirely for the regular-file case; asserting on whether some
//     particular internal Open fired would make these two tests describe
//     an implementation detail the fix is free to change, not the
//     contract it must keep. Rewritten below to assert only the
//     OBSERVABLE contract: the error grepGateFS.Open returns for
//     "gone.txt" satisfies errors.Is(err, fs.ErrNotExist) — true
//     regardless of whether one open or two internal opens produced it.
package tools

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// statFailFS is a minimal fs.FS stub — the one process-edge (the
// filesystem) refuseNonRegular actually depends on, per test-plan-and-write's
// mock-boundary rule: nothing else in the call is faked. It reports a
// caller-chosen Stat error for one named entry and records whether Open was
// ever called for that entry, so a test can assert the entry was never
// opened or read.
type statFailFS struct {
	fs.FS
	failName string
	statErr  error
	opened   bool
}

func (s *statFailFS) Stat(name string) (fs.FileInfo, error) {
	if name == s.failName {
		return nil, s.statErr
	}
	return fs.Stat(s.FS, name)
}

func (s *statFailFS) Open(name string) (fs.File, error) {
	if name == s.failName {
		s.opened = true
	}
	return s.FS.Open(name)
}

// newStatFailFixture seeds a real temp directory with one regular file
// (present, so a refusal can only be explained by the Stat failure itself,
// never by the entry being genuinely absent) and returns an os.DirFS rooted
// there for statFailFS to wrap.
func newStatFailFixture(t *testing.T) (dir string, base fs.FS) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "entry.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatalf("seed entry.txt: %v", err)
	}
	return dir, os.DirFS(dir)
}

// TestRefuseNonRegular_FailClosedOnOpaqueStatError was DELETED in the
// fix-round-3 RED-pack correction — see the file header's "fix-round-3
// RED-pack correction" note, item 1. It called refuseNonRegular, which has
// zero production callers on 3d33a7950; TestGrepGateFS_Open_StatErrorRefusesWithoutOpening
// below already proves the identical fail-closed-on-opaque-error property
// through the live call path with a real OS error, so this was redundant
// dead-code coverage, not a gap.

// TestRefuseNonRegular_NotExistLeftToOpen is the discriminating control:
// fs.ErrNotExist must keep the pre-fix behaviour exactly (nil — Open
// reports the absence itself, truthfully), never turn into a refusal.
func TestRefuseNonRegular_NotExistLeftToOpen(t *testing.T) {
	_, base := newStatFailFixture(t)
	stub := &statFailFS{FS: base, failName: "gone.txt", statErr: &fs.PathError{Op: "stat", Path: "gone.txt", Err: fs.ErrNotExist}}

	err := refuseNonRegular(stub, "gone.txt")
	if err != nil {
		t.Fatalf("fs.ErrNotExist must be left to Open (nil here), got %v", err)
	}
}

// fix3RedOpenSpy wraps a real fs.FS and records whether Open(name) was ever
// called for the entry under test — the "the entry must never reach
// Open/content-read" half of F1/S3's own oracle, restated by fix3's finding
// N4: a skipped integration test asserts nothing about the LIVE fail-closed
// branch, so this replaces the fs.Stat-shaped stub (no longer reachable
// after the A3 os.OpenFile rewrite) with a real on-disk fixture that drives
// os.OpenFile itself into a genuine, non-ENOENT error and a real fs.FS spy
// that proves the guarded fsys's own Open was never reached.
type fix3RedOpenSpy struct {
	fs.FS
	opened *bool
}

func (s fix3RedOpenSpy) Open(name string) (fs.File, error) {
	*s.opened = true
	return s.FS.Open(name)
}

// TestGrepGateFS_Open_StatErrorRefusesWithoutOpening is F1/S3's integration
// case, through the real production caller, rewritten for fix3/N4 to drive
// the LIVE (post-A3) fail-closed branch instead of an fs.FS stub the A3
// rewrite made unreachable.
//
// ORACLE: a symlink LOOP is a genuine, POSIX-standard, non-ENOENT open
// failure (syscall.ELOOP) that requires no fs.FS stub and no timing —
// os.OpenFile(hostAbs, regularReadOpenFlags(), 0) on a self-referential
// symlink fails deterministically on every platform this repo supports
// (Hard Constraint: Linux, macOS, Windows), independent of O_NONBLOCK (which
// only changes FIFO/device open blocking, not symlink-loop detection).
// refuseNonRegularViaOpen's own non-ENOENT branch (grep_scope.go) must wrap
// it as fs.ErrPermission and grepGateFS.Open must return that refusal
// WITHOUT ever calling g.fsys.Open(name) — the spy's opened flag is the
// direct, non-inferred proof of "never opened".
func TestGrepGateFS_Open_StatErrorRefusesWithoutOpening(t *testing.T) {
	dir := t.TempDir()
	loop := filepath.Join(dir, "loop")
	// A relative self-referential symlink: "loop" -> "loop", resolved
	// relative to its own directory, i.e. right back to itself.
	if err := os.Symlink("loop", loop); err != nil {
		t.Fatalf("create symlink loop: %v", err)
	}

	var opened bool
	fsys := fix3RedOpenSpy{FS: os.DirFS(dir), opened: &opened}
	g := grepGateFS{fsys: fsys, raw: os.DirFS(dir), root: dir, policy: fspolicy.FSPolicy{}}

	_, err := g.Open("loop")
	if err == nil {
		t.Fatal("N4: a symlink loop is neither a directory nor a regular file — grepGateFS.Open must refuse it, got nil (ALLOW)")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("N4: refusal must wrap fs.ErrPermission (the fail-closed branch), got %v", err)
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("N4: refusal must be an *fs.PathError, got %T: %v", err, err)
	}
	if opened {
		t.Fatal("N4: the guarded fsys's own Open was reached — the kind check must refuse BEFORE any content-read Open, not merely alongside it")
	}
}

// TestGrepGateFS_Open_ELOOPDiscriminatingControl is the discriminating
// control for the test above: a genuinely-missing entry must be reported to
// the CALLER as fs.ErrNotExist — the truthful absence — not turned into a
// refusal, exactly as TestGrepGateFS_Open_StatNotExistStillOpens already
// asserts through the fs.FS-stub shape. This restates it through the real
// os.OpenFile path so the ELOOP assertion above cannot be satisfied by a
// fail-closed-on-anything gate that also (wrongly) refuses a plain absence.
//
// Rewritten (fix-round-3 RED-pack correction, item 2): the pre-correction
// version asserted `opened` — that the guarded fsys's OWN Open call was
// reached — which encodes ONE particular internal shape (a verify step
// followed by a separate reopen) rather than the OBSERVABLE contract this
// test actually owns. Asserting on the returned error's identity instead
// is agnostic to whether the fix keeps, removes or reshapes any internal
// reopen.
func TestGrepGateFS_Open_ELOOPDiscriminatingControl(t *testing.T) {
	dir := t.TempDir()
	g := grepGateFS{fsys: os.DirFS(dir), raw: os.DirFS(dir), root: dir, policy: fspolicy.FSPolicy{}}

	_, err := g.Open("gone.txt")
	if err == nil {
		t.Fatal("control: gone.txt does not exist on disk; Open must report that, not succeed")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("control: a genuinely-missing entry must surface fs.ErrNotExist to the caller (the kind check must not swallow ENOENT behind a refusal), got %v", err)
	}
}

// TestGrepGateFS_Open_StatNotExistStillOpens is the same integration
// control via the fs.FS-stub shape: a genuinely-missing entry's absence
// reaches the caller truthfully as fs.ErrNotExist — unchanged behaviour.
//
// Rewritten (fix-round-3 RED-pack correction, item 2): the pre-correction
// version asserted `fsys.opened` directly — see this file's header note
// and the sibling rewrite above for why that is an internal-implementation
// assertion, not the contract this test owns.
func TestGrepGateFS_Open_StatNotExistStillOpens(t *testing.T) {
	dir, base := newStatFailFixture(t)
	raw := &statFailFS{FS: base, failName: "gone.txt", statErr: &fs.PathError{Op: "stat", Path: "gone.txt", Err: fs.ErrNotExist}}
	fsys := &statFailFS{FS: base, failName: "gone.txt"}
	g := grepGateFS{fsys: fsys, raw: raw, root: dir, policy: fspolicy.FSPolicy{}}

	_, err := g.Open("gone.txt")
	if err == nil {
		t.Fatal("gone.txt does not exist on disk either; Open must report that, not succeed")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an fs.ErrNotExist Stat result must surface fs.ErrNotExist to the caller (the truthful absence, not a refusal-shaped error), got %v", err)
	}
}
