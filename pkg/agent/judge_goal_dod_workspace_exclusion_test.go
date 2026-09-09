// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_goal_dod_workspace_exclusion_test.go is the required INV-4
// regression test named by the code-review fix-wave: goal_compile_llm.go's
// own doc comment (ADR-080 D-CONTEXT2) asserts "The Judge is NOT touched by
// this file; it never receives workspace instructions (INV-4 ...)" — the
// compile turn gets the operator's AUTHORITATIVE workspace/project
// instructions (buildWorkspaceInstructionsNote) so it can derive a
// workspace-provenance DoD layer, but that trusted-context injection is a
// COMPILE-time-only seam. This test proves buildJudgeUserContent
// (judge.go) — the Judge's own input assembly — never emits that heading,
// even when judging a goal whose DoD carries a workspace-derived item.
package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// judgeWorkspaceInstructionsHeading is the exact AUTHORITATIVE-context
// heading buildGoalCompileMessages (goal_compile_llm.go) injects into the
// COMPILE turn only — INV-4 requires it never reach the Judge.
const judgeWorkspaceInstructionsHeading = "AUTHORITATIVE workspace/project instructions"

// TestBuildJudgeUserContent_INV4_NeverReceivesWorkspaceInstructionsHeading
// judges a goal whose criteria (Criteria UNION DoD, exactly what
// compiledGoalCriteriaFor feeds into adjudication) include a
// workspace-provenance DoD item — the one case the ADR-080 D-CONTEXT2
// comment calls out by name — and asserts the Judge's assembled input
// carries NO trace of the compile-time AUTHORITATIVE workspace-instructions
// heading. extraContext is deliberately empty here (a real workspace-note
// injection would only ever happen through that parameter on some OTHER
// call site, never on the Judge's own dispatch) so this test proves the
// exclusion is structural — buildJudgeUserContent has no code path that
// could add it — not just an accident of what this call happened to pass.
func TestBuildJudgeUserContent_INV4_NeverReceivesWorkspaceInstructionsHeading(t *testing.T) {
	criteria := []task.AcceptanceCriterion{
		{ID: "c1", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
			Text: "the launch post is written"},
		// The judged-set union (compiledGoalCriteriaFor) folds Goal.DoD
		// straight into the criteria list the Judge scores — including a
		// workspace-provenance item, exactly the layer D-CONTEXT2 derives
		// FROM the workspace instructions at compile time.
		{ID: "d1", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
			Provenance: task.ProvenanceWorkspace,
			Text:       "commits follow the project's conventional-commit style"},
	}
	evidence := []task.EvidenceRecord{
		{ID: "ev1", TaskID: "t1", CriterionID: "c1", Attempt: 1, Command: "go build ./...", ExitCode: 0, Output: "ok"},
	}

	content, err := buildJudgeUserContent(criteria, evidence,
		"CLAIM-SENTINEL: done", "" /* extraContext */, "WINDOW-SENTINEL", "DIFF-SENTINEL")
	if err != nil {
		t.Fatalf("buildJudgeUserContent: %v", err)
	}

	if strings.Contains(content, judgeWorkspaceInstructionsHeading) {
		t.Fatalf("INV-4 violated: the Judge's assembled input contains the compile-time-only "+
			"AUTHORITATIVE workspace-instructions heading, want it excluded entirely:\n%s", content)
	}
	// Sanity: the workspace-provenance DoD criterion IS in the judged set
	// (its text/id ride the criteria JSON) — the exclusion is specifically
	// of the INSTRUCTIONS TEXT/heading, not of the criterion itself.
	if !strings.Contains(content, "d1") || !strings.Contains(content, "conventional-commit") {
		t.Fatalf("setup: the workspace-provenance DoD criterion must still be part of the judged criteria:\n%s", content)
	}
}
