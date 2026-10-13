package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func wu21DeleteFixture(t *testing.T) (*systools.Deps, string, string) {
	t.Helper()
	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("USERPROFILE", isolated)
	t.Setenv("OMNIPUS_HOME", isolated)
	deps, home := newTestDepsWithHome(t)
	t.Setenv("OMNIPUS_HOME", home)
	store := agentstore.New(home)
	if err := store.Create("self", &config.AgentConfig{ID: "self", Name: "Self"}); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []string{"alpha", "beta"} {
		if err := workspace.SaveRecord(home, workspace.Workspace{ID: ws, Name: ws, Status: "active", CoreTeam: []string{"self"}}); err != nil {
			t.Fatal(err)
		}
		for _, agent := range []string{"self", "other"} {
			wu21DeleteWrite(t, wu21PairPath(home, ws, agent), ws+":"+agent)
		}
	}
	revision, err := store.PeekRevision("self")
	if err != nil {
		t.Fatal(err)
	}
	return deps, home, revision
}

func wu21PairPath(home, workspace, agent string) string {
	return filepath.Join(home, "workspaces", workspace, ".omnipus", "carry-forward", agent+".md")
}

func wu21DeleteWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Amendment §3.2(6): delete only this agent's pair files in every workspace.
// Membership-only removal stays non-destructive and is the positive control.
func TestWU21_AgentDeleteRemovesAllPairSummariesAndPreservesOtherAgents(t *testing.T) {
	deps, home, revision := wu21DeleteFixture(t)
	res := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": "alpha", "revision": currentWorkspaceRevision(t, home, "alpha"), "core_team": []any{},
	})
	if res.IsError {
		t.Fatalf("membership-only removal failed: %s", res.ForLLM)
	}
	t.Run("membership_removal_preserves_summary", func(t *testing.T) {
		b, err := os.ReadFile(wu21PairPath(home, "alpha", "self"))
		if err != nil || string(b) != "alpha:self" {
			t.Fatalf("§3.2(6): membership removal deleted/changed pair data: bytes=%q err=%v", b, err)
		}
	})
	out := systools.RunAgentDeleteCascade(home, "self", revision, deps.DeleteSessionStore(), nil)
	if out.CleanupFailed || out.DeletionErr != nil {
		t.Fatalf("healthy delete failed: warnings=%v error=%v", out.Warnings, out.DeletionErr)
	}
	for _, ws := range []string{"alpha", "beta"} {
		if _, err := os.Stat(wu21PairPath(home, ws, "self")); !os.IsNotExist(err) {
			t.Errorf("§3.2(6): deleted agent's %s carry-forward survives (err=%v)", ws, err)
		}
		b, err := os.ReadFile(wu21PairPath(home, ws, "other"))
		if err != nil || string(b) != ws+":other" {
			t.Errorf("other agent's %s summary was changed: bytes=%q err=%v", ws, b, err)
		}
	}
}

// An unremovable file-shaped target is a portable failure injection: a
// non-empty directory cannot be unlinked as a file even by a root test user.
// Cleanup must warn and keep the entity for retry, not recursively erase an
// unexpected directory or claim that the pair file was removed.
func TestWU21_AgentDeletePairCleanupFailureIsVisibleAndKeepsEntity(t *testing.T) {
	deps, home, revision := wu21DeleteFixture(t)
	blocked := wu21PairPath(home, "alpha", "self")
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	wu21DeleteWrite(t, filepath.Join(blocked, "unremovable-child"), "blocker")
	out := systools.RunAgentDeleteCascade(home, "self", revision, deps.DeleteSessionStore(), nil)
	if !out.CleanupFailed || len(out.Warnings) == 0 {
		t.Errorf("§3.2(6): pair cleanup failure was not visible: CleanupFailed=%t warnings=%v", out.CleanupFailed, out.Warnings)
	}
	if _, err := agentstore.New(home).Get("self"); err != nil {
		t.Errorf("failed pair cleanup removed entity instead of keeping it retryable: %v", err)
	}
	b, err := os.ReadFile(wu21PairPath(home, "beta", "other"))
	if err != nil || string(b) != "beta:other" {
		t.Errorf("failed cleanup changed unrelated agent summary: bytes=%q err=%v", b, err)
	}
}
