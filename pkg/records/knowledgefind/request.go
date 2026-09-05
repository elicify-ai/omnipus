// Omnipus — ADR-068 D15.3 / spec 4.1.2: the parameters, and every refusal that
// is not an empty result.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// Bounds and defaults. Each is stated once, here, so the refusal message and the
// enforcement can never quote different numbers.
const (
	// DefaultLimit is the page size when the caller names none.
	DefaultLimit = 50
	// MaxLimit is the cap. Above it the request is CLAMPED and the clamp is
	// REPORTED (FR-063) — never silently truncated.
	MaxLimit = 200
	// MaxHops is FR-065's ceiling. A third hop is refused, not walked.
	MaxHops = 2
	// MaxGroupLevels is FR-027's.
	MaxGroupLevels = 2

	// MaxFilterLeaves and MaxFilterDepth are FR-023c, the bound on the ONE
	// input whose cost multiplies against the candidate bound. They were
	// enforced on a saved VIEW (records/view.go's measureViewFilterTree) and on
	// nothing else, so a caller-supplied 400-leaf tree over 40,000 candidates
	// passed B1 and then ran 16M comparisons where the requirement budgets
	// 3.2M. The numbers are the view's numbers, because a view and a request
	// are the same tree evaluated by the same comparator.
	MaxFilterLeaves = 64
	MaxFilterDepth  = 8
)

// AcceptedParameters is the closed set of argument names, in the order the tool
// schema declares them. FR-022c refuses an undeclared parameter BY NAME with
// this list attached, rather than ignoring it — a silently dropped argument is
// how a caller comes to believe a constraint was applied that never was.
var AcceptedParameters = []string{
	"words", "type", "kind", "filter", "view", "near", "hops", "join",
	"group_by", "sort", "select", "aggregate", "explain", "limit", "cursor", "detail",
}

// Kind values.
//
// `record` narrows `note` to the notes that DECLARE a record type — the rows
// whose record_type is non-empty. It used to be a synonym that narrowed
// NOTHING: it mapped to the same propindex.KindNote as `note`, left
// Selector.RecordType empty, and had no consumer anywhere in the tree, while
// the kind=task refusal actively steered callers to it. Advertised and inert is
// the one outcome that is not acceptable, so it narrows.
//
// The narrowing is applied in Go, over the candidate's own RecordType
// (evaluation.visit), NOT by a Selector field: ruling R-A keeps the store told
// only a record type, a note kind and a path prefix, and "any declared type" is
// not one of the three.
//
// The store's own kind column only ever holds note or attachment.
const (
	KindNote       = "note"
	KindRecord     = "record"
	KindTask       = "task"
	KindAttachment = "attachment"
)

// query is the validated, defaulted form of a request — what actually ran.
//
// It exists separately from the wire type because the wire type carries what the
// CALLER SENT and this carries what was EXECUTED, and FR-122 requires the second
// to be echoed. Collapsing them would make a clamp invisible, which is the
// requirement's whole subject.
type query struct {
	words      string
	recordType string
	kind       string
	schema     *records.Schema
	filter     *node
	view       string
	near       string
	hops       int
	join       []string
	groupBy    []groupSpec
	sort       []sortKey
	selectCols []string
	aggregates []aggregate
	explain    bool
	limit      int
	limitAsked int
	clamped    bool
	cursor     string
	minimal    bool
	// renderRows is Deps.RenderRows, carried so applyLimit and the budget can
	// both see it. Zero is every tool call.
	renderRows int

	// resolve maps a wikilink to a record identity — D5.1's wikilink -> file
	// -> record id, the SAME seam the comparator's R-8 comparisons run
	// through (compare_oracle.go's Comparator.ResolveRelation). Grouping by a
	// relation (FR-029) needs it too: D5/R-8's rule is that a relation
	// compares by target identity, never by display text, and grouping is a
	// form of equality — two spellings of the same target (an alias, or a
	// stale wikilink not yet rewritten by ADR-067 D10's rename) must land in
	// ONE group, not fork into two. Set once, in Find, from Deps.Resolve; a
	// nil value is legal and degrades relation grouping to the folded raw
	// link text, which is worse but not silent (see project.go's groupKeys).
	resolve records.RelationResolver

	// touched is every property the query names, in first-mention order. It is
	// what `explain` reports and what the schema check ranges over.
	touched []string

	// ns is the three namespaces this query resolves names against — the
	// record type's own schema, FR-130's `file.*` and the view's `formula.*`.
	// It is built once, in parse, and every property position reads it.
	ns *namespace

	// set is every record type in scope. It is what the UNTYPED namespace
	// resolves an ordinary property name against (FR-018b): with no `type`
	// there is no single schema to ask, so the question becomes "which in-scope
	// types declare this name, and do they agree".
	set *records.SchemaSet
}

type sortKey struct {
	property string
	desc     bool
	prop     *records.Property
}

// groupSpec is ONE validated grouping key — the property, the direction the
// GROUPS run in, and the declared property the direction is interpreted
// against.
//
// It carries the same three fields as sortKey and for the same reason: the
// direction is meaningless without the declared type. `desc` over an `enum`
// reverses a LEXICAL order (R-5/R-E: an enum has no declared-position ordinal,
// and records.comparisonDomain maps it onto text), while `desc` over an
// `integer` or a `date` reverses a NATURAL one. Grouping used to be a bare
// []string, which is exactly why a view that recorded `direction: desc` could
// not be served at all (records.ServeRefusalGroupDirection): there was nowhere
// in the executed query to put the answer, so refusing was the only honest
// option left.
type groupSpec struct {
	property string
	desc     bool
	prop     *records.Property
}

type aggregate struct {
	op       string
	property string
	prop     *records.Property
}

// parse validates a request completely BEFORE anything is retrieved (FR-023).
//
// Every failure here returns a *RefusalError carrying the remedy. None of them
// returns an empty result: "no matches" and "you spelled it wrong" are
// indistinguishable to a caller, and the second is far more common.
func parse(req generated.VaultFindRequest, set *records.SchemaSet, formulas map[string]string, renderRows int) (*query, *RefusalError) {
	q := &query{
		kind:       KindNote,
		limit:      DefaultLimit,
		set:        set,
		renderRows: renderRows,
	}

	// A PRESENT-BUT-BLANK `type` or `kind` never reaches here: Find refuses it
	// before a saved view is expanded (checkBlankNarrowing). That ordering is
	// load-bearing rather than tidy — applyView gates on `req.Type == nil`, so
	// a blank string is non-nil, discards the view's own `type:` and then
	// resolves to no schema, running the view's filter against every note in
	// the vault and presenting it as the view's answer.
	if req.Kind != nil {
		q.kind = string(*req.Kind)
		switch q.kind {
		case KindNote, KindRecord, KindTask, KindAttachment:
		default:
			return nil, refuse(problem(generated.UnsupportedParameter,
				fmt.Sprintf("%q is not a kind of row this vault holds", q.kind),
				"use one of: "+strings.Join([]string{KindNote, KindRecord, KindTask, KindAttachment}, ", ")), nil)
		}
	}
	if req.Words != nil {
		q.words = strings.TrimSpace(*req.Words)
	}
	if req.View != nil {
		q.view = strings.TrimSpace(*req.View)
	}
	if req.Near != nil {
		q.near = strings.TrimSpace(*req.Near)
	}
	if req.Explain != nil {
		q.explain = *req.Explain
	}
	if req.Cursor != nil {
		q.cursor = *req.Cursor
	}
	if req.Detail != nil {
		q.minimal = string(*req.Detail) == "minimal"
	}

	if r := q.applyHops(req); r != nil {
		return nil, r
	}
	if r := q.applyLimit(req); r != nil {
		return nil, r
	}
	if r := q.resolveType(req, set); r != nil {
		return nil, r
	}
	// THE NAMESPACE IS BUILT BETWEEN THE TYPE AND THE FIRST PROPERTY POSITION,
	// and the order is forced: a formula's static type (FR-143a) is inferred
	// against the record type's schema, so the type has to be resolved first —
	// and every property position after this point resolves through the
	// namespace, so it has to exist before the first of them.
	if r := q.buildNamespace(formulas); r != nil {
		return nil, r
	}
	if r := q.applyFilter(req); r != nil {
		return nil, r
	}
	if r := q.applyColumns(req); r != nil {
		return nil, r
	}
	return q, nil
}

// applyHops enforces FR-065, and also the case the spec's table leaves implicit:
// `hops` without `near` has nothing to walk from.
//
// The maximum is on the schema too, so a well-behaved caller is stopped at the
// wire. It is enforced AGAIN here because a schema violation surfaces as "your
// body was invalid", which tells the caller nothing about the bound or the
// remedy — and Go's generated type carries a plain *int that no decoder checks.
func (q *query) applyHops(req generated.VaultFindRequest) *RefusalError {
	if req.Hops == nil {
		if q.near != "" {
			q.hops = 1
		}
		return nil
	}
	h := *req.Hops
	if q.near == "" {
		return refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("hops=%d was given with no near, so there is no note to walk from", h),
			"add near=<path or [[wikilink]]>, or drop hops"), nil)
	}
	if h > MaxHops {
		return refuse(problem(generated.HopLimitExceeded,
			fmt.Sprintf("hops=%d exceeds the limit of %d", h, MaxHops),
			fmt.Sprintf("run a second knowledge_find from one of these results — a %d-hop walk is a "+
				"follow-up query you should make knowingly", h)), nil)
	}
	if h < 1 {
		return refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("hops=%d is not a number of link steps", h),
			"hops is 1 or 2; drop it to use 1"), nil)
	}
	q.hops = h
	return nil
}

// applyLimit clamps rather than rejects, and RECORDS the clamp. FR-063: silent
// truncation is the incumbent behaviour this design cites as motivating
// evidence, and shipping our own would be indefensible.
func (q *query) applyLimit(req generated.VaultFindRequest) *RefusalError {
	if req.Limit == nil {
		return nil
	}
	n := *req.Limit
	if n < 1 {
		return refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("limit=%d is not a page size", n),
			fmt.Sprintf("limit is between 1 and %d; drop it to use %d", MaxLimit, DefaultLimit)), nil)
	}
	q.limitAsked = n
	// The cap is MaxLimit for a model and RenderRows for an in-process
	// renderer that declared its own bound. Either way it is a CAP, and either
	// way exceeding it clamps and SAYS SO — the clamp is never silent.
	ceiling := q.limitCap()
	if n > ceiling {
		q.limit = ceiling
		q.clamped = true
		return nil
	}
	q.limit = n
	return nil
}

// limitCap is the page-size ceiling this query is answered under.
func (q *query) limitCap() int {
	if q.renderRows > 0 {
		return q.renderRows
	}
	return MaxLimit
}

// resolveType is FR-024 for the record type itself.
func (q *query) resolveType(req generated.VaultFindRequest, set *records.SchemaSet) *RefusalError {
	// Blank is REFUSED upstream, in checkBlankNarrowing, not tolerated here.
	// Tolerating it was half of the asymmetry that let a blank `type` discard a
	// saved view's own narrowing while still reading as "untyped" downstream.
	if req.Type == nil {
		return nil
	}
	q.recordType = *req.Type
	sc, ok := set.Get(q.recordType)
	if !ok {
		declared := set.Types()
		sort.Strings(declared)
		p := problem(generated.UnknownRecordType,
			fmt.Sprintf("no record type %q is declared in this vault", q.recordType),
			"call knowledge_describe to see the declared record types")
		if len(declared) > 0 {
			p.Permitted = &declared
			p.Reason += "; declared: " + strings.Join(declared, ", ")
		} else {
			// An empty vault and a mistyped name must not read the same. Saying
			// "declared: " with nothing after it would do exactly that.
			p.Reason += "; this vault declares no record types at all"
			p.Fix = str("declare one with knowledge_configure, or search without a type")
		}
		return refuse(p, nil)
	}
	q.schema = sc
	return nil
}

// applyFilter builds the tree. Every leaf is validated against the schema here,
// once, before any record is touched — which is FR-023, and which is also why
// the engine takes records.PreparedFilter into the candidate loop rather than
// re-validating 50,000 times.
func (q *query) applyFilter(req generated.VaultFindRequest) *RefusalError {
	if req.Filter == nil {
		return nil
	}
	// NO "a filter needs a record type" GUARD HERE ANY MORE, and its removal is
	// the untyped multi-type view (FR-018d). `file.mtime > "2026-01-01"` and
	// `formula.age < 30` name properties that belong to no record type, so a
	// blanket refusal would make the whole `file.*` namespace unreachable from
	// the one query shape it was designed for. A leaf naming a TYPED property
	// with no type given is still refused — by namespace.resolve, which says so
	// per leaf and names both escapes rather than refusing the whole filter.
	// FR-023c BEFORE anything is prepared. The bound is on the tree the caller
	// sent, so it is measured on the wire shape rather than on the built one.
	if r := checkFilterTreeBounds(*req.Filter); r != nil {
		return r
	}
	n, r := buildNode(*req.Filter, q.namespace())
	if r != nil {
		return r
	}
	q.filter = n
	q.touched = append(q.touched, n.properties()...)
	return nil
}

// applyColumns validates select / sort / group_by / join / aggregate against the
// schema. Each is FR-024's posture: named, listed, never silently dropped.
//
// group_by does NOT resolve a derived inverse (D5's "company.deals", declared
// only as deal.company's `inverse:`): an inverse is many-valued and computed
// from the properties index, never a stored column, and there is no execution
// path here that materialises one — groupKeys (project.go) reads a survivor's
// decoded VALUES, and renderProperties only ever decodes properties this
// type's OWN schema declares. An earlier attempt to accept an inverse name
// wired the refusal-bypass in applyColumns without ever wiring the value
// computation project.go needs, which meant `group_by=deals` stopped
// refusing and instead returned exactly one group — "absent: true" —
// covering every row, presented as a complete, confident answer. group_by is
// refused on an inverse name (FR-024's ordinary "unknown property" message)
// until inverse computation is implemented end to end.
func (q *query) applyColumns(req generated.VaultFindRequest) *RefusalError {
	// ONE lookup for every position, and it is the namespace's. Before this,
	// applyColumns owned a second copy of FR-024's refusal — which is how
	// `select` and `filter` come to disagree about which names exist.
	lookup := q.namespace().resolve

	if req.Select != nil {
		for _, name := range *req.Select {
			if _, r := lookup("select", name); r != nil {
				return r
			}
			q.selectCols = append(q.selectCols, name)
			q.touched = append(q.touched, name)
		}
	}
	if req.GroupBy != nil {
		if len(*req.GroupBy) > MaxGroupLevels {
			return refuse(problem(generated.UnsupportedParameter,
				fmt.Sprintf("group_by names %d levels; the limit is %d", len(*req.GroupBy), MaxGroupLevels),
				"group by the outer property, then run a second knowledge_find inside the group you want"), nil)
		}
		for _, g := range *req.GroupBy {
			prop, r := lookup("group_by", g.Property)
			if r != nil {
				return r
			}
			// THE DIRECTION IS VALIDATED, not pattern-matched against "desc" —
			// the same rule `sort` applies immediately below, for the same
			// reason. `desc := *g.Direction == "desc"` alone would make
			// `descending`, `DESC` and `down` all group ASCENDING with nothing
			// said, which is the precise silence a directional group key was
			// added to end. Unlike sort, grouping refuses NEITHER a `many`
			// property (FR-028 puts one record in several groups on purpose)
			// NOR a relation (FR-029 groups by target identity); a relation's
			// GROUPS still order by their rendered label, because that is what
			// a group is identified by on the page.
			if g.Direction != nil && !g.Direction.Valid() {
				p := problem(generated.UnsupportedParameter,
					fmt.Sprintf("group_by names direction %q on %q, which is not a group direction",
						string(*g.Direction), g.Property),
					`use "asc" or "desc", or omit direction — omitted means asc`)
				p.Property = str(g.Property)
				permitted := []string{
					string(generated.VaultFindGroupByDirectionAsc),
					string(generated.VaultFindGroupByDirectionDesc),
				}
				p.Permitted = &permitted
				return refuse(p, nil)
			}
			desc := g.Direction != nil && *g.Direction == generated.VaultFindGroupByDirectionDesc
			q.groupBy = append(q.groupBy, groupSpec{property: g.Property, desc: desc, prop: prop})
			q.touched = append(q.touched, g.Property)
		}
	}
	if req.Sort != nil {
		for _, s := range *req.Sort {
			prop, r := lookup("sort", s.Property)
			if r != nil {
				return r
			}
			// F14 — ordering IS comparison (spec FR-021's revision-6 list),
			// governed by the SAME rules as every other comparison: R-1 and
			// R-13 like R-4/R-5/R-7. records.Compare's zero Comparator has no
			// RelationResolver, so a relation/person operand always failed to
			// resolve and fell through to CompareRelationUnresolved — but
			// that was never the real defect. The comparator's own
			// operatorDefinedForType table (compare_oracle.go) declares
			// OpLess/OpGreater FALSE for TypeRelation and TypePerson
			// UNCONDITIONALLY: even a query that wired a real resolver would
			// still have no ordering defined for a relation, because R-1
			// gives it no ordering family at all — `join` already type-checks
			// its property for exactly this reason (below), and sort must
			// too, or it silently ignores the constraint it echoes back as
			// executed.
			if prop.Type == records.TypeRelation || prop.Type == records.TypePerson {
				p := problem(generated.UnsupportedOperator,
					fmt.Sprintf("sort names %q, which is a %s property; ordering is not defined for a relation "+
						"(R-1 defines no ordering operator for it, resolved or not)",
						s.Property, prop.Type),
					"join "+s.Property+" and sort a column of the joined record instead, or sort a property ordering is defined for")
				p.Property = str(s.Property)
				return refuse(p, nil)
			}
			// F15 — R-13's arity rule: ordering is not defined against a
			// `many` property, in a filter or in a sort. The comparator
			// refuses `arr < 5` against a many `arr`; sortSurvivors reaching
			// around that refusal by comparing only element [0]
			// (assemble.go's firstValue) is the arity rule applied to filter
			// and bypassed for sort, which is the "quietly half-applied"
			// failure FR-021's revision 6 names sorting for.
			if prop.Many {
				p := problem(generated.OrderingOnManyProperty,
					fmt.Sprintf("sort names %q, a many-valued %s property; a list has no single order (R-13) — "+
						"comparing only its first stored value would be a silent, arbitrary answer",
						s.Property, prop.Type),
					"group_by "+s.Property+" to see the distribution, or filter on its contents instead of sorting by them")
				p.Property = str(s.Property)
				return refuse(p, nil)
			}
			// THE DIRECTION IS VALIDATED, not pattern-matched against "desc".
			//
			// `desc := *s.Direction == "desc"` alone means every other spelling
			// — `descending`, `DESC`, `down`, `""` — sorts ASCENDING with
			// nothing said, so an agent-authored "top 10 deals" view with
			// `direction: descending` and `limit: 10` returns the ten SMALLEST
			// and reports itself complete. The generated enum already knows
			// which two spellings exist; nothing was asking it. This project
			// already refuses the same mistake for a GROUPING direction
			// (records.ServeRefusalGroupDirection) — the asymmetry was the
			// defect, not the strictness.
			if s.Direction != nil && !s.Direction.Valid() {
				p := problem(generated.UnsupportedParameter,
					fmt.Sprintf("sort names direction %q on %q, which is not a sort direction",
						string(*s.Direction), s.Property),
					`use "asc" or "desc", or omit direction — omitted means asc`)
				p.Property = str(s.Property)
				permitted := []string{
					string(generated.VaultFindSortDirectionAsc),
					string(generated.VaultFindSortDirectionDesc),
				}
				p.Permitted = &permitted
				return refuse(p, nil)
			}
			desc := s.Direction != nil && *s.Direction == generated.VaultFindSortDirectionDesc
			q.sort = append(q.sort, sortKey{property: s.Property, desc: desc, prop: prop})
			q.touched = append(q.touched, s.Property)
		}
	}
	if req.Join != nil {
		for _, name := range *req.Join {
			prop, r := lookup("join", name)
			if r != nil {
				return r
			}
			if prop.Type != records.TypeRelation && prop.Type != records.TypePerson {
				p := problem(generated.RelationTypeMismatch,
					fmt.Sprintf("join names %q, which is a %s property; only a relation can be followed",
						name, prop.Type),
					"join a relation property, or add "+name+" to select to render it as a column of this record")
				p.Property = str(name)
				return refuse(p, nil)
			}
			q.join = append(q.join, name)
			q.touched = append(q.touched, name)
		}
	}
	if req.Aggregate != nil {
		for _, a := range *req.Aggregate {
			op := string(a.Op)
			if !a.Op.Valid() {
				// The op enum is CLOSED at fifteen (FR-150). An op outside it
				// is refused here rather than falling through to a reducer that
				// has no case for it: an empty value reads as an answer.
				return refuse(problem(generated.UnsupportedParameter,
					fmt.Sprintf("%q is not a summary; the fifteen are %s",
						op, strings.Join(allSummaryOps(), ", ")),
					"use one of "+strings.Join(allSummaryOps(), ", ")), nil)
			}
			agg := aggregate{op: op}
			if op == opCount {
				if a.Property != nil && *a.Property != "" {
					return refuse(problem(generated.UnsupportedParameter,
						fmt.Sprintf("count was given the property %q, but count counts ROWS, not values", *a.Property),
						"drop the property from the count, or use sum/min/max to reduce that property"), nil)
				}
				q.aggregates = append(q.aggregates, agg)
				continue
			}
			if a.Property == nil || *a.Property == "" {
				return refuse(problem(generated.UnsupportedParameter,
					fmt.Sprintf("%s needs a property to reduce", op),
					"name the property, or use count to count rows"), nil)
			}
			prop, r := lookup("aggregate", *a.Property)
			if r != nil {
				return r
			}
			if !opDefinedForType(op, prop.Type) {
				// FR-155: a summary a type does not define is REFUSED NAMING
				// THE ONES IT DOES — never answered with a zero, and never
				// refused with a bare "not supported" that leaves the caller
				// guessing at what to ask instead.
				defined := strings.Join(opsDefinedFor(prop.Type), ", ")
				p := problem(generated.TypeMismatch,
					fmt.Sprintf("%s(%s) is not defined: %s is a %s property, and the summaries defined for %s are %s",
						op, *a.Property, *a.Property, prop.Type, prop.Type, defined),
					"use one of "+defined+", or count to count rows")
				p.Property = a.Property
				return refuse(p, nil)
			}
			agg.property = *a.Property
			agg.prop = prop
			q.aggregates = append(q.aggregates, agg)
			q.touched = append(q.touched, *a.Property)
		}
	}
	return nil
}

// selector is everything the store is allowed to decide (ruling R-A).
//
// Read the three assignments and notice what is NOT here: no property, no value,
// no operator. A typed predicate is unexpressible in a propindex.Selector — that
// is the ruling enforced by a type rather than by a comment, and it is why this
// function cannot accidentally push a filter down even if someone wanted it to.
func (q *query) selector(pathPrefix string) propindex.Selector {
	sel := propindex.Selector{RecordType: q.recordType, PathPrefix: pathPrefix}
	switch q.kind {
	case KindAttachment:
		sel.Kind = propindex.KindAttachment
	case KindNote, KindRecord, KindTask:
		sel.Kind = propindex.KindNote
	}
	return sel
}

// needsPropertyIndex reports whether this query reaches the properties index for
// anything the platform gate covers, and which capability to name if it does.
//
// The order is the order the refusals should be reported in: a caller that asked
// for a typed filter AND a join needs to hear about the filter first, because it
// is the narrowing they will fix first.
func (q *query) capabilities() []records.PropertyIndexCapability {
	var caps []records.PropertyIndexCapability
	// kind=record is in this list because it narrows on the candidate's own
	// record_type — a typed narrowing, answerable only from the properties
	// index. Leaving it out would let it degrade to "every note" on a build
	// where that index cannot exist, which is the silent-broadening shape this
	// gate exists to refuse.
	if q.filter != nil || q.recordType != "" || q.kind == KindRecord {
		caps = append(caps, records.CapabilityTypedFilter)
	}
	if len(q.join) > 0 || q.near != "" {
		caps = append(caps, records.CapabilityRelationJoin)
	}
	if len(q.groupBy) > 0 {
		caps = append(caps, records.CapabilityGrouping)
	}
	if len(q.aggregates) > 0 {
		caps = append(caps, records.CapabilityAggregation)
	}
	return caps
}
