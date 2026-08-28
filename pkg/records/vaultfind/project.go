// Omnipus — spec FR-046, FR-123, FR-124, FR-125, FR-125a: rows, borrowed
// columns, groups and totals.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultfind

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

// computeTotals reduces every requested aggregate.
//
// The scope clause is built in the same function as the number, so the two can
// never drift apart. A total whose scope is added by a later layer is a total
// that will eventually be rendered without one, and FR-125's whole subject is
// that a bare number over a partially-evaluated set is a wrong answer.
func computeTotals(q *query, rows []survivor) []generated.VaultFindTotal {
	out := make([]generated.VaultFindTotal, 0, len(q.aggregates))
	shown := len(rows)
	if shown > q.limit {
		shown = q.limit
	}

	for _, a := range q.aggregates {
		t := generated.VaultFindTotal{
			Op:    generated.VaultFindTotalOp(a.op),
			Label: a.label(),
		}
		if a.op == "count" {
			t.Value = group3(len(rows))
			t.Scope = fmt.Sprintf("over %d of %d evaluated rows (%d shown)",
				len(rows), len(rows), shown)
			out = append(out, t)
			continue
		}

		var acc records.Decimal
		var have bool
		counted, skipped := 0, 0
		for _, s := range rows {
			v, ok := firstValue(s, a.property)
			if !ok {
				skipped++
				continue
			}
			counted++
			if !have {
				acc, have = v.Number, true
				continue
			}
			switch a.op {
			case "sum":
				sum, err := acc.Add(v.Number)
				if err != nil {
					// An exact sum that cannot be represented is REFUSED, never
					// rounded to fit. A silently rounded total is a number
					// nobody computed wearing the authority of one that was.
					t.Refused = boolPtr(true)
					t.Value = ""
					t.Scope = fmt.Sprintf("no total: %v", err)
					have = false
				}
				acc = sum
			case "min":
				if v.Number.Cmp(acc) < 0 {
					acc = v.Number
				}
			case "max":
				if v.Number.Cmp(acc) > 0 {
					acc = v.Number
				}
			}
		}

		if t.Refused != nil && *t.Refused {
			out = append(out, t)
			continue
		}
		if !have {
			// No row carried a value. That is not zero, and rendering it as zero
			// would state a fact about the corpus that is not true.
			t.Refused = boolPtr(true)
			t.Value = ""
			t.Scope = fmt.Sprintf("no total: none of the %d evaluated rows carries a value for %s",
				len(rows), a.property)
			out = append(out, t)
			continue
		}

		t.Value = groupDigits(acc.String())
		t.Scope = fmt.Sprintf("over %d of %d evaluated rows (%d shown)", counted, len(rows), shown)
		if skipped > 0 {
			// The scope names what it EXCLUDED, in the same sentence as the
			// number, so a reader cannot take the total for a whole-set figure.
			t.Scope += fmt.Sprintf("; %d row(s) carry no %s and are not included", skipped, a.property)
		}
		out = append(out, t)
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
		rows   []survivor
	}
	order := []string{}
	buckets := map[string]*bucket{}

	for _, s := range e.survivors {
		keys, unresolved := groupKeys(q, s, outer)
		reportUnresolvedRelation(e, s, outer, unresolved)
		for _, key := range keys {
			id := key.bucket
			if key.absent {
				id = "\x00absent"
			}
			b, ok := buckets[id]
			if !ok {
				b = &bucket{key: key.key, absent: key.absent}
				buckets[id] = b
				order = append(order, id)
			}
			b.rows = append(b.rows, s)
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		bi, bj := buckets[order[i]], buckets[order[j]]
		return groupOrderLess(bi.key, bi.absent, order[i], bj.key, bj.absent, order[j])
	})

	out := make([]generated.VaultFindGroup, 0, len(order))
	for _, id := range order {
		b := buckets[id]
		g := generated.VaultFindGroup{
			Property: outer,
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

func buildSubgroups(q *query, e *evaluation, property string, rows []survivor) []generated.VaultFindSubgroup {
	type bucket struct {
		key    string
		absent bool
		rows   []survivor
	}
	order := []string{}
	buckets := map[string]*bucket{}
	for _, s := range rows {
		keys, unresolved := groupKeys(q, s, property)
		reportUnresolvedRelation(e, s, property, unresolved)
		for _, key := range keys {
			id := key.bucket
			if key.absent {
				id = "\x00absent"
			}
			b, ok := buckets[id]
			if !ok {
				b = &bucket{key: key.key, absent: key.absent}
				buckets[id] = b
				order = append(order, id)
			}
			b.rows = append(b.rows, s)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		bi, bj := buckets[order[i]], buckets[order[j]]
		return groupOrderLess(bi.key, bi.absent, order[i], bj.key, bj.absent, order[j])
	})
	out := make([]generated.VaultFindSubgroup, 0, len(order))
	for _, id := range order {
		b := buckets[id]
		g := generated.VaultFindSubgroup{
			Property: property,
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

// groupOrderLess is the sort predicate shared by buildGroups and
// buildSubgroups: absent last, then by the FOLDED display key (R-5c: `Won`,
// `won` and `WON` sort as one, the same order equality groups them by), with
// ties broken on the bucket identity itself so the order is total and
// deterministic (R-5d's rule, generalised from a value's raw bytes to a
// group's dedupe key — for a relation that key IS the record identity, which
// is exactly the tiebreaker two same-titled-but-distinct targets need).
//
// It is a plain function over the four scalars rather than a generic over
// the two local bucket types: a type declared INSIDE a function cannot carry
// a method in Go, and lifting it to package scope purely to satisfy a type
// constraint would cost more clarity than the six lines of duplication it
// removes.
func groupOrderLess(keyI string, absentI bool, idI, keyJ string, absentJ bool, idJ string) bool {
	if absentI != absentJ {
		// Absence sorts last: "nobody recorded this" is not a value, and
		// leading with it puts the least informative group first.
		return !absentI
	}
	if fi, fj := records.FoldKey(keyI), records.FoldKey(keyJ); fi != fj {
		return records.FoldLess(keyI, keyJ)
	}
	return idI < idJ
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
		keys = append(keys, groupKey{key: display, bucket: bucket})
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
	p.Fix = str("run vault_describe check_integrity to see why " + strings.Join(targets, ", ") + " does not resolve")
	e.recordProblems([]generated.RecordProblem{p})
}

func pathsOf(rows []survivor) []string {
	out := make([]string, 0, len(rows))
	for _, s := range rows {
		out = append(out, s.cand.Path)
	}
	return out
}
