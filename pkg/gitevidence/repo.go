// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// Repo is a hidden go-git evidence repository rooted at a workspace's
// work/ directory (FR-151). It is safe for concurrent use: all mutating
// operations (Commit) are serialized by an internal mutex — "one writer" —
// so that parallel plan members sharing one checkout (the subdir isolation
// rung) cannot corrupt the index (FR-151's "serialize commits").
type Repo struct {
	dir  string
	repo *git.Repository

	mu sync.Mutex

	guard   SizeGuard
	redact  func(string) string
	scanner *audit.SecretScanner
	now     func() time.Time
}

// Dir returns the absolute path this Repo is rooted at (the workspace's
// work/ directory).
func (r *Repo) Dir() string { return r.dir }

// Option configures a Repo at Open time.
type Option func(*Repo)

// WithSizeGuard overrides the default SizeGuard (OBS-3). See
// DefaultSizeGuard's doc comment for the media-bloat retention note this
// guard exists to cover.
func WithSizeGuard(g SizeGuard) Option {
	return func(r *Repo) { r.guard = g }
}

// WithRedactor installs the sensitive-value-registry scan (FR-153/MIN-5)
// used to exclude a write-set path from a boundary commit when its content
// matches a registered secret. f is typically wired from
// config.Config.SensitiveDataReplacer() at the gateway boot seam, mirroring
// pkg/task.EvidenceStore.Redact — see that type's doc comment for the
// precedent. f should return its input UNCHANGED when no secret is
// present; a nil Option (the default) disables the scan (e.g. test
// harnesses with no registered secrets).
//
// Superseded by WithSecretScanner (DoD-11 — reconciled to ONE guard on
// commit, not two): when both are supplied to the same Repo, WithSecretScanner
// wins and this redactor is never consulted — see (*Repo).fileHasSecret. Kept
// for callers that only have a raw config.Config.SensitiveDataReplacer() and
// no *audit.SecretScanner instance; a caller with the fuller scanner should
// prefer WithSecretScanner, which also catches runtime-obtained secrets via
// pkg/audit's format-pattern layer (sk-…, ghp_…, JWTs, AWS keys, …), not just
// the credential-store registry this callback alone can see.
func WithRedactor(f func(string) string) Option {
	return func(r *Repo) { r.redact = f }
}

// WithSecretScanner installs the fuller MIN-5 secret-detection engine
// (pkg/audit.SecretScanner — credential-store registry AND format-pattern
// layers, ADR-053 D17/MIN-5) as this Repo's ONE secret guard on commit
// (DoD-11: reconciles the two secret-detection mechanisms this package and
// pkg/audit each independently offer into a single call path — see
// (*Repo).fileHasSecret). s is typically constructed once via
// audit.NewSecretScanner(cfg.SensitiveDataReplacer(), extraPatterns) at the
// gateway boot seam and reused across every workspace's Repo. A nil s is a
// no-op (matches WithRedactor's nil-disables convention).
func WithSecretScanner(s *audit.SecretScanner) Option {
	return func(r *Repo) { r.scanner = s }
}

// WithClock overrides the commit-timestamp clock. Test-only hook; real
// callers should never need this.
func WithClock(now func() time.Time) Option {
	return func(r *Repo) { r.now = now }
}

// Open is the work/-dir auto-init hook (FR-151): it idempotently ensures a
// hidden go-git repository exists at dir, creating it on first call and
// re-opening it (recognized via a marker file written at init time, see
// nested.go) on every subsequent call for the same dir.
//
// If a pre-existing, non-Omnipus git repository is found AT dir (the
// directory already has a `.git` with no Omnipus marker — e.g. the operator
// cloned their own project directly into work/) or ABOVE dir (an ancestor
// directory is itself a git working tree — e.g. OMNIPUS_HOME lives inside
// the operator's own repo), Open does NOT initialize anything and returns
// ErrNestedRepo wrapping the path where the foreign repo was found. This is
// the signalled-degraded contract (MIN-6): callers MUST treat ErrNestedRepo
// as "no git evidence layer for this workspace", not as a fatal error —
// the caller's own work/ directory creation (e.g. os.MkdirAll) is
// unaffected; only the git layer is skipped.
func Open(dir string, opts ...Option) (*Repo, error) {
	if dir == "" {
		return nil, fmt.Errorf("gitevidence: empty repo dir")
	}
	if ancestor, found, err := ancestorHasGit(dir); err != nil {
		return nil, fmt.Errorf("gitevidence: probe ancestors of %s for a nested repo: %w", dir, err)
	} else if found {
		return nil, fmt.Errorf("%w: found at %s", ErrNestedRepo, ancestor)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("gitevidence: ensure repo dir %s: %w", dir, err)
	}

	present, err := hasDotGit(dir)
	if err != nil {
		return nil, fmt.Errorf("gitevidence: stat %s/.git: %w", dir, err)
	}

	var gitRepo *git.Repository
	if present {
		if !hasMarker(dir) {
			return nil, fmt.Errorf("%w: existing non-Omnipus repo at %s", ErrNestedRepo, dir)
		}
		gitRepo, err = git.PlainOpen(dir)
		if err != nil {
			return nil, fmt.Errorf("gitevidence: open existing evidence repo at %s: %w", dir, err)
		}
	} else {
		gitRepo, err = git.PlainInit(dir, false)
		if err != nil {
			if errors.Is(err, git.ErrRepositoryAlreadyExists) {
				// Lost a benign init race against another process/goroutine
				// between our hasDotGit probe and PlainInit. Re-probe the
				// marker rather than assuming it's ours.
				if !hasMarker(dir) {
					return nil, fmt.Errorf("%w: existing non-Omnipus repo at %s", ErrNestedRepo, dir)
				}
				gitRepo, err = git.PlainOpen(dir)
				if err != nil {
					return nil, fmt.Errorf("gitevidence: open evidence repo at %s after init race: %w", dir, err)
				}
			} else {
				return nil, fmt.Errorf("gitevidence: init repo at %s: %w", dir, err)
			}
		} else if err := writeMarker(dir); err != nil {
			return nil, fmt.Errorf("gitevidence: write init marker at %s: %w", dir, err)
		} else {
			logger.InfoCF("gitevidence", "auto-initialized hidden evidence repo", map[string]any{"dir": dir})
		}
	}

	// D17 (FR-152 caveat 3): a repo produced purely via go-git is byte-for-
	// byte indistinguishable from one built by the real `git` binary, so an
	// agent with bash access could tamper with it directly (commit --amend,
	// rm -rf .git, …) unless the exec-chokepoint guard is armed for this
	// dir. Registering unconditionally here — not left to each Open caller
	// to remember — means every path that materializes/opens a work/ dir
	// (workspace.EnsureWorkDir today; any future direct caller, e.g. the
	// Play engine's commit orchestration) arms the same process-wide guard
	// (pkg/sandbox.DefaultGitGuard, already consulted by every
	// hardened_exec.Run call) the moment Omnipus's own evidence repo is
	// confirmed present. Deliberately only on the success path below — NOT
	// when ErrNestedRepo bailed out above — protecting a directory whose
	// .git belongs to the OPERATOR's own repo (MIN-6 degraded case) would
	// wrongly block the operator's own legitimate git commands against it.
	// Protect's only failure mode (filepath.Abs erroring) is vanishingly
	// rare and never worth failing Open's directory-creation contract over
	// (mirrors Open's own "the git layer degrades, the caller's directory
	// creation is unaffected" contract) — logged at WARN, not returned.
	if protectErr := sandbox.ProtectRepoWorkdir(dir); protectErr != nil {
		logger.WarnCF("gitevidence", "could not arm the exec-block guard for this evidence repo", map[string]any{
			"dir": dir, "error": protectErr.Error(),
		})
	}

	r := &Repo{
		dir:    dir,
		repo:   gitRepo,
		guard:  DefaultSizeGuard(),
		now:    time.Now,
		redact: nil,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Head returns the current HEAD commit hash, or "" with a nil error when
// the repository has no commits yet (an unborn HEAD — plumbing.ErrReferenceNotFound).
func (r *Repo) Head() (string, error) {
	ref, err := r.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("gitevidence: head: %w", err)
	}
	return ref.Hash().String(), nil
}
