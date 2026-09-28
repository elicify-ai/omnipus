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
package tools

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// errStatIOFailure is an opaque fs.FS-specific Stat failure — deliberately
// neither fs.ErrNotExist nor fs.ErrPermission — exercising the "ANY other
// Stat error" branch the fix requires, not merely the one error type the
// pre-fix code already happened to special-case in its callers.
var errStatIOFailure = errors.New("stub: transient stat I/O failure")

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

// TestRefuseNonRegular_FailClosedOnOpaqueStatError is F1/S3's direct-call
// case: a Stat error that is not fs.ErrNotExist must refuse, not allow.
func TestRefuseNonRegular_FailClosedOnOpaqueStatError(t *testing.T) {
	_, base := newStatFailFixture(t)
	stub := &statFailFS{FS: base, failName: "entry.txt", statErr: errStatIOFailure}

	err := refuseNonRegular(stub, "entry.txt")
	if err == nil {
		t.Fatal("F1/S3: a non-NotExist Stat error must refuse (fail closed), got nil (ALLOW)")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("refusal must wrap fs.ErrPermission, got %v", err)
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("refusal must be an *fs.PathError, got %T: %v", err, err)
	}
	if pathErr.Op != "open" || pathErr.Path != "entry.txt" {
		t.Fatalf("PathError = {Op:%q Path:%q}, want {Op:\"open\" Path:\"entry.txt\"}", pathErr.Op, pathErr.Path)
	}
	if !strings.Contains(err.Error(), errStatIOFailure.Error()) {
		t.Fatalf("refusal reason must surface WHY the Stat failed (the original error), got: %v", err)
	}
}

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
// control for the test above: a genuinely-missing entry (fs.ErrNotExist)
// must still reach the guarded fsys's Open, exactly as
// TestGrepGateFS_Open_StatNotExistStillOpens already asserts through the
// fs.FS-stub shape — this restates it through the real os.OpenFile path so
// the ELOOP assertion above cannot be satisfied by a fail-closed-on-anything
// gate that also (wrongly) refuses a plain absence.
func TestGrepGateFS_Open_ELOOPDiscriminatingControl(t *testing.T) {
	dir := t.TempDir()
	var opened bool
	fsys := fix3RedOpenSpy{FS: os.DirFS(dir), opened: &opened}
	g := grepGateFS{fsys: fsys, raw: os.DirFS(dir), root: dir, policy: fspolicy.FSPolicy{}}

	_, err := g.Open("gone.txt")
	if err == nil {
		t.Fatal("control: gone.txt does not exist on disk; Open must report that, not succeed")
	}
	if !opened {
		t.Fatal("control: a genuinely-missing entry must still reach the guarded fsys's Open (unchanged behaviour) — the kind check must not swallow ENOENT")
	}
}

// TestGrepGateFS_Open_StatNotExistStillOpens is the same integration
// control: a genuinely-missing entry still reaches the guarded fsys's Open,
// which reports the absence itself — unchanged behaviour.
func TestGrepGateFS_Open_StatNotExistStillOpens(t *testing.T) {
	dir, base := newStatFailFixture(t)
	raw := &statFailFS{FS: base, failName: "gone.txt", statErr: &fs.PathError{Op: "stat", Path: "gone.txt", Err: fs.ErrNotExist}}
	fsys := &statFailFS{FS: base, failName: "gone.txt"}
	g := grepGateFS{fsys: fsys, raw: raw, root: dir, policy: fspolicy.FSPolicy{}}

	_, err := g.Open("gone.txt")
	if err == nil {
		t.Fatal("gone.txt does not exist on disk either; Open must report that, not succeed")
	}
	if !fsys.opened {
		t.Fatal("an fs.ErrNotExist Stat result must still reach the guarded fsys's Open (unchanged behaviour)")
	}
}
