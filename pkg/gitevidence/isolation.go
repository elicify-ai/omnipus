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

// OpenIsolatedCheckoutAtRung is OpenIsolatedCheckout but starts the ladder
// at startRung instead of the top (e.g. a caller who already knows, from
// the SizeGuard media-bloat concern, that a full clone of this particular
// repo is too expensive can pass RungGoGitClone or RungSubdir directly to
// skip the higher rung(s) entirely). Rungs above startRung are never
// attempted. The subdir rung's only failure mode is its own os.MkdirAll,
// so this only returns an error when that fails too — there is no lower
// rung to degrade to.
func OpenIsolatedCheckoutAtRung(baseDir, targetDir string, startRung Rung) (*IsolatedCheckout, error) {
	var errs []error

	if startRung <= RungSystemGitWorktree {
		ic, err := openSystemGitWorktree(baseDir, targetDir)
		if err == nil {
			return ic, nil
		}
		errs = append(errs, err)
		logger.WarnCF("gitevidence", "isolation ladder: system-git worktree rung failed, degrading", map[string]any{
			"base_dir": baseDir, "target_dir": targetDir, "error": err.Error(),
		})
	}
	if startRung <= RungGoGitClone {
		ic, err := openGoGitClone(baseDir, targetDir)
		if err == nil {
			return ic, nil
		}
		errs = append(errs, err)
		logger.WarnCF("gitevidence", "isolation ladder: go-git clone rung failed, degrading", map[string]any{
			"base_dir": baseDir, "target_dir": targetDir, "error": err.Error(),
		})
	}

	ic, err := openSubdir(baseDir, targetDir)
	if err != nil {
		errs = append(errs, err)
		return nil, fmt.Errorf("gitevidence: isolation ladder exhausted, even the subdir rung failed: %w", errors.Join(errs...))
	}
	return ic, nil
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
