// Omnipus — translating an Obsidian Base `and:`/`or:`/`not:` filter TREE into
// the ViewDef VERSION-2 filter tree (ADR-068 D24.1, spec FR-018b): one
// `all`/`any`/`not` shape over the ten SQL operators, which is the same
// grammar `knowledge_find` already evaluates.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT CHANGED HERE, AND WHY IT IS THE WHOLE POINT
//
// This file used to flatten a Base filter tree into an AND-only list of leaves,
// because an AND-only list was all the view format could store. Every construct
// the flattening could not carry — an `or:` group, a multi-clause `not:`,
// `file.inFolder(...)` — became a named loss in a ROW-SET-AFFECTING position,
// and FR-105 then correctly disabled the view. Forty-eight of the founder's
// forty-nine imported views were disabled for exactly that reason.
//
// The view format stores a TREE. So this file builds one, and the three
// constructs above are translations rather than losses:
//
//	or:  [A, B]           ->  {any: [A, B]}
//	not: [A, B]           ->  {not: {all: [A, B]}}   (Obsidian ANDs, then negates)
//	file.inFolder("x")    ->  records.TranslateFileMethod — FR-134's normative
//	                          {any: [{file.folder,=,x}, {file.folder,LIKE,x/%}]}
//
// TWO PHASES, AND THE SPLIT IS NOT COSMETIC. Walking the Base tree happens
// HERE, with no schema in hand — the record type a view queries is DERIVED from
// the `type == "..."` literals this walk finds, so it cannot be an input to the
// walk. What this file produces is therefore an INTERMEDIATE tree (rawNode)
// whose leaves are not yet checked against any schema. view_write.go resolves
// it once the type is known, which is where a leaf can still fail and become a
// named loss.
//
// ONE DIVERGENCE IS ACCEPTED AND NAMED RATHER THAN HIDDEN. Obsidian's `==` is
// case-SENSITIVE; ours is case-INSENSITIVE over the folded value (spec ruling
// R-D / FR-011a) — universally, for every comparison the product makes. So a
// translated equality can match a record whose value differs only in case,
// which is strictly more rows. It is accepted because FR-104's translation
// table is normative and maps `==` to `=` without qualification, and because
// the alternative — treating the engine's own comparison rule as a broadening —
// would make every equality untranslatable and pin the clean count at zero
// forever. It is written down here so nobody later discovers it as a surprise.
// ---------------------------------------------------------------------------

// rawKind is what one node of the intermediate tree is.
type rawKind int

const (
	// rawKindLost is a subtree this importer could not translate at all. It
	// carries the verbatim source so the loss can name it (FR-101).
	rawKindLost rawKind = iota
	// rawKindLeaf is one comparison, still unresolved against a schema.
	rawKindLeaf
	// rawKindPrebuilt is a node pkg/records already built for us — the file
	// methods, whose meaning is FR-134's and lives in records.TranslateFileMethod.
	rawKindPrebuilt
	rawKindAll
	rawKindAny
	rawKindNot
)

// rawNode is one node of the intermediate tree: the Base filter's shape, with
// its leaves not yet checked against a record type's schema.
type rawNode struct {
	Kind     rawKind
	Leaf     v2Leaf
	Prebuilt *generated.VaultFilterNode
	Kids     []*rawNode
	// Verbatim is the content-preserving YAML re-serialisation of the source
	// (sub)tree, used when this node — or anything under it — has to be
	// reported as a loss instead of translated.
	Verbatim string
}

// leafShape is WHICH Obsidian filter idiom a leaf came from. The shape is kept
// rather than resolved immediately because the faithful translation of four of
// the six depends on the property's DECLARED TYPE, which this file does not
// know (see the two-phase note in the header).
type leafShape int

const (
	// shapeCompare is `prop OP literal` — the operator is already one of the
	// ten and needs no type to choose.
	shapeCompare leafShape = iota
	// shapeContains is Obsidian's `prop.contains("x")`. On a many property it
	// is element membership (`=`, R-9); on a text property it is substring
	// (`LIKE '%x%'`). Two different operators, decided by the declared type.
	shapeContains
	// shapeTruthy is a BARE `prop` — a JavaScript truthy test. See
	// truthyAdmitsAFalsyValue in view_write.go for why it is refused on the
	// types that admit a present-but-falsy value.
	shapeTruthy
	// shapeFalsy is `!prop`. `IS NULL` is a strict SUBSET of it (Obsidian's
	// falsy also catches `false`, `0` and `""`), so it is always safe.
	shapeFalsy
	// shapeIsSet is `prop != ""`. FR-007a rules the translation: for a
	// non-text property the empty string IS the absent state, so "is set"
	// becomes `IS NOT NULL`.
	shapeIsSet
	// shapeIsEmpty is `prop == ""`, which has no safe translation — see
	// buildV2LeafNode.
	shapeIsEmpty
)

// v2Leaf is one comparison this importer intends to emit, before its property
// has been checked against a schema.
type v2Leaf struct {
	Property string
	Shape    leafShape
	// Op is set for shapeCompare only.
	Op    generated.VaultFilterNodeOp
	Value string
	// Source is the original expression text, so a refusal quotes what the
	// operator actually wrote rather than a reconstruction of it.
	Source string
}

// TreeTranslation is what walking one filter (sub)tree produced.
//
// It carries NO loss list any more, deliberately. A loss is emitted exactly
// once, by the resolution pass in view_write.go, so that a subtree lost at
// parse time and a subtree lost at schema-resolution time are reported by one
// code path in one order — the previous split made double-counting a matter of
// remembering not to.
type TreeTranslation struct {
	// Root is the intermediate tree, or nil when the filter was absent or
	// contained nothing but `type ==` literals.
	Root *rawNode
	// TypeLiterals are every `type == "X"` equality this subtree asserted
	// UNCONDITIONALLY (i.e. found directly inside an `and:`, never inside an
	// `or:` or a `not:`, where "either type" is not the same fact as "this
	// type"). They are never emitted as filter leaves — they are ViewDef.Type
	// evidence instead (see resolveViewType in view_write.go).
	TypeLiterals []string
}

// TranslateFilterTree walks one Base `filters:` value (a leaf string, an
// `and:`/`or:`/`not:` node, or nil).
//
// A subtree it cannot translate becomes a rawKindLost node IN PLACE rather
// than being dropped, so the resolution pass reports it with the position it
// came from and FR-105 disables the view. Nothing is ever silently omitted.
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
		return TreeTranslation{Root: lostNode(renderVerbatim(node))}
	}
}

func lostNode(verbatim string) *rawNode {
	return &rawNode{Kind: rawKindLost, Verbatim: verbatim}
}

// reFileMethod matches the three file METHODS that translate to a filter.
// `file.asLink()` is deliberately absent: records.TranslateFileMethod refuses
// it by name, and routing it there gives the operator that named refusal
// instead of a generic "shape not recognised".
var reFileMethod = regexp.MustCompile(`^file\.(inFolder|hasTag|hasLink|asLink)\(\s*(.*?)\s*\)$`)

// reEmptyCompare matches Obsidian's `prop != ""` / `prop == ""` idiom. It runs
// before parseLeaf because parseLeaf refuses an empty literal outright — under
// version 1 there was no faithful translation, and under version 2 there is
// exactly one, for exactly one of the two operators (FR-007a).
var reEmptyCompare = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(==|!=)\s*(""|'')$`)

func translateLeafExpr(expr string) TreeTranslation {
	s := strings.TrimSpace(expr)
	if s == "" {
		return TreeTranslation{Root: lostNode(s)}
	}

	// The compound-expression guard runs before EVERY pattern, not just before
	// parseLeaf's. reFileMethod is `$`-anchored on a closing paren, so
	// `file.inFolder("a") && file.inFolder("b")` matches it with the argument
	// `a") && file.inFolder("b` — a folder name nothing is in, ANDed into the
	// view as if it were the operator's. See leaf.go's header for the whole
	// failure this closes.
	if containsUnquotedLogicalOperator(s) {
		return TreeTranslation{Root: lostNode(s)}
	}

	if m := reFileMethod.FindStringSubmatch(s); m != nil {
		return TreeTranslation{Root: fileMethodNode(m[1], m[2], s)}
	}

	if m := reEmptyCompare.FindStringSubmatch(s); m != nil {
		shape := shapeIsSet
		if m[2] == "==" {
			shape = shapeIsEmpty
		}
		return TreeTranslation{Root: &rawNode{
			Kind:     rawKindLeaf,
			Leaf:     v2Leaf{Property: m[1], Shape: shape, Source: s},
			Verbatim: s,
		}}
	}

	parsed := parseLeaf(s)
	switch parsed.Kind {
	case leafTypeLiteral:
		return TreeTranslation{TypeLiterals: []string{parsed.TypeLiteral}}
	case leafFilter:
		return TreeTranslation{Root: nodeFromRawLeaf(parsed.Filter, s)}
	default:
		return TreeTranslation{Root: lostNode(s)}
	}
}

// fileMethodNode hands one `file.<method>(arg)` call to pkg/records, which owns
// what it MEANS (FR-134). The argument is unquoted here because the quoting is
// `.base` syntax and the meaning is not: `inFolder("99-Temp")` and
// `inFolder('99-Temp')` name the same folder.
func fileMethodNode(method, rawArg, source string) *rawNode {
	arg, quoted, _ := unquoteLiteral(rawArg)
	if !quoted && strings.TrimSpace(rawArg) != "" {
		// A computed argument (`inFolder(someVar)`) is not a shape this
		// importer reads, and guessing would put a variable name in a filter.
		return lostNode(source)
	}
	node, err := records.TranslateFileMethod(records.FileMethod(method), arg)
	if err != nil {
		return lostNode(source)
	}
	built := node
	return &rawNode{Kind: rawKindPrebuilt, Prebuilt: &built, Verbatim: source}
}

// nodeFromRawLeaf maps leaf.go's version-1 vocabulary onto the version-2 one.
//
// leaf.go is the shared leaf PARSER and still speaks the seven retired
// operators; this function is the only place that translation happens, so the
// two vocabularies never mix in the same expression.
//
// `!=` becomes a TREE NEGATION rather than the `<>` leaf, and the difference is
// not stylistic: `{not: {p,=,v}}` INCLUDES records where `p` is absent and
// `{p,<>,v}` EXCLUDES them (VaultFilterNode's own contract, spec §8 R-2).
// Obsidian's `status != "done"` matches a note with no `status` at all, so the
// negation is the faithful one and `<>` would silently drop rows.
func nodeFromRawLeaf(rl RawLeaf, source string) *rawNode {
	leaf := v2Leaf{Property: rl.Property, Source: source}
	switch rl.Op {
	case "eq":
		leaf.Shape, leaf.Op = shapeCompare, generated.Equal
	case "lt":
		leaf.Shape, leaf.Op = shapeCompare, generated.LessThan
	case "lte":
		leaf.Shape, leaf.Op = shapeCompare, generated.LessThanEqual
	case "gt":
		leaf.Shape, leaf.Op = shapeCompare, generated.GreaterThan
	case "gte":
		leaf.Shape, leaf.Op = shapeCompare, generated.GreaterThanEqual
	case "contains":
		leaf.Shape = shapeContains
	case "is_absent":
		// RawLeaf.Negate is part of leaf.go's ENCODING here, not a tree
		// negation: a bare `prop` arrives as `is_absent` with Negate set,
		// meaning "has a value". Version 2 spells that `IS NOT NULL` directly,
		// so the flag is consumed by the shape and must NOT also become a
		// `not` wrapper — doing so would negate the test twice and turn "has a
		// value" back into "is absent".
		if rl.Truthy {
			leaf.Shape = shapeTruthy
		} else {
			leaf.Shape = shapeFalsy
		}
		return &rawNode{Kind: rawKindLeaf, Leaf: leaf, Verbatim: source}
	default:
		return lostNode(source)
	}
	if len(rl.Values) > 0 {
		leaf.Value = rl.Values[0]
	}
	node := &rawNode{Kind: rawKindLeaf, Leaf: leaf, Verbatim: source}
	if rl.Negate {
		return &rawNode{Kind: rawKindNot, Kids: []*rawNode{node}, Verbatim: source}
	}
	return node
}

func translateCombinator(m map[string]any) TreeTranslation {
	if len(m) != 1 {
		// A Base filter node is always exactly one of and/or/not; anything
		// else is a shape this importer does not recognise at all.
		return TreeTranslation{Root: lostNode(renderVerbatim(m))}
	}
	for key, val := range m {
		items := asList(val)
		switch key {
		case "and":
			return translateAnd(items)
		case "or":
			return translateOr(items, m)
		case "not":
			return translateNot(items, m)
		default:
			return TreeTranslation{Root: lostNode(renderVerbatim(m))}
		}
	}
	return TreeTranslation{} // unreachable (len(m) == 1 guaranteed above)
}

// translateAnd is the one context in which a `type == "X"` literal is an
// UNCONDITIONAL fact about the view, so it is the one context that harvests
// them.
func translateAnd(items []any) TreeTranslation {
	out := TreeTranslation{}
	var kids []*rawNode
	for _, it := range items {
		sub := TranslateFilterTree(it)
		out.TypeLiterals = append(out.TypeLiterals, sub.TypeLiterals...)
		if sub.Root != nil {
			kids = append(kids, sub.Root)
		}
	}
	out.Root = allNode(kids)
	return out
}

// allNode wraps children in `all`, collapsing the pointless cases: no children
// is no filter, and one child needs no combinator around it.
func allNode(kids []*rawNode) *rawNode {
	switch len(kids) {
	case 0:
		return nil
	case 1:
		return kids[0]
	default:
		return &rawNode{Kind: rawKindAll, Kids: kids}
	}
}

// translateOr builds `any`, and harvests a type literal in EXACTLY ONE case.
//
// TYPE LITERALS ARE NOT UNCONDITIONAL INSIDE A DISJUNCTION — read before
// changing this. `or: [type == "round", type == "company"]` asserts "round OR
// company", not "round AND company", and a ViewDef declares at most ONE
// `type:`. Version 2 makes `type` optional, which permits an untyped view — but
// an untyped view spans EVERY note in scope, which is strictly MORE rows than
// "one of these two types", and that is the broadening FR-105 forbids. There is
// no filter leaf to fall back on either: `type` is the record discriminator,
// not a declared property of any schema. So a mixed-type `or:` is lost whole,
// exactly as it was under version 1.
//
// THE ONE EXCEPTION IS DISTRIBUTIVITY, NOT A JUDGEMENT CALL. When EVERY branch
// asserts the SAME single type, the group has the shape
//
//	(A AND T) OR (B AND T)
//
// which is identically equal to `T AND (A OR B)` — the same rows, by the
// distributive law, with nothing weakened and nothing assumed. That is exactly
// the founder's Subscriptions.base, whose outer filter is two folder scopes
// each re-asserting `type == "subscription"`, and it disabled all four of that
// base's views. The type is harvested and the REMAINDERS become the
// disjunction.
//
// The conditions are deliberately strict, because "almost the same shape" is
// where a broadening would enter:
//
//   - every branch must name the same ONE type — a branch naming none does not
//     require it, so factoring one out would newly require it of that branch;
//   - every branch must have something LEFT after the type is removed. A branch
//     that was only `type == T` makes the disjunction a tautology under T; that
//     is still exactly equal, but it is a different simplification with a
//     different proof, so it is refused rather than folded in here.
func translateOr(items []any, original map[string]any) TreeTranslation {
	lost := TreeTranslation{Root: lostNode(renderVerbatim(original))}

	var kids []*rawNode
	var lits []string
	for _, it := range items {
		sub := TranslateFilterTree(it)
		if sub.Root == nil || containsLost(sub.Root) {
			return lost
		}
		branchLits := distinctSorted(sub.TypeLiterals)
		if len(branchLits) > 1 {
			return lost
		}
		lits = append(lits, branchLits...)
		kids = append(kids, sub.Root)
	}
	if len(kids) == 0 {
		return lost
	}

	var harvested []string
	switch {
	case len(lits) == 0:
		// No branch mentions the type at all — an ordinary disjunction.
	case len(lits) == len(kids) && len(distinctSorted(lits)) == 1:
		harvested = distinctSorted(lits)
	default:
		// Some branches assert a type and some do not, or they assert
		// different ones. Neither factors.
		return lost
	}

	if len(kids) == 1 {
		// A one-branch disjunction is that branch.
		return TreeTranslation{Root: kids[0], TypeLiterals: harvested}
	}
	return TreeTranslation{
		Root:         &rawNode{Kind: rawKindAny, Kids: kids, Verbatim: renderVerbatim(original)},
		TypeLiterals: harvested,
	}
}

// translateNot handles `not: [...]`. Obsidian ANDs the wrapped list and negates
// the whole thing, which is precisely `{not: {all: [...]}}` — so unlike version
// 1, a multi-clause `not:` needs no De Morgan expansion and loses nothing.
//
// A `type ==` literal inside a `not:` is still lost whole: "not this type" is a
// set difference over the discriminator, and a ViewDef has no way to say it.
func translateNot(items []any, original map[string]any) TreeTranslation {
	var kids []*rawNode
	for _, it := range items {
		sub := TranslateFilterTree(it)
		if len(sub.TypeLiterals) > 0 || sub.Root == nil || containsLost(sub.Root) {
			return TreeTranslation{Root: lostNode(renderVerbatim(original))}
		}
		kids = append(kids, sub.Root)
	}
	inner := allNode(kids)
	if inner == nil {
		return TreeTranslation{Root: lostNode(renderVerbatim(original))}
	}
	return TreeTranslation{Root: &rawNode{Kind: rawKindNot, Kids: []*rawNode{inner}, Verbatim: renderVerbatim(original)}}
}

// containsLost reports whether any node in a subtree failed to translate.
//
// It exists so that `or:` and `not:` can be lost AS A UNIT. Keeping the
// translatable half of a disjunction changes which side of the "either" the
// view requires; keeping the translatable half of a negation changes what is
// being excluded. Both are reported verbatim instead (SC-010, FR-101).
func containsLost(n *rawNode) bool {
	if n == nil {
		return false
	}
	if n.Kind == rawKindLost {
		return true
	}
	for _, k := range n.Kids {
		if containsLost(k) {
			return true
		}
	}
	return false
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
