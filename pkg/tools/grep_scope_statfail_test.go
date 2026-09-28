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

// TestGrepGateFS_Open_StatErrorRefusesWithoutOpening is F1/S3's integration
// case, through the real production caller: grepGateFS.Open consults
// refuseNonRegular(g.raw, name) BEFORE calling g.fsys.Open(name). A
// non-NotExist Stat error on the raw (kind-check) side must refuse without
// ever reaching the guarded fsys's Open — "grep must not open/read that
// entry" — and must not crash (a panic here would fail the test process).
func TestGrepGateFS_Open_StatErrorRefusesWithoutOpening(t *testing.T) {
	dir, base := newStatFailFixture(t)
	raw := &statFailFS{FS: base, failName: "entry.txt", statErr: errStatIOFailure}
	fsys := &statFailFS{FS: base, failName: "entry.txt"}
	g := grepGateFS{fsys: fsys, raw: raw, root: dir, policy: fspolicy.FSPolicy{}}

	f, err := g.Open("entry.txt")
	if err == nil {
		if f != nil {
			f.Close()
		}
		t.Fatal("F1/S3: grepGateFS.Open must refuse an entry whose Stat errored non-NotExist, got success")
	}
	if fsys.opened {
		t.Fatal("F1/S3: the guarded fsys's Open must never be called for an entry refused by the kind check")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("refusal must wrap fs.ErrPermission, got %v", err)
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
