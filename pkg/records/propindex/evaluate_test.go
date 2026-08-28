// Omnipus — the evaluation path: narrow in SQL, decide in Go, with the fan-out
// gone before the comparator sees a record.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// evaluate runs a query and returns the matched paths in stream order.
func evaluate(t *testing.T, store Store, q Query) ([]string, Report) {
	t.Helper()
	var paths []string
	rep, err := Evaluate(context.Background(), store, q, func(m Match) error {
		paths = append(paths, m.Path)
		return nil
	})
	if err != nil {
		t.Fatalf("Evaluate(%+v): %v", q.Selector, err)
	}
	return paths, rep
}

// seedConditions writes one plant per named condition, at a predictable path.
func seedConditions(t *testing.T, store Store, conditions ...string) *records.Schema {
	t.Helper()
	sc := plantSchema(t)
	for i, cond := range conditions {
		mustUpsert(t, store, plantNote(t, i+1, cond))
	}
	return sc
}

// ---------------------------------------------------------------------------
// THE COMPARATOR DECIDES — and the answers it gives are the ones SQLite would
// have got WRONG.
//
// A test that only asserted "the filter worked" would pass against a SQL
// pushdown too, which is exactly why the corpus below is built out of the five
// executed contradictions in the package doc. Each case is one SQLite would
// answer differently.
// ---------------------------------------------------------------------------

// TestEvaluate_DecidesInGoWhereSQLiteWouldBeWrong is the behavioural half of
// ruling R-A. sqlgate_test.go asserts no comparison REACHES SQL; this asserts
// the comparison that happens instead gives the RIGHT answer.
func TestEvaluate_DecidesInGoWhereSQLiteWouldBeWrong(t *testing.T) {
	store, _ := openIndex(t, Options{})
	sc := plantSchema(t)

	// Values chosen so SQLite's own defaults produce a different answer.
	//
	//	cuttings: '3' > 2 is TRUE in SQLite for a TEXT '3' (R-1).
	//	condition: an enum whose DECLARED order is not its alphabetical order.
	//	labels: mixed case, which SQLite's LIKE folds only in ASCII.
	//	species: a non-ASCII name SQLite's lower() cannot fold at all.
	mustUpsert(t, store,
		note(t, "garden/a.md", sc, "---\ntype: plant\nid: PL-1\nspecies: Müller\ncondition: growing\ncuttings: 10\nlabels: [Indoor, HUMID]\n---\n"),
		note(t, "garden/b.md", sc, "---\ntype: plant\nid: PL-2\nspecies: MÜLLER\ncondition: dormant\ncuttings: 3\nlabels: [outdoor]\n---\n"),
		note(t, "garden/c.md", sc, "---\ntype: plant\nid: PL-3\nspecies: other\ncondition: seedling\n---\n"),
	)

	for _, tc := range []struct {
		name   string
		filter records.Filter
		want   []string
		why    string
	}{
		{
			name:   "R-1 numbers order as numbers, not as text",
			filter: records.Filter{Property: "cuttings", Op: records.OpGreater, Literal: "9", LiteralGiven: true},
			want:   []string{"garden/a.md"},
			why:    "SQLite ranks the text '3' above the number 9; 10 is the only record above it",
		},
		{
			name:   "FR-011a full Unicode folding, which SQLite cannot do at all",
			filter: records.Filter{Property: "species", Op: records.OpEqual, Literal: "müller", LiteralGiven: true},
			want:   []string{"garden/a.md", "garden/b.md"},
			why:    "lower('MÜLLER') is 'mÜller' in this SQLite: it folds ASCII only, so neither record would match",
		},
		{
			name:   "R-9 element-wise LIKE over a many property, case-insensitively",
			filter: records.Filter{Property: "labels", Op: records.OpLike, Literal: "indoor", LiteralGiven: true},
			want:   []string{"garden/a.md"},
			why:    "the stored element is 'Indoor'; LIKE is anchored and case-insensitive",
		},
		{
			name:   "FR-008 a negative filter INCLUDES the record that never said",
			filter: records.Filter{Property: "cuttings", Op: records.OpEqual, Literal: "3", LiteralGiven: true, Negate: true},
			want:   []string{"garden/a.md", "garden/c.md"},
			why:    "NOT (cuttings = 3) over a NULL returns no rows in SQLite; c.md declares no cuttings and belongs in the answer",
		},
		{
			name:   "FR-008's opt-out still excludes it",
			filter: records.Filter{Property: "cuttings", Op: records.OpEqual, Literal: "3", LiteralGiven: true, Negate: true, ExcludeAbsent: true},
			want:   []string{"garden/a.md"},
			why:    "ExcludeAbsent is the explicit opt-out; without it the absent record is included",
		},
		{
			name:   "R-2 `<>` is a leaf, and over an absent side it is FALSE",
			filter: records.Filter{Property: "cuttings", Op: records.OpNotEqual, Literal: "3", LiteralGiven: true},
			want:   []string{"garden/a.md"},
			why:    "`<>` is NOT the negation flag — c.md's absent cuttings compares false, as SQL's own `<>` does over NULL",
		},
		{
			name:   "R-3 IS NULL is true exactly for the record that never said",
			filter: records.Filter{Property: "cuttings", Op: records.OpIsNull},
			want:   []string{"garden/c.md"},
			why:    "absence is a distinct state (FR-007), not a value",
		},
		{
			name:   "R-5 enum equality resolves through the declared value",
			filter: records.Filter{Property: "condition", Op: records.OpIn, Literals: []string{"growing", "seedling"}},
			want:   []string{"garden/a.md", "garden/c.md"},
			why:    "IN is set membership over the declared enum values",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, rep := evaluate(t, store, Query{Schema: sc, Filters: []records.Filter{tc.filter}})
			assertPaths(t, got, tc.want, tc.why)
			if rep.Matched != len(tc.want) {
				t.Errorf("Report.Matched is %d, want %d", rep.Matched, len(tc.want))
			}
			if rep.Considered != 3 {
				t.Errorf("Report.Considered is %d, want 3 — every narrowed candidate must reach the comparator", rep.Considered)
			}
		})
	}
}

func assertPaths(t *testing.T, got, want []string, why string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("matched %v, want %v\n  %s", got, want, why)
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Fatalf("matched %v, want %v\n  %s", got, want, why)
		}
	}
}

// TestEvaluate_ConjoinsFiltersAndReportsEveryProblem covers two things one
// corpus can carry: a record must satisfy EVERY filter, and the problem list
// must not depend on filter order.
func TestEvaluate_ConjoinsFiltersAndReportsEveryProblem(t *testing.T) {
	store, _ := openIndex(t, Options{})
	sc := plantSchema(t)
	mustUpsert(t, store,
		note(t, "garden/a.md", sc, "---\ntype: plant\nid: PL-1\nspecies: Fern\ncondition: growing\ncuttings: 10\n---\n"),
		note(t, "garden/b.md", sc, "---\ntype: plant\nid: PL-2\nspecies: Fern\ncondition: dormant\ncuttings: 10\n---\n"),
		note(t, "garden/c.md", sc, "---\ntype: plant\nid: PL-3\nspecies: Fern\ncondition: growing\ncuttings: 2\n---\n"),
	)

	both := []records.Filter{
		{Property: "condition", Op: records.OpEqual, Literal: "growing", LiteralGiven: true},
		{Property: "cuttings", Op: records.OpGreater, Literal: "5", LiteralGiven: true},
	}
	got, rep := evaluate(t, store, Query{Schema: sc, Filters: both})
	assertPaths(t, got, []string{"garden/a.md"}, "a record must satisfy EVERY filter")
	if !rep.Complete() {
		t.Errorf("a clean corpus reported problems: %+v / %+v", rep.Problems, rep.ComparisonProblems)
	}

	// Reversing the filters must not change the answer OR the report.
	reversed := []records.Filter{both[1], both[0]}
	gotRev, repRev := evaluate(t, store, Query{Schema: sc, Filters: reversed})
	assertPaths(t, gotRev, got, "filter order must not change the answer")
	if repRev.Considered != rep.Considered || repRev.Matched != rep.Matched {
		t.Errorf("filter order changed the report: %+v vs %+v", rep, repRev)
	}
}

// TestEvaluate_DoesNotShortCircuitPastAProblem is the assertion the clean
// corpus above CANNOT make, and the reason it is a separate test.
//
// A conjunction that stopped at its first rejection would still return the
// right RECORDS — which is why the answer-level checks above pass either way.
// What it would lose is the PROBLEM: a record already excluded by filter 1
// would never have filter 2 applied, so a fault filter 2 would have found goes
// unreported, and `complete: true` is returned over an answer that silently
// skipped a corrupt value. FR-026 requires the offending records to be named,
// and short-circuiting makes that a function of the order the caller happened
// to write the query in.
//
// The corpus is built so filter 1 REJECTS the record and filter 2 would REPORT
// it. Under short-circuiting the problem list comes back empty.
func TestEvaluate_DoesNotShortCircuitPastAProblem(t *testing.T) {
	store, _ := openIndex(t, Options{})

	// Indexed against a schema with no `height_cm`, so the index holds no
	// state for it — the stale-index case.
	narrow, rej := records.ParseSchema("plant.yaml", []byte(`
schema_version: 1
type: plant
properties:
  species:   { type: text }
  condition: { type: enum, values: [seedling, growing, dormant] }
`))
	if rej != nil {
		t.Fatalf("fixture schema: %s", rej.String())
	}
	mustUpsert(t, store, note(t, "garden/a.md", narrow,
		"---\ntype: plant\nid: PL-1\nspecies: Fern\ncondition: growing\n---\n"))

	sc := plantSchema(t) // declares height_cm
	rejecting := records.Filter{Property: "condition", Op: records.OpEqual, Literal: "dormant", LiteralGiven: true}
	reporting := records.Filter{Property: "height_cm", Op: records.OpGreater, Literal: "10", LiteralGiven: true}

	got, rep := evaluate(t, store, Query{Schema: sc, Filters: []records.Filter{rejecting, reporting}})

	assertPaths(t, got, nil, "the first filter excludes the only record")
	if rep.Complete() {
		t.Fatal("the answer claims COMPLETE. Evaluation stopped at the first rejection, so the " +
			"second filter never ran and the fault it would have found went unreported. FR-026 " +
			"requires the offending records to be NAMED — short-circuiting makes the problem list " +
			"depend on the order the caller wrote the filters in.")
	}
	var found bool
	for _, p := range rep.Problems {
		if p.Property == "height_cm" && p.RecordPath == "garden/a.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("no problem named height_cm on the excluded record; got %+v", rep.Problems)
	}
}

// TestEvaluate_TwoFiltersOnOnePropertySeeOneOperand guards the per-record
// decode memoisation.
//
// Decoding a property twice would be a second decode path over one record, and
// StaleValueError makes the divergence observable: one filter could report the
// value undecodable while the other compared it happily. The observable proof
// that it is decoded ONCE is that the fault is reported once, not twice.
func TestEvaluate_TwoFiltersOnOnePropertySeeOneOperand(t *testing.T) {
	store, _ := openIndex(t, Options{})

	narrow, rej := records.ParseSchema("plant.yaml", []byte(`
schema_version: 1
type: plant
properties:
  species: { type: text }
`))
	if rej != nil {
		t.Fatalf("fixture schema: %s", rej.String())
	}
	mustUpsert(t, store, note(t, "garden/a.md", narrow,
		"---\ntype: plant\nid: PL-1\nspecies: Fern\n---\n"))

	sc := plantSchema(t)
	_, rep := evaluate(t, store, Query{Schema: sc, Filters: []records.Filter{
		{Property: "height_cm", Op: records.OpGreater, Literal: "1", LiteralGiven: true},
		{Property: "height_cm", Op: records.OpLess, Literal: "99", LiteralGiven: true},
	}})

	var n int
	for _, p := range rep.Problems {
		if p.Property == "height_cm" && p.Code == records.FindingStaleIndex {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the same property was resolved %d times for one record; it must be decoded ONCE "+
			"so that two filters over it compare the SAME operand. Problems: %+v", n, rep.Problems)
	}
}

// ---------------------------------------------------------------------------
// THE FAN-OUT
// ---------------------------------------------------------------------------

// TestEvaluate_TheComparatorSeesEachRecordExactlyOnce is the property the
// header of evaluate.go names.
//
// The corpus is chosen so the join genuinely fans out: eight declared
// properties plus a two-element list is a dozen rows per record. If any of
// those rows reached the comparator on its own, the count below would exceed
// the number of records — and, worse, a `many` property would be compared as
// half a list, which answers a different question without saying so.
func TestEvaluate_TheComparatorSeesEachRecordExactlyOnce(t *testing.T) {
	store, _ := openIndex(t, Options{})
	sc := seedConditions(t, store, "growing", "dormant", "seedling", "growing")

	visits := map[string]int{}
	rep, err := Evaluate(context.Background(), store, Query{
		Schema: sc,
		// A filter that accepts everything, so every candidate reaches the end.
		Filters: []records.Filter{{Property: "species", Op: records.OpIsNotNull}},
	}, func(m Match) error {
		visits[m.Path]++
		return nil
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(visits) != 4 || rep.Considered != 4 || rep.Matched != 4 {
		t.Fatalf("visited %d distinct records, considered %d, matched %d; want 4/4/4",
			len(visits), rep.Considered, rep.Matched)
	}
	for path, n := range visits {
		if n != 1 {
			t.Errorf("%s reached the comparator %d times; the join fan-out must be de-duplicated "+
				"into ONE record before any comparison is taken", path, n)
		}
	}
}

// TestEvaluate_ManyPropertyArrivesWhole is the fan-out assertion from the side
// that would actually produce a wrong answer.
//
// `labels: [indoor, humid]` is two rows. R-9 reads a `many` property as ONE
// operand, so a record whose labels arrived one row at a time would match
// `labels IN ('humid')` on its second row and not its first — or, with the
// assembly half-done, not at all.
func TestEvaluate_ManyPropertyArrivesWhole(t *testing.T) {
	store, _ := openIndex(t, Options{})
	sc := plantSchema(t)
	mustUpsert(t, store,
		note(t, "garden/a.md", sc, "---\ntype: plant\nid: PL-1\nspecies: Fern\nlabels: [indoor, humid, rare]\n---\n"),
		note(t, "garden/b.md", sc, "---\ntype: plant\nid: PL-2\nspecies: Fern\nlabels: [outdoor]\n---\n"),
	)

	// Every element of the list must be reachable, including the LAST — the one
	// a truncated assembly would drop.
	for _, label := range []string{"indoor", "humid", "rare"} {
		got, _ := evaluate(t, store, Query{
			Schema:  sc,
			Filters: []records.Filter{{Property: "labels", Op: records.OpIn, Literals: []string{label}}},
		})
		assertPaths(t, got, []string{"garden/a.md"},
			"element "+label+" must be present in the assembled operand")
	}

	// And the whole list must arrive on the Match, in source order.
	var labels []string
	if _, err := Evaluate(context.Background(), store, Query{
		Schema:  sc,
		Filters: []records.Filter{{Property: "labels", Op: records.OpIn, Literals: []string{"indoor"}}},
	}, func(m Match) error {
		pv, ok := m.Value("labels")
		if !ok {
			t.Fatal("the Match carries no labels value")
		}
		for _, v := range pv.Values {
			labels = append(labels, v.Text)
		}
		return nil
	}); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if strings.Join(labels, ",") != "indoor,humid,rare" {
		t.Errorf("the assembled list is %v; want [indoor humid rare] in source order", labels)
	}
}

// ---------------------------------------------------------------------------
// REFUSALS — the query is rejected, never answered with zero rows
// ---------------------------------------------------------------------------

// TestEvaluate_RejectsABadQueryBeforeTouchingARecord is FR-023/FR-024.
func TestEvaluate_RejectsABadQueryBeforeTouchingARecord(t *testing.T) {
	store, _ := openIndex(t, Options{})
	sc := seedConditions(t, store, "growing")

	for _, tc := range []struct {
		name    string
		query   Query
		wantSub string
	}{
		{
			name:    "an unknown property is refused with the valid names",
			query:   Query{Schema: sc, Filters: []records.Filter{{Property: "colour", Op: records.OpEqual, Literal: "red", LiteralGiven: true}}},
			wantSub: "has no property",
		},
		{
			name:    "an unknown operator is refused with the supported set",
			query:   Query{Schema: sc, Filters: []records.Filter{{Property: "species", Op: "CONTAINS", Literal: "x", LiteralGiven: true}}},
			wantSub: "not a supported operator",
		},
		{
			name:    "filters with no record type are refused",
			query:   Query{Filters: []records.Filter{{Property: "species", Op: records.OpIsNull}}},
			wantSub: "declares no record type",
		},
		{
			name: "a selector narrowing to a different type from the filters is refused",
			query: Query{
				Selector: Selector{RecordType: "bed"},
				Schema:   sc,
				Filters:  []records.Filter{{Property: "species", Op: records.OpIsNull}},
			},
			wantSub: "must be the same type",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			visited := 0
			_, err := Evaluate(context.Background(), store, tc.query, func(Match) error {
				visited++
				return nil
			})
			if err == nil {
				t.Fatalf("the query was ANSWERED rather than refused. FR-024: a query naming "+
					"something the schema does not declare must be rejected with the valid names — "+
					"an empty or partial result set is indistinguishable from 'nothing matched', "+
					"and the second is far more common. visited=%d", visited)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("refusal %q does not contain %q", err.Error(), tc.wantSub)
			}
			if visited != 0 {
				t.Errorf("the refusal came AFTER %d records were visited; FR-023 requires the "+
					"filter to be validated before any record is touched", visited)
			}
		})
	}
}

// TestEvaluate_NarrowsToTheSchemaTypeByDefault is the guard on the wrong-answer
// case the refusal above cannot catch.
//
// A query with a schema and an EMPTY selector is unambiguous intent, so the
// record type is filled in. Without that, ordinary notes and records of other
// types stream into the comparator, every declared property reads ABSENT for
// them, and FR-008 then puts every one of them into the answer to a negative
// filter — a wrong answer with no error channel.
func TestEvaluate_NarrowsToTheSchemaTypeByDefault(t *testing.T) {
	store, _ := openIndex(t, Options{})
	sc := plantSchema(t)
	mustUpsert(t, store,
		note(t, "garden/a.md", sc, "---\ntype: plant\nid: PL-1\nspecies: Fern\ncondition: growing\n---\n"),
		// An ordinary note: no record type, no schema, no properties (FR-005).
		note(t, "garden/readme.md", nil, "---\ntitle: How the garden works\n---\n"),
	)

	// A NEGATIVE filter is the probe, because it is the one an unnarrowed
	// population silently inflates.
	got, rep := evaluate(t, store, Query{
		Schema:  sc,
		Filters: []records.Filter{{Property: "condition", Op: records.OpEqual, Literal: "dormant", LiteralGiven: true, Negate: true}},
	})
	assertPaths(t, got, []string{"garden/a.md"},
		"the ordinary note is not a plant and must never be narrowed into a plant query")
	if rep.Considered != 1 {
		t.Errorf("the comparator saw %d candidates; only the plant should have been narrowed to. "+
			"An ordinary note reaching a typed filter reads ABSENT for every property, and FR-008 "+
			"then includes it in every negative filter's answer", rep.Considered)
	}
}

// TestEvaluate_PropagatesTheBoundRefusal checks that FR-064's refusal reaches
// the caller as a refusal rather than as a short answer.
func TestEvaluate_PropagatesTheBoundRefusal(t *testing.T) {
	store, _ := openIndex(t, Options{})
	sc := seedConditions(t, store, "growing", "dormant")

	// B2 counts what the comparator ACCEPTED. Driving the real bound needs
	// 10,001 records; instead the visit callback's own error is used to prove
	// the error channel is not swallowed, and bounds_test.go drives the bounds
	// themselves against the store.
	sentinel := errors.New("the caller stopped the stream")
	_, err := Evaluate(context.Background(), store, Query{
		Schema:  sc,
		Filters: []records.Filter{{Property: "species", Op: records.OpIsNotNull}},
	}, func(Match) error { return sentinel })

	if !errors.Is(err, sentinel) {
		t.Fatalf("a visit error came back as %v; it must reach the caller unchanged, "+
			"never be swallowed into a short answer", err)
	}
}

// ---------------------------------------------------------------------------
// THE INDEX AND THE SCHEMA DISAGREE
// ---------------------------------------------------------------------------

// TestEvaluate_ReportsAStaleIndexRatherThanAssumingAbsence is the case that
// looks like a normal answer and is not.
//
// A property added to the schema after a note was indexed has NO state row for
// that note. "The note says nothing" and "this index does not know what the
// note says" are different facts, and FR-008 treats the first as an INCLUSION —
// so assuming the second is the first puts records into a negative filter's
// answer on the strength of a stale index.
func TestEvaluate_ReportsAStaleIndexRatherThanAssumingAbsence(t *testing.T) {
	store, _ := openIndex(t, Options{})

	// Index against a schema that does not declare `condition` yet.
	narrow, rej := records.ParseSchema("plant.yaml", []byte(`
schema_version: 1
type: plant
properties:
  species: { type: text }
`))
	if rej != nil {
		t.Fatalf("fixture schema: %s", rej.String())
	}
	mustUpsert(t, store, note(t, "garden/a.md", narrow,
		"---\ntype: plant\nid: PL-1\nspecies: Fern\ncondition: growing\n---\n"))

	// Now query with the WIDER schema, which declares `condition`.
	wide := plantSchema(t)
	got, rep := evaluate(t, store, Query{
		Schema:  wide,
		Filters: []records.Filter{{Property: "condition", Op: records.OpIsNull}},
	})

	// The record is still evaluated — the property reads absent — but the
	// answer must not claim to be complete.
	assertPaths(t, got, []string{"garden/a.md"}, "the property is treated as absent")
	if rep.Complete() {
		t.Fatal("the answer claims to be COMPLETE while the index holds no state for the property " +
			"being filtered on. That is the silent wrong answer ADR-068 exists to remove: the note " +
			"says `condition: growing` and the answer says it says nothing.")
	}
	var found bool
	for _, p := range rep.Problems {
		if p.Code == records.FindingStaleIndex && p.Property == "condition" && p.RecordPath == "garden/a.md" {
			found = true
			if !strings.Contains(p.Reason, "re-index") {
				t.Errorf("the problem does not name the remedy: %q", p.Reason)
			}
		}
	}
	if !found {
		t.Errorf("no stale_index problem named the record and property; got %+v", rep.Problems)
	}
}

// TestEvaluate_AnUndecodableValueExcludesAndReports is R-4 over a stored value.
//
// A value the current schema no longer admits is NOT absence: something is
// written there, it is just unreadable. It must be excluded and reported, never
// swept into a negative filter's answer by double negation.
func TestEvaluate_AnUndecodableValueExcludesAndReports(t *testing.T) {
	store, _ := openIndex(t, Options{})

	// Index with an enum that admits `retired`.
	old, rej := records.ParseSchema("plant.yaml", []byte(`
schema_version: 1
type: plant
properties:
  species:   { type: text }
  condition: { type: enum, values: [growing, retired] }
`))
	if rej != nil {
		t.Fatalf("fixture schema: %s", rej.String())
	}
	mustUpsert(t, store,
		note(t, "garden/a.md", old, "---\ntype: plant\nid: PL-1\nspecies: Fern\ncondition: retired\n---\n"),
		note(t, "garden/b.md", old, "---\ntype: plant\nid: PL-2\nspecies: Fern\ncondition: growing\n---\n"),
	)

	// Query with a schema that has dropped `retired`.
	now := plantSchema(t) // seedling / growing / dormant
	got, rep := evaluate(t, store, Query{
		Schema: now,
		// NEGATED, because that is the direction a mistake would hide in: a
		// value that "could not be compared" must NOT be re-included.
		Filters: []records.Filter{{Property: "condition", Op: records.OpEqual, Literal: "growing", LiteralGiven: true, Negate: true}},
	})

	assertPaths(t, got, nil,
		"a.md's stored value no longer decodes, so it is excluded and reported — never re-included by negation; "+
			"b.md is `growing`, which the negated filter excludes")
	if rep.Complete() {
		t.Fatal("the answer claims COMPLETE while a stored value could not be decoded")
	}
	var found bool
	for _, p := range rep.Problems {
		if p.Code == records.FindingStaleIndex && p.RecordPath == "garden/a.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("the undecodable record was excluded but NOT named; got %+v", rep.Problems)
	}
}

// TestEvaluate_NoFiltersReturnsTheWholeNarrowedPopulation is the scope-only
// query, and it is the case that proves the narrowing still reaches SQL.
func TestEvaluate_NoFiltersReturnsTheWholeNarrowedPopulation(t *testing.T) {
	store, _ := openIndex(t, Options{})
	seedConditions(t, store, "growing", "dormant", "seedling")
	mustUpsert(t, store, note(t, "shed/readme.md", nil, "---\ntitle: Shed\n---\n"))

	got, rep := evaluate(t, store, Query{Selector: Selector{PathPrefix: "garden/"}})
	if len(got) != 3 || rep.Matched != 3 {
		t.Fatalf("a scope-only query returned %v (matched %d); want the three notes under garden/", got, rep.Matched)
	}
	for _, p := range got {
		if !strings.HasPrefix(p, "garden/") {
			t.Errorf("%s is outside the requested scope", p)
		}
	}
}
