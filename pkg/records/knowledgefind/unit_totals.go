// Omnipus — view-kinds-design-2026-09-03 §3 G2/G3, ruled as D7 (2026-09-05):
// the FIND layer's per-unit totals.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// `aggregate` used to reduce a column with no idea that PropertyDef.
// unit_property existed, so sum(amount) over 100.50 SGD and 200.00 EUR
// answered "300.50" — a figure in no currency, stated with the same confidence
// as a correct one, in the surface an agent reads directly. The view endpoint
// had already been fixed and was compensating: it refused to route a view
// total through this engine at all. That compensation was a wall around one
// door in a building with two.
//
// G2 is now enforced HERE, where the arithmetic happens, so both surfaces
// inherit it and neither has to re-derive it. The view endpoint's own
// per-unit reducer (pkg/gateway/rest_knowledge_view.go, aggregateViewRows)
// still exists because it reads the engine's RENDERED CELLS for a different
// purpose — but every RULE the two share is stated once, in this file, and
// delegated to from there. That direction is forced: pkg/gateway imports this
// package and cannot be imported by it.
//
// THE FOUR DECISIONS (D7 in the design doc's §9), because the code is where
// they are enforced and a reader should not have to leave it to know them:
//
//	D7.1  ONE TOTAL PER UNIT VALUE. One requested aggregate yields N totals,
//	      each carrying `unit`, `unit_property` and its own FR-125 scope
//	      clause. No combined figure is expressible.
//	D7.2  ONE column budget, not N. B3 (FR-151) is a bound on memory HELD, and
//	      partitioning does not make a value cost more, so the single
//	      columnBuffer is shared across every partition — the stated guarantee
//	      is unchanged rather than multiplied by unit cardinality. The NUMBER
//	      of partitions gets its own bound (unitPartitionMax) with its own
//	      refusal.
//	D7.3  THE UNTYPED CASE REFUSES, exactly as the view endpoint's ruled
//	      behaviour does: a query naming no `type` refuses to total any
//	      property ANY in-scope schema pairs with a unit, naming the declaring
//	      types and the fix. A property no schema pairs with a unit keeps its
//	      unit-less total — with no declaration there are no units to cross.
//	D7.4  THE SCOPE CLAUSE IS EMITTED PER UNIT (FR-125), over that unit's own
//	      row subset, with the G3 exclusion counted in the same sentence.
// ---------------------------------------------------------------------------

// unitPartitionMax is D7.2's bound on the NUMBER of per-unit partitions.
//
// It is a SEPARATE bound from B3 and it exists for a different reason. B3
// bounds memory; this bounds the ANSWER. A unit column with more than 64
// distinct values is not a unit column — it is a number or an identifier
// declared as one by mistake — and 64 totals already exceed what the 4 kB
// response budget can render or a reader can use. Past it the honest answer is
// a refusal that names the bound: a truncated list of totals would look
// complete, which is the one output this whole surface exists to remove.
const unitPartitionMax = 64

// AggregateCrossesNoUnits is the closed list of summaries whose answer is a
// COUNT rather than a quantity.
//
// Counting rows, absences, presences or distinct values says nothing about
// what the numbers are denominated in, so no unit can be crossed and no
// partition is owed: `unique(amount)` over SGD and EUR is "how many distinct
// amounts", which is a dimensionless number and a correct one. Everything else
// either combines values (sum, avg, median, stddev, range) or SELECTS one of
// them as representative (min, max, earliest, latest), and both kinds answer
// in the unit's own domain.
//
// It is exported because the view endpoint gates its legacy `aggregates:` key
// on the same question, and two closed lists of one closed set drift the first
// time either is extended.
func AggregateCrossesNoUnits(op string) bool {
	switch op {
	case opCount, opEmpty, opFilled, opUnique:
		return true
	default:
		return false
	}
}

// TypesDeclaringUnitFor lists, sorted, every record type in scope whose schema
// declares `prop` with a companion unit property.
//
// It is the question D7.3 turns on, asked identically by this engine's untyped
// gate and by the view endpoint's.
func TypesDeclaringUnitFor(set *records.SchemaSet, prop string) []string {
	if set == nil {
		return nil
	}
	var out []string
	for _, t := range set.Types() {
		sc, ok := set.Get(t)
		if !ok || sc == nil {
			continue
		}
		if p, found := sc.Property(prop); found && p != nil && p.UnitProperty != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// UntypedUnitTotalReason is D7.3's refusal, in the words both surfaces use.
//
// `subject` is what has no `type` — "query" here, "view" at the endpoint. The
// REMEDY is deliberately NOT shared: adding `type=invoice` to a request and
// declaring `type:` in a view file are different acts on different artefacts,
// and a fix line that fits both would name neither.
func UntypedUnitTotalReason(subject, prop string, declaring []string) string {
	return fmt.Sprintf(
		"this %s declares no `type`, and record type %s declares %q with a companion unit — "+
			"a total that cannot resolve units would add across them (G2); the rows themselves are still shown",
		subject, strings.Join(declaring, "/"), prop)
}

// ExcludedFromUnitTotalsReason is G3's footer sentence: how many rows are in no
// total, and WHY, with each cause carrying its own count.
//
// A row with NO unit and a row with TWO are both rightly excluded — neither has
// confirmed which unit its number is in — but they are excluded for opposite
// reasons with opposite fixes: fill one in, or pick one of two. Reporting both
// as "no confirmed currency value" tells an operator to supply a value that is
// already there twice.
func ExcludedFromUnitTotalsReason(missing, ambiguous int, unitProps []string) string {
	prop := strings.Join(unitProps, "/")
	// rowsVerbOf pairs the noun with its verb, so the plural branch cannot
	// drift from the singular one (UAT finding D4: "3 rows has").
	rowsVerbOf := func(n int, singular, plural string) string {
		if n == 1 {
			return "1 row " + singular
		}
		return fmt.Sprintf("%d rows %s", n, plural)
	}
	rowsOf := func(n int) string {
		if n == 1 {
			return "1 row"
		}
		return fmt.Sprintf("%d rows", n)
	}
	var causes []string
	if missing > 0 {
		causes = append(causes, fmt.Sprintf("%s no confirmed %s value", rowsVerbOf(missing, "has", "have"), prop))
	}
	if ambiguous > 0 {
		causes = append(causes, fmt.Sprintf("%s more than one %s value, so which one its number is in is not confirmed",
			rowsVerbOf(ambiguous, "records", "record"), prop))
	}
	return fmt.Sprintf("%s excluded from every total (G3), still shown: %s",
		rowsOf(missing+ambiguous), strings.Join(causes, "; "))
}

// ---------------------------------------------------------------------------
// The scope one total covers
// ---------------------------------------------------------------------------

// totalScope is what reduceAggregate needs in order to state FR-125's clause
// for a total computed over a SUBSET of the evaluated rows.
//
// Before D7 the denominator was simply len(rows), because every total saw every
// row. A per-unit total does not, and a scope reading "over 1 of 1 evaluated
// rows" for one of three would be a true sentence about the wrong set.
type totalScope struct {
	// evaluated is the FULL evaluated set — the denominator, always.
	evaluated int
	// shown is how many rows the response renders.
	shown int

	// unit is the unit value this total covers, empty for a unit-less total.
	unit string
	// unitProperty is the companion property `unit` was read from.
	unitProperty string

	// The G3 exclusions of the WHOLE split, repeated on every partition's
	// clause. They are stated per total rather than once at the end because
	// FR-125's rule is that a total carries its own qualification: a reader who
	// takes one line out of the answer must take the caveat with it.
	excludedMissing   int
	excludedAmbiguous int
}

// clauses is the per-unit tail appended to FR-125's standard scope sentence.
// The standard clause keeps its exact wording and position, so a unit-LESS
// total is byte-identical to what it was before D7.
func (ts totalScope) clauses() string {
	if ts.unit == "" {
		return ""
	}
	s := fmt.Sprintf("; in %s only — one total per unit value, never combined across units (G2)", ts.unit)
	if ts.excludedMissing+ts.excludedAmbiguous > 0 {
		s += "; " + ExcludedFromUnitTotalsReason(ts.excludedMissing, ts.excludedAmbiguous, []string{ts.unitProperty})
	}
	return s
}

// ---------------------------------------------------------------------------
// Splitting the rows
// ---------------------------------------------------------------------------

// unitOutcome is what reading one row's unit settled. It mirrors the view
// endpoint's viewUnitOutcome exactly; the two read different inputs (typed
// values here, rendered cells there) and reach the same three answers.
type unitOutcome int

const (
	unitConfirmed unitOutcome = iota
	// unitMissing: the row records no unit at all, or one that could not be
	// read.
	unitMissing
	// unitAmbiguous: MORE THAN ONE value. The row HAS a unit — it has two —
	// and its number is in one of them, unknowably.
	unitAmbiguous
)

// unitOf reads one row's companion-unit property.
func unitOf(s survivor, unitProp string) (display string, outcome unitOutcome) {
	pv, ok := s.values[unitProp]
	if !ok || pv.State == records.StateAbsent || pv.State == records.StateNonConforming {
		return "", unitMissing
	}
	switch len(pv.Values) {
	case 0:
		// R-3: an empty list IS a value, and it still confirms no unit.
		return "", unitMissing
	case 1:
		return renderTyped(pv.Values[0]), unitConfirmed
	default:
		return "", unitAmbiguous
	}
}

// hasNumberValue reports whether a row contributes anything to a column
// reduction at all. A row that does NOT is counted by scanColumn's own
// withoutValue/nonConforming counters, which is why it is carried into every
// partition rather than dropped (see unitSplit.parts).
func hasNumberValue(s survivor, prop string) bool {
	pv, ok := s.values[prop]
	if !ok || pv.State == records.StateAbsent || pv.State == records.StateNonConforming {
		return false
	}
	return len(pv.Values) > 0
}

type unitPartition struct {
	// key is the FOLDED unit value — `SGD` and `sgd` are one unit, the same
	// rule R-5 groups an enum by.
	key string
	// display is the first spelling seen for that key, so the answer says the
	// unit back the way a note wrote it.
	display string
	rows    []survivor
}

// unitSplit is one aggregate's rows, partitioned by confirmed unit value.
type unitSplit struct {
	parts     []unitPartition
	missing   int
	ambiguous int
	// refusal is non-empty when unitPartitionMax fired. Nothing computed from
	// a partial split may be returned (the FR-154 posture, applied to D7.2's
	// second bound).
	refusal string
}

func (u unitSplit) excluded() int { return u.missing + u.ambiguous }

// partitionByUnit splits the evaluated set by confirmed unit value.
//
// ROWS WITH NO NUMBER AT ALL ARE CARRIED INTO EVERY PARTITION. They contribute
// no value to any total — scanColumn counts them as withoutValue and states
// them in the scope ("3 rows carry no amount and are not included") — and
// dropping them would silently delete that clause from every per-unit answer.
// Carrying them keeps ONE authority for the counting: scanColumn's own, exactly
// as for a unit-less total.
func partitionByUnit(rows []survivor, numberProp, unitProp string) unitSplit {
	var (
		split   unitSplit
		byKey   = map[string]int{}
		carried []survivor
	)
	for _, s := range rows {
		if !hasNumberValue(s, numberProp) {
			carried = append(carried, s)
			continue
		}
		display, outcome := unitOf(s, unitProp)
		switch outcome {
		case unitMissing:
			split.missing++
			continue
		case unitAmbiguous:
			split.ambiguous++
			continue
		}
		key := records.FoldKey(display)
		idx, seen := byKey[key]
		if !seen {
			if len(split.parts) >= unitPartitionMax {
				split.refusal = fmt.Sprintf(
					"%q holds more than %d distinct values, and a number with a companion unit totals once per "+
						"unit value (G2) — more totals than that is not an answer a reader can use, and a truncated "+
						"list of them would look complete; narrow the filter to fewer %s values, or total a property "+
						"with no companion unit",
					unitProp, unitPartitionMax, unitProp)
				return unitSplit{refusal: split.refusal}
			}
			idx = len(split.parts)
			byKey[key] = idx
			split.parts = append(split.parts, unitPartition{key: key, display: display})
		}
		split.parts[idx].rows = append(split.parts[idx].rows, s)
	}
	sort.Slice(split.parts, func(i, j int) bool { return split.parts[i].key < split.parts[j].key })
	for i := range split.parts {
		split.parts[i].rows = append(split.parts[i].rows, carried...)
	}
	return split
}

// ---------------------------------------------------------------------------
// The driver
// ---------------------------------------------------------------------------

// newTotal is the empty total one aggregate answers with, before any reduction
// — the shape a refusal carries too, so a refused total is never an omission.
func newTotal(a aggregate) generated.VaultFindTotal {
	return generated.VaultFindTotal{
		Op:    generated.VaultFindTotalOp(a.op),
		Label: a.label(),
	}
}

// unitContextFor answers, for one requested aggregate: which companion unit
// property governs it (empty for none), and whether totalling it is permitted
// at all (a non-empty second return is the refusal, already worded).
//
// G4 needs no gate here: parse() already refuses a summary the property's
// DECLARED type does not define (opDefinedForType / FR-155), so `sum` over a
// text property never reaches this file — it is refused before anything is
// retrieved, naming the summaries text does define. An undeclared name in an
// untyped query resolves in the text domain by rule, so it is refused by the
// same gate.
func unitContextFor(q *query, a aggregate) (unitProp, refusal string) {
	if a.property == "" || AggregateCrossesNoUnits(a.op) {
		return "", ""
	}
	if q.recordType != "" {
		if a.prop != nil {
			return a.prop.UnitProperty, ""
		}
		return "", ""
	}
	// D7.3 — untyped. The namespace resolved this name across every in-scope
	// type and deliberately kept no companion unit (there is no single
	// declaration to read one from), so the question is asked of the schema set
	// directly.
	declaring := TypesDeclaringUnitFor(q.set, a.property)
	if len(declaring) == 0 {
		return "", ""
	}
	return "", UntypedUnitTotalReason("query", a.property, declaring) +
		fmt.Sprintf(" — add type=%s so %q resolves its companion unit and totals once per unit value, "+
			"or total a property no record type pairs with a unit", declaring[0], a.property)
}

// reduceAggregateSet computes every total ONE requested aggregate answers with:
// one when the number carries no unit, N when it does, or a single refused one
// when D7 forbids the reduction outright.
func reduceAggregateSet(q *query, a aggregate, rows []survivor, shown int) []generated.VaultFindTotal {
	base := totalScope{evaluated: len(rows), shown: shown}

	// D7.2 — ONE budget, declared here and threaded through every partition, so
	// B3's stated bound is on this aggregate's total memory rather than on each
	// partition's share of it.
	var buf columnBuffer

	unitProp, refusal := unitContextFor(q, a)
	if refusal != "" {
		return []generated.VaultFindTotal{refuseTotal(newTotal(a), refusal)}
	}
	if unitProp == "" {
		return []generated.VaultFindTotal{reduceAggregate(q, a, rows, base, &buf)}
	}

	split := partitionByUnit(rows, a.property, unitProp)
	if split.refusal != "" {
		return []generated.VaultFindTotal{refuseTotal(newTotal(a), split.refusal)}
	}
	if len(split.parts) == 0 {
		if split.excluded() == 0 {
			// Nothing carried a value at all. That is not a unit problem, and
			// the unit-blind path already words it exactly right ("none of the
			// N evaluated rows carries a value for amount").
			return []generated.VaultFindTotal{reduceAggregate(q, a, rows, base, &buf)}
		}
		return []generated.VaultFindTotal{refuseTotal(newTotal(a), fmt.Sprintf(
			"no row carrying a value for %q has a confirmed %s, so there is no unit to total in — %s",
			a.property, unitProp,
			ExcludedFromUnitTotalsReason(split.missing, split.ambiguous, []string{unitProp})))}
	}

	out := make([]generated.VaultFindTotal, 0, len(split.parts))
	for _, p := range split.parts {
		ts := base
		ts.unit, ts.unitProperty = p.display, unitProp
		ts.excludedMissing, ts.excludedAmbiguous = split.missing, split.ambiguous
		out = append(out, reduceAggregate(q, a, p.rows, ts, &buf))
	}
	return out
}
