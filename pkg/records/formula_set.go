// Omnipus — ADR-068 D24.3 / spec FR-141, FR-146, FR-148: the view's formula set.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// A single formula is parsed and typed by formula_parse.go and formula_type.go.
// This file owns everything that is only true of a SET of them — a view's
// `formulas:` map (FR-141) — and there are exactly two such things, both of
// which were missing from the design one revision ago and both of which are
// unrecoverable at query time:
//
//  1. FR-146's CAPS. One formula ≤ 64 nodes / depth 8; a view ≤ 16 formulas;
//     a view's formulas ≤ 256 nodes in TOTAL. The third cap is the one that
//     needs the set: sixteen individually legal formulas can still be 1,024
//     nodes together.
//
//  2. FR-148's CYCLES. `a: formula.b + 1` / `b: formula.a + 1` parses clean,
//     types clean, and passes every per-formula bound — and then recurses
//     forever. An unspecified HANG is the worst severity a defect of this kind
//     has, because it has no error, no wrong answer and no timeout: the query
//     simply never returns.
//
// THE COST ARITHMETIC THE CAPS COME FROM, stated because the previous version
// of it was wrong by a factor of ~64 and nobody noticed until a review did.
// Formulas do NOT multiply against the candidate bound "exactly as filter
// leaves do". They multiply against the candidate bound AND the leaf count:
// 64 leaves × 64-node formulas × 50,000 candidates = 204.8M steps over exact
// rationals, against the 3.2M that FR-023c's numbers were chosen to define.
//
// MEMOIZATION is what makes the cost additive instead of multiplicative: each
// distinct formula is evaluated ONCE per candidate and reused across every
// leaf, sort key and aggregate referencing it. With the caps above the defined
// worst case is 50,000 × (64 + 256) = 16M steps — five times FR-023c's 3.2M,
// stated openly rather than hidden, and the caps come down if the W
// measurement says so.
//
// The caps and the memoization are ONE decision. Enforce the caps without
// memoizing and the bound is a fiction; memoize without the caps and sixteen
// hundred nodes are still sixteen hundred nodes per candidate.
// ---------------------------------------------------------------------------

// FR-146's three caps. They are FR-023c's numbers where FR-023c has one, and
// new where it does not; each refusal names WHICH cap it hit, because "too
// large" without the dimension leaves the author guessing between three.
const (
	// maxFormulaNodes is the per-formula node cap.
	maxFormulaNodes = 64
	// maxFormulaDepth is the per-formula depth cap.
	maxFormulaDepth = 8
	// maxFormulasPerView is how many formulas one view may define.
	maxFormulasPerView = 16
	// maxFormulaNodesPerView is the TOTAL node budget across a view's formulas.
	//
	// A referenced formula's nodes count ONCE (FR-148), which falls out of
	// summing each formula's own tree: a `formula.b` reference is one node in
	// a's tree, and b's own nodes are counted in b's entry. So a reference chain
	// cannot smuggle cost past the cap, which is precisely what an
	// expand-references-then-count implementation would have allowed.
	maxFormulaNodesPerView = 256
)

// FormulaSet is a view's validated formulas: name → declaration, with the
// reference graph checked and the caps applied.
//
// It is produced ONLY by ValidateFormulaSet, and that is the invariant the
// whole layer rests on (FR-140): if a FormulaSet exists, every formula in it
// parsed, typed, fitted the caps and sat in an acyclic graph. Evaluation
// therefore has no error path for any of those things, because it cannot be
// handed a set where they are open questions.
type FormulaSet struct {
	decls map[string]FormulaDecl
	names []string
	nodes int
}

// Len is the number of formulas.
func (s *FormulaSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.decls)
}

// Names returns the formula names in sorted order.
func (s *FormulaSet) Names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.names))
	copy(out, s.names)
	return out
}

// Get returns one declaration.
func (s *FormulaSet) Get(name string) (FormulaDecl, bool) {
	if s == nil {
		return FormulaDecl{}, false
	}
	d, ok := s.decls[name]
	return d, ok
}

// TotalNodes is the set's measured usage of FR-146's 256-node view budget.
func (s *FormulaSet) TotalNodes() int {
	if s == nil {
		return 0
	}
	return s.nodes
}

// LookupFormula implements FormulaEnv, so a validated set can type a later
// expression — the query path's `formula.<name>` resolution and the importer's
// incremental build both need exactly this.
func (s *FormulaSet) LookupFormula(name string) (FormulaDecl, bool) { return s.Get(name) }

// ValidateFormulaSet is THE write-path and load-path entry point (FR-140).
//
// `knowledge_configure` calls it before storing a view; the view loader calls it
// on every load so a hand-edited file is re-checked. It runs the passes in a
// fixed order and returns EVERY refusal it found rather than the first, because
// an author fixing four formulas one round-trip at a time is an author who
// stops using formulas.
//
// The order is deliberate and is itself a requirement:
//
//  1. the view-level count cap      — cheap, and independent of content
//  2. parse                         — FR-140
//  3. per-formula node/depth caps   — FR-146, before typing walks the tree
//  4. the total-node cap            — FR-146, needs every tree
//  5. cycles                        — FR-148, needs every reference list
//  6. types                         — FR-143a, needs an acyclic graph to
//     resolve `formula.<name>` against
//
// STEP 5 BEFORE STEP 6 IS LOAD-BEARING. Type inference resolves a
// `formula.<name>` reference by looking up the referenced formula's declaration
// — which, in a cyclic set, means inferring a type that depends on itself. Put
// the cycle check after typing and the type checker is the thing that hangs.
func ValidateFormulaSet(sources map[string]string, schema *Schema) (*FormulaSet, []*FormulaError) {
	var errs []*FormulaError

	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)

	// 1 — the count cap. Reported once, naming the cap and the count.
	if len(names) > maxFormulasPerView {
		errs = append(errs, &FormulaError{
			Offset: -1,
			Code:   FormulaErrTooLarge,
			Reason: fmt.Sprintf("the view defines %d formulas; FR-146 caps a view at %d", len(names), maxFormulasPerView),
			Expected: fmt.Sprintf("at most %d formulas per view — the cap exists because a view's formulas are evaluated once per candidate, and %d × %d nodes is the bound the whole query budget is derived from",
				maxFormulasPerView, maxFormulasPerView, maxFormulaNodes),
		})
	}

	// 2 and 3 — parse, then the per-formula caps.
	trees := map[string]FormulaDecl{}
	for _, name := range names {
		src := sources[name]
		if err := validFormulaName(name); err != nil {
			errs = append(errs, err)
			continue
		}
		root, perr := ParseFormula(src)
		if perr != nil {
			perr.Formula = name
			errs = append(errs, perr)
			continue
		}
		nodes := FormulaNodeCount(root)
		depth := FormulaDepth(root)
		if nodes > maxFormulaNodes {
			errs = append(errs, &FormulaError{
				Formula: name, Offset: -1, Code: FormulaErrTooLarge,
				Reason:   fmt.Sprintf("the expression has %d nodes; FR-146 caps one formula at %d", nodes, maxFormulaNodes),
				Expected: fmt.Sprintf("at most %d nodes", maxFormulaNodes),
			})
			continue
		}
		if depth > maxFormulaDepth {
			errs = append(errs, &FormulaError{
				Formula: name, Offset: -1, Code: FormulaErrTooLarge,
				Reason:   fmt.Sprintf("the expression nests %d levels deep; FR-146 caps one formula at %d", depth, maxFormulaDepth),
				Expected: fmt.Sprintf("at most %d levels of nesting", maxFormulaDepth),
			})
			continue
		}
		trees[name] = FormulaDecl{
			Name:   name,
			Source: src,
			Root:   root,
			Nodes:  nodes,
			Depth:  depth,
			Refs:   formulaRefs(root),
		}
	}

	// 4 — the view's total node budget.
	total := 0
	for _, name := range names {
		if d, ok := trees[name]; ok {
			total += d.Nodes
		}
	}
	if total > maxFormulaNodesPerView {
		errs = append(errs, &FormulaError{
			Offset: -1, Code: FormulaErrTooLarge,
			Reason: fmt.Sprintf("the view's formulas come to %d nodes in total; FR-146 caps a view at %d", total, maxFormulaNodesPerView),
			Expected: fmt.Sprintf("at most %d nodes across all of a view's formulas — the total is what sets the per-candidate cost, so sixteen individually legal formulas can still be refused together",
				maxFormulaNodesPerView),
		})
	}

	// 5 — cycles, before typing (see the ordering note above).
	if cycleErrs := detectFormulaCycles(names, trees); len(cycleErrs) > 0 {
		errs = append(errs, cycleErrs...)
		// A cyclic graph cannot be typed: inference would follow the same
		// edges. Report the cycles and stop, rather than hanging in step 6.
		return nil, errs
	}

	// 6 — types, in DEPENDENCY ORDER so a `formula.<name>` reference always
	// resolves against a declaration that is already inferred.
	env := SchemaFormulaEnv{Schema: schema, Formulas: map[string]FormulaDecl{}}
	for _, name := range topoOrderFormulas(names, trees) {
		d, ok := trees[name]
		if !ok {
			continue
		}
		typ, arity, scale, terr := InferFormulaType(d.Root, env)
		if terr != nil {
			terr.Formula = name
			errs = append(errs, terr)
			continue
		}
		d.Type, d.Arity, d.Scale = typ, arity, scale
		env.Formulas[name] = d
		trees[name] = d
	}

	if len(errs) > 0 {
		return nil, errs
	}

	set := &FormulaSet{decls: map[string]FormulaDecl{}, nodes: total}
	for _, name := range names {
		set.decls[name] = trees[name]
		set.names = append(set.names, name)
	}
	sort.Strings(set.names)
	return set, nil
}

// validFormulaName refuses a name a query could not address.
//
// `formula.<name>` is how a query reaches a formula (FR-140), so a name with a
// dot, a space or a leading digit is a formula nothing can ever refer to. Better
// refused at write time than stored and unreachable.
func validFormulaName(name string) *FormulaError {
	if name == "" {
		return &FormulaError{Offset: -1, Code: FormulaErrName,
			Reason: "a formula must have a name", Expected: "a name a query can write after `formula.`"}
	}
	toks, err := lexFormula(name)
	if err != nil || len(toks) != 2 || toks[0].kind != tokIdent || toks[0].text != name {
		return &FormulaError{Formula: name, Offset: -1, Code: FormulaErrName,
			Reason:   fmt.Sprintf("`%s` is not a usable formula name", name),
			Expected: "a single name — letters, digits and underscore, not starting with a digit — because a query addresses a formula as `formula.<name>`"}
	}
	return nil
}

// detectFormulaCycles is FR-148, and it is DELIBERATELY the same shape as the
// guard `pkg/records/frontmatter.go` already applies to YAML aliases.
//
// That file's `b.active[n.Alias]` check exists for exactly this reason: a
// self-referential structure hangs an evaluator, and the fix is an ACTIVE SET —
// the set of nodes on the current walk — consulted before descending and
// removed on the way back up. `seen` (nodes finished) and `active` (nodes on
// the stack) are two different sets and conflating them is the classic bug: it
// reports a cycle for a diamond, where two formulas legitimately reference a
// third.
//
// The refusal names the PATH, `a → b → a`, because a cycle among sixteen
// formulas is otherwise a puzzle the author has to solve by hand.
func detectFormulaCycles(names []string, trees map[string]FormulaDecl) []*FormulaError {
	var errs []*FormulaError
	done := map[string]bool{}
	active := map[string]bool{}
	var stack []string
	reported := map[string]bool{}

	var walk func(name string)
	walk = func(name string) {
		d, ok := trees[name]
		if !ok {
			// A reference to a formula the view does not define. That is a
			// typing fault, not a cycle — step 6 reports it by name.
			return
		}
		if active[name] {
			// The cycle is the tail of the current stack from `name` onward,
			// closed by `name` again.
			start := 0
			for i, s := range stack {
				if s == name {
					start = i
					break
				}
			}
			path := append(append([]string{}, stack[start:]...), name)
			key := strings.Join(canonicalCycle(path), ">")
			if !reported[key] {
				reported[key] = true
				errs = append(errs, &FormulaError{
					Formula: path[0],
					Offset:  -1,
					Code:    FormulaErrCycle,
					Reason: fmt.Sprintf("FR-148: the formulas reference each other in a cycle — %s. Evaluating any of them would recurse forever",
						formulaRefPathString(path)),
					Expected: "a formula graph with no cycles",
					Path:     path,
				})
			}
			return
		}
		if done[name] {
			return
		}
		active[name] = true
		stack = append(stack, name)
		for _, ref := range d.Refs {
			walk(ref)
		}
		stack = stack[:len(stack)-1]
		delete(active, name)
		done[name] = true
	}

	for _, name := range names {
		walk(name)
	}
	return errs
}

// canonicalCycle rotates a cycle path so the same cycle discovered from two
// different entry points de-duplicates to one refusal. Without it,
// `a → b → a` and `b → a → b` are reported as two separate cycles.
func canonicalCycle(path []string) []string {
	if len(path) < 2 {
		return path
	}
	ring := path[:len(path)-1]
	least := 0
	for i, s := range ring {
		if s < ring[least] {
			least = i
		}
	}
	out := make([]string, 0, len(ring))
	for i := range ring {
		out = append(out, ring[(least+i)%len(ring)])
	}
	return out
}

// topoOrderFormulas returns names with every formula after the ones it
// references, so type inference resolves each reference against an already
// inferred declaration.
//
// It is only ever called on a graph detectFormulaCycles has passed, so the
// recursion terminates. The `active` set is retained anyway, as a belt: a future
// caller that reorders the passes gets a truncated order rather than a hang, and
// step 6's unresolved-reference refusal then names the formula.
func topoOrderFormulas(names []string, trees map[string]FormulaDecl) []string {
	var out []string
	done := map[string]bool{}
	active := map[string]bool{}

	var visit func(string)
	visit = func(name string) {
		if done[name] || active[name] {
			return
		}
		d, ok := trees[name]
		if !ok {
			return
		}
		active[name] = true
		for _, ref := range d.Refs {
			visit(ref)
		}
		delete(active, name)
		done[name] = true
		out = append(out, name)
	}
	for _, name := range names {
		visit(name)
	}
	return out
}
