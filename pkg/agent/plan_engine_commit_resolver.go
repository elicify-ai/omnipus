// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"os"

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
// Work-tree restore caveat (D10): this resolver resolves the resume BASELINE
// commit hash only. It does NOT check out / reset the member's working tree to
// that commit. In the current shared subdir-checkout model (one go-git repo per
// workspace, members isolated by write-set paths, not separate worktrees) a
// per-member `git checkout` would clobber sibling members' files. The committed
// work is already present in the shared working tree (a boundary commit
// snapshots, it does not remove files), so a resumed member continues from its
// prior committed progress regardless. A hard per-member work-tree reset
// requires the D10 per-member worktree isolation rung, which is not yet wired;
// until then the resolved hash is persisted on the task (ResumeFromCommit) as
// the resume baseline the worker turn / plan Judge consume.
type LastMemberCommitResolver struct {
	taskStore *task.Store
	home      string
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
	if r == nil || r.taskStore == nil || r.home == "" || taskID == "" {
		return "", nil
	}
	t, err := r.taskStore.Get(taskID)
	if err != nil {
		// Task deleted between Play and resolve, or store unreadable — there
		// is no workspace to resolve a repo for. Degrade to fresh attempt.
		logger.WarnCF("plan_engine", "member resume: could not load task for commit resolve — fresh attempt",
			map[string]any{"plan_id": planID, "task_id": taskID, "error": err.Error()})
		return "", nil
	}
	if t.WorkspaceID == "" {
		return "", nil
	}
	dir := workspace.WorkDir(r.home, t.WorkspaceID)

	// Do NOT materialize a work dir as a side effect of Play: if the work dir
	// was never created (no member ever ran here), there is no evidence repo
	// to resume from — fresh attempt, same as a never-committed member.
	if _, statErr := os.Stat(dir); statErr != nil {
		return "", nil
	}
	repo, err := gitevidence.Open(dir)
	if err != nil {
		if errors.Is(err, gitevidence.ErrNestedRepo) {
			logger.WarnCF("plan_engine", "member resume: nested-repo degrade — fresh attempt",
				map[string]any{"plan_id": planID, "task_id": taskID, "workspace_id": t.WorkspaceID, "dir": dir})
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
