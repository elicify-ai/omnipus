// Omnipus — the imported view's formulas answer to the clock at QUERY time,
// not to the clock at import time.
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

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// THE DEFECT THIS FILE EXISTS TO MAKE IMPOSSIBLE
//
// A formula is evaluated at QUERY time against a clock. The import happens
// ONCE. So `today()` inside a saved view has to mean "today when the view
// runs", and if any part of this pipeline resolved it to a date instead of
// carrying the CALL, the founder's "due in the next seven days" view would be
// frozen to the afternoon it was imported.
//
// THAT FAILURE HAS NO SYMPTOM. The view still loads, still serves, still
// returns rows, and every one of those rows is a real note. It is simply
// answering a question about a day in the past — and it goes on looking
// completely normal for as long as nobody checks the dates by hand. No error,
// no empty result, no rejected file: exactly the shape of wrong answer this
// whole importer is written against.
//
// WHICH MEANS ONE CLOCK PROVES NOTHING. A test that imports the vault and
// asserts a row set at a single instant passes identically whether the view
// carries `today()` or carries `2026-01-01` baked in at import. The only
// oracle that separates them is TWO clocks and a DIFFERENCE, so that is what
// this file asserts: the same stored view, the same notes, two instants, two
// different answers.
//
// HOW MUCH OF THIS IS THE PRODUCT AND HOW MUCH IS THE TEST. Everything except
// the eleven-line leaf loop is product code, in the order production runs it:
//
//	Run                          the real importer
//	records.LoadSchemas          the real schema loader
//	records.LoadViews            the real view loader, which RE-VALIDATES the
//	                             formulas it finds (view.go::validateViewFormulas)
//	records.NewViewFindLoader    the real view->find bridge
//	  .View(name)                the real servability decision
//	  .Formulas(name)            the real formula hand-off (ViewFormulaLoader)
//	records.ValidateFormulaSet   the real validator
//	records.NewFormulaEvaluator  the real evaluator, given the clock
//	records.Filter.MatchValue    the real comparator, with the real R-2 rule
//
// The test owns the walk over the filter's leaves and nothing else — no
// comparison logic, no date arithmetic, no absence rule. If the importer writes
// the wrong formula, the product evaluates the wrong formula faithfully and the
// row sets will not be what a person worked out by hand.
// ---------------------------------------------------------------------------

// clockFixtureDue is each fixture task's `due:` date, restated here so the
// expectations below are checkable without opening the notes. Derived by hand
// from testdata/clock/notes.
var clockFixtureDue = map[string]string{
	"Renew the domain":    "2026-01-05",
	"File the return":     "2026-01-12",
	"Ship the importer":   "2026-01-20",
	"Refactor the parser": "2026-02-28",
	"Someday maybe":       "", // no `due:` at all
}

// clockA and clockB are one week apart. Both are far in the past on purpose:
// a fixture instant that happens to be near the machine's own clock is a
// fixture that tests the machine (pkg/records/formula_eval_test.go records the
// mutation that survived exactly that mistake).
func clockA() time.Time { return time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC) }
func clockB() time.Time { return time.Date(2026, 1, 8, 9, 0, 0, 0, time.UTC) }

// TestImportedFormulaView_AnswersToTheClockAtQueryTime is the whole point of
// carrying `formulas:` at all.
//
// The view is Deadlines.base's "Due Within Seven Days":
//
//	formulas:  days_until_due: if(due, (date(due) - today()).days, "")
//	filter:    formula.days_until_due >= 0  AND  formula.days_until_due <= 7
//
// HAND-DERIVED EXPECTATIONS. days_until_due is (due - today) in whole days, so
// the window [0, 7] is "due today or in the next seven days":
//
//	                       due          at 2026-01-01   at 2026-01-08
//	Renew the domain       2026-01-05        +4  IN         -3  out
//	File the return        2026-01-12       +11  out        +4  IN
//	Ship the importer      2026-01-20       +19  out       +12  out
//	Refactor the parser    2026-02-28       +58  out       +51  out
//	Someday maybe          (none)         absent  out    absent  out
//
// So the answer MOVES: {Renew the domain} on the first day, {File the return}
// on the second. Not a superset, not a subset — a different row, which no
// frozen date could produce for both.
func TestImportedFormulaView_AnswersToTheClockAtQueryTime(t *testing.T) {
	root := clockFixture(t)
	imp := clockLoadImported(t, root)

	gotA := clockRowsAt(t, imp, clockA())
	gotB := clockRowsAt(t, imp, clockB())

	wantA := []string{"Renew the domain"}
	wantB := []string{"File the return"}

	if !clockSameRows(gotA, wantA) {
		t.Errorf("at %s the view returned %v, hand-derived expectation is %v",
			clockA().Format("2006-01-02"), gotA, wantA)
	}
	if !clockSameRows(gotB, wantB) {
		t.Errorf("at %s the view returned %v, hand-derived expectation is %v",
			clockB().Format("2006-01-02"), gotB, wantB)
	}

	// THE ASSERTION THAT KILLS THE FROZEN-DATE BUG. The two above could both
	// be satisfied by an implementation that read the clock correctly; this one
	// cannot be satisfied by one that did not. It is stated separately, and
	// with its own message, so a failure says WHICH defect it found.
	if clockSameRows(gotA, gotB) {
		t.Fatalf("the same view returned the SAME rows (%v) a week apart.\n"+
			"`today()` has been resolved to a fixed date somewhere between the `.base` file and the evaluator, "+
			"so this view is frozen to the day it was imported — it will keep returning a correct-looking answer "+
			"to a question about the past, with nothing anywhere to say so.", gotA)
	}
}

// TestImportedFormulaView_IsReachableThroughTheRealBridge is the other half of
// "done", and it is separate on purpose: a view can carry a perfectly correct
// formula and still be unservable, in which case the test above would be
// asserting the behaviour of something no user can reach.
//
// It existed as a real gap until this release. A view declaring `formulas:`
// was refused outright by the view->find bridge (ServeRefusalFormula, "a
// knowledge_find request carries no formulas"), and records.ViewFindLoader did
// not implement knowledgefind.ViewFormulaLoader either — so emitting a
// `formulas:` block would have turned four of the founder's ENABLED views into
// unservable ones. Both halves are closed now, and this test fails if either
// re-opens.
func TestImportedFormulaView_IsReachableThroughTheRealBridge(t *testing.T) {
	root := clockFixture(t)
	// The same load the clock test does, for its assertions that the view was
	// written ENABLED and with a `formulas:` block — a servable view that
	// carried no formula would satisfy everything below for the wrong reason.
	imp := clockLoadImported(t, root)

	schemas, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("reloading schemas: %v", err)
	}
	views, _, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("reloading views: %v", err)
	}
	name := clockViewName(t, views)
	if imp.Def.Name != name {
		t.Fatalf("the loaded view is named %q but the bridge is being asked about %q", imp.Def.Name, name)
	}
	loader := records.NewViewFindLoader(views)

	if _, servable := loader.View(name); !servable {
		refusal, _ := loader.ServeRefusal(name)
		t.Fatalf("the view->find bridge will not serve %q: %s\n"+
			"A view carrying formulas that cannot be served is worse than one that dropped them: "+
			"the column is there, the filter is there, and asking for it is refused.", name, refusal.String())
	}
	if !clockContains(loader.Names(), name) {
		t.Errorf("%q is servable but absent from the bridge's own list of servable views (%v) — "+
			"knowledge_find would report it as unknown", name, loader.Names())
	}
	sources, ok := loader.Formulas(name)
	if !ok || len(sources) == 0 {
		t.Fatalf("the loader handed knowledge_find no formulas for %q (ok=%v, %d source(s)); "+
			"every `formula.<name>` in the view would then resolve against nothing", name, ok, len(sources))
	}
	if !strings.Contains(sources["days_until_due"], "today()") {
		t.Errorf("the stored formula is %q — it must still CALL today(), not carry a date resolved at import time",
			sources["days_until_due"])
	}
}

// TestImportedFormulaView_TheEmptyStringBranchNarrowsAndNeverBroadens pins
// FR-105's direction on the one rewrite that changes an answer.
//
// The Obsidian source ends `, "")`, and this importer writes that branch as
// ABSENT. In JavaScript `"" >= 0` and `"" <= 7` are both TRUE — `""` coerces to
// 0 — so Obsidian's own view INCLUDES "Someday maybe", the task with no due
// date at all. Ours excludes it, because R-2 makes every comparison against an
// absent value FALSE.
//
// That is a real difference and it is the PERMITTED direction: fewer rows,
// never more. The test asserts the row is absent at BOTH clocks, so the
// narrowing is a property of the translation rather than an accident of one
// instant — and it is asserted rather than described, because a comment cannot
// fail when someone later "fixes" absence to compare as zero.
func TestImportedFormulaView_TheEmptyStringBranchNarrowsAndNeverBroadens(t *testing.T) {
	root := clockFixture(t)
	imp := clockLoadImported(t, root)

	for _, at := range []time.Time{clockA(), clockB()} {
		rows := clockRowsAt(t, imp, at)
		if clockContains(rows, "Someday maybe") {
			t.Errorf("at %s the view returned the task with NO due date (%v). "+
				"Our absent-vs-Obsidian's-\"\" difference must only ever REMOVE rows (FR-105); "+
				"returning it means absence started comparing as a value.", at.Format("2006-01-02"), rows)
		}
	}
	if clockFixtureDue["Someday maybe"] != "" {
		t.Fatal("the fixture note this test is about now has a due date, so it no longer exercises the empty branch")
	}
}

// ---------------------------------------------------------------------------
// Fixture plumbing
// ---------------------------------------------------------------------------

// clockFixture copies testdata/clock into a temp vault and imports it with the
// real importer.
func clockFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("testdata", "clock")
	for _, sub := range []string{"notes", "bases"} {
		base := filepath.Join(src, sub)
		// The notes land at the vault ROOT and the bases keep their `bases/`
		// folder, matching testdata/fr105 — a folder filter is a path from the
		// vault root, so the layout is part of what the expectations mean.
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
		t.Fatalf("importing the clock fixture: %v", err)
	}
	return root
}

// clockImported is everything the query path reads back off disk: the stored
// view, its record type's schema, its validated formula set, and the notes.
type clockImported struct {
	Def      generated.ViewDef
	Schema   *records.Schema
	Formulas *records.FormulaSet
	Notes    map[string]records.Record
}

// clockLoadImported reads all of it, through the product's own loaders.
func clockLoadImported(t *testing.T, root string) clockImported {
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

	name := clockViewName(t, views)
	sv, ok := views.Get(name)
	if !ok {
		t.Fatalf("view %q vanished between Names() and Get()", name)
	}
	def := sv.Def

	if def.Disabled != nil && *def.Disabled {
		t.Fatalf("the importer stored %q DISABLED, with untranslated: %v.\n"+
			"This view's only untranslatable content was supposed to be nothing: its formula is the shape "+
			"the two rewrites exist for, and both of its filter leaves compare that formula against a number.",
			name, clockStrings(def.Untranslated))
	}
	if def.Formulas == nil || len(*def.Formulas) == 0 {
		t.Fatalf("the importer wrote %q with NO `formulas:` block, so `formula.days_until_due` resolves against nothing", name)
	}
	if def.Type == nil {
		t.Fatalf("view %q resolved to no record type", name)
	}
	schema, ok := schemas.Get(*def.Type)
	if !ok {
		t.Fatalf("view %q names record type %q, which the vault does not declare", name, *def.Type)
	}

	// The REAL validator, over the sources actually on disk — the same call the
	// query path makes (knowledgefind/namespace.go::buildNamespace).
	set, errs := records.ValidateFormulaSet(*def.Formulas, schema)
	if len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		t.Fatalf("the formulas the importer wrote do not validate: %s", strings.Join(msgs, "; "))
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
		notes[stem] = records.ParseRecord(abs, data)
	}
	return clockImported{Def: def, Schema: schema, Formulas: set, Notes: notes}
}

// clockViewName finds the one view the fixture produces.
func clockViewName(t *testing.T, views *records.ViewSet) string {
	t.Helper()
	names := views.Names()
	if len(names) != 1 {
		t.Fatalf("the clock fixture is meant to produce exactly one view; it produced %d: %v", len(names), names)
	}
	return names[0]
}

// clockRowsAt evaluates the stored view's filter over every task at one
// instant and returns the matching notes' stems.
func clockRowsAt(t *testing.T, imp clockImported, at time.Time) []string {
	t.Helper()
	def, schema, set, notes := imp.Def, imp.Schema, imp.Formulas, imp.Notes
	if def.Filter == nil {
		t.Fatal("the stored view carries no filter, so there is nothing for two clocks to disagree about")
	}
	var out []string
	for stem, rec := range notes {
		if def.Type != nil && rec.TypeName() != *def.Type {
			continue
		}
		// ONE EVALUATOR PER RECORD, given THIS instant — which is exactly what
		// knowledgefind does (find.go builds it with queryNow(d) once per
		// query and calls Begin per candidate).
		ev := records.NewFormulaEvaluator(set, records.Comparator{}, at)
		ev.Begin(clockCandidate{rec: rec, schema: schema})
		if clockEvalNode(t, *def.Filter, schema, rec, ev) {
			out = append(out, stem)
		}
	}
	sort.Strings(out)
	return out
}

// clockEvalNode walks the filter tree. It is the only test-owned code in the
// loop and it carries NO comparison logic: every leaf is decided by
// records.Filter, and every formula value by records.FormulaEvaluator.
func clockEvalNode(t *testing.T, n generated.VaultFilterNode, schema *records.Schema, rec records.Record, ev *records.FormulaEvaluator) bool {
	t.Helper()
	switch {
	case n.All != nil:
		for _, c := range *n.All {
			if !clockEvalNode(t, c, schema, rec, ev) {
				return false
			}
		}
		return true
	case n.Any != nil:
		for _, c := range *n.Any {
			if clockEvalNode(t, c, schema, rec, ev) {
				return true
			}
		}
		return false
	case n.Not != nil:
		return !clockEvalNode(t, *n.Not, schema, rec, ev)
	case n.Property != nil:
		return clockMatchLeaf(t, n, schema, rec, ev)
	default:
		t.Fatalf("filter node is neither a combinator nor a leaf: %+v", n)
		return false
	}
}

func clockMatchLeaf(t *testing.T, n generated.VaultFilterNode, schema *records.Schema, rec records.Record, ev *records.FormulaEvaluator) bool {
	t.Helper()
	if n.Op == nil {
		t.Fatalf("leaf on %q carries no operator", *n.Property)
	}
	f := records.Filter{Property: *n.Property, Op: records.Operator(*n.Op)}
	if n.Value != nil {
		f.Literal, f.LiteralGiven = *n.Value, true
	}

	if !strings.HasPrefix(f.Property, "formula.") {
		res, err := f.Match(schema, rec)
		if err != nil {
			t.Fatalf("the product's comparator refused a filter the importer wrote (%s %s %q): %v", f.Property, f.Op, f.Literal, err)
		}
		return res.Matched
	}

	name := strings.TrimPrefix(f.Property, "formula.")
	result, ok := ev.Evaluate(name)
	if !ok {
		t.Fatalf("the evaluator does not know formula %q, which the stored view's filter names", name)
	}
	// The formula's value as a PropertyValue, through the product's own
	// converter, then through the same comparator every other leaf uses.
	prop := &records.Property{Name: f.Property, Type: clockPropertyTypeOf(t, result.Type)}
	left, converted := result.PropertyValue(f.Property)
	if !converted || result.Absent {
		left = records.PropertyValue{Property: prop, State: records.StateAbsent}
	}
	left.Property = prop
	sc := &records.Schema{
		SchemaVersion: 1,
		Type:          "formula-operand",
		Properties:    map[string]*records.Property{f.Property: prop},
		PropertyOrder: []string{f.Property},
	}
	res, err := f.MatchValue(records.Comparator{}, sc, left)
	if err != nil {
		t.Fatalf("the product's comparator refused a formula leaf the importer wrote (%s %s %q): %v", f.Property, f.Op, f.Literal, err)
	}
	return res.Matched
}

func clockPropertyTypeOf(t *testing.T, ft records.FormulaType) records.PropertyType {
	t.Helper()
	switch ft {
	case records.FormulaNumber:
		return records.TypeDecimal
	case records.FormulaText:
		return records.TypeText
	case records.FormulaBoolean:
		return records.TypeCheckbox
	case records.FormulaDate:
		return records.TypeDate
	}
	t.Fatalf("formula type %q has no property type to compare against", ft)
	return ""
}

// clockCandidate adapts one parsed record to records.FormulaCandidate.
type clockCandidate struct {
	rec    records.Record
	schema *records.Schema
}

func (c clockCandidate) FormulaProperty(name string) (records.PropertyValue, bool) {
	prop, ok := c.schema.Property(name)
	if !ok {
		return records.PropertyValue{}, false
	}
	return records.ResolveProperty(c.rec, prop), true
}

func (c clockCandidate) FormulaFileProperty(name string) (records.PropertyValue, bool) {
	pv, err := records.ResolveFileProperty(name, records.FileMeta{Path: c.rec.Path})
	if err != nil {
		return records.PropertyValue{}, false
	}
	return pv, true
}

func clockSameRows(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func clockContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func clockStrings(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}
