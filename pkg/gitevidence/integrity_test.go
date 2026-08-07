// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestGitEvidence_Integrity_EmptyLastKnownAlwaysOK(t *testing.T) {
	r, _ := newTestRepo(t, WithClock(fixedClock()))
	status, err := r.CheckIntegrity("")
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if !status.OK {
		t.Fatalf("status = %+v, want OK (no prior commit on record, unborn repo)", status)
	}
}

func TestGitEvidence_Integrity_OKWhenHeadUnchanged(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "a.txt", "a")
	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"a.txt"})
	if err != nil || res.Skipped {
		t.Fatalf("seed commit: %+v %v", res, err)
	}

	status, err := r.CheckIntegrity(res.Hash)
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if !status.OK {
		t.Fatalf("status = %+v, want OK", status)
	}
}

func TestGitEvidence_Integrity_OKWhenLastKnownIsAncestorOfHead(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "a.txt", "v1")
	c1, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"a.txt"})
	if err != nil || c1.Skipped {
		t.Fatalf("commit 1: %+v %v", c1, err)
	}
	writeFile(t, dir, "a.txt", "v2")
	c2, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "2"}, []string{"a.txt"})
	if err != nil || c2.Skipped {
		t.Fatalf("commit 2: %+v %v", c2, err)
	}

	status, err := r.CheckIntegrity(c1.Hash)
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if !status.OK {
		t.Fatalf("status = %+v, want OK (c1 is an ancestor of current HEAD c2 — the engine's own serialized commits advanced history normally)", status)
	}
	if status.HeadHash != c2.Hash {
		t.Fatalf("HeadHash = %q, want %q", status.HeadHash, c2.Hash)
	}
}

// Simulates the exact D17 caveat-3 bypass the spike proved empirically: the
// repository's history gets rewritten out-of-band (there, via the real git
// binary doing `commit --amend`; here, via direct plumbing manipulation to
// stay hermetic) so the engine's last-known commit is no longer an
// ancestor of HEAD.
func TestGitEvidence_Integrity_LostOnOutOfBandHistoryRewrite(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "a.txt", "v1")
	c1, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"a.txt"})
	if err != nil || c1.Skipped {
		t.Fatalf("commit 1: %+v %v", c1, err)
	}

	rewriteHeadAsOrphan(t, r, "AMENDED out-of-band, bypassing the engine")

	status, err := r.CheckIntegrity(c1.Hash)
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if status.OK {
		t.Fatalf("status = %+v, want NOT OK after an out-of-band rewrite", status)
	}
	if !strings.Contains(status.Reason, "evidence integrity lost") {
		t.Fatalf("Reason = %q, want to contain the surfaced phrase %q", status.Reason, "evidence integrity lost")
	}
}

func TestGitEvidence_Integrity_LostWhenLastKnownHashUnresolvable(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "a.txt", "v1")
	c1, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"a.txt"})
	if err != nil || c1.Skipped {
		t.Fatalf("commit 1: %+v %v", c1, err)
	}

	bogus := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	status, err := r.CheckIntegrity(bogus)
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if status.OK {
		t.Fatalf("status = %+v, want NOT OK for an unresolvable last-known commit", status)
	}
}

// rewriteHeadAsOrphan builds a brand-new, parentless commit object (reusing
// an existing tree so it's a valid commit) and force-points the current
// branch reference at it directly via the repository's object/reference
// storer — bypassing Repo.Commit entirely, exactly as an out-of-band `git`
// invocation would.
func rewriteHeadAsOrphan(t *testing.T, r *Repo, message string) plumbing.Hash {
	t.Helper()
	headRef, err := r.repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	oldCommit, err := r.repo.CommitObject(headRef.Hash())
	if err != nil {
		t.Fatalf("load old HEAD commit: %v", err)
	}

	orphan := &object.Commit{
		Author:    object.Signature{Name: "out-of-band", Email: "oob@example.com", When: time.Now()},
		Committer: object.Signature{Name: "out-of-band", Email: "oob@example.com", When: time.Now()},
		Message:   message,
		TreeHash:  oldCommit.TreeHash,
		// No ParentHashes: an orphan commit, not a descendant of headRef.
	}
	obj := r.repo.Storer.NewEncodedObject()
	if encodeErr := orphan.Encode(obj); encodeErr != nil {
		t.Fatalf("encode orphan commit: %v", encodeErr)
	}
	hash, err := r.repo.Storer.SetEncodedObject(obj)
	if err != nil {
		t.Fatalf("store orphan commit: %v", err)
	}
	newRef := plumbing.NewHashReference(headRef.Name(), hash)
	if err := r.repo.Storer.SetReference(newRef); err != nil {
		t.Fatalf("force HEAD to orphan commit: %v", err)
	}
	return hash
}
