// Omnipus — translating an Obsidian Base `and:`/`or:`/`not:` filter TREE into
// the ViewDef VERSION-2 filter tree (ADR-068 D24.1, spec FR-018b): one
// `all`/`any`/`not` shape over the ten SQL operators, which is the same
// grammar `knowledge_find` already evaluates.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"regexp"
	"sort"
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
	// rawKindTypedAny is a disjunction EVERY branch of which asserts exactly
	// one `type == "X"` — the shape that used to be lost whole as a
	// "mixed-type disjunction".
	//
	// It is DEFERRED, not translated: what it means depends on the record type
	// the VIEW resolves to, and translateOr does not know that yet (the base's
	// outer filter is translated once, before any view is looked at). It is
	// settled by ReduceTypedDisjunctions, and it never survives to the
	// resolution pass in either direction — it becomes a real node or a
	// rawKindLost carrying the same verbatim it would have carried before.
	rawKindTypedAny
)

// typedBranch is one branch of a rawKindTypedAny: the single record type it
// asserts, and whatever else it said.
type typedBranch struct {
	// RecordType is the branch's one `type == "X"` literal. Never empty — a
	// disjunction with an untyped branch is not a rawKindTypedAny.
	RecordType string
	// Remainder is the branch MINUS its type literal, and NIL is meaningful
	// rather than missing: the branch was nothing but the type literal, so
	// under that type it is simply TRUE.
	Remainder *rawNode
}

// rawNode is one node of the intermediate tree: the Base filter's shape, with
// its leaves not yet checked against a record type's schema.
type rawNode struct {
	Kind     rawKind
	Leaf     v2Leaf
	Prebuilt *generated.VaultFilterNode
	Kids     []*rawNode
	// Branches is set for rawKindTypedAny ONLY, and Kids is empty there. They
	// are separate fields on purpose: every existing walker over this tree
	// recurses through Kids, and a deferred disjunction must not be walked as
	// though its branches were already unconditional.
	Branches []typedBranch
	// Verbatim is the content-preserving YAML re-serialisation of the source
	// (sub)tree, used when this node — or anything under it — has to be
	// reported as a loss instead of translated.
	Verbatim string
	// Reason is why a rawKindLost subtree could not be translated, in the
	// words the operator reads. It is EMPTY for a loss whose diagnosis is
	// only knowable later, against a schema — those are explained by the
	// resolution pass instead (view_write.go), never twice.
	Reason string
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
	// falsy also catches `false`, `0` and `""`), so it is safe wherever a
	// SUBSET is safe — which is not everywhere. See v2Leaf.Negated.
	shapeFalsy
	// shapeIsSet is `prop != ""`. FR-007a rules the translation TWICE, once
	// per side of its own rule: for a NON-TEXT property the empty string IS
	// the absent state, so "is set" becomes `IS NOT NULL`; for a TEXT
	// property `""` stays a PRESENT value, so `IS NOT NULL` over-matches and
	// the operator that does not is `<>` against the empty literal.
	shapeIsSet
	// shapeIsEmpty is `prop == ""`. On TEXT it is `= ""` — "present and
	// empty". On every other type there is no empty literal to compare
	// against at all (FR-007a), and `IS NULL` would over-match; see
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
	// Negated records that this leaf sits under an ODD number of `not:`
	// wrappers, and it exists because a proof about a leaf is not a proof
	// about the view.
	//
	// Several of the translations below are APPROXIMATE in one direction:
	// they return a SUBSET of what the Obsidian expression selects, which
	// FR-105 permits — fewer rows, never more. A `not:` inverts that. The
	// subset becomes a SUPERSET, and knowledge_find's `not` has no absence
	// rule of its own to soften it (tree.go evaluates a bare
	// `!inner.matched`; FR-008's absent-rescue lives on the negative
	// OPERATOR inside records.PreparedFilter and never reaches a
	// COMBINATOR). So an approximate leaf that is safe at the top level is a
	// BROADENING under one `not:`, and buildV2LeafNode refuses it there by
	// name instead.
	//
	// It is set by view_write.go's resolveTree, which is the only place that
	// knows the path from the root, and it is FALSE by default so a leaf
	// built without thinking about polarity is treated as the exact one it
	// has to be.
	Negated bool
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

// lostNodeWithReason is lostNode plus the diagnosis, for the losses this file
// can already explain at parse time. Keeping the two constructors apart is what
// stops an empty reason being passed by accident at the dozen call sites that
// genuinely have none.
func lostNodeWithReason(verbatim, reason string) *rawNode {
	return &rawNode{Kind: rawKindLost, Verbatim: verbatim, Reason: reason}
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
		return TreeTranslation{Root: lostNodeWithReason(s, parsed.Reason)}
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
// not a declared property of any schema. So a mixed-type `or:` cannot be
// translated HERE, and under version 1 that was the end of it.
//
// IT IS NO LONGER THE END OF IT, AND THE REASON IS THAT THIS FUNCTION WAS
// ANSWERING THE WRONG QUESTION. A base's outer filter is translated ONCE, before
// any view is read, so the only question available here was "what does this
// disjunction mean on its own" — and on its own it means "either type", which is
// indeed unrepresentable. But no view ever applies it on its own. Every view
// applies `outer AND view` (view_write.go's conjoin), and in this vault every
// view re-asserts ONE of the disjuncts in its own `and:`:
//
//	Content.base:  (inFolder AND (type=="content" OR type=="brand-kit")) AND type=="content"
//
// which is `inFolder AND type=="content"` by absorption — (X ∨ Y) ∧ X ≡ X. So
// the mixed branch is deferred rather than lost: it becomes a rawKindTypedAny,
// and ReduceTypedDisjunctions settles it per view, once the view's record type
// is known. See that function for the exactness proof.
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
	verbatim := renderVerbatim(original)
	lost := TreeTranslation{Root: lostNode(verbatim)}

	// ONE pass, keeping BOTH readings of every branch — the remainder tree and
	// the single type literal it asserts, if any — because the three outcomes
	// below need different halves of the same walk.
	type branch struct {
		lit       string // "" when the branch names no record type
		remainder *rawNode
	}
	var bs []branch
	for _, it := range items {
		sub := TranslateFilterTree(it)
		if containsLost(sub.Root) {
			return lost
		}
		branchLits := distinctSorted(sub.TypeLiterals)
		if len(branchLits) > 1 {
			// One branch pinning two types is false, not narrow. Refused
			// rather than folded: an always-false disjunct only THINS a
			// disjunction, but proving that is a different proof.
			return lost
		}
		b := branch{remainder: sub.Root}
		if len(branchLits) == 1 {
			b.lit = branchLits[0]
		}
		if b.lit == "" && b.remainder == nil {
			// A branch that asserted nothing at all — an empty `or:` member.
			// Not a shape with a meaning to preserve.
			return lost
		}
		bs = append(bs, b)
	}
	if len(bs) == 0 {
		return lost
	}

	typed, untyped := 0, 0
	litSet := map[string]struct{}{}
	everyRemainderPresent := true
	for _, b := range bs {
		if b.lit == "" {
			untyped++
		} else {
			typed++
			litSet[b.lit] = struct{}{}
		}
		if b.remainder == nil {
			everyRemainderPresent = false
		}
	}

	// CASE 1 — no branch mentions the type at all. An ordinary disjunction,
	// unchanged from version 1.
	if typed == 0 {
		return TreeTranslation{Root: anyNode(collectRemainders(bs, func(b branch) *rawNode { return b.remainder }), verbatim)}
	}

	// CASE 2 — DISTRIBUTIVITY. Every branch names the SAME one type and every
	// branch has something left after it is removed, so
	//
	//	(A AND T) OR (B AND T)  ==  T AND (A OR B)
	//
	// exactly, by the distributive law. The type is harvested and the
	// remainders become the disjunction. This is the founder's
	// Subscriptions.base and it is UNCHANGED — the conditions below are the
	// same two the version-1 comment set out, and case 3 is only reached when
	// they fail.
	if untyped == 0 && len(litSet) == 1 && everyRemainderPresent {
		var harvested []string
		for lit := range litSet {
			harvested = append(harvested, lit)
		}
		return TreeTranslation{
			Root:         anyNode(collectRemainders(bs, func(b branch) *rawNode { return b.remainder }), verbatim),
			TypeLiterals: harvested,
		}
	}

	// CASE 3 — EVERY branch names a type, but they do not all name the SAME
	// one (or one of them is nothing but its type literal). Nothing factors out
	// and nothing can be decided here, so the whole disjunction is DEFERRED to
	// the per-view pass, which knows the record type this will be applied
	// under. No type is harvested: "either type" is not "this type", and that
	// was always the correct half of the old refusal.
	//
	// A branch with NO type literal keeps this out of case 3 deliberately.
	// Under `type == T` such a branch survives whatever T is, so the reduction
	// would still be exact — but no base in this vault has that shape, so
	// there is nothing to grade it against, and an ungraded reduction is not
	// worth the row it might invent.
	if untyped == 0 {
		branches := make([]typedBranch, 0, len(bs))
		for _, b := range bs {
			branches = append(branches, typedBranch{RecordType: b.lit, Remainder: b.remainder})
		}
		return TreeTranslation{Root: &rawNode{Kind: rawKindTypedAny, Branches: branches, Verbatim: verbatim}}
	}

	// Some branches assert a type and some do not. Neither factors.
	return lost
}

// collectRemainders is the small adapter that keeps the two `anyNode` call
// sites above from each rebuilding the same slice.
func collectRemainders[T any](in []T, f func(T) *rawNode) []*rawNode {
	out := make([]*rawNode, 0, len(in))
	for _, v := range in {
		if n := f(v); n != nil {
			out = append(out, n)
		}
	}
	return out
}

// anyNode wraps children in `any`, collapsing the pointless cases exactly as
// allNode does: no children is no filter, and a one-branch disjunction is that
// branch.
func anyNode(kids []*rawNode, verbatim string) *rawNode {
	switch len(kids) {
	case 0:
		return nil
	case 1:
		return kids[0]
	default:
		return &rawNode{Kind: rawKindAny, Kids: kids, Verbatim: verbatim}
	}
}

// ---------------------------------------------------------------------------
// SETTLING A DEFERRED DISJUNCTION — WHY THIS IS EXACT, AND WHERE IT STOPS
//
// A rawKindTypedAny is `⋁ᵢ (type == Tᵢ ∧ Rᵢ)`, each Rᵢ possibly absent (the
// branch was only its type literal). ReduceTypedDisjunctions is handed the ONE
// record type the view resolved to, T, and rewrites it to `⋁_{Tᵢ = T} Rᵢ`.
//
// THE PROOF DOES NOT DEPEND ON OBSIDIAN'S SEMANTICS, WHICH IS WHY IT HOLDS.
// The natural argument — "a note has one `type:`, so a branch naming a
// different one is false" — is true but it rests on a fact about the source
// vault, and the whole FR-105 discipline exists because facts about the source
// vault are where broadenings come from. The argument that needs nothing:
//
//	THE VIEW THIS PRODUCES WRITES `type: T`, AND A TYPED VIEW RETURNS ONLY
//	RECORDS WHOSE RecordType IS EXACTLY T.
//
// That is our own scoping (knowledgefind's query.selector puts T in the
// propindex Selector's RecordType; a candidate of any other type is never
// streamed at all). So a branch requiring `type == Tᵢ ≠ T` can only ever have
// contributed rows THIS VIEW COULD NOT RETURN IN THE FIRST PLACE. Dropping it
// removes nothing. And a branch requiring `type == T` is asserting something
// already guaranteed, so it reduces to its remainder. The rewrite is an
// EQUIVALENCE under the view's own type scope, not a subset of it — nothing to
// invert, nothing to be careful about downstream.
//
// It is exactly the same reasoning translateOr's case 2 already used for one
// type, applied to the case where the branches disagree. What was missing was
// never a wire format. It was the view's type, which is resolved thirty lines
// after the outer filter is translated.
//
// FOUR PLACES IT DELIBERATELY STOPS, each because the reduction stops being an
// equivalence or stops being gradeable:
//
//   - AND-POSITION ONLY. The rewrite is conditioned on `type == T` being
//     asserted alongside, which the view's own filter does. That conditioning
//     is only available where the disjunction is conjoined with it — under an
//     `or:` or a `not:` it is not, and a node reducing to TRUE inside an `any:`
//     would make the whole disjunction true. Outside AND-position it stays lost.
//   - AN UNTYPED VIEW (T == "") CANNOT REDUCE. With no type asserted the
//     disjunction really does span two domains, and importing it untyped is the
//     vault-wide broadening the old refusal correctly named.
//   - NO SURVIVING BRANCH means the effective filter is FALSE. That is exact —
//     the Obsidian view returns nothing — but "write a view that can never
//     match" is a different product decision from "translate this filter", so
//     it stays lost rather than being silently emitted as an empty view.
//   - UNDER A NEGATION, refused by translateNot. The equivalence above IS
//     preserved by negation, so this one is conservatism rather than necessity;
//     no base in this vault has the shape, so there is nothing to grade it
//     against.
//
// In every stopping case the node degrades to a rawKindLost carrying the SAME
// verbatim it carried before, so the loss text, its position and its
// classification in the report are byte-identical to the old refusal.
// ---------------------------------------------------------------------------

// ReduceTypedDisjunctions settles every deferred type-guarded disjunction in one
// translated tree, under the record type the view resolved to.
//
// It returns a REWRITTEN COPY and mutates nothing. That is load-bearing rather
// than hygienic: a base's outer filter is translated ONCE and then handed to
// every view in the base (TranslateBase), so reducing it in place under the
// first view's type would silently apply that type's reduction to all the
// others — five views in Content.base, resolving to two different types.
func ReduceTypedDisjunctions(t TreeTranslation, recordType string) TreeTranslation {
	out := t
	out.Root = reduceTypedNode(t.Root, recordType, true)
	return out
}

// reduceTypedNode rewrites one subtree. `andPos` reports whether this node sits
// in AND-position from the root — true at the root, preserved through `all`,
// and false under `any` or `not`.
//
// A nil return means "this subtree is TRUE, drop it", which only an `all` (or
// the root) may act on. That is precisely why andPos is tracked rather than
// assumed.
func reduceTypedNode(n *rawNode, recordType string, andPos bool) *rawNode {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case rawKindTypedAny:
		if !andPos {
			return lostNode(n.Verbatim)
		}
		return reduceTypedAny(n, recordType)

	case rawKindAll:
		kids := make([]*rawNode, 0, len(n.Kids))
		for _, k := range n.Kids {
			if rk := reduceTypedNode(k, recordType, true); rk != nil {
				kids = append(kids, rk)
			}
		}
		return allNode(kids)

	case rawKindAny, rawKindNot:
		kids := make([]*rawNode, 0, len(n.Kids))
		for _, k := range n.Kids {
			rk := reduceTypedNode(k, recordType, false)
			if rk == nil {
				// Unreachable today: only a rawKindTypedAny can reduce to
				// nothing, and one is refused outright outside AND-position
				// just above. It is handled rather than ignored because a
				// vanished disjunct would make an `any:` TRUE and a `not:`
				// FALSE, which is the one way this rewrite could broaden.
				return lostNode(n.Verbatim)
			}
			kids = append(kids, rk)
		}
		if n.Kind == rawKindAny {
			return anyNode(kids, n.Verbatim)
		}
		cp := *n
		cp.Kids = kids
		return &cp

	default:
		// A leaf, a prebuilt node or an already-lost one. Nothing to settle.
		return n
	}
}

// reduceTypedAny applies the equivalence to ONE deferred disjunction.
func reduceTypedAny(n *rawNode, recordType string) *rawNode {
	if recordType == "" {
		return lostNode(n.Verbatim)
	}
	kept := make([]*rawNode, 0, len(n.Branches))
	for _, b := range n.Branches {
		if b.RecordType != recordType {
			continue
		}
		if b.Remainder == nil {
			// This branch is `type == T` and nothing else, so under T it is
			// TRUE — and one true disjunct makes the whole disjunction true.
			// It contributes no constraint at all.
			return nil
		}
		kept = append(kept, b.Remainder)
	}
	if len(kept) == 0 {
		return lostNode(n.Verbatim)
	}
	return anyNode(kept, n.Verbatim)
}

// containsTypedAny reports whether a subtree still holds a deferred
// disjunction, so `not:` can refuse one rather than carry it somewhere its
// reduction has not been proved.
func containsTypedAny(n *rawNode) bool {
	if n == nil {
		return false
	}
	if n.Kind == rawKindTypedAny {
		return true
	}
	for _, k := range n.Kids {
		if containsTypedAny(k) {
			return true
		}
	}
	return false
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
		if len(sub.TypeLiterals) > 0 || sub.Root == nil || containsLost(sub.Root) || containsTypedAny(sub.Root) {
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
	// A deferred disjunction keeps its branches OUTSIDE Kids, and this walk is
	// what decides whether a group is carried at all. Missing them would let a
	// lost leaf ride inside a branch remainder into a translated view — the
	// one shape "lost as a unit" exists to prevent.
	for _, b := range n.Branches {
		if containsLost(b.Remainder) {
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

// ---------------------------------------------------------------------------
// CARRYING A BASE'S `formulas:` BLOCK (FR-140 / FR-141)
//
// A `.base` file declares computed properties once, at the top level, and any
// view in the file names one as `formula.<name>` in a filter, a column, a sort
// key, a grouping or a summary. Until this code existed ParsedBase dropped the
// block on the floor, so 57 references across 14 of the founder's 18 bases had
// nothing to resolve against.
//
// THE EXPRESSION LANGUAGES ARE NOT THE SAME LANGUAGE, and that is the whole
// difficulty. Obsidian's formulas are JavaScript expressions over JavaScript
// values: an absent property is `undefined`, `""` is falsy, a bare property is
// a truthiness test, and `if()`'s two branches may return different types. Ours
// (pkg/records) is a statically typed expression language over ADR-068's
// PropertyValue: absence PROPAGATES (R-14), `""` is absence for every non-text
// type (FR-007a), `if()`'s condition must be a boolean and its branches must
// share ONE type (FR-143a).
//
// So a base formula is not copied — it is TRANSLATED, by exactly two rewrites,
// each of which is a proved equivalence and neither of which is a guess. What
// neither rewrite reaches stays a NAMED LOSS at every position that references
// it, which is what keeps FR-105 true: a formula that decides a row set and did
// not translate leaves its view DISABLED, exactly as it is today.
//
// THE TWO REWRITES, AND THE PROOF OF EACH
//
// W1 — `if(C, X, "")` becomes `if(C, X)`.
//
//	`""` is Obsidian's "show nothing" idiom for the else branch, and a
//	two-argument `if` is ours: evalIf's own comment calls the missing branch
//	"absence, which is what FR-143a means by 'or one branch be absent'". The
//	two render identically. It is also the ONLY way the founder's
//	`if(cost.isType("number"), …, "")` can be carried at all — FR-143a refuses
//	a number then-branch against a text else-branch outright.
//
//	ONE DIVERGENCE, AND IT IS NAMED WHERE IT CAN MATTER. In a COMPARISON,
//	JavaScript's `"" <= 60` is true and our absence compares FALSE under R-2.
//	So a filter over such a formula returns FEWER rows than Obsidian's. Fewer
//	is the direction FR-105 permits, and buildFormulaLeafNode names it on the
//	view rather than leaving it to be discovered.
//
// W2 — `if(P, X, <nothing>)` becomes `X`, when P is a bare DATE property and X
// is absent wherever P is.
//
//	Obsidian's bare `P` is a truthiness test. For a `date` property — and for
//	no other type — truthy is exactly PRESENT: FR-007a makes `""` absent on a
//	date, and a real date is never falsy in JavaScript. So the guard asks "is P
//	present", and the guarded expression already answers absence when P is
//	absent, because absence propagates through every step the founder's
//	formulas take: arithmetic over an absent operand is absent
//	(formula_eval.go's evalArithmetic), and a field access on an absent
//	receiver is absent (evalFieldAccess). The guard is therefore REDUNDANT, and
//	dropping a redundant guard changes no value on any record.
//
//	`date` IS THE WHOLE PERMITTED SET, deliberately. On text, `""` is a PRESENT
//	falsy value (FR-007a says so in as many words); on a number it is `0`; on a
//	checkbox it is `false`. For those three "truthy" and "present" are
//	different questions and the rewrite would be a guess. absentWhenAbsent is
//	the second half of the same discipline: it walks the guarded expression and
//	answers true only for the node kinds whose absence behaviour is written
//	down in the evaluator, and false for everything else.
//
// W3 — `if(P, X, <nothing>)` becomes `if(P != "", X)`, when P is a bare
// single-valued TEXT property.
//
//	W2 could not reach this and said so: on text `""` is a PRESENT falsy value
//	(FR-007a), so "truthy" and "present" are different questions and DROPPING
//	the guard would change the value of records whose P is the empty string.
//	That reasoning was right about DROPPING the guard, which was the only move
//	anyone had asked about. Keeping the guard and merely SPELLING it in our own
//	grammar is a different question with a better answer, and the operator it
//	needs — a comparison against the EMPTY LITERAL on a text property — is the
//	one commit 9d2b16c4 established exists (`VaultFilterNode.value` carries no
//	`minLength`; `records.Filter.LiteralGiven` exists in as many words because
//	"the empty string is a legitimate value for `=`"; and inside a FORMULA the
//	comparison does not even go through a filter — evalComparison hands the two
//	operands straight to the ONE comparator).
//
//	THE THREE STATES, WHICH IS THE WHOLE PROOF. FR-007a keeps `""` PRESENT on a
//	text property, so absent and empty are DIFFERENT states here and a rewrite
//	has to match Obsidian on both, not on one of them:
//
//	  state of a text P    Obsidian: bare `P`        ours: `P != ""`
//	  absent               `undefined` -> FALSY      FALSE — §8 R-2, either
//	                                                 side absent is false for
//	                                                 every operator but
//	                                                 IS NULL / IS NOT NULL
//	                                                 (compare_oracle.go); a
//	                                                 formula calls the
//	                                                 comparator DIRECTLY, so
//	                                                 FR-008's absent-rescue —
//	                                                 which lives a layer up in
//	                                                 Filter.Match — never
//	                                                 applies
//	  present, `""`        `""` -> FALSY             FALSE — `"" <> ""`
//	  present, a value     non-empty string ->       TRUE — FoldKey case-folds
//	                       TRUTHY                    and does NOT trim, so `" "`
//	                                                 is a value on both sides
//
//	The two columns coincide on every state, so the CONDITION is exact — not a
//	subset, which matters more than it sounds: a subset would be safe only in
//	positive position and would have to be refused under a `not:` the way the
//	`!= ""` LEAF is (view_write.go's shapeIsSet). There is nothing to invert
//	here. The rewritten formula takes the same branch on the same records, and
//	a filter over it therefore selects the same rows at any depth of negation.
//
//	WHY IT IMPOSES NO CONDITION ON THE GUARDED EXPRESSION, where W2 does. W2
//	DELETES the guard, so the guarded expression has to answer absence on its
//	own; W3 KEEPS it. X is evaluated on exactly the records Obsidian evaluates
//	it on, so whether X propagates absence is not this rewrite's question.
//
//	`many` IS EXCLUDED, like W2's. JavaScript reads an empty ARRAY as truthy
//	while `<> ""` on a many property is element-wise (R-9) and answers false
//	for it. That is a narrowing rather than a broadening, but it is not an
//	equivalence, and this rewrite only makes equivalences.
//
//	THE ONE STATE THAT IS NOT ONE OF THE THREE, named rather than discovered
//	later: a record whose single-valued text P holds a LIST is NON-CONFORMING
//	(§8 R-4, validate.go's arity check), and R-4 answers false for every
//	operator. JavaScript reads the array as truthy. Ours takes the else-branch
//	where Obsidian takes the then-branch — FEWER rows, the direction FR-105
//	permits. It is also not a divergence this rewrite introduces: R-4 is
//	product-wide, and W2's guard-DROPPING has the identical behaviour there,
//	because the guarded expression over a non-conforming operand is absent too.
//
// WHAT IS DELIBERATELY NOT REWRITTEN. `if(P, <boolean over P>, false)` reduces
// too — a comparison over an absent operand is FALSE under R-2, not absent, so
// the then-branch already yields the else-branch's value. It is left as a
// named loss all the same: that proof runs through the comparator's rules
// rather than through absence propagation, `!=` behaves differently from the
// other five operators under R-2, and a second, differently-argued rewrite is
// how a translator starts being approximately right. The founder's
// `is_overdue` formulas stay lost, their views stay DISABLED, and the report
// says so.
// ---------------------------------------------------------------------------

// FormulaTranslation is a base's `formulas:` block, translated.
type FormulaTranslation struct {
	// Sources is the source text this importer will write into a view file's
	// `formulas:` map, keyed by name. Every entry parsed, typed against the
	// view's schema and fitted FR-146's caps as part of one validated set.
	Sources map[string]string
	// Set is that validated set, so a reference can be resolved to a TYPE
	// without re-parsing anything.
	Set *records.FormulaSet
	// Refused names every formula that could not be carried, with the reason,
	// so a reference to one can be reported quoting the real fault instead of
	// "no such formula".
	Refused map[string]string
	// Rewritten records, per carried formula, what W1/W2 changed — reported
	// where the formula is USED, because a source that no longer matches the
	// `.base` file must not be discoverable only by diffing two files.
	Rewritten map[string]string
}

// FormulaNames lists the carried formulas, sorted.
func (ft FormulaTranslation) FormulaNames() []string {
	out := make([]string, 0, len(ft.Sources))
	for n := range ft.Sources {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Closure returns the given names plus every formula they reach transitively,
// sorted, restricted to formulas that were actually carried.
//
// A VIEW DECLARES ONLY WHAT IT USES, and that is FR-146 arithmetic rather than
// tidiness: a view is capped at 16 formulas and 256 nodes IN TOTAL, and every
// declared formula is evaluated once per candidate. Emitting a base's whole
// block into every one of its views spends that budget on formulas the view
// never names — and the founder's Tasks.base, whose four formulas include one
// that alone nests twelve levels deep, is exactly the shape that makes the
// difference matter.
//
// The closure is over Refs because a reference must not dangle: Deals.base's
// `is_stale` names `formula.age`, and a view declaring the first without the
// second is a file records.ValidateViewAgainstSchemas refuses WHOLE
// (RejectViewUnknownFormula) rather than one clause of.
func (ft FormulaTranslation) Closure(names []string) []string {
	if ft.Set == nil {
		return nil
	}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(n string) {
		if seen[n] {
			return
		}
		decl, ok := ft.Set.Get(n)
		if !ok {
			return
		}
		seen[n] = true
		for _, r := range decl.Refs {
			walk(r)
		}
	}
	for _, n := range names {
		walk(n)
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Declared reports one carried formula's declaration.
func (ft FormulaTranslation) Declared(name string) (records.FormulaDecl, bool) {
	if ft.Set == nil {
		return records.FormulaDecl{}, false
	}
	return ft.Set.Get(name)
}

// RefusalFor explains why a formula is not available, in the operator's own
// terms. It answers for a name the base never declared as well as for one it
// declared and this importer could not carry, because a reference reported as
// "no such formula" when the base plainly declares it sends a reader to the
// wrong file.
func (ft FormulaTranslation) RefusalFor(name string) string {
	if r, ok := ft.Refused[name]; ok {
		return r
	}
	return fmt.Sprintf("the base file declares no formula %q", name)
}

// maxFormulaTranslationPasses bounds the drop-and-revalidate loop below. Each
// pass drops at least one formula, so the loop cannot outrun the number of
// formulas; the constant is a belt against a future validator that reports an
// error it does not attribute to a name, which would otherwise spin.
const maxFormulaTranslationPasses = 32

// TranslateFormulas translates one base's `formulas:` block against the schema
// of the record type a view resolved to (nil for an untyped view, which is
// handled honestly one layer down: SchemaFormulaEnv refuses every property
// operand, so a formula naming one is refused rather than guessed).
//
// A FORMULA THAT FAILS IS DROPPED, AND THE REST STILL CARRY. That is what the
// pass loop is for. ValidateFormulaSet reports every failure it found, each
// attributed to a name; dropping those and re-validating lets a base with one
// untranslatable formula keep the other three — and it also resolves the
// knock-on case correctly, because a formula referencing a dropped one fails on
// the NEXT pass with `formula.x names a formula the view does not define` and
// is dropped in its turn, rather than being carried as a dangling reference the
// loader would then refuse the whole file over.
func TranslateFormulas(pb *ParsedBase, schema *records.Schema) FormulaTranslation {
	out := FormulaTranslation{
		Sources:   map[string]string{},
		Refused:   map[string]string{},
		Rewritten: map[string]string{},
	}
	if pb == nil || len(pb.FormulaNames) == 0 {
		return out
	}

	candidates := map[string]string{}
	for _, name := range pb.FormulaNames {
		src, readable := pb.Formulas[name]
		if !readable {
			// ParseBaseFile kept the NAME and refused to invent a source for a
			// value that was not a scalar string. Refusing here by name is the
			// whole point of that split: the key is reported rather than
			// quietly never having existed.
			out.Refused[name] = "the base declares this formula with a value that is not an expression string, so there is no source to translate"
			continue
		}
		rewritten, note := rewriteFormulaSource(src, schema)
		candidates[name] = rewritten
		if note != "" {
			out.Rewritten[name] = note
		}
	}

	for pass := 0; pass < maxFormulaTranslationPasses && len(candidates) > 0; pass++ {
		set, errs := records.ValidateFormulaSet(candidates, schema)
		if len(errs) == 0 {
			out.Sources = candidates
			out.Set = set
			return out
		}
		dropped := false
		for _, e := range errs {
			if e.Formula == "" {
				// A SET-LEVEL refusal — one of FR-146's caps on the view as a
				// whole. It is attributable to no single formula, so nothing
				// can be dropped to satisfy it and carrying a subset would be
				// this importer choosing which of the operator's formulas to
				// keep. The whole block is refused instead, naming the cap.
				for name := range candidates {
					out.Refused[name] = "the base's `formulas:` block as a whole was refused: " + e.Error()
				}
				out.Sources = map[string]string{}
				out.Rewritten = map[string]string{}
				return out
			}
			if _, still := candidates[e.Formula]; !still {
				continue
			}
			delete(candidates, e.Formula)
			out.Refused[e.Formula] = e.Error()
			delete(out.Rewritten, e.Formula)
			dropped = true
		}
		if !dropped {
			// Every reported error named a formula already gone. Nothing more
			// can be dropped, so refuse what remains rather than loop.
			for name := range candidates {
				out.Refused[name] = "this formula could not be validated as part of the base's formula set"
			}
			out.Sources = map[string]string{}
			out.Rewritten = map[string]string{}
			return out
		}
	}
	return out
}

// rewriteFormulaSource applies W1, W2 and W3 until none fires, returning the
// translated source and a human-readable note naming what changed (empty when
// the source was carried verbatim).
//
// It is deliberately a SOURCE-TEXT rewrite rather than a tree rewrite: FR-141
// stores source, this package has no formula printer, and a printer written to
// serve one rewrite would silently reformat every formula it touched. Each
// candidate substring is parsed before it is used, so nothing this function
// emits has escaped records.ParseFormula.
func rewriteFormulaSource(src string, schema *records.Schema) (out string, note string) {
	out = strings.TrimSpace(src)
	var notes []string
	for pass := 0; pass < 4; pass++ {
		args, ok := splitTopLevelIfArgs(out)
		if !ok {
			break
		}
		if len(args) == 3 && isEmptyTextLiteral(args[2]) {
			out = "if(" + args[0] + ", " + args[1] + ")"
			notes = append(notes, `its "" else-branch was dropped — an omitted `+"`if`"+` branch is this product's own spelling of "show nothing", and FR-143a refuses a number branch paired with a text one`)
			continue
		}
		if len(args) == 2 {
			if guard, guarded, reduced := reduceRedundantDateGuard(args[0], args[1], schema); reduced {
				out = guarded
				notes = append(notes, "its `if("+guard+", …)` presence guard was dropped as REDUNDANT — the guarded expression already answers absence wherever `"+guard+"` is absent, so no record's value changes")
				continue
			}
			if guard, rewritten, ok := spellTextTruthinessGuard(args[0], args[1], schema); ok {
				out = rewritten
				notes = append(notes, "its bare `if("+guard+", …)` truthiness guard was spelled as `"+guard+` != ""`+"` — on a text property Obsidian's truthiness is exactly \"present and not the empty string\", which is what this comparison selects on all three states (absent, `\"\"`, a value), so the same records take the same branch")
				continue
			}
		}
		break
	}
	if _, err := records.ParseFormula(out); err != nil {
		// A rewrite that does not parse is not a rewrite. Fall back to the
		// operator's own text so the refusal quotes what they wrote.
		return strings.TrimSpace(src), ""
	}
	return out, strings.Join(notes, "; ")
}

// reduceRedundantDateGuard decides W2. It returns the guarded expression when
// the guard is provably redundant, and reports false otherwise — including for
// every case it simply cannot prove, which is the safe answer.
func reduceRedundantDateGuard(guard, guarded string, schema *records.Schema) (string, string, bool) {
	if schema == nil {
		return "", "", false
	}
	guardNode, err := records.ParseFormula(guard)
	if err != nil {
		return "", "", false
	}
	ref, ok := guardNode.(*records.Ref)
	if !ok || ref.Kind != records.RefProperty {
		return "", "", false
	}
	prop, found := schema.Property(ref.Name)
	if !found || prop.Type != records.TypeDate || prop.Many {
		// Only a single-valued DATE property has "truthy" and "present" as the
		// same question — see this section's header. A `many` date is excluded
		// too: an empty list is present-and-falsy in JavaScript.
		return "", "", false
	}
	guardedNode, err := records.ParseFormula(guarded)
	if err != nil {
		return "", "", false
	}
	if !absentWhenAbsent(guardedNode, ref.Name) {
		return "", "", false
	}
	return ref.Name, strings.TrimSpace(guarded), true
}

// spellTextTruthinessGuard decides W3. It returns the guard's own source text
// and the whole rewritten `if(...)` when the guard is a bare single-valued TEXT
// property, and reports false otherwise — including for every case it cannot
// prove, which is the safe answer.
//
// IT REWRITES THE CONDITION AND KEEPS THE `if`, which is why it needs nothing
// from the guarded expression. See W3's proof in this section's header for the
// three-state table that makes `P != ""` the exact spelling of Obsidian's
// truthiness on text — and for the single non-conforming state where the two
// differ, in the narrowing direction, for a reason (§8 R-4) that predates this
// rewrite and applies to W2's guard-dropping identically.
func spellTextTruthinessGuard(guard, guarded string, schema *records.Schema) (string, string, bool) {
	if schema == nil {
		return "", "", false
	}
	guardNode, err := records.ParseFormula(guard)
	if err != nil {
		return "", "", false
	}
	ref, ok := guardNode.(*records.Ref)
	if !ok || ref.Kind != records.RefProperty {
		return "", "", false
	}
	prop, found := schema.Property(ref.Name)
	if !found || prop.Type != records.TypeText || prop.Many {
		// TEXT and single-valued is the whole permitted set. Every other type
		// either has no empty-string VALUE to compare against at all (FR-007a
		// makes `""` the absent state there, so `P != ""` is `IS NOT NULL`
		// spelled awkwardly and W2 already rules the one case it fits), or —
		// `many` — reads an empty list as truthy in JavaScript while `<>` is
		// element-wise here.
		return "", "", false
	}
	// The guard's OWN source text is reused rather than re-printed from the
	// parsed Ref: this package has no formula printer, and a name that needed
	// quoting would come back out unquoted from one written to serve this line.
	g := strings.TrimSpace(guard)
	return g, `if(` + g + ` != "", ` + strings.TrimSpace(guarded) + `)`, true
}

// absentWhenAbsent reports whether an expression is ABSENT on every record
// where the named property is absent.
//
// IT IS A WHITELIST OVER THE EVALUATOR'S WRITTEN-DOWN RULES, not an inference.
// Every `true` below cites a rule in pkg/records/formula_eval.go, and anything
// not listed answers FALSE — so a node kind added to the grammar tomorrow costs
// a rewrite that does not fire (a formula reported as a named loss), never a
// rewrite that fires wrongly (a formula whose values silently changed).
func absentWhenAbsent(n records.FormulaNode, prop string) bool {
	switch node := n.(type) {
	case *records.Ref:
		// The property itself. Absent is absent.
		return node.Kind == records.RefProperty && node.Name == prop
	case *records.Call:
		// `date(x)` / `time(x)` read a value; evalCall's conversions answer
		// absence for an absent argument. Every OTHER call — `if`, `list`,
		// `mean`, `isType`, the `file.*` predicates — can produce a value from
		// an absent argument, so none of them is here.
		switch node.Name {
		case "date", "time":
			return len(node.Args) == 1 && absentWhenAbsent(node.Args[0], prop)
		}
		return false
	case *records.BinaryOp:
		// evalArithmetic: "if left.absent || right.absent { return absentOf }".
		// The COMPARISON and LOGICAL operators are excluded on purpose — R-2
		// makes a comparison over an absent operand FALSE, which is a value.
		switch node.Op {
		case "+", "-", "*", "/", "%":
			return absentWhenAbsent(node.Left, prop) || absentWhenAbsent(node.Right, prop)
		}
		return false
	case *records.UnaryOp:
		return absentWhenAbsent(node.Operand, prop)
	case *records.FieldAccess:
		// evalFieldAccess: "if recv.absent { return absentOf(rule.result) }".
		return absentWhenAbsent(node.Receiver, prop)
	}
	return false
}

// splitTopLevelIfArgs reads `if(a, b)` or `if(a, b, c)` and returns the
// argument source slices, verbatim.
//
// It refuses anything that is not exactly one top-level `if` call spanning the
// WHOLE expression — `if(a,b) + 1` has a closing paren before the end and is
// rejected here rather than mis-split. The scan tracks quotes as well as
// parens, so a comma inside a string literal is not an argument boundary.
func splitTopLevelIfArgs(src string) ([]string, bool) {
	s := strings.TrimSpace(src)
	if !strings.HasPrefix(s, "if") {
		return nil, false
	}
	rest := strings.TrimSpace(s[len("if"):])
	if !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
		return nil, false
	}
	inner := rest[1 : len(rest)-1]

	var args []string
	var cur strings.Builder
	depth := 0
	var quote byte
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
			cur.WriteByte(c)
		case '(', '[':
			depth++
			cur.WriteByte(c)
		case ')', ']':
			depth--
			if depth < 0 {
				// The `(` we consumed was not the call's own outermost paren —
				// e.g. `if(a) && if(b)`. Not a shape this reads.
				return nil, false
			}
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(cur.String()))
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	if depth != 0 || quote != 0 {
		return nil, false
	}
	args = append(args, strings.TrimSpace(cur.String()))
	if len(args) < 2 || len(args) > 3 {
		return nil, false
	}
	for _, a := range args {
		if a == "" {
			return nil, false
		}
	}
	return args, true
}

// isEmptyTextLiteral reports whether an argument is exactly the empty string
// literal — Obsidian's "show nothing" else-branch. `" "` is NOT one: a space is
// a value, and treating it as absence would drop a character the operator typed.
func isEmptyTextLiteral(arg string) bool {
	s := strings.TrimSpace(arg)
	return s == `""` || s == `''`
}
