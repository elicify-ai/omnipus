package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setOmnipusHome points OMNIPUS_HOME at a fresh temp dir for the test and
// restores the prior value on cleanup. Returns the home dir.
func setOmnipusHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	return home
}

// gitAvailable reports whether the git binary is present; worktree tests skip
// when it is not (CI always has git, but be defensive).
func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping worktree test")
	}
}

// initRepo creates a git repo with one commit in dir and returns dir.
func initRepo(t *testing.T, dir string) string {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestPrepareWorkspace_RepoSource_CreatesWorktree(t *testing.T) {
	gitAvailable(t)
	setOmnipusHome(t)
	repo := initRepo(t, t.TempDir())

	ws, err := PrepareWorkspace(context.Background(), "run-abc123", repo)
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = ws.Teardown(context.Background()) })

	if ws.Mode != IsolationWorktree {
		t.Fatalf("Mode = %q, want %q", ws.Mode, IsolationWorktree)
	}
	// The worktree dir must exist and contain the committed file (proves it is a
	// real checkout, not an empty dir).
	if _, statErr := os.Stat(filepath.Join(ws.Dir, "README.md")); statErr != nil {
		t.Fatalf("worktree missing checked-out file: %v", statErr)
	}
	// And the run marker must record the full runID.
	if got := readRunMarker(ws.Dir); got != "run-abc123" {
		t.Fatalf("run marker = %q, want %q", got, "run-abc123")
	}
	// The worktree must be registered in the source repo.
	out, err := runGit(context.Background(), repo, "worktree", "list")
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	if !strings.Contains(out, ws.Dir) {
		t.Fatalf("worktree %q not registered in repo:\n%s", ws.Dir, out)
	}
}

func TestPrepareWorkspace_NonRepoSource_CreatesTempDir(t *testing.T) {
	setOmnipusHome(t)
	plain := t.TempDir() // not a git repo

	ws, err := PrepareWorkspace(context.Background(), "run-xyz", plain)
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = ws.Teardown(context.Background()) })

	if ws.Mode != IsolationTempDir {
		t.Fatalf("Mode = %q, want %q", ws.Mode, IsolationTempDir)
	}
	if fi, err := os.Stat(ws.Dir); err != nil || !fi.IsDir() {
		t.Fatalf("temp run dir missing: %v", err)
	}
	if got := readRunMarker(ws.Dir); got != "run-xyz" {
		t.Fatalf("run marker = %q, want %q", got, "run-xyz")
	}
}

func TestPrepareWorkspace_EmptySource_TempDir(t *testing.T) {
	setOmnipusHome(t)
	ws, err := PrepareWorkspace(context.Background(), "run-empty", "")
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = ws.Teardown(context.Background()) })
	if ws.Mode != IsolationTempDir {
		t.Fatalf("Mode = %q, want tempdir", ws.Mode)
	}
}

func TestTeardown_RemovesWorktreeAndRegistration(t *testing.T) {
	gitAvailable(t)
	setOmnipusHome(t)
	repo := initRepo(t, t.TempDir())

	ws, err := PrepareWorkspace(context.Background(), "run-td", repo)
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	dir := ws.Dir
	if err := ws.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still present after teardown: %v", err)
	}
	// Registration must be gone too.
	out, _ := runGit(context.Background(), repo, "worktree", "list")
	if strings.Contains(out, dir) {
		t.Fatalf("worktree still registered after teardown:\n%s", out)
	}
}

func TestTeardown_Idempotent(t *testing.T) {
	setOmnipusHome(t)
	ws, err := PrepareWorkspace(context.Background(), "run-idem", "")
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if err := ws.Teardown(context.Background()); err != nil {
		t.Fatalf("first Teardown: %v", err)
	}
	if err := ws.Teardown(context.Background()); err != nil {
		t.Fatalf("second Teardown should be no-op: %v", err)
	}
	// nil-safe
	var nilWS *RunWorkspace
	if err := nilWS.Teardown(context.Background()); err != nil {
		t.Fatalf("nil Teardown: %v", err)
	}
}

func TestSanitizeRunID_NoTraversal(t *testing.T) {
	setOmnipusHome(t)
	// A hostile runID must not escape RunsRoot.
	ws, err := PrepareWorkspace(context.Background(), "../../etc/passwd", "")
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = ws.Teardown(context.Background()) })

	root, _ := RunsRoot()
	rel, err := filepath.Rel(root, ws.Dir)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if strings.HasPrefix(rel, "..") || strings.ContainsRune(rel, os.PathSeparator) {
		t.Fatalf("run dir escaped RunsRoot: root=%q dir=%q rel=%q", root, ws.Dir, rel)
	}
	// Yet the marker preserves the true runID for attribution.
	if got := readRunMarker(ws.Dir); got != "../../etc/passwd" {
		t.Fatalf("marker lost true runID: %q", got)
	}
}

func TestPrepareWorkspace_DoublePrepare_Errors(t *testing.T) {
	setOmnipusHome(t)
	ws, err := PrepareWorkspace(context.Background(), "run-dup", "")
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	t.Cleanup(func() { _ = ws.Teardown(context.Background()) })
	if _, err := PrepareWorkspace(context.Background(), "run-dup", ""); err == nil {
		t.Fatal("second prepare of same runID should error (dir exists)")
	}
}
