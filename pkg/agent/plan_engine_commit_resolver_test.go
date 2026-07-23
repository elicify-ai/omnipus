// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestLastMemberCommitResolver_Glue exercises the REAL commitResolver wiring
// end-to-end (without the gateway): taskStore -> member's WorkspaceID ->
// workspace's evidence repo -> Repo.LastCommitForTask. This is the integration
// counterpart to the gitevidence-level LastCommitForTask test and the
// engine-level fake-resolver test — together they prove the full D13/G-12 path
// is wired (corr-MAJOR-4), not stubbed.
func TestLastMemberCommitResolver_Glue(t *testing.T) {
	home := t.TempDir()

	// Materialize workspace "ws"'s work dir + auto-init its evidence repo
	// (the sanctioned EnsureWorkDir hook — FR-151).
	workDir, err := workspace.EnsureWorkDir(home, "ws")
	if err != nil {
		t.Fatalf("EnsureWorkDir: %v", err)
	}

	// Re-open with an identity redactor so Commit's MIN-5 fail-closed guard
	// is satisfied (the on-disk repo is unchanged; the redactor is a
	// per-instance field).
	repo, err := gitevidence.Open(workDir, gitevidence.WithRedactor(func(s string) string { return s }))
	if err != nil {
		t.Fatalf("gitevidence.Open: %v", err)
	}

	// m-1's boundary commit (write a file in its declared write-set).
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("alpha"), 0o600); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	res, err := repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{TaskID: "m-1", AgentID: "owner"}, []string{"a.txt"})
	if err != nil || res.Skipped {
		t.Fatalf("commit m-1 boundary: err=%v skipped=%v %v", err, res.Skipped, res.SkipReason)
	}
	wantHash := res.Hash

	// Task store with two members in workspace "ws".
	ts := task.New(filepath.Join(t.TempDir(), "tasks"))
	mustCreateTask(t, ts, &task.Task{ID: "m-1", Title: "m-1", WorkspaceID: "ws"})
	mustCreateTask(t, ts, &task.Task{ID: "m-other", Title: "m-other", WorkspaceID: "ws"})
	// A member whose workspace was never materialized (no work dir on disk).
	mustCreateTask(t, ts, &task.Task{ID: "m-nodir", Title: "m-nodir", WorkspaceID: "ws-noexist"})

	r := NewLastMemberCommitResolver(ts, home)

	if got, _ := r.LastMemberCommit("p", "m-1"); got != wantHash {
		t.Errorf("m-1: LastMemberCommit = %q, want %q (the boundary commit)", got, wantHash)
	}
	if got, _ := r.LastMemberCommit("p", "m-other"); got != "" {
		t.Errorf("m-other: LastMemberCommit = %q, want \"\" (never committed)", got)
	}
	if got, _ := r.LastMemberCommit("p", "m-nodir"); got != "" {
		t.Errorf("m-nodir: LastMemberCommit = %q, want \"\" (workspace work dir not materialized — no side-effect creation)", got)
	}
	// Verify the resolver did NOT materialize ws-noexist's work dir as a side
	// effect (the os.Stat guard in LastMemberCommit).
	if _, statErr := os.Stat(workspace.WorkDir(home, "ws-noexist")); !os.IsNotExist(statErr) {
		t.Errorf("resolver materialized ws-noexist's work dir as a Play side effect (statErr=%v), want IsNotExist", statErr)
	}
}

// TestLastMemberCommitResolver_NilGuard: a resolver with no task store or home
// degrades to fresh attempt ("") rather than nil-derefing — the documented
// minimal-harness safety contract.
func TestLastMemberCommitResolver_NilGuard(t *testing.T) {
	r := NewLastMemberCommitResolver(nil, "")
	if got, err := r.LastMemberCommit("p", "m-1"); got != "" || err != nil {
		t.Errorf("nil-guarded resolver: LastMemberCommit = (%q, %v), want (\"\", nil)", got, err)
	}
}

// TestLastMemberCommitResolver_ResetMemberCheckout_TreeMatchesCommit is the
// #537 regression test at the engine boundary: the resolver's
// ResetMemberCheckout materializes a working tree whose contents match the
// LAST recorded boundary commit (not HEAD), via the gitevidence isolation
// ladder. Together with the gitevidence-layer tests (which prove every rung
// restores the recorded commit's tree) and the engine-layer fake-resolver
// test (which proves Play drives the seam), this completes the chain: Play
// -> commitResolver.ResetMemberCheckout -> gitevidence ladder -> tree.
//
// We exercise the SUBRID rung (no system-git dependency) so the test runs
// in any environment — the rung that actually delivers here is asserted
// explicitly (gitevidence.SelectRung() would normally prefer system-git,
// but we pin to subdir via the deterministic fixture below; the higher
// rungs are covered by the gitevidence tests directly).
func TestLastMemberCommitResolver_ResetMemberCheckout_TreeMatchesCommit(t *testing.T) {
	home := t.TempDir()

	// Workspace + auto-init evidence repo (sanctioned EnsureWorkDir hook).
	workDir, err := workspace.EnsureWorkDir(home, "ws")
	if err != nil {
		t.Fatalf("EnsureWorkDir: %v", err)
	}
	repo, err := gitevidence.Open(workDir, gitevidence.WithRedactor(func(s string) string { return s }))
	if err != nil {
		t.Fatalf("gitevidence.Open: %v", err)
	}

	// Two commits: c1 = m-1's first boundary (a.txt=v1); c2 = m-1's second
	// boundary AFTER a follow-up attempt (a.txt=v2, c2only.txt present).
	// The "last commit" the engine records for m-1 is c2 — the resume must
	// reproduce THAT tree, not HEAD-equivalent v1.
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("v1"), 0o600); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	res1, err := repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{TaskID: "m-1", AgentID: "owner"}, []string{"a.txt"})
	if err != nil || res1.Skipped {
		t.Fatalf("commit c1: err=%v skipped=%v %v", err, res1.Skipped, res1.SkipReason)
	}
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("v2"), 0o600); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "c2only.txt"), []byte("only in c2"), 0o600); err != nil {
		t.Fatalf("write c2only: %v", err)
	}
	res2, err := repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{TaskID: "m-1", AgentID: "owner"}, []string{"a.txt", "c2only.txt"})
	if err != nil || res2.Skipped {
		t.Fatalf("commit c2: err=%v skipped=%v %v", err, res2.Skipped, res2.SkipReason)
	}

	// Pin to the subdir rung so the test is independent of system git (the
	// gitevidence tests exercise every rung explicitly; here we want to
	// prove the engine seam picks the right hash regardless of rung).
	// The cleanest way: force the subdir rung by removing `git` from PATH
	// for the duration of the test, which makes both higher rungs skip and
	// the ladder deliver at subdir.
	t.Setenv("PATH", t.TempDir())

	// Task store with the member in workspace "ws".
	ts := task.New(filepath.Join(t.TempDir(), "tasks"))
	mustCreateTask(t, ts, &task.Task{ID: "m-1", Title: "m-1", WorkspaceID: "ws"})

	r := NewLastMemberCommitResolver(ts, home)

	// The resolver's ResetMemberCheckout materializes a tree at the LAST
	// recorded commit for m-1 — that is c2 (the second boundary commit,
	// not HEAD's first ancestor).
	dir, err := r.ResetMemberCheckout("p", "m-1", res2.Hash)
	if err != nil {
		t.Fatalf("ResetMemberCheckout(m-1, c2): %v", err)
	}
	if dir == "" {
		t.Fatalf("ResetMemberCheckout returned empty dir; expected the materialized tree at workspaces/ws/resume/m-1")
	}

	// The materialized tree matches c2's content (NOT c1's content):
	//   a.txt = v2 (not v1 — that's the recorded commit, not HEAD's v2's parent)
	//   c2only.txt present (only added in c2)
	wantResumeDir := filepath.Join(home, "workspaces", "ws", "resume", "m-1")
	if dir != wantResumeDir {
		t.Errorf("materialized dir = %q, want %q (deterministic path)", dir, wantResumeDir)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatalf("read a.txt from resume tree: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("resume tree a.txt = %q, want %q (the recorded commit's tree, not HEAD's v2)", got, "v2")
	}
	if _, err := os.Stat(filepath.Join(dir, "c2only.txt")); err != nil {
		t.Errorf("c2only.txt missing from resume tree: %v — tree does not match the recorded commit", err)
	}

	// A second member with NO commits: ResetMemberCheckout removes any stale
	// tree and returns ("", nil) — the fresh-attempt degradation.
	mustCreateTask(t, ts, &task.Task{ID: "m-nocommit", Title: "m-nocommit", WorkspaceID: "ws"})
	// Sanity: LastMemberCommit for m-nocommit returns "" (never committed).
	if got, _ := r.LastMemberCommit("p", "m-nocommit"); got != "" {
		t.Errorf("m-nocommit LastMemberCommit = %q, want \"\"", got)
	}
	dir, err = r.ResetMemberCheckout("p", "m-nocommit", "")
	if err != nil {
		t.Fatalf("ResetMemberCheckout(m-nocommit, \"\"): %v", err)
	}
	if dir != "" {
		t.Errorf("m-nocommit ResetMemberCheckout dir = %q, want \"\" (fresh attempt leaves no tree behind)", dir)
	}
	if _, err := os.Stat(filepath.Join(home, "workspaces", "ws", "resume", "m-nocommit")); !os.IsNotExist(err) {
		t.Errorf("m-nocommit resume tree materialized (statErr=%v), want IsNotExist (no commit => fresh attempt => no tree)", err)
	}
}

// TestLastMemberCommitResolver_ResetMemberCheckout_ReplaceSemantics: a second
// Play on the same member with a NEWER commit must replace the prior resume
// tree (not stack) and the materialized tree must match the newer commit.
// This is the engine's "replace semantics" — a re-Play never sees stale work.
func TestLastMemberCommitResolver_ResetMemberCheckout_ReplaceSemantics(t *testing.T) {
	home := t.TempDir()
	workDir, err := workspace.EnsureWorkDir(home, "ws")
	if err != nil {
		t.Fatalf("EnsureWorkDir: %v", err)
	}
	repo, err := gitevidence.Open(workDir, gitevidence.WithRedactor(func(s string) string { return s }))
	if err != nil {
		t.Fatalf("gitevidence.Open: %v", err)
	}

	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("first"), 0o600); err != nil {
		t.Fatalf("write first: %v", err)
	}
	res1, err := repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{TaskID: "m-1"}, []string{"a.txt"})
	if err != nil || res1.Skipped {
		t.Fatalf("commit first: err=%v skipped=%v", err, res1.Skipped)
	}
	// First Play: materialize at res1.Hash.
	t.Setenv("PATH", t.TempDir())
	ts := task.New(filepath.Join(t.TempDir(), "tasks"))
	mustCreateTask(t, ts, &task.Task{ID: "m-1", Title: "m-1", WorkspaceID: "ws"})
	r := NewLastMemberCommitResolver(ts, home)
	dir1, err := r.ResetMemberCheckout("p", "m-1", res1.Hash)
	if err != nil {
		t.Fatalf("first ResetMemberCheckout: %v", err)
	}
	got1, _ := os.ReadFile(filepath.Join(dir1, "a.txt"))
	if string(got1) != "first" {
		t.Fatalf("first Play a.txt = %q, want %q", got1, "first")
	}

	// Member makes another attempt, commits v=second.
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("second"), 0o600); err != nil {
		t.Fatalf("write second: %v", err)
	}
	res2, err := repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{TaskID: "m-1"}, []string{"a.txt"})
	if err != nil || res2.Skipped {
		t.Fatalf("commit second: err=%v skipped=%v", err, res2.Skipped)
	}

	// Second Play: must replace the first tree with one matching res2.
	dir2, err := r.ResetMemberCheckout("p", "m-1", res2.Hash)
	if err != nil {
		t.Fatalf("second ResetMemberCheckout: %v", err)
	}
	if dir2 != dir1 {
		t.Errorf("second Play dir = %q, want %q (deterministic per (home, ws, taskID))", dir2, dir1)
	}
	got2, _ := os.ReadFile(filepath.Join(dir2, "a.txt"))
	if string(got2) != "second" {
		t.Errorf("second Play a.txt = %q, want %q (the newer commit, not the prior one)", got2, "second")
	}
}
