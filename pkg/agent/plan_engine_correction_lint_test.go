// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// plan_engine_correction_lint_test.go — the ENGINE-side coverage for
// plan-lint on the correction path (H3 review finding).
//
// pkg/plan's lint_correction_test.go says what plan.LintCorrection decides. It
// does not say that anything CALLS it — every test there invokes the pure
// function directly, so deleting the three-line LintCorrection block from
// validateCorrection left the whole suite green and the fix silently absent.
// An unlinted member added to a running plan is precisely the live defect
// (member `epsilon`, is_join=false, converging four predecessors, two of them
// mutually parallel) that approve would have refused, so the wiring is the
// part that matters and it had no test at all.
//
// These tests drive AppendCorrection — the real entrypoint, with its real
// intent-log commit — and fail if that call is removed.

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
