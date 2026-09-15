// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_outcome_parse_adr084_test.go covers parseJudgeResponse's D-B
// behaviour (ADR-084 revision 9, wave E9). The judge spec's revision-2
// design (JUDGE-FR-015/FR-016/FR-024/FR-025) added a THIRD `outcome` value
// and REWROTE a `met` with a missing/empty/whitespace evidence_quote to
// `unable_to_verify`. Operator decision D-B withdraws the rewrite in full:
// "the Judge judges as a human would, on common sense. A quote that is
// missing, empty, or does not verify does NOT flip the verdict." There is
// no Outcome field anywhere in this contract (C-02, D-H) — Met stays a
// bool, decided entirely by the model.
//
// What survives, and what this file actually proves: parseJudgeResponse
// still DETECTS a weak-quote `met` and REPORTS it (JUDGE-FR-024's
// detection condition, FR-026's before-truncation ordering, FR-027's
// count-and-log survive as pure reporting) — and never touches Met or
// Reason because of it.
package agent

import (
	"strings"
	"testing"
)

// TestParseJudgeResponse_MetWithWeakQuote_NeverRewritten is D-B's central
// guarantee: a `met` verdict with a missing, empty, or whitespace-only
// evidence_quote keeps Met==true and its original Reason verbatim.
func TestParseJudgeResponse_MetWithWeakQuote_NeverRewritten(t *testing.T) {
	raw := `{"met":true,"criteria":[
		{"id":"c-missing","met":true,"reason":"looks complete"},
		{"id":"c-empty","met":true,"reason":"looks complete too","evidence_quote":""},
		{"id":"c-whitespace","met":true,"reason":"whitespace only","evidence_quote":"   \t\n  "},
		{"id":"c-grounded","met":true,"reason":"grounded","evidence_quote":"a real quote from the file"}
	],"summary":"x"}`

	out, err := parseJudgeResponse(raw)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	if len(out.Criteria) != 4 {
		t.Fatalf("got %d criteria, want 4", len(out.Criteria))
	}
	for _, c := range out.Criteria {
		if !c.Met {
			t.Errorf("criterion %q: Met = false, want true (D-B: a weak quote never flips the verdict)", c.ID)
		}
	}
	// Reason must be preserved byte-for-byte — the D2b "engine reason
	// naming the rule" rewrite (JUDGE-FR-025) is CANCELLED as a rewrite.
	if out.Criteria[0].Reason != "looks complete" {
		t.Errorf("Reason was rewritten: got %q, want %q (unchanged)", out.Criteria[0].Reason, "looks complete")
	}
	if out.Criteria[1].Reason != "looks complete too" {
		t.Errorf("Reason was rewritten: got %q", out.Criteria[1].Reason)
	}
}

// TestParseJudgeResponse_WeakEvidenceCriterionIDs_ReportsExactSet proves
// JUDGE-FR-027's surviving obligation: the weak-quote criteria are counted
// (by id), and ONLY criteria that are both Met==true AND quote-weak are
// counted — an unmet criterion with no quote was never in scope of the old
// rewrite rule and is not reported here either; a genuinely grounded `met`
// is not reported.
func TestParseJudgeResponse_WeakEvidenceCriterionIDs_ReportsExactSet(t *testing.T) {
	raw := `{"met":false,"criteria":[
		{"id":"c-missing","met":true,"reason":"a"},
		{"id":"c-empty","met":true,"reason":"b","evidence_quote":""},
		{"id":"c-unmet-no-quote","met":false,"reason":"not done"},
		{"id":"c-grounded","met":true,"reason":"c","evidence_quote":"real evidence text"}
	],"summary":"x"}`

	out, err := parseJudgeResponse(raw)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	want := map[string]bool{"c-missing": true, "c-empty": true}
	if len(out.WeakEvidenceCriterionIDs) != len(want) {
		t.Fatalf("WeakEvidenceCriterionIDs = %v, want exactly %v", out.WeakEvidenceCriterionIDs, want)
	}
	for _, id := range out.WeakEvidenceCriterionIDs {
		if !want[id] {
			t.Errorf("unexpected id in WeakEvidenceCriterionIDs: %q", id)
		}
	}
}

// TestParseJudgeResponse_TruncatesTopLevelAndArrayQuotes proves the
// ADR-074 D7 500-rune bound applies uniformly to the legacy top-level
// evidence_quote AND to every entry of the new evidence[] array
// (JUDGE-FR-070a) — neither an over-long top-level quote nor an over-long
// per-clause quote can reach persistence unbounded.
func TestParseJudgeResponse_TruncatesTopLevelAndArrayQuotes(t *testing.T) {
	longQuote := strings.Repeat("x", 600)
	raw := `{"met":true,"criteria":[{
		"id":"c1","met":true,"reason":"r","evidence_quote":"` + longQuote + `",
		"evidence":[{"part":"p1","quote":"` + longQuote + `"}]
	}],"summary":"x"}`

	out, err := parseJudgeResponse(raw)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	c := out.Criteria[0]
	if got := len([]rune(c.EvidenceQuote)); got != maxEvidenceQuoteRunes {
		t.Errorf("top-level EvidenceQuote length = %d, want %d", got, maxEvidenceQuoteRunes)
	}
	if len(c.Evidence) != 1 {
		t.Fatalf("got %d evidence entries, want 1", len(c.Evidence))
	}
	if got := len([]rune(c.Evidence[0].Quote)); got != maxEvidenceQuoteRunes {
		t.Errorf("evidence[0].Quote length = %d, want %d", got, maxEvidenceQuoteRunes)
	}
	// The 600-rune quote is genuinely non-empty pre-truncation, so this
	// criterion must NOT be reported as weak-evidence (FR-026's ordering
	// proof: detection ran on real, non-empty content, not on a value
	// truncation could have degraded).
	for _, id := range out.WeakEvidenceCriterionIDs {
		if id == "c1" {
			t.Error("a genuinely non-empty (pre-truncation) quote must not be reported as weak evidence")
		}
	}
}

// TestEvidenceQuoteIsWeak_Cases pins the exact predicate JUDGE-FR-024's
// detection condition uses.
func TestEvidenceQuoteIsWeak_Cases(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"missing", "", true},
		{"whitespace_only", "   \t\n  ", true},
		{"real_content", "index.html renders the pricing table", false},
		{"single_char", "x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evidenceQuoteIsWeak(tc.raw); got != tc.want {
				t.Errorf("evidenceQuoteIsWeak(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestParseJudgeResponse_NoOutcomeField proves the wire/parse contract
// never gains an Outcome value anywhere (C-02, D-H): a judge response that
// tries to declare a three-state outcome is simply ignored — Met is the
// only verdict-shape bool, decided by the model's own "met" field.
func TestParseJudgeResponse_NoOutcomeField(t *testing.T) {
	raw := `{"met":true,"criteria":[{"id":"c1","met":true,"reason":"r","outcome":"unable_to_verify"}],"summary":"x"}`
	out, err := parseJudgeResponse(raw)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	if !out.Criteria[0].Met {
		t.Error("an unrecognised 'outcome' field in the model's JSON must have zero effect on Met")
	}
}
