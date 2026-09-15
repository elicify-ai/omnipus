// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_grounding_adr084_test.go is the end-to-end companion to
// verifier_provenance_test.go's unit-level coverage: it dispatches a FULL
// al.JudgeCriteria() adjudication (through runVerifierAdjudication's real
// mapping loop) for each of JUDGE-FR-006, FR-007a, FR-014a, FR-028, FR-029
// and FR-063a, and asserts the single property D-B makes load-bearing for
// every one of them: the Judge's own Met/verdict survives UNCHANGED
// regardless of the grounding/attribution issue detected — these are
// REPORTS, never gates. (ADR-084 revision 9, operator decision D-B;
// corroborated by GOAL-FR-038/FR-039.)

package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func dispatchProseAdjudication(
	t *testing.T, criterion task.AcceptanceCriterion, judgeJSON string,
) JudgeCriteriaResult {
	t.Helper()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: judgeJSON}, nil
	}}
	judgeInst.Provider = fake

	return al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-" + t.Name(),
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{criterion},
		Attempt:         1,
		ClaimText:       "done",
	})
}

// JUDGE-FR-028: a met missing evidence_target where its source requires
// one MUST NOT be rewritten (D-B cancels the spec's original
// unable_to_verify rewrite).
func TestVerifierGrounding_FR028_MetMissingTarget_StaysMet(t *testing.T) {
	c := proseCriterion("c1", "the feature works")
	result := dispatchProseAdjudication(t, c,
		`{"met":true,"criteria":[{"id":"c1","met":true,"reason":"I read the code and it works",`+
			`"evidence_quote":"func Foo() {}","evidence_source":"file_read"}]}`)
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatal("JUDGE-FR-028/D-B: a met missing evidence_target must stay met — reported, never rewritten")
	}
}

// JUDGE-FR-029: a met with an unrecognised/absent evidence_source MUST NOT
// be rewritten.
func TestVerifierGrounding_FR029_MetWithUnrecognisedSource_StaysMet(t *testing.T) {
	c := proseCriterion("c1", "the feature works")
	result := dispatchProseAdjudication(t, c,
		`{"met":true,"criteria":[{"id":"c1","met":true,"reason":"it works",`+
			`"evidence_quote":"q","evidence_source":"not-a-real-source"}]}`)
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatal("JUDGE-FR-029/D-B: a met with an unrecognised evidence_source must stay met")
	}
}

// JUDGE-FR-014a: a met grounded via session_read for a criterion that
// names a filesystem path MUST NOT be rewritten (inspect_session cannot
// show file contents — this is a reported red flag, not a gate).
func TestVerifierGrounding_FR014a_SessionReadGroundingFilesystemCriterion_StaysMet(t *testing.T) {
	c := proseCriterion("c1", "neon-2048/index.html must contain the win condition")
	result := dispatchProseAdjudication(t, c,
		`{"met":true,"criteria":[{"id":"c1","met":true,"reason":"the child session mentions index.html",`+
			`"evidence_quote":"q","evidence_source":"session_read","evidence_target":"sess-child-1"}]}`)
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatal("JUDGE-FR-014a/D-B: a met grounded via session_read for a filesystem-path criterion must stay met")
	}
}

// JUDGE-FR-006: a met whose evidence array carries fewer entries than the
// criterion's persisted clause count MUST NOT be rewritten.
func TestVerifierGrounding_FR006_FewerEvidenceEntriesThanClauseCount_StaysMet(t *testing.T) {
	c := proseCriterion("c1", "a is true and b is true and c is true")
	c.ClauseCount = 3
	result := dispatchProseAdjudication(t, c,
		`{"met":true,"criteria":[{"id":"c1","met":true,"reason":"all three hold",`+
			`"evidence":[{"part":"a is true","source":"file_read","target":"a.txt","quote":"a=1"}]}]}`)
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatal("JUDGE-FR-006/D-B: a met with fewer evidence entries than the clause count must stay met")
	}
}

// JUDGE-FR-007a: an unmet whose reason asserts absence is reported, not
// escalated into a withheld/unable_to_verify retry — the round is
// consumed exactly as any ordinary unmet round would be.
func TestVerifierGrounding_FR007a_UnmetAssertingAbsence_ConsumesRoundNormally(t *testing.T) {
	c := proseCriterion("c1", "the config file contains the new flag")
	result := dispatchProseAdjudication(t, c,
		`{"met":false,"criteria":[{"id":"c1","met":false,"reason":"the flag is not present in the file"}]}`)
	if result.Unavailable {
		t.Fatal("JUDGE-FR-007a/D-B: an absence-asserting unmet must be an ordinary consumed round, not Unavailable/withheld")
	}
	if result.Verdict == nil || result.Verdict.Met {
		t.Fatal("expected a real, scored unmet verdict")
	}
}

// JUDGE-FR-063a: an unmet whose reason asserts a refusal is reported, not
// escalated into a withheld retry.
func TestVerifierGrounding_FR063a_UnmetAssertingRefusal_ConsumesRoundNormally(t *testing.T) {
	c := proseCriterion("c1", "/etc/hosts must contain the new entry")
	result := dispatchProseAdjudication(t, c,
		`{"met":false,"criteria":[{"id":"c1","met":false,"reason":"the read was refused — outside confinement"}]}`)
	if result.Unavailable {
		t.Fatal("JUDGE-FR-063a/D-B: a refusal-asserting unmet must be an ordinary consumed round, not Unavailable/withheld")
	}
	if result.Verdict == nil || result.Verdict.Met {
		t.Fatal("expected a real, scored unmet verdict")
	}
}

// TestVerifierGrounding_MultipleIssuesAtOnce_AllReportedNoneGate is a
// belt-and-braces composite: stack FR-028 (missing target) and FR-029
// (bad source) on the SAME met verdict — still met.
func TestVerifierGrounding_MultipleIssuesAtOnce_AllReportedNoneGate(t *testing.T) {
	c := proseCriterion("c1", "it works")
	result := dispatchProseAdjudication(t, c,
		fmt.Sprintf(`{"met":true,"criteria":[{"id":"c1","met":true,"reason":"it works",`+
			`"evidence_quote":"q","evidence_source":"bogus-source"}]}`))
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatal("D-B: stacking multiple grounding issues on one met verdict must still never gate it")
	}
}
