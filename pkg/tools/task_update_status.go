// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// task_update_status.go — update_task status values (tool-parity spec r17
// §7.2 TK-14, D-11). The six statuses exist; the tool sets inbox, next, done
// and failed. in_progress stays dispatch-only (#593, checked in
// buildPatchFields) and blocked engine-only. Every transition is the store's
// (validateTransition): done is terminal, failed may return to next or inbox,
// and the store's own message is the refusal.

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// foreignReadyRefusalText is the §7.11 refusal for a transition to next on a
// task whose workspace differs from the caller's turn workspace.
const foreignReadyRefusalText = "This task runs in another workspace: only its assignee there or a person " +
	"can make it ready to run. Nothing was changed."

// checkStatusAuthority refuses the statuses this tool never sets and the
// foreign activation of a task (TK-14 r7, R4-MAJ-003): moving a task to next
// makes it dispatchable, which is the receiving workspace's own pickup. A turn
// with no workspace is unaffected.
func (tu *taskUpdateToolExecute) checkStatusAuthority(st task.Status) (*ToolResult, bool) {
	if st == task.StatusBlocked && tu.existing.Status != task.StatusBlocked {
		return ErrorResult("status \"blocked\" is set only by the engine when a dependency is unmet; " +
			"it cannot be set with update_task"), true
	}
	if st != task.StatusNext || tu.existing.Status == task.StatusNext {
		return nil, false
	}
	ownWS := strings.TrimSpace(ToolWorkspaceID(tu.ctx))
	if ownWS != "" && tu.existing.WorkspaceID != "" && tu.existing.WorkspaceID != ownWS {
		return ErrorResult(foreignReadyRefusalText), true
	}
	return nil, false
}
