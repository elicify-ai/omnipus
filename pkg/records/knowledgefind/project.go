// Omnipus — spec FR-046, FR-123, FR-124, FR-125, FR-125a: rows, borrowed
// columns, groups and totals.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// renderRow projects one survivor into the wire row.
func renderRow(q *query, s survivor) generated.VaultFindRow {
	row := generated.VaultFindRow{
		Path:  s.cand.Path,
		Title: titleOf(s.cand.Path),
		Cells: []generated.VaultFindCell{},
		Joins: []generated.VaultFindJoin{},
	}
	if s.cand.RecordID != "" {
		row.Id = str(s.cand.RecordID)
	}
	// `minimal` drops the columns; the header and the problem count survive the
	// trim, because the caveat is the one thing a shorter answer must not lose.
	if q.minimal {
		return row
	}
	for _, prop := range q.renderProperties() {
		if isJoined(q, prop.Name) {
			continue
		}
		pv, ok := s.values[prop.Name]
		if !ok {
			continue
		}
		row.Cells = append(row.Cells, generated.VaultFindCell{
			Property: prop.Name,
			Value:    renderValue(pv),
		})
	}
	return row
}

func isJoined(q *query, name string) bool {
	for _, j := range q.join {
		if j == name {
			return true
		}
	}
	return false
}

// titleOf is the note's display name — the file stem. It is derived rather than
// stored because the vault's own filename IS the title (D7's "filename is
// identity"), and a second stored title would be a second source of truth.
func titleOf(path string) string {
	base := path
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".md")
}

// renderValue turns a resolved property into the text a row shows.
//
// THE DECIMAL RULE, stated because it changed under us. Spec 4.2's worked
// example renders `180,000.00` on the strength of a property-level
// `scale: 2` declaration — and Stage 1 REMOVED property-level scale from the
// schema entirely (schema.go: "A `decimal` deliberately does NOT gain a
// property-level scale"; the bound is per-value at parse time). So the surviving
// half of that rule is the one that applies: a decimal renders AT THE VALUE'S OWN
// SCALE, exactly as the note wrote it (FR-046, render-what-the-file-says).
//
// Thousands separators are a choice of this compact-text projection and are
// never part of a stored or a compared value.
func renderValue(pv records.PropertyValue) string {
	switch pv.State {
	case records.StateAbsent:
		return ""
	case records.StateNonConforming:
		// The row still renders, and it renders what the FILE says rather than a
		// blank. A blank would read as "no value", and the reader would go
		// looking for a missing property instead of a malformed one — the
		// problem list already names it, and the two must agree.
		if len(pv.Values) == 0 {
			return "(unreadable)"
		}
	}
	parts := make([]string, 0, len(pv.Values))
	for _, v := range pv.Values {
		parts = append(parts, renderTyped(v))
	}
	return strings.Join(parts, ", ")
}

func renderTyped(v records.TypedValue) string {
	switch v.Type {
	case records.TypeInteger, records.TypeDecimal:
		return groupDigits(v.Number.String())
	case records.TypeEnum:
		return v.Enum.Name
	case records.TypeRelation, records.TypePerson:
		return v.Link.Raw
	case records.TypeDate:
		return v.Date.String()
	}
	return v.Text
}

// groupDigits inserts thousands separators into an exact decimal string without
// going near a float. The fractional part is left exactly as written.
func groupDigits(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	out := group3String(intPart)
	if neg {
		out = "-" + out
	}
	return out + frac
}

func group3String(s string) string {
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, parts...), ",")
}

// ---------------------------------------------------------------------------
// TOTALS — computed over the FULL EVALUATED SET, never the page (FR-125a)
// ---------------------------------------------------------------------------

// computeTotals reduces every requested aggregate over the FULL evaluated set.
//
// It is a loop and nothing else: the fifteen summaries and their two
// computational classes live in summaries.go, so that "which reductions exist"
// is answered in one place rather than by reading a switch statement here. The
// scope clause is built beside each number, in reduceAggregate, for the reason
// FR-125 gives — a total whose scope is attached by a later layer is a total
// that will eventually be rendered without one, and a bare number over a
// partially-evaluated set is a wrong answer.
func computeTotals(q *query, rows []survivor) []generated.VaultFindTotal {
	out := make([]generated.VaultFindTotal, 0, len(q.aggregates))
	for _, a := range q.aggregates {
		out = append(out, reduceAggregate(q, a, rows))
	}
	return out
}

func boolPtr(b bool) *bool { return &b }

// ---------------------------------------------------------------------------
// GROUPS — FR-027 (two levels), FR-028 (a record appears in every group)
// ---------------------------------------------------------------------------

// buildGroups groups the evaluated set, and reports the group half of a
// relation's two failure modes (D5, matching pkg/knowledge/integrity.go's
// vocabulary): a value that never resolved to a record is EXCLUDED from
// every group and NAMED in the problem list — "reported... never silently
// rendered as a distinct group of one" — rather than either forming a group
// of its own or being folded into "absent", which would misreport a record
// that HAS a value as one that has none.
//
// A record holding SEVERAL values of the grouped property appears in EVERY group
// it belongs to (FR-028), so the group counts legitimately sum to more than the
// row count. Each group therefore states its own count rather than leaving the
// reader to add them up. THIS IS NOT A DOUBLE COUNT: it is Obsidian's own
// alternative — one combined group literally named "Finance Business" for a
// record tagged both — that the Obsidian team confirms is intentional and that
// ADR-068 D10 rejects by name as useless for categorisation. A record that is
// both `vendor` and `partner` belongs in the vendor view AND the partner view;
// a system that only ever put it in one would answer "how many vendors" wrong.
func buildGroups(q *query, e *evaluation) []generated.VaultFindGroup {
	if len(q.groupBy) == 0 {
		return nil
	}
	outer := q.groupBy[0]

	type bucket struct {
		key    string
		absent bool
		order  records.TypedValue
		hasVal bool
		rows   []survivor
	}
	order := []string{}
	buckets := map[string]*bucket{}

	for _, s := range e.survivors {
		keys, unresolved := groupKeys(q, s, outer.property)
		reportUnresolvedRelation(e, s, outer.property, unresolved)
		for _, key := range keys {
			id := key.bucket
			if key.absent {
				id = "\x00absent"
			}
			b, ok := buckets[id]
			if !ok {
				b = &bucket{key: key.key, absent: key.absent, order: key.order, hasVal: !key.absent}
				buckets[id] = b
				order = append(order, id)
			}
			b.rows = append(b.rows, s)
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		bi, bj := buckets[order[i]], buckets[order[j]]
		return groupOrderLess(
			groupOrder{key: bi.key, id: order[i], absent: bi.absent, value: bi.order, hasValue: bi.hasVal},
			groupOrder{key: bj.key, id: order[j], absent: bj.absent, value: bj.order, hasValue: bj.hasVal},
			outer.desc)
	})

	out := make([]generated.VaultFindGroup, 0, len(order))
	for _, id := range order {
		b := buckets[id]
		g := generated.VaultFindGroup{
			Property: outer.property,
			Key:      b.key,
			Count:    len(b.rows),
			Paths:    pathsOf(b.rows),
		}
		if b.absent {
			g.Absent = boolPtr(true)
		}
		if len(q.groupBy) > 1 {
			sub := buildSubgroups(q, e, q.groupBy[1], b.rows)
			g.Subgroups = &sub
		}
		out = append(out, g)
	}
	return out
}

// buildSubgroups is the inner level, and it reads its OWN direction from its
// OWN key. The two levels are independent: `group_by=[{owner}, {stage, desc}]`
// runs owners ascending and stages descending inside each one, because that is
// what was asked for. Applying the outer key's direction to both would answer a
// question nobody put.
func buildSubgroups(q *query, e *evaluation, key groupSpec, rows []survivor) []generated.VaultFindSubgroup {
	type bucket struct {
		key    string
		absent bool
		order  records.TypedValue
		hasVal bool
		rows   []survivor
	}
	order := []string{}
	buckets := map[string]*bucket{}
	for _, s := range rows {
		keys, unresolved := groupKeys(q, s, key.property)
		reportUnresolvedRelation(e, s, key.property, unresolved)
		for _, k := range keys {
			id := k.bucket
			if k.absent {
				id = "\x00absent"
			}
			b, ok := buckets[id]
			if !ok {
				b = &bucket{key: k.key, absent: k.absent, order: k.order, hasVal: !k.absent}
				buckets[id] = b
				order = append(order, id)
			}
			b.rows = append(b.rows, s)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		bi, bj := buckets[order[i]], buckets[order[j]]
		return groupOrderLess(
			groupOrder{key: bi.key, id: order[i], absent: bi.absent, value: bi.order, hasValue: bi.hasVal},
			groupOrder{key: bj.key, id: order[j], absent: bj.absent, value: bj.order, hasValue: bj.hasVal},
			key.desc)
	})
	out := make([]generated.VaultFindSubgroup, 0, len(order))
	for _, id := range order {
		b := buckets[id]
		g := generated.VaultFindSubgroup{
			Property: key.property,
			Key:      b.key,
			Count:    len(b.rows),
			Paths:    pathsOf(b.rows),
		}
		if b.absent {
			g.Absent = boolPtr(true)
		}
		out = append(out, g)
	}
	return out
}

// groupOrder is one bucket, reduced to what deciding its position needs.
//
// It carries both a display key and a VALUE because the two answer different
// questions. `key` is what the reader sees and what a relation group is
// identified by; `value` is the typed thing the comparator can order — and for
// a number or a date those two disagree in a way that matters: as text, "9"
// sorts after "12".
type groupOrder struct {
	key      string
	id       string
	absent   bool
	value    records.TypedValue
	hasValue bool
}

// groupOrderLess is the sort predicate shared by buildGroups and
// buildSubgroups.
//
// THE ORDER, IN FULL:
//
//  1. ABSENT LAST, IN BOTH DIRECTIONS. This is a decision, and it is the same
//     one row sorting already makes (assemble.go's compareByProperty). A record
//     with no value has not got a small value and has not got a large one
//     either: absence is OUTSIDE the order, not at one end of it. Letting
//     `desc` reverse it would put "nobody recorded this" first, precisely where
//     a reader asking for the biggest group is looking.
//  2. BY VALUE, THROUGH THE COMPARATOR (records.Compare), so grouping orders a
//     number numerically and a date chronologically. This is the clause that
//     makes `desc` mean something: descending a backlink count must put 12
//     before 9, and the folded-text order below puts "12" before "9" because
//     `1` < `9`. The comparator is asked rather than a switch written here, so
//     grouping inherits R-1's domains rather than growing a second opinion
//     about what orders — including R-5/R-E's ruling that an enum orders
//     LEXICALLY over its value with no declared-position ordinal
//     (records.comparisonDomain maps enum onto text).
//  3. BY THE FOLDED DISPLAY KEY, when the comparator declines to order the two
//     (a relation, a person, a checkbox — R-1 defines no ordering for them) or
//     when it calls them equal. R-5c: `Won`, `won` and `WON` order as one, the
//     same order equality groups them by.
//  4. BY THE BUCKET IDENTITY, so the order is total and deterministic (R-5d's
//     rule, generalised from a value's raw bytes to a group's dedupe key — for
//     a relation that key IS the record identity, which is exactly the
//     tiebreaker two same-titled-but-distinct targets need).
//
// THE IDENTITY TIEBREAK IS NOT REVERSED BY `desc`, and that is deliberate. It
// is not part of the order the caller asked for; it exists so two runs of one
// query cannot return the same groups in different arrangements. Reversing it
// would make `desc` a claim about something the caller never expressed.
//
// It is a plain function over two small structs rather than a generic over
// the two local bucket types: a type declared INSIDE a function cannot carry
// a method in Go, and lifting it to package scope purely to satisfy a type
// constraint would cost more clarity than the few lines of duplication it
// removes.
func groupOrderLess(a, b groupOrder, desc bool) bool {
	if a.absent != b.absent {
		return !a.absent
	}
	if a.hasValue && b.hasValue {
		if c, ok := records.Compare(a.value, b.value); ok && c != 0 {
			if desc {
				return c > 0
			}
			return c < 0
		}
	}
	if fa, fb := records.FoldKey(a.key), records.FoldKey(b.key); fa != fb {
		if desc {
			return records.FoldLess(b.key, a.key)
		}
		return records.FoldLess(a.key, b.key)
	}
	return a.id < b.id
}

type groupKey struct {
	// key is the DISPLAY text — what a reader sees. For a relation this is
	// the wikilink as the source record wrote it (D22.4's "render what the
	// file says" carried into grouping).
	key string
	// bucket is the DEDUPE identity — what decides whether two values land
	// in the SAME group. For a scalar value it is the folded key (FR-011a:
	// text and enum matching is case-insensitive, and grouping is a form of
	// equality); for a relation or person it is the resolved record id
	// (D5/R-8: identity, never display text) so an alias or a not-yet-
	// rewritten wikilink cannot fork a group the way a raw-text bucket would.
	bucket string
	absent bool
	// order is the TYPED value this group orders by — the same value the
	// comparator would be handed in a filter. It is carried beside the display
	// text because the two order differently the moment the property is a
	// number or a date: `12` sorts before `9` as text and after it as a
	// number, and a descending group of backlink counts is unreadable if the
	// first is used. Zero for an absent group, which never reaches a value
	// comparison (groupOrderLess puts absence outside the order).
	order records.TypedValue
}

// groupKeys is FR-028: a record with several values belongs to several
// groups. unresolved carries every relation/person value that named a real
// property but could not be resolved to a record identity — D5's "reported,
// never silently rendered as a distinct group of one" applied at query time.
//
// An ABSENT property yields exactly one key, marked absent — a real group rather
// than a dropped record. The records nobody recorded a value for are frequently
// the ones being asked about, and silently omitting them from a grouped answer
// is the checkbox third-state defect in another costume. A record whose ONLY
// values are unresolved relations is neither: it HAS a value, so it is not
// absent, and D5 forbids inventing a group for what the value failed to
// resolve to — so it belongs to NO group here, and is named in the problem
// list by the caller instead.
func groupKeys(q *query, s survivor, property string) (keys []groupKey, unresolved []records.TypedValue) {
	pv, ok := s.values[property]
	if !ok || pv.State == records.StateAbsent || len(pv.Values) == 0 {
		return []groupKey{{absent: true}}, nil
	}
	seen := map[string]bool{}
	for _, v := range pv.Values {
		display := renderTyped(v)
		bucket := records.FoldKey(display)
		if v.Type == records.TypeRelation || v.Type == records.TypePerson {
			switch {
			case q.resolve == nil:
				// No resolver was wired at all (Deps.Resolve was nil). This
				// is a degraded mode, not a silent one: every relation
				// COMPARISON in the same response already reports
				// CompareRelationUnresolved for the identical reason, so the
				// caller has already been told resolution is unavailable —
				// falling back to the folded raw text here (rather than
				// excluding every relation value from every group) keeps
				// grouping usable in that degraded state instead of
				// returning nothing at all.
			default:
				id, ok := q.resolve(v.Link)
				if !ok || id == "" {
					unresolved = append(unresolved, v)
					continue
				}
				// \x01 cannot appear in a folded key (FoldKey never emits a
				// C0 control byte), so an identity bucket can never collide
				// with a scalar's folded-text bucket by coincidence.
				bucket = "\x01id:" + id
			}
		}
		if seen[bucket] {
			continue
		}
		seen[bucket] = true
		keys = append(keys, groupKey{key: display, bucket: bucket, order: v})
	}
	if len(keys) == 0 {
		if len(unresolved) > 0 {
			return nil, unresolved
		}
		return []groupKey{{absent: true}}, nil
	}
	return keys, unresolved
}

// reportUnresolvedRelation names every value groupKeys excluded, using the
// SAME vocabulary the comparator (CompareRelationUnresolved) and
// check_integrity (CategoryUnresolvedRelation) already use for this exact
// failure mode — a second name for one cause is how a caller ends up
// handling one and treating the other as unrelated.
func reportUnresolvedRelation(e *evaluation, s survivor, property string, unresolved []records.TypedValue) {
	if len(unresolved) == 0 {
		return
	}
	id := s.cand.RecordID
	if id == "" {
		id = s.cand.Path
	}
	targets := make([]string, 0, len(unresolved))
	for _, v := range unresolved {
		targets = append(targets, v.Link.Raw)
	}
	p := generated.RecordProblem{
		Code: generated.DanglingRelation,
		Reason: fmt.Sprintf("%s: group_by %s could not resolve %s to a record — excluded from every group",
			id, property, strings.Join(targets, ", ")),
		Records: []string{id},
		Paths:   &[]string{s.cand.Path},
	}
	p.Property = str(property)
	p.Fix = str("run knowledge_describe check_integrity to see why " + strings.Join(targets, ", ") + " does not resolve")
	e.recordProblems([]generated.RecordProblem{p})
}

func pathsOf(rows []survivor) []string {
	out := make([]string, 0, len(rows))
	for _, s := range rows {
		out = append(out, s.cand.Path)
	}
	return out
}
