// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// evidence_committer_test.go covers the PRODUCER half of D13/G-12
// Play-from-commit (E.4) and the resume-tree consumption that depends on it
// (E.5).
//
// Why these tests exist: the 14-reviewer DoD-7 sign-off found that
// gitevidence.Repo.Commit had ZERO production callers, so LastMemberCommit
// always resolved "" and Play silently degraded to a fresh attempt. Every gate
// stayed green because the degrade is indistinguishable from success — no test
// asserted that a boundary commit was actually CREATED. The round-trip test
// below is exactly that missing assertion: it fails if the producer is ever
// unwired again.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// testSecretScanner builds the MIN-5 secret guard the committer requires.
// gitevidence.Commit refuses to commit without one, so this is not optional
// scaffolding — it mirrors the gateway boot seam's own construction.
func testSecretScanner(t *testing.T) *audit.SecretScanner {
	t.Helper()
	s, err := audit.NewSecretScanner(strings.NewReplacer(), nil)
	if err != nil {
		t.Fatalf("audit.NewSecretScanner: %v", err)
	}
	return s
}

// TestEvidenceCommitter_RoundTrip_ProducerFeedsResolver is the E.4 regression
// test: a terminal plan member's write set is committed by the PRODUCER and
// then read back by the CONSUMER (LastMemberCommit) that Play depends on.
//
// This is the end-to-end contract. Before E.4 the producer did not exist, so
// the resolver's read returned "" — this test would have failed on the
// resolver assertion, naming the exact gap.
func TestEvidenceCommitter_RoundTrip_ProducerFeedsResolver(t *testing.T) {
	home := t.TempDir()
	workDir, err := workspace.EnsureWorkDir(home, "ws")
	if err != nil {
		t.Fatalf("EnsureWorkDir: %v", err)
	}
	if writeErr := os.WriteFile(filepath.Join(workDir, "out.txt"), []byte("member output"), 0o600); writeErr != nil {
		t.Fatalf("seed work file: %v", writeErr)
	}

	tk := &task.Task{
		ID:           "m-round-trip",
		Title:        "round-trip member",
		WorkspaceID:  "ws",
		AgentID:      "worker",
		AttemptCount: 1,
		WriteSet:     []string{"out.txt"},
	}

	committer := NewWorkspaceEvidenceCommitter(home, testSecretScanner(t))
	res, recorded, err := committer.CommitTaskBoundary(tk)
	if err != nil {
		t.Fatalf("CommitTaskBoundary: %v", err)
	}
	if !recorded || res == nil {
		t.Fatal("CommitTaskBoundary returned no result — the producer recorded nothing, " +
			"which is the exact E.4 defect (Play would silently take the fresh-attempt path)")
	}
	if res.Skipped {
		t.Fatalf("boundary commit skipped (%s) — expected a real commit", res.SkipReason)
	}
	if res.Hash == "" {
		t.Fatal("boundary commit returned an empty hash")
	}

	// The consumer half: this is what PlanEngine.Play calls. It MUST see the
	// commit the producer just wrote, or Play resumes from nothing.
	store := task.New(filepath.Join(home, "tasks"))
	if createErr := store.Create(tk); createErr != nil {
		t.Fatalf("seed task store: %v", createErr)
	}
	resolver := NewLastMemberCommitResolver(store, home)
	got, err := resolver.LastMemberCommit("p-1", tk.ID)
	if err != nil {
		t.Fatalf("LastMemberCommit: %v", err)
	}
	if got != res.Hash {
		t.Fatalf("resolver read back %q, want the produced commit %q — producer and consumer are not "+
			"talking to the same repo (D13/G-12 round trip broken)", got, res.Hash)
	}
}

// TestEvidenceCommitter_GracefulDegrades pins the "nothing to record" cases as
// (nil, nil) rather than errors — a task that legitimately has no evidence must
// never fail an otherwise-successful run.
func TestEvidenceCommitter_GracefulDegrades(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if _, err := workspace.EnsureWorkDir(home, "ws"); err != nil {
		t.Fatalf("EnsureWorkDir: %v", err)
	}
	scanner := testSecretScanner(t)

	cases := []struct {
		name      string
		committer *WorkspaceEvidenceCommitter
		task      *task.Task
	}{
		{"nil task", NewWorkspaceEvidenceCommitter(home, scanner), nil},
		{"unwired home", NewWorkspaceEvidenceCommitter("", scanner), &task.Task{ID: "a", WorkspaceID: "ws", WriteSet: []string{"x"}}},
		{"unwired scanner", NewWorkspaceEvidenceCommitter(home, nil), &task.Task{ID: "a", WorkspaceID: "ws", WriteSet: []string{"x"}}},
		{"no workspace", NewWorkspaceEvidenceCommitter(home, scanner), &task.Task{ID: "a", WriteSet: []string{"x"}}},
		{"no write set", NewWorkspaceEvidenceCommitter(home, scanner), &task.Task{ID: "a", WorkspaceID: "ws"}},
		{"unmaterialized workspace", NewWorkspaceEvidenceCommitter(home, scanner), &task.Task{ID: "a", WorkspaceID: "never-made", WriteSet: []string{"x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, recorded, err := tc.committer.CommitTaskBoundary(tc.task)
			if err != nil {
				t.Fatalf("expected a graceful (nil, false, nil) degrade, got error: %v", err)
			}
			if recorded || res != nil {
				t.Fatalf("expected no commit result, got recorded=%v res=%+v", recorded, res)
			}
		})
	}
}

// TestResumeWorkDirOverride_RootsTurnAtRestoredTree is the E.5 regression test:
// resolveTurnWorkDirOrRefuse — the gate BOTH the native and external-cli
// dispatch paths share — must root a Play-resumed member's turn at the restored
// checkout rather than the workspace's shared work/ dir.
//
// Before E.5 the resume directory was materialized and then never read, so the
// resumed turn ran against the shared tree and the Judge diffed the wrong
// baseline.
func TestResumeWorkDirOverride_RootsTurnAtRestoredTree(t *testing.T) {
	resumeDir := t.TempDir()

	ctx := WithResumeWorkDirOverride(context.Background(), resumeDir)
	got, err := resolveTurnWorkDirOrRefuse(ctx, "worker", t.TempDir(), "ws")
	if err != nil {
		t.Fatalf("resolveTurnWorkDirOrRefuse: %v", err)
	}
	if got != resumeDir {
		t.Fatalf("turn rooted at %q, want the resume tree %q — the materialized "+
			"checkout is being ignored (E.5 defect)", got, resumeDir)
	}
}

// TestResumeWorkDirOverride_EmptyIsNoOp pins the ordinary (non-resumed) path:
// an empty override must leave ctx untouched so the turn falls through to the
// normal workspace resolution.
func TestResumeWorkDirOverride_EmptyIsNoOp(t *testing.T) {
	base := context.Background()
	if got := WithResumeWorkDirOverride(base, ""); got != base {
		t.Fatal("empty resume dir must return ctx unmodified (no-op contract)")
	}
	if got := WithResumeWorkDirOverride(base, "   "); got != base {
		t.Fatal("whitespace-only resume dir must return ctx unmodified")
	}
	if got := resumeWorkDirOverrideFromContext(base); got != "" {
		t.Fatalf("unset override read back %q, want empty", got)
	}
}
