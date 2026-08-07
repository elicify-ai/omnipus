// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// LastMemberCommitResolver is the gitevidence-backed commitResolver (D13/G-12).
// It maps (planID, taskID) -> the member task's WorkspaceID -> that workspace's
// hidden evidence repo (workspace.WorkDir) -> the most recent boundary commit
// naming task=<taskID> (gitevidence.Repo.LastCommitForTask).
//
// Per-workspace degrade (FR-155/MIN-6): a workspace whose work/ dir lives under
// a non-Omnipus git repo (gitevidence.ErrNestedRepo), whose work/ dir was never
// materialized, or whose repo simply has no boundary commit for this member
// yields "" -> the engine takes the fresh-attempt path. The resolver is wired
// unconditionally at boot and degrades PER WORKSPACE, so a valid evidence repo
// on one workspace is never masked by a nested-repo degrade on another.
//
// Work-tree restore (D13/#537): ResetMemberCheckout materializes the member's
// resume working tree at the resolved commit via the gitevidence isolation
// ladder (OpenIsolatedCheckoutAtCommit — the D10 rungs), at the deterministic
// path workspaces/<ws>/resume/<taskID> (OUTSIDE the evidence repo's work/ dir,
// so the materialized tree never pollutes the repo it was restored from). The
// path is deterministic by (home, workspace, task) so downstream consumers
// (worker turn, plan Judge) can locate it without a new persisted field. A
// materialization failure degrades the member to the shared-tree resume — the
// resolved hash is still persisted as ResumeFromCommit and the committed work
// remains present in the shared tree (a boundary commit snapshots, it does not
// remove files) — so the failure is never fatal to Play.
type LastMemberCommitResolver struct {
	taskStore *task.Store
	home      string
}

// resumeDirName is the per-workspace directory (workspaces/<ws>/resume/) under
// which per-member Play-from-commit checkout trees are materialized.
const resumeDirName = "resume"

// memberResumeDir returns the deterministic resume-checkout path for a plan
// member: workspaces/<wsID>/resume/<taskID>. Both IDs are validated against
// path escape (the workspace ID via workspace.SafeWorkspaceDir; the task ID
// with the same rule — it comes from the task store, not the wire, but a
// corrupt record must never become a traversal primitive).
func memberResumeDir(home, wsID, taskID string) (string, error) {
	wsDir, err := workspace.SafeWorkspaceDir(home, wsID)
	if err != nil {
		return "", err
	}
	if taskID == "" || strings.ContainsAny(taskID, "/\\") || strings.Contains(taskID, "..") {
		return "", fmt.Errorf("gitevidence resume checkout: unsafe task id %q", taskID)
	}
	return filepath.Join(wsDir, resumeDirName, taskID), nil
}

// NewLastMemberCommitResolver constructs a gitevidence-backed commitResolver
// for Play-from-commit (D13/G-12). home is the Omnipus home directory under
// which workspaces/<id>/work/ is resolved. The boot seam (pkg/gateway) supplies
// both the live task store and homePath.
//
// A nil taskStore or empty home yields a resolver whose LastMemberCommit always
// returns ("", nil) — i.e. fresh attempt — so a minimal test harness that
// forgets to wire them degrades safely rather than nil-derefing.
func NewLastMemberCommitResolver(taskStore *task.Store, home string) *LastMemberCommitResolver {
	return &LastMemberCommitResolver{taskStore: taskStore, home: home}
}

// LastMemberCommit implements commitResolver. It is the single call the plan
// engine makes during Play to resolve a member's resume baseline.
func (r *LastMemberCommitResolver) LastMemberCommit(planID, taskID string) (string, error) {
	dir, ok, err := r.memberEvidenceDir(planID, taskID)
	if err != nil || !ok {
		return "", err
	}
	repo, err := gitevidence.Open(dir)
	if err != nil {
		if errors.Is(err, gitevidence.ErrNestedRepo) {
			logger.WarnCF("plan_engine", "member resume: nested-repo degrade — fresh attempt",
				map[string]any{"plan_id": planID, "task_id": taskID, "dir": dir})
			return "", nil
		}
		return "", err
	}
	hash, err := repo.LastCommitForTask(taskID)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// memberEvidenceDir resolves the member's workspace evidence-repo dir. ok is
// false (with nil error) for every fresh-attempt degrade: nil resolver fields,
// unloaded task, no workspace, or a work dir that was never materialized —
// which is deliberately NOT created as a side effect of Play.
//
// On a stat error, only os.ErrNotExist is treated as the documented
// "unmaterialized work dir" fresh-attempt degrade. Every other error
// (permission, I/O, ELOOP, ENOTDIR, EACCES, broken symlink) is logged as
// a WARN and returned as a wrapped error so the caller's "degrade to fresh
// attempt" path can log the real reason — fixes the silent-failure pattern
// where any stat error looked like an unmaterialized work dir.
func (r *LastMemberCommitResolver) memberEvidenceDir(planID, taskID string) (dir string, ok bool, err error) {
	if r == nil || r.taskStore == nil || r.home == "" || taskID == "" {
		return "", false, nil
	}
	t, err := r.taskStore.Get(taskID)
	if err != nil {
		// Task deleted between Play and resolve, or store unreadable — there
		// is no workspace to resolve a repo for. Degrade to fresh attempt.
		logger.WarnCF("plan_engine", "member resume: could not load task for commit resolve — fresh attempt",
			map[string]any{"plan_id": planID, "task_id": taskID, "error": err.Error()})
		return "", false, nil //nolint:nilerr
	}
	if t.WorkspaceID == "" {
		return "", false, nil
	}
	dir = workspace.WorkDir(r.home, t.WorkspaceID)
	if _, statErr := os.Stat(dir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "", false, nil
		}
		// Any other stat error (permission, ELOOP, broken symlink, ENOTDIR,
		// EACCES, I/O) is NOT the documented "unmaterialized" case — surface
		// it so the caller can log the real reason instead of degrading
		// silently.
		logger.WarnCF("plan_engine", "member resume: workspace work dir stat failed — fresh attempt",
			map[string]any{"plan_id": planID, "task_id": taskID, "dir": dir, "error": statErr.Error()})
		return "", false, fmt.Errorf("plan_engine: member resume: stat work dir %s: %w", dir, statErr)
	}
	return dir, true, nil
}

// ResetMemberCheckout implements commitResolver (D13/#537): it materializes
// the member's resume working tree at hash via the gitevidence isolation
// ladder (gitevidence.OpenIsolatedCheckoutAtCommit — the shipped D10 rungs),
// replacing any prior resume tree for the member, and returns the checkout
// directory (the deterministic workspaces/<ws>/resume/<taskID> path).
//
// hash == "" is the fresh-attempt case: any stale resume tree from a previous
// Play is removed and "" is returned — the fresh-attempt path leaves no tree
// behind. All no-evidence degrades (nil resolver fields, unloaded task, no
// workspace, unmaterialized work dir) also return ("", nil) after best-effort
// stale-tree cleanup where the path is still derivable.
//
// A materialization failure returns a non-nil error; the caller (Play) treats
// it as non-fatal and degrades the member to the shared-tree resume with the
// baseline hash still persisted.
func (r *LastMemberCommitResolver) ResetMemberCheckout(planID, taskID, hash string) (string, error) {
	if r == nil || r.taskStore == nil || r.home == "" || taskID == "" {
		return "", nil
	}
	t, err := r.taskStore.Get(taskID)
	if err != nil {
		logger.WarnCF("plan_engine", "member resume checkout: could not load task — no tree materialized",
			map[string]any{"plan_id": planID, "task_id": taskID, "error": err.Error()})
		return "", nil //nolint:nilerr
	}
	if t.WorkspaceID == "" {
		return "", nil
	}
	target, err := memberResumeDir(r.home, t.WorkspaceID, taskID)
	if err != nil {
		return "", err
	}
	workDir, ok, err := r.memberEvidenceDir(planID, taskID)
	if err != nil {
		return "", err
	}

	// Replace semantics: tear down any prior resume tree for this member
	// first, so a re-Play never stacks checkouts and a fresh attempt leaves
	// no tree behind. For the no-evidence degrade (ok == false) this cleanup
	// is all we do — the removal needs no repo handle (RemoveIsolatedCheckout
	// guards on the base repo's .git itself).
	if _, statErr := os.Stat(target); statErr == nil {
		if rmErr := gitevidence.RemoveIsolatedCheckout(workDir, target); rmErr != nil {
			return "", fmt.Errorf("plan_engine: member resume checkout: remove stale tree %s: %w", target, rmErr)
		}
	}
	if !ok || hash == "" {
		return "", nil
	}

	ic, err := gitevidence.OpenIsolatedCheckoutAtCommit(workDir, target, hash)
	if err != nil {
		return "", fmt.Errorf("plan_engine: member resume checkout at %s: %w", hash, err)
	}
	logger.InfoCF("plan_engine", "member resume: checkout materialized at commit",
		map[string]any{"plan_id": planID, "task_id": taskID, "commit": hash, "dir": ic.Dir, "rung": ic.Rung.String()})
	return ic.Dir, nil
}

// --- Producer half (E.4 / D13 / G-12) ------------------------------------
//
// Everything above is the CONSUMER of boundary commits. Without a producer it
// is inert: LastMemberCommit always resolves "" and Play silently degrades to
// a fresh attempt, which looks identical to a successful resume. The two are
// co-located deliberately so the pair cannot drift apart again.

// evidenceCommitter records a write-set-scoped boundary commit for a task that
// has just reached a terminal state. It is an interface so TaskExecutor can be
// driven by a fake in tests, and so a nil committer degrades gracefully (no
// evidence recorded) rather than nil-dereferencing.
type evidenceCommitter interface {
	// CommitTaskBoundary commits t's declared WriteSet into its workspace's
	// evidence repo. A nil CommitResult with a nil error means "nothing to
	// record" (not a failure) — see WorkspaceEvidenceCommitter for the cases.
	CommitTaskBoundary(t *task.Task) (*gitevidence.CommitResult, error)
}

// WorkspaceEvidenceCommitter is the gitevidence-backed evidenceCommitter. It
// mirrors LastMemberCommitResolver's resolution exactly — task -> WorkspaceID
// -> workspace.WorkDir -> hidden evidence repo — so the commit it writes is the
// one LastCommitForTask will later read back.
//
// The secret scanner is REQUIRED, not optional: gitevidence.Commit refuses a
// commit when no secret guard is configured (MIN-5 fail-closed), so a committer
// built without one would silently record nothing on every boundary. The
// constructor takes it explicitly to make that dependency impossible to forget.
type WorkspaceEvidenceCommitter struct {
	home    string
	scanner *audit.SecretScanner
}

// NewWorkspaceEvidenceCommitter constructs the boundary-commit producer. home
// is the Omnipus home under which workspaces/<id>/work/ resolves; scanner is
// the shared MIN-5 secret guard built once at the gateway boot seam.
//
// A nil scanner or empty home yields a committer whose CommitTaskBoundary is a
// no-op, so a partially-wired test harness degrades to "no evidence" rather
// than failing a task that otherwise succeeded.
func NewWorkspaceEvidenceCommitter(home string, scanner *audit.SecretScanner) *WorkspaceEvidenceCommitter {
	return &WorkspaceEvidenceCommitter{home: home, scanner: scanner}
}

// CommitTaskBoundary implements evidenceCommitter. It returns (nil, nil) for
// every legitimate "nothing to record" case: nil receiver/task, unwired
// home/scanner, a task with no workspace, a task that declared no write set
// (the overwhelmingly common case — only plan MEMBERS carry one), or a
// workspace whose work dir was never materialized. A nested-repo workspace
// (FR-155/MIN-6) degrades the same way the resolver's read path does.
//
// Errors are returned, not swallowed: the caller logs them and continues, so a
// broken evidence repo never fails an otherwise-successful task.
func (c *WorkspaceEvidenceCommitter) CommitTaskBoundary(t *task.Task) (*gitevidence.CommitResult, error) {
	if c == nil || c.home == "" || c.scanner == nil || t == nil {
		return nil, nil
	}
	if t.WorkspaceID == "" || len(t.WriteSet) == 0 {
		return nil, nil
	}
	dir := workspace.WorkDir(c.home, t.WorkspaceID)
	if _, statErr := os.Stat(dir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			// Work dir never materialized — nothing to snapshot. Play will
			// take the documented fresh-attempt path.
			return nil, nil
		}
		return nil, fmt.Errorf("evidence commit: stat work dir %s: %w", dir, statErr)
	}
	repo, err := gitevidence.Open(dir, gitevidence.WithSecretScanner(c.scanner))
	if err != nil {
		if errors.Is(err, gitevidence.ErrNestedRepo) {
			logger.WarnCF("task_executor", "evidence commit: nested-repo degrade — no boundary commit",
				map[string]any{"task_id": t.ID, "dir": dir})
			return nil, nil
		}
		return nil, fmt.Errorf("evidence commit: open %s: %w", dir, err)
	}
	return repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{
		TaskID:    t.ID,
		AttemptID: strconv.Itoa(t.AttemptCount),
		AgentID:   t.AgentID,
	}, t.WriteSet)
}
