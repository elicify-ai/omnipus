// dod_distinct_test.go — the distinctness half of the Definition-of-Done rule
// (GOAL-FR-021/FR-047/FR-048, operator decision D-C).
//
// The oracle here is the rule as STATED in ValidateDoDDistinct's contract, not
// as implemented: every expectation below is derived from "equal after trim,
// whitespace-collapse and case-fold, and nothing else", including the cases the
// rule must deliberately let through.

package task

import (
	"errors"
	"strings"
	"testing"
)

// uatDuplicateSentence is the exact text a UAT tester pasted into both the
// acceptance-criteria box and the Definition-of-Done box; the task saved.
const uatDuplicateSentence = "Running the script prints the exact line: Hello, UAT-T2"

func crits(texts ...string) []AcceptanceCriterion {
	out := make([]AcceptanceCriterion, 0, len(texts))
	for _, s := range texts {
		out = append(out, AcceptanceCriterion{Text: s, Status: CritPending})
	}
	return out
}

func TestValidateDoDDistinct(t *testing.T) {
	cases := []struct {
		name     string
		criteria []AcceptanceCriterion
		dod      []AcceptanceCriterion
		wantErr  bool
		// wantNames, when set, must appear in the refusal so the author can
		// find the offending item.
		wantNames []string
	}{
		{
			name:      "byte-identical is the floor — the exact UAT case",
			criteria:  crits(uatDuplicateSentence),
			dod:       crits(uatDuplicateSentence),
			wantErr:   true,
			wantNames: []string{uatDuplicateSentence},
		},
		{
			name:     "leading and trailing whitespace does not make it distinct",
			criteria: crits("the tests pass"),
			dod:      crits("  the tests pass\n"),
			wantErr:  true,
		},
		{
			name:     "internal whitespace runs collapse",
			criteria: crits("the tests pass"),
			dod:      crits("the   tests\tpass"),
			wantErr:  true,
		},
		{
			name:     "case alone does not make it distinct",
			criteria: crits("The Tests Pass"),
			dod:      crits("the tests pass"),
			wantErr:  true,
		},
		{
			name:     "one duplicate among several still collides",
			criteria: crits("the tests pass", "the binary builds", "no secrets leak"),
			dod:      crits("a CHANGELOG entry exists", "the binary builds"),
			wantErr:  true,
			// dod[1] is the offender, not dod[0].
			wantNames: []string{"the binary builds"},
		},
		{
			name:     "a near-paraphrase at a different altitude is legitimate",
			criteria: crits("the tests pass"),
			dod:      crits("the test suite passes in CI on a clean checkout"),
			wantErr:  false,
		},
		{
			name:     "a superset sentence containing the criterion is not a restatement",
			criteria: crits("the tests pass"),
			dod:      crits("the tests pass and the coverage report is attached"),
			wantErr:  false,
		},
		{
			name:     "punctuation is compared, not stripped — the rule stops at whitespace and case",
			criteria: crits("the tests pass"),
			dod:      crits("the tests pass."),
			wantErr:  false,
		},
		{
			name:     "wholly different lists are fine",
			criteria: crits("the script prints the greeting"),
			dod:      crits("the script is committed to the repository"),
			wantErr:  false,
		},
		{
			name:     "duplicates WITHIN the dod list are not this rule's business",
			criteria: crits("the tests pass"),
			dod:      crits("the binary builds", "the binary builds"),
			wantErr:  false,
		},
		{
			name:     "empty criteria list defers to the count rule",
			criteria: nil,
			dod:      crits(uatDuplicateSentence),
			wantErr:  false,
		},
		{
			name:     "empty dod list defers to the count rule",
			criteria: crits(uatDuplicateSentence),
			dod:      nil,
			wantErr:  false,
		},
		{
			name:     "two blank items do not collide with each other",
			criteria: crits("   "),
			dod:      crits("\t\n"),
			wantErr:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDoDDistinct(tc.criteria, tc.dod)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateDoDDistinct(%q, %q) = nil, want a refusal",
						textsOf(tc.criteria), textsOf(tc.dod))
				}
				if !errors.Is(err, ErrValidation) {
					t.Errorf("refusal must be an ErrValidation so every surface maps it to the same "+
						"400, got %T: %v", err, err)
				}
				if !strings.Contains(err.Error(), "distinct") {
					t.Errorf("refusal must state the rule in the same word it is advertised in; got %q",
						err.Error())
				}
				for _, name := range tc.wantNames {
					if !strings.Contains(err.Error(), name) {
						t.Errorf("refusal must name the offending text %q; got %q", name, err.Error())
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateDoDDistinct(%q, %q) = %v, want nil — the rule must not over-reach",
					textsOf(tc.criteria), textsOf(tc.dod), err)
			}
		})
	}
}

func textsOf(cs []AcceptanceCriterion) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return out
}
