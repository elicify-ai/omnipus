// Omnipus — spec FR-007, FR-008, FR-020c, FR-063, FR-064, FR-076a, AC-F2..F7:
// what knowledge_find actually returns.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestFind_TypedFilterReturnsTheMatchingRecords is the end-to-end path over a
// REAL properties index: index three notes, filter, get the right one back.
func TestFind_TypedFilterReturnsTheMatchingRecords(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")
	f.plant(2, "dormant", "12.00")
	f.plant(3, "growing", "88.50")

	resp := mustFind(t, f.deps(), req(withType("plant"), withFilter(leaf("condition", "=", "growing"))))

	got := rowIDs(resp)
	if len(got) != 2 {
		t.Fatalf("rows = %v, want the two growing plants", got)
	}
	if !resp.Complete {
		t.Errorf("a clean query over a clean corpus reported complete=false: %q", deref(resp.CompleteReason))
	}
	// SELECTED is what the query selected — survivors plus anything it could not
	// read. It is NOT the narrowed candidate population: counting the record
	// that simply did not match as "selected" would make the header report it as
	// unevaluated, which is a caveat about a record that is perfectly fine.
	if resp.Counts.Selected != 2 {
		t.Errorf("counts.selected = %d, want 2 — the two it selected, none unreadable",
			resp.Counts.Selected)
	}
	if resp.Counts.Evaluated != 2 {
		t.Errorf("counts.evaluated = %d, want 2", resp.Counts.Evaluated)
	}
}

// TestFind_CaseInsensitiveEnumEquality is FR-011a: an enum resolves to its
// DECLARED spelling however the note wrote it.
func TestFind_CaseInsensitiveEnumEquality(t *testing.T) {
	f := newFixture(t)
	f.write("garden/a.md", "---\ntype: plant\nid: PL-0001\nspecies: Fern\ncondition: GROWING\n---\n")
	f.write("garden/b.md", "---\ntype: plant\nid: PL-0002\nspecies: Fern\ncondition: Growing\n---\n")

	resp := mustFind(t, f.deps(), req(withType("plant"), withFilter(leaf("condition", "=", "growing"))))
	if len(resp.Rows) != 2 {
		t.Fatalf("rows = %v, want both spellings; an enum's declared value is the one that matches", rowIDs(resp))
	}
	// And they RENDER as the declared spelling, so grouping and equality agree.
	for _, row := range resp.Rows {
		for _, c := range row.Cells {
			if c.Property == "condition" && c.Value != "growing" {
				t.Errorf("condition rendered as %q; the DECLARED spelling is what a report shows", c.Value)
			}
		}
	}
}

// TestFind_NegationIncludesAbsentButDiamondDoesNot is FR-008 against section 8
// R-2 — the correction review round 6 made as C-7, and the single most
// misreadable rule in the whole surface.
//
// `{not: {p, "=", v}}` INCLUDES the records that never said. `{p, "<>", v}`
// EXCLUDES them, because that is what SQL does with a NULL column. Getting these
// the same way round would silently change which records a caller receives.
func TestFind_NegationIncludesAbsentButDiamondDoesNot(t *testing.T) {
	f := newFixture(t)
	f.write("garden/said-growing.md", "---\ntype: plant\nid: PL-0001\nspecies: Fern\ncondition: growing\n---\n")
	f.write("garden/said-dormant.md", "---\ntype: plant\nid: PL-0002\nspecies: Fern\ncondition: dormant\n---\n")
	f.write("garden/never-said.md", "---\ntype: plant\nid: PL-0003\nspecies: Fern\n---\n")

	t.Run("NOT of = includes the record that never said", func(t *testing.T) {
		resp := mustFind(t, f.deps(),
			req(withType("plant"), withFilter(notNode(leaf("condition", "=", "growing")))))
		got := rowIDs(resp)
		if !contains(got, "PL-0003") {
			t.Errorf("rows = %v, want PL-0003 among them. FR-008: \"days I did not meditate\" "+
				"must contain the days carrying no value at all — precisely the days being asked about.", got)
		}
		if !contains(got, "PL-0002") {
			t.Errorf("rows = %v, want the dormant record too", got)
		}
		if contains(got, "PL-0001") {
			t.Errorf("rows = %v, must not contain the growing record", got)
		}
	})

	t.Run("<> excludes the record that never said", func(t *testing.T) {
		resp := mustFind(t, f.deps(),
			req(withType("plant"), withFilter(leaf("condition", "<>", "growing"))))
		got := rowIDs(resp)
		if contains(got, "PL-0003") {
			t.Errorf("rows = %v, must NOT contain PL-0003. In SQL `x <> 'v'` over a NULL x "+
				"drops the row, and adopting SQL's names without SQL's semantics is what "+
				"ruling R-B forbids. To include it, negate an = leaf.", got)
		}
		if !contains(got, "PL-0002") {
			t.Errorf("rows = %v, want the dormant record", got)
		}
	})

	t.Run("IS NULL selects exactly the record that never said", func(t *testing.T) {
		resp := mustFind(t, f.deps(), req(withType("plant"), withFilter(leaf("condition", "IS NULL"))))
		if got := rowIDs(resp); len(got) != 1 || got[0] != "PL-0003" {
			t.Errorf("rows = %v, want exactly PL-0003", got)
		}
	})
}

// TestFind_LikeIsAnchoredToTheWholeValue is FR-022b — the case UAT F-2.4 was
// told to record, because it is where a caller's intuition is wrong.
//
// `LIKE 'Fern'` selects what `= 'Fern'` selects. It is NEVER a substring match.
func TestFind_LikeIsAnchoredToTheWholeValue(t *testing.T) {
	f := newFixture(t)
	f.write("garden/a.md", "---\ntype: plant\nid: PL-0001\nspecies: Fern\n---\n")
	f.write("garden/b.md", "---\ntype: plant\nid: PL-0002\nspecies: Fernwood Giant\n---\n")

	t.Run("no wildcard is exactly equality", func(t *testing.T) {
		resp := mustFind(t, f.deps(), req(withType("plant"), withFilter(leaf("species", "LIKE", "Fern"))))
		if got := rowIDs(resp); len(got) != 1 || got[0] != "PL-0001" {
			t.Errorf("rows = %v, want only PL-0001: a pattern with no unescaped wildcard "+
				"is exactly `=`, never a substring", got)
		}
	})

	t.Run("a trailing wildcard matches the prefix", func(t *testing.T) {
		resp := mustFind(t, f.deps(), req(withType("plant"), withFilter(leaf("species", "LIKE", "Fern%"))))
		if got := rowIDs(resp); len(got) != 2 {
			t.Errorf("rows = %v, want both", got)
		}
	})
}

// TestFind_BrokenValueIsNamedAndExcluded is FR-025/FR-026: a record whose value
// cannot be read is REPORTED BY NAME, and the completeness verdict says so.
//
// It is the failure mode the whole design exists to remove — the alternative is
// a confident total over the records that happened to parse.
func TestFind_BrokenValueIsNamedAndExcluded(t *testing.T) {
	f := newFixture(t)
	f.write("garden/ok.md", "---\ntype: plant\nid: PL-0001\nspecies: Fern\nheight_cm: 41.25\n---\n")
	f.write("garden/broken.md", "---\ntype: plant\nid: PL-0052\nspecies: Fern\nheight_cm: 50k\n---\n")

	resp := mustFind(t, f.deps(), req(withType("plant"), withFilter(leaf("height_cm", ">=", "10"))))

	if resp.Complete {
		t.Errorf("a corpus with an unreadable value reported complete=true")
	}
	if len(resp.Problems) == 0 {
		t.Fatalf("the unreadable record was excluded and NOT reported — a silent wrong answer")
	}
	found := false
	for _, p := range resp.Problems {
		if contains(p.Records, "PL-0052") {
			found = true
			// spec 4.2's own shape: the offending literal AND the shape that
			// was expected, in one line, with the remedy attached.
			if !strings.Contains(p.Reason, "50k") {
				t.Errorf("the problem does not quote what the file actually holds: %q. "+
					"\"height_cm does not conform\" names the fault and withholds every "+
					"fact needed to fix it.", p.Reason)
			}
			if !strings.Contains(p.Reason, "decimal") {
				t.Errorf("the problem does not state what was expected: %q", p.Reason)
			}
			if p.Fix == nil || *p.Fix == "" {
				t.Errorf("the problem names no fix")
			}
		}
	}
	if !found {
		t.Errorf("PL-0052 is not named in the problem list: %+v", resp.Problems)
	}
	if contains(rowIDs(resp), "PL-0052") {
		t.Errorf("the unreadable record was returned as a row as well as reported")
	}
}

// TestFind_WordsIntersectWithTheTypedFilter is AC-F2's composition rule: the
// answer is the INTERSECTION, never the union.
func TestFind_WordsIntersectWithTheTypedFilter(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")
	f.plant(2, "dormant", "12.00")
	f.plant(3, "growing", "88.50")

	// The text index matches plants 1 and 2; the filter matches 1 and 3.
	f.text.only = []string{"garden/plant-0001.md", "garden/plant-0002.md"}

	words := "monstera"
	r := req(withType("plant"), withFilter(leaf("condition", "=", "growing")))
	r.Words = &words

	resp := mustFind(t, f.depsWithText(), r)
	got := rowIDs(resp)
	if len(got) != 1 || got[0] != "PL-0001" {
		t.Errorf("rows = %v, want only PL-0001 — the intersection. A record inside the word "+
			"set but failing the filter must be ABSENT, and one matching the filter but "+
			"outside the word set must be ABSENT.", got)
	}
}

// TestFind_WordSearchFanoutTruncationIsReportedNotSilent is F6 (code review
// A). textFanout is max(200, limit*20): for a small requested page (limit=5
// here, fanout=200), a corpus with MORE than 200 documents matching `words`
// cannot be answered from a single fanout, and the typed filter below only
// ever sees the ones that fit. Before the fix this was invisible: the same
// shape as a genuine zero-hit answer.
//
// None of the 201 word-matched paths here need to correspond to real plant
// records — F6 is entirely about findRecords' OWN detection of the fanout
// being exhausted, which happens before the typed filter or the properties
// store ever runs, so this is a fast, pure-Go fixture despite exercising a
// 200-document-wide bound.
func TestFind_WordSearchFanoutTruncationIsReportedNotSilent(t *testing.T) {
	f := newFixture(t)

	const n = 201 // one past textFanout's 200-match floor
	only := make([]string, 0, n)
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("garden/word-match-%04d.md", i)
		only = append(only, p)
		f.text.hits[p] = TextHit{Path: p, SourceHash: "h", Score: float64(n - i)}
	}
	f.text.only = only

	words := "meeting"
	r := req(withType("plant"))
	r.Words = &words
	// textFanout floors at 200 only for limit <= 10 (limit*20 < 200); the
	// default limit (50) would ask for a fanout of 1000, which 201 hits does
	// not cross. A small explicit limit keeps the fixture at the 200-hit
	// floor instead of needing 1001 synthetic hits.
	small := 5
	r.Limit = &small
	resp := mustFind(t, f.depsWithText(), r)

	if resp.Complete {
		t.Fatal("Complete = true after the word-search fanout was exhausted " +
			"(201 matches against a 200-match fanout) — indistinguishable from " +
			"a genuine zero-hit answer, which is the F6 defect")
	}
	if deref(resp.CompleteReason) == "" {
		t.Error("CompleteReason is empty despite Complete=false")
	}
	found := false
	for _, p := range resp.Problems {
		if p.Code == generated.TextSearchTruncated {
			found = true
			if p.Fix == nil || strings.TrimSpace(*p.Fix) == "" {
				t.Error("the text_search_truncated problem names no fix")
			}
		}
	}
	if !found {
		t.Errorf("no text_search_truncated problem in the response; problems = %+v", resp.Problems)
	}
	// This must NOT be the zero-hit path: NearestTerms is for a query that
	// genuinely found nothing in a corpus this layer finished searching, and
	// stating it here would claim a completeness this answer does not have.
	if resp.NearestTerms != nil {
		t.Error("NearestTerms is set on a truncated answer — that path is for a genuine, " +
			"fully-searched zero-hit answer, not one that gave up partway through")
	}
}

// TestFind_ZeroHitsReportsTheVocabularyAndStops is FR-114/FR-115 and AC-F4.
func TestFind_ZeroHitsReportsTheVocabularyAndStops(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")
	f.text.only = nil
	f.text.terms = []generated.VaultTermCount{
		{Term: "monstera", Documents: 412},
		{Term: "monsteras", Documents: 37},
	}

	words := "monsterra"
	r := req(withType("plant"))
	r.Words = &words
	resp := mustFind(t, f.depsWithText(), r)

	// AC-F4: a zero-hit answer is COMPLETE. It is not an error and not a
	// partial answer — there is genuinely nothing that matched.
	if !resp.Complete {
		t.Errorf("a zero-hit answer reported complete=false: %q", deref(resp.CompleteReason))
	}
	out := Render(resp)
	if !strings.Contains(out, "0 records matched") {
		t.Errorf("a zero-hit answer does not say so plainly:\n%s", out)
	}
	if !strings.Contains(out, "monstera (412)") {
		t.Errorf("the vocabulary the index holds is not reported:\n%s", out)
	}
	// It must NOT have broadened the query on the caller's behalf.
	if len(resp.Rows) != 0 {
		t.Errorf("a zero-hit search returned %d rows; the system reports the terms it holds "+
			"and STOPS. A user who searched for one thing and received results for a "+
			"broader thing has been given a wrong answer with no error channel.", len(resp.Rows))
	}
}

// TestFind_TasksAreCheckboxRowsWithLineNumbers is FR-076a and AC-F7.
func TestFind_TasksAreCheckboxRowsWithLineNumbers(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")
	f.plant(2, "dormant", "12.00")

	kind := generated.VaultFindRequestKind(KindTask)
	resp := mustFind(t, f.deps(), generated.VaultFindRequest{Kind: &kind})

	if len(resp.Rows) != 4 {
		t.Fatalf("rows = %d, want 4 — two checkboxes in each of two notes. "+
			"A task row is one checkbox LINE, not one note.", len(resp.Rows))
	}
	open, done := 0, 0
	for _, row := range resp.Rows {
		if row.Line == nil || *row.Line < 1 {
			t.Errorf("a task row carries no line number: %+v", row)
			continue
		}
		if row.Status == nil {
			t.Fatalf("a task row carries no status: %+v", row)
		}
		switch *row.Status {
		case "open":
			open++
		case "done":
			done++
		}
		if row.Text == nil || *row.Text == "" {
			t.Errorf("a task row carries no text: %+v", row)
		}
	}
	if open != 2 || done != 2 {
		t.Errorf("open=%d done=%d, want 2 and 2", open, done)
	}

	// The line number must REACH THE RENDERED TEXT, or a reader can still
	// mistake the row for the note.
	out := Render(resp)
	if !strings.Contains(out, ":15") && !strings.Contains(out, ":16") {
		t.Errorf("no line number reaches the rendered rows:\n%s", out)
	}
	if !strings.Contains(out, "repot in spring") {
		t.Errorf("the checkbox text does not reach the rendered rows:\n%s", out)
	}
}

// TestFind_TaskKindRefusesArgumentsItCannotHonour is code-review-A F2.
//
// Before the fix, `Find` routed on `q.kind == KindTask` BEFORE `filter` and
// `words` (among others) were consumed, and `findTasks` read only the
// selector, limit and cursor. `filter` was validated against the schema and
// then dropped on the floor — this exact request used to return EVERY
// checkbox row in scope for type=plant, with `complete: true` and
// `query_echo` claiming the filter and the words had run. The only prior
// `kind=task` test (`TestFind_TasksAreCheckboxRowsWithLineNumbers`) passes
// `Kind` alone, so it could not and did not catch this — this test supplies
// the ignored arguments.
func TestFind_TaskKindRefusesArgumentsItCannotHonour(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")
	f.plant(2, "dormant", "12.00")

	kind := generated.VaultFindRequestKind(KindTask)
	words := "budget"
	r := req(withType("plant"), withFilter(leaf("condition", "=", "growing")))
	r.Kind = &kind
	r.Words = &words

	resp := mustRefuse(t, f.deps(), r)

	if len(resp.Problems) == 0 {
		t.Fatalf("a refusal carries no problem: %+v", resp)
	}
	reason := resp.Problems[0].Reason
	if !strings.Contains(reason, "filter") {
		t.Errorf("the refusal does not name filter, the argument it cannot honour: %q", reason)
	}
	if !strings.Contains(reason, "words") {
		t.Errorf("the refusal does not name words, the argument it cannot honour: %q", reason)
	}
	if resp.Problems[0].Fix == nil || *resp.Problems[0].Fix == "" {
		t.Errorf("the refusal names no remedy")
	}
}

// TestFind_TaskKindHonoursTypeAlone is the companion to the refusal test: a
// `kind=task` request scoped only by `type` (which reaches the checkbox
// stream through the same Selector every other kind uses) is NOT refused.
func TestFind_TaskKindHonoursTypeAlone(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	kind := generated.VaultFindRequestKind(KindTask)
	r := req(withType("plant"))
	r.Kind = &kind

	resp := mustFind(t, f.deps(), r)
	if len(resp.Rows) != 2 {
		t.Fatalf("rows = %d, want 2 — a type-scoped task query must still run", len(resp.Rows))
	}
}

// TestFind_TaskKindReportsStalenessAgainstTheTextIndex is code-review-A F11.
//
// Before the fix, the task path hard-coded `Index.Agreeing: len(rows)` and
// never called `d.Text.SourceHash`, despite `TaskHit` carrying a
// `SourceHash` exactly the way `Candidate` does. FR-020c's freshness
// comparison is per RETURNED record and is not scoped to the record path
// (FR-020c1 scopes it to what a query returned, not to which kind returned
// it) — so a stale task row rendered as agreeing, with no `stale` flag and
// no problem entry: reported fresh while genuinely stale.
//
// This test makes the index GENUINELY disagree with disk — the text index's
// stored hash for the note is deliberately wrong — and asserts the response
// still claims agreement only if the defect is present.
func TestFind_TaskKindReportsStalenessAgainstTheTextIndex(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	// The properties index (and the note on disk) hold whatever f.write
	// computed as the real source hash. The text index is made to disagree —
	// simulating a note that changed since the text index last saw it.
	f.text.hits["garden/plant-0001.md"] = TextHit{
		Path: "garden/plant-0001.md", SourceHash: "deliberately-stale-hash", Score: 1,
	}

	kind := generated.VaultFindRequestKind(KindTask)
	resp := mustFind(t, f.deps(), generated.VaultFindRequest{Kind: &kind})

	if len(resp.Rows) != 2 {
		t.Fatalf("rows = %d, want 2 — both checkboxes of the one note", len(resp.Rows))
	}
	for _, row := range resp.Rows {
		if row.Stale == nil || !*row.Stale {
			t.Errorf("row for %s is not flagged stale despite the two indexes disagreeing: %+v",
				row.Path, row)
		}
	}
	if resp.Index == nil || resp.Index.Agreeing != 0 {
		t.Errorf("index.agreeing = %v, want 0 — the properties index and the text index "+
			"disagree about every row returned here", resp.Index)
	}
	if resp.Complete {
		t.Errorf("a response whose rows are all stale reported complete=true: %q", deref(resp.CompleteReason))
	}
	found := false
	for _, p := range resp.Problems {
		if p.Code == generated.StaleRecord && contains(p.Records, "garden/plant-0001.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("no stale_record problem names the note: %+v", resp.Problems)
	}
}

// TestFind_LimitIsClampedAndTheClampIsReported is FR-063.
func TestFind_LimitIsClampedAndTheClampIsReported(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	over := 5000
	r := req(withType("plant"))
	r.Limit = &over
	resp := mustFind(t, f.deps(), r)

	if resp.LimitClamped == nil || !*resp.LimitClamped {
		t.Fatalf("limit=5000 was not reported as clamped")
	}
	if resp.LimitApplied == nil || *resp.LimitApplied != MaxLimit {
		t.Errorf("limit_applied = %v, want %d", resp.LimitApplied, MaxLimit)
	}
	out := Render(resp)
	if !strings.Contains(out, "clamped from 5000") {
		t.Errorf("the clamp is not visible in the rendered response, so a caller would have "+
			"to make a second call to discover it:\n%s", out)
	}
}

// TestFind_PagingIsCursorBasedAndAStaleCursorIsAnError is the pagination
// contract: a cursor that cannot be honoured is an ERROR, never a silent
// restart. A silent restart returns page one while the caller believes it is
// reading page four.
func TestFind_PagingIsCursorBasedAndAStaleCursorIsAnError(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 5; i++ {
		f.plant(i, "growing", fmt.Sprintf("%d.00", i))
	}

	limit := 2
	r := req(withType("plant"))
	r.Limit = &limit
	first := mustFind(t, f.deps(), r)
	if len(first.Rows) != 2 || first.NextCursor == nil {
		t.Fatalf("first page: rows=%d cursor=%v", len(first.Rows), first.NextCursor)
	}

	r.Cursor = first.NextCursor
	second := mustFind(t, f.deps(), r)
	if len(second.Rows) != 2 {
		t.Fatalf("second page rows = %d, want 2", len(second.Rows))
	}
	for _, id := range rowIDs(second) {
		if contains(rowIDs(first), id) {
			t.Errorf("page two repeats %s from page one", id)
		}
	}

	t.Run("a cursor from another epoch is refused", func(t *testing.T) {
		d := f.deps()
		d.Epoch = 9999
		resp := mustRefuse(t, d, r)
		if resp.Problems[0].Code != generated.StaleCursor {
			t.Errorf("code = %s, want stale_cursor", resp.Problems[0].Code)
		}
		if !strings.Contains(resp.Problems[0].Reason, "8814") ||
			!strings.Contains(resp.Problems[0].Reason, "9999") {
			t.Errorf("the refusal names neither epoch: %q", resp.Problems[0].Reason)
		}
	})
}

// TestFind_StaleRowIsReturnedAndFlagged is FR-020c and AC-F5.
//
// The row is RETURNED, not dropped: dropping it would make the answer quietly
// smaller with nothing saying so. It is flagged, named in PROBLEMS, and the
// verdict becomes no.
func TestFind_StaleRowIsReturnedAndFlagged(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	// The TEXT index has seen different bytes from the properties index. That is
	// the divergence FR-020c detects, in the direction that matters.
	h := f.text.hits["garden/plant-0001.md"]
	h.SourceHash = "deadbeef"
	f.text.hits["garden/plant-0001.md"] = h
	f.text.only = []string{"garden/plant-0001.md"}

	words := "monstera"
	r := req(withType("plant"))
	r.Words = &words
	resp := mustFind(t, f.depsWithText(), r)

	if len(resp.Rows) != 1 {
		t.Fatalf("the stale row was dropped instead of flagged; rows=%d", len(resp.Rows))
	}
	if resp.Rows[0].Stale == nil || !*resp.Rows[0].Stale {
		t.Errorf("the row is not marked stale")
	}
	if resp.Complete {
		t.Errorf("a stale row did not move the completeness verdict to no")
	}
	if resp.Index == nil || resp.Index.Agreeing != 0 || resp.Index.Returned != 1 {
		t.Errorf("index state = %+v, want 0 of 1 agreeing", resp.Index)
	}
	found := false
	for _, p := range resp.Problems {
		if p.Code == generated.StaleRecord {
			found = true
			// The wording must not claim which side is behind — the comparison
			// establishes disagreement, not direction.
			if strings.Contains(p.Reason, "properties index is stale") {
				t.Errorf("the problem claims a direction the mechanism cannot establish: %q", p.Reason)
			}
			if !strings.Contains(p.Reason, "disagree") {
				t.Errorf("the problem does not report a disagreement: %q", p.Reason)
			}
		}
	}
	if !found {
		t.Errorf("no stale_record problem was raised: %+v", resp.Problems)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

var _ = context.Background
