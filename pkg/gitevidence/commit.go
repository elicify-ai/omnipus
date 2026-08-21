// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Boundary identifies the kind of engine event that triggered an
// auto-commit (FR-151): a task boundary, an attempt boundary, or an agent
// boundary. It is folded into the commit message so a human (or the Judge)
// reading `git log` can see WHY a commit exists without external context.
type Boundary string

const (
	BoundaryTask    Boundary = "task"
	BoundaryAttempt Boundary = "attempt"
	BoundaryAgent   Boundary = "agent"
)

// CommitMeta identifies WHO/WHAT a boundary commit is for (FR-151: "message
// = task · attempt · agent"). Any field may be empty; empty fields render
// as "-" in the commit message rather than being omitted, so the message
// shape stays stable/parseable regardless of which fields a caller has.
type CommitMeta struct {
	TaskID    string
	AttemptID string
	AgentID   string
}

// CommitResult reports what a Commit call actually did. A Skipped result
// with a nil error is a normal, expected outcome (not a failure) — callers
// MUST check Skipped, not just the error, to decide whether evidence exists
// for this boundary.
type CommitResult struct {
	// Hash is the new commit's hex hash. Empty when Skipped.
	Hash string
	// Skipped is true when no commit was made — see SkipReason.
	Skipped bool
	// SkipReason explains why Skipped is true. One of:
	// "empty_write_set", "size_guard", "no_changes",
	// "all_paths_excluded_for_secrets".
	SkipReason string
	// Committed lists the concrete write-set files actually staged into
	// this commit (repo-relative, "/"-separated), sorted.
	Committed []string
	// Contention lists paths OUTSIDE the declared write-set that were
	// dirty in the working tree at commit time — i.e. another concurrent
	// member's half-written change on a shared (subdir-rung) checkout.
	// These are NEVER staged or committed by this call; they are surfaced
	// here so the caller can hand them to the Judge as contention evidence
	// (G-15/CRIT-2 — "never silently swallowed").
	Contention []string
	// ExcludedForSecret lists write-set files that were NOT staged because
	// their content matched the registered sensitive-value scan (MIN-5).
	ExcludedForSecret []string
	// ExcludedSymlink lists write-set entries that were NOT staged because
	// the path itself is a symlink (spec FR-5.5, ADR-063 D4): a workspace
	// mount is materialized as a symlink work/<name> -> host_path, and a
	// symlink must never be read-and-restaged as if it were the file/tree it
	// points at — that would either error out reading a directory (a
	// symlink to a directory) or, worse, silently stage the CONTENT of a
	// file living outside this repository entirely (a symlink to a file).
	// See the symlink guard in Commit's per-file staging loop for why the
	// exclusion happens at Lstat time, before the content is ever read.
	ExcludedSymlink []string
}

// Commit takes a write-set-scoped, serialized boundary commit (FR-151).
// Only the paths named by writeSet (files, or directories — expanded
// recursively) are staged; any OTHER dirty path in the working tree is left
// untouched and reported in the result's Contention list, never committed
// and never silently dropped. Concurrent callers on the same Repo are
// serialized by an internal mutex, so parallel plan members sharing one
// checkout cannot corrupt the index mid-stage.
//
// A write-set path whose content matches the registered sensitive-value
// scan — WithSecretScanner (the preferred/superseding engine, DoD-11) or
// the older WithRedactor callback — is excluded from the commit and
// reported in ExcludedForSecret — this package never stages a matching
// path, so a secret written and then deleted within the SAME boundary
// window never reaches history at all. See the package doc's caveat 3 for what is (and
// is not) covered once a secret HAS already reached a prior commit: this
// package does not rewrite history; the documented remediation is
// operational (rotate the credential; the evidence repo has no
// filter-branch/rebase primitive proven safe by the spike).
func (r *Repo) Commit(boundary Boundary, meta CommitMeta, writeSet []string) (*CommitResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := &CommitResult{}

	if len(writeSet) == 0 {
		result.Skipped = true
		result.SkipReason = "empty_write_set"
		return result, nil
	}

	// MAJOR-3 / MIN-5 (fail-closed): REFUSE to commit when NO secret guard
	// is configured. Previously fileHasSecret returned (false, "") on a nil
	// scanner AND nil redactor, so a Commit with no guard wired would stage
	// every file unconditionally — letting any secret pass straight into
	// history, which is the exact fail-open defect MIN-5 requires us to
	// refuse. Commit is the chokepoint that enforces the invariant: a
	// Phase-2 caller wiring this Repo can't "forget" the scanner, because
	// Commit itself rejects the call. Callers that legitimately want a
	// no-op scan (e.g. test harnesses, or an environment with no registered
	// secrets) pass WithRedactor(func(s string) string { return s }) — an
	// identity redactor that always reports "no secret" — which is a
	// deliberate, visible opt-in rather than a silent nil default.
	// WithSecretScanner still supersedes WithRedactor when both are set
	// (see fileHasSecret); this guard only requires that AT LEAST ONE be
	// non-nil.
	if r.scanner == nil && r.redact == nil {
		return nil, fmt.Errorf(
			"gitevidence: commit refused: no secret scanner or redactor configured (MIN-5 fail-closed) " +
				"— pass WithSecretScanner or WithRedactor to Open",
		)
	}

	expanded, err := expandWriteSet(r.dir, writeSet)
	if err != nil {
		return nil, err
	}
	declared := make(map[string]bool, len(expanded))
	for _, f := range expanded {
		declared[f.relPath] = true
	}

	wt, err := r.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("gitevidence: worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("gitevidence: status: %w", err)
	}
	dirty := make(map[string]bool, len(status))
	for file, st := range status {
		if st.Worktree == git.Unmodified && st.Staging == git.Unmodified {
			continue
		}
		clean := cleanSlashPath(file)
		dirty[clean] = true
		if !declared[clean] {
			result.Contention = append(result.Contention, clean)
		}
	}
	sort.Strings(result.Contention)

	// Only the declared write-set files git ACTUALLY sees as dirty are
	// candidates for staging: (a) a clean/already-committed file must not
	// be re-added (Worktree.Add succeeds on it but stages no real change,
	// which then makes wt.Commit fail with "cannot create empty commit");
	// (b) a path that was created and deleted again before ever being
	// tracked/committed has NOTHING for git status to report, so it never
	// reaches Worktree.Add at all — exactly the MIN-5 "never reaches
	// history" guarantee for a same-window write-then-delete.
	var toStage []expandedFile
	for _, f := range expanded {
		if dirty[f.relPath] {
			toStage = append(toStage, f)
		}
	}

	fileCount, totalBytes, err := sizeOfExpandedSet(r.dir, toStage)
	if err != nil {
		return nil, err
	}
	if r.guard.exceeds(fileCount, totalBytes) {
		result.Skipped = true
		result.SkipReason = fmt.Sprintf(
			"size_guard: write-set touches %d files/%d bytes, exceeds max %d files/%d bytes",
			fileCount, totalBytes, r.guard.MaxFiles, r.guard.MaxBytes,
		)
		logger.WarnCF("gitevidence", "boundary commit skipped: size guard tripped", map[string]any{
			"dir": r.dir, "boundary": string(boundary), "files": fileCount, "bytes": totalBytes,
			"max_files": r.guard.MaxFiles, "max_bytes": r.guard.MaxBytes,
		})
		return result, nil
	}

	for _, f := range toStage {
		abs := filepath.Join(r.dir, filepath.FromSlash(f.relPath))

		// Spec FR-5.5 (ADR-063 D4): a workspace mount is materialized as a
		// symlink work/<name> -> host_path. VERIFIED (not assumed — see the
		// package's git-status experiment this guard replaced): go-git's own
		// wt.Status() already treats a symlink as ONE atomic, opaque entry
		// and never walks through it, so a write-set path reaching INSIDE a
		// mount (e.g. "mymount/src/main.go") never shows up as dirty in the
		// first place and is naturally dropped before this loop ever sees
		// it. The one gap that verification found: a write-set entry naming
		// the mount's TOP-LEVEL symlink itself is NOT filtered upstream —
		// os.ReadFile below follows it, which either errors the WHOLE
		// Commit call ("is a directory", failing every other declared file
		// in the same boundary too) or, for a symlink-to-file target, would
		// silently read and stage bytes that live entirely outside this
		// repository. Checking Lstat here, before any read, closes both: the
		// symlink itself is excluded (never read, never staged) and every
		// other declared file in the same boundary still commits normally.
		if fi, lstatErr := os.Lstat(abs); lstatErr == nil && fi.Mode()&os.ModeSymlink != 0 {
			result.ExcludedSymlink = append(result.ExcludedSymlink, f.relPath)
			logger.WarnCF("gitevidence", "excluding write-set path from boundary commit: path is a symlink (e.g. a workspace mount) — its target is never read or staged", map[string]any{
				"dir": r.dir, "boundary": string(boundary), "path": f.relPath,
			})
			continue
		} else if lstatErr != nil && !os.IsNotExist(lstatErr) {
			return nil, fmt.Errorf("gitevidence: lstat write-set file %s: %w", f.relPath, lstatErr)
		}

		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				// A path git status reports dirty but that's absent on
				// disk is a deletion of a PREVIOUSLY TRACKED file (status
				// only reports paths it has some record of); Worktree.Add
				// stages that removal from the index.
				if _, addErr := wt.Add(f.relPath); addErr != nil {
					return nil, fmt.Errorf("gitevidence: stage deletion of %s: %w", f.relPath, addErr)
				}
				result.Committed = append(result.Committed, f.relPath)
				continue
			}
			return nil, fmt.Errorf("gitevidence: read write-set file %s: %w", f.relPath, readErr)
		}
		if tainted, detail := r.fileHasSecret(f.relPath, data); tainted {
			result.ExcludedForSecret = append(result.ExcludedForSecret, f.relPath)
			logger.WarnCF("gitevidence", "excluding write-set path from boundary commit: sensitive value detected", map[string]any{
				"dir": r.dir, "boundary": string(boundary), "path": f.relPath, "detail": detail,
			})
			continue
		}
		if _, addErr := wt.Add(f.relPath); addErr != nil {
			return nil, fmt.Errorf("gitevidence: stage %s: %w", f.relPath, addErr)
		}
		result.Committed = append(result.Committed, f.relPath)
	}
	sort.Strings(result.Committed)
	sort.Strings(result.ExcludedForSecret)
	sort.Strings(result.ExcludedSymlink)

	if len(result.Committed) == 0 {
		result.Skipped = true
		switch {
		case len(result.ExcludedForSecret) > 0:
			result.SkipReason = "all_paths_excluded_for_secrets"
		case len(result.ExcludedSymlink) > 0:
			result.SkipReason = "all_paths_excluded_as_symlinks"
		default:
			result.SkipReason = "no_changes"
		}
		return result, nil
	}

	when := time.Now
	if r.now != nil {
		when = r.now
	}
	sig := &object.Signature{
		Name:  "omnipus-gitevidence",
		Email: "gitevidence@omnipus.local",
		When:  when(),
	}
	hash, commitErr := wt.Commit(formatCommitMessage(boundary, meta), &git.CommitOptions{
		Author:    sig,
		Committer: sig,
	})
	if commitErr != nil {
		return nil, fmt.Errorf("gitevidence: commit: %w", commitErr)
	}
	result.Hash = hash.String()
	return result, nil
}

// fileHasSecret is the ONE MIN-5 secret-detection call path a boundary
// commit consults (DoD-11 — reconciling WithSecretScanner and WithRedactor
// into a single guard, not two independent checks that could disagree or
// double-scan). WithSecretScanner (pkg/audit.SecretScanner — credential-
// registry AND format-pattern layers) is preferred and supersedes
// WithRedactor when both are configured on the same Repo: only one of the
// two branches below ever runs for a given file, never both. detail is a
// human-readable, non-secret-leaking label suitable for logging (the
// SecretScanner branch reuses SecretFinding.Detail; the legacy redactor
// branch has no equivalent label to offer, since it only ever reported
// "changed or not").
func (r *Repo) fileHasSecret(relPath string, data []byte) (tainted bool, detail string) {
	if r.scanner != nil {
		findings := r.scanner.ScanBytes(relPath, data)
		if len(findings) == 0 {
			return false, ""
		}
		return true, findings[0].Detail
	}
	if r.redact != nil {
		s := string(data)
		if r.redact(s) != s {
			return true, "registered credential"
		}
	}
	return false, ""
}

func formatCommitMessage(b Boundary, m CommitMeta) string {
	return fmt.Sprintf("[%s] task=%s attempt=%s agent=%s", orDash(string(b)), orDash(m.TaskID), orDash(m.AttemptID), orDash(m.AgentID))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func sizeOfExpandedSet(root string, files []expandedFile) (count int, totalBytes int64, err error) {
	for _, f := range files {
		abs := filepath.Join(root, filepath.FromSlash(f.relPath))
		fi, statErr := os.Stat(abs)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				count++
				continue
			}
			return 0, 0, fmt.Errorf("gitevidence: stat %q: %w", f.relPath, statErr)
		}
		count++
		totalBytes += fi.Size()
	}
	return count, totalBytes, nil
}
