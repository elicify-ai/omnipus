// Omnipus — UAT 2026-09-13 D-61: a negative date difference says what it is
// in the find cell, not only on records.FormulaResult.
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
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// noteVault is one record type, two notes and one saved view with two
// formulas over the same date: one at FR-144's undeclared scale and one whose
// author DECLARED a scale with toFixed.
func noteVault(t *testing.T) Deps {
	t.Helper()
	root := t.TempDir()

	schemaDir := records.SchemaDir(root)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(schemas): %v", err)
	}
	if err := os.WriteFile(filepath.Join(schemaDir, "deadline.yaml"), []byte(`schema_version: 1
type: deadline
properties:
  title: { type: text }
  due:   { type: date }
`), 0o600); err != nil {
		t.Fatalf("WriteFile(schema): %v", err)
	}
	set, schemaReport, schemaErr := records.LoadSchemas(root)
	if schemaErr != nil {
		t.Fatalf("LoadSchemas: %v", schemaErr)
	}
	if !schemaReport.OK() {
		t.Fatalf("the fixture schema was rejected: %v", schemaReport.Rejections)
	}

	viewDir := records.ViewsDir(root)
	if err := os.MkdirAll(viewDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(views): %v", err)
	}
	if err := os.WriteFile(filepath.Join(viewDir, "countdown.yaml"), []byte(`name: countdown
type: deadline
formulas:
  days_left: (date(due) - today()).days
  days_left_fixed: toFixed((date(due) - today()).days, 2)
properties:
  - title
  - formula.days_left
  - formula.days_left_fixed
`), 0o600); err != nil {
		t.Fatalf("WriteFile(view): %v", err)
	}
	views, viewReport, err := records.LoadViews(root, set)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !viewReport.OK() {
		t.Fatalf("the fixture view was rejected by the loader: %v", viewReport.Rejections)
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
	for path, due := range map[string]string{
		"work/past.md":   "2026-08-20", // 11 days BEFORE the query clock
		"work/future.md": "2026-09-02", // 2 days AFTER it
	} {
		b := []byte("---\ntype: deadline\ntitle: " + filepath.Base(path) + "\ndue: " + due + "\n---\n\nbody\n")
		rec := records.ParseRecord(path, b)
		schema, _ := set.Get(rec.TypeName())
		rows := propindex.BuildNoteRows(rec, schema, b, propindex.SourceHash(b))
		rows.Size = int64(len(b))
		rows.MtimeNanos = 1
		if err := store.UpsertNote(context.Background(), rows); err != nil {
			t.Fatalf("UpsertNote(%s): %v", path, err)
		}
		text.hits[path] = TextHit{Path: path, SourceHash: rows.SourceHash, Score: 1}
	}
	return Deps{
		Schemas: set, Store: store, Text: text,
		Views: records.NewViewFindLoader(views), Epoch: 61,
		Now: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
	}
}

// negativeDateDifferenceNote is the evaluator's explanation, written out here
// from pkg/records/formula_eval.go's D-61 text rather than read back from the
// code under test, so a silent change to either end fails this file.
const negativeDateDifferenceNote = "negative because the first date is later than the second — this span counts DOWN from the first date to the second, so read it as a countdown, not an elapsed count"

func noteCells(t *testing.T, resp generated.VaultFindResponse) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{}
	for _, row := range resp.Rows {
		cells := map[string]string{}
		for _, c := range row.Cells {
			cells[c.Property] = c.Value
		}
		out[row.Path] = cells
	}
	return out
}

// TestFindCell_NegativeDateDifferenceCarriesItsNote is D-61's missing
// consumer. The formula engine explained a negative date difference on
// records.FormulaResult.Note, and knowledge_find dropped it: a past-due
// deadline showed a bare "-11", indistinguishable from an elapsed count. The
// note now rides in the cell's own Value, which is the one string the compact
// row text, the VaultFindCell wire value and the base preview all show.
func TestFindCell_NegativeDateDifferenceCarriesItsNote(t *testing.T) {
	deps := noteVault(t)
	view := "countdown"
	resp := mustFind(t, deps, generated.VaultFindRequest{View: &view})
	cells := noteCells(t, resp)

	past, ok := cells["work/past.md"]
	if !ok {
		t.Fatalf("work/past.md missing from the rows; rows = %v", rowIDs(resp))
	}
	want := "-11" + formulaNoteSeparator + negativeDateDifferenceNote
	if got := past["formula.days_left"]; got != want {
		t.Errorf("formula.days_left for a past-due note = %q\nwant %q", got, want)
	}
	// The separator is pinned as literal text: a reader, and every consumer
	// that reads the number back out of the cell, sees exactly this.
	if formulaNoteSeparator != ", note: " {
		t.Errorf("formulaNoteSeparator = %q, want %q", formulaNoteSeparator, ", note: ")
	}

	// The compact text an agent reads carries it too — it is built from the
	// same cell, not from a second rendering.
	if out := Render(resp); !strings.Contains(out, "formula.days_left "+want) {
		t.Errorf("the compact row text does not carry the note beside the value:\n%s", out)
	}
}

// TestFindCell_PositiveDateDifferenceHasNoNote — the note is about the SIGN.
// An ordinary elapsed or remaining count is shown bare.
func TestFindCell_PositiveDateDifferenceHasNoNote(t *testing.T) {
	deps := noteVault(t)
	view := "countdown"
	cells := noteCells(t, mustFind(t, deps, generated.VaultFindRequest{View: &view}))

	future, ok := cells["work/future.md"]
	if !ok {
		t.Fatal("work/future.md missing from the rows")
	}
	if got := future["formula.days_left"]; got != "2" {
		t.Errorf("formula.days_left for a note two days out = %q, want %q (no note on a positive difference)", got, "2")
	}
	if got := future["formula.days_left_fixed"]; got != "2.00" {
		t.Errorf("formula.days_left_fixed = %q, want %q", got, "2.00")
	}
}

// TestFindCell_DeclaredScaleIsUnaffectedByTheNote — a toFixed formula keeps
// the digits its author declared. The note, where the evaluator gives one,
// is appended AFTER them and never alters them.
func TestFindCell_DeclaredScaleIsUnaffectedByTheNote(t *testing.T) {
	deps := noteVault(t)
	view := "countdown"
	cells := noteCells(t, mustFind(t, deps, generated.VaultFindRequest{View: &view}))

	got := cells["work/past.md"]["formula.days_left_fixed"]
	number, _, _ := strings.Cut(got, formulaNoteSeparator)
	if number != "-11.00" {
		t.Errorf("formula.days_left_fixed = %q; the declared-scale number must be exactly %q", got, "-11.00")
	}
}
