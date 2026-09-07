// Omnipus — spec FR-027, FR-028, FR-064, FR-073, FR-123, FR-125a, AC-F3: the
// plan, the two bounds, grouping and totals.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// EXPLAIN — AC-F3
// ---------------------------------------------------------------------------

// TestExplain_NamesEveryPropertyAndItsIndex is AC-F3(a).
//
// The criterion was strengthened for a reason: as first written it asserted only
// that two calls agreed, which a CONSTANT-RETURNING STUB satisfies perfectly. So
// the plan must name every property the query touches and the index each will be
// answered from, which a stub gets WRONG rather than missing.
func TestExplain_NamesEveryPropertyAndItsIndex(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	yes := true
	sorts := []generated.VaultFindSort{{Property: "planted"}}
	aggs := []generated.VaultFindAggregate{{Op: "sum", Property: strPtr("height_cm")}}
	groups := []generated.VaultFindGroupBy{{Property: "condition"}}

	r := req(withType("plant"), withFilter(generated.VaultFilterNode{
		All: &[]generated.VaultFilterNode{
			leaf("condition", "=", "growing"),
			leaf("cuttings", ">=", "2"),
		},
	}))
	r.Explain = &yes
	r.Sort = &sorts
	r.Aggregate = &aggs
	r.GroupBy = &groups

	resp := mustFind(t, f.deps(), r)
	if resp.Plan == nil {
		t.Fatalf("explain returned no plan")
	}
	out := Render(resp)
	t.Logf("\n────────── explain (%d bytes) ──────────\n%s"+
		"────────────────────────────────────────", len(out), out)

	for _, property := range []string{"condition", "cuttings", "planted", "height_cm"} {
		if !strings.Contains(out, property) {
			t.Errorf("the plan does not name %q, a property the query touches:\n%s", property, out)
		}
	}
	// EVERY comparison must be sourced from the Go comparator. A plan showing a
	// comparison answered by the properties index is reporting a ruling
	// violation, and this is where that becomes visible.
	for _, step := range *resp.Plan {
		if step.Stage == generated.Compare {
			if step.Source == nil || *step.Source != generated.VaultFindPlanStepSourceGoComparator {
				t.Errorf("a comparison step is sourced from %v, not the Go comparator. "+
					"SQLite narrows; the comparator decides.", step.Source)
			}
		}
		if step.Stage == generated.Narrow {
			if step.Source == nil || *step.Source != generated.VaultFindPlanStepSourcePropertiesIndex {
				t.Errorf("the narrowing step is not sourced from the properties index: %v", step.Source)
			}
		}
	}
}

// TestExplain_EvaluatesNothingAndIsUnchangedByAMutation is AC-F3(b) and (c).
//
// The corpus mutation is chosen to CHANGE THE PLAN IF EVALUATION WERE HAPPENING:
// it adds records that match the filter and removes none, so any plan derived
// from data rather than from the request would differ. A mutation that happens
// not to change the plan proves nothing, which is why the fixture excludes that
// case rather than trusting it.
func TestExplain_EvaluatesNothingAndIsUnchangedByAMutation(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	yes := true
	r := req(withType("plant"), withFilter(leaf("condition", "=", "growing")))
	r.Explain = &yes

	// A store that FAILS on any retrieval. An explain that touched it would not
	// merely differ — it would error, which is a louder signal than a diff.
	d := f.deps()
	d.Store = refusingStore{Store: f.store}

	before := Render(mustFind(t, d, r))

	for i := 2; i <= 40; i++ {
		f.plant(i, "growing", fmt.Sprintf("%d.00", i))
	}

	after := Render(mustFind(t, d, r))

	if before != after {
		t.Errorf("the explain plan changed after a corpus mutation, so it is being "+
			"derived from data rather than from the request.\nbefore:\n%s\nafter:\n%s",
			before, after)
	}
	// AC-F3, revision 6: NOTHING may differ, index_epoch included. An explain
	// evaluates nothing, so it should not observe an epoch at all.
	if strings.Contains(before, "index_epoch") {
		t.Errorf("the explain response carries an index_epoch. A plan is not a result:\n%s", before)
	}
	if strings.Contains(before, "INDEX:") {
		t.Errorf("the explain response reports index freshness, which it cannot have checked:\n%s", before)
	}
}

// refusingStore fails every read. It is how "zero candidate retrievals" is
// asserted at the store boundary rather than by inspection.
type refusingStore struct{ propindex.Store }

var errNoRetrieval = errors.New("explain retrieved a candidate")

func (refusingStore) CountCandidates(context.Context, propindex.Selector) (int, error) {
	return 0, errNoRetrieval
}
func (refusingStore) Candidates(context.Context, propindex.Selector, func(propindex.Candidate) (propindex.Verdict, error)) error {
	return errNoRetrieval
}
func (refusingStore) Tasks(context.Context, propindex.Selector, func(propindex.TaskHit) error) error {
	return errNoRetrieval
}

// ---------------------------------------------------------------------------
// THE TWO BOUNDS — FR-064
// ---------------------------------------------------------------------------

// TestBound_B1QuotesItsCountAndNamesScope.
//
// B1 counts the narrowed population BEFORE retrieval, so its count is exact and
// quoting it is honest. Its remedy is SCOPE or KIND and must NOT be "add a
// filter" — a filter does not change the number that fired, and naming a remedy
// that does not reduce the number is worse than naming none.
func TestBound_B1QuotesItsCountAndNamesScope(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	d := f.deps()
	d.Store = hugeStore{Store: f.store, count: propindex.BoundNarrowedCandidates + 6412}

	resp := mustRefuse(t, d, req(withType("plant"), withFilter(leaf("condition", "=", "growing"))))
	p := resp.Problems[0]

	if p.Code != generated.EvaluationBoundExceeded {
		t.Errorf("code = %s, want evaluation_bound_exceeded — B1 and B2 are distinct causes "+
			"because they name different remedies", p.Code)
	}
	if !strings.Contains(p.Reason, "56,412") {
		t.Errorf("B1 does not quote its exact count: %q", p.Reason)
	}
	if !strings.Contains(p.Reason, "50,000") {
		t.Errorf("B1 does not state its limit: %q", p.Reason)
	}
	fix := deref(p.Fix)
	if !strings.Contains(fix, "scope") && !strings.Contains(fix, "kind") {
		t.Errorf("B1's remedy names neither scope nor kind: %q", fix)
	}
	if strings.Contains(fix, "add a filter") || strings.Contains(fix, "tighten a filter") {
		t.Errorf("B1's remedy says to add a filter, which does not change the number "+
			"that fired: %q", fix)
	}
}

// TestBound_B2QuotesNoTotalItNeverComputed.
//
// B2 aborts DURING evaluation, so the count stops at the cap and never reaches a
// true total. The message must not quote one — that would state a number nobody
// computed, wearing the authority of one that was.
func TestBound_B2QuotesNoTotalItNeverComputed(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	d := f.deps()
	d.Store = boundedStore{Store: f.store}

	resp := mustRefuse(t, d, req(withType("plant"), withFilter(leaf("condition", "=", "growing"))))
	p := resp.Problems[0]

	if p.Code != generated.CandidateCapExceeded {
		t.Errorf("code = %s, want candidate_cap_exceeded", p.Code)
	}
	if !strings.Contains(p.Reason, "more than 10,000") {
		t.Errorf("B2 does not state the cap it stopped at: %q", p.Reason)
	}
	// The survivors count at the moment of abort is an artefact of where the
	// stream stopped, not a total. It must not be presented as one.
	if strings.Contains(p.Reason, "10,001") || strings.Contains(p.Reason, "matched 1") {
		t.Errorf("B2 quotes a survivor total it never finished counting: %q", p.Reason)
	}
	if !strings.Contains(deref(p.Fix), "filter") {
		t.Errorf("B2's remedy does not name a filter, which DOES reduce survivors: %q", deref(p.Fix))
	}
}

type hugeStore struct {
	propindex.Store
	count int
}

func (h hugeStore) CountCandidates(context.Context, propindex.Selector) (int, error) {
	return h.count, nil
}

type boundedStore struct{ propindex.Store }

func (boundedStore) CountCandidates(context.Context, propindex.Selector) (int, error) { return 12, nil }
func (boundedStore) Candidates(context.Context, propindex.Selector, func(propindex.Candidate) (propindex.Verdict, error)) error {
	return &propindex.BoundError{
		Bound: "B2", Count: propindex.BoundSurvivors + 1, Limit: propindex.BoundSurvivors,
		Remedy: "add or tighten a filter",
	}
}

// ---------------------------------------------------------------------------
// GROUPING AND TOTALS
// ---------------------------------------------------------------------------

// TestGroup_ARecordWithSeveralValuesJoinsEveryGroup is FR-028.
func TestGroup_ARecordWithSeveralValuesJoinsEveryGroup(t *testing.T) {
	f := newFixture(t)
	f.write("garden/a.md", "---\ntype: plant\nid: PL-0001\nspecies: Fern\nlabels: [indoor, humid]\n---\n")
	f.write("garden/b.md", "---\ntype: plant\nid: PL-0002\nspecies: Fern\nlabels: [indoor]\n---\n")
	f.write("garden/c.md", "---\ntype: plant\nid: PL-0003\nspecies: Fern\n---\n")

	groups := []generated.VaultFindGroupBy{{Property: "labels"}}
	r := req(withType("plant"))
	r.GroupBy = &groups
	resp := mustFind(t, f.deps(), r)

	if resp.Groups == nil {
		t.Fatalf("no groups were returned")
	}
	byKey := map[string]generated.VaultFindGroup{}
	absent := 0
	for _, g := range *resp.Groups {
		if g.Absent != nil && *g.Absent {
			absent++
			continue
		}
		byKey[g.Key] = g
	}
	if byKey["indoor"].Count != 2 {
		t.Errorf("group indoor = %d, want 2 — PL-0001 holds two labels and belongs to BOTH groups",
			byKey["indoor"].Count)
	}
	if byKey["humid"].Count != 1 {
		t.Errorf("group humid = %d, want 1", byKey["humid"].Count)
	}
	// The record that never said is a REAL group, not a dropped record. The
	// records nobody recorded a value for are frequently the ones being asked
	// about.
	if absent != 1 {
		t.Errorf("the absent group is missing; PL-0003 has no labels and must still be grouped")
	}
	out := Render(resp)
	if !strings.Contains(out, "(no value)") {
		t.Errorf("the absent group does not render as absence:\n%s", out)
	}
}

// TestTotals_ComputedOverTheEvaluatedSetNotThePage is FR-125a.
//
// The two counts must genuinely DIFFER, or the assertion cannot fail. That is
// the defect an earlier revision of the spec's worked example shipped: its
// header said 14 evaluated and its total said "over 12 of 12 rows" — the same
// number twice, so the test the requirement mandates could not have failed.
func TestTotals_ComputedOverTheEvaluatedSetNotThePage(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 10; i++ {
		f.plant(i, "growing", "10.00")
	}

	limit := 3
	aggs := []generated.VaultFindAggregate{
		{Op: "sum", Property: strPtr("height_cm")},
		{Op: "count"},
	}
	r := req(withType("plant"))
	r.Limit = &limit
	r.Aggregate = &aggs

	resp := mustFind(t, f.deps(), r)
	if len(resp.Rows) != 3 {
		t.Fatalf("rows = %d, want the page of 3", len(resp.Rows))
	}
	if resp.Counts.Evaluated != 10 {
		t.Fatalf("evaluated = %d, want 10", resp.Counts.Evaluated)
	}

	var sum generated.VaultFindTotal
	for _, tot := range resp.Totals {
		if tot.Op == "sum" {
			sum = tot
		}
	}
	if sum.Value != "100.00" {
		t.Errorf("sum = %q, want 100.00 — ten rows of 10.00, not the three shown. "+
			"A page-scoped number wearing the word \"total\" is the defect FR-125a "+
			"exists to remove.", sum.Value)
	}
	if !strings.Contains(sum.Scope, "10") || !strings.Contains(sum.Scope, "3 shown") {
		t.Errorf("the scope does not distinguish evaluated from shown: %q", sum.Scope)
	}
}

// TestTotals_NoRowCarriesAValueIsNotZero.
//
// Rendering an absent total as 0 would state a fact about the corpus that is not
// true, and a reader would act on it.
func TestTotals_NoRowCarriesAValueIsNotZero(t *testing.T) {
	f := newFixture(t)
	f.write("garden/a.md", "---\ntype: plant\nid: PL-0001\nspecies: Fern\n---\n")

	aggs := []generated.VaultFindAggregate{{Op: "sum", Property: strPtr("height_cm")}}
	r := req(withType("plant"))
	r.Aggregate = &aggs
	resp := mustFind(t, f.deps(), r)

	tot := resp.Totals[0]
	if tot.Refused == nil || !*tot.Refused {
		t.Errorf("a total over rows that carry no value was returned as %q rather than refused",
			tot.Value)
	}
	if tot.Value != "" {
		t.Errorf("a refused total carries the value %q; a zero would be read as an answer", tot.Value)
	}
	out := Render(resp)
	if !strings.Contains(out, "no total") {
		t.Errorf("the refused total does not say so:\n%s", out)
	}
}

// TestTotals_RowsWithoutAValueAreNamedInTheScope is FR-125's scope clause doing
// real work: the total says what it EXCLUDED, in the same sentence as the number.
func TestTotals_RowsWithoutAValueAreNamedInTheScope(t *testing.T) {
	f := newFixture(t)
	f.write("garden/a.md", "---\ntype: plant\nid: PL-0001\nspecies: Fern\nheight_cm: 10.00\n---\n")
	f.write("garden/b.md", "---\ntype: plant\nid: PL-0002\nspecies: Fern\n---\n")

	aggs := []generated.VaultFindAggregate{{Op: "sum", Property: strPtr("height_cm")}}
	r := req(withType("plant"))
	r.Aggregate = &aggs
	resp := mustFind(t, f.deps(), r)

	tot := resp.Totals[0]
	if tot.Value != "10.00" {
		t.Errorf("sum = %q, want 10.00", tot.Value)
	}
	if !strings.Contains(tot.Scope, "not included") {
		t.Errorf("the scope does not say the row without a value was excluded: %q", tot.Scope)
	}
}
