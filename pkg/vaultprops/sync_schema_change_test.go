// Omnipus — CONFIRMED #1: a schema change must re-derive the notes it governs,
// even though not one byte of those notes moved.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// A row in the properties index is derived from TWO inputs: the note's bytes
// and the schema its `type:` names. Sync's freshness test covered only the
// first, so the second could change with nothing to notice it — schema files
// live under `.omnipus-vault/records/`, which the note walk does not visit, so
// creating or editing one moves no note's hash.
//
// The failure that produced is the worst shape this project has a name for: an
// operator with 412 notes already carrying `type: company` runs
// knowledge_configure create_record_type company, every one of those notes
// hash-matches on the next reconcile, and `knowledge_find type=company` returns
// ZERO rows while reporting complete — permanently, with no error and no
// staleness flag anywhere.
//
// The fix stores, on each note's row, the fingerprint of the schema that
// derived it and the type that note DECLARES (which is not the same as the
// record_type it resolved to — an undefined type resolves to none, FR-005).
// The skip test then compares both inputs. The tests below fix the BEHAVIOUR,
// not the mechanism: each one changes a schema, re-syncs, and asserts what the
// index answers afterwards.
// ---------------------------------------------------------------------------

const companyNote = "---\n" +
	"type: company\n" +
	"id: CO-0001\n" +
	"status: active\n" +
	"---\n" +
	"# Acme\n"

const companySchemaV1 = "schema_version: 1\n" +
	"type: company\n" +
	"properties:\n" +
	"  status: { type: text }\n"

const companySchemaV2 = "schema_version: 1\n" +
	"type: company\n" +
	"properties:\n" +
	"  status: { type: text }\n" +
	"  tier: { type: text }\n"

// countTypedCandidates asks the store how many records it holds of one record
// type — the same narrowing knowledge_find's `type=` argument produces.
func countTypedCandidates(t *testing.T, home, root, recordType string) int {
	t.Helper()
	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()
	n, err := store.CountCandidates(context.Background(), propindex.Selector{RecordType: recordType})
	if err != nil {
		t.Fatalf("CountCandidates(%q): %v", recordType, err)
	}
	return n
}

// writeSchema drops a schema file into the vault's record directory. It writes
// ONLY that file: no note is touched, so no note's content hash moves — which
// is the whole condition under test.
func writeSchema(t *testing.T, root, typeName, body string) {
	t.Helper()
	dir := filepath.Join(root, ".omnipus-vault", "records")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir records: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, typeName+".yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
}

// TestSync_CreatingARecordTypeReIndexesTheNotesThatAlreadyDeclareIt is
// CONFIRMED #1's headline case.
func TestSync_CreatingARecordTypeReIndexesTheNotesThatAlreadyDeclareIt(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		"Companies/Acme.md":                 companyNote,
		"Companies/Beta.md":                 companyNote,
		"Notes/ordinary.md":                 "# Nothing typed here\n",
		"Plants/Fern.md":                    fernNote,
		".omnipus-vault/records/plant.yaml": plantSchema,
	})
	ctx := context.Background()

	// First pass: `company` is not a declared type yet, so both company notes
	// are ordinary notes (FR-005) and the index holds no `company` record.
	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if n := countTypedCandidates(t, home, root, "company"); n != 0 {
		t.Fatalf("before the schema exists the index must hold no company record, got %d", n)
	}

	// The operator declares the type. NOT ONE NOTE CHANGES.
	writeSchema(t, root, "company", companySchemaV1)

	stats, err := Sync(ctx, home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if n := countTypedCandidates(t, home, root, "company"); n != 2 {
		t.Fatalf("after declaring the type, knowledge_find type=company must reach 2 records, got %d "+
			"(sync stats: %+v)", n, stats)
	}

	// And the re-derivation is TARGETED: only the two notes that declare
	// `company` were re-indexed. The plant, the ordinary note and nothing else
	// paid for a schema change that does not govern them.
	if stats.Indexed != 2 {
		t.Errorf("a schema change must re-index only the notes that declare that type: "+
			"expected 2 re-indexed, got %d (stats %+v)", stats.Indexed, stats)
	}
}

// TestSync_EditingARecordTypeReDerivesItsNotes is the `edit_record_type` half:
// the type already existed, so the notes already resolved to it, and the defect
// is that a CHANGED declaration is never applied.
func TestSync_EditingARecordTypeReDerivesItsNotes(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		"Companies/Acme.md": "---\ntype: company\nid: CO-0001\nstatus: active\ntier: gold\n---\n# Acme\n",
	})
	ctx := context.Background()
	writeSchema(t, root, "company", companySchemaV1)
	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	// `tier` is on disk but undeclared, so it is stored as a raw frontmatter
	// row with no declared type.
	if got := declaredPropertyType(t, home, root, "Companies/Acme.md", "tier"); got != "" {
		t.Fatalf("before the edit, `tier` must carry no declared type, got %q", got)
	}

	// The operator adds `tier` to the type. NOT ONE NOTE CHANGES.
	writeSchema(t, root, "company", companySchemaV2)

	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if got := declaredPropertyType(t, home, root, "Companies/Acme.md", "tier"); got != "text" {
		t.Fatalf("after declaring `tier` on the type, the note's row must carry it as a declared "+
			"text property, got %q", got)
	}
}

// TestSync_DeletingARecordTypeReDerivesItsNotes closes the third direction: a
// type that stops existing must stop being answered for.
func TestSync_DeletingARecordTypeReDerivesItsNotes(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{"Companies/Acme.md": companyNote})
	ctx := context.Background()
	writeSchema(t, root, "company", companySchemaV1)
	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if n := countTypedCandidates(t, home, root, "company"); n != 1 {
		t.Fatalf("setup: expected 1 company record, got %d", n)
	}

	if err := os.Remove(filepath.Join(root, ".omnipus-vault", "records", "company.yaml")); err != nil {
		t.Fatalf("remove schema: %v", err)
	}
	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if n := countTypedCandidates(t, home, root, "company"); n != 0 {
		t.Fatalf("after the type is deleted the index must hold no company record, got %d", n)
	}
}

// TestSync_AnUnchangedSchemaCostsNoReIndex is the other half of the bound, and
// it is what keeps the fix from degenerating into "re-index everything
// whenever a schema exists". Two consecutive syncs with nothing touched at all
// must re-index nothing.
func TestSync_AnUnchangedSchemaCostsNoReIndex(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		"Companies/Acme.md":                 companyNote,
		"Plants/Fern.md":                    fernNote,
		".omnipus-vault/records/plant.yaml": plantSchema,
	})
	ctx := context.Background()
	writeSchema(t, root, "company", companySchemaV1)
	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	stats, err := Sync(ctx, home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if stats.Indexed != 0 {
		t.Fatalf("a reconcile with nothing changed must re-index nothing, got %d (stats %+v)",
			stats.Indexed, stats)
	}
	if stats.Unchanged != 2 {
		t.Fatalf("expected both notes to be skipped, got %+v", stats)
	}
}

// declaredPropertyType reads back the DECLARED type the index recorded for one
// property of one note. It is "" for a raw frontmatter row (a key the schema
// does not declare), and the property type name for a declared one.
func declaredPropertyType(t *testing.T, home, root, notePath, prop string) string {
	t.Helper()
	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	var got string
	err := store.Candidates(context.Background(), propindex.Selector{PathPrefix: notePath},
		func(c propindex.Candidate) (propindex.Verdict, error) {
			if c.Path != notePath {
				return propindex.Rejected, nil
			}
			if p, ok := c.Prop(prop); ok {
				got = string(p.Type)
			}
			return propindex.Rejected, nil
		})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	return got
}
