// Omnipus — FR-018d provisioning: declaring a record type from its `.base`
// file when no note in the vault carries it, and the FR-105 partition that
// makes doing so safe.
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
// WHAT THIS FILE DEFENDS
//
// Provisioning declares a record type nobody has written a note for. The whole
// design rests on ONE property, and it is the property a later change is most
// likely to trade away for a better clean count:
//
//	A PROPERTY WHOSE BASE USAGE A `text` DECLARATION CANNOT TRANSLATE
//	FAITHFULLY IS LEFT UNDECLARED, SO THE CLAUSE BECOMES A NAMED LOSS AND
//	FR-105 DISABLES THE VIEW.
//
// `operatorDefinedForType` (pkg/records/compare_oracle.go) permits all four
// ordering operators on text, LEXICALLY. So a declared-text `amount` would make
// `amount > 100` answer TRUE for the value "50" — MORE rows than the Obsidian
// original, which is the one direction FR-105 forbids. Nothing else in the
// pipeline catches it: the leaf builds, the view loads, the file looks right,
// and it quietly answers a question nobody asked.
//
// TestProvisioning_OrderingComparisonLeavesPropertyUndeclared is the guard.
// Mutation-verified: declaring the ordering-compared property as text (delete
// the `shapeCompare && isOrderingOp` case from provisionUsesFromLeaves) turns
// it RED — the view converts ENABLED with no loss at all, which is exactly the
// silent broadening it exists to catch.
// ---------------------------------------------------------------------------

// provisionVault writes a vault from a map of relative path to file body,
// imports it, and returns the root and the report. Nothing is shared with the
// other test files' fixtures on purpose: this one's whole subject is a vault
// with NO notes of the type under test, which every other fixture has.
func provisionVault(t *testing.T, files map[string]string) (root string, rep *Report) {
	t.Helper()
	root = t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
		}
	}
	var err error
	rep, err = Run(root, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return root, rep
}

// findView returns one view outcome by display name.
func findView(t *testing.T, rep *Report, name string) ViewOutcome {
	t.Helper()
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.DisplayName == name {
				return v
			}
		}
	}
	t.Fatalf("no view named %q in the report; bases=%+v", name, rep.Bases)
	return ViewOutcome{}
}

// findProvisioned returns one provisioned type by name.
func findProvisioned(t *testing.T, rep *Report, recordType string) ProvisionedType {
	t.Helper()
	for _, p := range rep.Provisioned {
		if p.Type == recordType {
			return p
		}
	}
	t.Fatalf("record type %q was not provisioned; provisioned=%+v", recordType, rep.Provisioned)
	return ProvisionedType{}
}

// anchorNote is one real note of an unrelated type. Every fixture here needs
// at least one, or there is no schema set at all and the run proves nothing
// about the interaction between observed and provisioned types.
const anchorNote = "---\ntype: memo\nsubject: anchor\n---\n\nbody\n"

// ---------------------------------------------------------------------------
// THE SAFETY PROPERTY
// ---------------------------------------------------------------------------

// TestProvisioning_OrderingComparisonLeavesPropertyUndeclared is the whole
// defence of this design. A base that compares a property with `>` on a type
// no note carries must NOT get that property declared as text — because text
// compares lexically and would return rows the Obsidian view excludes.
//
// The assertion is deliberately made at three levels, because any one of them
// alone can pass for the wrong reason: the property is absent from the
// declaration, the view is DISABLED, and the disabling loss NAMES the property.
func TestProvisioning_OrderingComparisonLeavesPropertyUndeclared(t *testing.T) {
	_, rep := provisionVault(t, map[string]string{
		"Notes/Anchor.md": anchorNote,
		"Bases/Ledger.base": `filters:
  and:
    - type == "ledger-entry"
views:
  - type: table
    name: Big Ones
    filters:
      and:
        - amount > 100
    order:
      - file.name
      - vendor
      - amount
`,
	})

	p := findProvisioned(t, rep, "ledger-entry")

	// 1. `amount` is NOT declared, and `vendor` — referenced only as a display
	//    column — IS. The second half matters: without it this test would also
	//    pass for an implementation that provisioned nothing at all.
	for _, name := range p.Properties {
		if name == "amount" {
			t.Fatalf("`amount` was declared %v — an ordering comparison against a text property compares LEXICALLY (\"50\" > \"100\" is true), so declaring it broadens the view (FR-105). Declared: %v",
				p.Properties, p.Properties)
		}
	}
	if !contains(p.Properties, "vendor") {
		t.Fatalf("`vendor` should be declared — it is only ever a display column, which cannot decide a row set. Declared: %v", p.Properties)
	}

	// 2. The omission is REPORTED, not silent. A property missing from a
	//    schema with no reason given is a decision the operator cannot correct.
	var omission string
	for _, o := range p.Omitted {
		if o.Property == "amount" {
			omission = o.Reason
		}
	}
	if omission == "" {
		t.Fatalf("`amount` was left undeclared but not reported in Omitted: %+v", p.Omitted)
	}
	if !strings.Contains(omission, ">") {
		t.Errorf("the omission reason should name the operator that caused it, got: %s", omission)
	}

	// 3. The view is DISABLED, and the disabling loss names `amount`. This is
	//    the end-to-end half: the undeclared property has to actually reach
	//    FR-105's partition, not merely be absent from a struct.
	v := findView(t, rep, "Big Ones")
	if v.Status == OutcomeRefused {
		t.Fatalf("the view was REFUSED, so provisioning did not happen at all: %s", v.RefusedReason)
	}
	if !v.Disabled {
		t.Fatalf("the view carries a dropped row-set clause (`amount > 100`) and MUST be disabled; losses=%v", v.Losses)
	}
	if !anyContains(v.DisablingLosses, "amount") {
		t.Fatalf("the disabling loss must NAME the undeclared property, got: %v", v.DisablingLosses)
	}
}

// TestProvisioning_ContainsLeavesPropertyUndeclared is the same partition's
// other half. `prop.contains("x")` on a text property becomes `LIKE '%x%'` —
// substring matching — which matches strictly more values than Obsidian's
// whole-element list membership. With no note to say whether the property is a
// list, declaring it text would broaden.
func TestProvisioning_ContainsLeavesPropertyUndeclared(t *testing.T) {
	_, rep := provisionVault(t, map[string]string{
		"Notes/Anchor.md": anchorNote,
		"Bases/Tagged.base": `filters:
  and:
    - type == "tagged-thing"
views:
  - type: table
    name: Investors
    filters:
      and:
        - segment.contains("investor")
    order:
      - file.name
      - segment
      - label
`,
	})

	p := findProvisioned(t, rep, "tagged-thing")
	if contains(p.Properties, "segment") {
		t.Fatalf("`segment` was declared %v — on text `.contains` becomes substring matching, which is broader than list membership (FR-105)", p.Properties)
	}
	if !contains(p.Properties, "label") {
		t.Fatalf("`label` is a display-only column and should be declared; got %v", p.Properties)
	}
	v := findView(t, rep, "Investors")
	if !v.Disabled {
		t.Fatalf("dropping a `contains` filter changes the row set, so the view must be disabled; losses=%v", v.Losses)
	}
}

// TestProvisioning_UnsafeUseAnywhereSuppressesTheDeclaration guards the
// precedence rule. A property used safely in one view and unsafely in another
// must NOT be declared: the unsafe clause is in the base file whatever else the
// base also does, and declaring the property would broaden that clause.
//
// Written with the safe view FIRST so an implementation that lets a later safe
// use overwrite an earlier omission fails here.
func TestProvisioning_UnsafeUseAnywhereSuppressesTheDeclaration(t *testing.T) {
	_, rep := provisionVault(t, map[string]string{
		"Notes/Anchor.md": anchorNote,
		"Bases/Mixed.base": `filters:
  and:
    - type == "mixed-thing"
views:
  - type: table
    name: By Score Equal
    filters:
      and:
        - score == "high"
    order:
      - file.name
      - label
      - score
  - type: table
    name: By Score Ordered
    filters:
      and:
        - score > 10
    order:
      - file.name
      - label
      - score
`,
	})

	p := findProvisioned(t, rep, "mixed-thing")
	// `label` keeps the type provisionable, so this asserts EXCLUSION of
	// `score` rather than the whole type falling away for want of any
	// declarable property.
	if !contains(p.Properties, "label") {
		t.Fatalf("`label` should be declared; got %v", p.Properties)
	}
	if contains(p.Properties, "score") {
		t.Fatalf("`score` is compared with `>` in one view, so it must not be declared anywhere; declared %v", p.Properties)
	}
	// Both views lose the clause; the equality one loses it too, because the
	// property is undeclared for the whole type. That is the cost of the
	// conservative rule and it is the right cost: a type has ONE schema.
	if v := findView(t, rep, "By Score Ordered"); !v.Disabled {
		t.Errorf("the ordering view must be disabled; losses=%v", v.Losses)
	}
}

// ---------------------------------------------------------------------------
// WHAT IS DECLARED
// ---------------------------------------------------------------------------

// TestProvisioning_DeclaresOnlyBaseNamedPropertiesAsPlainText pins the shape of
// the declaration itself. Everything is text, scalar and optional — the three
// choices that make the schema unable to REJECT the first real note the
// operator writes, which is the strongest objection to provisioning at all.
func TestProvisioning_DeclaresOnlyBaseNamedPropertiesAsPlainText(t *testing.T) {
	root, rep := provisionVault(t, map[string]string{
		"Notes/Anchor.md": anchorNote,
		"Bases/Compliance.base": `filters:
  and:
    - file.inFolder("Areas")
    - type == "compliance"
views:
  - type: table
    name: By Authority
    groupBy:
      property: authority
      direction: ASC
    order:
      - file.name
      - authority
      - due_date
      - status
`,
	})

	p := findProvisioned(t, rep, "compliance")
	want := []string{"authority", "due_date", "status"}
	if strings.Join(p.Properties, ",") != strings.Join(want, ",") {
		t.Fatalf("declared properties = %v, want exactly the names the base writes %v — no invented scaffolding, no `file.*`, no `type`",
			p.Properties, want)
	}

	// The written file is the artefact that matters, so it is read back through
	// the REAL loader rather than asserted against the in-memory struct.
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the provisioned schema does not load: %+v", report.Rejections)
	}
	sc, ok := set.Get("compliance")
	if !ok {
		t.Fatalf("the loader does not know the provisioned type; types=%v", set.Types())
	}
	for _, name := range want {
		prop := sc.Properties[name]
		if prop == nil {
			t.Fatalf("property %q missing from the loaded schema", name)
		}
		if prop.Type != records.TypeText {
			t.Errorf("property %q declared %s, want text — text is the one type never validated for shape, so it cannot reject a future note", name, prop.Type)
		}
		if prop.Many {
			t.Errorf("property %q declared many — arity IS enforced, so asserting it would reject a scalar the operator writes", name)
		}
		if prop.Required {
			t.Errorf("property %q declared required — nothing in a `.base` file states an obligation", name)
		}
	}
}

// TestProvisioning_SchemaFileCarriesItsOwnAccount checks the header comment.
// The console report scrolls away; the schema file is where the correction is
// made, so the account has to be beside the thing being edited — and it must
// not change what the schema MEANS, which is why the file is also parsed.
func TestProvisioning_SchemaFileCarriesItsOwnAccount(t *testing.T) {
	root, _ := provisionVault(t, map[string]string{
		"Notes/Anchor.md": anchorNote,
		"Bases/Hiring.base": `filters:
  and:
    - type == "candidate"
views:
  - type: table
    name: Pipeline
    order:
      - file.name
      - role
      - stage
`,
	})

	data, err := os.ReadFile(filepath.Join(records.SchemaDir(root), "candidate.yaml"))
	if err != nil {
		t.Fatalf("reading the provisioned schema: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"DECLARED FROM",       // says it was not observed
		"Bases/Hiring.base",   // says which base
		"knowledge_configure", // says how to correct it
		"role", "stage",       // says what was assumed
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the schema file header does not mention %q; header:\n%s", want, firstLines(text, 12))
		}
	}
	// The account is a COMMENT: it must not survive into the parsed schema.
	sc, rej := records.ParseSchema("candidate.yaml", data)
	if rej != nil {
		t.Fatalf("the header comment broke the parser: %+v", rej)
	}
	if sc.Type != "candidate" {
		t.Errorf("parsed type = %q, want candidate", sc.Type)
	}
}

// ---------------------------------------------------------------------------
// WHAT IS NOT PROVISIONED
// ---------------------------------------------------------------------------

// TestProvisioning_ObservedTypeIsNeverProvisioned — a type real notes carry is
// inferred from those notes and never touched by provisioning, whatever its
// base says. Observation outranks a base file's word for it.
func TestProvisioning_ObservedTypeIsNeverProvisioned(t *testing.T) {
	_, rep := provisionVault(t, map[string]string{
		"Notes/Real.md": "---\ntype: task\nstatus: open\npriority: 3\n---\n\nbody\n",
		"Bases/Tasks.base": `filters:
  and:
    - type == "task"
views:
  - type: table
    name: Open
    filters:
      and:
        - status == "open"
    order:
      - file.name
      - status
`,
	})

	for _, p := range rep.Provisioned {
		if p.Type == "task" {
			t.Fatalf("`task` has real notes and must be inferred, not provisioned: %+v", p)
		}
	}
	// And the inferred schema keeps its OBSERVED types — proving provisioning
	// did not overwrite it with a flat text declaration.
	var found bool
	for _, ts := range rep.Types {
		if ts.Type == "task" {
			found = true
			if ts.NoteCount != 1 {
				t.Errorf("task NoteCount = %d, want 1", ts.NoteCount)
			}
			if ts.IntegerCount == 0 && ts.EnumCount == 0 {
				t.Errorf("task was declared entirely as text (%+v) — provisioning appears to have overwritten the inferred schema", ts)
			}
		}
	}
	if !found {
		t.Fatalf("task missing from the inferred type summaries")
	}
}

// TestProvisioning_TypeWithNoDeclarablePropertyIsNotProvisioned guards the
// RejectNoProperties trap. records.ParseSchema refuses a schema whose
// `properties:` mapping is empty, so provisioning a type that yields none would
// trade a named view refusal for a schema-load failure — strictly worse, and it
// would take the whole reload down rather than one view.
func TestProvisioning_TypeWithNoDeclarablePropertyIsNotProvisioned(t *testing.T) {
	root, rep := provisionVault(t, map[string]string{
		"Notes/Anchor.md": anchorNote,
		"Bases/Bare.base": `filters:
  and:
    - type == "bare-thing"
views:
  - type: table
    name: Everything
    order:
      - file.name
`,
	})

	for _, p := range rep.Provisioned {
		if p.Type == "bare-thing" {
			t.Fatalf("a type whose base names no usable property must not be provisioned: %+v", p)
		}
	}
	if _, err := os.Stat(filepath.Join(records.SchemaDir(root), "bare-thing.yaml")); !os.IsNotExist(err) {
		t.Fatalf("no schema file should have been written for `bare-thing` (stat err = %v)", err)
	}
	// The schema set must still load — the point of the guard.
	_, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the schema set does not load: %+v", report.Rejections)
	}
	// And the view keeps its honest refusal rather than a broken import.
	if v := findView(t, rep, "Everything"); v.Status != OutcomeRefused {
		t.Errorf("the view should still be REFUSED, got %s", v.Status)
	}
}

// TestProvisioning_IllegalTypeNameIsNotProvisioned — a base may not smuggle in
// a record type name a NOTE would have been refused for. The type name becomes
// a file name, and validRecordTypeName is the one grammar that decides it.
func TestProvisioning_IllegalTypeNameIsNotProvisioned(t *testing.T) {
	root, rep := provisionVault(t, map[string]string{
		"Notes/Anchor.md": anchorNote,
		"Bases/Evil.base": `filters:
  and:
    - type == "../../../pwned"
views:
  - type: table
    name: Escape
    order:
      - file.name
      - owner
`,
	})

	for _, p := range rep.Provisioned {
		if strings.Contains(p.Type, "..") || strings.Contains(p.Type, "/") {
			t.Fatalf("provisioned an illegal type name %q — it becomes a file path", p.Type)
		}
	}
	outside := filepath.Join(filepath.Dir(root), "pwned.yaml")
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("a file was written outside the vault at %s", outside)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func anyContains(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// TWO CASES THE BLOCK ABOVE DOES NOT COVER
// ---------------------------------------------------------------------------

// A base asking for a numeric SUMMARY is the operator's own statement that the
// property is not prose — the one display position that is evidence about a
// property's TYPE rather than just its name. Declaring it `text` anyway would
// carry a summary the serve path then refuses by name
// (knowledgefind/request.go's opDefinedForType), so the property is left
// undeclared and the dropped summary is reported.
//
// This must cost the view NO rows: loss.go classifies `aggregates` as an
// annotation, so the view still imports ENABLED. Asserting that is the point —
// a carve-out that quietly disabled the view would trade one silent harm for
// another.
func TestProvisioning_NumericSummaryLeavesPropertyUndeclared(t *testing.T) {
	_, rep := provisionVault(t, map[string]string{
		"notes/anchor.md": anchorNote,
		"bases/AR.base": `
filters:
  and:
    - type == "invoice"
views:
  - type: table
    name: Aging
    order:
      - client
      - amount
    summaries:
      amount: Sum
`,
	})

	p := findProvisioned(t, rep, "invoice")
	if contains(p.Properties, "amount") {
		t.Errorf("`amount` was declared `text` while the base asks for Sum(amount); a sum over prose is refused at serve time and means nothing. Properties=%v", p.Properties)
	}
	if !contains(p.Properties, "client") {
		t.Errorf("`client` is only ever a display column and must still be declared. Properties=%v", p.Properties)
	}
	var reason string
	for _, o := range p.Omitted {
		if o.Property == "amount" {
			reason = o.Reason
		}
	}
	if strings.TrimSpace(reason) == "" {
		t.Fatalf("`amount` was dropped with no reason recorded — a property dropped without a word is the silence this package exists to remove. Omitted=%+v", p.Omitted)
	}

	v := findView(t, rep, "Aging")
	if v.Disabled {
		t.Errorf("the view was DISABLED over a dropped SUMMARY; an aggregate cannot change a row set (loss.go's partition), so this trades one silent harm for another. DisablingLosses=%v", v.DisablingLosses)
	}
	if !anyContains(v.Losses, "amount") {
		t.Errorf("the dropped summary was not named in the view's losses: %v", v.Losses)
	}
}

// A view whose record type the translator cannot resolve — folder-scoped with
// no `type ==` at all, or asserting two types at once — is refused (or written
// untyped) for a reason provisioning does not address. Declaring a schema for
// it would fix nothing and would hide the real cause.
//
// Both halves matter and are asserted separately. Nothing is provisioned; AND
// the run still SUCCEEDS and the folder-scoped view is still written untyped
// (FR-018b). Under mutation — removing the unresolved-type guard and the
// type-name guard together — this test dies on the second half first: the
// import aborts with `refusing to write a schema for record type ""`. A guard
// removed here does not produce a subtly wrong view, it takes the whole import
// down, which is why the success of the run is part of the oracle.
func TestProvisioning_ViewWithNoSingleResolvedTypeIsNotProvisioned(t *testing.T) {
	for _, tc := range []struct {
		name string
		base string
		// wantUntypedView is the display name of a view that must still be
		// written, with NO `type:`, because FR-018b keeps a folder-scoped view
		// expressible. Empty when the view is legitimately refused instead.
		wantUntypedView string
	}{
		{"folder-scoped, no type literal anywhere", `
filters:
  and:
    - file.inFolder("notes")
views:
  - type: table
    name: Folder scoped
    order:
      - status
`, "Folder scoped"},
		{"the view asserts two types at once", `
views:
  - type: table
    name: Two types
    filters:
      and:
        - type == "alpha"
        - type == "beta"
    order:
      - status
`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, rep := provisionVault(t, map[string]string{
				"notes/anchor.md": anchorNote,
				"bases/X.base":    tc.base,
			})
			if len(rep.Provisioned) != 0 {
				t.Fatalf("provisioned a type for a view whose type does not resolve: %+v", rep.Provisioned)
			}
			if tc.wantUntypedView == "" {
				return
			}
			v := findView(t, rep, tc.wantUntypedView)
			if v.Status == OutcomeRefused {
				t.Fatalf("the folder-scoped view was REFUSED; FR-018b keeps an untyped view expressible and provisioning must not have changed that. reason=%s", v.RefusedReason)
			}
			if v.ResolvedType != "" {
				t.Fatalf("the folder-scoped view acquired a record type %q; provisioning must never give a view a type its own base did not state", v.ResolvedType)
			}
		})
	}
}
