// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"strings"
	"testing"
)

func TestGitEvidence_Diff_WriteSetScopedExcludesOutOfScopeChanges(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))

	writeFile(t, dir, "src/main.go", "package main\nfunc main(){}\n")
	writeFile(t, dir, "media/screenshot.bin", "not-really-binary-but-large-enough")
	c1, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"src", "media"})
	if err != nil || c1.Skipped {
		t.Fatalf("seed commit: res=%+v err=%v", c1, err)
	}

	writeFile(t, dir, "src/main.go", "package main\nfunc main(){println(1)}\n")
	writeFile(t, dir, "media/screenshot.bin", "a completely different re-encoded blob")
	c2, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "2"}, []string{"src", "media"})
	if err != nil || c2.Skipped {
		t.Fatalf("second commit: res=%+v err=%v", c2, err)
	}

	ev, err := r.Diff(c1.Hash, c2.Hash, []string{"src"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if ev.Total != 2 {
		t.Fatalf("Total = %d, want 2 (both src/main.go and media/screenshot.bin changed)", ev.Total)
	}
	if ev.Matched != 1 {
		t.Fatalf("Matched = %d, want 1 (only src/main.go is in scope)", ev.Matched)
	}
	if len(ev.Files) != 1 || ev.Files[0].Path != "src/main.go" {
		t.Fatalf("Files = %+v, want exactly [src/main.go]", ev.Files)
	}
	if !strings.Contains(ev.Files[0].Patch, "println") {
		t.Fatalf("Patch = %q, want to contain the actual diff content", ev.Files[0].Patch)
	}
}

func TestGitEvidence_Diff_UnscopedReturnsEveryChange(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "a.txt", "a")
	writeFile(t, dir, "b.txt", "b")
	c1, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"a.txt", "b.txt"})
	if err != nil || c1.Skipped {
		t.Fatalf("seed commit: %+v %v", c1, err)
	}
	writeFile(t, dir, "a.txt", "a2")
	writeFile(t, dir, "b.txt", "b2")
	c2, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "2"}, []string{"a.txt", "b.txt"})
	if err != nil || c2.Skipped {
		t.Fatalf("second commit: %+v %v", c2, err)
	}

	ev, err := r.Diff(c1.Hash, c2.Hash, nil)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if ev.Matched != ev.Total || ev.Total != 2 {
		t.Fatalf("unscoped Diff: Matched=%d Total=%d, want both 2", ev.Matched, ev.Total)
	}
}

func TestGitEvidence_Diff_ClassifiesInsertModifyDelete(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "keep.txt", "v1")
	writeFile(t, dir, "remove.txt", "will go away")
	c1, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"keep.txt", "remove.txt"})
	if err != nil || c1.Skipped {
		t.Fatalf("seed commit: %+v %v", c1, err)
	}

	writeFile(t, dir, "keep.txt", "v2")
	deleteFile(t, dir, "remove.txt")
	writeFile(t, dir, "new.txt", "brand new")
	c2, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "2"}, []string{"keep.txt", "remove.txt", "new.txt"})
	if err != nil || c2.Skipped {
		t.Fatalf("second commit: %+v %v", c2, err)
	}

	ev, err := r.Diff(c1.Hash, c2.Hash, nil)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	kinds := map[string]string{}
	for _, f := range ev.Files {
		kinds[f.Path] = f.Kind
	}
	if kinds["keep.txt"] != "modify" {
		t.Fatalf("keep.txt kind = %q, want modify", kinds["keep.txt"])
	}
	if kinds["remove.txt"] != "delete" {
		t.Fatalf("remove.txt kind = %q, want delete", kinds["remove.txt"])
	}
	if kinds["new.txt"] != "insert" {
		t.Fatalf("new.txt kind = %q, want insert", kinds["new.txt"])
	}
}

func TestGitEvidence_Diff_UnknownCommitErrors(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "a.txt", "a")
	c1, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"a.txt"})
	if err != nil || c1.Skipped {
		t.Fatalf("seed commit: %+v %v", c1, err)
	}

	bogus := "0000000000000000000000000000000000000000"
	if _, err := r.Diff(c1.Hash, bogus, nil); err == nil {
		t.Fatalf("Diff against an unresolvable commit = nil error, want an error")
	}
}

// TestGitEvidence_AttemptDiff_HeadVsParent proves AttemptDiff returns the
// LATEST boundary commit's write-set-scoped changes (HEAD vs its parent) — the
// per-attempt diff evidence the idle/plan Judge feeds (G-3/G-15, FR-144).
func TestGitEvidence_AttemptDiff_HeadVsParent(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "a.txt", "v1")
	writeFile(t, dir, "b.txt", "v1")
	if _, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"a.txt", "b.txt"}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	// Second attempt: only a.txt changes.
	writeFile(t, dir, "a.txt", "v2-the-latest-attempt")
	c2, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "2"}, []string{"a.txt"})
	if err != nil || c2.Skipped {
		t.Fatalf("second commit: %+v %v", c2, err)
	}

	ev, err := r.AttemptDiff(nil)
	if err != nil {
		t.Fatalf("AttemptDiff: %v", err)
	}
	if ev.Matched != 1 || len(ev.Files) != 1 {
		t.Fatalf("AttemptDiff: Matched=%d len(Files)=%d, want 1 (only a.txt changed in the latest commit)", ev.Matched, len(ev.Files))
	}
	if ev.Files[0].Path != "a.txt" {
		t.Fatalf("Files[0].Path = %q, want a.txt", ev.Files[0].Path)
	}
	if !strings.Contains(ev.Files[0].Patch, "v2-the-latest-attempt") {
		t.Fatalf("Patch must contain the latest attempt's content, got %q", ev.Files[0].Patch)
	}
}

// TestGitEvidence_AttemptDiff_RootCommitAllInserts proves a root commit (no
// parent) diffs against the empty tree — every file surfaces as an insertion
// so the first attempt's full write-set is still fed to the Judge.
func TestGitEvidence_AttemptDiff_RootCommitAllInserts(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "a.txt", "first")
	writeFile(t, dir, "b.txt", "also first")
	if _, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"a.txt", "b.txt"}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	ev, err := r.AttemptDiff(nil)
	if err != nil {
		t.Fatalf("AttemptDiff root: %v", err)
	}
	if ev.Matched != 2 {
		t.Fatalf("root AttemptDiff: Matched=%d, want 2 (both files are insertions)", ev.Matched)
	}
	for _, f := range ev.Files {
		if f.Kind != "insert" {
			t.Errorf("root commit file %s kind = %q, want insert", f.Path, f.Kind)
		}
	}
}

// TestGitEvidence_AttemptDiff_UnbornReturnsEmpty proves an unborn HEAD (no
// commits yet) returns an empty DiffEvidence — nothing to diff; the Judge
// degrades to the transcript window + machine evidence.
func TestGitEvidence_AttemptDiff_UnbornReturnsEmpty(t *testing.T) {
	r, _ := newTestRepo(t, WithClock(fixedClock()))
	ev, err := r.AttemptDiff(nil)
	if err != nil {
		t.Fatalf("AttemptDiff unborn: %v", err)
	}
	if ev == nil || len(ev.Files) != 0 {
		t.Fatalf("unborn AttemptDiff: want empty DiffEvidence, got %+v", ev)
	}
}
