// view_resolve.go: Resolve the intermediate filter tree against the view's record type

package vaultimport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"gopkg.in/yaml.v3"
)

// conjoin ANDs the base's outer filter with the view's own — which is exactly
// what Obsidian does, and the one place the two trees meet.
func conjoin(a, b *generated.VaultFilterNode) *generated.VaultFilterNode {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	kids := []generated.VaultFilterNode{*a, *b}
	return &generated.VaultFilterNode{All: &kids}
}

// declaredSchema returns the view's record type as a real *records.Schema.
//
// It falls back to rendering one on demand when the field was not populated,
// which keeps every hand-built leafResolver in the tests answering the same way
// a production one does — a resolver whose schema silently became nil would
// make this file's engine-derived checks pass by not running.
func (r leafResolver) declaredSchema() *records.Schema {
	if r.schema != nil {
		return r.schema
	}
	return schemaForType(r.schemas, r.recordType)
}

// typed reports whether this view declares a record type, and therefore whether
// a property's declared type is knowable at all.
func (r leafResolver) typed() bool { return r.recordType != "" }

// resolve turns one intermediate subtree into a real VaultFilterNode, plus the
// named losses it produced.
//
// The FR-105 posture per combinator, stated once:
//
//	all  a child that could not be resolved is DROPPED and its loss named.
//	     Dropping a conjunct BROADENS, which is why that loss sits in a
//	     row-set-affecting position and disables the whole view.
//	any  a child that could not be resolved loses the WHOLE group. Keeping
//	     the rest would narrow the view to one side of an "either" the
//	     operator wrote deliberately — reported instead of guessed.
//	not  same: half of a negation is a different exclusion, not a partial one.
//	     It ALSO flips the polarity every leaf beneath it is judged at — see
//	     resolveTree and v2Leaf.Negated.
func (r leafResolver) resolve(n *rawNode, pos LossPosition) (*generated.VaultFilterNode, []string) {
	node, losses := r.resolveTree(n, pos, false)
	return node, renderLosses(losses)
}

// render writes the loss the way the report reads it — `[position] expression`
// with the reason after a " — " separator when there is one. A loss with no
// reason renders exactly as it always did; splitLossLine then reports no
// reason, and report.go classifies it from the expression's shape.
func (l resolvedLoss) render() string {
	if l.Reason == "" {
		return lossf(l.Pos, "%s", l.Expr)
	}
	return lossf(l.Pos, "%s — %s", l.Expr, l.Reason)
}

// ---------------------------------------------------------------------------
// A LOSS IS AN EXPRESSION AND A REASON, AND THEY HAVE TO TRAVEL SEPARATELY
//
// They used to be glued together the moment a loss was made, as one rendered
// string. That is why a clause could be dropped with NO stated reason: `!=`
// desugars into a TREE NEGATION over an `=` leaf (nodeFromRawLeaf, and it must
// — `{not: {p,=,v}}` keeps the records where `p` is absent and `{p,<>,v}` drops
// them), so when the leaf underneath cannot be built, the failure is a CHILD's
// and the thing that must be NAMED is the PARENT's text. Reporting the parent
// meant throwing the child's whole diagnosis away. Six clauses across three of
// the founder's bases were reported as gone with nothing said about why.
//
// Keeping the two halves apart until the last moment lets the wrapper report
// its own expression AND the child's reason, which is the FR-107 answer:
//
//	[view filter] realm != "personal" — property "realm" is not declared in …
//
// WHAT THIS DELIBERATELY DOES NOT DO. It changes no node, no loss count and no
// loss position — only the words. That restraint is the point rather than
// modesty: `not:` is where a narrowing becomes a BROADENING. A multi-clause
// `not:` "loses nothing" only because Obsidian ANDs then negates, and the
// negation has no absence rule of its own downstream (knowledge_find evaluates
// `!inner.matched` flat; FR-008's absent-rescue lives on the negative
// OPERATORS in records.PreparedFilter.MatchValue and never reaches a
// COMBINATOR). So a clause that is safely narrower on its own is not safely
// narrower under a negation, and nothing may become newly translatable here
// without a proof at the TREE level. Improving a sentence needs no such proof.
//
// THAT PROOF NOW EXISTS, AND IT IS A MECHANISM RATHER THAN A PROMISE.
// resolveTree carries the POLARITY of the path from the root — flipped by
// every `not:`, preserved by `all` and `any` — down to each leaf as
// v2Leaf.Negated, and buildV2LeafNode refuses by name every translation that
// is only a SUBSET of the Obsidian clause when it finds itself under an odd
// number of negations. A translation that is EXACT (`=` on a declared value,
// `IS NOT NULL` for a truthy test on a type with no falsy value) is unaffected
// at any polarity, which is what keeps the ordinary `p != "done"` desugar —
// itself a `not:` over an `=` leaf — translating exactly as before.
// ---------------------------------------------------------------------------

// resolvedLoss is one named loss before it is rendered: the EXPRESSION the base
// file carried, and separately the diagnosis of why it went.
type resolvedLoss struct {
	Pos    LossPosition
	Expr   string
	Reason string
}

func renderLosses(in []resolvedLoss) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, l := range in {
		out = append(out, l.render())
	}
	return out
}

// joinReasons gathers the diagnoses of the children a combinator lost, in the
// order they were written, without repeating one. A child that had no reason
// contributes nothing rather than an empty clause.
func joinReasons(in []resolvedLoss) string {
	seen := map[string]bool{}
	var out []string
	for _, l := range in {
		if l.Reason == "" || seen[l.Reason] {
			continue
		}
		seen[l.Reason] = true
		out = append(out, l.Reason)
	}
	return strings.Join(out, "; ")
}

// resolveTree is resolve's body, working in unrendered losses so that a
// combinator can report its OWN expression with its CHILD's reason.
func (r leafResolver) resolveTree(n *rawNode, pos LossPosition, neg bool) (*generated.VaultFilterNode, []resolvedLoss) {
	if n == nil {
		return nil, nil
	}
	switch n.Kind {
	case rawKindLost:
		return nil, []resolvedLoss{{Pos: pos, Expr: n.Verbatim, Reason: n.Reason}}

	case rawKindPrebuilt:
		return n.Prebuilt, nil

	case rawKindLeaf:
		// The POLARITY travels with the leaf, because the leaf builder is
		// where a translation is chosen and only the walk knows how many
		// `not:` wrappers stand above it. `all` and `any` preserve it: a
		// subset conjunct narrows a conjunction and a subset disjunct
		// narrows a disjunction. `not` inverts it. See v2Leaf.Negated.
		leaf := n.Leaf
		leaf.Negated = neg
		node, reason, ok := buildV2LeafNode(r, leaf)
		if !ok {
			return nil, []resolvedLoss{{Pos: LossFilterLeaf, Expr: describeLeaf(leaf), Reason: reason}}
		}
		return node, nil

	case rawKindAll:
		var kids []generated.VaultFilterNode
		var losses []resolvedLoss
		for _, k := range n.Kids {
			child, childLosses := r.resolveTree(k, pos, neg)
			losses = append(losses, childLosses...)
			if child != nil {
				kids = append(kids, *child)
			}
		}
		switch len(kids) {
		case 0:
			return nil, losses
		case 1:
			return &kids[0], losses
		default:
			return &generated.VaultFilterNode{All: &kids}, losses
		}

	case rawKindAny, rawKindNot:
		var kids []generated.VaultFilterNode
		childNeg := neg
		if n.Kind == rawKindNot {
			childNeg = !neg
		}
		for _, k := range n.Kids {
			child, childLosses := r.resolveTree(k, pos, childNeg)
			if child == nil || len(childLosses) > 0 {
				// The group's own verbatim is what is NAMED, not the child's —
				// a reader has to see which `or:`/`not:` block went missing,
				// and a half-named group reads as if the rest survived. The
				// child's REASON rides along, because "which" and "why" are
				// two different questions and only the first was ever answered.
				return nil, []resolvedLoss{{Pos: pos, Expr: n.Verbatim, Reason: joinReasons(childLosses)}}
			}
			kids = append(kids, *child)
		}
		if len(kids) == 0 {
			return nil, []resolvedLoss{{Pos: pos, Expr: n.Verbatim}}
		}
		if n.Kind == rawKindNot {
			inner := kids[0]
			if len(kids) > 1 {
				all := kids
				inner = generated.VaultFilterNode{All: &all}
			}
			return &generated.VaultFilterNode{Not: &inner}, nil
		}
		return &generated.VaultFilterNode{Any: &kids}, nil
	}
	return nil, []resolvedLoss{{Pos: pos, Expr: n.Verbatim, Reason: n.Reason}}
}

func describeLeaf(l v2Leaf) string {
	if l.Source != "" {
		return l.Source
	}
	return l.Property
}

// ---------------------------------------------------------------------------
// Rendering a filter tree as the YAML records.ParseView reads back
// ---------------------------------------------------------------------------

// filterNodeYAML renders one VaultFilterNode as a key-ordered YAML mapping.
//
// The keys are the generated type's own `json:` tags, and they have to be
// exactly those: ParseView decodes the file through encoding/json with
// DisallowUnknownFields, so a near-miss spelling is a REJECTED view rather
// than a silently ignored key.
func filterNodeYAML(n generated.VaultFilterNode) *yaml.Node {
	var pairs []ordPair
	if n.Property != nil {
		pairs = append(pairs, ordPair{Key: "property", Value: *n.Property})
	}
	if n.Op != nil {
		pairs = append(pairs, ordPair{Key: "op", Value: string(*n.Op)})
	}
	if n.Value != nil {
		pairs = append(pairs, ordPair{Key: "value", Value: *n.Value})
	}
	if n.Values != nil {
		pairs = append(pairs, ordPair{Key: "values", Value: *n.Values})
	}
	if n.All != nil {
		pairs = append(pairs, ordPair{Key: "all", Value: filterChildrenYAML(*n.All)})
	}
	if n.Any != nil {
		pairs = append(pairs, ordPair{Key: "any", Value: filterChildrenYAML(*n.Any)})
	}
	if n.Not != nil {
		pairs = append(pairs, ordPair{Key: "not", Value: filterNodeYAML(*n.Not)})
	}
	return orderedMap(pairs...)
}

func filterChildrenYAML(children []generated.VaultFilterNode) *yaml.Node {
	nodes := make([]*yaml.Node, 0, len(children))
	for _, c := range children {
		nodes = append(nodes, filterNodeYAML(c))
	}
	return seq(nodes...)
}

// ---------------------------------------------------------------------------
// The non-filter halves of a view
// ---------------------------------------------------------------------------

// checkProperty answers whether a property name may appear in a view file this
// importer writes, in a COMPARISON position (a filter, a grouping key, a sort
// key, an aggregate target) or a DISPLAY one (`properties`).
//
// It mirrors records.ValidateViewAgainstSchemas' own checkV2Prop, and it has to:
// a name that check rejects makes the loader refuse the WHOLE view file, so a
// name this importer cannot vouch for must become a named loss here instead.
//
// An UNTYPED view checks nothing outside the reserved namespaces, which is not
// an oversight — FR-018b resolves an untyped view's ordinary property names
// over FR-021e's raw rows at query time, so there is no name the loader would
// refuse and therefore none this function may.
func (r leafResolver) checkProperty(name string, comparison bool) (reason string, ok bool) {
	switch {
	case strings.HasPrefix(name, formulaNamespace):
		// FR-140: a query reaches a formula ONLY as `formula.<name>`, resolved
		// against the saved view's own block. The reference is legal exactly
		// when this view will declare that formula — anything else makes the
		// loader refuse the WHOLE file (RejectViewUnknownFormula), so a name
		// that did not translate has to become a named loss here instead.
		ref := strings.TrimPrefix(name, formulaNamespace)
		if _, carried := r.formulas.Declared(ref); !carried {
			return r.formulas.RefusalFor(ref), false
		}
		return "", true
	case records.IsFileNamespace(name):
		if !records.IsFileProperty(name) {
			return fmt.Sprintf("%q is not one of the reserved file properties (%s)", name, strings.Join(records.FilePropertyNames, ", ")), false
		}
		if comparison && name == records.FileSelfProp {
			return fmt.Sprintf("%q is the note itself and is not a comparison target", name), false
		}
		return "", true
	case !r.typed():
		// FR-018b makes every ordinary name LEGAL in an untyped view, and the
		// loader accordingly refuses none of them. The ENGINE is a different
		// question, and this is where the two were confused: knowledge_find
		// resolves an untyped name against every in-scope record type, and
		// REFUSES the whole request when two of them declare it in different
		// comparison domains ("An untyped query will not split one name across
		// two domains"). So a name this importer cannot vouch for has to become
		// a named loss here as well.
		if reason, split := r.untypedSplitDomain(name); split {
			return reason, false
		}
		return "", true
	default:
		if _, found := r.schemas.Lookup(r.recordType, name); !found {
			return fmt.Sprintf("not a declared property of %q", r.recordType), false
		}
		return "", true
	}
}

// untypedSplitDomain reports whether an UNTYPED view naming this property would
// be refused by knowledge_find because two in-scope record types declare it in
// different comparison domains.
//
// WHY THIS IS RESTATED RATHER THAN CALLED, and what is done about it. The rule
// lives in knowledgefind's own `sameUntypedDomain`, and calling it would be the
// better shape — a peer exported SummaryOpDefinedForType for exactly this
// reason and view_write.go's summary gate calls it. It is unexported, and this
// change owns no file in that package, so the rule is written out here instead
// and then GRADED against the engine: TestUntypedSplitDomain_PredictsFindExactly
// puts every declaration pair to both this predicate and a real
// knowledgefind.Find, and a single disagreement fails. A restatement nobody
// measures is how the importer and the engine drift apart; a restatement
// measured against the engine is a slower export.
//
// THE THREE FIELDS ARE THE ENGINE'S, not a selection made here: declared TYPE,
// declared ARITY, and — for a link-valued property — the TARGET type. All three
// decide which rule of §8 a comparison runs under. An enum's VALUE SET is
// deliberately NOT one of them: two `enum` declarations are one domain and the
// engine unions their sets.
func (r leafResolver) untypedSplitDomain(name string) (reason string, split bool) {
	if r.schemas == nil || strings.HasPrefix(name, formulaNamespace) || records.IsFileNamespace(name) {
		return "", false
	}
	type decl struct {
		recordType string
		prop       InferredProperty
	}
	types := make([]string, 0, len(r.schemas.byType))
	for t := range r.schemas.byType {
		types = append(types, t)
	}
	sort.Strings(types)

	var decls []decl
	for _, t := range types {
		if p, ok := r.schemas.Lookup(t, name); ok {
			decls = append(decls, decl{recordType: t, prop: p})
		}
	}
	if len(decls) < 2 {
		return "", false
	}
	first := decls[0]
	for _, d := range decls[1:] {
		if sameInferredDomain(first.prop, d.prop) {
			continue
		}
		return fmt.Sprintf(
			"this view declares no record type, and %q is declared differently by two of them: %s declares it %s and %s declares it %s. knowledge_find will not split one name across two domains in an untyped query — it REFUSES the whole request, so a view carrying this clause imports clean and returns nothing but a refusal the first time anyone opens it. Give the view a record type, or rename the property in one of the two",
			name, first.recordType, describeInferredDecl(first.prop), d.recordType, describeInferredDecl(d.prop)), true
	}
	return "", false
}

// sameInferredDomain is knowledgefind's `sameUntypedDomain`, over this
// package's inferred declarations. Field for field, in the same order, so a
// reader can diff the two by eye.
func sameInferredDomain(a, b InferredProperty) bool {
	if a.Type != b.Type || a.Many != b.Many {
		return false
	}
	if a.Type == records.TypeRelation || a.Type == records.TypePerson {
		return a.To == b.To
	}
	return true
}

// describeInferredDecl renders one declaration the way the engine's own
// conflict refusal does, so the two messages read alike.
func describeInferredDecl(p InferredProperty) string {
	out := string(p.Type)
	if p.To != "" {
		out += " to " + p.To
	}
	if p.Many {
		out += " (many)"
	}
	return out
}
