// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitEvidence_Isolation_SelectRungReflectsSystemGitAvailability(t *testing.T) {
	if systemGitAvailable() {
		if got := SelectRung(); got != RungSystemGitWorktree {
			t.Fatalf("SelectRung() = %v with a system git binary on PATH, want RungSystemGitWorktree", got)
		}
	}

	t.Setenv("PATH", t.TempDir()) // a PATH with no `git` binary on it
	// go-git's local clone transport ALSO shells out to `git-upload-pack`
	// (see RungGoGitClone's doc comment), so with no system git at all,
	// the only rung actually deliverable is subdir.
	if got := SelectRung(); got != RungSubdir {
		t.Fatalf("SelectRung() = %v with no system git on PATH, want RungSubdir (go-git clone also needs system git)", got)
	}
}

func TestGitEvidence_Isolation_OpenAtSystemGitWorktreeRung(t *testing.T) {
	if !systemGitAvailable() {
		t.Skip("no system git binary on PATH in this environment")
	}
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "seed.txt", "seed content")
	if res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"seed.txt"}); err != nil || res.Skipped {
		t.Fatalf("seed commit: res=%+v err=%v", res, err)
	}

	target := filepath.Join(t.TempDir(), "isolated-stream")
	ic, err := OpenIsolatedCheckout(dir, target)
	if err != nil {
		t.Fatalf("OpenIsolatedCheckout: %v", err)
	}
	defer func() {
		if err := ic.Cleanup(); err != nil {
			t.Errorf("Cleanup: %v", err)
		}
	}()

	if ic.Rung != RungSystemGitWorktree {
		t.Fatalf("Rung = %v, want RungSystemGitWorktree", ic.Rung)
	}
	data, err := os.ReadFile(filepath.Join(ic.Dir, "seed.txt"))
	if err != nil {
		t.Fatalf("read seed.txt from isolated worktree: %v", err)
	}
	if string(data) != "seed content" {
		t.Fatalf("isolated worktree content = %q, want %q", data, "seed content")
	}

	if err := ic.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, statErr := os.Stat(ic.Dir); !os.IsNotExist(statErr) {
		t.Fatalf("isolated worktree dir still present after Cleanup: statErr=%v", statErr)
	}
	// Make Cleanup idempotent-safe for the deferred call above (git
	// worktree remove on an already-removed worktree errors, which is
	// fine/expected — swallow it there via a fresh no-op guard).
	ic.Cleanup = func() error { return nil }
}

func TestGitEvidence_Isolation_ForcedGoGitCloneRungProducesIndependentCopy(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "seed.txt", "seed content")
	if res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"seed.txt"}); err != nil || res.Skipped {
		t.Fatalf("seed commit: res=%+v err=%v", res, err)
	}

	target := filepath.Join(t.TempDir(), "cloned-stream")
	ic, err := OpenIsolatedCheckoutAtRung(dir, target, RungGoGitClone)
	if err != nil {
		t.Fatalf("OpenIsolatedCheckoutAtRung(RungGoGitClone): %v", err)
	}
	if ic.Rung != RungGoGitClone {
		t.Fatalf("Rung = %v, want RungGoGitClone", ic.Rung)
	}
	data, err := os.ReadFile(filepath.Join(ic.Dir, "seed.txt"))
	if err != nil {
		t.Fatalf("read seed.txt from clone: %v", err)
	}
	if string(data) != "seed content" {
		t.Fatalf("clone content = %q, want %q", data, "seed content")
	}

	// Prove independence: mutating the clone must not affect the source.
	if err := os.WriteFile(filepath.Join(ic.Dir, "seed.txt"), []byte("mutated in clone"), 0o600); err != nil {
		t.Fatalf("mutate clone: %v", err)
	}
	origData, err := os.ReadFile(filepath.Join(dir, "seed.txt"))
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(origData) != "seed content" {
		t.Fatalf("original was mutated via the clone: %q", origData)
	}

	if err := ic.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, statErr := os.Stat(ic.Dir); !os.IsNotExist(statErr) {
		t.Fatalf("clone dir still present after Cleanup")
	}
}

func TestGitEvidence_Isolation_ForcedSubdirRungSharesTheSameCheckout(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "shared.txt", "shared content")
	if res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"shared.txt"}); err != nil || res.Skipped {
		t.Fatalf("seed commit: res=%+v err=%v", res, err)
	}

	target := filepath.Join(dir, "streams", "exploratory-1")
	ic, err := OpenIsolatedCheckoutAtRung(dir, target, RungSubdir)
	if err != nil {
		t.Fatalf("OpenIsolatedCheckoutAtRung(RungSubdir): %v", err)
	}
	if ic.Rung != RungSubdir {
		t.Fatalf("Rung = %v, want RungSubdir", ic.Rung)
	}
	// It's a SUBDIRECTORY of the same checkout, not an isolated copy — the
	// sibling file is directly visible via the shared filesystem tree.
	if _, err := os.Stat(filepath.Join(dir, "shared.txt")); err != nil {
		t.Fatalf("shared.txt not visible from the base checkout: %v", err)
	}

	if err := ic.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("subdir checkout still present after Cleanup")
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("Cleanup of a subdir checkout must never remove the base dir: %v", statErr)
	}
}

func TestGitEvidence_Isolation_SubdirCleanupRefusesToRemoveBaseDir(t *testing.T) {
	_, dir := newTestRepo(t, WithClock(fixedClock()))

	ic, err := OpenIsolatedCheckoutAtRung(dir, dir, RungSubdir)
	if err != nil {
		t.Fatalf("OpenIsolatedCheckoutAtRung: %v", err)
	}
	if err := ic.Cleanup(); err == nil {
		t.Fatalf("Cleanup() targeting the base dir itself = nil error, want a refusal error")
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("base dir was removed despite the refusal: %v", statErr)
	}
}

func TestGitEvidence_Isolation_DegradesPastUnavailableSystemGit(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "seed.txt", "seed content")
	if res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"seed.txt"}); err != nil || res.Skipped {
		t.Fatalf("seed commit: res=%+v err=%v", res, err)
	}

	t.Setenv("PATH", t.TempDir()) // no `git` binary reachable
	target := filepath.Join(t.TempDir(), "degraded-stream")
	ic, err := OpenIsolatedCheckout(dir, target)
	if err != nil {
		t.Fatalf("OpenIsolatedCheckout with no system git: %v", err)
	}
	// Both the worktree AND go-git-clone rungs need a system `git` binary
	// (go-git's local clone transport shells out to `git-upload-pack`) —
	// with neither available, the ladder must bottom out at subdir, the
	// only rung with zero external dependency.
	if ic.Rung != RungSubdir {
		t.Fatalf("Rung = %v, want RungSubdir (both higher rungs need system git, which is unavailable)", ic.Rung)
	}
}

func TestGitEvidence_Isolation_RungStringIsStable(t *testing.T) {
	cases := map[Rung]string{
		RungSystemGitWorktree: "system_git_worktree",
		RungGoGitClone:        "go_git_clone",
		RungSubdir:            "subdir",
	}
	for rung, want := range cases {
		if got := rung.String(); got != want {
			t.Fatalf("Rung(%d).String() = %q, want %q", rung, got, want)
		}
	}
}
