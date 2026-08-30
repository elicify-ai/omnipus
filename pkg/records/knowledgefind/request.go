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
)

// AcceptedParameters is the closed set of argument names, in the order the tool
// schema declares them. FR-022c refuses an undeclared parameter BY NAME with
// this list attached, rather than ignoring it — a silently dropped argument is
// how a caller comes to believe a constraint was applied that never was.
var AcceptedParameters = []string{
	"words", "type", "kind", "filter", "view", "near", "hops", "join",
	"group_by", "sort", "select", "aggregate", "explain", "limit", "cursor", "detail",
}

// Kind values. `record` is a synonym for `note` narrowed to notes that declare a
// type; the store's own kind column only ever holds note or attachment.
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
	groupBy    []string
	sort       []sortKey
	selectCols []string
	aggregates []aggregate
	explain    bool
	limit      int
	limitAsked int
	clamped    bool
	cursor     string
	minimal    bool

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
}

type sortKey struct {
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
func parse(req generated.VaultFindRequest, set *records.SchemaSet) (*query, *RefusalError) {
	q := &query{
		kind:  KindNote,
		limit: DefaultLimit,
	}

	if req.Kind != nil && *req.Kind != "" {
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
	if n > MaxLimit {
		q.limit = MaxLimit
		q.clamped = true
		return nil
	}
	q.limit = n
	return nil
}

// resolveType is FR-024 for the record type itself.
func (q *query) resolveType(req generated.VaultFindRequest, set *records.SchemaSet) *RefusalError {
	if req.Type == nil || *req.Type == "" {
		return nil
	}
	q.recordType = *req.Type
	sc, ok := set.Get(q.recordType)
	if !ok {
		declared := set.Types()
		sort.Strings(declared)
		p := problem(generated.UnknownRecordType,
			fmt.Sprintf("no record type %q is declared in this vault", q.recordType),
			"call vault_describe to see the declared record types")
		if len(declared) > 0 {
			p.Permitted = &declared
			p.Reason += "; declared: " + strings.Join(declared, ", ")
		} else {
			// An empty vault and a mistyped name must not read the same. Saying
			// "declared: " with nothing after it would do exactly that.
			p.Reason += "; this vault declares no record types at all"
			p.Fix = str("declare one with vault_configure, or search without a type")
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
	if q.schema == nil {
		return refuse(problem(generated.UnsupportedParameter,
			"a typed filter needs a record type: property names are scoped to their type, "+
				"so there is nothing to resolve the names against",
			"add type=<record type>, or search with words instead of a filter"), nil)
	}
	n, r := buildNode(*req.Filter, q.schema)
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
	lookup := func(kind, name string) (*records.Property, *RefusalError) {
		if q.schema == nil {
			return nil, refuse(problem(generated.UnsupportedParameter,
				fmt.Sprintf("%s names the property %q, but no record type was given", kind, name),
				"add type=<record type> so property names can be resolved"), nil)
		}
		prop, ok := q.schema.Property(name)
		if !ok {
			names := q.schema.PropertyNames()
			p := problem(generated.UnknownProperty,
				fmt.Sprintf("unknown property %q on record type %q; declared: %s",
					name, q.schema.Type, strings.Join(names, ", ")),
				"call vault_describe record_type="+q.schema.Type+" to see the declared properties")
			p.Property = str(name)
			p.Permitted = &names
			return nil, refuse(p, nil)
		}
		return prop, nil
	}

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
		for _, name := range *req.GroupBy {
			if _, r := lookup("group_by", name); r != nil {
				return r
			}
			q.groupBy = append(q.groupBy, name)
			q.touched = append(q.touched, name)
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
			desc := s.Direction != nil && string(*s.Direction) == "desc"
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
			agg := aggregate{op: op}
			if op == "count" {
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
			if prop.Type != records.TypeInteger && prop.Type != records.TypeDecimal {
				p := problem(generated.TypeMismatch,
					fmt.Sprintf("%s(%s) is not defined: %s is a %s property, and only integer and decimal can be reduced",
						op, *a.Property, *a.Property, prop.Type),
					"use count to count rows, or group_by "+*a.Property+" to see the distribution")
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
	if q.filter != nil || q.recordType != "" {
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
