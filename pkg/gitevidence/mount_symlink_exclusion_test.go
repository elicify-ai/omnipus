// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestCommit_MountSymlink_NeverStagesOperatorContent is the spec FR-5.5
// regression: "A test MUST assert an evidence commit taken while a mount
// exists contains no file from inside the mount."
//
// A workspace mount (ADR-061 D4) materializes as a symlink work/<name> ->
// host_path. This test simulates that directly (no dependency on
// pkg/workspace — a plain os.Symlink is exactly what workspace.CreateMount
// produces) and proves two things that were VERIFIED empirically, not
// assumed:
//
//  1. go-git's own status walk treats the symlink as one opaque, atomic
//     entry and never descends into it — a write-set path reaching INSIDE
//     the mount is never even reported dirty, so it can never be staged.
//  2. A write-set entry naming the mount's own top-level name (the realistic
//     shape if a caller ever declares "the directory I touched" rather than
//     individual files) is excluded by the Lstat guard added to Commit,
//     rather than erroring the whole boundary commit or reading through the
//     link.
func TestCommit_MountSymlink_NeverStagesOperatorContent(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))

	// The "operator's real repository" — entirely outside the evidence
	// repo's own directory tree.
	hostDir := t.TempDir()
	operatorFile := filepath.Join(hostDir, "operator-secret-plan.txt")
	if err := os.WriteFile(operatorFile, []byte("OPERATOR CONTENT — must never enter Omnipus's evidence history"), 0o600); err != nil {
		t.Fatalf("seed host dir: %v", err)
	}

	mountLink := filepath.Join(dir, "client-repo")
	if err := os.Symlink(hostDir, mountLink); err != nil {
		t.Fatalf("symlink mount: %v", err)
	}

	// A legitimate, Omnipus-owned file changed in the same boundary.
	writeFile(t, dir, "notes.md", "agent's own notes")

	t.Run("write-set names a path INSIDE the mount", func(t *testing.T) {
		res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{
			"client-repo/operator-secret-plan.txt",
			"notes.md",
		})
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if contains(res.Committed, "client-repo/operator-secret-plan.txt") {
			t.Fatalf("Committed = %v, must NEVER include a path inside the mount", res.Committed)
		}
		if !contains(res.Committed, "notes.md") {
			t.Fatalf("Committed = %v, want notes.md still committed (unrelated to the mount)", res.Committed)
		}
	})

	t.Run("write-set names the mount's own top-level entry", func(t *testing.T) {
		// A second, otherwise-untouched-by-the-first-commit legitimate file,
		// so this sub-test has something real to prove still commits.
		writeFile(t, dir, "notes2.md", "more notes")
		res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t2"}, []string{
			"client-repo",
			"notes2.md",
		})
		if err != nil {
			t.Fatalf("Commit must not error just because the write-set named a mount's top-level symlink: %v", err)
		}
		if contains(res.Committed, "client-repo") {
			t.Fatalf("Committed = %v, must NEVER commit the mount symlink/its target as a tracked entry", res.Committed)
		}
		if !contains(res.ExcludedSymlink, "client-repo") {
			t.Fatalf("ExcludedSymlink = %v, want it to record that the mount entry was excluded", res.ExcludedSymlink)
		}
		if !contains(res.Committed, "notes2.md") {
			t.Fatalf("Committed = %v, want notes2.md still committed (unrelated to the mount)", res.Committed)
		}
	})

	// Belt and suspenders: inspect the actual commit tree(s) and assert no
	// blob anywhere in history equals the operator's file content, and no
	// path under "client-repo/" was ever tracked.
	head, err := r.Head()
	if err != nil || head == "" {
		t.Fatalf("Head: %q, %v", head, err)
	}
	commitObj, err := r.repo.CommitObject(plumbing.NewHash(head))
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	tree, err := commitObj.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	walkErr := tree.Files().ForEach(func(f *object.File) error {
		name := filepath.ToSlash(f.Name)
		if name == "client-repo" || strings.HasPrefix(name, "client-repo/") {
			t.Fatalf("commit tree contains a path from inside the mount: %s", f.Name)
		}
		content, cerr := f.Contents()
		if cerr == nil && content == "OPERATOR CONTENT — must never enter Omnipus's evidence history" {
			t.Fatalf("commit tree contains the operator's file content under path %s", f.Name)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk commit tree: %v", walkErr)
	}
}
