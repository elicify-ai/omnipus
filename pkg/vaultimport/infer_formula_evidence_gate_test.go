// Omnipus — containment clause 2 of "a base formula declares a property's
// type": the population the gate is asked about is the one this run VALIDATES,
// not the one inference happened to see first.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE DEFENDS, AND WHY IT IS ITS OWN FILE
//
// TypePropertiesFromBaseFormulas promotes a value-less `text` property to
// `date` on the strength of a `.base` formula that wraps it in `date()`. The
// promotion is STRICTER than what it replaces — records.TypeDate accepts six
// ISO layouts, `text` accepts every string — so the only thing standing
// between it and a note the importer itself reports invalid is containment
// clause 2: no note this run will validate as the record type may carry a
// value for the property.
//
// THE CLAUSE HAS A POPULATION, AND THE POPULATION MOVES. Run's order is:
//
//	CollectTypeGroups        <- ObservedCount is frozen here
//	InferSchema
//	InferTypesForUntypedNotes  <- FR-104b writes `type:` into untyped notes
//	provisionTypesFromBases
//	TypePropertiesFromBaseFormulas
//	writeSchemas / records.Validate
//
// A note with no `type:` JOINS a record type in the middle of that list. It
// was invisible to CollectTypeGroups, so nothing it carries reaches
// ObservedCount — and it is fully visible to records.Validate, so everything
// it carries can fail against the schema this run writes. Asking the frozen
// count is asking about the wrong population, and the answer is wrong in the
// one direction that costs something.
//
// The founder's vault has 27 untyped notes feeding that path, so this is a
// live route rather than a hypothetical one.
//
// TestFormulaEvidence_ANoteThatJOINSTheTypeIsSeenByTheGate is the forcing
// test. MUTATION-VERIFIED against the exact defect it exists to catch: revert
// clause 2 to `p.ObservedCount != 0` and it turns RED on both of its
// assertions — the property is promoted to `date`, and the note FR-104b typed
// two steps earlier is reported invalid by the same run.
//
// TestFormulaEvidence_TheRuleIsREACHEDByRun is the other half, and it is here
// because this branch has twice shipped a rule that was correct, tested and
// never called. Every other test in this file could pass with the call deleted
// from run.go. That one cannot: it asserts on the SCHEMA FILE Run wrote to
// disk and never names the rule at all.
// ---------------------------------------------------------------------------

// formulaGateVault writes a vault from relative path -> body, imports it for
// real, and returns the root and the report.
//
// It is deliberately this file's own helper rather than a shared one: every
// fixture below turns on the EXACT note population, and a helper that another
// file could add an anchor note to would move the thing being measured.
func formulaGateVault(t *testing.T, files map[string]string) (string, *Report) {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
		}
	}
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return root, rep
}

// writtenPropertyType reads a property's type back out of the schema file this
// run WROTE, through records.LoadSchemas — the real loader, not the in-memory
// map the rule mutated.
//
// Reading the file rather than the map is the point everywhere it is used: the
// rule's whole reason for running where it does is that the promoted type has
// to reach the written schema, and an assertion against the in-memory map
// would pass for an implementation that never got it there.
func writtenPropertyType(t *testing.T, root, recordType, property string) records.PropertyType {
	t.Helper()
	set, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading the schemas this run wrote: %v", err)
	}
	schema, ok := set.Get(recordType)
	if !ok {
		t.Fatalf("this run wrote no schema for record type %q", recordType)
	}
	p, found := schema.Property(property)
	if !found {
		t.Fatalf("the written %q schema declares no property %q", recordType, property)
	}
	return p.Type
}

// widgetBase is one base whose formula wraps a bare property in `date()`.
// It is the shape every `days_to_*` / `days_since_*` formula in the founder's
// vault has.
const widgetBase = `filters:
  and:
    - type == "widget"
formulas:
  age: if(serviced, (today() - date(serviced)).days, "")
views:
  - type: table
    name: Serviced
    order:
      - file.name
      - label
      - serviced
      - formula.age
`

// ---------------------------------------------------------------------------
// THE WIRING PROOF
// ---------------------------------------------------------------------------

// TestFormulaEvidence_TheRuleIsREACHEDByRun proves the rule is CALLED, not
// merely correct.
//
// It drives the whole of Run and then reads the schema file off disk. It names
// no function of the rule, so deleting the call from run.go turns it RED —
// which is exactly what a test that invoked TypePropertiesFromBaseFormulas
// itself could never do.
//
// It also pins the two things that make the position in Run load-bearing:
// the type reaches the WRITTEN schema (not just the index), and the report
// carries the account so the founder can overrule it.
func TestFormulaEvidence_TheRuleIsREACHEDByRun(t *testing.T) {
	root, rep := formulaGateVault(t, map[string]string{
		"Notes/Widget A.md":  "---\ntype: widget\nlabel: A\nserviced:\n---\n\nbody\n",
		"Bases/Widgets.base": widgetBase,
	})

	if got := writtenPropertyType(t, root, "widget", "serviced"); got != records.TypeDate {
		t.Fatalf("widget.serviced was written as %q, want %q — `Bases/Widgets.base` applies date() to it and no note carries a value, so the base is the only evidence there is. A `text` here means the rule never ran: check that run.go still CALLS TypePropertiesFromBaseFormulas, between provisioning and writeSchemas.",
			got, records.TypeDate)
	}

	if len(rep.FormulaEvidenced) != 1 {
		t.Fatalf("the report carries %d formula-evidenced type(s), want 1 — a type this run guessed and did not print is a guess the founder cannot correct. Got: %+v", len(rep.FormulaEvidenced), rep.FormulaEvidenced)
	}
	fe := rep.FormulaEvidenced[0]
	if fe.RecordType != "widget" || fe.Property != "serviced" {
		t.Errorf("reported %s.%s, want widget.serviced", fe.RecordType, fe.Property)
	}
	if fe.Was != records.TypeText || fe.Type != records.TypeDate {
		t.Errorf("reported %s -> %s, want text -> date", fe.Was, fe.Type)
	}

	// The founder overrules this by editing a base file, so the report has to
	// name WHICH base and WHICH formula. A type with no traceable evidence is
	// the failure this whole honesty payload exists to prevent.
	if len(fe.Evidence) != 1 {
		t.Fatalf("reported %d piece(s) of evidence, want 1: %+v", len(fe.Evidence), fe.Evidence)
	}
	ev := fe.Evidence[0]
	if !strings.Contains(ev.Base, "Widgets.base") {
		t.Errorf("evidence names base %q, want the file that carries the formula", ev.Base)
	}
	if ev.Formula != "age" {
		t.Errorf("evidence names formula %q, want \"age\"", ev.Formula)
	}
	if !strings.Contains(ev.Source, "date(serviced)") {
		t.Errorf("evidence quotes source %q, which does not contain the operator's own `date(serviced)` — the report quotes his text so he can find the line", ev.Source)
	}
	if ev.Function != "date" {
		t.Errorf("evidence names function %q, want \"date\" — the report tells him which call to check", ev.Function)
	}
}

// ---------------------------------------------------------------------------
// THE FORCING TEST FOR CLAUSE 2
// ---------------------------------------------------------------------------

// TestFormulaEvidence_ANoteThatJOINSTheTypeIsSeenByTheGate is the defect this
// file exists for.
//
// `Notes/Untyped.md` carries no `type:`. Its key set matches `widget` exactly,
// so FR-104b writes `type: widget` into it — AFTER inference froze
// ObservedCount and BEFORE records.Validate reads it. It carries
// `serviced: sometime next spring`, which is not any of the six ISO layouts
// records.TypeDate accepts.
//
// So the promotion must NOT happen, and the reason it must not is measurable:
// with it, the note the importer typed two steps earlier is reported invalid
// by the same run.
//
// BOTH assertions are made, and neither is redundant. The second is the bar
// the founder set; the first is the only one that stays meaningful if some
// later change makes validation lenient.
func TestFormulaEvidence_ANoteThatJOINSTheTypeIsSeenByTheGate(t *testing.T) {
	// `label: A` on BOTH notes is deliberate, not laziness. One note carrying
	// `label: A` makes `label` an inferred enum whose closed set is {A}, and
	// FR-104b refuses to type a note whose VALUE the candidate schema would
	// reject — so an untyped note saying `label: B` never joins the type at
	// all and the fixture would quietly measure nothing. The premise check
	// below is what caught that.
	root, rep := formulaGateVault(t, map[string]string{
		"Notes/Widget A.md":  "---\ntype: widget\nlabel: A\nserviced:\n---\n\nbody\n",
		"Notes/Untyped.md":   "---\nlabel: A\nserviced: sometime next spring\n---\n\nbody\n",
		"Bases/Widgets.base": widgetBase,
	})

	// The premise. Without it the test is vacuous: it would be asserting that
	// a property is not promoted in a vault where nothing ever joined the type.
	joined := false
	for _, n := range rep.TypeInference.Notes {
		if strings.Contains(n.RelPath, "Untyped") && n.Inferred == "widget" && n.Written {
			joined = true
		}
	}
	if !joined {
		t.Fatalf("PREMISE FAILED: FR-104b did not write `type: widget` into Notes/Untyped.md, so no note joined the record type and this test measures nothing. Outcomes: %+v", rep.TypeInference.Notes)
	}

	// 1. The gate held. `serviced` carries a real value on a note of this
	//    record type — it is simply a value inference never saw — so the base
	//    file does not get to overrule it.
	if got := writtenPropertyType(t, root, "widget", "serviced"); got != records.TypeText {
		t.Errorf("widget.serviced was written as %q, want %q. A note that JOINED this record type under FR-104b carries `sometime next spring` for it. Clause 2 must be asked of the notes as they stand when the rule runs — `InferredProperty.ObservedCount` was frozen by CollectTypeGroups before that note had a type at all.",
			got, records.TypeText)
	}
	if len(rep.FormulaEvidenced) != 0 {
		t.Errorf("the rule reported %d promotion(s) and should have made none: %+v", len(rep.FormulaEvidenced), rep.FormulaEvidenced)
	}

	// 2. The bar itself, measured rather than argued: no note this run typed
	//    is reported invalid by this run.
	assertNoNoteThisRunTypedIsInvalid(t, root, rep)
}

// assertNoNoteThisRunTypedIsInvalid is the acceptance bar, applied to the
// notes FR-104b wrote a `type:` into.
//
// It re-reads the notes and the schemas FROM DISK and validates through
// records.ValidateRecord — the real validator — because the claim is about
// what this run left behind, not about what it held in memory.
func assertNoNoteThisRunTypedIsInvalid(t *testing.T, root string, rep *Report) {
	t.Helper()
	typed := map[string]bool{}
	for _, n := range rep.TypeInference.Notes {
		if n.Written {
			typed[n.RelPath] = true
		}
	}
	if len(typed) == 0 {
		return
	}
	set, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading the schemas this run wrote: %v", err)
	}
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning the vault: %v", err)
	}
	notes, _, err := LoadNotes(inv)
	if err != nil {
		t.Fatalf("re-loading the notes: %v", err)
	}
	bad := 0
	for _, n := range notes {
		if !typed[n.RelPath] {
			continue
		}
		rr := records.ValidateRecord(set, n.Rec, records.ValidateOptions{})
		if !rr.Recognised {
			continue
		}
		for _, f := range rr.Findings {
			bad++
			t.Errorf("%s: this run wrote `type: %s` into this note and the SAME run now reports it invalid: %v", n.RelPath, n.Rec.TypeName(), f)
		}
	}
	t.Logf("acceptance bar: %d note(s) typed by this run, %d finding(s) against them — the bar is 0", len(typed), bad)
}

// ---------------------------------------------------------------------------
// WHAT COUNTS AS EVIDENCE
// ---------------------------------------------------------------------------

// TestFormulaEvidence_TimeIsAdmittedAlongsideDate pins the second entry of
// dateEvidencingFunctions.
//
// `time(x)` is the STRICTER of the pair — records' own inferCall accepts only
// `date` for it, where `date(x)` also accepts text — so refusing it while
// admitting its sibling would be a rule about which spelling the operator
// happened to choose. The report has to say which one it saw, or it sends him
// looking for text that is not in his file.
func TestFormulaEvidence_TimeIsAdmittedAlongsideDate(t *testing.T) {
	root, rep := formulaGateVault(t, map[string]string{
		"Notes/Widget A.md": "---\ntype: widget\nlabel: A\nserviced:\n---\n\nbody\n",
		"Bases/Widgets.base": `filters:
  and:
    - type == "widget"
formulas:
  when: time(serviced)
views:
  - type: table
    name: Serviced
    order:
      - file.name
      - label
      - serviced
`,
	})

	if got := writtenPropertyType(t, root, "widget", "serviced"); got != records.TypeDate {
		t.Fatalf("widget.serviced was written as %q, want %q — `time(serviced)` is defined over a date and nothing else, which is stronger evidence than the `date()` case already admitted", got, records.TypeDate)
	}
	if len(rep.FormulaEvidenced) != 1 {
		t.Fatalf("want 1 reported promotion, got %d: %+v", len(rep.FormulaEvidenced), rep.FormulaEvidenced)
	}
	if fn := rep.FormulaEvidenced[0].Evidence[0].Function; fn != "time" {
		t.Errorf("the report names the function %q; the operator wrote `time(...)`, and a report that says `date()` about it sends him looking for a call he never made", fn)
	}
}

// TestFormulaEvidence_OnlyABarePropertyIsEvidence pins the restriction that
// keeps this a reading of what the operator wrote rather than an inference
// about it.
//
// Each case below is a `date(...)` this rule must NOT read as a declaration
// about `serviced`, and each is checked through the WRITTEN schema.
func TestFormulaEvidence_OnlyABarePropertyIsEvidence(t *testing.T) {
	cases := []struct {
		name    string
		formula string
		why     string
	}{
		{
			name:    "an expression over the property, not the property",
			formula: `age: date(serviced + "-01")`,
			why:     "`date(x + \"-01\")` says the CONCATENATION is a date. `serviced` could be a year, a month name, or a number — the operator is building a date out of it, not reading one from it.",
		},
		{
			name:    "a file-metadata reference is not a property",
			formula: `age: date(file.name)`,
			why:     "`file.*` is FR-130 virtual metadata; there is no frontmatter property here to declare.",
		},
		{
			name:    "a reference to another formula is not a property",
			formula: "age: date(serviced)\n  wrapped: date(formula.age)",
			why:     "`formula.*` names a computed value, which already has a static type of its own.",
		},
		{
			name:    "a two-argument call is not this function",
			formula: `age: contains(serviced, "x")`,
			why:     "only the closed dateEvidencingFunctions set carries a declaration, and only at arity one.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The last case deliberately ALSO contains a legitimate
			// `date(serviced)`, so the property is promoted there. What is
			// under test is that `date(formula.age)` contributed nothing —
			// measured by the property `age` never being declared at all.
			root, _ := formulaGateVault(t, map[string]string{
				"Notes/Widget A.md":  "---\ntype: widget\nlabel: A\nserviced:\n---\n\nbody\n",
				"Bases/Widgets.base": "filters:\n  and:\n    - type == \"widget\"\nformulas:\n  " + tc.formula + "\nviews:\n  - type: table\n    name: Serviced\n    order:\n      - file.name\n      - label\n      - serviced\n",
			})
			set, _, err := records.LoadSchemas(root)
			if err != nil {
				t.Fatalf("loading schemas: %v", err)
			}
			schema, ok := set.Get("widget")
			if !ok {
				t.Fatal("no widget schema was written")
			}
			// `age` is a FORMULA name, never a property. If the walk ever
			// treated a `formula.`/`file.` reference as a property name this
			// is where it would show up as an invented declaration.
			if _, found := schema.Property("age"); found {
				t.Errorf("the widget schema declares a property `age`, but `age` is the name of a FORMULA — this rule must never invent a property. %s", tc.why)
			}
			if _, found := schema.Property("file.name"); found {
				t.Error("the widget schema declares `file.name` as a property — FR-130 virtual metadata is not frontmatter")
			}
		})
	}
}

// TestFormulaEvidence_NonBareArgumentLeavesTheTypeAlone is the same
// restriction stated as the thing that actually matters: the property's TYPE.
//
// It is separate from the case table above because that table proves nothing
// was INVENTED, and this proves nothing was PROMOTED. An implementation that
// stopped checking for a bare Ref would pass the first and fail this.
func TestFormulaEvidence_NonBareArgumentLeavesTheTypeAlone(t *testing.T) {
	root, rep := formulaGateVault(t, map[string]string{
		"Notes/Widget A.md": "---\ntype: widget\nlabel: A\nserviced:\n---\n\nbody\n",
		"Bases/Widgets.base": `filters:
  and:
    - type == "widget"
formulas:
  age: date(serviced + "-01")
views:
  - type: table
    name: Serviced
    order:
      - file.name
      - label
      - serviced
`,
	})

	if got := writtenPropertyType(t, root, "widget", "serviced"); got != records.TypeText {
		t.Errorf("widget.serviced was written as %q, want %q — the operator wrapped an EXPRESSION over the property in date(), which says the expression is a date and nothing about the property's own type",
			got, records.TypeText)
	}
	if len(rep.FormulaEvidenced) != 0 {
		t.Errorf("the rule reported %d promotion(s) from a non-bare argument: %+v", len(rep.FormulaEvidenced), rep.FormulaEvidenced)
	}
}

// TestFormulaEvidence_ObservedValuesBeatTheBaseFile pins the half of clause 2
// that was never in doubt, so a later change cannot trade it away while fixing
// the half that was.
//
// `serviced` here holds real, non-date values on ordinary typed notes.
// Inference reads it as text FROM THOSE VALUES, and a base file does not
// outrank data — exactly as a name does not.
//
// SEVEN notes, each with a DIFFERENT `serviced` value, and both halves of that
// are forced by the inference rules rather than chosen. Fewer than seven
// distinct values (enumSmallEnough) or values that repeat twice on average
// (enumMinAvgRepeat) make the property an inferred `enum`, and clause 3 would
// then refuse the promotion for a reason that has nothing to do with the
// clause under test — the test would pass while measuring nothing. It is the
// shape `subscription.renewal_date` has in the founder's own vault: seventeen
// distinct hand-written placeholder spellings in a column that is otherwise
// dates.
func TestFormulaEvidence_ObservedValuesBeatTheBaseFile(t *testing.T) {
	files := map[string]string{"Bases/Widgets.base": widgetBase}
	for _, v := range []string{
		"whenever we get to it", "PLACEHOLDER — never serviced", "ask Ravi",
		"after the recall", "TBD", "not applicable", "see the paper file",
	} {
		files["Notes/Widget "+v+".md"] = "---\ntype: widget\nlabel: A\nserviced: " + v + "\n---\n\nbody\n"
	}
	root, rep := formulaGateVault(t, files)

	// The premise: `serviced` really is TEXT here, so clause 2 is what refuses
	// the promotion rather than clause 3 refusing it for being an enum.
	if got := writtenPropertyType(t, root, "widget", "serviced"); got == records.TypeEnum {
		t.Fatalf("PREMISE FAILED: widget.serviced inferred as %q, so this fixture exercises clause 3 (only ever promotes text) instead of clause 2 (data beats a base file). Give it more distinct values.", got)
	}

	if got := writtenPropertyType(t, root, "widget", "serviced"); got != records.TypeText {
		t.Errorf("widget.serviced was written as %q, want %q — a note of this type carries `whenever we get to it`, and typing the property `date` would make the operator's own note invalid against the schema this run just wrote",
			got, records.TypeText)
	}
	if len(rep.FormulaEvidenced) != 0 {
		t.Errorf("the rule reported %d promotion(s) over observed data: %+v", len(rep.FormulaEvidenced), rep.FormulaEvidenced)
	}
	assertNoNoteThisRunTypedIsInvalid(t, root, rep)
}

// ---------------------------------------------------------------------------
// WHERE THE REAL-VAULT NUMBER LIVES
//
// The founder-facing form of this bar — "over his own 757 notes, how many did
// this rule invalidate" — is measured in infer_formula_evidence_vault_test.go,
// which also FORCES the counter to fire so its zero is a measured zero. It is
// deliberately NOT repeated here: this file had a second copy of it, and two
// spellings of one measurement is how two accounts of one decision drift
// apart.
//
// The division of labour between the two files is the useful part. On the real
// vault the two readings of clause 2 — the frozen ObservedCount and the true
// post-FR-104b count — happen to AGREE, so that test stays green either way and
// cannot see this defect at all. The vault where they disagree is built here,
// by hand, in TestFormulaEvidence_ANoteThatJOINSTheTypeIsSeenByTheGate.
// ---------------------------------------------------------------------------
