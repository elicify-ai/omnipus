// Spec-4 FR-5.3 (M-4) — startup reaper for orphaned runner run directories.
//
// An external-CLI run normally tears down its own git-worktree / temp dir when
// it ends (RunWorkspace.Teardown). A crash, SIGKILL, or power loss can leave a
// run dir behind: an "orphan". Orphans accumulate disk and, for worktrees, leak
// `git worktree` metadata in the source repo.
//
// The reaper runs ONCE at gateway boot, BEFORE any new run is dispatched, so it
// can safely assume that any run dir present at boot belongs to a prior process
// and is therefore an orphan (no run from THIS process exists yet). It removes
// each orphan via the same teardown path the live code uses. For worktree
// orphans it also prunes the source repo's stale worktree registrations.
//
// Design note (why "everything at boot is an orphan" is safe): runner run dirs
// live under a single dedicated root ($OMNIPUS_HOME/runner-runs) that nothing
// else writes to, and dispatch only creates dirs AFTER boot. Running the reaper
// before the first dispatch means there is no live run to mis-classify. If a
// future design runs concurrent gateways against one OMNIPUS_HOME, this
// assumption must be revisited (add a liveness lock per run dir).

package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ReapResult summarizes a reaper sweep for logging and tests.
type ReapResult struct {
	// Scanned is the number of candidate run dirs examined.
	Scanned int
	// Removed is the number of orphan dirs successfully removed.
	Removed int
	// Errors holds per-dir removal errors (non-fatal; the sweep continues).
	Errors []error
	// RemovedDirs lists the absolute paths that were removed (for tests/audit).
	RemovedDirs []string
}

// ReapOrphans scans the runner-runs root and removes every run directory it
// finds, treating them all as orphans (see file header for why this is safe at
// boot). It is the M-4 startup GC.
//
// It is resilient: a failure to remove one orphan is recorded in Result.Errors
// and the sweep proceeds. A missing root is not an error (nothing to reap).
//
// Worktree orphans are removed with `git worktree remove --force` first (to
// unregister them from their source repo), then os.RemoveAll cleans any
// residue. The source repo is read from git's own worktree metadata, not from
// the run dir, because the run dir's .git is a gitdir pointer file.
func ReapOrphans(ctx context.Context) (ReapResult, error) {
	var res ReapResult

	root, err := RunsRoot()
	if err != nil {
		return res, err
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, fmt.Errorf("reading runner-runs root %q: %w", root, err)
	}

	// Source repos of removed worktree orphans, deduped. After all dirs are
	// gone we run `git worktree prune` once per repo to drop any worktree
	// registrations whose checkout vanished without a clean `git worktree
	// remove` (e.g. a previous manual rm -rf or a partial crash).
	pruneRepos := map[string]struct{}{}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), runDirPrefix) {
			// Not a runner-owned dir; leave it untouched.
			continue
		}
		runDir := filepath.Join(root, e.Name())
		res.Scanned++

		runID := readRunMarker(runDir)
		repoRoot, removeErr := reapOne(ctx, runDir)
		if repoRoot != "" {
			pruneRepos[repoRoot] = struct{}{}
		}
		if removeErr != nil {
			res.Errors = append(res.Errors,
				fmt.Errorf("reaping %q (run %q): %w", runDir, runID, removeErr))
			slog.Warn("runner reaper: failed to remove orphan run dir",
				"dir", runDir, "run_id", runID, "error", removeErr)
			continue
		}
		res.Removed++
		res.RemovedDirs = append(res.RemovedDirs, runDir)
		slog.Info("runner reaper: removed orphan run dir",
			"dir", runDir, "run_id", runID)
	}

	for repoRoot := range pruneRepos {
		if _, err := runGit(ctx, repoRoot, "worktree", "prune"); err != nil {
			// Non-fatal: the dir is already gone; a stale registration is
			// cosmetic and self-heals on the next `git worktree` op.
			slog.Warn("runner reaper: git worktree prune failed",
				"repo", repoRoot, "error", err)
		}
	}

	return res, nil
}

// reapOne removes a single orphan run dir, using the git-worktree-aware path
// when the dir is a worktree checkout, then RemoveAll as the backstop. It
// returns the source repo top-level (empty when the dir was not a worktree) so
// the caller can prune the repo's worktree metadata after removal.
func reapOne(ctx context.Context, runDir string) (repoRoot string, err error) {
	if root, ok := worktreeRepoRoot(ctx, runDir); ok {
		repoRoot = root
		// Best-effort unregister from the source repo; log failures as Warn since
		// stale worktree registrations accumulate `git worktree` metadata.
		if _, rmErr := runGit(ctx, root, "worktree", "remove", "--force", runDir); rmErr != nil {
			slog.Warn("runner reaper: git worktree remove failed",
				"dir", runDir, "repo", repoRoot, "error", rmErr)
		}
	}
	if rmErr := os.RemoveAll(runDir); rmErr != nil {
		return repoRoot, fmt.Errorf("removing %q: %w", runDir, rmErr)
	}
	return repoRoot, nil
}

// worktreeRepoRoot reports whether runDir is a git worktree and, if so, returns
// the source repository's top-level directory. A worktree checkout contains a
// `.git` FILE (not a dir) pointing at <repo>/.git/worktrees/<name>; we resolve
// the repo top-level via `git -C <runDir> rev-parse --git-common-dir`.
func worktreeRepoRoot(ctx context.Context, runDir string) (string, bool) {
	gitPath := filepath.Join(runDir, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil || fi.IsDir() {
		// No .git, or a full .git dir → not a linked worktree.
		return "", false
	}
	// common-dir is "<repo>/.git"; its parent is the repo top-level.
	out, err := runGit(ctx, runDir, "rev-parse", "--git-common-dir")
	if err != nil {
		slog.Warn("worktreeRepoRoot: git rev-parse failed", "runDir", runDir, "err", err)
		return "", false
	}
	commonDir := strings.TrimSpace(out)
	if commonDir == "" {
		return "", false
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(runDir, commonDir)
	}
	return filepath.Dir(commonDir), true
}
