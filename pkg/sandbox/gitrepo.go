// Package sandbox — nested-repo detection (ADR-053 MIN-6 / R§8.4).
//
// Before the git-evidence layer initializes its hidden repo at
// "<workspace>/work/.git", it must check whether the user already put a git
// repository at or ABOVE that work dir. If so, the user's repo WINS and the
// engine's repo is skipped — we never touch, re-init, or commit into a repo a
// human owns. That skip is a DEGRADED contract, and it must be SIGNALLED, not
// silent (MIN-6): the planner learns its isolation options are reduced, the
// Judge learns rung-1 (per-attempt diff) evidence is unavailable, and the owner
// gets a one-time notice.

package sandbox

import (
	"os"
	"path/filepath"
)

// JudgeRung1Unavailable is the exact signal handed to the Judge when no
// engine-owned boundary commits exist for a member (nested user repo, size-guard
// skip, or subdir-only rung). The Judge fails closed on the rung-1 diff channel
// and weights the other evidence rungs instead (R§8.4 / FR-155).
const JudgeRung1Unavailable = "rung-1 diffs unavailable"

// NestedRepoResult is the signalled degraded contract produced by
// DetectNestedRepo. It is a plain domain type (not a wire type); consumers
// (planner, Judge, workspace owner UI) translate the fields they need into
// their own contract schemas. Every field is populated with a real value — the
// zero value (Nested=false) means "no pre-existing repo; the engine may create
// its own".
type NestedRepoResult struct {
	// WorkDir is the canonical directory the engine intended to own
	// ("<workspace>/work"). Always set.
	WorkDir string

	// Nested is true when a pre-existing git repository was found at or above
	// WorkDir. When true, the engine's repo is skipped (OursSkipped).
	Nested bool

	// UserRepoRoot is the directory that contains the user's .git (the repo
	// root), and UserGitDir is that .git path itself. Empty when !Nested.
	UserRepoRoot string
	UserGitDir   string

	// OursSkipped is true whenever Nested is true — the user's repo wins and the
	// engine does not create or write its own repo. It mirrors Nested but is a
	// distinct field so a consumer reads intent explicitly.
	OursSkipped bool

	// PlannerNote tells the planner its isolation ladder is reduced: it must not
	// author a join/rung the runtime can no longer execute (R§8.4 / D10).
	PlannerNote string

	// JudgeContract is the exact string handed to the Judge — always
	// JudgeRung1Unavailable when Nested, empty otherwise (FR-155).
	JudgeContract string

	// OwnerNotice is the one-time human-facing notice for the workspace owner.
	OwnerNotice string

	// Reason is a machine-stable explanation of the decision (SEC-17 style).
	Reason string
}

// DetectNestedRepo walks from workDir up to the filesystem root looking for a
// pre-existing git repository (a ".git" directory OR a ".git" file — git
// worktrees and submodules use a ".git" file that points elsewhere). If one is
// found at or above workDir, it returns a signalled degraded contract in which
// the user's repo wins and the engine's repo is skipped.
//
// This MUST be called BEFORE the git-evidence layer creates its own repo. At
// that point any ".git" discovered is necessarily the user's (or a prior run's)
// — the engine's does not exist yet — so no exclusion of "our own .git" is
// needed or attempted.
//
// workDir does not need to exist yet; detection resolves the nearest existing
// ancestor and walks from there. A resolve failure returns a non-nested result
// with the error (fail-open on detection is acceptable here because the caller
// then attempts init, which fails loudly if the tree is truly unusable).
func DetectNestedRepo(workDir string) (NestedRepoResult, error) {
	canon, err := canonicalizePath(workDir)
	if err != nil {
		if abs, aerr := filepath.Abs(workDir); aerr == nil {
			canon = filepath.Clean(abs)
		} else {
			return NestedRepoResult{WorkDir: filepath.Clean(workDir)}, err
		}
	}

	res := NestedRepoResult{WorkDir: canon}

	dir := canon
	for {
		gitPath := filepath.Join(dir, ".git")
		if info, statErr := os.Lstat(gitPath); statErr == nil {
			// A .git directory OR a .git file both denote a repository here.
			if info.IsDir() || info.Mode().IsRegular() {
				res.Nested = true
				res.OursSkipped = true
				res.UserRepoRoot = dir
				res.UserGitDir = gitPath
				where := "at"
				if dir != canon {
					where = "above"
				}
				res.Reason = "git.nested.detected: pre-existing user repository " + where +
					" the work dir (" + gitPath + ") — user repo wins, engine repo skipped"
				res.PlannerNote = "isolation options reduced: a user-owned git repository exists " + where +
					" " + canon + "; do not author a rung/join that requires an engine-owned repo here"
				res.JudgeContract = JudgeRung1Unavailable
				res.OwnerNotice = "Omnipus detected your existing git repository at " + dir +
					" and will NOT create or modify its own evidence repo inside it. " +
					"Per-attempt git diffs (rung-1 Judge evidence) are unavailable for this workspace; " +
					"other evidence channels are used instead."
				return res, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding a repo.
			break
		}
		dir = parent
	}

	res.Nested = false
	res.Reason = "git.nested.none: no pre-existing repository at or above " + canon +
		"; engine may create its own evidence repo"
	return res, nil
}
