// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/task"
)

// lintTailMember builds a well-formed tail member: every non-lint
// precondition validateCorrection checks (id, title, criteria) is satisfied,
// so a rejection can only come from plan-lint.
func lintTailMember(id string) task.Task {
	return task.Task{
		ID: id, Title: id, WorkspaceID: "ws", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{planProseCriterion(id + " work is done")},
	}
}
