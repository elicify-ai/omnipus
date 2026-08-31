// Omnipus — W3: spelling an Obsidian bare-property truthiness guard on a TEXT
// property as `P != ""`, and the three-state proof that the two coincide.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"encoding/json"
	"fmt"
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
// WHAT IS BEING CLAIMED, AND WHAT WOULD FALSIFY IT
//
// The claim is an EQUIVALENCE, not a safe approximation: on a single-valued
// text property, Obsidian's bare-`P` truthiness test and this product's
// `P != ""` select the same records in every state a text property can be in.
// FR-007a is what makes that a three-state question rather than a two-state
// one — `""` stays PRESENT on text, so absent and empty are DIFFERENT states
// and a translation that gets one right and the other wrong is wrong.
//
// A SUBSET WOULD NOT BE GOOD ENOUGH, which is why the tests below are equality
// assertions and not one-sided ones. A subset narrows in positive position and
// BROADENS under a `not:` — that is exactly why view_write.go refuses the
// `!= ""` LEAF inside a negation. An equivalence has nothing to invert, and
// TestW3_TheGuardIsExactUnderANegationToo is what turns that sentence into
// something that can fail.
//
// WHAT IS PRODUCT CODE HERE. Everything except the tree walk:
//
//	Run                          the real importer
//	records.LoadSchemas          the real schema loader
//	records.LoadViews            the real view loader (re-validates formulas)
//	records.NewViewFindLoader    the real view->find bridge
//	records.ValidateFormulaSet   the real formula validator
//	records.NewFormulaEvaluator  the real evaluator, given the clock
//	records.Filter.Match(Value)  the real comparator, with the real R-2 rule
//
// The tree walk is test-owned, and its `not` is deliberately the ENGINE's rule
// rather than the one the older fr105 harness in this package uses. They are
// not the same rule: knowledgefind's tree.go evaluates a `nodeNot` as a bare
// `!inner.matched` with no absence re-admission, while fr105EvalNode pushes the
// negation down into records.Filter.Negate, where FR-008 DOES re-admit absent
// records. Grading a negated view through the second one would grade a
// behaviour no query has. w3Eval uses the first.
// ---------------------------------------------------------------------------

// w3Schema declares one property of every type the guard rule has to
// distinguish, so a case can move a single property's type and watch the
// rewrite stop firing.
func w3Schema(t *testing.T) *records.Schema {
	t.Helper()
	schema, rejection := records.ParseSchema("thing.yaml", []byte(`schema_version: 1
type: thing
properties:
  due:     { type: date }
  note:    { type: text }
  notes:   { type: text, many: true }
  cost:    { type: decimal }
  done:    { type: checkbox }
`))
	if rejection != nil {
		t.Fatalf("the fixture schema was rejected: %+v", rejection)
	}
	return schema
}

// TestW3_SpellsATextTruthinessGuardAndNothingElse pins the rewrite's trigger.
//
// The two failure directions are not symmetric and the table covers both. A
// rewrite that does not fire costs a named loss and a disabled view — visible,
// annoying, safe. A rewrite that fires on the wrong type changes a value with
// no error, no refusal and no loss line: the view loads, the column renders,
// every row has a number in it, and the number is wrong on exactly the records
// the guard existed for. So every "must not" row below is a type on which
// `P != ""` is NOT Obsidian's truthiness.
func TestW3_SpellsATextTruthinessGuardAndNothingElse(t *testing.T) {
	schema := w3Schema(t)

	for _, tc := range []struct {
		name string
		in   string
		want string
		why  string
	}{
		{
			name: "the founder's shape: a TEXT guard over arithmetic",
			in:   `if(note, (date(due) - today()).days, "")`,
			want: `if(note != "", (date(due) - today()).days)`,
			why:  "on text, `\"\"` is PRESENT (FR-007a) and falsy, so `<> \"\"` is truthiness spelled exactly — the guard is KEPT and re-expressed, never dropped",
		},
		{
			name: "Subscriptions.base's own days_to_renewal, verbatim",
			in:   `if(renewal_date, (date(renewal_date) - today()).days, "")`,
			want: `if(renewal_date, (date(renewal_date) - today()).days)`,
			why:  "the fixture schema does not declare `renewal_date`, so there is no declared type to decide on and the rewrite must decline — the real vault's schema does declare it, and TestW3_TheRealVaultViewIsEnabledAndDoesNotBroaden is where that case is graded",
		},
		{
			name: "a DATE guard is still W2's, not W3's",
			in:   `if(due, (date(due) - today()).days, "")`,
			want: `(date(due) - today()).days`,
			why:  "W2 proves the guard REDUNDANT on a date and drops it outright; W3 must not intercept that case and leave a guard W2 had already shown decides nothing",
		},
		{
			name: "a NUMBER guard is never spelled",
			in:   `if(cost, (date(due) - today()).days, "")`,
			want: `if(cost, (date(due) - today()).days)`,
			why:  "0 is present and falsy, and `cost <> \"\"` is not that question — FR-007a makes `\"\"` the ABSENT state on a decimal, so the comparison is `IS NOT NULL` in disguise and would admit `cost: 0`",
		},
		{
			name: "a CHECKBOX guard is never spelled",
			in:   `if(done, (date(due) - today()).days, "")`,
			want: `if(done, (date(due) - today()).days)`,
			why:  "false is present and falsy",
		},
		{
			name: "a MANY text guard is never spelled",
			in:   `if(notes, (date(due) - today()).days, "")`,
			want: `if(notes, (date(due) - today()).days)`,
			why:  "JavaScript reads an empty ARRAY as truthy while `<> \"\"` is element-wise on a many property (R-9) and answers false for it — a narrowing, but not an equivalence, and this rewrite only makes equivalences",
		},
		{
			name: "a guard that is not a bare property is never spelled",
			in:   `if(note.contains("x"), (date(due) - today()).days, "")`,
			want: `if(note.contains("x"), (date(due) - today()).days)`,
			why:  "the proof is about a property's own truthiness; an expression already yields whatever it yields and `<> \"\"` says nothing about it",
		},
		{
			name: "a FILE property guard is never spelled",
			in:   `if(file.name, (date(due) - today()).days, "")`,
			want: `if(file.name, (date(due) - today()).days)`,
			why:  "RefFile is not RefProperty: `file.name` has no schema declaration to read a type off, and it is never empty anyway",
		},
		{
			name: "a guard on an UNDECLARED property is never spelled",
			in:   `if(nosuch, (date(due) - today()).days, "")`,
			want: `if(nosuch, (date(due) - today()).days)`,
			why:  "with no declared type there is nothing to prove, and guessing text would be the guess this rewrite exists not to make",
		},
		{
			name: "an already-boolean condition is left alone",
			in:   `if(note == "x", (date(due) - today()).days, "")`,
			want: `if(note == "x", (date(due) - today()).days)`,
			why:  "W1 drops the `\"\"` branch and there is no truthiness guard left to spell — a second pass must not wrap the comparison in another one",
		},
		{
			name: "the rewrite does not run away with itself",
			in:   `if(note, 1, "")`,
			want: `if(note != "", 1)`,
			why:  "`note != \"\"` is not a bare property ref, so the next pass finds nothing to spell and the loop terminates on its own rather than on the pass cap",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := rewriteFormulaSource(tc.in, schema)
			if got != tc.want {
				t.Errorf("rewriteFormulaSource(%q)\n got: %q\nwant: %q\nwhy: %s", tc.in, got, tc.want, tc.why)
			}
		})
	}
}

// TestW3_DeclinesWithNoSchema is separate because a nil schema is how the
// caller says "this view has no record type", and a rewrite that read a type
// off nothing would fire on every untyped view in the vault at once.
func TestW3_DeclinesWithNoSchema(t *testing.T) {
	const src = `if(note, 1, "")`
	got, _ := rewriteFormulaSource(src, nil)
	if got != `if(note, 1)` {
		t.Errorf("with no schema, rewriteFormulaSource(%q) = %q; W1 must still fire and W3 must not, because there is no declared type to prove anything about", src, got)
	}
}

// TestW3_TheNoteNamesTheRewriteWhereTheFormulaIsUsed exists because a source
// this importer changed must not be discoverable only by diffing two files.
func TestW3_TheNoteNamesTheRewriteWhereTheFormulaIsUsed(t *testing.T) {
	_, note := rewriteFormulaSource(`if(note, 1, "")`, w3Schema(t))
	for _, want := range []string{"note", `!= ""`, "truthiness"} {
		if !strings.Contains(note, want) {
			t.Errorf("the rewrite note %q does not mention %q — the operator reading the report has to be able to see what changed and why", note, want)
		}
	}
}

// ---------------------------------------------------------------------------
// THE THREE-STATE PROOF, EXECUTED
// ---------------------------------------------------------------------------

// w3States is the fixture's whole point: one note per state a single-valued
// text property can be in, plus the whitespace case, with the hand-derived
// JavaScript answer for each written down next to it.
//
// The whitespace row is not padding. `FoldKey` case-folds and does NOT trim, so
// `"   " <> ""` is TRUE — which is what JavaScript says about `"   "` too. An
// implementation that trimmed before comparing would agree with this fixture on
// every other row and disagree here, and nothing else in this file would catch
// it.
var w3States = []struct {
	stem    string
	label   string // the `label:` line, verbatim; "" means the key is absent
	truthy  bool   // JavaScript's answer for a bare `label`
	because string
}{
	{stem: "State absent", label: "", truthy: false, because: "`undefined` is falsy"},
	{stem: "State empty", label: `label: ""`, truthy: false, because: "`\"\"` is falsy, and FR-007a keeps it a PRESENT value here — this is the row `IS NOT NULL` would get wrong"},
	{stem: "State spaces", label: `label: "   "`, truthy: true, because: "a non-empty string is truthy, whitespace included"},
	{stem: "State value", label: `label: alpha`, truthy: true, because: "a non-empty string is truthy"},
}

// w3Filler is sixteen further widgets carrying sixteen DISTINCT labels, and it
// is load-bearing rather than padding.
//
// The importer's own inference reads a small, repeated vocabulary as an ENUM
// (infer.go's enumMaxDistinct = 15). With only the four state notes above,
// `label` came back as `enum` and W3 — which fires on `text` alone — correctly
// declined, leaving this whole file measuring the wrong thing. Sixteen distinct
// values is what makes the inferred type TEXT, which is the type the founder's
// own `renewal_date` has and the only type this rewrite is about.
//
// They are all truthy, so they belong in the positive half of every expectation
// below, and the expectations are derived from the tables rather than written
// out — a filler that changed state would move the answer with it.
var w3Filler = func() []struct {
	stem    string
	label   string
	truthy  bool
	because string
} {
	var out []struct {
		stem    string
		label   string
		truthy  bool
		because string
	}
	for i := 0; i < 16; i++ {
		out = append(out, struct {
			stem    string
			label   string
			truthy  bool
			because string
		}{
			stem:    fmt.Sprintf("Filler %02d", i),
			label:   fmt.Sprintf("label: distinct-value-%02d", i),
			truthy:  true,
			because: "a non-empty string is truthy; present so that `label` infers as text rather than as a closed enum",
		})
	}
	return out
}()

// w3AllStates is the corpus: the four states plus the fillers.
func w3AllStates() []struct {
	stem    string
	label   string
	truthy  bool
	because string
} {
	out := append([]struct {
		stem    string
		label   string
		truthy  bool
		because string
	}{}, w3States...)
	return append(out, w3Filler...)
}

// w3TruthyStems is the hand-derived set of notes Obsidian's guard admits.
func w3TruthyStems() []string {
	var out []string
	for _, s := range w3AllStates() {
		if s.truthy {
			out = append(out, s.stem+".md")
		}
	}
	sort.Strings(out)
	return out
}

// w3FalsyStems is its complement.
func w3FalsyStems() []string {
	var out []string
	for _, s := range w3AllStates() {
		if !s.truthy {
			out = append(out, s.stem+".md")
		}
	}
	sort.Strings(out)
	return out
}

// w3Base is a `.base` file whose four views ask the same guarded formula four
// different questions: the guard's own answer, that answer negated, and the
// same pair through a comparison whose JavaScript coercion differs from ours.
//
// `tag` is `if(label, 1, "")` — the founder's exact shape with the arithmetic
// replaced by a constant, so that what the views measure is the GUARD and
// nothing else. `guard_date` is the same shape over a DATE property, carried by
// W2's guard-dropping, and it is here so that W3's behaviour can be compared
// against a rewrite that was already accepted rather than only against a
// hand-derived expectation.
const w3Base = `filters:
  and:
    - type == "widget"
formulas:
  tag: if(label, 1, "")
views:
  - type: table
    name: Guarded
    filters:
      and:
        - formula.tag == 1
  - type: table
    name: Guarded Negated
    filters:
      not:
        - formula.tag == 1
`

// w3Fixture writes the four state notes plus the base, imports them with the
// real importer, and returns the vault root.
func w3Fixture(t *testing.T) (string, *Report) {
	t.Helper()
	root := t.TempDir()
	for _, s := range w3AllStates() {
		body := "---\ntype: widget\n"
		if s.label != "" {
			body += s.label + "\n"
		}
		body += "---\n\n# " + s.stem + "\n"
		if err := os.WriteFile(filepath.Join(root, s.stem+".md"), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", s.stem, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "bases"), 0o750); err != nil {
		t.Fatalf("creating bases/: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bases", "Widgets.base"), []byte(w3Base), 0o600); err != nil {
		t.Fatalf("writing the base: %v", err)
	}
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("importing the W3 fixture: %v", err)
	}
	return root, rep
}

// w3Loaded is everything the query path reads back off disk.
type w3Loaded struct {
	root    string
	schemas *records.SchemaSet
	views   *records.ViewSet
	notes   map[string]fr105Note
}

// w3Notes reads every note in the imported vault back off disk through the
// product's own parser, keyed by its VAULT-RELATIVE PATH.
//
// THE KEY IS THE POINT, and it is why this does not reuse fr105Notes. That
// helper keys by filename STEM, and the founder's vault contains the same stem
// in several folders — `Fly.io.md` exists three times (CRM, Subscriptions,
// 99-Temp), and so do about forty others. One silently overwrites the rest, so
// a view over `01-Areas/Subscriptions` is graded against whichever copy the map
// walk happened to store last. Measured: it invented a two-row "narrowing" in a
// view with no formula in its filter at all, and it would hide a broadening the
// same way, by making the extra row unreachable. A path is unique; a stem is
// not, and the oracle names rows by path in the first place.
func w3Notes(t *testing.T, root string) map[string]fr105Note {
	t.Helper()
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning the imported vault: %v", err)
	}
	out := map[string]fr105Note{}
	for _, abs := range inv.Notes {
		data, readErr := os.ReadFile(abs) //nolint:gosec // path from this run's own scan
		if readErr != nil {
			t.Fatalf("reading %s: %v", abs, readErr)
		}
		// Relative to the SCAN's own root, not the caller's: on macOS a temp
		// dir is reached through /var and resolves to /private/var.
		rel, relErr := filepath.Rel(inv.Root, abs)
		if relErr != nil {
			t.Fatalf("relativising %s: %v", abs, relErr)
		}
		key := filepath.ToSlash(rel)
		if _, clash := out[key]; clash {
			t.Fatalf("two notes share the vault-relative path %q, which cannot happen on a filesystem — the scan is returning the same file twice", key)
		}
		out[key] = fr105Note{Rec: records.ParseRecord(abs, data), RelPath: key}
	}
	return out
}

func w3Load(t *testing.T, root string) w3Loaded {
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
	return w3Loaded{root: root, schemas: schemas, views: views, notes: w3Notes(t, root)}
}

// w3View finds one stored view by its display name and returns it, asserting
// it is ENABLED and servable through the real bridge.
//
// A view that is stored disabled returns nothing when it is served, so a row
// assertion over it would pass for the wrong reason — which is the specific
// way this measurement can lose its power.
func w3View(t *testing.T, l w3Loaded, rep *Report, display string) generated.ViewDef {
	t.Helper()
	loader := records.NewViewFindLoader(l.views)
	var seen []string
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			seen = append(seen, v.DisplayName)
			if v.DisplayName != display || v.OutputRelPath == "" {
				continue
			}
			slug := strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml")
			sv, ok := l.views.Get(slug)
			if !ok {
				t.Fatalf("the import reports writing %q for view %q but no such view loaded", slug, display)
			}
			if sv.Def.Disabled != nil && *sv.Def.Disabled {
				t.Fatalf("view %q was stored DISABLED, with untranslated: %v — a disabled view serves nothing, so any row assertion over it would pass vacuously",
					display, w3Strings(sv.Def.Untranslated))
			}
			if _, servable := loader.View(slug); !servable {
				refusal, _ := loader.ServeRefusal(slug)
				t.Fatalf("the view->find bridge will not serve %q: %s", display, refusal.String())
			}
			return sv.Def
		}
	}
	t.Fatalf("no imported view is named %q; the import produced %v", display, seen)
	return generated.ViewDef{}
}

func w3Strings(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

// w3Rows serves one stored view at one instant and returns the matching notes'
// vault-relative paths.
func w3Rows(t *testing.T, l w3Loaded, def generated.ViewDef, at time.Time) []string {
	t.Helper()
	if def.Type == nil {
		t.Fatalf("view %q resolved to no record type", def.Name)
	}
	schema, ok := l.schemas.Get(*def.Type)
	if !ok {
		t.Fatalf("view %q names record type %q, which the vault does not declare", def.Name, *def.Type)
	}
	var set *records.FormulaSet
	if def.Formulas != nil && len(*def.Formulas) > 0 {
		var errs []*records.FormulaError
		// The REAL validator, over the sources actually on disk — the same call
		// the query path makes (knowledgefind/namespace.go::buildNamespace).
		set, errs = records.ValidateFormulaSet(*def.Formulas, schema)
		if len(errs) > 0 {
			msgs := make([]string, 0, len(errs))
			for _, e := range errs {
				msgs = append(msgs, e.Error())
			}
			t.Fatalf("the formulas the importer wrote for %q do not validate: %s", def.Name, strings.Join(msgs, "; "))
		}
	}
	var out []string
	for key, note := range l.notes {
		if note.Rec.TypeName() != *def.Type {
			continue
		}
		var ev *records.FormulaEvaluator
		if set != nil {
			ev = records.NewFormulaEvaluator(set, records.Comparator{}, at)
			ev.Begin(w3Candidate{rec: note.Rec, rel: note.RelPath, schema: schema})
		}
		if def.Filter == nil || w3Eval(t, *def.Filter, schema, note, ev) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// w3Eval walks a stored filter tree the way knowledgefind's own tree.go does.
//
// The `not` case is the reason this function exists rather than reusing
// fr105EvalNode: tree.go's `nodeNot` is a bare `!inner.matched`, with no
// re-admission of absent records, while fr105EvalNode pushes the negation into
// records.Filter.Negate where FR-008 DOES re-admit them. Those two answer
// differently on exactly the records this file is about, and only the first one
// is what a query does.
func w3Eval(t *testing.T, n generated.VaultFilterNode, schema *records.Schema, note fr105Note, ev *records.FormulaEvaluator) bool {
	t.Helper()
	switch {
	case n.All != nil:
		for _, c := range *n.All {
			if !w3Eval(t, c, schema, note, ev) {
				return false
			}
		}
		return true
	case n.Any != nil:
		for _, c := range *n.Any {
			if w3Eval(t, c, schema, note, ev) {
				return true
			}
		}
		return false
	case n.Not != nil:
		return !w3Eval(t, *n.Not, schema, note, ev)
	case n.Property != nil:
		return w3Leaf(t, n, schema, note, ev)
	default:
		t.Fatalf("filter node is neither a combinator nor a leaf: %+v", n)
		return false
	}
}

func w3Leaf(t *testing.T, n generated.VaultFilterNode, schema *records.Schema, note fr105Note, ev *records.FormulaEvaluator) bool {
	t.Helper()
	if n.Op == nil {
		t.Fatalf("leaf on %q carries no operator", *n.Property)
	}
	f := records.Filter{Property: *n.Property, Op: records.Operator(*n.Op)}
	if n.Value != nil {
		f.Literal, f.LiteralGiven = *n.Value, true
	}
	if n.Values != nil {
		f.Literals = *n.Values
	}

	if records.IsFileNamespace(f.Property) {
		left, err := records.ResolveFileProperty(f.Property, records.FileMeta{Path: note.RelPath})
		if err != nil {
			t.Fatalf("the product refused a file property the importer wrote (%s): %v", f.Property, err)
		}
		res, matchErr := f.MatchValue(records.Comparator{}, records.FilePropertySchema(), left)
		if matchErr != nil {
			t.Fatalf("the product's comparator refused a file filter the importer wrote (%s %s %q): %v", f.Property, f.Op, f.Literal, matchErr)
		}
		return res.Matched
	}

	if !strings.HasPrefix(f.Property, "formula.") {
		res, err := f.Match(schema, note.Rec)
		if err != nil {
			t.Fatalf("the product's comparator refused a filter the importer wrote (%s %s %q): %v", f.Property, f.Op, f.Literal, err)
		}
		return res.Matched
	}

	if ev == nil {
		t.Fatalf("the stored view's filter names %q but the view declares no formulas", f.Property)
	}
	name := strings.TrimPrefix(f.Property, "formula.")
	result, ok := ev.Evaluate(name)
	if !ok {
		t.Fatalf("the evaluator does not know formula %q, which the stored view's filter names", name)
	}
	prop := &records.Property{Name: f.Property, Type: w3PropertyTypeOf(t, result.Type)}
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

func w3PropertyTypeOf(t *testing.T, ft records.FormulaType) records.PropertyType {
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

// w3Candidate adapts one parsed record to records.FormulaCandidate.
type w3Candidate struct {
	rec    records.Record
	rel    string
	schema *records.Schema
}

func (c w3Candidate) FormulaProperty(name string) (records.PropertyValue, bool) {
	prop, ok := c.schema.Property(name)
	if !ok {
		return records.PropertyValue{}, false
	}
	return records.ResolveProperty(c.rec, prop), true
}

func (c w3Candidate) FormulaFileProperty(name string) (records.PropertyValue, bool) {
	pv, err := records.ResolveFileProperty(name, records.FileMeta{Path: c.rel})
	if err != nil {
		return records.PropertyValue{}, false
	}
	return pv, true
}

func w3Clock() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) }

// TestW3_TheGuardAdmitsExactlyWhatObsidianTruthinessAdmits is the proof.
//
// The formula is `if(label, 1, "")` and the view filters `formula.tag == 1`, so
// a row is in the view exactly when the guard fired. The expectation is
// hand-derived from JavaScript, one row per state, written in w3States next to
// the reason:
//
//	label absent   `undefined` falsy   -> else branch -> not in the view
//	label ""       falsy               -> else branch -> not in the view
//	label "   "    truthy              -> then branch -> IN the view
//	label alpha    truthy              -> then branch -> IN the view
//
// `== 1` is chosen for the comparison on purpose. JavaScript's `"" == 1` is
// FALSE and our absent-vs-1 is false under R-2, so the two languages agree
// about the ELSE branch here and the only thing this view can disagree about is
// the GUARD — which is what is being measured.
func TestW3_TheGuardAdmitsExactlyWhatObsidianTruthinessAdmits(t *testing.T) {
	root, rep := w3Fixture(t)
	l := w3Load(t, root)

	// THE INSTRUMENT'S POWER, CHECKED BEFORE THE ANSWER IS READ. If the vault
	// held no widgets, or if the property came back as anything but a
	// single-valued text, every row assertion below would be satisfied by an
	// empty answer and would prove nothing.
	widgets := 0
	for _, n := range l.notes {
		if n.Rec.TypeName() == "widget" {
			widgets++
		}
	}
	if widgets != len(w3AllStates()) {
		t.Fatalf("the fixture imported %d widget note(s), not the %d states this test is about — the expectations below would be graded against the wrong corpus", widgets, len(w3AllStates()))
	}
	schema, ok := l.schemas.Get("widget")
	if !ok {
		t.Fatal("the import declared no `widget` record type")
	}
	prop, found := schema.Property("label")
	if !found {
		t.Fatal("the inferred schema does not declare `label`, so the rewrite had no type to read and this test is measuring the wrong thing")
	}
	if prop.Type != records.TypeText || prop.Many {
		t.Fatalf("`label` was inferred as %s (many=%v); W3 only fires on a single-valued text property, so this fixture no longer exercises it", prop.Type, prop.Many)
	}

	def := w3View(t, l, rep, "Guarded")
	if def.Formulas == nil {
		t.Fatal("the stored view carries no `formulas:` block, so `formula.tag` resolves against nothing")
	}
	// THAT THE REWRITE RAN AT ALL, checked by what it is NOT rather than by
	// what it is. Asserting the exact emitted text here would turn every
	// deliberate mutation of the operator into a bail-out on this line, so the
	// rows below — the thing actually being measured — would never be reached.
	src := (*def.Formulas)["tag"]
	if strings.HasPrefix(strings.ReplaceAll(src, " ", ""), "if(label,") {
		t.Fatalf("the stored formula is still %q — the bare guard was carried unchanged, which our grammar cannot type, so what follows would grade the old behaviour", src)
	}

	got := w3Rows(t, l, def, w3Clock())
	want := w3TruthyStems()
	if !w3Same(got, want) {
		t.Errorf("the guarded view returned %v; Obsidian's own truthiness admits %v.\n%s", got, want, w3Why())
	}
}

// TestW3_TheGuardIsExactUnderANegationToo is the tree-level check, and it is a
// separate test because a proof about a leaf is not a proof about a view.
//
// A translation that were merely a SUBSET of Obsidian's clause would pass the
// test above and fail this one: under `not:` a subset inverts into a superset
// and the view returns MORE rows than the original, which is the one thing
// FR-105 forbids. That is exactly why view_write.go refuses the `!= ""` LEAF
// inside a negation. W3 is an equivalence rather than a subset, so there is
// nothing to invert — and this is where that claim is made falsifiable.
//
// The negated view's hand-derived expectation is the complement of the positive
// one over the same four widgets, because `not` in knowledgefind's tree is a
// bare `!inner.matched`.
func TestW3_TheGuardIsExactUnderANegationToo(t *testing.T) {
	root, rep := w3Fixture(t)
	l := w3Load(t, root)

	positive := w3Rows(t, l, w3View(t, l, rep, "Guarded"), w3Clock())
	negated := w3Rows(t, l, w3View(t, l, rep, "Guarded Negated"), w3Clock())

	if want := w3FalsyStems(); !w3Same(negated, want) {
		t.Errorf("the NEGATED view returned %v; Obsidian's own `not` over the same guard admits %v.\n%s", negated, want, w3Why())
	}

	// AND THE TWO HALVES MUST PARTITION THE CORPUS. Asserting the negated row
	// set alone would still pass if both views had somehow gone wrong in the
	// same direction; asserting that they are exact complements cannot.
	union := append(append([]string{}, positive...), negated...)
	sort.Strings(union)
	all := make([]string, 0, len(w3AllStates()))
	for _, s := range w3AllStates() {
		all = append(all, s.stem+".md")
	}
	sort.Strings(all)
	if !w3Same(union, all) {
		t.Errorf("the guarded view (%v) and its negation (%v) do not partition the four widgets (%v) — one of them is answering a different question than the other", positive, negated, all)
	}
}

func w3Why() string {
	var b strings.Builder
	b.WriteString("hand-derived, one row per state a text property can be in (FR-007a keeps `\"\"` PRESENT, so absent and empty are different states); the sixteen fillers are all truthy and exist only to make `label` infer as text rather than as a closed enum:\n")
	for _, s := range w3States {
		label := s.label
		if label == "" {
			label = "(the key is absent)"
		}
		b.WriteString(fmt.Sprintf("  %-14s %-16s truthy=%-5v  %s\n", s.stem, label, s.truthy, s.because))
	}
	return b.String()
}

func w3Same(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

// ---------------------------------------------------------------------------
// THE REAL VAULT, GRADED AGAINST THE INDEPENDENT ORACLE
// ---------------------------------------------------------------------------

// TestW3_TheRealVaultViewIsEnabledAndDoesNotBroaden grades the one view this
// rewrite actually un-disables in the founder's vault.
//
// Subscriptions.base declares `days_to_renewal: if(renewal_date, (date(
// renewal_date) - today()).days, "")` and `renewal_date` is inferred as TEXT —
// the inference declining, on evidence, to read a column that is half dates and
// half prose as a date. Before W3 the residual `if(renewal_date, …)` did not
// type-check, the formula was refused, and both of "Renewing <14d"'s filter
// clauses became losses in a row-set-deciding position, so FR-105 stored the
// view DISABLED.
//
// A NEWLY ENABLED VIEW IS EXACTLY WHERE A BROADENING WOULD HIDE, so it is
// graded against the hand-derived oracle rather than against anything this
// project computed — and the assertion is one-sided in FR-105's own direction:
// a row we return that Obsidian does not is a failure; a row Obsidian returns
// that we do not is logged and permitted.
func TestW3_TheRealVaultViewIsEnabledAndDoesNotBroaden(t *testing.T) {
	oraclePath := os.Getenv(fr105OracleEnv)
	if oraclePath == "" {
		t.Skipf("%s is unset — set it to the hand-derived expected-row-set JSON for the real vault", fr105OracleEnv)
	}
	root := fixtureVaultCopy(t)
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	data, err := os.ReadFile(oraclePath) //nolint:gosec // operator-supplied acceptance oracle
	if err != nil {
		t.Fatalf("reading the oracle: %v", err)
	}
	var oracle fr105JSONOracle
	if err = json.Unmarshal(data, &oracle); err != nil {
		t.Fatalf("parsing the oracle: %v", err)
	}
	want := map[string][]string{}
	for _, b := range oracle.Bases {
		for _, v := range b.Views {
			want[b.Base+"|"+v.Name] = fr105Sorted(v.Rows)
		}
	}

	l := w3Load(t, root)

	graded := 0
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.OutputRelPath == "" {
				continue
			}
			slug := strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml")
			sv, ok := l.views.Get(slug)
			if !ok {
				t.Errorf("the import reports writing %q but no such view loaded", slug)
				continue
			}
			if sv.Def.Formulas == nil {
				continue
			}
			// Only the views whose formulas THIS rewrite produced. A view
			// carrying only W1/W2 output is somebody else's measurement.
			spelled := false
			for _, src := range *sv.Def.Formulas {
				if strings.Contains(src, `!= ""`) {
					spelled = true
				}
			}
			if !spelled {
				continue
			}
			key := filepath.Base(b.BaseRelPath) + "|" + v.DisplayName
			expected, known := want[key]
			if !known {
				t.Errorf("view %q of %s carries a W3-rewritten formula and the oracle does not cover it — an ungraded view is exactly where a broadening hides", v.DisplayName, b.BaseRelPath)
				continue
			}
			if sv.Def.Disabled != nil && *sv.Def.Disabled {
				t.Logf("view %q of %s carries a W3 formula but is still DISABLED for another loss (%v) — not graded",
					v.DisplayName, b.BaseRelPath, w3Strings(sv.Def.Untranslated))
				continue
			}
			candidates := 0
			for _, n := range l.notes {
				if sv.Def.Type != nil && n.Rec.TypeName() == *sv.Def.Type {
					candidates++
				}
			}
			if candidates == 0 {
				t.Errorf("UNFALSIFIABLE %q (%s): the vault holds no notes of record type %v, so this view returns 0 rows whatever its filter says and grading it proves nothing",
					v.DisplayName, b.BaseRelPath, sv.Def.Type)
				continue
			}
			got := w3Rows(t, l, sv.Def, w3Clock())
			graded++

			// INSTRUMENT POWER, PER VIEW. An expectation of zero rows is
			// satisfied by a translation that dropped every clause, so a view
			// whose oracle is empty is reported as unfalsifiable rather than
			// counted as a pass.
			if len(expected) == 0 {
				t.Logf("UNFALSIFIABLE %q (%s): the oracle expects 0 rows, so returning 0 proves nothing about this translation", v.DisplayName, b.BaseRelPath)
			}
			extra := fr105MissingFrom(expected, got)
			if len(extra) > 0 {
				t.Errorf("FR-105 BROADENING in %q (%s): the imported view returns %d row(s) the Obsidian original does not: %v",
					v.DisplayName, b.BaseRelPath, len(extra), extra)
			}
			if missing := fr105MissingFrom(got, expected); len(missing) > 0 {
				t.Logf("NARROWING (allowed by FR-105, recorded here anyway) in %q (%s): the Obsidian original returns %d row(s) the import does not: %v",
					v.DisplayName, b.BaseRelPath, len(missing), missing)
			}
			t.Logf("GRADED %q (%s): oracle=%d rows  imported=%d rows  over %d candidate %s note(s)",
				v.DisplayName, b.BaseRelPath, len(expected), len(got), candidates, *sv.Def.Type)

			// THE MUTATION, RUN IN-TEST. The comparison above is only worth
			// something if it could have seen a broadening at all. Serving the
			// same view with its filter stripped is the maximally broadened
			// translation; if THAT is not detected, the row count is being
			// compared against something that cannot move — which is exactly
			// how three views were graded EXACT in this project against a
			// record type the vault holds no notes of.
			broad := sv.Def
			broad.Filter = nil
			widened := w3Rows(t, l, broad, w3Clock())
			extraWhenWidened := fr105MissingFrom(expected, widened)
			if len(extraWhenWidened) == 0 {
				t.Errorf("INSTRUMENT HAS NO POWER on %q: serving it with EVERY filter clause removed returns %d row(s) and NONE of them is a row the oracle lacks, so the grade above would not have caught a broadening either",
					v.DisplayName, len(widened))
				continue
			}
			t.Logf("INSTRUMENT PROVED on %q: with every filter clause stripped the same pipeline returns %d row(s), %d of them rows the oracle does not have — so the %d-row grade above is a measured zero, not an unfalsifiable one",
				v.DisplayName, len(widened), len(extraWhenWidened), len(extra))
		}
	}
	if graded == 0 {
		t.Fatal("no W3-rewritten view was graded, so this test asserted nothing")
	}
	t.Logf("W3: %d view(s) graded against the independent oracle", graded)
}
