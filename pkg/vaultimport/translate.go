// Omnipus — translating an Obsidian Base `and:`/`or:`/`not:` filter TREE
// into the flat, AND-only leaf list ADR-068's ViewDef actually stores.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// TreeTranslation is what walking one filter (sub)tree produced.
type TreeTranslation struct {
	// Leaves are the clauses this package CAN carry into a RecordFilter,
	// combined with AND (ViewDef's own combinator — see view.go's header:
	// "Filter clauses, combined with AND").
	Leaves []RawLeaf
	// TypeLiterals are every `type == "X"` equality this subtree asserted
	// UNCONDITIONALLY (i.e. found directly inside an `and:`, never inside an
	// `or:`, where "either type" is not the same fact as "this type").
	// These are never emitted as filter leaves — they are ViewDef.Type
	// evidence instead (see ResolveViewType in view_write.go).
	TypeLiterals []string
	// Lost is every expression or subtree this package could not translate,
	// preserved verbatim (content-preserving YAML re-serialisation of the
	// exact untranslated node — FR-101).
	Lost []string
}

func (t *TreeTranslation) merge(o TreeTranslation) {
	t.Leaves = append(t.Leaves, o.Leaves...)
	t.TypeLiterals = append(t.TypeLiterals, o.TypeLiterals...)
	t.Lost = append(t.Lost, o.Lost...)
}

// TranslateFilterTree walks one Base `filters:` value (a leaf string, an
// `and:`/`or:`/`not:` node, or nil) and returns everything it could and
// could not carry over.
//
// AN OR GROUP IS ALWAYS "LOST" AS ONE UNIT, never partially translated: our
// filter is a flat AND list with no disjunction at all (ViewDef carries no
// boolean-tree shape whatsoever), so there is no partial credit to give —
// dropping only SOME of an OR's branches would silently change which side
// of the "either" the view now requires. It is rendered verbatim rather than
// walked further (SC-010, FR-101), and its `type ==` literals are
// deliberately NOT harvested for view-type resolution — see the header note
// on typeLiteralsAreUnconditional below.
func TranslateFilterTree(node any) TreeTranslation {
	if node == nil {
		return TreeTranslation{}
	}
	switch v := node.(type) {
	case string:
		return translateLeafExpr(v)
	case map[string]any:
		return translateCombinator(v)
	default:
		return TreeTranslation{Lost: []string{renderVerbatim(node)}}
	}
}

func translateLeafExpr(expr string) TreeTranslation {
	parsed := parseLeaf(expr)
	switch parsed.Kind {
	case leafTypeLiteral:
		return TreeTranslation{TypeLiterals: []string{parsed.TypeLiteral}}
	case leafFilter:
		return TreeTranslation{Leaves: []RawLeaf{parsed.Filter}}
	default:
		return TreeTranslation{Lost: []string{strings.TrimSpace(expr)}}
	}
}

func translateCombinator(m map[string]any) TreeTranslation {
	if len(m) != 1 {
		// A Base filter node is always exactly one of and/or/not; anything
		// else is a shape this importer does not recognise at all.
		return TreeTranslation{Lost: []string{renderVerbatim(m)}}
	}
	for key, val := range m {
		items := asList(val)
		switch key {
		case "and":
			out := TreeTranslation{}
			for _, it := range items {
				out.merge(TranslateFilterTree(it))
			}
			return out
		case "or":
			// TYPE LITERALS ARE NOT UNCONDITIONAL HERE — read before
			// changing this. `or: [type == "round", and: [type ==
			// "company", ...]]` asserts "round OR company", not "round
			// AND company"; harvesting both as if they narrowed the SAME
			// view to one type would be wrong on its face (a view cannot
			// have two required types). Every real Base in this vault's
			// fixture that mixes types under `or:` already re-asserts a
			// single `type ==` directly inside the VIEW's own `and:`
			// filter (a sibling, unconditional context), which is where
			// ResolveViewType finds it instead. So the whole `or:` group
			// is rendered verbatim and reported lost, full stop — never
			// partially mined for a type.
			return TreeTranslation{Lost: []string{renderVerbatim(m)}}
		case "not":
			return translateNot(items, m)
		default:
			return TreeTranslation{Lost: []string{renderVerbatim(m)}}
		}
	}
	return TreeTranslation{} // unreachable (len(m) == 1 guaranteed above)
}

// translateNot handles `not: [...]`. Obsidian ANDs the wrapped list then
// negates the whole thing (De Morgan's `NOT (A AND B) = (NOT A) OR (NOT B)`
// — a disjunction), which our flat AND-of-leaves model can only represent
// when the wrapped list collapses to EXACTLY one clean leaf: negating a
// single leaf is just that leaf's own `negate` flag, no OR required. Two or
// more wrapped clauses, or anything already lost inside the wrap, makes the
// whole `not:` a lost unit instead of a guess.
func translateNot(items []any, original map[string]any) TreeTranslation {
	inner := TreeTranslation{}
	for _, it := range items {
		inner.merge(TranslateFilterTree(it))
	}
	if len(inner.Lost) == 0 && len(inner.TypeLiterals) == 0 && len(inner.Leaves) == 1 {
		leaf := inner.Leaves[0]
		leaf.Negate = !leaf.Negate
		return TreeTranslation{Leaves: []RawLeaf{leaf}}
	}
	return TreeTranslation{Lost: []string{renderVerbatim(original)}}
}

func asList(v any) []any {
	if v == nil {
		return nil
	}
	if l, ok := v.([]any); ok {
		return l
	}
	// A single-item `not:`/`and:`/`or:` value that was not written as a
	// list at all (Base's grammar does not allow this, but a hand-edited
	// file might) — treat the scalar as a one-item list rather than
	// dropping it.
	return []any{v}
}

// renderVerbatim re-serialises an untranslated (sub)tree as YAML text — the
// FR-101 "preserved verbatim" payload. It is content-preserving (every key,
// value and nesting level from the original survives), not a byte-identical
// copy of the source file's own formatting — see doc.go.
func renderVerbatim(node any) string {
	out, err := yaml.Marshal(node)
	if err != nil {
		return "<unrenderable expression>"
	}
	return strings.TrimSpace(string(out))
}
