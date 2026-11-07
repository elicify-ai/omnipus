// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// task_creator_notice.go — the creator copy of a run's terminal notice (T10
// spec r6 FR-18, FR-19).
//
// When a run of a task completes, the agent that created the task receives
// one copy of the same full notice the run's captured recipients receive,
// through the same upward delivery. The rules:
//
//   - agent creators only: a task created by a person has no creator copy,
//     even when the person's username equals an agent id (CreatedByAgentID
//     is set only on the agent creation path);
//   - one notice per address: a creator that is already a captured recipient
//     (the assignee or the starter) gets nothing extra;
//   - the T8 workspace boundary: the copy goes to the creator's main in the
//     task's own workspace and is subject to the same send-time visibility
//     check as every recipient (recipientStillVisible). A creator that is not
//     an eligible main there — a foreign-workspace creator — gets no copy and
//     no result content; its status-only view is unchanged;
//   - an owed copy that cannot be resolved is recorded as a lost notice,
//     exactly like a captured recipient's;
//   - the creator is never added to the run's captured recipients, so the
//     stored run record is unchanged.
//
// Skipped occurrences, goal-loop retries and status changes made outside a
// run never reach the terminal delivery, so they never notify the creator.

import (
	"slices"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// noticeTargets returns the addresses a run's terminal notice is sent to: the
// run's captured recipients, then the creator copy when one is owed. run must
// not be nil.
func noticeTargets(t *task.Task, run *activeRun) []string {
	targets := slices.Clone(run.recipients)
	if creator := creatorNoticeAddress(t, run); creator != "" {
		targets = append(targets, creator)
	}
	return targets
}

// creatorNoticeAddress is the main session in the task's workspace of the
// agent that created t, or "" when no creator copy is owed: a person-created
// task, or a creator that is already a captured recipient.
func creatorNoticeAddress(t *task.Task, run *activeRun) string {
	if t.CreatedByAgentID == "" {
		return ""
	}
	address, err := session.MainSessionID(t.WorkspaceID, t.CreatedByAgentID)
	if err != nil {
		// Unreachable for a stored task: both ids were validated on write.
		logger.ErrorCF("task_executor", "creator notice: the creator's main session id cannot be formed",
			map[string]any{"task_id": t.ID, "workspace_id": t.WorkspaceID,
				"creator": t.CreatedByAgentID, "error": err.Error()})
		return ""
	}
	if slices.Contains(run.recipients, address) {
		return ""
	}
	return address
}
