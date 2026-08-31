// Omnipus — a formula declared on a SCHEMA property is refused, never answered
// with a blank column (ADR-068 D24.3 / FR-140 / FR-141).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// WHAT THESE TESTS PIN
//
// Before this change, a schema-declared formula LOADED, VALIDATED and then
// rendered as an empty column in an answer that called itself COMPLETE. That is
// the one failure mode this whole surface is written against: an operator
// reading a blank `double_height` next to a populated `height_cm` concludes
// their notes are wrong, not that the feature was never wired.
//
// The refusal is asserted through Find, on the rendered text a model actually
// reads — not on the helper that builds it. A test over refuseSchemaDeclaredFormulas
// alone would keep passing if the call site were deleted, which is precisely the
// shape of green that produced the defect.
// ---------------------------------------------------------------------------

// The fixture vault declares one record type WITH a derived property and one
// without, so the blast radius of the refusal is measured rather than asserted.
const derivedTypeYAML = `
schema_version: 1
type: plant
label: Plant
properties:
  species:       { type: text }
  height_cm:     { type: decimal }
  double_height: { type: decimal, formula: "height_cm * 2" }
`

const plainTypeYAML = `
schema_version: 1
type: bed
label: Bed
properties:
  name:   { type: text }
  slots:  { type: integer }
`

// formulaVault loads the schemas above through records.LoadSchemas — the
// production path — and indexes one note of each type.
func formulaVault(t *testing.T) Deps {
	t.Helper()

	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for name, src := range map[string]string{"plant.yaml": derivedTypeYAML, "bed.yaml": plainTypeYAML} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		// The whole point of the refusal below is that the LOADER accepts this
		// schema. A rejection here would mean the gap has moved and these tests
		// are asserting against a vault that no longer exists.
		t.Fatalf("the fixture schemas were rejected by the loader: %v", report.Rejections)
	}

	store, err := propindex.Open(context.Background(), filepath.Join(t.TempDir(), "properties.db"), propindex.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	text := &stubText{hits: map[string]TextHit{}}
	notes := map[string]string{
		"garden/plant-1.md": "---\ntype: plant\nspecies: Monstera deliciosa\nheight_cm: 12.5\n---\n\n# A plant\n",
		"garden/bed-1.md":   "---\ntype: bed\nname: East wall\nslots: 6\n---\n\n# A bed\n",
	}
	for path, src := range notes {
		b := []byte(src)
		rec := records.ParseRecord(path, b)
		sc, _ := set.Get(rec.TypeName())
		rows := propindex.BuildNoteRows(rec, sc, b, propindex.SourceHash(b))
		if err := store.UpsertNote(context.Background(), rows); err != nil {
			t.Fatalf("UpsertNote(%s): %v", path, err)
		}
		text.hits[path] = TextHit{Path: path, SourceHash: rows.SourceHash, Score: 1}
	}

	return Deps{Schemas: set, Store: store, Text: text, Epoch: 4211}
}

// TestSchemaDeclaredFormula_IsRefusedNamingTheRemedy is the behavioural proof.
func TestSchemaDeclaredFormula_IsRefusedNamingTheRemedy(t *testing.T) {
	d := formulaVault(t)
	typ := "plant"
	sel := []string{"species", "height_cm", "double_height"}

	resp, err := Find(context.Background(), d, generated.VaultFindRequest{Type: &typ, Select: &sel})
	if err == nil {
		t.Fatalf("a query over a record type declaring a formula property was ANSWERED, not refused.\n"+
			"Rendered:\n%s", Render(resp))
	}

	rendered := Render(resp)

	// It must not have answered. A refusal that still ships rows is the
	// half-reachable state, wearing a warning.
	if len(resp.Rows) != 0 {
		t.Errorf("the refusal returned %d row(s); a refusal evaluates nothing", len(resp.Rows))
	}
	if !resp.Refused {
		t.Errorf("the response is not marked refused, so a caller cannot tell it from a partial answer:\n%s", rendered)
	}
	if resp.Complete {
		t.Errorf("the refusal reports itself COMPLETE:\n%s", rendered)
	}

	// The message must carry everything an author needs to act without opening
	// another file: which property, which file, and where the formula belongs
	// instead.
	for _, want := range []string{
		`"double_height"`,
		"plant.yaml",
		"`formulas:` map",
		FormulaNamespace + "double_height",
		"FR-140/FR-141",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the refusal an author reads does not mention %q.\nRendered:\n%s", want, rendered)
		}
	}
}

// TestSchemaDeclaredFormula_RefusesEvenWhenTheQueryNeverNamesIt.
//
// The refusal is a property of the TYPE, not of the argument list, because a
// query that merely selects `species` over the same type is answered from a
// schema whose derived property is a lie the operator has not hit YET. Refusing
// only the queries that name it is how a broken schema stays in a vault for
// months.
func TestSchemaDeclaredFormula_RefusesEvenWhenTheQueryNeverNamesIt(t *testing.T) {
	d := formulaVault(t)
	typ := "plant"
	sel := []string{"species"}

	resp, err := Find(context.Background(), d, generated.VaultFindRequest{Type: &typ, Select: &sel})
	if err == nil {
		t.Fatalf("a query over the offending type was answered because it did not name the derived "+
			"property.\nRendered:\n%s", Render(resp))
	}
}

// TestSchemaDeclaredFormula_OtherRecordTypesAreUnaffected bounds the blast
// radius to exactly what a per-schema load rejection would cost.
func TestSchemaDeclaredFormula_OtherRecordTypesAreUnaffected(t *testing.T) {
	d := formulaVault(t)
	typ := "bed"
	sel := []string{"name", "slots"}

	resp, err := Find(context.Background(), d, generated.VaultFindRequest{Type: &typ, Select: &sel})
	if err != nil {
		t.Fatalf("a record type declaring no formula was refused: %v", err)
	}
	if len(resp.Rows) != 1 {
		t.Fatalf("expected the one indexed bed, got %d row(s):\n%s", len(resp.Rows), Render(resp))
	}
	if rendered := Render(resp); !strings.Contains(rendered, "East wall") {
		t.Errorf("the unaffected type's answer lost its data:\n%s", rendered)
	}
}

// TestSchemaDeclaredFormula_UntypedQueryIsNotRefused.
//
// An untyped query resolves no typed property at all (FR-018d), so it cannot
// reach a derived one and there is nothing to refuse. Refusing it would take
// the whole vault down for one schema file.
func TestSchemaDeclaredFormula_UntypedQueryIsNotRefused(t *testing.T) {
	d := formulaVault(t)
	words := "plant"
	if st, ok := d.Text.(*stubText); ok {
		st.only = []string{"garden/plant-1.md"}
	}

	if _, err := Find(context.Background(), d, generated.VaultFindRequest{Words: &words}); err != nil {
		t.Fatalf("an untyped query was refused because some OTHER record type declares a formula: %v", err)
	}
}

// ---------------------------------------------------------------------------
// THE SEAM, PINNED — the loader still ACCEPTS what the query path refuses
//
// The right home for this refusal is records.validateSchemaFormulas, so
// LoadSchemas rejects the file once, when the operator writes it, instead of on
// every query forever. That function is in pkg/records/schema.go, outside this
// change's ownership, so the refusal lives in find.go meanwhile.
//
// This test fails THE DAY THE LOADER STARTS REFUSING, so nobody has to remember
// to come back and remove the duplicate.
// ---------------------------------------------------------------------------

func TestSeam_SchemaLoaderStillAcceptsAPropertyFormula(t *testing.T) {
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plant.yaml"), []byte(derivedTypeYAML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("records.LoadSchemas now REJECTS a schema-property formula: %v\n"+
			"That is the intended outcome, not a regression — the refusal has reached the loader, where it "+
			"costs one message at authoring time instead of one per query. Delete refuseSchemaDeclaredFormulas\n"+
			"and its call site in find.go, delete this test, and keep the behavioural tests above only if\n"+
			"they still describe something the loader does not.",
			report.Rejections)
	}
}
