// Omnipus — regression coverage for code review A findings F8, F14, F15:
// pagination cursors computed before the byte budget trims the page, and
// sort silently accepting properties R-1/R-13 define no ordering for.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"fmt"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// TEST BUILDERS — used only by this SQLite-tagged file, so they live here
// rather than in the untagged builders_test.go (see notNode's own note in
// fixture_test.go: an unused helper under the other tag set is a lint
// failure).
// ---------------------------------------------------------------------------

func withLimit(n int) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) { r.Limit = &n }
}

func withCursor(c string) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) { r.Cursor = &c }
}

// withSort builds a single sort key. desc="" omits the direction (asc by
// default); "asc" or "desc" sets it explicitly.
func withSort(property, desc string) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) {
		k := generated.VaultFindSort{Property: property}
		if desc != "" {
			d := generated.VaultFindSortDirection(desc)
			k.Direction = &d
		}
		ks := []generated.VaultFindSort{k}
		r.Sort = &ks
	}
}

// ---------------------------------------------------------------------------
// F8 — pagination cursor must never skip a row the byte budget trimmed
// ---------------------------------------------------------------------------

// TestFind_PaginationCursorNeverSkipsARowTheBudgetTrimmed is F8's repro and
// regression in one test: construct the 200-survivor case the review cited,
// page through it using the cursor the response itself hands back, and prove
// BY SET DIFFERENCE that every one of the 200 rows was returned by SOME page.
//
// The plant fixture renders 8 columns per row (species, condition, planted,
// height_cm, cuttings, bed, keeper, labels), wide enough that a 50-row page
// overflows the 4000-byte response budget on its own — this reproduces the
// review's exact numbers (200 survivors, limit=50, the byte budget trims rows
// off a 50-row page) without needing to inflate any field by hand.
//
// BEFORE THE FIX: NextCursor was stamped from the PRE-TRIM page size
// (offset + len(rows), computed before trimToBudget ran), so it started the
// next page past every row the budget had just cut off the end of the
// current one. Those rows were never returned by ANY page — this test failed
// with a non-empty "SET DIFFERENCE" list (rows in the missing 20-49 range of
// the first page, and the equivalent range of every page after it).
func TestFind_PaginationCursorNeverSkipsARowTheBudgetTrimmed(t *testing.T) {
	f := newFixture(t)
	const n = 200
	want := map[string]bool{}
	for i := 1; i <= n; i++ {
		f.plant(i, "growing", fmt.Sprintf("%d.00", i))
		want[fmt.Sprintf("PL-%04d", i)] = true
	}

	seen := map[string]bool{}
	var dupes []string
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > 40 {
			t.Fatalf("did not terminate after 40 pages (%d/%d rows seen so far); "+
				"NextCursor is not making forward progress", len(seen), n)
		}
		r := req(withType("plant"), withLimit(50))
		if cursor != "" {
			r = req(withType("plant"), withLimit(50), withCursor(cursor))
		}
		resp := mustFind(t, f.deps(), r)

		for _, row := range resp.Rows {
			id := rowID(row)
			if seen[id] {
				dupes = append(dupes, id)
			}
			seen[id] = true
		}
		if resp.NextCursor == nil {
			break
		}
		cursor = *resp.NextCursor
	}

	if len(dupes) > 0 {
		sort.Strings(dupes)
		t.Errorf("the cursor returned %d row(s) more than once across %d pages: %v",
			len(dupes), pages, dupes)
	}

	var missing []string
	for id := range want {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("SET DIFFERENCE — %d of %d records were returned by NO page at all "+
			"across %d pages: %v", len(missing), n, pages, missing)
	}
	if len(seen) != n {
		t.Fatalf("saw %d distinct rows across every page combined, want exactly %d", len(seen), n)
	}
}

// TestFindTasks_PaginationCursorNeverSkipsARowTheBudgetTrimmed is F8's twin
// for the `kind=task` path (responses.go::findTasks) — "same shape" per the
// review. Task rows render narrower than note rows (one checkbox line each,
// no columns), so this drives a bigger corpus to reach the same byte-budget
// overflow with a 50-row page.
func TestFindTasks_PaginationCursorNeverSkipsARowTheBudgetTrimmed(t *testing.T) {
	f := newFixture(t)
	const n = 400
	want := map[string]bool{}
	for i := 1; i <= n; i++ {
		path := fmt.Sprintf("garden/task-plant-%04d.md", i)
		f.write(path, fmt.Sprintf(`---
type: plant
id: PL-%04d
species: Monstera deliciosa, a fairly verbose species name to widen the line
condition: growing
---

- [ ] repot plant %d in spring, checking the drainage holes are still clear
`, i, i))
		// Task rows are addressed by path + line, not by the plant's own id —
		// rowID falls back to row.Path when Id is unset, and a task row's Id
		// is never set (FR-076a: a row is one THING AT A PATH, not the note).
		want[path] = true
	}

	seen := map[string]bool{}
	var dupes []string
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > 40 {
			t.Fatalf("did not terminate after 40 pages (%d/%d rows seen so far); "+
				"NextCursor is not making forward progress", len(seen), n)
		}
		kind := generated.VaultFindRequestKindTask
		r := req(withType("plant"), withLimit(50))
		r.Kind = &kind
		if cursor != "" {
			r = req(withType("plant"), withLimit(50), withCursor(cursor))
			r.Kind = &kind
		}
		resp := mustFind(t, f.deps(), r)

		for _, row := range resp.Rows {
			id := row.Path
			if seen[id] {
				dupes = append(dupes, id)
			}
			seen[id] = true
		}
		if resp.NextCursor == nil {
			break
		}
		cursor = *resp.NextCursor
	}

	if len(dupes) > 0 {
		sort.Strings(dupes)
		t.Errorf("the cursor returned %d row(s) more than once across %d pages: %v",
			len(dupes), pages, dupes)
	}

	var missing []string
	for id := range want {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("SET DIFFERENCE — %d of %d task rows were returned by NO page at all "+
			"across %d pages: %v", len(missing), n, pages, missing)
	}
}

// ---------------------------------------------------------------------------
// F14 — sort on a relation or person property is refused, not silently
// dropped to the path tiebreak
// ---------------------------------------------------------------------------

// TestFind_SortOnRelationOrPersonPropertyIsRefused is F14. Before the fix,
// `sort=bed desc` (or `sort=keeper desc`) was ACCEPTED, echoed back as
// executed, and then silently ignored: records.Compare's zero Comparator has
// no RelationResolver, so every relation/person comparison reported
// CompareRelationUnresolved and sortSurvivors fell through to the path
// tiebreak, returning ascending PATH order while `complete: true` and the
// echo both claimed the caller's sort ran.
func TestFind_SortOnRelationOrPersonPropertyIsRefused(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.00")
	f.plant(2, "growing", "20.00")
	f.plant(3, "growing", "30.00")

	for _, prop := range []string{"bed", "keeper"} {
		t.Run(prop, func(t *testing.T) {
			resp := mustRefuse(t, f.deps(), req(withType("plant"), withSort(prop, "desc")))

			if len(resp.Problems) != 1 {
				t.Fatalf("problems = %d, want exactly 1: %+v", len(resp.Problems), resp.Problems)
			}
			p := resp.Problems[0]
			if p.Property == nil || *p.Property != prop {
				t.Errorf("problem.Property = %v, want %q", p.Property, prop)
			}
			if p.Code != generated.UnsupportedOperator {
				t.Errorf("problem.Code = %q, want %q (R-1's operatorDefinedForType has no ordering "+
					"operator for a relation/person, resolved or not)", p.Code, generated.UnsupportedOperator)
			}
			// The echo must still show the sort AS RECEIVED (FR-122's raw
			// echo on early refusal) — a refusal with no echo leaves the
			// caller unable to tell whether the argument they sent was even
			// seen.
			if resp.QueryEcho == "" {
				t.Errorf("a refused sort produced an empty echo")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// F15 — sort on a many-valued property is refused, not silently narrowed to
// element [0]
// ---------------------------------------------------------------------------

// TestFind_SortOnManyPropertyIsRefused is F15. Before the fix, `sort=labels`
// was ACCEPTED and assemble.go's firstValue silently compared only
// values[0] of a `many` property — exactly the ordering R-13 refuses in a
// filter (`labels < 'x'` is refused naming the arity), reached around for
// sort instead of honoured.
func TestFind_SortOnManyPropertyIsRefused(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.00")
	f.plant(2, "growing", "20.00")

	resp := mustRefuse(t, f.deps(), req(withType("plant"), withSort("labels", "")))

	if len(resp.Problems) != 1 {
		t.Fatalf("problems = %d, want exactly 1: %+v", len(resp.Problems), resp.Problems)
	}
	p := resp.Problems[0]
	if p.Property == nil || *p.Property != "labels" {
		t.Errorf("problem.Property = %v, want %q", p.Property, "labels")
	}
	if p.Code != generated.OrderingOnManyProperty {
		t.Errorf("problem.Code = %q, want %q (R-13: ordering is not defined across arity)",
			p.Code, generated.OrderingOnManyProperty)
	}
}

// TestFind_SortOnAnOrdinaryPropertyStillWorks pins the non-regression: F14
// and F15's new refusals must not catch a plain scalar sort. height_cm is
// decimal, single-valued — R-1/R-13 both define ordering for it.
func TestFind_SortOnAnOrdinaryPropertyStillWorks(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "30.00")
	f.plant(2, "growing", "10.00")
	f.plant(3, "growing", "20.00")

	resp := mustFind(t, f.deps(), req(withType("plant"), withSort("height_cm", "asc")))
	got := rowIDs(resp)
	want := []string{"PL-0002", "PL-0003", "PL-0001"}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows = %v, want %v (ascending by height_cm)", got, want)
		}
	}
}
