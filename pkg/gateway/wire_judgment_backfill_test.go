// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression for the UAT-found bug (2026-09-07): a task/plan criterion persisted
// before the ADR-080 `judgment` field carries an empty judgment, and the task/plan
// list responses (toWireCriteria / plan DoD) emitted it verbatim as "" — which the
// SPA's generated zod schema (judgment enum {boolean,quantitative,artifact},
// required on the response) rejects, breaking the whole Tasks screen on upgrade.
// wireCriterionJudgment backfills via task.InferJudgment so a legacy criterion is
// never returned schema-invalid.

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestWireCriterionJudgment_BackfillsLegacyEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   task.AcceptanceCriterion
		want task.JudgmentKind
	}{
		{"legacy prose, no judgment -> boolean", task.AcceptanceCriterion{Kind: task.KindProse, Text: "the script reads well", Judgment: ""}, task.JudgmentBoolean},
		{"legacy check, no judgment -> boolean", task.AcceptanceCriterion{Kind: task.KindCheck, Text: "tests pass", Judgment: "", Check: &task.CriterionCheck{Command: "go test ./...", ExpectedExitCode: 0}}, task.JudgmentBoolean},
		{"legacy behavior, no judgment -> quantitative", task.AcceptanceCriterion{Kind: task.KindBehavior, Text: "used the tool", Judgment: "", Behavior: &task.CriterionBehavior{Tool: "bash"}}, task.JudgmentQuantitative},
		{"explicit artifact preserved", task.AcceptanceCriterion{Kind: task.KindProse, Text: "a file exists", Judgment: task.JudgmentArtifact}, task.JudgmentArtifact},
		{"explicit quantitative preserved", task.AcceptanceCriterion{Kind: task.KindProse, Text: "at least 3 items", Judgment: task.JudgmentQuantitative}, task.JudgmentQuantitative},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wireCriterionJudgment(tc.in)
			if got != tc.want {
				t.Fatalf("wireCriterionJudgment = %q, want %q", got, tc.want)
			}
			if got == "" {
				t.Fatal("wireCriterionJudgment must never return empty — the wire enum is required")
			}
		})
	}
}
