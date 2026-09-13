// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_summarize_adr084_test.go covers JUDGE-FR-022 as adapted by ADR-084
// revision 9 (D-B, C-02): the string a worker/operator actually receives
// (JudgeCriteriaResult.Reason, produced by summarizeVerdict) must
// distinguish "could not verify" (the verification MECHANISM did not
// complete — a persistently-blocked deterministic check, or a prose
// criterion the verifier ran on but never judged) from "not done" (the
// Judge looked and judged it unmet). The original design keyed this split
// on a three-state Outcome field; that field is retired (C-02, D-H), so
// this wave threads the SAME distinction through as real engine state
// (couldNotVerifyIDs, computed in JudgeCriteria and passed straight to
// summarizeVerdict) rather than resurrecting Outcome or sniffing Reason
// text.
package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

func verdictWith(perCriterion ...task.CriterionVerdict) *task.JudgeVerdict {
	met := len(perCriterion) > 0
	for _, c := range perCriterion {
		if !c.Met {
			met = false
			break
		}
	}
	return &task.JudgeVerdict{PerCriterion: perCriterion, Met: met}
}

func TestSummarizeVerdict_AllMet(t *testing.T) {
	v := verdictWith(task.CriterionVerdict{CriterionID: "c1", Met: true})
	if got, want := summarizeVerdict(v, nil), "all criteria met"; got != want {
		t.Errorf("summarizeVerdict = %q, want %q", got, want)
	}
}

func TestSummarizeVerdict_EmptyPerCriterion_FailClosed(t *testing.T) {
	v := verdictWith()
	if got, want := summarizeVerdict(v, nil), "no criteria were adjudicated (fail-closed, NFR-2)"; got != want {
		t.Errorf("summarizeVerdict = %q, want %q", got, want)
	}
}

// TestSummarizeVerdict_UnmetOnly_NoCouldNotVerifySet mirrors the
// pre-existing behaviour for the common case: every unmet criterion is
// genuinely judged unmet, and the message reads exactly as it always did.
func TestSummarizeVerdict_UnmetOnly_NoCouldNotVerifySet(t *testing.T) {
	v := verdictWith(
		task.CriterionVerdict{CriterionID: "c1", Met: false},
		task.CriterionVerdict{CriterionID: "c2", Met: false},
	)
	got := summarizeVerdict(v, nil)
	if got != "unmet criteria: c1, c2" {
		t.Errorf("summarizeVerdict = %q, want %q", got, "unmet criteria: c1, c2")
	}
	if strings.Contains(got, "could not verify") {
		t.Errorf("no criterion is in couldNotVerifyIDs; the reason must not carry a could-not-verify clause: %q", got)
	}
}

// TestSummarizeVerdict_CouldNotVerifyOnly proves a criterion whose
// verification mechanism failed is labelled "could not verify", never
// "unmet".
func TestSummarizeVerdict_CouldNotVerifyOnly(t *testing.T) {
	v := verdictWith(
		task.CriterionVerdict{CriterionID: "c1", Met: false, Reason: "check timed out (unable_to_verify)"},
	)
	got := summarizeVerdict(v, []string{"c1"})
	if got != "could not verify: c1" {
		t.Errorf("summarizeVerdict = %q, want %q", got, "could not verify: c1")
	}
	if strings.Contains(got, "unmet criteria") {
		t.Errorf("a could-not-verify-only result must not also carry an unmet-criteria clause: %q", got)
	}
}

// TestSummarizeVerdict_MixedPartition proves the two clauses coexist and
// each id lands in exactly the right one — the central FR-022 guarantee.
func TestSummarizeVerdict_MixedPartition(t *testing.T) {
	v := verdictWith(
		task.CriterionVerdict{CriterionID: "genuinely-unmet", Met: false},
		task.CriterionVerdict{CriterionID: "blocked-check", Met: false},
		task.CriterionVerdict{CriterionID: "unjudgeable-prose", Met: false},
		task.CriterionVerdict{CriterionID: "met-one", Met: true},
	)
	got := summarizeVerdict(v, []string{"blocked-check", "unjudgeable-prose"})

	if !strings.Contains(got, "unmet criteria: genuinely-unmet") {
		t.Errorf("must carry an unmet clause naming genuinely-unmet: %q", got)
	}
	if !strings.Contains(got, "could not verify:") {
		t.Errorf("must carry a could-not-verify clause: %q", got)
	}
	for _, id := range []string{"blocked-check", "unjudgeable-prose"} {
		if !strings.Contains(got, id) {
			t.Errorf("could-not-verify clause must name %q: %q", id, got)
		}
	}
	// Cross-contamination check: neither could-not-verify id may appear
	// inside the "unmet criteria:" clause text, and the genuinely-unmet id
	// must not appear inside the "could not verify:" clause text.
	unmetClauseEnd := strings.Index(got, "; could not verify:")
	if unmetClauseEnd == -1 {
		t.Fatalf("expected a '; could not verify:' separator between the two clauses, got %q", got)
	}
	unmetClause := got[:unmetClauseEnd]
	couldNotVerifyClause := got[unmetClauseEnd:]
	for _, id := range []string{"blocked-check", "unjudgeable-prose"} {
		if strings.Contains(unmetClause, id) {
			t.Errorf("could-not-verify id %q leaked into the unmet clause: %q", id, unmetClause)
		}
	}
	if strings.Contains(couldNotVerifyClause, "genuinely-unmet") {
		t.Errorf("genuinely-unmet leaked into the could-not-verify clause: %q", couldNotVerifyClause)
	}
	if strings.Contains(got, "met-one") {
		t.Errorf("a met criterion must never appear in either clause: %q", got)
	}
}

// TestSummarizeVerdict_MetOverridesCouldNotVerify proves a criterion that
// ultimately reads Met==true (the persistently-blocked escalation only
// ever produces Met==false, but this guards the general contract) is never
// listed in either clause, even if it happens to be in couldNotVerifyIDs —
// summarizeVerdict itself short-circuits to "all criteria met" whenever
// v.Met is true, before either clause is built.
func TestSummarizeVerdict_MetOverridesCouldNotVerify(t *testing.T) {
	v := verdictWith(task.CriterionVerdict{CriterionID: "c1", Met: true})
	got := summarizeVerdict(v, []string{"c1"})
	if got != "all criteria met" {
		t.Errorf("summarizeVerdict = %q, want %q (v.Met short-circuits before any partition)", got, "all criteria met")
	}
}
