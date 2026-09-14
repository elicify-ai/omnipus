// Omnipus — regression coverage for the 2026-09-13 UAT findings D-08, D-10,
// D-11, D-33 and D-60 (request shape, paging and the query echo).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func withCursorOnly(c string) generated.VaultFindRequest {
	return generated.VaultFindRequest{Cursor: &c}
}

// TestUAT_D08_CursorOnlyFollowUpContinuesTheSameQuery — Q-03/Q-23: following
// the tool's own `NEXT page knowledge_find cursor="…"` turned page 1 of
// `type=plant sort=height_cm desc limit=2` into page 2 of `limit=50`, an
// unfiltered scan of every note kind.
func TestUAT_D08_CursorOnlyFollowUpContinuesTheSameQuery(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 5; i++ {
		f.plant(i, "growing", []string{"10.0", "50.0", "30.0", "40.0", "20.0"}[i-1])
	}
	f.write("garden/loose.md", "# Just a note\n")
	d := f.deps()

	page1 := mustFind(t, d, req(withType("plant"), withSort("height_cm", "desc"), withLimit(2)))
	if page1.NextCursor == nil {
		t.Fatalf("page 1 issued no cursor: %s", Render(page1))
	}
	if got := rowPaths(page1); len(got) != 2 || got[0] != "garden/plant-0002.md" || got[1] != "garden/plant-0004.md" {
		t.Fatalf("page 1 order: %v", got)
	}

	page2 := mustFind(t, d, withCursorOnly(*page1.NextCursor))
	if got := rowPaths(page2); len(got) != 2 || got[0] != "garden/plant-0003.md" || got[1] != "garden/plant-0005.md" {
		t.Fatalf("D-08: page 2 must continue type=plant sort=height_cm desc limit=2, got %v\n%s", got, Render(page2))
	}
	for _, want := range []string{"type=plant", "sort=height_cm desc", "limit=2"} {
		if !strings.Contains(page2.QueryEcho, want) {
			t.Errorf("the page-2 echo must document the continued query (%q), got %q", want, page2.QueryEcho)
		}
	}
	if strings.Contains(Render(page2), "garden/loose.md") {
		t.Errorf("an unrelated note leaked into a typed continuation:\n%s", Render(page2))
	}
}

// TestUAT_D08_CursorWithADifferentQueryIsRefused — the other half: a cursor
// sent alongside a query it was not issued for must not be answered for
// either query.
func TestUAT_D08_CursorWithADifferentQueryIsRefused(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 3; i++ {
		f.plant(i, "growing", "10.0")
	}
	d := f.deps()
	page1 := mustFind(t, d, req(withType("plant"), withLimit(1)))
	if page1.NextCursor == nil {
		t.Fatal("no cursor")
	}
	// The same query re-sent with the cursor is fine (the other natural way to page).
	mustFind(t, d, req(withType("plant"), withLimit(1), withCursor(*page1.NextCursor)))
	// A different one is not.
	resp := mustRefuse(t, d, req(withType("plant"), withLimit(2), withCursor(*page1.NextCursor)))
	if resp.Problems[0].Code != generated.StaleCursor {
		t.Fatalf("want stale_cursor, got %+v", resp.Problems)
	}
	if !strings.Contains(resp.Problems[0].Reason, "different query") {
		t.Errorf("the refusal must say the cursor belongs to a different query: %q", resp.Problems[0].Reason)
	}
}

// TestUAT_D10_LongDirectionSpellingsAreServed — B-25/Q-08: a saved view with
// `direction: descending` was accepted at write time and refused at every
// query ("descending" is not a sort direction). The two parsers now share
// one vocabulary.
func TestUAT_D10_LongDirectionSpellingsAreServed(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.0")
	f.plant(2, "growing", "30.0")
	f.plant(3, "growing", "20.0")
	d := f.deps()

	resp := mustFind(t, d, req(withType("plant"), withSort("height_cm", "descending")))
	if got := rowPaths(resp); got[0] != "garden/plant-0002.md" || got[2] != "garden/plant-0001.md" {
		t.Fatalf("`descending` must sort descending, got %v", got)
	}
	if !strings.Contains(resp.QueryEcho, "sort=height_cm desc") {
		t.Errorf("the echo must render the canonical direction, got %q", resp.QueryEcho)
	}
	asc := mustFind(t, d, req(withType("plant"), withSort("height_cm", "ascending")))
	if got := rowPaths(asc); got[0] != "garden/plant-0001.md" {
		t.Fatalf("`ascending` must sort ascending, got %v", got)
	}
	// Anything else is still refused by name.
	mustRefuse(t, d, req(withType("plant"), withSort("height_cm", "down")))

	dir := generated.VaultFindGroupByDirectionDescending
	gb := []generated.VaultFindGroupBy{{Property: "condition", Direction: &dir}}
	r := req(withType("plant"))
	r.GroupBy = &gb
	grouped := mustFind(t, d, r)
	if !strings.Contains(grouped.QueryEcho, "group_by=condition desc") {
		t.Errorf("group_by `descending` must be served as desc, echo %q", grouped.QueryEcho)
	}
}

// TestUAT_D33_RowsWithoutAValueSortLastInBothDirections — Q-03: `sort: start
// desc` put the two notes with no value at the top, and nothing said so.
func TestUAT_D33_RowsWithoutAValueSortLastInBothDirections(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.0")
	f.plant(2, "growing", "30.0")
	f.write("garden/plant-0009.md", "---\ntype: plant\nid: PL-0009\nspecies: Fern\ncondition: growing\n---\n")
	d := f.deps()

	desc := mustFind(t, d, req(withType("plant"), withSort("height_cm", "desc")))
	if got := rowPaths(desc); got[len(got)-1] != "garden/plant-0009.md" || got[0] != "garden/plant-0002.md" {
		t.Fatalf("D-33: the value-less row must come LAST on desc, got %v", got)
	}
	asc := mustFind(t, d, req(withType("plant"), withSort("height_cm", "asc")))
	if got := rowPaths(asc); got[len(got)-1] != "garden/plant-0009.md" || got[0] != "garden/plant-0001.md" {
		t.Fatalf("the value-less row must come last on asc too, got %v", got)
	}
	if !strings.Contains(desc.QueryEcho, "no value") {
		t.Errorf("the null-ordering rule must be stated in the echo, got %q", desc.QueryEcho)
	}
}

// TestUAT_D60_EchoNamesTheSelectClause — Q-19.
func TestUAT_D60_EchoNamesTheSelectClause(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.0")
	r := req(withType("plant"))
	sel := []string{"file.mtime", "file.size", "species"}
	r.Select = &sel
	resp := mustFind(t, f.deps(), r)
	if !strings.Contains(resp.QueryEcho, "select=file.mtime,file.size,species") {
		t.Fatalf("D-60: the echo omits the projection: %q", resp.QueryEcho)
	}
}

// TestUAT_D11_NumbersBooleansAndListsAreAcceptedAsFilterLiterals — Q-21: the
// wire typing of `value` as a string made `IN` uninvokable (an array was
// rejected before the engine saw it, while the operator demanded one) and
// rejected an unquoted number for `>`.
func TestUAT_D11_NumbersBooleansAndListsAreAcceptedAsFilterLiterals(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "seedling", "10.5")
	f.plant(2, "growing", "30.25")
	f.plant(3, "dormant", "20.0")
	d := f.deps()

	call := func(raw string) (string, error) { return Call(context.Background(), d, []byte(raw)) }

	out, err := call(`{"type":"plant","filter":{"property":"height_cm","op":">","value":20}}`)
	if err != nil {
		t.Fatalf("D-11: an unquoted number was refused: %v\n%s", err, out)
	}
	if !strings.Contains(out, "plant-0002") || strings.Contains(out, "plant-0001") {
		t.Fatalf("height_cm > 20 must select plant-0002 only:\n%s", out)
	}
	if !strings.Contains(out, "height_cm > '20'") {
		t.Errorf("the echo must show the literal as the engine read it:\n%s", out)
	}

	out, err = call(`{"type":"plant","filter":{"property":"condition","op":"IN","value":["seedling","dormant"]}}`)
	if err != nil {
		t.Fatalf("D-11: IN with an array in `value` was refused: %v\n%s", err, out)
	}
	if !strings.Contains(out, "plant-0001") || !strings.Contains(out, "plant-0003") || strings.Contains(out, "plant-0002") {
		t.Fatalf("IN (seedling, dormant) must select plants 1 and 3:\n%s", out)
	}

	out, err = call(`{"type":"plant","filter":{"property":"height_cm","op":"IN","values":[10.5, 20]}}`)
	if err != nil {
		t.Fatalf("D-11: numbers inside `values` were refused: %v\n%s", err, out)
	}
	if !strings.Contains(out, "plant-0001") || !strings.Contains(out, "plant-0003") {
		t.Fatalf("IN (10.5, 20) must keep every digit and select plants 1 and 3:\n%s", out)
	}

	out, err = call(`{"type":"plant","filter":{"property":"height_cm","op":"=","value":{"n":1}}}`)
	if err == nil {
		t.Fatalf("an object literal must be refused, got:\n%s", out)
	}
	if !strings.Contains(out, "no lexical form") {
		t.Errorf("the refusal must explain why an object cannot be a literal:\n%s", out)
	}
}

// TestR3_CursorAcceptsTheSameQueryReserializedDifferently — round-3 cut list
// (2026-09-14 review): restoreCursorQuery compared the follow-up's non-cursor
// fields against the query sealed in the cursor as RAW JSON BYTES, so the SAME
// query spelled a second way was refused as StaleCursor. The comparison is now
// semantic, over the decoded structs, using the engine's own equivalences: a
// direction's long spelling (`descending` ≡ `desc`, D-10), and a field stated
// explicitly on one side against the same field left to its default on the
// other (`kind: note` ≡ omitted, `limit: 50` ≡ omitted, `detail: standard` ≡
// omitted, `explain: false` ≡ omitted).
func TestR3_CursorAcceptsTheSameQueryReserializedDifferently(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.0")
	f.plant(2, "growing", "20.0")
	f.plant(3, "growing", "30.0")
	f.plant(4, "growing", "40.0")
	f.plant(5, "growing", "50.0")
	d := f.deps()

	page1 := mustFind(t, d, req(withType("plant"), withSort("height_cm", "desc"), withLimit(2)))
	if page1.NextCursor == nil {
		t.Fatalf("page 1 issued no cursor: %s", Render(page1))
	}
	if got := rowPaths(page1); len(got) != 2 || got[0] != "garden/plant-0005.md" || got[1] != "garden/plant-0004.md" {
		t.Fatalf("page 1 order: %v", got)
	}

	t.Run("direction spelled the other valid way", func(t *testing.T) {
		// The identical query, with `descending` where page 1 said `desc`.
		// Both are valid enum members that parse() executes identically
		// (D-10), so this is a continuation, not a different query.
		page2 := mustFind(t, d, req(withType("plant"), withSort("height_cm", "descending"), withLimit(2), withCursor(*page1.NextCursor)))
		if got := rowPaths(page2); len(got) != 2 || got[0] != "garden/plant-0003.md" || got[1] != "garden/plant-0002.md" {
			t.Fatalf("the same query re-serialized must continue the page, got %v\n%s", got, Render(page2))
		}
	})

	t.Run("defaults stated explicitly", func(t *testing.T) {
		// The identical query with every default spelled out: `kind: note`,
		// `detail: standard` and `explain: false` against the omitted forms
		// page 1 sent, and the same limit the cursor remembers.
		kind := generated.VaultFindRequestKind(KindNote)
		detail := generated.VaultFindRequestDetail("standard")
		explain := false
		r := req(withType("plant"), withSort("height_cm", "desc"), withLimit(2), withCursor(*page1.NextCursor))
		r.Kind = &kind
		r.Detail = &detail
		r.Explain = &explain
		page2 := mustFind(t, d, r)
		if got := rowPaths(page2); len(got) != 2 || got[0] != "garden/plant-0003.md" || got[1] != "garden/plant-0002.md" {
			t.Fatalf("stating the defaults explicitly must continue the page, got %v\n%s", got, Render(page2))
		}
	})
}

// TestR3_CursorAcceptsASubsetOfTheRememberedQuery — the round-3 cut list's own
// example: `{cursor, limit}` after `{words, limit}` was refused as StaleCursor.
// The cursor carries the whole query (D-08), so a follow-up that re-sends only
// some of it — and contradicts none of it — is a continuation of that query,
// exactly as the cursor-only form already was.
func TestR3_CursorAcceptsASubsetOfTheRememberedQuery(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 5; i++ {
		f.plant(i, "growing", "10.0")
	}
	f.text.only = []string{
		"garden/plant-0001.md", "garden/plant-0002.md", "garden/plant-0003.md",
		"garden/plant-0004.md", "garden/plant-0005.md",
	}
	d := f.deps()

	page1 := mustFind(t, d, req(withWords("monstera"), withLimit(2)))
	if page1.NextCursor == nil {
		t.Fatalf("page 1 issued no cursor: %s", Render(page1))
	}

	// The finding's example: the cursor plus the page size, `words` left to
	// the cursor to remember.
	page2 := mustFind(t, d, req(withLimit(2), withCursor(*page1.NextCursor)))
	if got := rowPaths(page2); len(got) != 2 {
		t.Fatalf("a subset follow-up must be answered, got %d rows\n%s", len(got), Render(page2))
	}
	for _, p := range rowPaths(page2) {
		if contains(rowPaths(page1), p) {
			t.Errorf("page two repeats %s from page one", p)
		}
	}
	if !strings.Contains(page2.QueryEcho, `words="monstera"`) {
		t.Errorf("the continuation must still document the remembered words query: %q", page2.QueryEcho)
	}
}

// TestR3_CursorStillRefusesAGenuinelyDifferentQuery — the guard D-08 added
// survives the semantic comparison: any field the follow-up sends that asks
// for something the cursor does not remember is still refused by name.
func TestR3_CursorStillRefusesAGenuinelyDifferentQuery(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 5; i++ {
		f.plant(i, "growing", "10.0")
	}
	f.text.only = []string{
		"garden/plant-0001.md", "garden/plant-0002.md", "garden/plant-0003.md",
		"garden/plant-0004.md", "garden/plant-0005.md",
	}
	d := f.deps()

	page1 := mustFind(t, d, req(withWords("monstera"), withLimit(2)))
	if page1.NextCursor == nil {
		t.Fatalf("page 1 issued no cursor: %s", Render(page1))
	}

	refusals := map[string]generated.VaultFindRequest{
		"a different page size":                    req(withWords("monstera"), withLimit(5), withCursor(*page1.NextCursor)),
		"a different words":                        req(withWords("fern"), withLimit(2), withCursor(*page1.NextCursor)),
		"a narrowing the cursor does not remember": req(withType("plant"), withLimit(2), withCursor(*page1.NextCursor)),
	}
	for name, r := range refusals {
		resp := mustRefuse(t, d, r)
		if resp.Problems[0].Code != generated.StaleCursor {
			t.Errorf("%s: code = %s, want stale_cursor", name, resp.Problems[0].Code)
		}
		if !strings.Contains(resp.Problems[0].Reason, "different query") {
			t.Errorf("%s: the refusal must say the cursor belongs to a different query: %q", name, resp.Problems[0].Reason)
		}
	}
}
