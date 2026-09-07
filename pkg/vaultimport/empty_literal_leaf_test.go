// Omnipus — `prop != ""` and `prop == ""` on a TEXT property: the empty
// LITERAL, which is a different question from `IS NOT NULL`.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS ABOUT
//
// `last_refreshed != ""` was refused for years-worth of runs with a reason
// that was CORRECT and INCOMPLETE: FR-007a keeps `""` a PRESENT value on a
// text property, so `IS NOT NULL` also matches the record whose value IS the
// empty string — a record the Obsidian filter excludes. All true. What nobody
// asked was whether the translation had to be `IS NOT NULL` at all.
//
// It does not. The engine compares against an EMPTY LITERAL:
// `VaultFilterNode.value` carries no `minLength`, knowledgefind's buildLeaf
// sets `LiteralGiven` from `value != nil` rather than from the string being
// non-empty, and records.Filter.LiteralGiven exists in as many words because
// "the empty string is a legitimate value for `=`". `<>` and `=` are both
// defined on text (operatorDefinedForType[TypeText]).
//
// The tests below are in three layers, and the middle one is the point:
//
//	1. the COMPARATOR's own verdicts, so the claim is measured rather than
//	   assumed, and measured against the product's real matching layer;
//	2. the WHOLE IMPORTER, driven from a `.base` file on disk — no test in
//	   this file calls buildV2LeafNode directly, because a rule that is
//	   correct and UNREACHABLE keeps its package green;
//	3. the TREE, because a proof about a leaf is not a proof about a view.
//	   A clause under a `not:` inverts, and a translation that only ever
//	   NARROWS becomes a BROADENING there.
// ---------------------------------------------------------------------------

// TestEmptyLiteral_TheComparatorSeparatesAbsentFromEmptyOnText is layer 1: what
// the product's matching layer actually answers for a text property in each of
// its three states.
//
// The expected column is derived from the rules, not from this importer:
//
//	FR-007a  on TEXT, `""` is a PRESENT value — absence and emptiness are two
//	         different states, which is why `IS NULL`/`IS NOT NULL` cannot tell
//	         the operator's question.
//	§8 R-2   a comparison where either side is absent is FALSE, for every
//	         operator except `IS NULL`. The leaf carries no Negate, so FR-008's
//	         re-inclusion — a property of the negative OPERATOR, not of a
//	         combinator — never applies.
//
// The `IS NOT NULL` row is deliberately asserted alongside: it is the operator
// the old refusal was about, and this test fails if anyone translates
// `prop != ""` back to it.
func TestEmptyLiteral_TheComparatorSeparatesAbsentFromEmptyOnText(t *testing.T) {
	schema, rej := records.ParseSchema("connector.yaml",
		[]byte("schema_version: 1\ntype: connector\nproperties:\n  venture:\n    type: text\n"))
	if rej != nil {
		t.Fatalf("fixture schema: %v", rej)
	}

	notes := map[string]records.Record{
		"absent": records.ParseRecord("absent.md", []byte("---\ntype: connector\n---\n")),
		"empty":  records.ParseRecord("empty.md", []byte("---\ntype: connector\nventure: \"\"\n---\n")),
		"value":  records.ParseRecord("value.md", []byte("---\ntype: connector\nventure: elicify\n---\n")),
	}

	type want struct{ absent, empty, value bool }
	cases := []struct {
		op      records.Operator
		literal bool // whether an (empty) literal is supplied
		want    want
		why     string
	}{
		{records.OpIsNotNull, false, want{false, true, true},
			"`IS NOT NULL` is the operator the refusal was about: it admits the PRESENT empty string, which is the row Obsidian's `!= \"\"` excludes"},
		{records.OpNotEqual, true, want{false, false, true},
			"`<> \"\"` is present-and-not-empty: absent is false by R-2, `\"\"` equals the literal, a value does not"},
		{records.OpEqual, true, want{false, true, false},
			"`= \"\"` is present-and-empty: absent is still false by R-2, so it is not `IS NULL` in disguise"},
		{records.OpIsNull, false, want{true, false, false},
			"`IS NULL` is the operator `== \"\"` was refused as: it admits the record that never declared the property"},
	}

	for _, tc := range cases {
		t.Run(string(tc.op), func(t *testing.T) {
			f := records.Filter{Property: "venture", Op: tc.op}
			if tc.literal {
				f.Literal, f.LiteralGiven = "", true
			}
			got := map[string]bool{}
			for name, rec := range notes {
				res, err := f.Match(schema, rec)
				if err != nil {
					t.Fatalf("the engine refused `venture %s \"\"`, so this translation is not available at all: %v", tc.op, err)
				}
				if len(res.Problems) != 0 || len(res.ComparisonProblems) != 0 {
					t.Fatalf("`venture %s \"\"` on the %s note reported problems %v / %v — a comparison that could not be MADE is not a translation",
						tc.op, name, res.Problems, res.ComparisonProblems)
				}
				got[name] = res.Matched
			}
			if got["absent"] != tc.want.absent || got["empty"] != tc.want.empty || got["value"] != tc.want.value {
				t.Errorf("`venture %s \"\"`: absent=%v empty=%v value=%v, want absent=%v empty=%v value=%v\n  %s",
					tc.op, got["absent"], got["empty"], got["value"],
					tc.want.absent, tc.want.empty, tc.want.value, tc.why)
			}
		})
	}
}

// emptyLiteralVault writes a minimal vault whose `venture` property is
// inferred as TEXT, with one note per state, and returns its root.
//
// SIXTEEN DISTINCT venture values, and the number is load-bearing rather than
// arbitrary: infer.go treats a property with at most `enumMaxDistinct` (15)
// distinct values as an `enum`, and on an enum FR-007a already makes `""` the
// absent state, so `IS NOT NULL` is the faithful translation and the whole
// TEXT question this file is about would never be reached. A fixture that
// inferred an enum would pass these tests while exercising nothing. The test
// asserts the inferred type as well, so this cannot go quietly stale.
func emptyLiteralVault(t *testing.T, viewFilter string) string {
	t.Helper()
	root := t.TempDir()
	ventures := []string{
		"elicify", "senantrix-ai", "omnipus-office", "myagentgigs",
		"rendara", "myclaw", "elicify-team-mcp", "omnipus",
		"clawhub", "sovereign-deep", "forge-gold", "liquid-silver",
		"deep-space", "octopus", "quill", "lantern",
	}
	for i, v := range ventures {
		body := "---\ntype: connector\nplatform: x\nventure: " + v + "\n---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("acc-%02d.md", i)), []byte(body), 0o600); err != nil {
			t.Fatalf("writing note %d: %v", i, err)
		}
	}
	// The two rows the whole question is about: one that WROTE an empty
	// string, and one that never declared the property at all.
	for name, extra := range map[string]string{
		"acc-empty":  "venture: \"\"\n",
		"acc-absent": "",
	} {
		body := "---\ntype: connector\nplatform: x\n" + extra + "---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(root, name+".md"), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	base := "filters:\n  and:\n    - type == \"connector\"\nviews:\n  - type: table\n    name: Ventures\n    filters:\n" + viewFilter
	if err := os.WriteFile(filepath.Join(root, "Connectors.base"), []byte(base), 0o600); err != nil {
		t.Fatalf("writing the base: %v", err)
	}
	return root
}

// emptyLiteralWantRows is every note the `!= ""` view must select: the sixteen
// that carry a venture, and neither `acc-empty` nor `acc-absent`.
func emptyLiteralWantRows() []string {
	out := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		out = append(out, fmt.Sprintf("acc-%02d", i))
	}
	sort.Strings(out)
	return out
}

// requireVentureIsText fails if the run did not infer `venture` as TEXT, which
// is the precondition every assertion in this file depends on.
func requireVentureIsText(t *testing.T, root string) {
	t.Helper()
	schemas, rep, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("reloading schemas: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("schemas rejected: %v", rep.Rejections)
	}
	sc, ok := schemas.Get("connector")
	if !ok {
		t.Fatal("the run declared no `connector` schema")
	}
	prop, ok := sc.Property("venture")
	if !ok {
		t.Fatal("the run declared no `venture` property")
	}
	if prop.Type != records.TypeText {
		t.Fatalf("`venture` was inferred as %s, not text — this fixture no longer exercises the TEXT branch at all", prop.Type)
	}
}

// emptyLiteralImport runs the WHOLE importer over that vault and returns the
// report plus the one view's outcome.
func emptyLiteralImport(t *testing.T, viewFilter string) (root string, vo ViewOutcome) {
	t.Helper()
	root = emptyLiteralVault(t, viewFilter)
	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.DisplayName == "Ventures" {
				return root, v
			}
		}
	}
	t.Fatalf("the importer produced no view named Ventures (report: %+v)", rep.Bases)
	return "", ViewOutcome{}
}

// emptyLiteralReloadedLeaf loads the written view through records.LoadViews —
// the real loader, which decodes the file with DisallowUnknownFields — and
// returns its single filter leaf.
func emptyLiteralReloadedLeaf(t *testing.T, root, outputRelPath string) generated.VaultFilterNode {
	t.Helper()
	schemas, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("reloading schemas: %v", err)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("reloading views: %v", err)
	}
	if !viewRep.OK() {
		t.Fatalf("the importer wrote a view the real loader rejects: %v", viewRep.Rejections)
	}
	slug := strings.TrimSuffix(filepath.Base(outputRelPath), ".yaml")
	sv, ok := views.Get(slug)
	if !ok {
		t.Fatalf("the loader knows no view %q", slug)
	}
	if sv.Def.Filter == nil {
		t.Fatal("the written view carries no filter at all")
	}
	n := *sv.Def.Filter
	if n.Property == nil {
		t.Fatalf("the written view's filter is not a single leaf: %+v", n)
	}
	return n
}

// emptyLiteralRows re-reads the written view through the PRODUCT's own loader
// and view->find bridge and returns the notes it selects.
func emptyLiteralRows(t *testing.T, root, outputRelPath string) []string {
	t.Helper()
	schemas, schemaRep, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("reloading schemas: %v", err)
	}
	if !schemaRep.OK() {
		t.Fatalf("the importer wrote schemas the real loader rejects: %v", schemaRep.Rejections)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("reloading views: %v", err)
	}
	if !viewRep.OK() {
		t.Fatalf("the importer wrote a view the real loader rejects: %v", viewRep.Rejections)
	}
	slug := strings.TrimSuffix(filepath.Base(outputRelPath), ".yaml")
	req, servable := records.NewViewFindLoader(views).View(slug)
	if !servable {
		t.Fatalf("view %q is not servable", slug)
	}
	if req.Type == nil {
		t.Fatal("the bridge produced a request with no record type")
	}
	schema, ok := schemas.Get(*req.Type)
	if !ok {
		t.Fatalf("no schema for %q", *req.Type)
	}
	var out []string
	for _, note := range emptyLiteralNotes(t, root) {
		if note.Rec.TypeName() != *req.Type {
			continue
		}
		if req.Filter == nil || fr105EvalNode(t, *req.Filter, schema, note) {
			out = append(out, strings.TrimSuffix(filepath.Base(note.RelPath), ".md"))
		}
	}
	sort.Strings(out)
	return out
}

func emptyLiteralNotes(t *testing.T, root string) []fr105Note {
	t.Helper()
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning: %v", err)
	}
	out := make([]fr105Note, 0, len(inv.Notes))
	for _, abs := range inv.Notes {
		data, readErr := os.ReadFile(abs) //nolint:gosec // path from this run's own scan
		if readErr != nil {
			t.Fatalf("reading %s: %v", abs, readErr)
		}
		rel, relErr := filepath.Rel(inv.Root, abs)
		if relErr != nil {
			t.Fatalf("relativising: %v", relErr)
		}
		out = append(out, fr105Note{Rec: records.ParseRecord(abs, data), RelPath: filepath.ToSlash(rel)})
	}
	return out
}

// TestEmptyStringIsSet_TranslatesThroughTheWholeImporter is layer 2, and it is
// driven from a `.base` FILE rather than from a v2Leaf literal on purpose: a
// test that calls the leaf builder itself can never fail for a missing CALL to
// it, and the value of this rule is entirely in being wired.
//
// It asserts the three things that can each be true without the other two: the
// clause is not a loss, the view is not disabled, and the file on disk carries
// the EMPTY LITERAL rather than `IS NOT NULL`.
func TestEmptyStringIsSet_TranslatesThroughTheWholeImporter(t *testing.T) {
	root, vo := emptyLiteralImport(t, "      and:\n        - venture != \"\"\n")
	requireVentureIsText(t, root)

	if vo.ResolvedType != "connector" {
		t.Fatalf("the view resolved to type %q, want connector", vo.ResolvedType)
	}
	for _, l := range vo.Losses {
		if strings.Contains(l, `venture != ""`) {
			t.Errorf("`venture != \"\"` is still a named loss: %s", l)
		}
	}
	if vo.Disabled {
		t.Fatalf("the view is DISABLED; disabling losses: %v", vo.DisablingLosses)
	}

	// The leaf as the PRODUCT's own loader reads it back off disk. Asserting
	// the reloaded node rather than the file's text is what proves the empty
	// literal SURVIVES the YAML round-trip: a `value:` that came back as
	// nothing would leave `<>` with no operand and no error to show for it.
	leaf := emptyLiteralReloadedLeaf(t, root, vo.OutputRelPath)
	if leaf.Op == nil || *leaf.Op != generated.LessThanGreaterThan {
		t.Errorf("the written view's operator is %v, want `<>` — `IS NOT NULL` admits the PRESENT empty string", leaf.Op)
	}
	if leaf.Value == nil {
		t.Fatal("the written view carries no `value` at all, so `<>` has nothing to compare against")
	}
	if *leaf.Value != "" {
		t.Errorf("the written view compares against %q, want the EMPTY literal", *leaf.Value)
	}

	// The row set, decided by the product's own loader, bridge and comparator.
	// `acc-empty` is the row that separates this translation from `IS NOT NULL`
	// and `acc-absent` is the row that separates it from a `not:` over `= ""`.
	want := emptyLiteralWantRows()
	if rows := emptyLiteralRows(t, root, vo.OutputRelPath); !equalStrings(rows, want) {
		t.Errorf("the imported view selects %v, want %v — `acc-empty` and `acc-absent` must both be out", rows, want)
	}
}

// TestEmptyStringIsEmpty_TranslatesThroughTheWholeImporter is the mirror case,
// `prop == ""`, which was refused as `IS NULL` for the same shape of reason.
func TestEmptyStringIsEmpty_TranslatesThroughTheWholeImporter(t *testing.T) {
	root, vo := emptyLiteralImport(t, "      and:\n        - venture == \"\"\n")
	requireVentureIsText(t, root)

	if vo.Disabled {
		t.Fatalf("the view is DISABLED; disabling losses: %v", vo.DisablingLosses)
	}
	leaf := emptyLiteralReloadedLeaf(t, root, vo.OutputRelPath)
	if leaf.Op == nil || *leaf.Op != generated.Equal {
		t.Errorf("the written view's operator is %v, want `=` — `IS NULL` admits every record that never declared the property", leaf.Op)
	}
	if leaf.Value == nil {
		t.Fatal("the written view carries no `value` at all")
	}
	if *leaf.Value != "" {
		t.Errorf("the written view compares against %q, want the EMPTY literal", *leaf.Value)
	}

	// Exactly the one note that WROTE an empty string. `acc-absent` never
	// declared `venture` and Obsidian's comparison does not select it.
	want := []string{"acc-empty"}
	if rows := emptyLiteralRows(t, root, vo.OutputRelPath); !equalStrings(rows, want) {
		t.Errorf("the imported view selects %v, want %v", rows, want)
	}
}

// TestEmptyStringIsSet_UnderANotIsStillRefused is layer 3 — the tree.
//
// `<> ""` is at best a SUBSET of Obsidian's `!= ""`: the two agree exactly if
// Obsidian reads an absent property as not-`!= ""`, and ours is narrower if
// Obsidian answers JavaScript's `undefined != ""` — which is TRUE. FR-105
// permits the narrower answer. It does NOT survive a negation: knowledge_find
// evaluates a `not` node as a bare `!inner.matched` (tree.go), with no absence
// rule of its own, so `not: [venture != ""]` would select the absent notes
// as well as the empty one where Obsidian, on the JavaScript reading, selects
// only the empty one. That is MORE rows, which is the one direction FR-105
// forbids.
//
// So the clause is refused there, the group is lost, and the view disables.
func TestEmptyStringIsSet_UnderANotIsStillRefused(t *testing.T) {
	root, vo := emptyLiteralImport(t, "      not:\n        - venture != \"\"\n")
	requireVentureIsText(t, root)

	if !vo.Disabled {
		t.Fatalf("`not: [venture != \"\"]` was translated and the view left ENABLED — a subset leaf inverts into a superset under a negation (losses: %v)", vo.Losses)
	}
	joined := strings.Join(vo.DisablingLosses, "\n")
	if !strings.Contains(joined, "not:") {
		t.Errorf("the disabling loss does not name the `not:` group it lost:\n%s", joined)
	}
	if !strings.Contains(joined, "inside a `not:`") {
		t.Errorf("the loss does not say the NEGATION is what makes the clause untranslatable, so the next reader re-investigates the leaf:\n%s", joined)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
