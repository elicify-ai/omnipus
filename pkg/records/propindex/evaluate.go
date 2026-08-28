// Omnipus — ADR-068 D16.2b (as reversed by ruling R-A) / D16.6, FR-021,
// FR-023..FR-026, FR-064: narrow in SQL, decide in Go.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package propindex

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// It is the join between the two halves of ruling R-A. The store NARROWS — a
// record type, a note kind, a path prefix, all set membership over indexed
// columns. This file DECIDES, in Go, through the same comparator and the same
// matching layer that a frontmatter-sourced filter goes through
// (records.PreparedFilter.MatchValue).
//
// The ruling is only worth something if the second half actually happens here.
// SQLite's defaults contradict nine of the thirteen comparison rules and the
// contradictions are silent:
//
//	'3' > 2                         -> 1       (any text outranks any number)
//	NOT (status = 'done') over NULL -> 0 rows  (the guarded form returns 1)
//	ORDER BY over an enum column    -> alphabetical, not declared order
//	'ACME' LIKE '%acme%'            -> 1
//	lower('MÜLLER')                 -> 'mÜller' (folds ASCII ONLY)
//
// Those become NOT APPLICABLE rather than defeated — but only while every
// comparison is taken here. sqlgate_test.go is the control that keeps it so.
//
// ---------------------------------------------------------------------------
// THE FAN-OUT, AND WHY IT IS THE COMPARATOR'S PROBLEM IF IT LEAKS
//
// A record with eight declared properties and a two-element list occupies a
// dozen rows of the `notes LEFT JOIN note_props` result. Those rows are
// assembled into ONE Candidate before visit is called (sqlite.go's
// streamCandidates), and this file asserts the property rather than assuming
// it: a record arriving at the comparator twice is a HARD ERROR, not a
// duplicate to tidy up afterwards.
//
// It matters more than tidiness. The comparator's element-wise rule (R-9) reads
// a `many` property's values as ONE operand — `labels IN ('indoor')` is a
// membership test over the whole list. Half a list is a different operand from
// the whole list, and it answers a different question without saying so. And a
// record visited twice is counted twice against FR-064's B2, which bounds
// MEMORY by counting what the comparator accepted.
// ---------------------------------------------------------------------------

// Query is one typed question: what to narrow on, and what to decide.
type Query struct {
	// Selector is the narrowing. It is the ONLY thing that reaches SQL.
	Selector Selector

	// Schema declares the record type the filters are written against. It is
	// required whenever Filters is non-empty — a filter names a property, and a
	// property only exists inside a declaration.
	Schema *records.Schema

	// Filters is a CONJUNCTION: a record matches when every filter matches. An
	// empty list matches every candidate, which is how a scope-only query is
	// expressed.
	//
	// This is deliberately not a boolean tree. Grouping, ordering, aggregation
	// and disjunction belong to the caller building a query surface on top;
	// what this file owns is the guarantee that each leaf is decided in Go.
	Filters []records.Filter

	// Comparator carries the relation resolver, when the caller has one. The
	// zero value resolves nothing, so a relation comparison reports
	// CompareRelationUnresolved rather than silently comparing link text.
	Comparator records.Comparator
}

// Match is one record the comparator ACCEPTED, with its values already decoded.
//
// The values are handed over because the caller almost always needs them next —
// to render, to group, to total — and re-deriving them would mean a second
// decode path over the same rows. Order preserves the schema's declaration
// order so a report reads the way the operator wrote their schema.
type Match struct {
	Path       string
	RecordType string
	RecordID   string
	// SourceHash is FR-020c's freshness token as the properties index saw it.
	// The caller compares it against the text index's hash for the same
	// document; CompareFreshness says what the comparison means.
	SourceHash string

	// Values holds every DECLARED property of the record type, including the
	// absent ones — absence is a state (FR-007), and a map that omitted it
	// would make "absent" and "the caller forgot to ask" the same lookup.
	Values map[string]records.PropertyValue
	Order  []string
}

// Value returns one decoded property.
func (m Match) Value(name string) (records.PropertyValue, bool) {
	v, ok := m.Values[name]
	return v, ok
}

// Report is what the evaluation says about its own completeness.
//
// FR-025/FR-026: a record excluded because its value could not be compared is
// NAMED, with the reason. An answer that quietly dropped it would be the silent
// wrong answer ADR-068 exists to remove — and it is the direction nobody
// notices, because a shorter list looks like a correct list.
type Report struct {
	// Considered is how many records the comparator saw. It is the population
	// B1 bounded, after the store narrowed.
	Considered int
	// Matched is how many it accepted.
	Matched int
	// Problems names records whose values were faulty or whose index rows could
	// not be decoded — each carries the record path and the property.
	Problems []records.Finding
	// ComparisonProblems carries the oracle's rule-level verdicts: an
	// unresolved relation, an enum value the schema does not declare, an
	// operator no rule defines for that type.
	ComparisonProblems []records.ComparisonProblem
}

// Complete reports whether every candidate was evaluated without incident. A
// false here is what a caller turns into "this answer may be incomplete".
func (r Report) Complete() bool {
	return len(r.Problems) == 0 && len(r.ComparisonProblems) == 0
}

// Evaluate narrows through the store and decides in Go, calling visit once per
// ACCEPTED record.
//
// The order of what happens is the requirement, in code:
//
//  1. Refuse on a build with no properties index, BY NAME (FR-020h). A typed
//     filter that returned an empty success there would tell the operator there
//     is nothing to find when the truth is that the question cannot be asked.
//  2. Validate every filter against the schema, ONCE, before any record is
//     touched (FR-023). A query naming an unknown property is REJECTED with the
//     valid names — never answered with zero rows (FR-024).
//  3. Narrow. The store takes B1 before it retrieves anything (FR-064).
//  4. Decide, one assembled record at a time, in Go.
//
// visit may be nil for a caller that only wants the Report — a count, say —
// which is why the count comes back in the Report rather than only through the
// callback.
func Evaluate(
	ctx context.Context,
	store Store,
	q Query,
	visit func(Match) error,
) (Report, error) {
	var rep Report

	if store == nil {
		return rep, fmt.Errorf("propindex: Evaluate called with no store")
	}

	// (1) FR-020h. Asked with the capability the caller is actually exercising,
	// so the refusal says "typed filters are unavailable on linux/mipsle"
	// rather than something generic. On a SQLite-capable build this is a
	// compile-time-constant branch the compiler removes.
	capability := records.CapabilityOpenIndex
	if len(q.Filters) > 0 {
		capability = records.CapabilityTypedFilter
	}
	if err := records.RequirePropertyIndex(capability); err != nil {
		return rep, err
	}

	// (2) FR-023 / FR-024. Prepared ONCE: a filter re-validated per record
	// would pay the schema lookup and the literal parse over a population
	// bounded only by B1 (50,000), and — worse — would turn a rejected QUERY
	// into a per-record problem, which reads as an incomplete answer rather
	// than as the spelling mistake it usually is.
	prepared, sel, err := q.prepare()
	if err != nil {
		return rep, err
	}

	seen := make(map[string]struct{})

	// (3) + (4). Everything below this line is Go.
	err = store.Candidates(ctx, sel, func(c Candidate) (Verdict, error) {
		// The fan-out must already be gone. See the header note: half a `many`
		// property is a different operand from the whole one, and a record
		// visited twice is counted twice against B2.
		if _, dup := seen[c.Path]; dup {
			return Rejected, fmt.Errorf(
				"propindex: the comparator was handed %q twice; the candidate stream must assemble "+
					"a record's rows into ONE record before deciding anything about it, and a "+
					"partially assembled record answers a different question without saying so", c.Path)
		}
		seen[c.Path] = struct{}{}
		rep.Considered++

		m, problems, cproblems, matched := decide(c, prepared, q.Comparator)
		rep.Problems = append(rep.Problems, problems...)
		rep.ComparisonProblems = append(rep.ComparisonProblems, cproblems...)
		if !matched {
			return Rejected, nil
		}

		rep.Matched++
		if visit != nil {
			if verr := visit(m); verr != nil {
				return Rejected, verr
			}
		}
		return Accepted, nil
	})
	if err != nil {
		return rep, err
	}
	return rep, nil
}

// prepare validates the query itself, before a single row is read.
func (q Query) prepare() ([]records.PreparedFilter, Selector, error) {
	sel := q.Selector

	if len(q.Filters) == 0 {
		return nil, sel, nil
	}
	if q.Schema == nil {
		return nil, sel, &records.QueryError{
			Property: q.Filters[0].Property,
			Reason: "a filter names a property, and a property only exists inside a record type; " +
				"this query carries filters but declares no record type",
			Supported: records.OperatorNames(),
		}
	}

	// The narrowing MUST select the record type the filters are written
	// against. Without it, ordinary notes and records of other types stream
	// into the comparator, every declared property reads ABSENT for them, and
	// FR-008's inclusion rule then puts every one of them into the answer to a
	// negative filter. That is a wrong answer with no error channel — the
	// filter did exactly what it says and the population was never the one the
	// caller meant.
	switch sel.RecordType {
	case "":
		sel.RecordType = q.Schema.Type
	case q.Schema.Type:
		// already narrowed to the right type.
	default:
		return nil, sel, &records.QueryError{
			Reason: fmt.Sprintf(
				"this query narrows to record type %q but its filters are written against %q; "+
					"the two must be the same type", sel.RecordType, q.Schema.Type),
			ValidNames: []string{q.Schema.Type},
		}
	}

	prepared := make([]records.PreparedFilter, 0, len(q.Filters))
	for _, f := range q.Filters {
		pf, err := f.Prepare(q.Schema)
		if err != nil {
			return nil, sel, err
		}
		prepared = append(prepared, pf)
	}
	return prepared, sel, nil
}

// decide applies every filter to one assembled candidate, in Go.
//
// A record matches when EVERY filter matches. Evaluation does not stop at the
// first rejection, and that is deliberate: FR-026 requires the offending
// records to be NAMED, so a corrupt value in the third filter must still be
// reported when the second filter already excluded the record. Short-circuiting
// would make the problem list depend on filter order, which is a report that
// silently changes when a caller reorders a query.
func decide(
	c Candidate,
	filters []records.PreparedFilter,
	cmp records.Comparator,
) (Match, []records.Finding, []records.ComparisonProblem, bool) {
	m := Match{
		Path:       c.Path,
		RecordType: c.RecordType,
		RecordID:   c.RecordID,
		SourceHash: c.SourceHash,
		Values:     make(map[string]records.PropertyValue, len(c.Props)),
		Order:      append([]string(nil), c.PropOrder...),
	}

	var (
		problems  []records.Finding
		cproblems []records.ComparisonProblem
		matched   = true
	)

	// Decoding is memoised per property because two filters over the same
	// property must see the SAME operand. Decoding twice would be a second
	// decode path over one record — and StaleValueError makes it observable:
	// one filter could report the value undecodable while the other compared it.
	decoded := make(map[string]records.PropertyValue, len(filters))

	for _, pf := range filters {
		name := pf.Property.Name

		left, ok := decoded[name]
		if !ok {
			var perr []records.Finding
			left, perr = resolveStored(c, pf.Property)
			problems = append(problems, perr...)
			decoded[name] = left
			m.Values[name] = left
		}

		res := pf.MatchValue(cmp, left)
		problems = append(problems, res.Problems...)
		cproblems = append(cproblems, res.ComparisonProblems...)
		if !res.Matched {
			matched = false
		}
	}
	return m, problems, cproblems, matched
}

// resolveStored turns one candidate's stored rows for one declared property
// into the operand the comparator consumes.
//
// The two failure shapes are kept apart because they send a reader to different
// files:
//
//   - NO ROW AT ALL. Every declared property of an indexed record gets a state
//     row (rows.go's BuildNoteRows), so a missing one means the SCHEMA has
//     moved on since the note was indexed. The record is still evaluated, with
//     the property ABSENT — but it is REPORTED, because "the note says nothing"
//     and "this index does not know what the note says" are different facts and
//     FR-008 treats the first as an inclusion. Assuming the second is the first
//     would put records into a negative filter's answer on the strength of a
//     stale index.
//   - A ROW THAT WILL NOT DECODE (StaleValueError). The stored value is no
//     longer admitted by the current schema, or a typed column came back empty.
//     Reported, and the property is marked NON-CONFORMING so R-4 excludes the
//     record rather than letting a value nobody could read match anything.
func resolveStored(c Candidate, prop *records.Property) (records.PropertyValue, []records.Finding) {
	base := records.Finding{
		RecordPath:   c.Path,
		RecordType:   c.RecordType,
		RecordID:     c.RecordID,
		Property:     prop.Name,
		ElementIndex: -1,
		Severity:     records.SeverityError,
	}

	sp, ok := c.Prop(prop.Name)
	if !ok {
		f := base
		f.Code = records.FindingStaleIndex
		f.Reason = fmt.Sprintf(
			"the properties index holds no state for %q on this record; the schema has changed since "+
				"the note was indexed, so the property is treated as absent — re-index this note to be sure",
			prop.Name)
		return records.PropertyValue{Property: prop, State: records.StateAbsent}, []records.Finding{f}
	}

	pv, err := sp.Typed(prop)
	if err != nil {
		f := base
		f.Code = records.FindingStaleIndex
		f.Reason = err.Error()
		// NON-CONFORMING, not absent. Something IS written there; it is just
		// unreadable, and R-4 excludes the record and reports it rather than
		// letting absence sweep it into a negative filter's answer.
		return records.PropertyValue{Property: prop, State: records.StateNonConforming}, []records.Finding{f}
	}
	pv.Property = prop
	return pv, nil
}
