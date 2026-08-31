// Omnipus — "a base formula declares a property's type", measured on the
// founder's own vault and printed where he can overrule it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE COVERS, AND WHAT IT DELIBERATELY LEAVES TO ITS NEIGHBOUR
//
// infer_formula_evidence_gate_test.go holds the rule's REACHABILITY and its
// containment clause 2 — that the rule is actually called by Run, and that a
// note joining the record type mid-run is seen by the gate. It works on small
// purpose-built vaults where the note population is the thing being measured.
//
// This file holds the three questions that one cannot answer:
//
//  1. THE OTHER CLAUSES, in one table. Clause 3 (only ever promotes `text`) and
//     clause 4 (never a list) are checked directly against the entry point.
//  2. THE ACCOUNT. A type taken on a base file's word and not PRINTED is a
//     guess the founder cannot overrule, so the rendered section is asserted,
//     not just the payload behind it.
//  3. THE REAL VAULT, in both directions. Promoting `text` to `date` is
//     STRICTER, so it is checked that no note validating today becomes
//     invalid — with the counter forced to fire once, so its zero is a
//     measurement rather than a number nobody has watched move — and that no
//     view of a touched record type returns a row its Obsidian original does
//     not, graded against a hand-derived oracle this project did not compute.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Containment clauses 3 and 4.
// ---------------------------------------------------------------------------

// formulaEvidenceBase builds one parsed `.base` with a single typed view whose
// formula wraps `prop` in `date()`.
func formulaEvidenceBase(t *testing.T, recordType, prop string) (string, map[string]*ParsedBase) {
	t.Helper()
	src := []byte("filters:\n  and:\n    - type == \"" + recordType + "\"\n" +
		"formulas:\n  age: if(" + prop + ", (today() - date(" + prop + ")).days, \"\")\n" +
		"views:\n  - type: table\n    name: All\n")
	pb, err := ParseBaseFile(src)
	if err != nil {
		t.Fatalf("the test's own base file does not parse: %v", err)
	}
	return "06-Bases/Test.base", map[string]*ParsedBase{"06-Bases/Test.base": pb}
}

// TestTypePropertiesFromBaseFormulas_OnlyPromotesAScalarText passes NO notes,
// which makes clause 2 admit everything — so whatever refuses a case here is
// the clause the case names, and not clause 2 refusing it for a reason the
// table is not about.
func TestTypePropertiesFromBaseFormulas_OnlyPromotesAScalarText(t *testing.T) {
	cases := []struct {
		name     string
		prop     InferredProperty
		wantType records.PropertyType
		wantFire bool
	}{
		{
			name:     "a value-less scalar text is re-read as a date",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText},
			wantType: records.TypeDate,
			wantFire: true,
		},
		{
			name:     "clause 3 — an enum is never overruled",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeEnum, Kind: ClassifyEnum, EnumValues: []string{"a", "b"}},
			wantType: records.TypeEnum,
		},
		{
			name:     "clause 3 — a checkbox is never overruled",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeCheckbox, Kind: ClassifyBoolean},
			wantType: records.TypeCheckbox,
		},
		{
			name:     "clause 3 — a relation is never overruled",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeRelation, Kind: ClassifyRelation, To: "company"},
			wantType: records.TypeRelation,
		},
		{
			name:     "clause 3 — an already-inferred date is left alone, and not reported a second time",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeDate, Kind: ClassifyDate},
			wantType: records.TypeDate,
		},
		{
			name:     "clause 4 — a list is refused; translate.go's W2 excludes a `many` date anyway",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText, Many: true},
			wantType: records.TypeText,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, parsed := formulaEvidenceBase(t, "legal-entity", "last_refreshed")
			inferred := map[string][]InferredProperty{"legal-entity": {tc.prop}}

			out := TypePropertiesFromBaseFormulas(inferred, nil, []string{rel}, parsed)

			if got := inferred["legal-entity"][0].Type; got != tc.wantType {
				t.Errorf("property type is %s, want %s", got, tc.wantType)
			}
			if fired := len(out) == 1; fired != tc.wantFire {
				t.Errorf("the rule reported %d decision(s), want fired=%v", len(out), tc.wantFire)
			}
			if reported := inferred["legal-entity"][0].FormulaEvidenced != nil; reported != tc.wantFire {
				t.Errorf("the evidence payload on the property is present=%v, want %v — a decision this run cannot report is a silent one", reported, tc.wantFire)
			}
			if tc.wantFire {
				if k := inferred["legal-entity"][0].Kind; k != ClassifyDateFromFormula {
					t.Errorf("kind is %q, want %q — the report groups its entries by kind", k, ClassifyDateFromFormula)
				}
				if was := out[0].Was; was != records.TypeText {
					t.Errorf("the account says it replaced %s, want text", was)
				}
			}
		})
	}
}

// TestTypePropertiesFromBaseFormulas_AnUntypedViewAttributesNothing is the
// FR-018b clause. An untyped view queries every note in scope, so a property
// name in it is not scoped to one record type, and a declaration read from it
// would land on whichever schemas happened to share the name — here, BOTH.
func TestTypePropertiesFromBaseFormulas_AnUntypedViewAttributesNothing(t *testing.T) {
	src := []byte("filters:\n  and:\n    - file.inFolder(\"01-Areas\")\n" +
		"formulas:\n  age: if(last_refreshed, (today() - date(last_refreshed)).days, \"\")\n" +
		"views:\n  - type: table\n    name: Everything\n")
	pb, err := ParseBaseFile(src)
	if err != nil {
		t.Fatalf("the test's own base file does not parse: %v", err)
	}
	inferred := map[string][]InferredProperty{
		"legal-entity": {{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText}},
		"contact":      {{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText}},
	}

	out := TypePropertiesFromBaseFormulas(inferred, nil, []string{"Untyped.base"},
		map[string]*ParsedBase{"Untyped.base": pb})

	if len(out) != 0 {
		t.Fatalf("an UNTYPED view typed %d property/ies: %+v — the name is not scoped to one record type there, so this declaration was attributed by guesswork", len(out), out)
	}
	for _, rt := range []string{"legal-entity", "contact"} {
		if got := inferred[rt][0].Type; got != records.TypeText {
			t.Errorf("%s.last_refreshed became %s", rt, got)
		}
	}
}

// TestTypePropertiesFromBaseFormulas_AnUnparseableFormulaIsNotEvidence: a
// formula this product cannot parse is already a NAMED loss on every view that
// uses it. Reading a type out of text nothing could parse would be inventing
// evidence rather than reading it.
func TestTypePropertiesFromBaseFormulas_AnUnparseableFormulaIsNotEvidence(t *testing.T) {
	src := []byte("filters:\n  and:\n    - type == \"legal-entity\"\n" +
		"formulas:\n  age: date(last_refreshed ((((\n" +
		"views:\n  - type: table\n    name: All\n")
	pb, err := ParseBaseFile(src)
	if err != nil {
		t.Fatalf("the test's own base file does not parse: %v", err)
	}
	if _, perr := records.ParseFormula(pb.Formulas["age"]); perr == nil {
		t.Fatalf("the fixture formula %q PARSES, so this case no longer tests what it says it does", pb.Formulas["age"])
	}
	inferred := map[string][]InferredProperty{
		"legal-entity": {{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText}},
	}
	if out := TypePropertiesFromBaseFormulas(inferred, nil, []string{"B.base"},
		map[string]*ParsedBase{"B.base": pb}); len(out) != 0 {
		t.Fatalf("a type was read out of an unparseable formula: %+v", out)
	}
}

// ---------------------------------------------------------------------------
// The account, as the founder reads it.
// ---------------------------------------------------------------------------

func TestFormulaEvidencedType_ReportLinesNameTheBaseAndTheFormula(t *testing.T) {
	f := FormulaEvidencedType{
		RecordType: "legal-entity",
		Property:   "last_refreshed",
		Type:       records.TypeDate,
		Was:        records.TypeText,
		Evidence: []FormulaEvidence{{
			Base:     "06-Bases/CRM.base",
			Formula:  "days_since_refresh",
			Source:   `if(last_refreshed, (today() - date(last_refreshed)).days, "")`,
			Function: "date",
		}},
	}
	got := strings.Join(f.ReportLines(), "\n")
	for _, must := range []string{
		"legal-entity.last_refreshed -> date",
		"06-Bases/CRM.base",
		"days_since_refresh",
		"date(last_refreshed)",
		"knowledge_configure set schema legal-entity property last_refreshed type=text",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("the account never says %q — the founder cannot overrule a guess whose source he is not shown, nor fix a formula he is not pointed at.\ngot:\n%s", must, got)
		}
	}
}

func TestCollectFormulaEvidencedTypes_IsStablyOrdered(t *testing.T) {
	mk := func(rt, p string) InferredProperty {
		return InferredProperty{Name: p, Type: records.TypeDate, Kind: ClassifyDateFromFormula,
			FormulaEvidenced: &FormulaEvidencedType{RecordType: rt, Property: p, Type: records.TypeDate, Was: records.TypeText}}
	}
	inferred := map[string][]InferredProperty{
		"legal-entity": {mk("legal-entity", "last_refreshed")},
		"compliance":   {mk("compliance", "due_date"), mk("compliance", "approved_on")},
		"invoice":      {mk("invoice", "due_date")},
	}
	want := []string{"compliance.approved_on", "compliance.due_date", "invoice.due_date", "legal-entity.last_refreshed"}
	// Repeated, because Go randomises map iteration per range and a single
	// pass can agree with the wanted order by luck.
	for attempt := 0; attempt < 16; attempt++ {
		got := CollectFormulaEvidencedTypes(inferred)
		if len(got) != len(want) {
			t.Fatalf("collected %d accounts, want %d — every decision must reach the report", len(got), len(want))
		}
		for i, w := range want {
			if g := got[i].RecordType + "." + got[i].Property; g != w {
				t.Fatalf("position %d is %s, want %s — the order must not depend on Go's randomised map iteration, or two identical runs print different reports", i, g, w)
			}
		}
	}
}

// TestRun_PrintsTheFormulaEvidencedSectionAndClearsTheView reads the two
// surfaces a person actually meets: the RENDERED report, and the view outcome.
//
// It does not call TypePropertiesFromBaseFormulas — the whole run does. The
// payload assertions live next door in the gate file; what is asserted here is
// that the payload is PRINTED, without the report contradicting its own entry,
// and that the losses the rule exists to recover are actually gone.
func TestRun_PrintsTheFormulaEvidencedSectionAndClearsTheView(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// `reviewed_on` matches no entry and neither affix of nameEvidencedDate,
	// so the base file's formula is the ONLY route to a `date` here.
	if nameEvidencedDate("reviewed_on") {
		t.Fatal("PREMISE FAILED: `reviewed_on` is now name-evidenced, so this fixture no longer isolates the formula rule")
	}
	// `reviewed_on: ""` — the EMPTY STRING, not an explicit null, and the
	// difference is the whole measurement. FR-007a makes `""` ABSENT on a
	// date and PRESENT on text, so this one character is what separates the
	// two schemas the rule chooses between. An explicit null is absent on
	// BOTH, and a fixture written that way still passes when the rule runs in
	// the wrong place — measured: it did, until this line changed.
	write("Ticket A.md", "---\ntype: ticket\nlabel: A\nreviewed_on: \"\"\n---\n\nbody\n")
	write("Ticket B.md", "---\ntype: ticket\nlabel: A\nreviewed_on: \"\"\n---\n\nbody\n")
	write("Reviews.base", `filters:
  and:
    - type == "ticket"
formulas:
  days_since_review: if(reviewed_on, (today() - date(reviewed_on)).days, "")
views:
  - type: table
    name: Reviewed
    filters:
      and:
        - reviewed_on != ""
    order:
      - file.name
      - reviewed_on
      - formula.days_since_review
`)

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	var buf bytes.Buffer
	rep.Render(&buf)
	out := buf.String()
	for _, must := range []string{
		"typed from a BASE FORMULA",
		"ticket.reviewed_on -> date",
		"Reviews.base",
		"days_since_review",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("the rendered report never says %q. If the whole section is missing, the most likely cause is that run.go never calls TypePropertiesFromBaseFormulas.\n%s", must, out)
		}
	}
	if strings.Contains(out, "CONTRADICTION") {
		t.Errorf("the report contradicts its own entry — a run calling its own correct decision impossible is worse than saying nothing:\n%s", out)
	}

	// AND THE POINT OF ALL OF IT. `reviewed_on != ""` has no faithful
	// translation on TEXT (FR-007a keeps "" a PRESENT value there, so
	// IS NOT NULL would BROADEN); on a date it is exactly IS NOT NULL, and
	// `if(reviewed_on, …)` reduces through translate.go's W2.
	seen := false
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.DisplayName != "Reviewed" {
				continue
			}
			seen = true
			if v.Disabled {
				t.Errorf("the view is still DISABLED; disabling losses: %v", v.DisablingLosses)
			}
			if len(v.Losses) != 0 {
				t.Errorf("the view still reports %d loss(es): %v", len(v.Losses), v.Losses)
			}
		}
	}
	if !seen {
		t.Fatal(`the import produced no view named "Reviewed", so nothing above was measured`)
	}

	// AND THE ROW SET, off disk, through the product's own loader and
	// comparator. This is the assertion that catches the rule running in the
	// WRONG PLACE rather than not at all.
	//
	// Move the call in run.go to after writeSchemas and everything above still
	// passes: the report and the view outcome are both read from the in-memory
	// `inferred` map, which the rule mutated either way. What changes is the
	// SCHEMA FILE — `reviewed_on` stays `text` on disk — and on text FR-007a
	// keeps `""` a PRESENT value, so `IS NOT NULL` then matches both notes.
	// The view imports clean, looks healthy, and returns two rows its Obsidian
	// original returns none of. That is the FR-105 broadening this whole rule
	// is written to avoid, and only a row count off disk sees it.
	schemas, schemaRep, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading the schemas this run wrote: %v", err)
	}
	if !schemaRep.OK() {
		t.Fatalf("the importer wrote schemas the real loader rejects: %v", schemaRep.Rejections)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("loading the views this run wrote: %v", err)
	}
	if !viewRep.OK() {
		for _, rej := range viewRep.Rejections {
			t.Errorf("the importer wrote a view the real loader rejects: %s", rej.String())
		}
		t.FailNow()
	}
	if len(views.Names()) != 1 {
		t.Fatalf("expected exactly one written view, got %v", views.Names())
	}
	slug := views.Names()[0]
	req, servable := records.NewViewFindLoader(views).View(slug)
	if !servable {
		refusal, _ := records.NewViewFindLoader(views).ServeRefusal(slug)
		t.Fatalf("the written view will not serve: %s", refusal.String())
	}
	if rows := fr105RowsFor(t, req, schemas, fr105Notes(t, root)); len(rows) != 0 {
		t.Errorf("the written view returns %v; both notes leave `reviewed_on` BLANK and Obsidian's `reviewed_on != \"\"` returns none of them. If `reviewed_on` reached the schema file as `text`, FR-007a makes `\"\"` a PRESENT value and IS NOT NULL matches both — check that run.go calls TypePropertiesFromBaseFormulas BEFORE writeSchemas, not after", rows)
	}
}

// ---------------------------------------------------------------------------
// The real vault. SKIPS without OMNIPUS_KB_FIXTURE.
// ---------------------------------------------------------------------------

// TestFixtureVault_TheImporterNeverInvalidatesANoteOverAFormulaGuess is the
// acceptance bar itself, stated the way the founder reads it: after a full
// import of his 757 notes, validate every one of them against the schemas the
// SAME run wrote, and count the findings naming a property this run typed from
// a base formula. That count must be zero.
//
// Deliberately the same shape as the name-guess bar in infer_no_values_test.go
// and deliberately BROADER than the property list: a schema change reaches
// every note of the type, and that is the population this rule can hurt.
func TestFixtureVault_TheImporterNeverInvalidatesANoteOverAFormulaGuess(t *testing.T) {
	root := fixtureVaultCopy(t)

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if len(rep.FormulaEvidenced) == 0 {
		t.Fatal("the run made no formula-based type decision, so this measurement is vacuous — the vault, the rule, or its wiring in run.go has changed")
	}
	guessed := map[string]bool{}
	for _, f := range rep.FormulaEvidenced {
		if f.Type != records.TypeDate {
			t.Errorf("%s.%s was read as %s; only `date` has an argued rule behind it", f.RecordType, f.Property, f.Type)
		}
		if len(f.Evidence) == 0 {
			t.Errorf("%s.%s was typed with no base formula recorded — a silent guess", f.RecordType, f.Property)
		}
		guessed[f.RecordType+"."+f.Property] = true
	}

	blamed, checked := formulaGuessBlame(t, root, guessed)
	t.Logf("REAL VAULT acceptance bar: %d formula-based type decisions over %d validated records, %d notes invalidated by one — the bar is 0",
		len(rep.FormulaEvidenced), checked, blamed)
	if blamed != 0 {
		t.Errorf("ACCEPTANCE BAR FAILED: %d note(s) are invalid against a property this import typed from a base formula", blamed)
	}
}

// formulaGuessBlame validates every note in an imported vault against the
// schemas that same import wrote, and counts the findings naming one of
// `guessed`. Returns (blamed, checked).
//
// It REPORTS rather than asserts, so that the test below can drive this exact
// code with a property that WILL fail and watch the count move. A guard nobody
// has seen fail is a number, not a measurement.
func formulaGuessBlame(t *testing.T, root string, guessed map[string]bool) (blamed, checked int) {
	t.Helper()
	schemaSet, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading the schemas this run wrote: %v", err)
	}
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning the vault: %v", err)
	}
	notes, _, err := LoadNotes(inv)
	if err != nil {
		t.Fatalf("re-loading notes: %v", err)
	}
	for _, n := range notes {
		typeName := n.Rec.TypeName()
		if typeName == "" {
			continue
		}
		rr := records.ValidateRecord(schemaSet, n.Rec, records.ValidateOptions{})
		if !rr.Recognised {
			continue
		}
		checked++
		for _, f := range rr.Findings {
			if f.Property == "" || !guessed[typeName+"."+f.Property] {
				continue
			}
			blamed++
			t.Logf("BLAMED: %s — %s.%s: %v", n.RelPath, typeName, f.Property, f)
		}
	}
	return blamed, checked
}

// TestFixtureVault_TheInvalidationCounterCanActuallyFire forces the zero above
// to be a MEASUREMENT.
//
// It imports the real vault, then finds a property the WRITTEN schema declares
// `date` and that some note fills in, and rewrites that one note's value to
// something no date layout accepts. The same counter is then asked about that
// property. If it still says zero, the acceptance bar above is blind and its
// zero means nothing — which is the only reading of a green nobody has watched
// fail.
//
// Nothing about the formula rule is exercised here on purpose: what is under
// test is the INSTRUMENT, not the rule.
func TestFixtureVault_TheInvalidationCounterCanActuallyFire(t *testing.T) {
	root := fixtureVaultCopy(t)
	if _, err := Run(root, true); err != nil {
		t.Fatalf("import failed: %v", err)
	}

	schemaSet, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading schemas: %v", err)
	}
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("scanning: %v", err)
	}
	notes, _, err := LoadNotes(inv)
	if err != nil {
		t.Fatalf("loading notes: %v", err)
	}

	var victimPath, victimType, victimProp string
	for _, n := range notes {
		typeName := n.Rec.TypeName()
		if typeName == "" {
			continue
		}
		schema, ok := schemaSet.Get(typeName)
		if !ok {
			continue
		}
		for _, key := range n.Rec.Frontmatter.Keys {
			p, declared := schema.Property(key)
			if !declared || p.Type != records.TypeDate {
				continue
			}
			if strings.TrimSpace(n.Rec.Frontmatter.Values[key].Text) == "" {
				continue
			}
			victimPath, victimType, victimProp = n.AbsPath, typeName, key
			break
		}
		if victimPath != "" {
			break
		}
	}
	if victimPath == "" {
		t.Fatal("no note in the fixture fills in a date-typed property, so the counter cannot be driven to fire — and the acceptance bar next door is therefore unproven")
	}

	data, err := os.ReadFile(victimPath) //nolint:gosec // a path from this test's own scan of its own temp copy
	if err != nil {
		t.Fatalf("reading the note to break: %v", err)
	}
	broken := strings.Replace(string(data), "\n"+victimProp+":", "\n"+victimProp+": not-a-date-at-all", 1)
	if broken == string(data) {
		t.Fatalf("the mutation applied NOTHING to %s (looking for %q), so a green here would mean nothing", victimPath, "\n"+victimProp+":")
	}
	if err := os.WriteFile(victimPath, []byte(broken), 0o600); err != nil {
		t.Fatalf("writing the broken note: %v", err)
	}

	blamed, checked := formulaGuessBlame(t, root, map[string]bool{victimType + "." + victimProp: true})
	if blamed == 0 {
		t.Fatalf("the counter reported 0 over %d records after %s.%s was deliberately given a value no date layout accepts, in %s — the ZERO the acceptance bar reports is not a measurement, because this instrument cannot see a failure",
			checked, victimType, victimProp, victimPath)
	}
	t.Logf("INSTRUMENT PROVED: forced %s.%s invalid in %s and the counter named %d finding(s) over %d records — the acceptance bar's zero is a measured zero",
		victimType, victimProp, filepath.Base(victimPath), blamed, checked)
}

// ---------------------------------------------------------------------------
// FR-105, against an oracle this project did not compute.
// ---------------------------------------------------------------------------

// fr105JSONOracle is the hand-derived expected row set for the founder's real
// vault. Only the fields this test reads are declared.
type fr105JSONOracle struct {
	Bases []struct {
		// The oracle spells this key `base`, and it holds the base file's
		// BASENAME (`CRM.base`), not a vault-relative path.
		Base  string `json:"base"`
		Views []struct {
			Name string   `json:"name"`
			Rows []string `json:"rows"`
		} `json:"views"`
	} `json:"bases"`
}

const fr105OracleEnv = "OMNIPUS_FR105_ORACLE"

// fr105OracleStems renders the oracle's row names in the same currency
// fr105RowsFor answers in: the note's filename STEM.
//
// The oracle names a row by its VAULT-RELATIVE PATH
// (`01-Areas/CRM/Composio (US).md`); fr105RowsFor keys the imported vault's
// notes by stem (`Composio (US)`). Comparing the two spellings directly
// reports every row of a perfectly-matching view as BOTH a broadening and a
// narrowing — which is what it did on the first run of this test, on a view
// whose 4 rows were exactly the oracle's 4 rows. A row-identity mismatch that
// presents as an FR-105 violation is worse than no check at all, because the
// real violation it would hide looks identical.
func fr105OracleStems(rows []string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, strings.TrimSuffix(filepath.Base(r), ".md"))
	}
	return out
}

// TestFixtureVault_FormulaEvidencedViewsNeverBroadenAgainstTheOracle is the
// second direction of the safety argument, and the one that cannot be settled
// by reasoning about a clause.
//
// Promoting a property to `date` changes how `P != ""` translates: on text it
// has no faithful translation at all (FR-007a keeps `""` PRESENT there), on a
// date it is exactly IS NOT NULL. A refused clause becomes a translated one,
// and a translated clause is only trustworthy against an expectation this
// project did not compute.
//
// THE GRADE IS AT THE VIEW, NOT THE LEAF. A clause under a `not:` inverts, so
// a narrowing at the leaf is a BROADENING at the view; nothing here reasons
// about a clause in isolation. Every view is evaluated through the PRODUCT's
// own loader, view->find bridge and comparator.
func TestFixtureVault_FormulaEvidencedViewsNeverBroadenAgainstTheOracle(t *testing.T) {
	oraclePath := os.Getenv(fr105OracleEnv)
	if oraclePath == "" {
		t.Skipf("%s is unset — set it to the hand-derived expected-row-set JSON for the real vault", fr105OracleEnv)
	}
	root := fixtureVaultCopy(t)

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	touched := map[string]bool{}
	for _, f := range rep.FormulaEvidenced {
		touched[f.RecordType] = true
	}
	if len(touched) == 0 {
		t.Fatal("the run typed no property from a base formula, so this grading is vacuous")
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
			want[b.Base+"|"+v.Name] = fr105Sorted(fr105OracleStems(v.Rows))
		}
	}
	if len(want) == 0 {
		t.Fatal("the oracle names no views — an empty oracle passes anything")
	}

	schemas, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading schemas: %v", err)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("loading views: %v", err)
	}
	if !viewRep.OK() {
		for _, rej := range viewRep.Rejections {
			t.Errorf("the importer wrote a view the real loader rejects: %s", rej.String())
		}
		t.FailNow()
	}
	notes := fr105Notes(t, root)
	loader := records.NewViewFindLoader(views)

	graded, broadened := 0, 0
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.OutputRelPath == "" || !touched[v.ResolvedType] {
				continue
			}
			expected, known := want[filepath.Base(b.BaseRelPath)+"|"+v.DisplayName]
			if !known {
				t.Errorf("view %q of %s is affected by this rule and the oracle does not cover it — an ungraded view is exactly where a broadening hides", v.DisplayName, b.BaseRelPath)
				continue
			}
			slug := strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml")
			sv, ok := views.Get(slug)
			if !ok {
				t.Errorf("the import reports writing %q but no such view loaded", slug)
				continue
			}
			// A DISABLED view is never served, so it cannot broaden — but its
			// rows are still evaluated, so that "disabled" is the reason it
			// returns nothing rather than a coincidence.
			wasDisabled := sv.Def.Disabled != nil && *sv.Def.Disabled
			sv.Def.Disabled = nil
			req, servable := loader.View(slug)
			if !servable {
				refusal, _ := loader.ServeRefusal(slug)
				t.Logf("view %q is not servable even with `disabled` cleared (%s) — nothing to grade", v.DisplayName, refusal.String())
				continue
			}
			got := fr105RowsFor(t, req, schemas, notes)
			graded++
			if extra := fr105MissingFrom(expected, got); len(extra) > 0 {
				broadened++
				t.Errorf("FR-105 BROADENING in %q (%s): the imported view returns %d row(s) the Obsidian original does not: %v",
					v.DisplayName, b.BaseRelPath, len(extra), extra)
			}
			if missing := fr105MissingFrom(got, expected); len(missing) > 0 {
				t.Logf("NARROWING (allowed by FR-105, recorded here anyway) in %q (%s): the Obsidian original returns %d row(s) the import does not: %v",
					v.DisplayName, b.BaseRelPath, len(missing), missing)
			}
			t.Logf("GRADED %-30q %-22s disabled=%v  oracle=%d rows  imported=%d rows",
				v.DisplayName, filepath.Base(b.BaseRelPath), wasDisabled, len(expected), len(got))
		}
	}
	if graded == 0 {
		t.Fatal("no view was graded, so this test asserted nothing")
	}
	t.Logf("FR-105: %d view(s) of the %d record type(s) this rule typed, graded against the independent oracle; %d broadening(s)",
		graded, len(touched), broadened)
}
