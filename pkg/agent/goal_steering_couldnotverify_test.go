// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_steering_couldnotverify_test.go pins the worker feedback after a
// "could not verify" round (UAT: a worker read a judge-unavailable reason as
// work to redo and polled a verification task 174 times). The steer must (a)
// keep normal unmet reasons exactly as they were, and (b) when the Judge
// itself could not run its check, tell the worker NOT to re-verify or poll —
// for both non-judgment reason shapes the engine produces (unable_to_verify,
// criterion_unjudgeable).
package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestGoalVerdictReasonText_SeparatesCouldNotVerifyFromRealUnmet(t *testing.T) {
	v := &task.JudgeVerdict{Met: false, PerCriterion: []task.CriterionVerdict{
		{CriterionID: "c1", Met: false, Reason: "the suite still fails"},
		{CriterionID: "c2", Met: false, Reason: "criterion_unjudgeable: verifier did not return a verdict for this criterion"},
		{CriterionID: "c3", Met: false, Reason: `bash policy is "deny" for agent "a" — verification mechanism could not run (unable_to_verify, ADR-049 D2 rule 2)`},
		{CriterionID: "c4", Met: true, Reason: "done"},
	}}

	got := goalVerdictReasonText(v)

	if !strings.Contains(got, "the suite still fails") {
		t.Errorf("reason %q lost the real unmet reason the worker must act on", got)
	}
	if strings.Contains(got, "criterion_unjudgeable") {
		t.Errorf("reason %q feeds the worker a judge-unavailable reason as work to redo (the 174-poll defect)", got)
	}
	if strings.Contains(got, "bash policy") {
		t.Errorf("reason %q feeds the worker an unable_to_verify mechanism reason as work to redo", got)
	}
	if !strings.Contains(got, goalJudgeCouldNotVerifyMarker) || !strings.Contains(got, "2 criterion/criteria") {
		t.Errorf("reason %q must carry one separate could-not-verify clause naming the count (got %q)", got, goalJudgeCouldNotVerifyMarker)
	}
}

func TestGoalVerdictReasonText_OnlyCouldNotVerify_StillReportsIt(t *testing.T) {
	v := &task.JudgeVerdict{Met: false, PerCriterion: []task.CriterionVerdict{
		{CriterionID: "c1", Met: false, Reason: "unable_to_verify: the check could not run"},
	}}
	got := goalVerdictReasonText(v)
	if !strings.Contains(got, goalJudgeCouldNotVerifyMarker) {
		t.Errorf("reason %q must still report that the Judge could not verify, not bury it", got)
	}
	if got == "(no reason recorded)" {
		t.Errorf("reason must not collapse to the no-reason sentinel when a could-not-verify clause exists")
	}
}

func TestGoalSteeringPrompt_JudgeCouldNotRun_TellsWorkerNotToReverifyOrPoll(t *testing.T) {
	reason := goalVerdictReasonText(&task.JudgeVerdict{Met: false, PerCriterion: []task.CriterionVerdict{
		{CriterionID: "c1", Met: false, Reason: "criterion_unjudgeable: verifier turn ran but produced no content"},
	}})
	got := goalSteeringPrompt("ship the report", reason)

	if !strings.Contains(got, "Continue working toward the goal: ship the report") {
		t.Errorf("steer %q lost the goal it is steering toward", got)
	}
	for _, must := range []string{"Do not re-run", "re-verify", "poll"} {
		if !strings.Contains(got, must) {
			t.Errorf("steer %q must explicitly forbid re-verification and polling (missing %q) — the Judge itself could not run", got, must)
		}
	}
	if strings.Contains(got, "criterion_unjudgeable") {
		t.Errorf("steer %q still hands the worker the raw judge-unavailable reason as work", got)
	}
}

func TestGoalSteeringPrompt_NormalUnmet_KeepsTheOriginalWording(t *testing.T) {
	got := goalSteeringPrompt("ship the report", "the suite still fails")
	want := "Continue working toward the goal: ship the report\n\n" +
		"The judge reviewed your last attempt and found it UNMET:\nthe suite still fails\n\nKeep going."
	if got != want {
		t.Errorf("a normal unmet reason must keep the exact original steer:\ngot  %q\nwant %q", got, want)
	}
	if strings.Contains(got, "Do not re-run") {
		t.Errorf("a normal unmet reason must NOT carry the do-not-reverify instruction")
	}
}
