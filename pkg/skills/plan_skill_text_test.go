// Omnipus — embedded plan SKILL.md prose regression test
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

import (
	"strings"
	"testing"
)

// planSkillText returns the plan skill exactly as it is compiled into the
// binary — the bytes PlanSupervisor and every other granted agent actually
// read. Reading the embed (not the file on disk) is the point: SeedDefaults
// never overwrites an existing skill dir, so the embed is the only copy whose
// content this repo controls.
func planSkillText(t *testing.T) string {
	t.Helper()
	b, err := embeddedSkills.ReadFile(embeddedRoot + "/plan/SKILL.md")
	if err != nil {
		t.Fatalf("read embedded plan SKILL.md: %v", err)
	}
	return string(b)
}

// TestPlanSkillEmbeddedText_AgreesWithCorrectionRules is the regression guard
// no compiler can provide. The skill is a go:embed'ed prompt, so a rule that
// contradicts the code it describes builds, ships, and is only discovered when
// an adjudicator follows the prompt and gets its correction rejected.
//
// Each assertion below pairs a line of prose with the enforcement that makes
// it true or false — not with a style preference.
func TestPlanSkillEmbeddedText_AgreesWithCorrectionRules(t *testing.T) {
	t.Parallel()
	text := planSkillText(t)

	// --- retired vocabulary and contradicted rules ------------------------
	absent := []struct {
		phrase string
		why    string
	}{
		{
			phrase: "awaiting_owner_correction",
			why: "the phase was renamed to awaiting_supervision; an adjudicator told to " +
				"wait for a phase that no longer exists waits forever",
		},
		{
			phrase: "awaiting owner correction",
			why:    "same retired phase, prose spelling",
		},
		{
			phrase: `forked "Planner" agent`,
			why: "the anti-forked-Planner instruction was rewritten; PlanSupervisor is " +
				"granted this same skill rather than carrying a clone of it (ADR-055)",
		},
		{
			phrase: "Optionally append a replacement",
			why: "supersede REQUIRES paired replacement work — plan_correct rejects a bare " +
				"supersede outright, so 'optionally' produces a rejected call every time",
		},
	}
	for _, a := range absent {
		if strings.Contains(text, a.phrase) {
			t.Errorf("plan SKILL.md still contains %q — %s", a.phrase, a.why)
		}
	}

	// --- rules the adjudicator must be able to read ----------------------
	present := []struct {
		phrase string
		why    string
	}{
		{
			phrase: "every acceptance criterion of the superseded member",
			why: "the FR-030b pairing/inheritance rule. Without it in the prompt, the " +
				"adjudicator's most natural supersede is the one the tool rejects",
		},
		{
			phrase: "**ABANDON**",
			why: "the honest exit must appear in the verb table the adjudicator chooses " +
				"from. A verb that exists only in the rubric leaves an unreachable DoD " +
				"looping until the supervision ceiling mislabels it supervision_unavailable",
		},
		{
			phrase: "awaiting_supervision",
			why:    "the current parked phase name",
		},
	}
	for _, p := range present {
		if !strings.Contains(text, p.phrase) {
			t.Errorf("plan SKILL.md is missing %q — %s", p.phrase, p.why)
		}
	}

	// The SUPERSEDE row must state the requirement, not merely mention
	// replacement work somewhere else in the document.
	row := supersedeVerbRow(text)
	if row == "" {
		t.Fatal("plan SKILL.md has no SUPERSEDE row in the correction-verb table")
	}
	for _, must := range []string{"REQUIRES", "criterion of the superseded member"} {
		if !strings.Contains(row, must) {
			t.Errorf("the SUPERSEDE verb row does not state the pairing requirement (%q missing):\n%s", must, row)
		}
	}
}

// supersedeVerbRow returns the correction-verb table row for SUPERSEDE, or ""
// when there is none.
func supersedeVerbRow(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(line, "**SUPERSEDE**") {
			return line
		}
	}
	return ""
}
