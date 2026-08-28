// Omnipus — shared fixtures for the properties-index tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// The fixture vocabulary is deliberately NOT the CRM one. ADR-068 D0 says the
// product ships mechanism and the vault ships convention, and a test corpus that
// talks about companies, deals and pipeline stages teaches the opposite by
// example — which is why the specification's own W0 wave is a vocabulary
// replacement. A greenhouse has every shape the tests need and implies no
// built-in record type.
const plantSchemaYAML = `
schema_version: 1
type: plant
label: Plant
identity:
  prefix: PL
properties:
  species:   { type: text, required: true }
  condition: { type: enum, values: [seedling, growing, dormant] }
  planted:   { type: date }
  height_cm: { type: decimal }
  cuttings:  { type: integer }
  bed:       { type: relation, to: bed }
  keeper:    { type: person }
  labels:    { type: text, many: true }
`

func plantSchema(t *testing.T) *records.Schema {
	t.Helper()
	sc, rej := records.ParseSchema("plant.yaml", []byte(plantSchemaYAML))
	if rej != nil {
		t.Fatalf("the fixture schema does not parse: %s", rej.String())
	}
	return sc
}

// openIndex opens a store in a fresh directory and closes it when the test ends.
func openIndex(t *testing.T, opts Options) (Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "properties.db")
	store, err := Open(context.Background(), path, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store, path
}

// note builds the rows for one note from its literal source, through the same
// parser the validator uses. Nothing in these tests hand-assembles a PropRow:
// a fixture that bypasses BuildNoteRows would test the storage of values the
// product never produces.
func note(t *testing.T, path string, sc *records.Schema, src string) NoteRows {
	t.Helper()
	b := []byte(src)
	rec := records.ParseRecord(path, b)
	return BuildNoteRows(rec, sc, b, SourceHash(b))
}

// plantNote writes a well-formed record of the fixture type.
func plantNote(t *testing.T, n int, condition string) NoteRows {
	t.Helper()
	src := fmt.Sprintf(`---
type: plant
id: PL-%04d
species: Monstera deliciosa
condition: %s
planted: 2026-03-1%d
height_cm: 41.25
cuttings: 3
bed: "[[Bed %d]]"
keeper: "[[Rosa]]"
labels: [indoor, humid]
---

# Plant %d

- [ ] repot in spring
- [x] moved to the east window
`, n, condition, n%10, n%4, n)
	return note(t, fmt.Sprintf("garden/plant-%04d.md", n), plantSchema(t), src)
}

func mustUpsert(t *testing.T, store Store, rows ...NoteRows) {
	t.Helper()
	for _, r := range rows {
		if err := store.UpsertNote(context.Background(), r); err != nil {
			t.Fatalf("UpsertNote(%s): %v", r.Path, err)
		}
	}
}

// collect drains a candidate stream, accepting everything.
func collect(t *testing.T, store Store, sel Selector) []Candidate {
	t.Helper()
	var out []Candidate
	err := store.Candidates(context.Background(), sel, func(c Candidate) (Verdict, error) {
		out = append(out, c)
		return Accepted, nil
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	return out
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
