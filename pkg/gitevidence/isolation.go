// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Rung identifies a tier of the isolation ladder (FR-154/FR-157): the
// mechanism used to give a plan stream (typically an exploratory member
// with no declared write-set) its own working tree, in decreasing order of
// isolation quality/cost. go-git has NO `git worktree add` equivalent
// (confirmed against go-git v5.19.1 source by the embedding spike — zero
// hits for any linked-worktree API) — RungSystemGitWorktree is only ever
// available when the real `git` binary is present on the runtime.
type Rung int

const (
	// RungSystemGitWorktree uses the real `git` binary (`git worktree
	// add`) against the evidence repo's `.git` — the cheapest real
	// isolation (a linked working tree sharing the object store), but
	// only available when a `git` binary is on PATH. This is an
	// ENGINE-internal exec of `git` for orchestration, distinct from — and
	// not a relaxation of — the agent-facing D17 deny-by-operation tool
	// surface, which is a separate concern owned outside this package (see
	// the package doc's Scope section).
	RungSystemGitWorktree Rung = iota
	// RungGoGitClone uses git.PlainClone against the local repo path. Per
	// the spike's Deliverable 3, this is a REAL local clone: a full,
	// independent object-store copy (measured at 1.27s / zero shared
	// inodes for a 20 MB repo), not a lightweight linked worktree.
	//
	// IMPORTANT — this is NOT a pure-Go, system-git-independent fallback:
	// go-git's local ("file") transport itself shells out to a system
	// `git-upload-pack` binary (confirmed by reading
	// plumbing/transport/file/client.go in the go-git v5.19.1 source —
	// it resolves the binary via os/exec, falling back to `git
	// --exec-path` when not directly on PATH), which is exactly why the
	// spike measured clone latency in the ~1 second range rather than
	// microseconds. When no system `git` is present at all, BOTH
	// RungSystemGitWorktree and RungGoGitClone are unavailable — see
	// SelectRung.
	RungGoGitClone
	// RungSubdir gives the stream a plain subdirectory of the SAME
	// checkout — no filesystem isolation at all (shared working tree AND
	// shared index/.git). Always available; callers choosing this rung
	// are responsible for their own write-set disjointness (plan-lint,
	// FR-156) or explicit serialization, since this package's Commit
	// mutex only serializes COMMITS, not arbitrary concurrent file writes
	// between commits.
	RungSubdir
)

// String renders the rung as a stable, lowercase, machine-parseable token
// (safe to log or persist).
func (r Rung) String() string {
	switch r {
	case RungSystemGitWorktree:
		return "system_git_worktree"
	case RungGoGitClone:
		return "go_git_clone"
	case RungSubdir:
		return "subdir"
	default:
		return fmt.Sprintf("unknown_rung(%d)", int(r))
	}
}

// IsolatedCheckout is a materialized isolated working tree produced by the
// isolation ladder, tagged with the rung that actually produced it (FR-154:
// "returning which rung is active so callers/Judge know the capability").
type IsolatedCheckout struct {
	// Dir is the isolated checkout's working-tree root.
	Dir string
	// Rung is the tier that actually succeeded — may be lower than what
	// the caller asked for if a higher rung failed at materialization
	// time (see OpenIsolatedCheckoutAtRung's doc comment).
	Rung Rung
	// Cleanup tears the checkout down (git worktree remove / rm -rf the
	// clone / rm -rf the subdir, matching Rung). Callers should always
	// invoke it once the stream's isolated work concludes.
	Cleanup func() error
}

const isolationCmdTimeout = 30 * time.Second

func systemGitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// SelectRung reports the highest isolation rung this runtime can OFFER,
// without creating anything (a pure capability probe, no side effects) —
// e.g. for the planner to decide at author time whether a join it wants to
// write is executable at all (FR-154: "the planner MUST never author a
// join the runtime cannot execute").
//
// Despite being a Go library, go-git's local clone rung (RungGoGitClone)
// is NOT independent of system git — its local transport shells out to
// `git-upload-pack` (see RungGoGitClone's doc comment) — so when no system
// `git` binary is present, SelectRung reports RungSubdir, not
// RungGoGitClone: reporting a rung the runtime cannot actually deliver
// would itself violate FR-154. A concrete OpenIsolatedCheckout call may
// still degrade further than what SelectRung reported, for a reason this
// static probe cannot foresee (e.g. a `git` binary LookPath finds but that
// then errors on `worktree add` for an unrelated reason, or a mid-run disk
// exhaustion) — OpenIsolatedCheckoutAtRung still tries RungGoGitClone as an
// intermediate fallback in that case, since it exercises a different git
// subcommand than `worktree add` and may succeed where that failed.
func SelectRung() Rung {
	if systemGitAvailable() {
		return RungSystemGitWorktree
	}
	return RungSubdir
}

// OpenIsolatedCheckout materializes an isolated working tree for a stream
// (an exploratory member per FR-157, or any plan member needing a real
// separate checkout) at the HIGHEST rung this runtime can actually deliver,
// trying system-git worktree, then go-git clone, then subdir in that order
// and degrading on failure (R§8.4/D10). baseDir must be an already-open
// evidence repo's directory (see Open); targetDir is where the isolated
// checkout should live — for the subdir rung specifically, targetDir
// SHOULD be a path under baseDir (it shares that repo's working tree by
// construction); for the other two rungs targetDir may be anywhere the
// caller has write access.
func OpenIsolatedCheckout(baseDir, targetDir string) (*IsolatedCheckout, error) {
	return OpenIsolatedCheckoutAtRung(baseDir, targetDir, RungSystemGitWorktree)
}

// rungStep is one tier of the isolation ladder: the rung it materializes
// and the function that opens a checkout at exactly that rung (no internal
// degradation — degradation is the ladder loop's job, so a caller restoring
// at a commit can require the tree to match the commit on EVERY rung).
type rungStep struct {
	rung Rung
	open func(baseDir, targetDir string) (*IsolatedCheckout, error)
}

// isolationLadder is the single source of truth for rung order (highest
// isolation first). Both OpenIsolatedCheckoutAtRung and
// OpenIsolatedCheckoutAtCommitRung iterate it; never reorder casually — the
// order IS the documented degradation contract (D10/FR-154).
var isolationLadder = []rungStep{
	{RungSystemGitWorktree, openSystemGitWorktree},
	{RungGoGitClone, openGoGitClone},
	{RungSubdir, openSubdir},
}

// runLadder is the single implementation of isolation-ladder traversal with
// rung filtering and exhaustion handling (used by both
// OpenIsolatedCheckoutAtRung and OpenIsolatedCheckoutAtCommitRung — the two
// callers differ only in WHAT they do once a rung's checkout opens:
// plain-open returns immediately; restore-at-commit must additionally walk
// the checkout back to the recorded commit hash and degrade on a mismatch).
//
// onOpened runs immediately after a rung's `open` succeeds; if it returns
// an error the rung degrades (its checkout is cleaned up via ic.Cleanup
// when cleanupOnError is non-nil) and the ladder continues. onOpened MAY
// also return the (ic, nil) success pair directly for callers like the
// plain-open path that have no post-open check.
//
// The returned (ic, nil) signals the ladder produced a checkout the caller
// can use; the returned error wraps every per-rung error when the ladder
// exhausts without a winner.
func runLadder(
	startRung Rung,
	baseDir, targetDir string,
	logPrefix string,
	onOpened func(ic *IsolatedCheckout, step rungStep) (*IsolatedCheckout, error),
) (*IsolatedCheckout, error) {
	var errs []error

	for _, step := range isolationLadder {
		if step.rung < startRung {
			continue // rungs above startRung are never attempted
		}
		ic, err := step.open(baseDir, targetDir)
		if err != nil {
			errs = append(errs, err)
			logger.WarnCF("gitevidence", logPrefix+": rung failed, degrading", map[string]any{
				"rung": step.rung.String(), "base_dir": baseDir, "target_dir": targetDir, "error": err.Error(),
			})
			continue
		}
		out, oerr := onOpened(ic, step)
		if oerr == nil {
			return out, nil
		}
		errs = append(errs, oerr)
		if cerr := ic.Cleanup(); cerr != nil {
			logger.WarnCF("gitevidence", logPrefix+": cleanup of unsuccessful checkout failed", map[string]any{
				"rung": ic.Rung.String(), "target_dir": targetDir, "error": cerr.Error(),
			})
		}
	}
	return nil, fmt.Errorf("gitevidence: isolation ladder exhausted, even the subdir rung failed: %w", errors.Join(errs...))
}

// OpenIsolatedCheckoutAtRung is OpenIsolatedCheckout but starts the ladder
// at startRung instead of the top (e.g. a caller who already knows, from
// the SizeGuard media-bloat concern, that a full clone of this particular
// repo is too expensive can pass RungGoGitClone or RungSubdir directly to
// skip the higher rung(s) entirely). Rungs above startRung are never
// attempted. The subdir rung's only failure mode is its own os.MkdirAll,
// so this only returns an error when that fails too — there is no lower
// rung to degrade to.
func OpenIsolatedCheckoutAtRung(baseDir, targetDir string, startRung Rung) (*IsolatedCheckout, error) {
	return runLadder(startRung, baseDir, targetDir, "isolation ladder",
		func(ic *IsolatedCheckout, step rungStep) (*IsolatedCheckout, error) {
			return ic, nil
		})
}

func openSystemGitWorktree(baseDir, targetDir string) (*IsolatedCheckout, error) {
	if !systemGitAvailable() {
		return nil, fmt.Errorf("gitevidence: system git binary not available on PATH")
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o700); err != nil {
		return nil, fmt.Errorf("gitevidence: prepare worktree parent dir for %s: %w", targetDir, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), isolationCmdTimeout)
	defer cancel()
	// #nosec G204 -- baseDir/targetDir are engine-controlled paths (the
	// evidence repo's own dir and an engine-chosen isolation-checkout
	// path), never raw agent input; this is engine-internal orchestration
	// exec, not the agent-facing tool surface (see package doc Scope).
	cmd := exec.CommandContext(ctx, "git", "-C", baseDir, "worktree", "add", "--detach", targetDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gitevidence: git worktree add %s: %w (%s)", targetDir, err, strings.TrimSpace(string(out)))
	}
	return &IsolatedCheckout{
		Dir:  targetDir,
		Rung: RungSystemGitWorktree,
		Cleanup: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), isolationCmdTimeout)
			defer cancel()
			// #nosec G204 -- see the openSystemGitWorktree exec above.
			rmCmd := exec.CommandContext(ctx, "git", "-C", baseDir, "worktree", "remove", "--force", targetDir)
			if out, rmErr := rmCmd.CombinedOutput(); rmErr != nil {
				return fmt.Errorf("gitevidence: git worktree remove %s: %w (%s)", targetDir, rmErr, strings.TrimSpace(string(out)))
			}
			return nil
		},
	}, nil
}

func openGoGitClone(baseDir, targetDir string) (*IsolatedCheckout, error) {
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o700); err != nil {
		return nil, fmt.Errorf("gitevidence: prepare clone parent dir for %s: %w", targetDir, err)
	}
	if _, err := git.PlainClone(targetDir, false, &git.CloneOptions{URL: baseDir}); err != nil {
		return nil, fmt.Errorf("gitevidence: go-git clone %s -> %s: %w", baseDir, targetDir, err)
	}
	return &IsolatedCheckout{
		Dir:  targetDir,
		Rung: RungGoGitClone,
		Cleanup: func() error {
			if err := os.RemoveAll(targetDir); err != nil {
				return fmt.Errorf("gitevidence: remove clone isolation checkout %s: %w", targetDir, err)
			}
			return nil
		},
	}, nil
}

func openSubdir(baseDir, targetDir string) (*IsolatedCheckout, error) {
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return nil, fmt.Errorf("gitevidence: create subdir isolation checkout %s: %w", targetDir, err)
	}
	return &IsolatedCheckout{
		Dir:  targetDir,
		Rung: RungSubdir,
		Cleanup: func() error {
			clean, err := filepath.Abs(targetDir)
			if err != nil {
				return fmt.Errorf("gitevidence: resolve subdir checkout %s for cleanup: %w", targetDir, err)
			}
			base, err := filepath.Abs(baseDir)
			if err != nil {
				return fmt.Errorf("gitevidence: resolve base dir %s for cleanup: %w", baseDir, err)
			}
			if clean == base {
				return fmt.Errorf("gitevidence: refusing to remove the evidence repo's own base dir %s", baseDir)
			}
			if err := os.RemoveAll(targetDir); err != nil {
				return fmt.Errorf("gitevidence: remove subdir isolation checkout %s: %w", targetDir, err)
			}
			return nil
		},
	}, nil
}

// --- Restore-at-commit (D13 Play-from-commit) ------------------------------

// OpenIsolatedCheckoutAtCommit materializes an isolated working tree whose
// files match commit hash EXACTLY (not the repo's current HEAD), walking the
// same isolation ladder as OpenIsolatedCheckout: a rung only counts as
// delivered when its tree has been restored to the recorded commit — a rung
// that opens but cannot restore (e.g. `git worktree add` succeeded but the
// checkout of hash failed) is torn down and the ladder degrades, exactly as
// for an open failure. This is the D13 Play-from-commit materialization
// step (#537): the resumed plan member gets a tree at its last recorded
// boundary commit instead of a fresh empty attempt.
func OpenIsolatedCheckoutAtCommit(baseDir, targetDir, hash string) (*IsolatedCheckout, error) {
	return OpenIsolatedCheckoutAtCommitRung(baseDir, targetDir, hash, RungSystemGitWorktree)
}

// OpenIsolatedCheckoutAtCommitRung is OpenIsolatedCheckoutAtCommit but starts
// the ladder at startRung (mirrors OpenIsolatedCheckoutAtRung). Rungs above
// startRung are never attempted.
func OpenIsolatedCheckoutAtCommitRung(baseDir, targetDir, hash string, startRung Rung) (*IsolatedCheckout, error) {
	if !isFullHexHash(hash) {
		return nil, fmt.Errorf("gitevidence: restore-at-commit requires a full 40-char hex commit hash, got %q", hash)
	}
	// Fail closed on a pre-existing non-empty target: the subdir rung's
	// restore writes the commit's files but does not sweep unrelated files
	// already present, so a dirty target would silently produce a tree that
	// does NOT match the recorded commit. Callers replacing a prior checkout
	// must remove it first (RemoveIsolatedCheckout) — that is exactly the
	// engine's replace semantics.
	if entries, err := os.ReadDir(targetDir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("gitevidence: restore-at-commit target %s is not empty — remove the prior checkout first", targetDir)
	}
	var errs []error
	for _, step := range isolationLadder {
		if step.rung < startRung {
			continue
		}
		ic, err := step.open(baseDir, targetDir)
		if err != nil {
			errs = append(errs, err)
			logger.WarnCF("gitevidence", "isolation ladder (restore-at-commit): rung failed to open, degrading", map[string]any{
				"rung": step.rung.String(), "base_dir": baseDir, "target_dir": targetDir, "commit": hash, "error": err.Error(),
			})
			continue
		}
		if err := restoreTreeAtCommit(ic, baseDir, hash); err != nil {
			errs = append(errs, err)
			logger.WarnCF("gitevidence", "isolation ladder (restore-at-commit): rung could not restore the recorded commit, degrading", map[string]any{
				"rung": ic.Rung.String(), "base_dir": baseDir, "target_dir": targetDir, "commit": hash, "error": err.Error(),
			})
			if cerr := ic.Cleanup(); cerr != nil {
				logger.WarnCF("gitevidence", "isolation ladder (restore-at-commit): cleanup of unrestored checkout failed", map[string]any{
					"rung": ic.Rung.String(), "target_dir": targetDir, "error": cerr.Error(),
				})
			}
			continue
		}
		return ic, nil
	}
	return nil, fmt.Errorf("gitevidence: isolation ladder exhausted restoring commit %s: %w", hash, errors.Join(errs...))
}

// isFullHexHash reports whether s is exactly 40 lowercase-or-uppercase hex
// chars (a full SHA-1 commit id). go-git's plumbing.NewHash silently maps
// malformed input toward the zero hash, so callers must validate up front —
// checking out the zero hash would fail-closed anyway, but with a far less
// legible error.
func isFullHexHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// restoreTreeAtCommit makes ic's working tree match commit hash, using the
// mechanism appropriate to the rung that produced ic. baseDir is the
// evidence repo the hash was recorded in (needed by the subdir rung, whose
// checkout carries no .git of its own).
func restoreTreeAtCommit(ic *IsolatedCheckout, baseDir, hash string) error {
	switch ic.Rung {
	case RungSystemGitWorktree:
		ctx, cancel := context.WithTimeout(context.Background(), isolationCmdTimeout)
		defer cancel()
		// #nosec G204 -- ic.Dir is an engine-controlled isolation-checkout
		// path and hash is a validated 40-char hex commit id (see the
		// openSystemGitWorktree exec comment for the exec-policy scope).
		cmd := exec.CommandContext(ctx, "git", "-C", ic.Dir, "checkout", "--detach", hash)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("gitevidence: git checkout --detach %s in worktree %s: %w (%s)", hash, ic.Dir, err, strings.TrimSpace(string(out)))
		}
		return nil
	case RungGoGitClone:
		repo, err := git.PlainOpen(ic.Dir)
		if err != nil {
			return fmt.Errorf("gitevidence: open clone %s for restore: %w", ic.Dir, err)
		}
		return checkoutHash(repo, ic.Dir, hash)
	case RungSubdir:
		repo, err := git.PlainOpen(baseDir)
		if err != nil {
			return fmt.Errorf("gitevidence: open base repo %s for subdir restore: %w", baseDir, err)
		}
		return materializeCommitTree(repo, hash, ic.Dir)
	default:
		return fmt.Errorf("gitevidence: cannot restore commit %s on %s", hash, ic.Rung)
	}
}

// checkoutHash switches an already-open repo's working tree to hash (used
// for the go-git clone rung; the clone is fresh and clean, so no force is
// needed — a dirty tree here would indicate engine misuse, and the error is
// honestly surfaced rather than force-overwritten).
func checkoutHash(repo *git.Repository, dir, hash string) error {
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("gitevidence: worktree for %s: %w", dir, err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(hash)}); err != nil {
		return fmt.Errorf("gitevidence: checkout commit %s in %s: %w", hash, dir, err)
	}
	return nil
}

// materializeCommitTree writes every file of commit hash's tree into
// targetDir (the subdir rung, whose checkout has no .git and therefore no
// native checkout operation). Regular files are restored content-and-rough-
// mode (0600, or 0700 when git recorded the executable bit); symlinks are
// recreated as symlinks, matching what `git checkout` would produce. Any
// other entry kind (submodules/gitlinks) fails closed — a silently partial
// restore would produce a tree that does NOT match the recorded commit,
// which is exactly what this function exists to prevent.
func materializeCommitTree(repo *git.Repository, hash, targetDir string) error {
	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return fmt.Errorf("gitevidence: resolve commit %s: %w", hash, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return fmt.Errorf("gitevidence: tree for commit %s: %w", hash, err)
	}
	return tree.Files().ForEach(func(f *object.File) error {
		dest := filepath.Join(targetDir, filepath.FromSlash(f.Name))
		switch f.Mode {
		case filemode.Regular, filemode.Executable:
			perm := os.FileMode(0o600)
			if f.Mode == filemode.Executable {
				perm = 0o700
			}
			content, err := f.Contents()
			if err != nil {
				return fmt.Errorf("gitevidence: read blob %s at %s: %w", f.Name, hash, err)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return fmt.Errorf("gitevidence: mkdir for %s: %w", dest, err)
			}
			if err := os.WriteFile(dest, []byte(content), perm); err != nil {
				return fmt.Errorf("gitevidence: write %s: %w", dest, err)
			}
			return nil
		case filemode.Symlink:
			target, err := f.Contents()
			if err != nil {
				return fmt.Errorf("gitevidence: read symlink blob %s at %s: %w", f.Name, hash, err)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return fmt.Errorf("gitevidence: mkdir for %s: %w", dest, err)
			}
			if err := os.Symlink(target, dest); err != nil {
				return fmt.Errorf("gitevidence: recreate symlink %s -> %s: %w", dest, target, err)
			}
			return nil
		default:
			return fmt.Errorf("gitevidence: unsupported tree entry %s (mode %s) at commit %s — refusing a partial restore", f.Name, f.Mode, hash)
		}
	})
}

// RemoveIsolatedCheckout tears down a previously materialized isolated
// checkout WITHOUT needing its IsolatedCheckout handle (the handle is
// per-call; a later engine pass — e.g. the next Play replacing a member's
// resume tree — only knows the path). It applies the correct removal per
// mechanism: a system-git linked worktree is unregistered via
// `git worktree remove --force` (plus a prune for any stale administrative
// entry whose directory already vanished), and any remnant directory (a
// go-git clone, a subdir checkout, or a partial dir left by an interrupted
// materialization) is removed outright. Unknown/absent states are not
// errors — this is idempotent replace semantics.
func RemoveIsolatedCheckout(baseDir, targetDir string) error {
	if systemGitAvailable() {
		if _, statErr := os.Stat(filepath.Join(baseDir, ".git")); statErr == nil {
			ctx, cancel := context.WithTimeout(context.Background(), isolationCmdTimeout)
			// #nosec G204 -- engine-controlled paths, same scope as the
			// openSystemGitWorktree exec above.
			rmCmd := exec.CommandContext(ctx, "git", "-C", baseDir, "worktree", "remove", "--force", targetDir)
			if out, err := rmCmd.CombinedOutput(); err != nil {
				// Not fatal: targetDir may not be a registered worktree
				// (clone/subdir rung, or already half-removed). Log at
				// debug and let the RemoveAll below finish the job.
				logger.DebugCF("gitevidence", "worktree remove during isolated-checkout teardown (may be a non-worktree path)", map[string]any{
					"base_dir": baseDir, "target_dir": targetDir, "error": err.Error(), "output": strings.TrimSpace(string(out)),
				})
			}
			pruneCmd := exec.CommandContext(ctx, "git", "-C", baseDir, "worktree", "prune")
			if out, err := pruneCmd.CombinedOutput(); err != nil {
				logger.DebugCF("gitevidence", "worktree prune during isolated-checkout teardown", map[string]any{
					"base_dir": baseDir, "error": err.Error(), "output": strings.TrimSpace(string(out)),
				})
			}
			cancel()
		}
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("gitevidence: remove isolated checkout %s: %w", targetDir, err)
	}
	return nil
}
