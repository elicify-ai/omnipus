// Omnipus — a schema-property formula is refused by the LOADER, so it never
// reaches a query (ADR-068 D24.3 / FR-140 / FR-141).
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
// WHAT THESE TESTS PIN, AND WHAT THEY USED TO PIN
//
// This file used to assert that Find REFUSED every query over a record type
// whose schema declared `formula:` on a property, because the loader accepted
// such a schema and nothing evaluated the result. Two correct halves that did
// not agree: the file loaded clean, and then the whole record type was dead at
// query time — discovered later, by somebody else, on a query that had nothing
// to do with formulas.
//
// The refusal now happens where the mistake is, in records/schema.go's
// propertyDeclKeys, when the FILE loads. So there is nothing left for this
// package to refuse, and refuseSchemaDeclaredFormulas is deleted rather than
// kept as a second guard — an unreachable refusal is a branch that cannot be
// told apart from a working one by any test.
//
// What this file holds now is the SEAM, from the query side: that the loader
// really does keep such a schema out of the SchemaSet, and that the cost of it
// doing so is exactly one record type. Both are measured through Find, on the
// rendered text a model actually reads.
// ---------------------------------------------------------------------------

// The fixture vault declares one record type WITH a derived property and one
// without, in the same vault, so the blast radius is measured rather than
// asserted.
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

// formulaVaultRoot writes both schemas into a fresh vault and returns its root.
func formulaVaultRoot(t *testing.T) string {
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
	return root
}

// formulaVault loads the vault above through records.LoadSchemas — the
// production path, rejections and all — and indexes one note of each type.
//
// It deliberately does NOT fail on a non-OK report: the whole point is that
// `plant.yaml` IS rejected and `bed.yaml` is not, and a helper that insisted on
// a clean load could not express the state under test.
func formulaVault(t *testing.T) (Deps, *records.SchemaLoadReport) {
	t.Helper()

	root := formulaVaultRoot(t)
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
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

	return Deps{Schemas: set, Store: store, Text: text, Epoch: 4211}, report
}

// TestSchemaFormula_TheLoaderKeepsItOutOfTheSchemaSet is the seam, asserted
// from this side. If the loader ever starts ACCEPTING a schema-property formula
// again, this fails here — where the consequence is, rather than in the loader's
// own tests only.
func TestSchemaFormula_TheLoaderKeepsItOutOfTheSchemaSet(t *testing.T) {
	d, report := formulaVault(t)

	if report.OK() {
		t.Fatalf("the loader ACCEPTED a schema declaring a formula property. Nothing evaluates one, so " +
			"`double_height` would render blank on every row of an answer calling itself COMPLETE")
	}
	if got := report.RejectedTypes(); len(got) != 1 || got[0] != "plant" {
		t.Fatalf("exactly the offending record type must be rejected; RejectedTypes() = %v", got)
	}
	if _, ok := d.Schemas.Get("plant"); ok {
		t.Fatalf("the rejected record type is in the SchemaSet, so a query would still be answered from it")
	}
}

// TestSchemaFormula_TheOffendingTypeIsSimplyUNKNOWNToAQuery states plainly what
// an operator now sees when they query the type whose schema was rejected.
//
// It is NOT a formula-shaped refusal any more, and that is correct: from the
// query's point of view `plant` is a record type this vault does not declare
// (FR-005) — its notes are ordinary notes. The vault-level report is where the
// reason lives, and it names the file.
func TestSchemaFormula_TheOffendingTypeIsSimplyUNKNOWNToAQuery(t *testing.T) {
	d, _ := formulaVault(t)
	typ := "plant"
	sel := []string{"species", "height_cm", "double_height"}

	resp, err := Find(context.Background(), d, generated.VaultFindRequest{Type: &typ, Select: &sel})
	if err == nil {
		t.Fatalf("a query naming a record type the loader rejected was ANSWERED.\nRendered:\n%s", Render(resp))
	}
	rendered := Render(resp)

	// The one thing that must never happen: rows, with a blank derived column,
	// under a COMPLETE banner. Every other outcome is honest; this one is not.
	if len(resp.Rows) != 0 {
		t.Errorf("the refusal returned %d row(s)", len(resp.Rows))
	}
	if resp.Complete {
		t.Errorf("a query that could not be answered reports itself COMPLETE:\n%s", rendered)
	}
	if !strings.Contains(rendered, "plant") {
		t.Errorf("the refusal does not name the type that was asked for:\n%s", rendered)
	}
}

// TestSchemaFormula_OtherRecordTypesStillLoadAndStillAnswer is the blast-radius
// measurement: a second record type in the SAME vault, in a file next to the
// rejected one, loads and answers with its data intact.
func TestSchemaFormula_OtherRecordTypesStillLoadAndStillAnswer(t *testing.T) {
	d, _ := formulaVault(t)
	typ := "bed"
	sel := []string{"name", "slots"}

	resp, err := Find(context.Background(), d, generated.VaultFindRequest{Type: &typ, Select: &sel})
	if err != nil {
		t.Fatalf("a record type declaring no formula was refused because a NEIGHBOURING file declared "+
			"one: %v", err)
	}
	if !resp.Complete {
		t.Errorf("the unaffected type's answer is not complete:\n%s", Render(resp))
	}
	if len(resp.Rows) != 1 {
		t.Fatalf("expected the one indexed bed, got %d row(s):\n%s", len(resp.Rows), Render(resp))
	}
	if rendered := Render(resp); !strings.Contains(rendered, "East wall") || !strings.Contains(rendered, "6") {
		t.Errorf("the unaffected type's answer lost its data:\n%s", rendered)
	}
}

// TestSchemaFormula_AnUntypedQueryIsUnaffected.
//
// An untyped word query spans the whole vault. One rejected schema file must
// not take it down — that would turn a per-file fault into a vault-wide one,
// which is the cost the load-time refusal was chosen to avoid.
func TestSchemaFormula_AnUntypedQueryIsUnaffected(t *testing.T) {
	d, _ := formulaVault(t)
	words := "plant"
	if st, ok := d.Text.(*stubText); ok {
		st.only = []string{"garden/plant-1.md"}
	}

	if _, err := Find(context.Background(), d, generated.VaultFindRequest{Words: &words}); err != nil {
		t.Fatalf("an untyped query was refused because one schema file in the vault was rejected: %v", err)
	}
}
