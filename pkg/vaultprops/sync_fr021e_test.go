package vaultprops

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// TestSync_SchemalessNoteReachesTheIndexWithItsFrontmatter is founder ruling 1
// (FR-021e) checked from the SYNC side: every note's frontmatter is indexed,
// typed or not.
//
// Sync's own half is that it parses and forwards unconditionally —
// records.ParseRecord reads frontmatter whether or not a type is declared, and
// sync hands the whole Record plus a nil schema to BuildNoteRows for a note
// whose type matches nothing. This test is what keeps that true, so that when
// the storage side stores raw rows the value is actually THERE rather than
// absent for a second reason nobody looked for.
//
// The behaviour the founder named: a note whose file says `status: open` must
// never answer TRUE to `status IS NULL`.
func TestSync_SchemalessNoteReachesTheIndexWithItsFrontmatter(t *testing.T) {
	skipWithoutSQLite(t)
	home := syncHome(t)
	root := syncVault(t, map[string]string{
		// No schema directory at all: nothing in this vault is a typed record.
		"Notes/Untyped.md": "---\nstatus: open\nowner: mia\n---\n# Untyped\n",
	})
	if _, err := Sync(context.Background(), home, root, SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	var got *propindex.Candidate
	if err := store.Candidates(context.Background(), propindex.Selector{}, func(c propindex.Candidate) (propindex.Verdict, error) {
		if c.Path == "Notes/Untyped.md" {
			cc := c
			got = &cc
		}
		return propindex.Rejected, nil
	}); err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if got == nil {
		t.Fatal("the untyped note is not in the index at all")
	}
	if got.RecordType != "" {
		t.Errorf("an untyped note must declare no record type, got %q", got.RecordType)
	}
	prop, ok := got.Prop("status")
	if !ok {
		t.Fatalf("the untyped note's own frontmatter key is absent from the index — `status IS NULL` would answer TRUE for a note whose file says `status: open` (FR-021e); index holds %v", got.PropOrder)
	}
	if len(prop.Elems) == 0 || prop.Elems[0].Text != "open" {
		t.Errorf("the raw value was not stored: %+v", prop)
	}
	if _, ok := got.Prop("owner"); !ok {
		t.Errorf("only one of the two frontmatter keys was stored: %v", got.PropOrder)
	}
}
