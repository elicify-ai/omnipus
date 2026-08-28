// Omnipus — FR-020a: the index is DERIVED and DISPOSABLE. W1's exit criterion.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"os"
	"reflect"
	"testing"
)

// snapshot renders everything the index will answer with, in a form two runs can
// be compared by.
//
// It deliberately reads through the PUBLIC read paths rather than the tables: a
// rebuild that produced identical rows but a different answer would be a
// rebuild that failed, and comparing tables would not notice.
func snapshot(t *testing.T, store Store) []any {
	t.Helper()
	ctx := context.Background()
	var out []any

	for _, sel := range []Selector{
		{},
		{RecordType: "plant"},
		{Kind: KindNote},
		{RecordType: "plant", Kind: KindNote, PathPrefix: "garden/"},
		{PathPrefix: "garden/plant-000"},
	} {
		n, err := store.CountCandidates(ctx, sel)
		if err != nil {
			t.Fatalf("CountCandidates(%+v): %v", sel, err)
		}
		out = append(out, sel, n)

		err = store.Candidates(ctx, sel, func(c Candidate) (Verdict, error) {
			// The map is rendered through PropOrder so the snapshot has a
			// deterministic shape; map iteration order would make two identical
			// indexes compare unequal at random.
			row := []any{c.Path, c.RecordType, c.RecordID, c.SourceHash}
			for _, name := range c.PropOrder {
				row = append(row, name, c.Props[name])
			}
			out = append(out, row)
			return Accepted, nil
		})
		if err != nil {
			t.Fatalf("Candidates(%+v): %v", sel, err)
		}

		if err := store.Tasks(ctx, sel, func(h TaskHit) error {
			out = append(out, h)
			return nil
		}); err != nil {
			t.Fatalf("Tasks(%+v): %v", sel, err)
		}
		if err := store.Relations(ctx, sel, func(h RelationHit) error {
			out = append(out, h)
			return nil
		}); err != nil {
			t.Fatalf("Relations(%+v): %v", sel, err)
		}
	}
	return out
}

// TestRebuild_DeleteTheFileAndReopenYieldsIdenticalResults is W1's exit
// criterion for FR-020a.
//
// The property being asserted is that the SQLite file holds nothing that is not
// reconstructible from Markdown. Notes are the sole source of truth (D8, D9), so
// deleting the index must cost a capability for as long as it takes to rebuild
// and cost no DATA at all — which is also what makes ADR-068 D16.2a survivable:
// a vault carried to a platform without SQLite loses a feature, never a fact.
//
// AC-16.5 is explicit that this criterion tests DISPOSABILITY and NOT the
// divergence mechanism, and that revision 5's version of W1's exit criterion —
// which tested only this — would have passed with the freshness mitigation
// entirely absent. Divergence is asserted separately, in ordering_test.go.
func TestRebuild_DeleteTheFileAndReopenYieldsIdenticalResults(t *testing.T) {
	ctx := context.Background()
	sc := plantSchema(t)

	// One corpus, built once, replayed identically into both indexes.
	corpus := []NoteRows{
		plantNote(t, 1, "seedling"),
		plantNote(t, 2, "growing"),
		plantNote(t, 3, "dormant"),
		note(t, "garden/notes/ordinary.md", nil, "# Ordinary\n\n- [ ] sweep the path\n"),
		note(t, "garden/plant-bad.md", sc, "---\ntype: plant\nid: PL-9999\nspecies: Yucca\ncondition: enormous\nlabels: not-a-list\n---\n"),
	}

	store, path := openIndex(t, Options{})
	if !store.NeedsFullIndex() {
		t.Error("a brand-new index must report that it needs a full index; otherwise nothing ever fills it")
	}
	if err := store.(*Index).UpsertNotes(ctx, corpus); err != nil {
		t.Fatalf("UpsertNotes: %v", err)
	}
	if store.NeedsFullIndex() {
		t.Error("an index holding notes still claims it needs a full index")
	}
	before := snapshot(t, store)
	if len(before) == 0 {
		t.Fatal("the snapshot is empty; this test would pass over an index that answers nothing")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Delete it. This is the operation an operator is invited to perform.
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the index file: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the index file survived its own deletion: %v", err)
	}

	rebuilt, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("reopening after deletion: %v", err)
	}
	defer func() {
		if err := rebuilt.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if !rebuilt.NeedsFullIndex() {
		t.Fatal("FR-020a: after the file was deleted the index must ask to be rebuilt, " +
			"not report itself populated — an index that thinks it is complete over zero notes " +
			"answers every query with a confident nothing")
	}
	if err := rebuilt.(*Index).UpsertNotes(ctx, corpus); err != nil {
		t.Fatalf("re-deriving from the notes: %v", err)
	}

	after := snapshot(t, rebuilt)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("FR-020a: the rebuilt index answers differently.\n  before: %#v\n  after:  %#v", before, after)
	}
}

// TestRebuild_AnIncompatibleSchemaVersionIsDiscardedNotMigrated.
//
// A derived store has no reason to own a migration path: re-deriving is cheaper
// than translating and cannot be subtly wrong. What it MUST NOT do is open an
// index written by a different shape and answer queries out of it — that is the
// silent-no-op failure the text index was found to have (spike S-2: same code,
// same document, same query, 1 hit on a fresh index and 0 hits with err=nil on
// an existing one).
func TestRebuild_AnIncompatibleSchemaVersionIsDiscardedNotMigrated(t *testing.T) {
	ctx := context.Background()
	store, path := openIndex(t, Options{})
	mustUpsert(t, store, plantNote(t, 1, "growing"))
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Stamp a version this build does not understand, the way a downgrade would.
	stamp, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	if _, err := stamp.(*Index).exec(ctx, PhaseOpen, "PRAGMA user_version = 999"); err != nil {
		t.Fatalf("stamping a foreign version: %v", err)
	}
	if err := stamp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("reopening a foreign-version index must not be an error, it must rebuild: %v", err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	if !reopened.NeedsFullIndex() {
		t.Error("an index written by an incompatible version was opened and treated as populated")
	}
	if got := collect(t, reopened, Selector{}); len(got) != 0 {
		t.Errorf("rows from an incompatible index survived: %#v", got)
	}
}
