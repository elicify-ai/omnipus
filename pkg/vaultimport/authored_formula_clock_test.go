// Omnipus — a filter clause the importer AUTHORED a formula for answers to the
// clock at QUERY time, and the clause it stands for really decides rows.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS BESIDE formula_query_clock_test.go
//
// That file proves the clock survives for a formula THE OPERATOR WROTE. This
// one proves it for a formula THE IMPORTER WROTE, which is a different claim
// with the same failure mode and no symptom either way: if any step between the
// `.base` file and the evaluator resolved `today()` to a date, the view would
// keep returning a correct-LOOKING answer to a question about the afternoon it
// was imported. No error, no empty result, no rejected file.
//
// ONE CLOCK PROVES NOTHING, so this asserts two clocks and a DIFFERENCE.
//
// AND A DIFFERENCE IS NOT ENOUGH EITHER, which is the second thing this
// fixture is built for. A view whose authored clauses decide nothing would pass
// a clock test that only compared it to itself, so the fixture puts a note on
// the wrong side of EACH clause and asserts it is excluded:
//
//	note                    close_date    at 2026-01-15   at 2026-02-15   what its exclusion proves
//	Closes this January     2026-01-20         IN              out        —
//	Closes this February    2026-02-10         out             IN         —
//	Closed last January     2025-01-20         out             out        the `.year` clause is live
//	Closes in December      2026-12-05         out             out        the `.month` clause is live
//	No close date at all    (none)             out             out        an absent operand is FALSE, not true
//
// Every expectation above is derived from the `.base` file's own Obsidian
// semantics, by hand, before any of it was run.
//
// HOW MUCH OF THIS IS PRODUCT CODE. Everything except the tree walk, which is
// shared with formula_query_clock_test.go and carries no comparison logic of
// its own: Run (the real importer), records.LoadSchemas / LoadViews (the real
// loaders, which RE-VALIDATE the formulas they find), records.ValidateFormulaSet
// (the real validator), records.NewFormulaEvaluator (the real evaluator, given
// the clock) and records.Filter.MatchValue (the real comparator, with the real
// R-2 absence rule).
// ---------------------------------------------------------------------------

// authoredClockA and authoredClockB are a month apart, and both are far from
// any machine's own clock so that a fixture instant can never accidentally
// agree with `time.Now()`.
func authoredClockA() time.Time { return time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC) }
func authoredClockB() time.Time { return time.Date(2026, 2, 15, 9, 0, 0, 0, time.UTC) }

// authoredFixtureCloseDates restates each fixture note's `close_date:` so the
// expectations below are checkable without opening five files.
var authoredFixtureCloseDates = map[string]string{
	"Closes this January":  "2026-01-20",
	"Closes this February": "2026-02-10",
	"Closed last January":  "2025-01-20",
	"Closes in December":   "2026-12-05",
	"No close date at all": "",
}

// TestAuthoredFormula_AnswersToTheClockAtQueryTime is the whole point of
// authoring the formula rather than resolving the expression at import.
func TestAuthoredFormula_AnswersToTheClockAtQueryTime(t *testing.T) {
	imp := authoredLoadImported(t, authoredFixture(t))

	gotA := authoredRowsAt(t, imp, authoredClockA())
	gotB := authoredRowsAt(t, imp, authoredClockB())

	wantA := []string{"Closes this January"}
	wantB := []string{"Closes this February"}

	if !clockSameRows(gotA, wantA) {
		t.Errorf("at %s the view returned %v, hand-derived expectation is %v",
			authoredClockA().Format("2006-01-02"), gotA, wantA)
	}
	if !clockSameRows(gotB, wantB) {
		t.Errorf("at %s the view returned %v, hand-derived expectation is %v",
			authoredClockB().Format("2006-01-02"), gotB, wantB)
	}

	// THE ASSERTION THAT KILLS THE FROZEN-DATE BUG. Stated separately, with its
	// own message, so a failure says WHICH defect it found.
	if clockSameRows(gotA, gotB) {
		t.Fatalf("the same view returned the SAME rows (%v) a month apart.\n"+
			"`today()` inside the formula this importer AUTHORED has been resolved to a fixed date somewhere "+
			"between the `.base` file and the evaluator, so the view is frozen to the day it was imported — "+
			"it will keep returning a correct-looking answer to a question about the past, with nothing to say so.", gotA)
	}
}

// TestAuthoredFormula_EachAuthoredClauseActuallyDecidesRows is the
// falsifiability half, asserted rather than logged.
//
// A grader that cannot fail is not a grader. Each note below sits on the wrong
// side of exactly one clause, so if that clause were dropped, mistranslated or
// authored against the wrong property, the note would appear in the answer and
// this test would say which clause went.
func TestAuthoredFormula_EachAuthoredClauseActuallyDecidesRows(t *testing.T) {
	imp := authoredLoadImported(t, authoredFixture(t))
	at := authoredClockA()
	rows := authoredRowsAt(t, imp, at)

	for _, tc := range []struct{ note, clause string }{
		{"Closed last January", "`date(close_date).year == today().year` — same MONTH as the clock, a different YEAR"},
		{"Closes in December", "`date(close_date).month == today().month` — same YEAR as the clock, a different MONTH"},
		{"No close date at all", "`close_date != \"\"`, and R-2's rule that a comparison against an absent operand is FALSE"},
	} {
		if clockContains(rows, tc.note) {
			t.Errorf("at %s the view returned %q. It must not: the clause that excludes it is %s.\n  rows: %v",
				at.Format("2006-01-02"), tc.note, tc.clause, rows)
		}
	}

	// AND THE POPULATION IS BIGGER THAN THE ANSWER, which is what makes the
	// three exclusions above measurements rather than coincidences. Five deals
	// exist; one is returned.
	if len(imp.Notes) < 5 {
		t.Fatalf("the fixture holds %d notes, so the exclusions above prove nothing", len(imp.Notes))
	}
	if len(rows) != 1 {
		t.Errorf("the view returned %d rows at %s, hand-derived expectation is exactly 1 (%v)",
			len(rows), at.Format("2006-01-02"), rows)
	}
}

// TestAuthoredFormula_IsWrittenAsSourceAndNamespaced pins the two properties of
// the authored formula that an operator can check by opening the file: it still
// CALLS today() rather than carrying a date, and its name is in the reserved
// namespace rather than anywhere it could be mistaken for one of his.
func TestAuthoredFormula_IsWrittenAsSourceAndNamespaced(t *testing.T) {
	root := authoredFixture(t)
	imp := authoredLoadImported(t, root)

	if imp.Def.Formulas == nil || len(*imp.Def.Formulas) != 2 {
		t.Fatalf("the view declares %v formulas, want the 2 this importer authored for its two expression clauses", imp.Def.Formulas)
	}
	for name, src := range *imp.Def.Formulas {
		if !strings.HasPrefix(name, authoredFormulaPrefix) {
			t.Errorf("authored formula %q is not in the reserved `%s` namespace — a name outside it can collide with one the operator writes later",
				name, authoredFormulaPrefix)
		}
		if !strings.Contains(src, "today()") {
			t.Errorf("formula %q is stored as %q — it must still CALL today(), not carry a date resolved at import time", name, src)
		}
	}

	// The clause the operator wrote must be recoverable from the file
	// VERBATIM, because that is what lets him check the translation himself.
	sources := make([]string, 0, 2)
	for _, s := range *imp.Def.Formulas {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	want := []string{"date(close_date).month == today().month", "date(close_date).year == today().year"}
	if !clockSameRows(sources, want) {
		t.Errorf("the stored sources are %v, want the operator's own two clauses verbatim %v", sources, want)
	}
}

// TestAuthoredFormula_TheViewIsEnabledAndNamesTheAuthoringInItsHeader is the
// reporting condition, asserted at the surface that survives.
//
// A console report scrolls away. An operator comparing this view against the
// `.base` file it came from is holding the FILE, so the file is where he has to
// be able to see that a definition in it is not his.
func TestAuthoredFormula_TheViewIsEnabledAndNamesTheAuthoringInItsHeader(t *testing.T) {
	root := authoredFixture(t)
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("re-importing: %v", err)
	}

	var vo *ViewOutcome
	for i := range rep.Bases {
		for j := range rep.Bases[i].Views {
			if rep.Bases[i].Views[j].DisplayName == "Closing This Month" {
				vo = &rep.Bases[i].Views[j]
			}
		}
	}
	if vo == nil {
		t.Fatal("the fixture produced no view named \"Closing This Month\"")
	}
	if vo.Disabled {
		t.Fatalf("the view is DISABLED; losses: %v", vo.Losses)
	}
	if len(vo.Losses) != 0 {
		t.Errorf("the view reports losses although both expression clauses were carried: %v", vo.Losses)
	}
	if len(vo.AuthoredFormulas) != 2 {
		t.Fatalf("the outcome names %d authored formulas, want 2: %v", len(vo.AuthoredFormulas), vo.AuthoredFormulas)
	}

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(vo.OutputRelPath))) //nolint:gosec // a path this test's own import produced
	if err != nil {
		t.Fatalf("reading the produced view: %v", err)
	}
	header := string(data)
	for _, want := range []string{
		"AUTHORED BY THE IMPORTER",
		"date(close_date).year == today().year",
		"date(close_date).month == today().month",
		authoredFormulaPrefix,
	} {
		if !strings.Contains(header, want) {
			t.Errorf("the produced view file does not tell the operator %q. A definition he did not write, arriving unannounced in a file he owns, is indistinguishable from a bug.\n%s", want, header)
		}
	}
}

// ---------------------------------------------------------------------------
// Fixture plumbing — the same shape as clockFixture, over its own testdata.
// ---------------------------------------------------------------------------

// authoredFixture copies testdata/authoredfilter into a temp vault and imports
// it with the real importer.
func authoredFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("testdata", "authoredfilter")
	for _, sub := range []string{"notes", "bases"} {
		// The notes land at the vault ROOT and the bases keep their `bases/`
		// folder, matching testdata/clock and testdata/fr105.
		base := filepath.Join(src, sub)
		relRoot := base
		if sub == "bases" {
			relRoot = src
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Fatalf("reading fixture %s: %v", sub, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			from := filepath.Join(base, e.Name())
			data, readErr := os.ReadFile(from) //nolint:gosec // committed fixture path
			if readErr != nil {
				t.Fatalf("reading %s: %v", from, readErr)
			}
			rel, relErr := filepath.Rel(relRoot, from)
			if relErr != nil {
				t.Fatalf("relativising %s: %v", from, relErr)
			}
			dst := filepath.Join(root, rel)
			if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
				t.Fatalf("creating %s: %v", filepath.Dir(dst), mkErr)
			}
			if wErr := os.WriteFile(dst, data, 0o644); wErr != nil { //nolint:gosec // test fixture
				t.Fatalf("writing %s: %v", dst, wErr)
			}
		}
	}
	if _, err := Run(root, true); err != nil {
		t.Fatalf("importing the authored-filter fixture: %v", err)
	}
	return root
}

// authoredLoadImported reads back everything the query path needs, through the
// product's own loaders — the same sequence clockLoadImported uses, over this
// fixture's one view.
func authoredLoadImported(t *testing.T, root string) clockImported {
	t.Helper()
	schemas, schemaRep, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("reloading the schemas this run wrote: %v", err)
	}
	if !schemaRep.OK() {
		t.Fatalf("the importer wrote schemas the real loader rejects: %v", schemaRep.Rejections)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("reloading the views this run wrote: %v", err)
	}
	if !viewRep.OK() {
		for _, rej := range viewRep.Rejections {
			t.Errorf("the importer wrote a view the real loader rejects: %s", rej.String())
		}
		t.FailNow()
	}
	names := views.Names()
	if len(names) != 1 {
		t.Fatalf("the authored-filter fixture is meant to produce exactly one view; it produced %d: %v", len(names), names)
	}
	sv, ok := views.Get(names[0])
	if !ok {
		t.Fatalf("view %q vanished between Names() and Get()", names[0])
	}
	def := sv.Def
	if def.Disabled != nil && *def.Disabled {
		t.Fatalf("the importer stored %q DISABLED, with untranslated: %v.\n"+
			"Both of this view's expression clauses are exactly the shape the authored-formula path exists for.",
			names[0], clockStrings(def.Untranslated))
	}
	if def.Formulas == nil || len(*def.Formulas) == 0 {
		t.Fatalf("the importer wrote %q with NO `formulas:` block, so nothing was authored and every assertion below would pass vacuously", names[0])
	}
	if def.Type == nil {
		t.Fatalf("view %q resolved to no record type", names[0])
	}
	schema, ok := schemas.Get(*def.Type)
	if !ok {
		t.Fatalf("view %q names record type %q, which the vault does not declare", names[0], *def.Type)
	}
	if p, found := schema.Property("close_date"); !found || p.Type != records.TypeDate {
		t.Fatalf("the fixture's `close_date` was inferred as %+v, but these expectations are about DATE arithmetic", p)
	}

	// The REAL validator, over the sources actually on disk — the same call the
	// query path makes.
	set, errs := records.ValidateFormulaSet(*def.Formulas, schema)
	if len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		t.Fatalf("the formulas the importer AUTHORED do not validate: %s", strings.Join(msgs, "; "))
	}

	notes := map[string]records.Record{}
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning the imported vault: %v", err)
	}
	for _, abs := range inv.Notes {
		data, readErr := os.ReadFile(abs) //nolint:gosec // path from this run's own scan
		if readErr != nil {
			t.Fatalf("reading %s: %v", abs, readErr)
		}
		stem := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
		if _, known := authoredFixtureCloseDates[stem]; !known {
			t.Fatalf("the fixture holds a note %q the expectation table does not mention", stem)
		}
		notes[stem] = records.ParseRecord(abs, data)
	}
	if len(notes) != len(authoredFixtureCloseDates) {
		t.Fatalf("read %d notes, the expectation table names %d", len(notes), len(authoredFixtureCloseDates))
	}
	return clockImported{Def: def, Schema: schema, Formulas: set, Notes: notes}
}

// authoredRowsAt evaluates the stored view at one instant, through the shared
// walk in formula_query_clock_test.go — deliberately the SAME walk, so a change
// to how a leaf is decided moves both tests together.
func authoredRowsAt(t *testing.T, imp clockImported, at time.Time) []string {
	t.Helper()
	return clockRowsAt(t, imp, at)
}
