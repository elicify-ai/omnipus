// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_user_content_adr084_test.go is JUDGE-FR-002a's oracle (ADR-084
// revision 9, wave E9): buildJudgeUserContent's two empty-section
// fallbacks — an unreadable workspace diff, an empty transcript window —
// used to restate E1's deleted rubric prohibition ("judge from X below
// only") in the USER message, at exactly the moment ADR-084 D2 says the
// Judge should go looking instead. Both fallbacks must now name a real
// investigative next step, phrased so it holds for a non-coding goal too
// (JUDGE-FR-111): open the artifacts the criteria name, list the
// workspace, inspect the in-scope sessions.

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestBuildJudgeUserContent_EmptySectionsDirectInvestigation is the spec's
// own named oracle: neither empty-section fallback contains a confinement
// phrase, and each names a next step.
func TestBuildJudgeUserContent_EmptySectionsDirectInvestigation(t *testing.T) {
	criteria := []task.AcceptanceCriterion{
		{ID: "c1", Kind: task.KindProse, Text: "the quarterly report is accurate"},
	}

	// Both the diff and the window are empty, so both fallbacks render.
	content, err := buildJudgeUserContent(criteria, nil, "claim text", "", "", "")
	if err != nil {
		t.Fatalf("buildJudgeUserContent: %v", err)
	}

	confinementPhrases := []string{"below only", "judge from the"}
	for _, phrase := range confinementPhrases {
		if strings.Contains(content, phrase) {
			t.Errorf("empty-section fallback still contains a confinement phrase %q:\n%s", phrase, content)
		}
	}

	nextSteps := []string{"open the artifacts", "list the workspace", "inspect the in-scope sessions"}
	for _, step := range nextSteps {
		if !strings.Contains(content, step) {
			t.Errorf("empty-section fallback must name the next step %q; full content:\n%s", step, content)
		}
	}

	// FR-111: the rewritten fallback must not assume a coding goal by
	// naming "the diff" or "the tests" as the expected evidence.
	lower := strings.ToLower(content)
	for _, forbidden := range []string{"the diff", "the tests"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("empty-section fallback must not assume a coding goal (found %q):\n%s", forbidden, content)
		}
	}
}

// TestBuildJudgeUserContent_DiffFallback_KeepsInfrastructureGapFraming
// proves FR-002a's retained clause: the diff fallback must still say this
// is an infrastructure gap, not a signal that no work happened — only the
// "judge from X below" tail is rewritten, the leading framing survives.
func TestBuildJudgeUserContent_DiffFallback_KeepsInfrastructureGapFraming(t *testing.T) {
	criteria := []task.AcceptanceCriterion{{ID: "c1", Kind: task.KindProse, Text: "x"}}

	content, err := buildJudgeUserContent(criteria, nil, "claim", "", "some window text", "")
	if err != nil {
		t.Fatalf("buildJudgeUserContent: %v", err)
	}
	if !strings.Contains(content, "this is an infrastructure gap, not a signal that no work happened") {
		t.Errorf("diff fallback must keep the infrastructure-gap framing:\n%s", content)
	}
	if strings.Contains(content, "judge from the transcript window and machine-check results below") {
		t.Error("diff fallback still contains the old confinement tail")
	}
}

// TestBuildJudgeUserContent_PopulatedSections_NoFallbackText proves the
// fallback strings only render when their section is genuinely empty — a
// populated diff/window must never accidentally carry the fallback text
// alongside the real evidence.
func TestBuildJudgeUserContent_PopulatedSections_NoFallbackText(t *testing.T) {
	criteria := []task.AcceptanceCriterion{{ID: "c1", Kind: task.KindProse, Text: "x"}}
	content, err := buildJudgeUserContent(criteria, nil, "claim", "", "WINDOW-SENTINEL", "DIFF-SENTINEL")
	if err != nil {
		t.Fatalf("buildJudgeUserContent: %v", err)
	}
	if strings.Contains(content, "open the artifacts the criteria name") {
		t.Errorf("a populated diff/window must not also render the empty-section fallback text:\n%s", content)
	}
	if !strings.Contains(content, "WINDOW-SENTINEL") || !strings.Contains(content, "DIFF-SENTINEL") {
		t.Errorf("populated sections must render their real content:\n%s", content)
	}
}
