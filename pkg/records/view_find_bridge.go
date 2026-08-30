// Omnipus — ADR-068 D15.3 / spec §4.1.2: translates a saved view (ViewDef,
// this package's own read-side format) into knowledge_find's own request
// shape, so `view: "name"` can resolve to a real, evaluable query.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"sort"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHY A TRANSLATION EXISTS AT ALL, AND WHY IT IS PARTIAL BY DESIGN
//
// A saved view (ViewDef) and a knowledge_find request (VaultFindRequest) are
// two independently designed wire formats that happen to describe
// overlapping queries. Most of a ViewDef translates mechanically —
// type/group_by/sort/limit/properties/aggregates are 1:1 in shape and, for
// the two enums involved (RecordSortDirection/VaultFindSortDirection,
// RecordAggregateOp/VaultFindAggregateOp), byte-identical in spelling.
//
// The FILTER half is not, and the gap is real rather than an oversight here:
//
//   - RecordFilterOp (a view's filter operator) is {contains, eq, gt, gte,
//     is_absent, lt, lte} — SEVEN values. VaultFilterNodeOp (find's filter
//     operator) is {=, <>, <, <=, >, >=, LIKE, IN, IS NULL, IS NOT NULL} —
//     TEN, entirely different spellings, and NO `contains`. A view filter
//     using `contains` (whole-element membership on a list, or substring on
//     text — spec §8 R-9/R-10) has no representation in find's grammar at
//     all; find's engine simply does not implement that comparison as a
//     leaf. Guessing a substitute (e.g. `LIKE` for text) would silently
//     change which records match — exactly the failure this whole surface
//     exists to prevent — so it is not guessed at.
//   - RecordFilter carries `via` (up to two relation hops PER LEAF); find's
//     VaultFilterNode leaf carries no `via` at all — find expresses
//     relation-scoped queries through `near`/`hops`/`join` at the REQUEST
//     level, a structurally different shape a per-leaf hop cannot be
//     mechanically rewritten into.
//
// So this translation is real for the filters it CAN carry over faithfully,
// and a view whose filter tree contains a `contains` clause or any `via` hop
// is reported UNTRANSLATABLE rather than partially translated (dropping the
// offending clause would silently broaden the result set, which is worse
// than refusing). See ViewFindLoader.Names/View below for what "reported"
// means given the ViewLoader interface's own (bool, no reason) shape.
// ---------------------------------------------------------------------------

// ViewFindLoader adapts a *ViewSet to knowledge_find's ViewLoader seam
// (pkg/records/knowledgefind.ViewLoader) — structurally, not by importing
// that package: knowledgefind already imports this one, and this package's
// own rule (doc.go: "depends on nothing else in Omnipus") stays true either
// way, since ViewLoader's shape is two ordinary methods.
type ViewFindLoader struct {
	views *ViewSet
}

// NewViewFindLoader wraps an already-loaded view set. A nil set behaves like
// an empty one — Names returns nothing and View always reports ok=false —
// which is the same "no saved views" shape LoadViews itself returns for a
// vault with no views directory.
func NewViewFindLoader(views *ViewSet) *ViewFindLoader {
	return &ViewFindLoader{views: views}
}

// Names lists the views this loader can actually SERVE through
// knowledge_find — i.e. every declared view MINUS any whose filter tree
// uses `contains` or `via`, which cannot be carried over (see this file's
// header). This is deliberately NOT every name ViewSet.Names() would report:
// knowledge_describe's own view listing is unfiltered (it is describing what
// the VAULT declares, not what one query surface can serve), so the two can
// legitimately disagree — a view knowledge_describe shows but
// knowledge_find's `view` argument reports as unknown is a real, narrower
// gap, and See View's doc comment for why that is the least-bad of the
// honest options available through this interface.
func (l *ViewFindLoader) Names() []string {
	if l == nil || l.views == nil {
		return nil
	}
	out := make([]string, 0, l.views.Len())
	for _, v := range l.views.Views() {
		if _, ok := translateView(v); ok {
			out = append(out, v.Name())
		}
	}
	sort.Strings(out)
	return out
}

// View resolves one saved view into knowledge_find's request shape.
//
// ok=false covers two DIFFERENT facts the interface cannot distinguish: the
// name is not declared at all, or it IS declared but its filter cannot be
// carried into find's grammar (see this file's header). Collapsing them is
// not a shortcut taken lightly — knowledgefind.ViewLoader is (name string)
// (generated.VaultFindRequest, bool), with no field for "exists but
// unsupported, and here is why". Reporting ok=true with the offending
// clause silently DROPPED would answer a broader question than the view
// asks, which is the one outcome every refusal in this surface exists to
// prevent; reporting ok=false is instead a real "this name is not offered
// here", together with a remedy the caller can act on (find.go's own
// refusal for an unknown view already names it: "call knowledge_describe
// include=views to see the saved views in scope" — which is where the
// FULL, untranslated list is visible, and "write the filter directly"
// remains available for exactly this case).
func (l *ViewFindLoader) View(name string) (generated.VaultFindRequest, bool) {
	if l == nil || l.views == nil {
		return generated.VaultFindRequest{}, false
	}
	v, ok := l.views.Get(name)
	if !ok {
		return generated.VaultFindRequest{}, false
	}
	return translateView(v)
}

// translateView performs the mechanical half unconditionally and the filter
// half only when every leaf translates.
func translateView(v *SavedView) (generated.VaultFindRequest, bool) {
	if v == nil {
		return generated.VaultFindRequest{}, false
	}
	def := v.Def

	req := generated.VaultFindRequest{
		Type: &def.Type,
	}
	if def.GroupBy != nil {
		gb := append([]string(nil), (*def.GroupBy)...)
		req.GroupBy = &gb
	}
	if def.Properties != nil {
		sel := append([]string(nil), (*def.Properties)...)
		req.Select = &sel
	}
	if def.Limit != nil {
		lim := *def.Limit
		req.Limit = &lim
	}
	if def.Sort != nil {
		sorts := make([]generated.VaultFindSort, 0, len(*def.Sort))
		for _, s := range *def.Sort {
			dir := generated.VaultFindSortDirection(s.Direction)
			sorts = append(sorts, generated.VaultFindSort{Property: s.Property, Direction: &dir})
		}
		req.Sort = &sorts
	}
	if def.Aggregates != nil {
		aggs := make([]generated.VaultFindAggregate, 0, len(*def.Aggregates))
		for _, a := range *def.Aggregates {
			aggs = append(aggs, generated.VaultFindAggregate{
				Op:       generated.VaultFindAggregateOp(a.Op),
				Property: a.Property,
			})
		}
		req.Aggregate = &aggs
	}

	if def.Filters == nil || len(*def.Filters) == 0 {
		return req, true
	}
	nodes := make([]generated.VaultFilterNode, 0, len(*def.Filters))
	for _, f := range *def.Filters {
		node, ok := translateRecordFilter(f)
		if !ok {
			// One untranslatable leaf makes the WHOLE view untranslatable —
			// a filter tree is a conjunction (ViewDef.Filters' own doc:
			// "combined with AND"), and silently dropping one AND-ed clause
			// broadens every record the view would otherwise have excluded.
			return generated.VaultFindRequest{}, false
		}
		nodes = append(nodes, node)
	}
	req.Filter = &generated.VaultFilterNode{All: &nodes}
	return req, true
}

// translateRecordFilter converts one ViewDef leaf into find's leaf grammar,
// or reports ok=false when the leaf uses `contains` or `via` (this file's
// header explains why neither has a faithful translation).
//
// THE NEGATE / INCLUDE_ABSENT TABLE THIS FUNCTION IMPLEMENTS, derived from
// each format's own documented semantics (RecordFilter.Negate/IncludeAbsent
// in contracts/components/schemas; find's own `not:` doc in
// pkg/records/knowledgefind/tool.go's Parameters()):
//
//   - is_absent, negate=false            -> {property, op: IS NULL}
//   - is_absent, negate=true             -> {property, op: IS NOT NULL}
//     (include_absent is documented exempt for is_absent at either polarity)
//   - eq/lt/lte/gt/gte, negate=false     -> {property, op: <mapped>, value}
//   - …, negate=true, include_absent=true (the RecordFilter default)
//     -> {not: {property, op: <mapped>, value}} — find's tree `not` already
//     re-includes an absent record at any depth (tool.go: "{not:{p,'=',v}}
//     to include records where p is absent"), which is the SAME re-inclusion
//     RecordFilter's own default performs.
//   - …, negate=true, include_absent=false (the explicit opt-out)
//     -> {property, op: <complement(mapped)>, value} — find's leaf-level
//     complement excludes an absent record exactly as SQL's own `<>`/`<=`/
//     etc. do over a NULL operand (tool.go: "{p,'<>',v} excludes them, as
//     it does in SQL"), which is what an explicit include_absent=false asks
//     for. eq's complement is `<>`; lt/gte and gt/lte are each other's.
func translateRecordFilter(f generated.RecordFilter) (generated.VaultFilterNode, bool) {
	if f.Via != nil && len(*f.Via) > 0 {
		return generated.VaultFilterNode{}, false
	}
	negate := f.Negate != nil && *f.Negate
	includeAbsent := f.IncludeAbsent == nil || *f.IncludeAbsent

	if f.Op == generated.IsAbsent {
		op := generated.ISNULL
		if negate {
			op = generated.ISNOTNULL
		}
		prop := f.Property
		return generated.VaultFilterNode{Property: &prop, Op: &op}, true
	}

	mapped, ok := recordFilterOpToFindOp(f.Op)
	if !ok {
		return generated.VaultFilterNode{}, false
	}
	value, ok := oneLexicalValue(f.Values)
	if !ok {
		return generated.VaultFilterNode{}, false
	}
	prop := f.Property

	if !negate {
		return generated.VaultFilterNode{Property: &prop, Op: &mapped, Value: &value}, true
	}
	if includeAbsent {
		leaf := generated.VaultFilterNode{Property: &prop, Op: &mapped, Value: &value}
		return generated.VaultFilterNode{Not: &leaf}, true
	}
	comp, ok := complementFindOp(mapped)
	if !ok {
		return generated.VaultFilterNode{}, false
	}
	return generated.VaultFilterNode{Property: &prop, Op: &comp, Value: &value}, true
}

// recordFilterOpToFindOp maps the five ordinary comparison ops that exist in
// BOTH vocabularies. `contains` is deliberately absent — see this file's
// header — and `is_absent` is handled by the caller before this is reached.
func recordFilterOpToFindOp(op generated.RecordFilterOp) (generated.VaultFilterNodeOp, bool) {
	switch op {
	case generated.Eq:
		return generated.Equal, true
	case generated.Lt:
		return generated.LessThan, true
	case generated.Lte:
		return generated.LessThanEqual, true
	case generated.Gt:
		return generated.GreaterThan, true
	case generated.Gte:
		return generated.GreaterThanEqual, true
	default:
		return "", false
	}
}

// complementFindOp is the SQL-style complement used when a view's negated
// clause explicitly opts OUT of FR-008's absence re-inclusion
// (include_absent: false) — see translateRecordFilter's table.
func complementFindOp(op generated.VaultFilterNodeOp) (generated.VaultFilterNodeOp, bool) {
	switch op {
	case generated.Equal:
		return generated.LessThanGreaterThan, true
	case generated.LessThan:
		return generated.GreaterThanEqual, true
	case generated.LessThanEqual:
		return generated.GreaterThan, true
	case generated.GreaterThan:
		return generated.LessThanEqual, true
	case generated.GreaterThanEqual:
		return generated.LessThan, true
	default:
		return "", false
	}
}

// oneLexicalValue reads the SINGLE value every op this file translates
// requires (RecordFilter.Values' own doc: "exactly one for every operator"
// other than is_absent, which never reaches here). More or fewer than one is
// a malformed view — LoadViews' own validation should have already caught
// it, but this function does not trust that and refuses to guess rather
// than silently reading the wrong element.
func oneLexicalValue(values *[]generated.RecordValue) (string, bool) {
	if values == nil || len(*values) != 1 {
		return "", false
	}
	return recordValueLexical((*values)[0])
}

// recordValueLexical renders one typed RecordValue in the LEXICAL form
// find's VaultFilterNode.Value expects — "the same text a frontmatter file
// would hold" (VaultFilterNode's own doc). Every scalar kind is already
// carried on the wire as its lexical string; a relation/person value's
// lexical form is its wikilink text without brackets (RecordRef.Link's own
// doc: "the durable, human-editable form"), which is exactly what a
// frontmatter file holds for one too.
func recordValueLexical(v generated.RecordValue) (string, bool) {
	switch {
	case v.Text != nil:
		return *v.Text, true
	case v.Enum != nil:
		return *v.Enum, true
	case v.Integer != nil:
		return *v.Integer, true
	case v.Decimal != nil:
		return *v.Decimal, true
	case v.Date != nil:
		return *v.Date, true
	case v.Person != nil:
		return v.Person.Link, true
	case v.Relation != nil:
		return v.Relation.Link, true
	default:
		return "", false
	}
}
