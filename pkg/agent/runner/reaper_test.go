package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReapOrphans_RemovesTempDirOrphans(t *testing.T) {
	setOmnipusHome(t)

	// Simulate a crashed run: a prepared temp-dir workspace whose Teardown never
	// ran (we deliberately do NOT defer Teardown).
	ws, err := PrepareWorkspace(context.Background(), "run-orphan-1", "")
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	orphanDir := ws.Dir
	if _, statErr := os.Stat(orphanDir); statErr != nil {
		t.Fatalf("orphan dir missing pre-reap: %v", statErr)
	}

	res, err := ReapOrphans(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphans: %v", err)
	}
	if res.Removed != 1 || res.Scanned != 1 {
		t.Fatalf("reap result Scanned=%d Removed=%d, want 1/1 (errs=%v)", res.Scanned, res.Removed, res.Errors)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("orphan dir still present after reap: %v", err)
	}
}

func TestReapOrphans_RemovesWorktreeOrphansAndPrunesRepo(t *testing.T) {
	gitAvailable(t)
	setOmnipusHome(t)
	repo := initRepo(t, t.TempDir())

	ws, err := PrepareWorkspace(context.Background(), "run-orphan-wt", repo)
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	orphanDir := ws.Dir
	// Confirm it's registered before reaping.
	pre, _ := runGit(context.Background(), repo, "worktree", "list")
	if !strings.Contains(pre, orphanDir) {
		t.Fatalf("worktree not registered pre-reap:\n%s", pre)
	}

	res, err := ReapOrphans(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphans: %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("Removed=%d, want 1 (errs=%v)", res.Removed, res.Errors)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("worktree orphan dir still present: %v", err)
	}
	// Registration must be cleaned from the source repo.
	post, _ := runGit(context.Background(), repo, "worktree", "list")
	if strings.Contains(post, orphanDir) {
		t.Fatalf("worktree still registered post-reap:\n%s", post)
	}
}

func TestReapOrphans_EmptyRoot_NoError(t *testing.T) {
	setOmnipusHome(t)
	// No runs prepared; root may not even exist yet.
	res, err := ReapOrphans(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphans on empty: %v", err)
	}
	if res.Scanned != 0 || res.Removed != 0 {
		t.Fatalf("expected zero work, got Scanned=%d Removed=%d", res.Scanned, res.Removed)
	}
}

func TestReapOrphans_IgnoresNonRunnerDirs(t *testing.T) {
	setOmnipusHome(t)
	root, err := RunsRoot()
	if err != nil {
		t.Fatal(err)
	}
	// A dir that does NOT carry the run- prefix must be left alone.
	keep := filepath.Join(root, "not-a-run")
	if mkErr := os.MkdirAll(keep, 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	res, err := ReapOrphans(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphans: %v", err)
	}
	if res.Scanned != 0 {
		t.Fatalf("non-runner dir was scanned: Scanned=%d", res.Scanned)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("non-runner dir was removed: %v", err)
	}
}

func TestReapOrphans_MultipleOrphans(t *testing.T) {
	setOmnipusHome(t)
	for _, id := range []string{"run-m1", "run-m2", "run-m3"} {
		if _, err := PrepareWorkspace(context.Background(), id, ""); err != nil {
			t.Fatalf("prepare %s: %v", id, err)
		}
	}
	res, err := ReapOrphans(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphans: %v", err)
	}
	if res.Removed != 3 {
		t.Fatalf("Removed=%d, want 3 (errs=%v)", res.Removed, res.Errors)
	}
}
