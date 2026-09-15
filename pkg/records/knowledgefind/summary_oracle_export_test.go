// Omnipus — the exported FR-155 oracle, graded against the engine it claims to
// predict. A writer that asks SummaryOpDefinedForType before it writes a saved
// view is only safe if the answer is the SAME answer Find would give.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS SEPARATELY FROM summaries_test.go
//
// summaries_test.go grades the ENGINE: given a request, does it refuse the
// right things. This file grades the EXPORT — the promise made to a caller
// outside this package that it may ask a cheap boolean instead of issuing a
// query. That promise is only worth anything if the boolean and the query
// never disagree, and "they call the same unexported function" is an
// implementation detail that a refactor can quietly end.
//
// So nothing below reads opsDefinedFor. Every expectation is either a hand
// written type/op fact from FR-150, or the OBSERVED behaviour of Find.
// ---------------------------------------------------------------------------

// bloomPropertyTypes is the bloom schema's declared types, written out by hand
// from bloomSchemaYAML rather than read back from the loaded set. Reading them
// back would make this table agree with itself.
var bloomPropertyTypes = map[string]records.PropertyType{
	"species":   records.TypeText,
	"height_cm": records.TypeDecimal,
	"cuttings":  records.TypeInteger,
	"planted":   records.TypeDate,
	"potted":    records.TypeCheckbox,
}

// TestSummaryOracleExport_PredictsFindExactly is the contract pkg/vaultimport's
// summary gate rests on, measured rather than asserted.
//
// Every (op, property) pair over a schema carrying one property of each type is
// put to BOTH surfaces: the exported predicate, and a real Find. A pair the
// predicate calls defined must ANSWER; a pair it calls undefined must REFUSE.
// A single disagreement means a writer using the export writes view files this
// engine will not serve — which is exactly the defect the export was added to
// close, reappearing one level up.
func TestSummaryOracleExport_PredictsFindExactly(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	// Every op except `count`, which takes no property and so has no type to
	// be defined over. The list is the closed fifteen minus that one.
	ops := []string{
		opSum, opAvg, opMedian, opStddev, opMin, opMax, opRange,
		opEarliest, opLatest, opChecked, opUnchecked,
		opEmpty, opFilled, opUnique,
	}

	agreed := 0
	for _, prop := range []string{"species", "height_cm", "cuttings", "planted", "potted"} {
		for _, op := range ops {
			t.Run(op+"_over_"+prop, func(t *testing.T) {
				predicted := SummaryOpDefinedForType(op, bloomPropertyTypes[prop])

				aggs := []generated.VaultFindAggregate{
					{Op: generated.VaultFindAggregateOp(op), Property: strPtr(prop)},
				}
				r := req(withType("bloom"))
				r.Aggregate = &aggs
				_, err := Find(t.Context(), f.deps(), r)
				answered := err == nil

				if predicted != answered {
					t.Fatalf("the exported oracle and the engine DISAGREE about %s(%s), a %s property: "+
						"SummaryOpDefinedForType said %v, Find %s. A writer trusting the export would %s.",
						op, prop, bloomPropertyTypes[prop], predicted,
						map[bool]string{true: "answered", false: "refused"}[answered],
						map[bool]string{true: "write a view this engine refuses to serve", false: "drop a summary that works"}[predicted])
				}
				agreed++
			})
		}
	}
	if agreed != len(ops)*len(bloomPropertyTypes) {
		t.Fatalf("only %d of %d pairs were actually put to both surfaces", agreed, len(ops)*len(bloomPropertyTypes))
	}
}

// TestSummaryOracleExport_NamesExactlyWhatTheRefusalNames holds the second half
// of the export: a caller refusing an op has to be able to say what the type
// DOES define, in the same words this package's own refusal uses. If the two
// lists drift, an operator reading an import report and an operator reading a
// query refusal are told different things about the same property.
//
// THE COMPARISON IS EQUALITY, NOT CONTAINMENT, and that is not pedantry. An
// earlier version of this test asked whether the refusal CONTAINED the exported
// list, and a mutation that truncated SummaryOpsDefinedFor to its first element
// survived it untouched — "empty" is contained in "empty, filled, unique". A
// caller told only the first of three alternatives is a caller sent back to
// guess, which is the failure FR-155 exists to remove.
func TestSummaryOracleExport_NamesExactlyWhatTheRefusalNames(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	// One op each type does NOT define, so each case provokes the refusal that
	// carries the list. Hand-picked from FR-150's domains, not read back.
	cases := map[string]string{
		"species":   opSum,      // sum over text
		"height_cm": opEarliest, // a date op over a number
		"cuttings":  opEarliest, // ditto
		"planted":   opSum,      // sum over a date
		"potted":    opSum,      // sum over a checkbox
	}

	for prop, op := range cases {
		t.Run(op+"_over_"+prop, func(t *testing.T) {
			aggs := []generated.VaultFindAggregate{
				{Op: generated.VaultFindAggregateOp(op), Property: strPtr(prop)},
			}
			r := req(withType("bloom"))
			r.Aggregate = &aggs
			resp := mustRefuse(t, f.deps(), r)
			if len(resp.Problems) != 1 {
				t.Fatalf("problems = %d, want 1: %+v", len(resp.Problems), resp.Problems)
			}
			reason := resp.Problems[0].Reason

			// The engine's own sentence ends "...and the summaries defined for
			// <type> are <list>". Take the list from the message rather than
			// from any table, so this side of the comparison is the OBSERVED
			// behaviour and not a second copy of the oracle.
			marker := " are "
			i := strings.LastIndex(reason, marker)
			if i < 0 {
				t.Fatalf("the refusal no longer names the defined summaries at all, so a caller is left guessing: %q", reason)
			}
			named := strings.TrimRight(strings.TrimSpace(reason[i+len(marker):]), ".")

			exported := strings.Join(SummaryOpsDefinedFor(bloomPropertyTypes[prop]), ", ")
			if named != exported {
				t.Errorf("the exported list and the engine's own refusal are not the same list for %s.\n  engine:   %q\n  exported: %q",
					bloomPropertyTypes[prop], named, exported)
			}
			if named == "" {
				t.Errorf("both sides are empty; every type defines empty, filled and unique")
			}
		})
	}
}

// TestSummaryOracleExport_AnUndefinedSummaryCostsEveryRow is the fact the
// importer's gate is JUSTIFIED by, and it is the one most easily assumed
// wrong: an undefined summary does not produce a blank total beside a full
// table. It takes the table with it.
//
// The two halves are the same request with and without the aggregate, so
// nothing here can pass by the query being empty for another reason.
func TestSummaryOracleExport_AnUndefinedSummaryCostsEveryRow(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	plain := mustFind(t, f.deps(), req(withType("bloom")))
	if len(plain.Rows) == 0 {
		t.Fatal("the corpus answered no rows without an aggregate, so the comparison below is vacuous")
	}

	aggs := []generated.VaultFindAggregate{{Op: generated.VaultFindAggregateOpSum, Property: strPtr("species")}}
	withSum := req(withType("bloom"))
	withSum.Aggregate = &aggs
	refused := mustRefuse(t, f.deps(), withSum)

	if len(refused.Rows) != 0 {
		t.Fatalf("sum over a text property returned %d row(s); the premise of the importer's summary gate "+
			"is that this refusal is TOTAL, and if it is partial the gate is over-strict", len(refused.Rows))
	}
	t.Logf("MEASURED: the same query answers %d row(s) without the summary and 0 with it — "+
		"one undefined summary costs every row, which is why a writer must not emit one", len(plain.Rows))
}
