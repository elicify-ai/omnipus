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
// THE FILTER HALF IS A COPY, NOT A TRANSLATION — AND THAT IS THE POINT
//
// A saved view (ViewDef) and a knowledge_find request (VaultFindRequest) are
// two wire formats describing overlapping queries. Most of a ViewDef carries
// over mechanically — type/sort/limit/properties/aggregates are 1:1 in shape
// and, for the two enums involved (RecordSortDirection/VaultFindSortDirection,
// RecordAggregateOp/VaultFindAggregateOp), byte-identical in spelling.
//
// A view's `filter` IS a VaultFilterNode tree: the same type, the same ten
// operators, the same combinators find already evaluates. So the filter half
// is a DEEP COPY. Nothing is mapped, so nothing can be mapped wrongly.
//
// IT USED TO BE A PARTIAL TRANSLATION, and the reason that is worth knowing is
// that the hazard is now structurally absent rather than merely guarded
// against. The retired flat view format spoke a SEPARATE seven-operator
// vocabulary {contains, eq, gt, gte, is_absent, lt, lte} against find's ten
// {=, <>, <, <=, >, >=, LIKE, IN, IS NULL, IS NOT NULL}, and two of its leaves
// had no faithful target at all:
//
//   - `contains` — whole-element membership on a list, substring on text
//     (spec §8 R-9/R-10). Find has no such leaf. The nearest neighbour,
//     `LIKE '%…%'`, is substring matching over the joined value:
//
//	`labels contains "in"` matches the ELEMENT `in`.
//	`labels LIKE '%in%'`   also matches `indoor`, `printing` and `min`.
//
//     That is BROADENING, and broadening a saved query on the operator's
//     behalf is the one thing this surface may never do (FR-105). Spec Draft
//     10 specified exactly that translation; Draft 11 withdrew it as review
//     finding F5, and the view was refused with the widening named instead.
//   - a per-leaf `via` relation hop — find scopes by relation at the REQUEST
//     level (`near`/`hops`/`join`), a structurally different shape.
//
// THE PROHIBITION SURVIVES; THE FORMAT THAT COULD VIOLATE IT DOES NOT. There
// is no longer a second operator vocabulary in a view, so there is no mapping
// step in which a `contains` could be widened into a `LIKE`. Do not reintroduce
// one: an operator-to-operator map in this file is the shape of the defect.
// FR-105 is still enforced where a rewrite can still happen — knowledge_configure,
// which refuses any migration that changes the row set.
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
// knowledge_find — i.e. every declared view MINUS the ones a
// ViewServeRefusal covers: a view stored `disabled`, and one grouping in
// descending order.
// This is deliberately NOT every name ViewSet.Names() would report:
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
	// ServeRefusalDisabled — FR-105: the view is stored but MUST NOT be
	// applied, because something in a row-set-affecting position could not be
	// translated when it was imported.
	ServeRefusalDisabled ViewServeRefusalCode = "view_disabled"
	// ServeRefusalGroupDirection — a `grouping` key asking for
	// descending group order. VaultFindRequest.group_by is a bare []string
	// with no direction field, so serving this view would flatten the
	// direction to ascending IN SILENCE — which is the precise failure
	// ViewGroupBy was added to end (24 of them in the founder's own vault).
	// Refused instead, until find's request carries a direction.
	ServeRefusalGroupDirection ViewServeRefusalCode = "group_direction_not_representable"
	// ServeRefusalFormula is RETIRED and nothing emits it.
	//
	// It named the refusal of every view declaring `formulas`, on the premise
	// that VaultFindRequest has no formulas key. The premise is still true;
	// the refusal was still wrong — the formulas reach knowledgefind through
	// the LOADER (ViewFormulaLoader, satisfied by ViewFindLoader.Formulas)
	// rather than through the request, so there was never anything to carry.
	//
	// The CONSTANT is kept, and deliberately: it is a wire-visible code that
	// has appeared in operator-facing refusal text, and a reader searching for
	// why a view was once unservable should land on this explanation rather
	// than on nothing. TestServeRefusalFormula_IsNeverEmitted pins that no
	// code path produces it.
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
// This is the "health view": the list an operator reads to see which saved
// views cannot currently be run, and why. Every entry is a seam in
// VaultFindRequest rather than a defect in the view — a stored `disabled`
// flag, a descending grouping, a declared formula — so the list shrinks as
// find's request grows, not as views are rewritten.
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

// Formulas returns one view's `formulas:` map as SOURCE TEXT, satisfying
// knowledgefind.ViewFormulaLoader.
//
// IT IS THE OTHER HALF OF DELETING THE FORMULA REFUSAL, and the two are one
// change. knowledgefind asks its loader for formulas by TYPE ASSERTION — a
// loader that does not implement the interface is read as "this vault has no
// formulas", silently and correctly. So a bridge that stopped refusing
// formula-bearing views without also implementing this method would serve them
// with every `formula.<name>` resolving against nothing: the view would run,
// return rows, and quietly answer a different question. That is strictly worse
// than the refusal it replaced, which is why the seam test guarding this pair
// checked for BOTH halves before it was deleted.
//
// ok=false means there is no such view, which is what lets knowledgefind tell
// "the view defines no formulas" (an empty map) apart from "there is no view"
// without a second lookup.
func (l *ViewFindLoader) Formulas(name string) (map[string]string, bool) {
	if l == nil || l.views == nil {
		return nil, false
	}
	v, ok := l.views.Get(name)
	if !ok || v == nil {
		return nil, false
	}
	if v.Def.Formulas == nil {
		return map[string]string{}, true
	}
	// COPIED, for the reason cloneFilterNode gives: handing out the view's own
	// map would let a caller that mutates it rewrite the SAVED view held in the
	// ViewSet, and the next query would silently ask a different question.
	out := make(map[string]string, len(*v.Def.Formulas))
	for k, src := range *v.Def.Formulas {
		out[k] = src
	}
	return out, true
}

// translateView carries a saved view across to a knowledge_find request, or
// reports why it cannot be served.
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
	// applying it is refused naming that expression. Checked first because a
	// disabled view that happened to carry over cleanly would otherwise be
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
	return translateViewQuery(def, req)
}

// translateViewMechanical is the half that is 1:1: type, display selection,
// limit, sort and aggregates. Every one of them is copied by VALUE.
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

// translateViewQuery carries the QUERY half across — grouping and the filter
// tree. The filter is a DEEP COPY rather than a translation: a view's `filter`
// already IS find's tree.
//
// ONE THING STILL DOES NOT FIT THROUGH THIS SEAM, and it is refused with a
// reason rather than dropped: a `grouping` key asking for DESCENDING group
// order. VaultFindRequest.group_by is a bare []string, so the direction would
// be flattened to ascending in silence, which is exactly the loss ViewGroupBy
// exists to end. Ascending IS the documented default, so an ascending key (or
// one with no direction) crosses losslessly. That is a seam in
// VaultFindRequest, not a defect in the view; it closes by giving find's
// request a directional group key.
//
// `formulas` USED TO BE THE SECOND ONE, AND IT IS NOT ANY MORE. This function
// refused every view declaring a formula, on the grounds that
// VaultFindRequest has no formulas key. The premise was true and the
// conclusion did not follow: the request never needed one, because the
// formulas travel BESIDE the request. knowledgefind reads them from the
// LOADER (its optional ViewFormulaLoader interface, satisfied by Formulas
// below) and validates them into the query's namespace, where every
// `formula.<name>` resolves against a real declaration.
//
// The cost of the old refusal was not theoretical. It made the importer's
// whole formula capability unreachable: a `.base` file's `formulas:` block
// could be translated perfectly and written into a view file, and that view
// would then be dropped from Names(), reported unknown by the `view`
// argument, and unservable — trading a named missing column for an outright
// refusal.
func translateViewQuery(def generated.ViewDef, req generated.VaultFindRequest) (generated.VaultFindRequest, *ViewServeRefusal) {
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
