// Omnipus — ADR-068 D15.3 / spec §4.1.2: translates a saved view (ViewDef,
// this package's own read-side format) into knowledge_find's own request
// shape, so `view: "name"` can resolve to a real, evaluable query.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"sort"
	"strings"

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
//
// ---------------------------------------------------------------------------
// SCHEMA_VERSION 2 CHANGED WHICH HALF IS HARD, AND IT DID NOT CHANGE THIS
// FILE'S ANSWER FOR VERSION 1 (spec FR-018b, review finding F5).
//
// A version-2 view's `filter` IS a VaultFilterNode tree — the same type,
// the same ten operators, the same combinators find already evaluates — so
// the filter half of a v2 view is a DEEP COPY, not a translation. Nothing
// is mapped, so nothing can be mapped wrongly.
//
// A version-1 view keeps the partial translation above, UNCHANGED, and the
// temptation this file exists to refuse is to close the gap by translating
// v1's `contains` into v2's `LIKE '%…%'` now that both live in one loader.
// Spec Draft 10 specified exactly that and Draft 11 withdrew it:
//
//	`labels contains "in"` matches the ELEMENT `in`.
//	`labels LIKE '%in%'`   also matches `indoor`, `printing` and `min`.
//
// That is BROADENING — more rows, silently, on a file the operator wrote
// years ago and has not looked at — applied automatically, in the revision
// that made broadening the one prohibited thing (FR-105). A v1 `contains`
// view therefore stays exactly as it is: loaded, listed by
// knowledge_describe, NOT served here, and the reason NAMED (see
// ViewServeRefusal below). It becomes servable when an operator explicitly
// migrates it through knowledge_configure — a decision a person makes with
// the widening in front of them, which is the whole difference.
//
// DO NOT ADD A `contains` CASE TO recordFilterOpToFindOp.
// TestViewFindLoader_V1ContainsIsNeverBroadenedToLike is the guard, and it
// asserts the widening by example over `in`/`indoor`.
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
		if _, refusal := translateView(v); refusal == nil {
			out = append(out, v.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// UNSERVABLE, WITH A REASON
//
// ViewLoader.View is (VaultFindRequest, bool) — no field for "this view
// exists and here is why it cannot be served". FR-018b requires the reason
// to be NAMED, so it is carried here, beside the interface rather than
// through it: knowledge_describe and knowledge_configure hold a
// *ViewFindLoader and can ask.
//
// This is deliberately NOT a silent difference between two lists. The gap
// between what knowledge_describe shows and what knowledge_find will serve
// is real and it is the DESIGNED outcome of refusing to broaden — so it is
// made legible rather than left for an operator to infer from a view that
// is listed in one place and unknown in another.
// ---------------------------------------------------------------------------

// ViewServeRefusalCode names WHY a loaded view cannot be served through
// knowledge_find.
type ViewServeRefusalCode string

const (
	// ServeRefusalUnknownView — no view of that name is loaded.
	ServeRefusalUnknownView ViewServeRefusalCode = "unknown_view"
	// ServeRefusalV1Contains — a version-1 `contains` leaf. THE reason this
	// whole refusal vocabulary exists: `contains` is whole-element membership
	// (or substring on text), and find's nearest neighbour, `LIKE '%…%'`, is
	// substring matching over the joined value. Substituting one for the
	// other would make `labels contains "in"` newly match `indoor`,
	// `printing` and `min` — more rows than the saved view ever asked for.
	ServeRefusalV1Contains ViewServeRefusalCode = "v1_contains_not_translatable"
	// ServeRefusalV1Via — a version-1 `via` hop. Find expresses relation
	// scope at the REQUEST level (`near`/`hops`/`join`), a structurally
	// different shape a per-leaf hop cannot be mechanically rewritten into.
	ServeRefusalV1Via ViewServeRefusalCode = "v1_via_not_translatable"
	// ServeRefusalV1Operand — a version-1 leaf whose operand list is not the
	// single value its operator requires, or whose value carries no
	// recognised type tag.
	ServeRefusalV1Operand ViewServeRefusalCode = "v1_operand_not_translatable"
	// ServeRefusalDisabled — FR-105: the view is stored but MUST NOT be
	// applied, because something in a row-set-affecting position could not be
	// translated when it was imported.
	ServeRefusalDisabled ViewServeRefusalCode = "view_disabled"
	// ServeRefusalGroupDirection — a version-2 `grouping` key asking for
	// descending group order. VaultFindRequest.group_by is a bare []string
	// with no direction field, so serving this view would flatten the
	// direction to ascending IN SILENCE — which is the precise failure
	// ViewGroupBy was added to end (24 of them in the founder's own vault).
	// Refused instead, until find's request carries a direction.
	ServeRefusalGroupDirection ViewServeRefusalCode = "group_direction_not_representable"
	// ServeRefusalFormula — a version-2 view declaring `formulas`.
	// VaultFindRequest has no formulas key, so find would resolve every
	// `formula.<name>` against nothing.
	ServeRefusalFormula ViewServeRefusalCode = "formula_not_representable"
)

// ViewServeRefusal is one loaded-but-unservable view.
type ViewServeRefusal struct {
	// Name is the view as knowledge_describe lists it.
	Name string
	Code ViewServeRefusalCode
	// Reason states what cannot be carried, in the operator's own vocabulary.
	Reason string
	// Remedy is what to do about it. Always populated: a refusal with no
	// remedy is a dead end, and the remedy here is never "give up" — the
	// filter can always be written directly.
	Remedy string
}

// String renders a refusal as one reviewable line.
//
// Deliberately not Error(), matching ViewRejection.String: this is a REPORT
// ENTRY, and an Error() method would let it be returned as a bare error and
// lose its Code and Name on the way.
func (r ViewServeRefusal) String() string {
	return fmt.Sprintf("%s: %s (%s) — %s", r.Name, r.Reason, r.Code, r.Remedy)
}

// ServeRefusal explains why one view is not served here.
//
// ok=false means the view IS servable, or there is no such view — the two are
// distinguished by asking l.views.Get, which is what the caller already has.
func (l *ViewFindLoader) ServeRefusal(name string) (ViewServeRefusal, bool) {
	if l == nil || l.views == nil {
		return ViewServeRefusal{}, false
	}
	v, ok := l.views.Get(name)
	if !ok {
		return ViewServeRefusal{}, false
	}
	_, refusal := translateView(v)
	if refusal == nil {
		return ViewServeRefusal{}, false
	}
	return *refusal, true
}

// Unservable lists every loaded view this loader will not serve, with its
// reason, sorted by view name.
//
// This is the list an operator needs to retire their unmigrated v1 views
// deliberately — the "health view" FR-018b names as the standing cost of
// keeping two vocabularies alive.
func (l *ViewFindLoader) Unservable() []ViewServeRefusal {
	if l == nil || l.views == nil {
		return nil
	}
	var out []ViewServeRefusal
	for _, v := range l.views.Views() {
		if _, refusal := translateView(v); refusal != nil {
			out = append(out, *refusal)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
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
	req, refusal := translateView(v)
	if refusal != nil {
		return generated.VaultFindRequest{}, false
	}
	return req, true
}

// translateView dispatches on the view's DECLARED schema_version.
//
// The dispatch is on the version and nothing else — never on "does it happen
// to have a `filter` key". ParseView already refuses a file that mixes the
// two vocabularies, and a loader that sniffed keys instead would quietly
// serve whichever half it noticed first on a SavedView some other code
// constructed.
func translateView(v *SavedView) (generated.VaultFindRequest, *ViewServeRefusal) {
	if v == nil {
		return generated.VaultFindRequest{}, &ViewServeRefusal{
			Code:   ServeRefusalUnknownView,
			Reason: "there is no such view",
			Remedy: "call knowledge_describe include=views to see the saved views in scope",
		}
	}
	def := v.Def

	// FR-105, before anything else: an imported view whose untranslatable
	// expression sat in a ROW-SET-AFFECTING position is stored DISABLED, and
	// applying it is refused naming that expression. Checked first because it
	// is true regardless of which version the file declares, and because a
	// disabled view that happened to translate cleanly would otherwise be
	// served — the one outcome `disabled` exists to prevent.
	if def.Disabled != nil && *def.Disabled {
		reason := fmt.Sprintf("view %q is stored disabled: something in a filter could not be translated when it was imported, and applying it anyway would return more rows than the original", def.Name)
		if def.Untranslated != nil && len(*def.Untranslated) > 0 {
			reason += ", specifically: " + strings.Join(*def.Untranslated, "; ")
		}
		return generated.VaultFindRequest{}, &ViewServeRefusal{
			Name:   def.Name,
			Code:   ServeRefusalDisabled,
			Reason: reason,
			Remedy: "rewrite the named expression through knowledge_configure, or write the filter directly",
		}
	}

	req := translateViewMechanical(def)
	if def.SchemaVersion == ViewVersion2 {
		return translateViewV2(def, req)
	}
	return translateViewV1(def, req)
}

// translateViewMechanical is the half that is 1:1 in BOTH versions: type,
// display selection, limit, sort and aggregates. Every one of them is copied
// by VALUE.
//
// The copying is not defensive habit. VaultFindRequest and ViewDef both carry
// pointers, and handing the request a pointer into the view's own struct
// would let a later mutation of the request rewrite the SAVED VIEW held in
// the ViewSet — a query silently editing the thing it queried.
func translateViewMechanical(def generated.ViewDef) generated.VaultFindRequest {
	req := generated.VaultFindRequest{}
	if def.Type != nil {
		viewType := *def.Type
		req.Type = &viewType
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
			var prop *string
			if a.Property != nil {
				p := *a.Property
				prop = &p
			}
			aggs = append(aggs, generated.VaultFindAggregate{
				Op:       generated.VaultFindAggregateOp(a.Op),
				Property: prop,
			})
		}
		req.Aggregate = &aggs
	}
	return req
}

// translateViewV1 is the ORIGINAL translation, unchanged by schema_version 2.
//
// It reads `filters` (the flat AND-list) and `group_by` (the bare name list),
// and it refuses `contains` and `via` exactly as it always did. Read this
// file's header before altering a line of it.
func translateViewV1(def generated.ViewDef, req generated.VaultFindRequest) (generated.VaultFindRequest, *ViewServeRefusal) {
	if def.GroupBy != nil {
		gb := append([]string(nil), (*def.GroupBy)...)
		req.GroupBy = &gb
	}
	if def.Filters == nil || len(*def.Filters) == 0 {
		return req, nil
	}
	nodes := make([]generated.VaultFilterNode, 0, len(*def.Filters))
	for i, f := range *def.Filters {
		node, refusal := translateRecordFilter(f, def.Name, i+1)
		if refusal != nil {
			// One untranslatable leaf makes the WHOLE view untranslatable —
			// a filter tree is a conjunction (ViewDef.Filters' own doc:
			// "combined with AND"), and silently dropping one AND-ed clause
			// broadens every record the view would otherwise have excluded.
			return generated.VaultFindRequest{}, refusal
		}
		nodes = append(nodes, node)
	}
	req.Filter = &generated.VaultFilterNode{All: &nodes}
	return req, nil
}

// translateViewV2 carries a version-2 view across, and the filter half is a
// DEEP COPY rather than a translation: a v2 `filter` already IS find's tree.
//
// TWO THINGS STILL DO NOT FIT THROUGH THIS SEAM, and both are refused with a
// reason rather than dropped:
//
//   - a `grouping` key asking for DESCENDING group order —
//     VaultFindRequest.group_by is a bare []string, so the direction would be
//     flattened to ascending in silence, which is exactly the loss
//     ViewGroupBy exists to end. Ascending IS the documented default, so an
//     ascending key (or one with no direction) crosses losslessly.
//   - `formulas` — VaultFindRequest has no formulas key at all, so every
//     `formula.<name>` in a filter, a sort or a selection would resolve
//     against nothing.
//
// Both are seams in VaultFindRequest, not defects in the view. They close by
// giving find's request a directional group key and a formulas map.
func translateViewV2(def generated.ViewDef, req generated.VaultFindRequest) (generated.VaultFindRequest, *ViewServeRefusal) {
	if def.Formulas != nil && len(*def.Formulas) > 0 {
		return generated.VaultFindRequest{}, &ViewServeRefusal{
			Name: def.Name,
			Code: ServeRefusalFormula,
			Reason: fmt.Sprintf("view %q declares %d formula(s), and a knowledge_find request carries no formulas: every `formula.<name>` it names would resolve against nothing",
				def.Name, len(*def.Formulas)),
			Remedy: "write the expression's effect as an ordinary filter, or query the view's underlying type directly",
		}
	}
	if def.Grouping != nil {
		names := make([]string, 0, len(*def.Grouping))
		for _, g := range *def.Grouping {
			if g.Direction != nil && *g.Direction == generated.ViewGroupByDirectionDesc {
				return generated.VaultFindRequest{}, &ViewServeRefusal{
					Name: def.Name,
					Code: ServeRefusalGroupDirection,
					Reason: fmt.Sprintf("view %q groups by %q in descending order, and a knowledge_find request's `group_by` carries no direction: serving it would silently reorder the groups ascending",
						def.Name, g.Property),
					Remedy: "group ascending, or write the query directly and order the groups yourself",
				}
			}
			names = append(names, g.Property)
		}
		if len(names) > 0 {
			req.GroupBy = &names
		}
	}
	if def.Filter != nil {
		req.Filter = cloneFilterNode(def.Filter)
	}
	return req, nil
}

// cloneFilterNode deep-copies a filter tree.
//
// EVERY pointer and slice is reallocated, to the leaves. A shallow copy would
// hand the request the view's own child slices, so a request the engine later
// normalises in place would rewrite the SAVED VIEW — and the next query
// against that view would silently ask a different question, with the file on
// disk still saying the original.
func cloneFilterNode(n *generated.VaultFilterNode) *generated.VaultFilterNode {
	if n == nil {
		return nil
	}
	out := generated.VaultFilterNode{}
	if n.All != nil {
		children := make([]generated.VaultFilterNode, 0, len(*n.All))
		for i := range *n.All {
			children = append(children, *cloneFilterNode(&(*n.All)[i]))
		}
		out.All = &children
	}
	if n.Any != nil {
		children := make([]generated.VaultFilterNode, 0, len(*n.Any))
		for i := range *n.Any {
			children = append(children, *cloneFilterNode(&(*n.Any)[i]))
		}
		out.Any = &children
	}
	if n.Not != nil {
		out.Not = cloneFilterNode(n.Not)
	}
	if n.Property != nil {
		p := *n.Property
		out.Property = &p
	}
	if n.Op != nil {
		op := *n.Op
		out.Op = &op
	}
	if n.Value != nil {
		val := *n.Value
		out.Value = &val
	}
	if n.Values != nil {
		vals := append([]string(nil), (*n.Values)...)
		out.Values = &vals
	}
	return &out
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
func translateRecordFilter(f generated.RecordFilter, viewName string, position int) (generated.VaultFilterNode, *ViewServeRefusal) {
	if f.Via != nil && len(*f.Via) > 0 {
		return generated.VaultFilterNode{}, &ViewServeRefusal{
			Name: viewName,
			Code: ServeRefusalV1Via,
			Reason: fmt.Sprintf("view %q follows relation %s in filter %d, and a knowledge_find filter leaf carries no `via`: find scopes by relation at the request level (near/hops/join), which a per-leaf hop cannot be mechanically rewritten into",
				viewName, strings.Join(derefStrings(f.Via), " → "), position),
			Remedy: "express the hop as the request's own join, or migrate the view to schema_version 2 through knowledge_configure",
		}
	}
	negate := f.Negate != nil && *f.Negate
	includeAbsent := f.IncludeAbsent == nil || *f.IncludeAbsent

	if f.Op == generated.IsAbsent {
		op := generated.ISNULL
		if negate {
			op = generated.ISNOTNULL
		}
		prop := f.Property
		return generated.VaultFilterNode{Property: &prop, Op: &op}, nil
	}

	// THE ONE OPERATOR WITH NO FAITHFUL TARGET. `contains` is whole-element
	// membership on a list and substring on text (spec §8 R-9/R-10); find's
	// grammar has no such leaf. The obvious substitute, `LIKE '%…%'`, is
	// SUBSTRING matching over the joined value, so it returns a SUPERSET —
	// `labels contains "in"` would newly match `indoor`, `printing` and `min`.
	// Broadening a saved query on the operator's behalf is prohibited
	// (FR-105), so the view is refused with the widening named.
	if f.Op == generated.Contains {
		return generated.VaultFilterNode{}, &ViewServeRefusal{
			Name: viewName,
			Code: ServeRefusalV1Contains,
			Reason: fmt.Sprintf("view %q uses `contains` on %q in filter %d, and knowledge_find has no `contains` leaf; the nearest operator, LIKE '%%…%%', matches SUBSTRINGS, so it would return rows this view excludes (`contains \"in\"` matches the element `in`; LIKE also matches `indoor` and `printing`)",
				viewName, f.Property, position),
			Remedy: "migrate the view through knowledge_configure, which asks whether you meant whole-element membership (`=`, and it will say that folding widens case) or a genuine pattern (`LIKE`) — or write the filter directly",
		}
	}

	mapped, ok := recordFilterOpToFindOp(f.Op)
	if !ok {
		return generated.VaultFilterNode{}, &ViewServeRefusal{
			Name: viewName,
			Code: ServeRefusalV1Operand,
			Reason: fmt.Sprintf("view %q uses operator %q on %q in filter %d, which has no equivalent in the knowledge_find grammar",
				viewName, string(f.Op), f.Property, position),
			Remedy: "write the filter directly, or migrate the view through knowledge_configure",
		}
	}
	value, ok := oneLexicalValue(f.Values)
	if !ok {
		return generated.VaultFilterNode{}, &ViewServeRefusal{
			Name: viewName,
			Code: ServeRefusalV1Operand,
			Reason: fmt.Sprintf("view %q compares %q in filter %d against something other than the single typed value operator %q requires",
				viewName, f.Property, position, string(f.Op)),
			Remedy: "repair the view file, or write the filter directly",
		}
	}
	prop := f.Property

	if !negate {
		return generated.VaultFilterNode{Property: &prop, Op: &mapped, Value: &value}, nil
	}
	if includeAbsent {
		leaf := generated.VaultFilterNode{Property: &prop, Op: &mapped, Value: &value}
		return generated.VaultFilterNode{Not: &leaf}, nil
	}
	comp, ok := complementFindOp(mapped)
	if !ok {
		return generated.VaultFilterNode{}, &ViewServeRefusal{
			Name: viewName,
			Code: ServeRefusalV1Operand,
			Reason: fmt.Sprintf("view %q negates %q in filter %d with include_absent false, and operator %q has no leaf-level complement",
				viewName, f.Property, position, string(f.Op)),
			Remedy: "drop include_absent: false, or write the filter directly",
		}
	}
	return generated.VaultFilterNode{Property: &prop, Op: &comp, Value: &value}, nil
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
