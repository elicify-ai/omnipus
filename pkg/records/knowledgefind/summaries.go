// Omnipus — spec FR-150..FR-155: the fifteen summary functions, their two
// computational classes, and B3 (the column-buffer bound).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// THE FIFTEEN (FR-150)
// ---------------------------------------------------------------------------
//
// They are ops of the EXISTING `aggregates` surface. There is no `summaries`
// wire key anywhere — the founder's ruling 2, and the reason it matters here is
// that a second key for the same capability makes an importer choose between
// them, and every importer would choose differently.

const (
	opCount     = "count"
	opSum       = "sum"
	opAvg       = "avg"
	opMedian    = "median"
	opStddev    = "stddev"
	opMin       = "min"
	opMax       = "max"
	opRange     = "range"
	opEarliest  = "earliest"
	opLatest    = "latest"
	opChecked   = "checked"
	opUnchecked = "unchecked"
	opEmpty     = "empty"
	opFilled    = "filled"
	opUnique    = "unique"
)

// The four domains of FR-150, exactly as VaultFindAggregate's own description
// enumerates them. `count` is in none of them: it takes no property.
var (
	// opsAnyType are defined for EVERY declared type — absence and presence and
	// distinctness are questions any property can answer.
	opsAnyType = []string{opEmpty, opFilled, opUnique}
	// opsNumber is the number domain. §8 R-1 makes `integer` and `decimal` ONE
	// comparison domain, so they share one op set rather than two.
	opsNumber = []string{opSum, opAvg, opMedian, opStddev, opMin, opMax, opRange}
	// opsDate is the date domain. `range` is here too and renders as a
	// DURATION; `earliest`/`latest` are the domain's own names for its extremes.
	opsDate = []string{opEarliest, opLatest, opMin, opMax, opRange}
	// opsCheckbox counts `true` and `false`. ABSENT is counted by neither, so
	// checked + unchecked is NOT the row count — the checkbox third state
	// (ADR-068 D3.2) surviving into the summaries.
	opsCheckbox = []string{opChecked, opUnchecked}
)

// opsDefinedFor is FR-155's oracle: the summaries a type defines, sorted so the
// refusal message reads the same way every time.
func opsDefinedFor(t records.PropertyType) []string {
	var out []string
	switch t {
	case records.TypeInteger, records.TypeDecimal:
		out = append(out, opsNumber...)
	case records.TypeDate:
		out = append(out, opsDate...)
	case records.TypeCheckbox:
		out = append(out, opsCheckbox...)
	}
	out = append(out, opsAnyType...)
	sort.Strings(out)
	return out
}

// allSummaryOps is the closed list of fifteen, for a refusal that has to name
// them. It is derived from the domain tables rather than written out a second
// time, so the list a caller is told about cannot drift from the list the
// reducer implements.
func allSummaryOps() []string {
	out := []string{opCount}
	out = append(out, opsAnyType...)
	out = append(out, opsNumber...)
	out = append(out, opsCheckbox...)
	for _, o := range opsDate {
		if o != opMin && o != opMax && o != opRange {
			out = append(out, o)
		}
	}
	sort.Strings(out)
	return out
}

func opDefinedForType(op string, t records.PropertyType) bool {
	for _, o := range opsDefinedFor(t) {
		if o == op {
			return true
		}
	}
	return false
}

// isPopulationOp is FR-151's class split, and it is a CLOSED list of two.
//
// Everything else streams in O(1): a running accumulator and nothing else,
// including Stddev — which needs only count, sum and sum-of-squares, all three
// exact in rational arithmetic, with the single rounding at the final square
// root. Adding a third op here is adding a third thing that can exhaust memory,
// so it is a decision with a bound attached, not a convenience.
func isPopulationOp(op string) bool { return op == opMedian || op == opUnique }

// ---------------------------------------------------------------------------
// B3 — THE COLUMN-BUFFER BOUND (FR-151)
// ---------------------------------------------------------------------------
//
// B1 bounds the candidate POPULATION and B2 bounds the SURVIVORS. Neither
// bounds what Median and Unique hold, and that gap was real rather than
// theoretical: a value count is not a record count. A `many` property at 20
// elements over B1's 50,000 candidates is 1,000,000 values, and 1 KB text
// values at B1 are 50 MB — so "one column" is only small in the scalar-decimal
// case Draft 10 measured and presented as the general one.
//
// B3 is therefore its own bound with its own refusal, and it fires MID-SCAN:
// the point of a bound that aborts is that the memory is never allocated, which
// a post-hoc check cannot deliver.
const (
	// columnBufferMaxValues is B3's value half.
	columnBufferMaxValues = 100_000
	// columnBufferMaxBytes is B3's byte half — whichever is reached first wins.
	columnBufferMaxBytes = 8 << 20 // 8 MiB
)

// columnBuffer counts what a population-class summary is ACTUALLY holding.
//
// "Actually" is load-bearing and differs per op: Median retains every value, so
// every value is admitted; Unique retains one entry per DISTINCT key, so a
// column of a million identical values holds one key and is admitted once. The
// bound is on memory held, and accounting a value that was deduped away would
// refuse a query whose real footprint is a single string.
type columnBuffer struct {
	values int
	bytes  int
}

// admit reserves room for one more retained value, or reports that B3 is
// reached. A false return must abort the scan — continuing past it and refusing
// afterwards would allocate exactly the memory the bound exists to prevent.
func (b *columnBuffer) admit(n int) bool {
	if b.values+1 > columnBufferMaxValues || b.bytes+n > columnBufferMaxBytes {
		return false
	}
	b.values++
	b.bytes += n
	return true
}

// refusal names the count REACHED and the remedy, which is the shape near.go
// established for a mid-scan abort: a bound that says only "too big" leaves the
// caller guessing at how much too big, and they guess wrong in both directions.
func (b *columnBuffer) refusal(a aggregate) string {
	return fmt.Sprintf(
		"%s buffers one column and reached the column-buffer bound mid-scan, "+
			"holding %s value(s) / %s byte(s) — the bound is %s values or %s bytes, "+
			"whichever comes first; narrow the filter, or summarise a scalar property",
		a.label(), group3(b.values), group3(b.bytes),
		group3(columnBufferMaxValues), group3(columnBufferMaxBytes))
}

// bufferedBytes is what one value costs the buffer. It is the SOURCE bytes —
// the quantity FR-151's own arithmetic is stated in ("1 KB text values at B1
// are 50 MB") — so the bound and the estimate that produced it measure the same
// thing.
func bufferedBytes(v records.TypedValue) int {
	n := len(v.Raw)
	if n == 0 {
		n = len(renderTyped(v))
	}
	if n == 0 {
		n = 1
	}
	return n
}

// ---------------------------------------------------------------------------
// PRECISION, DECLARED (FR-152)
// ---------------------------------------------------------------------------

// summaryScaleBump is FR-152's "+ 2".
const summaryScaleBump = 2

// declaredSummaryScale answers "at what scale is this rendered" BEFORE the
// number is computed, so the answer can be stated in the label alongside it.
//
// FR-152 words the rule as "the property's declared scale + 2". There is no
// property-level scale to read: Stage 1 removed it from the schema outright
// (schema.go — "a `decimal` deliberately does NOT gain a property-level scale";
// the bound is per-value at parse time), and the surviving half of the rule is
// that a decimal renders at the scale the note itself wrote. So the base is
// derived, and derived the same way every time:
//
//   - an `integer` property declares scale 0, so its summaries render at 2;
//   - a `decimal` property's base is the LARGEST scale among the values that
//     actually entered the summary — the most precise thing any note said about
//     this property — so a column written to 2 places produces a mean at 4 and
//     never invents a precision no note claimed.
//
// FR-152 also calls the scale overridable. There is no wire field to override
// it with: RecordAggregate/VaultFindAggregate carry `op` and `property` only.
// That is a contract gap, named here rather than papered over with an invented
// field.
func declaredSummaryScale(a aggregate, sc *columnScan) int32 {
	var base int32
	if a.prop != nil && a.prop.Type == records.TypeDecimal {
		base = sc.maxScale
	}
	return base + summaryScaleBump
}

// roundedNote is the half of FR-152 that faces the reader: a rounded number
// says it is rounded, in the label, next to itself.
func roundedNote(scale int32) string {
	return fmt.Sprintf(" — exact, rendered rounded to %d decimal place(s), round-half-even", scale)
}

// populationNote is FR-153. Obsidian's documentation does not say which
// standard deviation theirs is, so ours DECLARES which one it is rather than
// guessing at a match; the importer records the divergence risk by name.
const populationNote = " — POPULATION standard deviation (divisor n)"

// ---------------------------------------------------------------------------
// THE SCAN
// ---------------------------------------------------------------------------

// columnScan is one pass over the evaluated set for one summary.
//
// Every field except `values` and `unique` is O(1) in the row count, which is
// FR-151's streaming class expressed as a struct: thirteen of the fifteen never
// touch the two that are not.
type columnScan struct {
	// Per-ROW state. A row is counted in exactly one of these three.
	withValue     int
	withoutValue  int
	nonConforming int

	// Per-VALUE count — NOT the row count, and the distinction is the whole of
	// FR-151's memory correction.
	count int

	// Number domain, streaming.
	sum      records.Decimal
	haveSum  bool
	sumErr   error
	ratSum   *big.Rat
	ratSumSq *big.Rat
	maxScale int32

	// Extremes, streaming, via the comparator — so `min` over dates and `min`
	// over numbers are one code path answered by one oracle.
	minV, maxV       records.TypedValue
	haveMin, haveMax bool

	// Checkbox, streaming.
	checked, unchecked, unreadable int

	// POPULATION CLASS ONLY — one of these two is non-empty, never both.
	values []records.TypedValue
	unique map[string]bool
}

// scanColumn walks the evaluated set once. A non-empty second return is B3
// firing: the scan aborted, and FR-154 forbids returning anything computed from
// what it had reached.
func scanColumn(q *query, a aggregate, rows []survivor) (*columnScan, string) {
	sc := &columnScan{ratSum: new(big.Rat), ratSumSq: new(big.Rat)}
	if a.op == opUnique {
		sc.unique = map[string]bool{}
	}
	var buf columnBuffer

	for _, s := range rows {
		pv, ok := s.values[a.property]
		switch {
		case !ok || pv.State == records.StateAbsent:
			sc.withoutValue++
			continue
		case pv.State == records.StateNonConforming:
			// §8 R-4: non-conformance is NOT absence. It is counted apart and
			// named in the scope, because folding it into "no value" is how a
			// corrupt value disappears from an answer.
			sc.nonConforming++
			sc.withoutValue++
			continue
		}
		// R-3: an empty list is a value, not absence — the row HAS the
		// property. It contributes no value to a column reduction, so it counts
		// as present and adds nothing.
		sc.withValue++

		for _, v := range pv.Values {
			sc.count++
			if isPopulationOp(a.op) {
				if !sc.buffer(q, a.op, v, &buf) {
					return nil, buf.refusal(a)
				}
				continue
			}
			switch a.op {
			case opChecked, opUnchecked:
				if v.Type != records.TypeCheckbox {
					// Unreachable: parse() refuses checked/unchecked on every
					// other type, and a checkbox value that was neither `true`
					// nor `false` never became a TypedValue at all — it is a
					// non-conforming property, counted above. Counted here
					// anyway, and named in the scope, because a value that is
					// silently in neither count is the checkbox third state
					// reappearing as a rounding error.
					sc.unreadable++
					continue
				}
				if v.Bool {
					sc.checked++
				} else {
					sc.unchecked++
				}
			default:
				sc.observe(v)
			}
		}
	}
	return sc, ""
}

// buffer retains one value for a population-class summary, or reports that B3
// is reached. It is the ONLY place either of the two buffers grows, so the
// bound cannot be bypassed by a second accumulation path being added later.
//
// What each op retains differs, and so does what it costs: Median keeps every
// value, priced at its source bytes; Unique keeps one entry per DISTINCT key,
// priced at the key, so a column of a million identical values holds one string
// and is charged for one.
func (sc *columnScan) buffer(q *query, op string, v records.TypedValue, buf *columnBuffer) bool {
	// The observed scale is tracked HERE as well as in observe(), because the
	// population class never reaches observe() and Median is rendered at a
	// declared scale derived from it. Without this line an even-count median
	// over `decimal` values renders at 2 places where its own Average renders
	// at 4 — two summaries of one column disagreeing about how precise that
	// column is, which is worse than either scale on its own.
	if v.Type == records.TypeInteger || v.Type == records.TypeDecimal {
		if s := v.Number.Scale(); s > sc.maxScale {
			sc.maxScale = s
		}
	}
	if op == opUnique {
		k := uniqueKey(q, v)
		if sc.unique[k] {
			return true
		}
		if !buf.admit(len(k)) {
			return false
		}
		sc.unique[k] = true
		return true
	}
	if !buf.admit(bufferedBytes(v)) {
		return false
	}
	sc.values = append(sc.values, v)
	return true
}

// observe folds one value into every streaming accumulator it belongs in.
func (sc *columnScan) observe(v records.TypedValue) {
	if v.Type == records.TypeInteger || v.Type == records.TypeDecimal {
		if s := v.Number.Scale(); s > sc.maxScale {
			sc.maxScale = s
		}
		switch {
		case !sc.haveSum:
			sc.sum, sc.haveSum = v.Number, true
		case sc.sumErr == nil:
			s, err := sc.sum.Add(v.Number)
			if err != nil {
				// An exact sum that cannot be represented is REFUSED, never
				// rounded to fit. A silently rounded total is a number nobody
				// computed wearing the authority of one that was.
				sc.sumErr = err
			} else {
				sc.sum = s
			}
		}
		r := ratOf(v.Number)
		sc.ratSum.Add(sc.ratSum, r)
		sc.ratSumSq.Add(sc.ratSumSq, new(big.Rat).Mul(r, r))
	}

	// The extremes go through records.Compare — the same three-valued oracle
	// the filter and the sort use. A `min` that reimplemented ordering would be
	// a second opinion about which of two values is smaller, and the two would
	// disagree on exactly the cases that are hard.
	if !sc.haveMin {
		sc.minV, sc.haveMin = v, true
	} else if c, ok := records.Compare(v, sc.minV); ok && c < 0 {
		sc.minV = v
	}
	if !sc.haveMax {
		sc.maxV, sc.haveMax = v, true
	} else if c, ok := records.Compare(v, sc.maxV); ok && c > 0 {
		sc.maxV = v
	}
}

// uniqueKey decides whether two values are ONE value for `unique`.
//
// It is the comparator's own notion of equality, not a string comparison of the
// rendered text, and the difference is visible in every domain:
//
//   - numbers: §8 R-1 makes `3` and `3.0` equal, so they share one key. Folding
//     the rendered text would count them as two.
//   - dates: HasTime does not affect comparison, so `2026-03-01` and
//     `2026-03-01T00:00:00Z` are one value.
//   - text and enum: R-5/R-D fold case, so `Won` and `won` are one — the same
//     rule that groups them together.
//   - relations and people: D5/R-8 identity, so an alias and a not-yet-rewritten
//     wikilink pointing at one record are one value. An UNRESOLVED link falls
//     back to its folded raw text rather than being dropped: grouping excludes
//     it (and reports it), but a count of distinct values that silently omitted
//     values would be the truncation FR-154 exists to forbid.
func uniqueKey(q *query, v records.TypedValue) string {
	switch v.Type {
	case records.TypeInteger, records.TypeDecimal:
		return "n:" + ratOf(v.Number).RatString()
	case records.TypeDate:
		return "d:" + v.Date.Instant.UTC().Format(time.RFC3339Nano)
	case records.TypeEnum:
		return "e:" + records.FoldKey(v.Enum.Name)
	case records.TypeCheckbox:
		if v.Bool {
			return "b:true"
		}
		return "b:false"
	case records.TypeRelation, records.TypePerson:
		if q != nil && q.resolve != nil {
			if id, ok := q.resolve(v.Link); ok && id != "" {
				return "\x01id:" + id
			}
		}
		return "t:" + records.FoldKey(v.Link.Raw)
	}
	return "t:" + records.FoldKey(renderTyped(v))
}

// ---------------------------------------------------------------------------
// THE REDUCTION
// ---------------------------------------------------------------------------

// reduceAggregate computes ONE summary over the FULL evaluated set.
//
// The scope clause is built here, beside the number, for the reason FR-125
// gives: a total whose scope is attached by a later layer is a total that will
// eventually be rendered without one.
func reduceAggregate(q *query, a aggregate, rows []survivor) generated.VaultFindTotal {
	t := generated.VaultFindTotal{
		Op:    generated.VaultFindTotalOp(a.op),
		Label: a.label(),
	}
	shown := len(rows)
	if shown > q.limit {
		shown = q.limit
	}
	wholeSet := fmt.Sprintf("over %d of %d evaluated rows (%d shown)", len(rows), len(rows), shown)

	// `count` counts ROWS and takes no property, so it never scans a column.
	if a.op == opCount {
		t.Value = group3(len(rows))
		t.Scope = wholeSet
		return t
	}

	// `empty` and `filled` read the property's STATE, over any type. They are
	// per-ROW questions, so they are answered before any per-value machinery.
	if a.op == opEmpty || a.op == opFilled {
		return rowStateTotal(t, a, rows, wholeSet)
	}

	sc, refusal := scanColumn(q, a, rows)
	if refusal != "" {
		// FR-154. The scan aborted, so there is no set to summarise — and a
		// summary over what happened to fit is the confidently-wrong answer
		// this whole surface exists to remove.
		return refuseTotal(t, refusal)
	}

	scope := scopeClause(a, sc, len(rows), shown)

	switch a.op {
	case opSum:
		if sc.sumErr != nil {
			return refuseTotal(t, sc.sumErr.Error())
		}
		if !sc.haveSum {
			return refuseTotal(t, noValueReason(a, len(rows)))
		}
		t.Value = groupDigits(sc.sum.String())

	case opMin, opEarliest:
		if !sc.haveMin {
			return refuseTotal(t, noValueReason(a, len(rows)))
		}
		t.Value = renderTyped(sc.minV)

	case opMax, opLatest:
		if !sc.haveMax {
			return refuseTotal(t, noValueReason(a, len(rows)))
		}
		t.Value = renderTyped(sc.maxV)

	case opRange:
		if !sc.haveMin || !sc.haveMax {
			return refuseTotal(t, noValueReason(a, len(rows)))
		}
		value, err := rangeValue(sc.minV, sc.maxV)
		if err != nil {
			return refuseTotal(t, err.Error())
		}
		t.Value = value

	case opAvg:
		if sc.count == 0 {
			return refuseTotal(t, noValueReason(a, len(rows)))
		}
		mean := new(big.Rat).Quo(sc.ratSum, new(big.Rat).SetInt64(int64(sc.count)))
		scale := declaredSummaryScale(a, sc)
		t.Value = groupDigits(renderRounded(mean, scale))
		t.Label = a.label() + roundedNote(scale)

	case opMedian:
		if len(sc.values) == 0 {
			return refuseTotal(t, noValueReason(a, len(rows)))
		}
		vals := sc.values
		sort.SliceStable(vals, func(i, j int) bool {
			c, ok := records.Compare(vals[i], vals[j])
			return ok && c < 0
		})
		n := len(vals)
		if n%2 == 1 {
			// An odd-count median IS one of the column's own values, so it is
			// rendered exactly as the note wrote it — nothing is computed, so
			// nothing is rounded, and the label says nothing about rounding.
			t.Value = renderTyped(vals[n/2])
			break
		}
		lo, hi := ratOf(vals[n/2-1].Number), ratOf(vals[n/2].Number)
		mid := new(big.Rat).Quo(new(big.Rat).Add(lo, hi), new(big.Rat).SetInt64(2))
		scale := declaredSummaryScale(a, sc)
		t.Value = groupDigits(renderRounded(mid, scale))
		t.Label = a.label() + roundedNote(scale)

	case opStddev:
		if sc.count == 0 {
			return refuseTotal(t, noValueReason(a, len(rows)))
		}
		n := new(big.Rat).SetInt64(int64(sc.count))
		mean := new(big.Rat).Quo(sc.ratSum, n)
		variance := new(big.Rat).Sub(
			new(big.Rat).Quo(sc.ratSumSq, n),
			new(big.Rat).Mul(mean, mean))
		if variance.Sign() < 0 {
			// Unreachable while the arithmetic above is exact rationals —
			// E[X²] ≥ E[X]² is not an approximation there. It is guarded
			// anyway so that an inexact accumulator introduced later fails as a
			// zero rather than as a panic inside big.Int.Sqrt.
			variance = new(big.Rat)
		}
		scale := declaredSummaryScale(a, sc)
		t.Value = groupDigits(sqrtRounded(variance, scale))
		t.Label = a.label() + populationNote + roundedNote(scale)

	case opUnique:
		t.Value = group3(len(sc.unique))

	case opChecked:
		t.Value = group3(sc.checked)

	case opUnchecked:
		t.Value = group3(sc.unchecked)

	default:
		// parse() rejects an op outside the fifteen before anything is
		// retrieved, so reaching here means the two lists have drifted apart.
		// Saying so beats returning an empty value that reads as an answer.
		return refuseTotal(t, fmt.Sprintf("%q is not a summary this build can compute", a.op))
	}

	t.Scope = scope
	return t
}

// rowStateTotal answers `empty` and `filled` — the two summaries defined for
// every type, because every property is either recorded on a record or not.
func rowStateTotal(t generated.VaultFindTotal, a aggregate, rows []survivor, wholeSet string) generated.VaultFindTotal {
	absent, present, nonConforming := 0, 0, 0
	for _, s := range rows {
		pv, ok := s.values[a.property]
		switch {
		case !ok || pv.State == records.StateAbsent:
			absent++
		case pv.State == records.StateNonConforming:
			nonConforming++
		default:
			present++
		}
	}
	n := present
	if a.op == opEmpty {
		n = absent
	}
	t.Value = group3(n)
	t.Scope = wholeSet
	if nonConforming > 0 {
		// R-4 again: a non-conforming value is neither absent nor a value, so
		// empty + filled does not reach the row count and the scope says why.
		t.Scope += fmt.Sprintf("; %d row(s) hold a non-conforming %s, counted by neither empty nor filled",
			nonConforming, a.property)
	}
	return t
}

// scopeClause is FR-125: what the number covers, and what it EXCLUDED, in the
// same sentence as the number.
func scopeClause(a aggregate, sc *columnScan, evaluated, shown int) string {
	s := fmt.Sprintf("over %d of %d evaluated rows (%d shown)", sc.withValue, evaluated, shown)
	if sc.count != sc.withValue {
		// A `many` property reduces over VALUES, not rows, and the two counts
		// differ. Leaving that implicit is how a reader takes a value count for
		// a record count.
		s += fmt.Sprintf("; %d value(s) read across them", sc.count)
	}
	if sc.withoutValue > 0 {
		s += fmt.Sprintf("; %d row(s) carry no %s and are not included", sc.withoutValue, a.property)
	}
	if sc.nonConforming > 0 {
		s += fmt.Sprintf(" (%d of them hold a non-conforming %s)", sc.nonConforming, a.property)
	}
	if sc.unreadable > 0 {
		s += fmt.Sprintf("; %d value(s) are neither true nor false and are counted by neither", sc.unreadable)
	}
	return s
}

func noValueReason(a aggregate, evaluated int) string {
	return fmt.Sprintf("none of the %d evaluated rows carries a value for %s", evaluated, a.property)
}

// refuseTotal is FR-154's shape: the total is PRESENT and marked refused,
// carrying no value. An omitted total reads as "there was nothing to add up",
// and a zero reads as an answer.
func refuseTotal(t generated.VaultFindTotal, reason string) generated.VaultFindTotal {
	yes := true
	t.Refused = &yes
	t.Value = ""
	t.Scope = "no total: " + reason
	return t
}

// rangeValue is max − min in whichever domain the values are in.
func rangeValue(lo, hi records.TypedValue) (string, error) {
	switch hi.Type {
	case records.TypeInteger, records.TypeDecimal:
		d, err := hi.Number.Sub(lo.Number)
		if err != nil {
			return "", err
		}
		return groupDigits(d.String()), nil
	case records.TypeDate:
		// FR-150: a date range renders as a DURATION. Two dates with no time
		// of day describe whole days, and rendering "8760h0m0s" for a year
		// would be an answer nobody asked in the units nobody wrote.
		d := hi.Date.Instant.Sub(lo.Date.Instant)
		if !lo.Date.HasTime && !hi.Date.HasTime {
			return fmt.Sprintf("%d days", int64(d/(24*time.Hour))), nil
		}
		return d.String(), nil
	}
	return "", fmt.Errorf("range is not defined over %s values", hi.Type)
}

// ---------------------------------------------------------------------------
// EXACT ARITHMETIC (FR-144's no-binary-float rule, applied to summaries)
// ---------------------------------------------------------------------------
//
// Every computed summary is exact until the moment it is rendered, and it is
// rendered at a scale that was DECLARED before the number existed. No float64
// appears anywhere in this file: a mean that round-tripped through a binary
// float would state digits nobody computed, which is the defect the whole type
// system was built to remove.

// ratOf converts an exact decimal to an exact rational. Both are exact, so the
// conversion is lossless in both directions.
func ratOf(d records.Decimal) *big.Rat {
	r := new(big.Rat).SetInt(d.Unscaled())
	switch s := int64(d.Scale()); {
	case s > 0:
		r.Quo(r, new(big.Rat).SetInt(pow10big(s)))
	case s < 0:
		r.Mul(r, new(big.Rat).SetInt(pow10big(-s)))
	}
	return r
}

func pow10big(n int64) *big.Int {
	if n <= 0 {
		return big.NewInt(1)
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)
}

// renderRounded renders an exact rational at `scale` decimal places, rounding
// half to EVEN.
//
// Half-even rather than half-up because a column of values ending in exactly .5
// is not rare in vault data — prices, hours, halves of anything — and half-up
// biases every such column upward by a consistent amount. The rule is named in
// the label so the reader never has to infer which one ran.
func renderRounded(v *big.Rat, scale int32) string {
	neg := v.Sign() < 0
	a := new(big.Rat).Abs(v)
	a.Mul(a, new(big.Rat).SetInt(pow10big(int64(scale))))

	q, rem := new(big.Int).QuoRem(a.Num(), a.Denom(), new(big.Int))
	switch new(big.Int).Lsh(rem, 1).Cmp(a.Denom()) {
	case 1:
		q.Add(q, big.NewInt(1))
	case 0:
		if q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
	}
	s := formatScaled(q, scale)
	if neg && q.Sign() != 0 {
		// A value that rounds to zero renders as "0.00", never "-0.00": the
		// minus would assert a sign the rendered number does not have.
		s = "-" + s
	}
	return s
}

// sqrtRounded is FR-151's "rounding only at the final square root".
//
// The variance reaches here EXACT. Taking its square root is the one step that
// leaves the rationals, so it is done in integer arithmetic at the target scale
// and rounded once, half to even — never by computing a float and formatting it.
//
// Writing v = N/D after scaling by 10^(2·scale), the integer answer is
// round(sqrt(N/D)). floor(sqrt(N/D)) is floor(isqrt(N·D)/D), and the half-way
// test compares the true value against (r + ½)² = (2r+1)²/4, i.e. 4N against
// D·(2r+1)² — all integers, so the comparison is exact and the tie is real
// rather than an artefact of the representation.
func sqrtRounded(v *big.Rat, scale int32) string {
	t := new(big.Rat).Mul(v, new(big.Rat).SetInt(pow10big(2*int64(scale))))
	n, d := t.Num(), t.Denom()

	r := new(big.Int).Sqrt(new(big.Int).Mul(n, d))
	r.Quo(r, d)

	lhs := new(big.Int).Mul(big.NewInt(4), n)
	m := new(big.Int).Add(new(big.Int).Lsh(r, 1), big.NewInt(1))
	rhs := new(big.Int).Mul(d, new(big.Int).Mul(m, m))
	switch lhs.Cmp(rhs) {
	case 1:
		r.Add(r, big.NewInt(1))
	case 0:
		if r.Bit(0) == 1 {
			r.Add(r, big.NewInt(1))
		}
	}
	return formatScaled(r, scale)
}

// formatScaled writes a non-negative integer as a decimal with `scale` places.
func formatScaled(q *big.Int, scale int32) string {
	digits := q.String()
	if scale <= 0 {
		return digits
	}
	for int32(len(digits)) <= scale {
		digits = "0" + digits
	}
	cut := len(digits) - int(scale)
	return digits[:cut] + "." + digits[cut:]
}
