// Omnipus — ADR-068 D16.2 / spec FR-021b: one narrowed record, decoded once.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// candidate is one record the narrowing predicates selected, with a memo of the
// properties already decoded.
//
// The memo exists because a filter tree can name one property in several leaves
// — `{any: [{arr, ">=", 50000}, {arr, "IS NULL"}]}` decodes `arr` twice without
// it — and decoding runs pkg/records' own parser over the stored elements. It is
// a per-candidate map and dies with the candidate, so it holds one record's
// properties at a time and never accumulates: FR-066b's "one record in memory at
// a time" is a property of the STREAM, and a memo that outlived the record would
// quietly break it.
//
// IT IS A POINTER TYPE, and that is load-bearing rather than stylistic: the
// formula evaluator is handed the candidate as a records.FormulaCandidate and
// calls BACK into it for every operand, so the value the evaluator holds and the
// value the filter tree reads have to be the same object. Two copies would mean
// two memos, and the second would silently re-decode everything.
type candidate struct {
	rows propindex.Candidate
	memo map[string]records.PropertyValue

	// schema is the record type's own declaration, for the formula layer's
	// FormulaProperty lookups. NIL IS LEGAL and is the untyped multi-type view:
	// every declared-property operand then resolves to false, which the formula
	// layer reads as absent — the honest answer for a view that declares no
	// type and therefore has no property to type.
	schema *records.Schema

	// file is FR-130's twelve virtual properties for this note, already
	// assembled from the parent row and the child-table prepasses. It is a
	// value, not a handle: there is nothing here that could reach a store, and
	// that absence is what makes ruling R-A structural on this path.
	file records.FileMeta

	// formulas is the query's evaluator, already Begun on THIS candidate, or
	// nil when the query declares none.
	formulas *records.FormulaEvaluator

	// formulaProblems are R-14/R-15's named problems — a division by zero, a
	// `%` over a fractional operand, an operand that did not conform. They are
	// collected here rather than returned from value() because value()'s
	// contract is "a value or a decode error", and a formula problem is
	// neither: the formula produced a legitimate ABSENT result and the reason
	// has to reach the response.
	formulaProblems []records.ComparisonProblem
}

func newCandidate(
	c propindex.Candidate,
	schema *records.Schema,
	file records.FileMeta,
	formulas *records.FormulaEvaluator,
) *candidate {
	cand := &candidate{
		rows:     c,
		memo:     make(map[string]records.PropertyValue, 4),
		schema:   schema,
		file:     file,
		formulas: formulas,
	}
	if formulas != nil {
		// Begin CLEARS the evaluator's memo. Forgetting it is the one way to
		// get a wrong answer out of the formula layer: every candidate would
		// receive the first candidate's formula values, silently.
		formulas.Begin(cand)
	}
	return cand
}

// identity is what a problem line names: the record identifier if the note
// carries one, and the path otherwise.
//
// An ordinary note with no identifier is the majority of every real vault
// (FR-005) and is not an error, so falling back to the path is the normal case
// rather than a degraded one.
func (c *candidate) identity() string {
	if c.rows.RecordID != "" {
		return c.rows.RecordID
	}
	return c.rows.Path
}

// value decodes one property into the form the comparator consumes.
//
// It routes on the property's NAMESPACE, and the three routes converge on one
// return type on purpose: everything downstream — the comparator, the sorter,
// the grouper, the fifteen summaries — sees a records.PropertyValue and cannot
// tell which route produced it. That is FR-142's "goes through the ONE
// comparator like any property value" made structural.
//
// An ABSENT property returns StateAbsent rather than an error, and the
// distinction is the whole of FR-007: absence is a legitimate third state, not a
// failure. It is what makes `{not: {p, "=", v}}` able to include the days nobody
// recorded a value for — precisely the days being asked about.
func (c *candidate) value(prop *records.Property) (records.PropertyValue, error) {
	if v, ok := c.memo[prop.Name]; ok {
		return v, nil
	}
	switch {
	case records.IsFileNamespace(prop.Name):
		return c.fileValue(prop)
	case isFormulaNamespace(prop.Name):
		return c.formulaValue(prop)
	}
	return c.storedValue(prop)
}

// storedValue is the original path: a property the record type declares, read
// from the candidate's own stored rows.
func (c *candidate) storedValue(prop *records.Property) (records.PropertyValue, error) {
	sp, ok := c.rows.Prop(prop.Name)
	if !ok {
		v := records.PropertyValue{Property: prop, State: records.StateAbsent}
		c.memo[prop.Name] = v
		return v, nil
	}
	// A RAW FRONTMATTER ROW carries no declared type, so it has no typed column
	// to decode from and StoredProp.Typed would refuse it — "a date element was
	// indexed with no value in its typed column" — for every note whose own type
	// does not declare this name. That refusal is correct for a TYPED row and
	// wrong here: FR-021e stores these rows precisely so an untyped query can
	// read them, and the contract says the value is "parsed in the domain the
	// name resolves to", with a value that does not parse there reported as
	// NON-CONFORMING rather than as a broken index.
	if sp.Type == propindex.RawPropertyType && prop.Type != propindex.RawPropertyType {
		return c.rawValue(prop, sp)
	}
	v, err := sp.Typed(prop)
	if err != nil {
		return records.PropertyValue{}, err
	}
	v.Property = prop
	c.memo[prop.Name] = v
	return v, nil
}

// rawValue parses one note's RAW frontmatter rows under the declaration the
// untyped namespace settled on.
//
// It goes through records.ParseValue — the same parser the validator and the
// index writer use — so an untyped query and a typed one read one value the
// same way. Nothing here re-implements a type: the only thing this function
// decides is that the SOURCE TEXT is what gets parsed, because a raw row is all
// source text by construction.
//
// A value that does not parse in the resolved domain is NON-CONFORMING, and its
// CONFORMING SIBLINGS are kept — the same rule records.ResolveProperty applies
// to a declared `many` property, and for the same reason: dropping the whole
// property would delete values the note demonstrably contains.
func (c *candidate) rawValue(prop *records.Property, sp propindex.StoredProp) (records.PropertyValue, error) {
	pv := records.PropertyValue{Property: prop, State: sp.State}
	if sp.State != records.StatePresent {
		c.memo[prop.Name] = pv
		return pv, nil
	}
	for _, e := range sp.Elems {
		n := records.Node{Kind: records.KindScalar, Text: e.Raw, Quoted: e.Quoted}
		// FR-007a: outside `text`, a blank scalar is ABSENT, not a value. The
		// predicate is records' own, so this cannot drift from the typed path.
		if records.AbsentByEmptyString(prop, n) {
			continue
		}
		v, verr := records.ParseValue(prop, n)
		if verr != nil {
			pv.State = records.StateNonConforming
			pv.Findings = append(pv.Findings, records.Finding{
				RecordPath: c.rows.Path,
				RecordType: c.rows.RecordType,
				Property:   prop.Name,
				Reason:     verr.Reason,
				Expected:   verr.Expected,
				Got:        verr.Got,
				Permitted:  verr.Permitted,
				Code:       verr.Code,
			})
			continue
		}
		pv.Values = append(pv.Values, v)
		pv.SourceIndex = append(pv.SourceIndex, e.SourcePos)
	}
	if len(pv.Values) == 0 && len(pv.Findings) == 0 {
		// Every element was blank-and-absent, so the property holds no value.
		pv.State = records.StateAbsent
	}
	c.memo[prop.Name] = pv
	return pv, nil
}

// fileValue resolves one of FR-130's virtual properties.
func (c *candidate) fileValue(prop *records.Property) (records.PropertyValue, error) {
	v, err := records.ResolveFileProperty(prop.Name, c.file)
	if err != nil {
		return records.PropertyValue{}, err
	}
	v.Property = prop
	c.memo[prop.Name] = v
	return v, nil
}

// formulaValue evaluates one of the view's formulas over this candidate.
//
// FR-146's memoization is the EVALUATOR's, not this memo's: each distinct
// formula is evaluated once per candidate and reused across every leaf, sort
// key and aggregate that names it, so per-candidate cost is additive in the
// formula's nodes rather than multiplicative in the leaf count. The memo here
// saves the PropertyValue wrapper, not the evaluation.
func (c *candidate) formulaValue(prop *records.Property) (records.PropertyValue, error) {
	name := strings.TrimPrefix(prop.Name, FormulaNamespace)
	if c.formulas == nil {
		// A query reached a formula with no evaluator wired. It is ABSENT
		// rather than an error because the namespace resolution that produced
		// this *Property already proved the formula exists — but it is
		// reported, because an unexplained absence is the failure this whole
		// surface is written against.
		c.formulaProblems = append(c.formulaProblems, records.ComparisonProblem{
			Code:     records.CompareNonConforming,
			Property: prop.Name,
			Detail:   prop.Name + " could not be evaluated: no formula evaluator was wired into this query",
		})
		v := records.PropertyValue{Property: prop, State: records.StateAbsent}
		c.memo[prop.Name] = v
		return v, nil
	}

	res, ok := c.formulas.Evaluate(name)
	if !ok {
		c.formulaProblems = append(c.formulaProblems, records.ComparisonProblem{
			Code:     records.CompareNonConforming,
			Property: prop.Name,
			Detail:   prop.Name + " is not defined by this view",
		})
		v := records.PropertyValue{Property: prop, State: records.StateAbsent}
		c.memo[prop.Name] = v
		return v, nil
	}
	c.formulaProblems = append(c.formulaProblems, res.Problems...)

	pv, ok := res.PropertyValue(prop.Formula)
	if !ok {
		// R-16/FR-147: a PRESENTATION result has no PropertyValue at all. The
		// namespace refuses this before a query runs, so reaching it means a
		// formula's declared type and its result disagree — reported, never
		// answered.
		c.formulaProblems = append(c.formulaProblems, records.ComparisonProblem{
			Code:     records.CompareNonConforming,
			Property: prop.Name,
			Detail:   prop.Name + " produced a presentation value, which does not compare (FR-147)",
		})
		v := records.PropertyValue{Property: prop, State: records.StateAbsent}
		c.memo[prop.Name] = v
		return v, nil
	}
	// The DECLARATION wins over the one PropertyValue synthesised: it is the
	// same static type by construction (FR-143a), and using one pointer keeps
	// the comparator, the arity rule and the memo reading the same object.
	pv.Property = prop
	c.memo[prop.Name] = pv
	return pv, nil
}

// evidence returns what the note actually held for a non-conforming property and
// the shape that would have been accepted.
//
// Both are empty when the property is conforming or absent, and when the note
// was indexed before the index carried this evidence — so a caller must render
// the fallback rather than an empty pair of quotes.
func (c *candidate) evidence(name string) (got, expected string) {
	sp, ok := c.rows.Prop(name)
	if !ok {
		return "", ""
	}
	return sp.Got, sp.Expected
}

// ---------------------------------------------------------------------------
// records.FormulaCandidate — the two questions the formula layer asks
// ---------------------------------------------------------------------------
//
// The interface is narrow on purpose: the formula layer must not know how a
// candidate was assembled or which statement streamed its tags. Both methods
// route through value(), so a formula operand and a filter leaf naming the same
// property get the SAME decode, the same memo and the same three-state answer.

// FormulaProperty resolves a declared property of the record under evaluation.
func (c *candidate) FormulaProperty(name string) (records.PropertyValue, bool) {
	prop, ok := c.declaredProperty(name)
	if !ok {
		return records.PropertyValue{}, false
	}
	v, err := c.value(prop)
	if err != nil {
		return records.PropertyValue{}, false
	}
	return v, true
}

// FormulaFileProperty resolves one of FR-130's `file.*` virtual properties.
//
// `file.file` is answered here and NOT through records.FileProperty, which
// refuses it: FR-130 excludes it from every FILTER position, and a formula
// operand is the one position it belongs in.
func (c *candidate) FormulaFileProperty(name string) (records.PropertyValue, bool) {
	v, err := records.ResolveFileProperty(name, c.file)
	if err != nil {
		return records.PropertyValue{}, false
	}
	return v, true
}

// declaredProperty is the formula layer's view of the schema. It is set by the
// evaluation that owns the candidate; a candidate with no schema attached
// answers false, which the formula layer reads as absent.
func (c *candidate) declaredProperty(name string) (*records.Property, bool) {
	if c.schema == nil {
		return nil, false
	}
	return c.schema.Property(name)
}
