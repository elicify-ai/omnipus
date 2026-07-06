// Spec-4 FR-5.3 — git-worktree isolation for external-agent CLI runs.
//
// Each external-CLI run executes inside its OWN filesystem boundary, separate
// from the agent's main working tree. This is Omnipus's cheap FS boundary that
// complements the external CLI's OWN kernel sandbox (Codex = Landlock/seccomp;
// Claude Code = its permission model). Omnipus does NOT add a new re-exec
// confiner (operator decision) — confinement is (tool sandbox) + (worktree).
//
// Two modes, chosen by whether the source is a git repository:
//
//   - Repo source  → `git worktree add --detach <rundir> HEAD` gives the run an
//     isolated checkout that shares the object store but has its own index and
//     working files. Teardown is `git worktree remove --force`.
//   - Non-repo src → a plain isolated temp directory under
//     $OMNIPUS_HOME/runner-runs/. The source tree (if any) is copied in is NOT
//     performed here — a non-repo run starts from an empty isolated scratch dir
//     (the spec's "isolated temp dir if not a repo"). Teardown is os.RemoveAll.
//
// Every run dir lives under the single reaper root (RunsRoot) and is named with
// a stable, parseable prefix so the startup reaper (reaper.go, FR-5.3 M-4) can
// GC orphans left behind by a crash.

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// runDirPrefix is the fixed prefix of every runner run directory. The reaper
// keys off this prefix to enumerate candidate orphan dirs under RunsRoot.
const runDirPrefix = "run-"

// runsSubdir is the directory under $OMNIPUS_HOME that holds all runner run
// directories (both worktree checkouts and non-repo scratch dirs).
const runsSubdir = "runner-runs"

// gitOpTimeout bounds each git plumbing call so a hung git (e.g. a credential
// helper prompting on a tty) cannot wedge run setup/teardown forever.
const gitOpTimeout = 30 * time.Second

// IsolationMode records how a run directory was created, so teardown can pick
// the correct removal path.
type IsolationMode string

const (
	// IsolationWorktree means the run dir is a `git worktree` of a repo source.
	IsolationWorktree IsolationMode = "worktree"
	// IsolationTempDir means the run dir is a plain isolated temp dir (non-repo).
	IsolationTempDir IsolationMode = "tempdir"
)

// RunWorkspace is an isolated filesystem boundary for a single external-CLI run.
// Always pair a successful PrepareWorkspace with a deferred Teardown.
type RunWorkspace struct {
	// RunID is the stable run identifier this workspace belongs to.
	RunID string
	// Dir is the absolute path the external CLI runs in (its cwd).
	Dir string
	// Mode records how Dir was created (worktree vs temp dir).
	Mode IsolationMode
	// repoRoot is the source git repo top-level (set only for worktree mode);
	// `git worktree remove` must run from inside the repo.
	repoRoot string
}

// RunsRoot returns the absolute reaper root directory
// ($OMNIPUS_HOME/runner-runs), creating it (0700) if absent. All run dirs live
// directly under it. This is the single directory the reaper scans.
func RunsRoot() (string, error) {
	root := filepath.Join(config.OmnipusHomeDir(), runsSubdir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("creating runner-runs root %q: %w", root, err)
	}
	return root, nil
}

// PrepareWorkspace creates an isolated run directory for runID.
//
//   - When sourceDir is inside a git repository, a detached `git worktree` of
//     that repo's HEAD is created and IsolationWorktree is returned.
//   - Otherwise (sourceDir empty, missing, or not a repo) a fresh isolated temp
//     directory is created and IsolationTempDir is returned.
//
// The caller MUST call Teardown on the returned workspace when the run ends,
// including on crash paths (defer it). runID is sanitized into the dir name so
// the reaper can recognize the dir; the FULL runID is also written to a marker
// file for unambiguous orphan attribution.
func PrepareWorkspace(ctx context.Context, runID, sourceDir string) (*RunWorkspace, error) {
	if runID == "" {
		return nil, fmt.Errorf("PrepareWorkspace: empty runID")
	}
	root, err := RunsRoot()
	if err != nil {
		return nil, err
	}

	dirName := runDirPrefix + sanitizeRunID(runID)
	runDir := filepath.Join(root, dirName)

	// Refuse to clobber an existing dir for the same run — that would be a
	// double-prepare bug; surface it loudly rather than corrupting state.
	if _, statErr := os.Stat(runDir); statErr == nil {
		return nil, fmt.Errorf("PrepareWorkspace: run dir already exists: %q", runDir)
	}

	repoRoot, isRepo := gitRepoRoot(ctx, sourceDir)
	if isRepo {
		ws, wErr := prepareWorktree(ctx, runID, repoRoot, runDir)
		if wErr == nil {
			return ws, nil
		}
		// Worktree creation failed (e.g. unborn HEAD, no commits). Fall back to
		// an isolated temp dir so the run still gets an FS boundary rather than
		// failing outright — the boundary is the security property, the
		// shared-objects optimization is not. Clean any partial worktree dir.
		_ = os.RemoveAll(runDir)
	}

	return prepareTempDir(runID, runDir)
}

// prepareWorktree creates a detached git worktree at runDir from repoRoot's HEAD.
func prepareWorktree(ctx context.Context, runID, repoRoot, runDir string) (*RunWorkspace, error) {
	// --detach: no branch ref is created, so concurrent runs of the same repo
	// don't collide on branch names and teardown is unambiguous.
	if _, err := runGit(ctx, repoRoot, "worktree", "add", "--detach", runDir, "HEAD"); err != nil {
		return nil, fmt.Errorf("git worktree add: %w", err)
	}
	if err := writeRunMarker(runDir, runID); err != nil {
		// Marker is required for reaper attribution; if we can't write it, undo
		// the worktree so we don't leak an unattributable dir.
		_, _ = runGit(ctx, repoRoot, "worktree", "remove", "--force", runDir)
		return nil, err
	}
	return &RunWorkspace{
		RunID:    runID,
		Dir:      runDir,
		Mode:     IsolationWorktree,
		repoRoot: repoRoot,
	}, nil
}

// prepareTempDir creates a plain isolated scratch dir at runDir.
func prepareTempDir(runID, runDir string) (*RunWorkspace, error) {
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating run temp dir %q: %w", runDir, err)
	}
	if err := writeRunMarker(runDir, runID); err != nil {
		_ = os.RemoveAll(runDir)
		return nil, err
	}
	return &RunWorkspace{
		RunID: runID,
		Dir:   runDir,
		Mode:  IsolationTempDir,
	}, nil
}

// Teardown removes the run's isolated directory. It is idempotent and safe to
// call from a defer even when PrepareWorkspace partially failed. For worktree
// mode it uses `git worktree remove --force` (which also prunes the repo's
// worktree metadata); it then falls back to os.RemoveAll to catch any residue.
func (w *RunWorkspace) Teardown(ctx context.Context) error {
	if w == nil || w.Dir == "" {
		return nil
	}
	var firstErr error
	if w.Mode == IsolationWorktree && w.repoRoot != "" {
		if _, err := runGit(ctx, w.repoRoot, "worktree", "remove", "--force", w.Dir); err != nil {
			// Record but continue to RemoveAll; a half-removed worktree must not
			// leak a directory.
			firstErr = fmt.Errorf("git worktree remove: %w", err)
		}
	}
	if err := os.RemoveAll(w.Dir); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("removing run dir %q: %w", w.Dir, err)
	}
	return firstErr
}

// --- helpers -------------------------------------------------------------

// runMarkerName is the file written into every run dir holding the full runID.
// The reaper reads it for orphan attribution; its presence also distinguishes a
// runner-owned dir from anything else that might land under RunsRoot.
const runMarkerName = ".omnipus-run"

// writeRunMarker writes the full runID into the run dir's marker file (0600).
func writeRunMarker(runDir, runID string) error {
	p := filepath.Join(runDir, runMarkerName)
	if err := os.WriteFile(p, []byte(runID+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing run marker %q: %w", p, err)
	}
	return nil
}

// readRunMarker returns the full runID recorded in a run dir, or "" if absent.
func readRunMarker(runDir string) string {
	b, err := os.ReadFile(filepath.Join(runDir, runMarkerName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// gitRepoRoot reports whether sourceDir is inside a git repository and, if so,
// returns the repository top-level directory. An empty or non-existent
// sourceDir is treated as "not a repo".
func gitRepoRoot(ctx context.Context, sourceDir string) (root string, isRepo bool) {
	if sourceDir == "" {
		return "", false
	}
	if fi, err := os.Stat(sourceDir); err != nil || !fi.IsDir() {
		return "", false
	}
	out, err := runGit(ctx, sourceDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	top := strings.TrimSpace(out)
	if top == "" {
		return "", false
	}
	return top, true
}

// runGit runs a git subcommand in dir with a bounded timeout and returns its
// stdout. git is invoked directly (not via a shell) so run setup is not a
// shell-injection surface.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Dir = dir
	// Keep git non-interactive: never prompt for credentials or editor.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		if stderr != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// sanitizeRunID maps a runID to a filesystem-safe directory-name fragment.
// Only [A-Za-z0-9._-] survive; everything else becomes '_'. This prevents path
// traversal (a runID of "../../etc" cannot escape RunsRoot) and keeps dir names
// portable. The FULL runID is preserved separately in the run marker file, so
// sanitisation collisions are detectable (PrepareWorkspace refuses an existing
// dir) and attribution stays exact.
func sanitizeRunID(runID string) string {
	var b strings.Builder
	b.Grow(len(runID))
	for _, r := range runID {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		s = "x"
	}
	return s
}
