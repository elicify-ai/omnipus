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
		if cleanupErr := ic.Cleanup(); cleanupErr != nil {
			t.Errorf("Cleanup: %v", cleanupErr)
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
	if writeErr := os.WriteFile(filepath.Join(ic.Dir, "seed.txt"), []byte("mutated in clone"), 0o600); writeErr != nil {
		t.Fatalf("mutate clone: %v", writeErr)
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

// --- Restore-at-commit (D13 Play-from-commit, #537) -------------------------

// seedTwoCommits builds an evidence repo with two boundary commits: c1
// records a.txt="v1" + sub/b.txt="bee" (task m-1); c2 records a.txt="v2" +
// c2only.txt (task m-2). Returns the repo dir and both hashes, so a
// restore-at-c1 must reproduce v1 + b.txt and must NOT contain c2only.txt —
// i.e. the tree matches the RECORDED commit, not HEAD.
func seedTwoCommits(t *testing.T) (dir, c1Hash, c2Hash string) {
	t.Helper()
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "a.txt", "v1")
	writeFile(t, dir, "sub/b.txt", "bee")
	res1, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "m-1"}, []string{"a.txt", "sub/b.txt"})
	if err != nil || res1.Skipped {
		t.Fatalf("commit c1: res=%+v err=%v", res1, err)
	}
	writeFile(t, dir, "a.txt", "v2")
	writeFile(t, dir, "c2only.txt", "only in c2")
	res2, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "m-2"}, []string{"a.txt", "c2only.txt"})
	if err != nil || res2.Skipped {
		t.Fatalf("commit c2: res=%+v err=%v", res2, err)
	}
	return dir, res1.Hash, res2.Hash
}

// assertTreeMatchesC1 verifies a restored tree matches the c1 commit exactly:
// a.txt is v1 (not HEAD's v2), sub/b.txt is present, c2only.txt is absent.
func assertTreeMatchesC1(t *testing.T, treeDir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(treeDir, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt from restored tree: %v", err)
	}
	if string(data) != "v1" {
		t.Errorf("restored a.txt = %q, want %q (the recorded commit, not HEAD's v2)", data, "v1")
	}
	data, err = os.ReadFile(filepath.Join(treeDir, "sub", "b.txt"))
	if err != nil {
		t.Fatalf("read sub/b.txt from restored tree: %v", err)
	}
	if string(data) != "bee" {
		t.Errorf("restored sub/b.txt = %q, want %q", data, "bee")
	}
	if _, statErr := os.Stat(filepath.Join(treeDir, "c2only.txt")); !os.IsNotExist(statErr) {
		t.Errorf("c2only.txt present in tree restored at c1 (statErr=%v) — tree does not match the recorded commit", statErr)
	}
}

// TestGitEvidence_Isolation_RestoreAtCommit_TreeMatchesRecordedCommit is the
// #537 regression test at the gitevidence layer: every ladder rung that this
// runtime can deliver must produce a tree matching the recorded commit.
func TestGitEvidence_Isolation_RestoreAtCommit_TreeMatchesRecordedCommit(t *testing.T) {
	rungs := []struct {
		name      string
		startRung Rung
		needsGit  bool
	}{
		{"full_ladder", RungSystemGitWorktree, false},
		{"system_git_worktree", RungSystemGitWorktree, true},
		{"go_git_clone", RungGoGitClone, true}, // local transport shells out to git-upload-pack
		{"subdir", RungSubdir, false},
	}
	for _, tc := range rungs {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsGit && !systemGitAvailable() {
				t.Skip("no system git binary on PATH in this environment")
			}
			// For every case but "full_ladder", pin the rung exactly: start
			// the ladder AT this rung and require delivery at-or-below it
			// only via degradation; the assertion below (tc.name !=
			// "full_ladder" && ic.Rung > tc.startRung) checks the delivered
			// rung explicitly.
			dir, c1Hash, _ := seedTwoCommits(t)
			target := filepath.Join(t.TempDir(), "restored")
			ic, err := OpenIsolatedCheckoutAtCommitRung(dir, target, c1Hash, tc.startRung)
			if err != nil {
				t.Fatalf("OpenIsolatedCheckoutAtCommitRung(%v): %v", tc.startRung, err)
			}
			if tc.name != "full_ladder" && ic.Rung > tc.startRung {
				t.Fatalf("Rung = %v, want <= %v (rungs above startRung are never attempted)", ic.Rung, tc.startRung)
			}
			defer func() {
				if cleanupErr := ic.Cleanup(); cleanupErr != nil {
					t.Errorf("Cleanup: %v", cleanupErr)
				}
			}()
			assertTreeMatchesC1(t, ic.Dir)
			// The evidence repo itself is untouched (HEAD content survives).
			data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
			if err != nil {
				t.Fatalf("read a.txt from base repo: %v", err)
			}
			if string(data) != "v2" {
				t.Errorf("base repo a.txt = %q, want %q — restore must not rewind the evidence repo", data, "v2")
			}
		})
	}
}

func TestGitEvidence_Isolation_RestoreAtCommit_RejectsMalformedHash(t *testing.T) {
	dir, _, _ := seedTwoCommits(t)
	target := filepath.Join(t.TempDir(), "restored")
	for _, bad := range []string{"", "deadbeef", "not-a-hash", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"} {
		if _, err := OpenIsolatedCheckoutAtCommit(dir, target, bad); err == nil {
			t.Errorf("OpenIsolatedCheckoutAtCommit(hash=%q) = nil error, want a validation error", bad)
		}
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Errorf("target dir materialized despite hash rejection: statErr=%v", statErr)
	}
}

func TestGitEvidence_Isolation_RestoreAtCommit_UnknownCommitFailsEveryRung(t *testing.T) {
	dir, _, _ := seedTwoCommits(t)
	target := filepath.Join(t.TempDir(), "restored")
	// A well-formed 40-hex hash that does not exist in this repo: every rung
	// opens fine but cannot restore, so the ladder must exhaust with an error
	// and leave no half-restored tree behind.
	missing := "0123456789abcdef0123456789abcdef01234567"
	if _, err := OpenIsolatedCheckoutAtCommit(dir, target, missing); err == nil {
		t.Fatalf("OpenIsolatedCheckoutAtCommit(unknown hash) = nil error, want ladder exhaustion")
	}
	if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
		t.Errorf("half-restored tree left behind at %s after ladder exhaustion: %d entries", target, len(entries))
	}
}

func TestGitEvidence_Isolation_RestoreAtCommit_RefusesNonEmptyTarget(t *testing.T) {
	dir, c1Hash, _ := seedTwoCommits(t)
	target := filepath.Join(t.TempDir(), "restored")
	writeFile(t, target, "stale.txt", "leftover from an interrupted materialization")
	if _, err := OpenIsolatedCheckoutAtCommit(dir, target, c1Hash); err == nil {
		t.Fatalf("OpenIsolatedCheckoutAtCommit into a non-empty target = nil error, want a refusal (a partial restore would not match the commit)")
	}
	// The stale content is untouched — the refusal is a no-op, not a wipe.
	data, err := os.ReadFile(filepath.Join(target, "stale.txt"))
	if err != nil || string(data) != "leftover from an interrupted materialization" {
		t.Errorf("stale target content modified by the refused restore: data=%q err=%v", data, err)
	}
}

func TestGitEvidence_Isolation_RemoveIsolatedCheckout(t *testing.T) {
	dir, c1Hash, _ := seedTwoCommits(t)
	target := filepath.Join(t.TempDir(), "restored")
	ic, err := OpenIsolatedCheckoutAtCommitRung(dir, target, c1Hash, RungSubdir)
	if err != nil {
		t.Fatalf("OpenIsolatedCheckoutAtCommitRung: %v", err)
	}
	ic.Cleanup = func() error { return nil } // removal under test is RemoveIsolatedCheckout
	assertTreeMatchesC1(t, target)

	if err := RemoveIsolatedCheckout(dir, target); err != nil {
		t.Fatalf("RemoveIsolatedCheckout: %v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Errorf("resume tree still present after RemoveIsolatedCheckout: statErr=%v", statErr)
	}
	// Idempotent: removing an already-absent checkout is not an error.
	if err := RemoveIsolatedCheckout(dir, target); err != nil {
		t.Errorf("RemoveIsolatedCheckout on an absent tree = %v, want nil (idempotent replace semantics)", err)
	}
	// The evidence repo survives teardown.
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr != nil {
		t.Errorf("evidence repo .git removed by checkout teardown: %v", statErr)
	}
}
