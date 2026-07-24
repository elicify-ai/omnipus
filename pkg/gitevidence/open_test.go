// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGitEvidence_Open_InitializesFreshRepoIdempotently(t *testing.T) {
	dir := t.TempDir()

	r1, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open() = %v, want nil error", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr != nil {
		t.Fatalf(".git not created: %v", statErr)
	}
	if !hasMarker(dir) {
		t.Fatalf("expected omnipus marker after first Open()")
	}
	head, err := r1.Head()
	if err != nil {
		t.Fatalf("Head() on a fresh repo = %v, want nil error", err)
	}
	if head != "" {
		t.Fatalf("Head() on a fresh (commit-less) repo = %q, want empty (unborn HEAD)", head)
	}

	// Second Open() on the SAME dir must succeed by reopening (not
	// re-initializing) — the idempotency FR-151 requires. The no-op redactor
	// satisfies Commit's MIN-5 fail-closed guard (MAJOR-3) since this test
	// exercises the reopen mechanics, not secret scanning.
	r2, err := Open(dir, WithRedactor(noOpRedactor))
	if err != nil {
		t.Fatalf("second Open() (idempotent reopen) = %v, want nil error", err)
	}
	if r2.Dir() != r1.Dir() {
		t.Fatalf("reopened repo dir = %q, want %q", r2.Dir(), r1.Dir())
	}

	// Prove the reopen is functional, not just error-free: commit through
	// r2 and confirm r1's underlying git state reflects it too.
	writeFile(t, dir, "hello.txt", "hi")
	res, err := r2.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"hello.txt"})
	if err != nil {
		t.Fatalf("Commit via reopened repo: %v", err)
	}
	if res.Skipped {
		t.Fatalf("Commit via reopened repo unexpectedly skipped: %s", res.SkipReason)
	}
	head1, err := r1.Head()
	if err != nil {
		t.Fatalf("Head() via original handle after reopen-commit: %v", err)
	}
	if head1 != res.Hash {
		t.Fatalf("original handle's Head() = %q after commit via reopened handle, want %q (same on-disk repo)", head1, res.Hash)
	}
}

func TestGitEvidence_Open_NestedRepoAtDirDegradesWithoutTouchingIt(t *testing.T) {
	dir := t.TempDir()
	// Simulate a pre-existing user repo occupying the target dir: a .git
	// directory with no Omnipus marker.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatalf("simulate user .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("simulate user .git/HEAD: %v", err)
	}

	_, err := Open(dir)
	if err == nil {
		t.Fatalf("Open() on a dir with a pre-existing non-Omnipus .git = nil error, want ErrNestedRepo")
	}
	if !errors.Is(err, ErrNestedRepo) {
		t.Fatalf("Open() error = %v, want wrapping ErrNestedRepo", err)
	}
	if hasMarker(dir) {
		t.Fatalf("Open() wrote an Omnipus marker into a pre-existing user repo — must never touch it")
	}
	// The user's HEAD file must be untouched.
	data, readErr := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if readErr != nil {
		t.Fatalf("read user HEAD: %v", readErr)
	}
	if string(data) != "ref: refs/heads/main\n" {
		t.Fatalf("user .git/HEAD was modified: %q", string(data))
	}
}

func TestGitEvidence_Open_NestedRepoAboveDirDegrades(t *testing.T) {
	root := t.TempDir()
	// A user repo at root (e.g. OMNIPUS_HOME itself lives inside the
	// operator's own git-tracked project).
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatalf("simulate ancestor .git: %v", err)
	}
	workDir := filepath.Join(root, "workspaces", "ws1", "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}

	_, err := Open(workDir)
	if err == nil {
		t.Fatalf("Open() under an ancestor user repo = nil error, want ErrNestedRepo")
	}
	if !errors.Is(err, ErrNestedRepo) {
		t.Fatalf("Open() error = %v, want wrapping ErrNestedRepo", err)
	}
	if _, statErr := os.Stat(filepath.Join(workDir, ".git")); !os.IsNotExist(statErr) {
		t.Fatalf("Open() initialized a .git under a nested-repo work dir; must skip entirely")
	}
}

func TestGitEvidence_Open_SiblingDirWithoutAncestorGitIsNotNested(t *testing.T) {
	root := t.TempDir()
	// A .git that is a SIBLING, not an ancestor, must not trip nested
	// detection (regression guard against an overly broad ancestor walk).
	if err := os.MkdirAll(filepath.Join(root, "sibling", ".git"), 0o700); err != nil {
		t.Fatalf("simulate sibling .git: %v", err)
	}
	workDir := filepath.Join(root, "workspaces", "ws1", "work")

	if _, err := Open(workDir); err != nil {
		t.Fatalf("Open() falsely detected a sibling .git as nested: %v", err)
	}
}
