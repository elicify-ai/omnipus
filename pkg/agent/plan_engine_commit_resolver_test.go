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
