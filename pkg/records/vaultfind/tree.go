// Omnipus — spec FR-022 / section 8 R-2: the filter tree, and where negation lives.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultfind

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// THE TREE IS OURS. THE LEAF IS NOT.
//
// A node is `all`, `any`, `not`, or a leaf. The combinators are evaluated here,
// in Go, over real booleans — which is the reason `{not: {p, "=", v}}` correctly
// re-includes records that never said, at ANY depth: SQL's `NOT` is three-valued
// and would drop every one of them.
//
// The LEAF is delegated whole to records.PreparedFilter. Not part of it — all of
// it: validation, the four-case ladder, absence, FR-008's re-inclusion, and the
// oracle's verdict. This package contains no comparison and no matching logic of
// its own, because a second matching layer is the same class of defect as a
// second comparator: the verified one sits off the query path while the
// unverified one decides the answer, and the truth table then guarantees the
// correctness of code nobody calls.
// ---------------------------------------------------------------------------

type nodeKind int

const (
	nodeAll nodeKind = iota
	nodeAny
	nodeNot
	nodeLeaf
)

type node struct {
	kind     nodeKind
	children []*node
	// leaf is the prepared predicate. Prepared ONCE, at parse time, and reused
	// across every candidate: FR-023 wants the filter validated before any
	// record is touched, and the candidate population is bounded only by B1 —
	// 50,000 — so re-validating per record would be 50,000 redundant schema
	// lookups in the hot path.
	leaf records.PreparedFilter
	// text is how this node renders in the QUERY: echo (FR-122).
	text string
}

// buildNode converts one wire node, refusing anything ambiguous.
func buildNode(n generated.VaultFilterNode, schema *records.Schema) (*node, *Refusal) {
	forms := 0
	if n.All != nil {
		forms++
	}
	if n.Any != nil {
		forms++
	}
	if n.Not != nil {
		forms++
	}
	isLeaf := n.Property != nil || n.Op != nil || n.Value != nil || n.Values != nil
	if isLeaf {
		forms++
	}

	switch {
	case forms == 0:
		return nil, refuse(problem(generated.UnsupportedParameter,
			"a filter node is empty: it names no property and no all/any/not",
			"write a leaf {property, op, value}, or a combinator {all: [...]}"), nil)
	case forms > 1:
		// Resolving this by precedence would be the silent-drop failure in a new
		// costume: the caller would receive an answer to a query they did not
		// write, with nothing saying which half was ignored.
		return nil, refuse(problem(generated.UnsupportedParameter,
			"a filter node sets more than one form at once; a node is EITHER all/any/not OR a leaf",
			"split it into a combinator whose children are the leaves"), nil)
	}

	switch {
	case n.All != nil:
		return buildCombinator(nodeAll, "AND", *n.All, schema)
	case n.Any != nil:
		return buildCombinator(nodeAny, "OR", *n.Any, schema)
	case n.Not != nil:
		child, r := buildNode(*n.Not, schema)
		if r != nil {
			return nil, r
		}
		return &node{kind: nodeNot, children: []*node{child}, text: "NOT (" + child.text + ")"}, nil
	}
	return buildLeaf(n, schema)
}

func buildCombinator(k nodeKind, joiner string, in []generated.VaultFilterNode, schema *records.Schema) (*node, *Refusal) {
	if len(in) == 0 {
		// An empty `all` is vacuously true and an empty `any` vacuously false,
		// and neither is what a caller who wrote `{all: []}` meant. Guessing
		// which produces a whole-vault result or an empty one with nothing
		// saying so.
		return nil, refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("an empty %s matches either everything or nothing, and it is not clear which was meant",
				strings.ToLower(joiner)),
			"give it at least one child, or drop the filter"), nil)
	}
	out := &node{kind: k}
	parts := make([]string, 0, len(in))
	for i := range in {
		child, r := buildNode(in[i], schema)
		if r != nil {
			return nil, r
		}
		out.children = append(out.children, child)
		parts = append(parts, child.text)
	}
	out.text = strings.Join(parts, " "+joiner+" ")
	if len(parts) > 1 {
		out.text = "(" + out.text + ")"
	}
	return out, nil
}

// buildLeaf validates one predicate through records.Filter.Prepare — which is
// where FR-024's unknown-property refusal, FR-022a's empty-LIKE refusal,
// FR-022c's unsupported-operator refusal, FR-022d's shape refusals and R-13's
// arity refusal all already live, each with the spec's own wording.
//
// This function does not restate any of them. It classifies the resulting
// records.QueryError into a wire code and passes the message through unchanged,
// because two places owning one refusal's wording is how the tool's copy goes
// stale the day the engine's improves.
func buildLeaf(n generated.VaultFilterNode, schema *records.Schema) (*node, *Refusal) {
	if n.Property == nil || *n.Property == "" {
		return nil, refuse(problem(generated.UnknownProperty,
			"a filter leaf names no property",
			"write {property, op, value}; call vault_describe to see the declared properties"), nil)
	}
	if n.Op == nil || *n.Op == "" {
		return nil, refuse(problem(generated.UnsupportedOperator,
			fmt.Sprintf("the filter on %q names no operator", *n.Property),
			"supported: "+strings.Join(records.OperatorNames(), ", ")), nil)
	}

	f := records.Filter{Property: *n.Property, Op: records.Operator(*n.Op)}
	if n.Value != nil {
		f.Literal = *n.Value
		f.LiteralGiven = true
	}
	if n.Values != nil {
		f.Literals = append([]string{}, *n.Values...)
		f.LiteralGiven = true
	}

	prepared, err := f.Prepare(schema)
	if err != nil {
		var qe *records.QueryError
		if errors.As(err, &qe) {
			return nil, refuse(fromQueryError(qe), err)
		}
		return nil, refuse(problem(generated.TypeMismatch,
			fmt.Sprintf("the filter on %q could not be prepared: %v", *n.Property, err),
			"correct the filter and re-run"), err)
	}
	return &node{kind: nodeLeaf, leaf: prepared, text: leafText(f)}, nil
}

// leafText renders a leaf the way SQL would write it, for the QUERY: echo.
func leafText(f records.Filter) string {
	switch f.Op {
	case records.OpIsNull, records.OpIsNotNull:
		return f.Property + " " + string(f.Op)
	case records.OpIn:
		quoted := make([]string, 0, len(f.Literals))
		for _, v := range f.Literals {
			quoted = append(quoted, "'"+v+"'")
		}
		return f.Property + " IN (" + strings.Join(quoted, ", ") + ")"
	}
	return f.Property + " " + string(f.Op) + " '" + f.Literal + "'"
}

// properties lists every property the tree names, deduplicated, in a stable
// order. It feeds `explain` and nothing else reads it.
func (n *node) properties() []string {
	seen := map[string]bool{}
	var out []string
	var walk func(*node)
	walk = func(x *node) {
		if x == nil {
			return
		}
		if x.kind == nodeLeaf {
			name := x.leaf.Property.Name
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
			return
		}
		for _, c := range x.children {
			walk(c)
		}
	}
	walk(n)
	sort.Strings(out)
	return out
}

// leaves returns every prepared leaf, for the explain plan.
func (n *node) leaves() []records.PreparedFilter {
	var out []records.PreparedFilter
	var walk func(*node)
	walk = func(x *node) {
		if x == nil {
			return
		}
		if x.kind == nodeLeaf {
			out = append(out, x.leaf)
			return
		}
		for _, c := range x.children {
			walk(c)
		}
	}
	walk(n)
	return out
}

// evalResult is one node's verdict over one candidate.
type evalResult struct {
	matched bool
	// problems are the record-level reasons a comparison could not be made.
	// They are collected even when the node's answer is decided, because
	// FR-026 requires the offending record to be NAMED whether or not it
	// happened to change the outcome.
	problems []generated.RecordProblem
	// blocked marks that at least one leaf could not be evaluated at all. A
	// blocked record is EXCLUDED and REPORTED — never swept into an answer by
	// double negation, which would be a silent wrong answer.
	blocked bool
}

// eval walks the tree over one decoded candidate.
//
// The combinators do NOT short-circuit, and that is deliberate. Short-circuiting
// an `all` at the first false leaf would stop collecting problems from the
// remaining leaves, so a record with three broken properties would report one —
// and the reader would fix it, re-run, and be told about the next one. The cost
// is evaluating leaves whose answer cannot change the verdict; the benefit is a
// problem list that is complete the first time.
func (n *node) eval(c records.Comparator, cand candidate) evalResult {
	switch n.kind {
	case nodeLeaf:
		return evalLeaf(c, cand, n.leaf)

	case nodeNot:
		inner := n.children[0].eval(c, cand)
		// A comparison that could not be MADE is not re-admitted by negation
		// (section 8, and filter.go's own header note). The record stays
		// excluded and stays reported.
		if inner.blocked {
			return evalResult{matched: false, problems: inner.problems, blocked: true}
		}
		return evalResult{matched: !inner.matched, problems: inner.problems}

	case nodeAll:
		out := evalResult{matched: true}
		for _, c2 := range n.children {
			r := c2.eval(c, cand)
			out.problems = append(out.problems, r.problems...)
			out.blocked = out.blocked || r.blocked
			if !r.matched {
				out.matched = false
			}
		}
		if out.blocked {
			out.matched = false
		}
		return out

	default: // nodeAny
		out := evalResult{}
		for _, c2 := range n.children {
			r := c2.eval(c, cand)
			out.problems = append(out.problems, r.problems...)
			out.blocked = out.blocked || r.blocked
			if r.matched {
				out.matched = true
			}
		}
		// An `any` whose OTHER branch genuinely matched is a real match, and a
		// blocked sibling must not erase it — the record demonstrably satisfies
		// the query. The blockage is still reported, so the reader learns the
		// value is broken without losing a correct row.
		if out.blocked && !out.matched {
			out.matched = false
		}
		return out
	}
}

// evalLeaf decodes the candidate's stored property and hands it to the ONE
// matching layer.
//
// The decode and the match share a single declaration — pf.Property — which is
// what stops a stale or hand-built *records.Property silently changing which
// type disposition and arity rule the comparator applies.
func evalLeaf(c records.Comparator, cand candidate, pf records.PreparedFilter) evalResult {
	prop := pf.Property
	left, err := cand.value(prop)
	if err != nil {
		// A stored value the current schema no longer admits. Reported, and the
		// record excluded — not skipped silently, which would make the index
		// quietly hold less than the vault does.
		p := problem(generated.StaleRecord, err.Error(),
			"re-index this note, or correct the value to one the schema declares", cand.identity())
		p.Property = str(prop.Name)
		p.Paths = &[]string{cand.rows.Path}
		return evalResult{matched: false, problems: []generated.RecordProblem{p}, blocked: true}
	}

	res := pf.MatchValue(c, left)

	var problems []generated.RecordProblem
	for _, f := range res.Problems {
		problems = append(problems, findingProblem(cand.rows.RecordID, cand.rows.Path, f))
	}
	for _, cp := range res.ComparisonProblems {
		problems = append(problems, comparisonProblem(cand.rows.RecordID, cand.rows.Path, cp))
	}
	return evalResult{
		matched:  res.Matched,
		problems: problems,
		blocked:  len(problems) > 0,
	}
}
