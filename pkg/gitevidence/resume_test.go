// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"testing"
)

// TestLastCommitForTask verifies the D13/G-12 resume-baseline lookup: walking
// HEAD ancestry for the most recent boundary commit naming task=<taskID>,
// across BOTH boundary kinds (task + attempt), with the no-commit fallback.
func TestLastCommitForTask(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))

	// m-1's first (task) boundary commit.
	writeFile(t, dir, "a.txt", "alpha")
	res1, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "m-1", AgentID: "owner"}, []string{"a.txt"})
	if err != nil || res1.Skipped {
		t.Fatalf("commit m-1 task boundary: err=%v skipped=%v %v", err, res1.Skipped, res1.SkipReason)
	}

	// m-2's boundary commit (interleaved after m-1).
	writeFile(t, dir, "b.txt", "beta")
	res2, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "m-2", AgentID: "owner"}, []string{"b.txt"})
	if err != nil || res2.Skipped {
		t.Fatalf("commit m-2 task boundary: err=%v skipped=%v %v", err, res2.Skipped, res2.SkipReason)
	}

	// m-1's LATER attempt boundary commit — this must win over res1.
	writeFile(t, dir, "a.txt", "alpha v2")
	res3, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "m-1", AttemptID: "att-3", AgentID: "owner"}, []string{"a.txt"})
	if err != nil || res3.Skipped {
		t.Fatalf("commit m-1 attempt boundary: err=%v skipped=%v %v", err, res3.Skipped, res3.SkipReason)
	}

	cases := []struct {
		name   string
		taskID string
		want   string
	}{
		{"m-1 resolves to most recent (attempt boundary, not the older task one)", "m-1", res3.Hash},
		{"m-2 resolves to its only commit", "m-2", res2.Hash},
		{"unknown task -> empty (fresh attempt)", "m-never", ""},
		{"empty taskID -> empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := r.LastCommitForTask(c.taskID)
			if err != nil {
				t.Fatalf("LastCommitForTask(%q) err = %v, want nil", c.taskID, err)
			}
			if got != c.want {
				t.Errorf("LastCommitForTask(%q) = %q, want %q", c.taskID, got, c.want)
			}
		})
	}
}

// TestLastCommitForTask_UnbornRepo: a repo with no commits yields "" (the
// FR-155 fresh-attempt fallback) — not an error.
func TestLastCommitForTask_UnbornRepo(t *testing.T) {
	r, _ := newTestRepo(t) // opened, nothing committed
	got, err := r.LastCommitForTask("m-1")
	if err != nil {
		t.Fatalf("LastCommitForTask on unborn repo err = %v, want nil", err)
	}
	if got != "" {
		t.Errorf("LastCommitForTask on unborn repo = %q, want \"\" (fresh attempt)", got)
	}
}

// TestExtractTaskID covers the message parser against the real formatCommitMessage
// shape and a couple of edge cases.
func TestExtractTaskID(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"[task] task=m-1 attempt=- agent=owner", "m-1"},
		{"[attempt] task=m-1 attempt=att-3 agent=-", "m-1"},
		{"[task] task=- attempt=- agent=-", "-"},
		{"unrelated commit message", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := extractTaskID(c.msg); got != c.want {
			t.Errorf("extractTaskID(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
}
