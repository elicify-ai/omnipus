// Omnipus — tests for the production records.RelationResolver (D5.1, FR-031).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// openResolverFixture builds a real properties index over a small corpus and
// a NoteIndex over the same paths, then wraps both in a RelationResolver —
// the two steps D5.1 names, wired for real rather than faked.
func openResolverFixture(t *testing.T) (*RelationResolver, propindex.Store) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build")
	}
	ctx := context.Background()
	store, err := propindex.Open(ctx, filepath.Join(t.TempDir(), "properties.db"), propindex.Options{})
	if err != nil {
		t.Fatalf("propindex.Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("closing the store: %v", cerr)
		}
	})

	rows := []propindex.NoteRows{
		{
			Path: "Deals/Big One.md", Kind: propindex.KindNote,
			RecordType: "deal", RecordID: "DE-0001", SourceHash: "aaaa",
			Relations: []propindex.RelationRow{
				{Prop: "company", Elem: 0, Target: "Acme Ltd", Raw: "[[Acme Ltd]]"},
			},
		},
		{
			Path: "Companies/Acme Ltd.md", Kind: propindex.KindNote,
			RecordType: "company", RecordID: "CO-0001", SourceHash: "bbbb",
		},
		{
			// A note that exists, but declares no record type — a real file
			// at the end of a real wikilink with nothing for R-8 to compare.
			Path: "Notes/loose.md", Kind: propindex.KindNote, SourceHash: "cccc",
		},
	}
	for _, r := range rows {
		if err := store.UpsertNote(ctx, r); err != nil {
			t.Fatalf("UpsertNote(%s): %v", r.Path, err)
		}
	}

	paths := make([]string, 0, len(rows))
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	notes := knowledge.NewNoteIndex(paths)
	return NewRelationResolver(ctx, notes, store), store
}

// TestRelationResolver_ForwardResolution is D5.1 itself: wikilink -> file ->
// record id.
func TestRelationResolver_ForwardResolution(t *testing.T) {
	r, _ := openResolverFixture(t)

	id, ok := r.Resolve(records.Wikilink{Target: "Acme Ltd", Raw: "[[Acme Ltd]]"})
	if !ok {
		t.Fatalf("Resolve([[Acme Ltd]]) did not resolve")
	}
	if id != "CO-0001" {
		t.Errorf("Resolve([[Acme Ltd]]) = %q, want CO-0001", id)
	}
}

// TestRelationResolver_AsFuncMatchesResolve pins that the two entry points —
// the closure compare_oracle.go's Comparator was built against, and the
// direct method — never disagree, since a caller could reasonably reach for
// either.
func TestRelationResolver_AsFuncMatchesResolve(t *testing.T) {
	r, _ := openResolverFixture(t)
	link := records.Wikilink{Target: "Acme Ltd", Raw: "[[Acme Ltd]]"}

	wantID, wantOK := r.Resolve(link)
	fn := r.AsFunc()
	gotID, gotOK := fn(link)
	if gotID != wantID || gotOK != wantOK {
		t.Fatalf("AsFunc()(link) = (%q, %v), Resolve(link) = (%q, %v); the two seams disagreed",
			gotID, gotOK, wantID, wantOK)
	}
}

// TestRelationResolver_IdentityIsWhatCompares proves R-8: a DIFFERENT
// wikilink spelling of the SAME target resolves to the SAME record identity
// — the property a rename or an alias must never break (D5.1: "a rename is
// safe from both directions"; D7: two spellings are one record).
func TestRelationResolver_IdentityIsWhatCompares(t *testing.T) {
	r, _ := openResolverFixture(t)

	byTarget, ok := r.Resolve(records.Wikilink{Target: "Acme Ltd", Raw: "[[Acme Ltd]]"})
	if !ok {
		t.Fatalf("Resolve by target did not resolve")
	}
	// [[Acme Ltd|Acme]] — an ALIAS, same target, different display text. R-8
	// is explicit that Display is never identity.
	byAlias, ok := r.Resolve(records.Wikilink{Target: "Acme Ltd", Display: "Acme", Raw: "[[Acme Ltd|Acme]]"})
	if !ok {
		t.Fatalf("Resolve by aliased link did not resolve")
	}
	if byTarget != byAlias {
		t.Fatalf("an alias of the SAME target resolved to a different identity: %q vs %q — "+
			"R-8 compares by identity, never by display text", byTarget, byAlias)
	}
}

// TestRelationResolver_UnresolvedWikilinkReportsMissing is FR-033's first
// half at the resolver's own seam: nothing in the collection carries this
// name, so there is no file, let alone an identity.
func TestRelationResolver_UnresolvedWikilinkReportsMissing(t *testing.T) {
	r, _ := openResolverFixture(t)

	if id, ok := r.Resolve(records.Wikilink{Target: "Nonexistent Co", Raw: "[[Nonexistent Co]]"}); ok {
		t.Fatalf("Resolve([[Nonexistent Co]]) = (%q, true), want ok=false — nothing names this note", id)
	}
	identity, ok := r.ResolveIdentity(records.Wikilink{Target: "Nonexistent Co", Raw: "[[Nonexistent Co]]"})
	if ok {
		t.Fatalf("ResolveIdentity([[Nonexistent Co]]) = (%+v, true), want ok=false", identity)
	}
}

// TestRelationResolver_ResolvedButNotARecord is the distinction ADR-068 O-5
// draws and pkg/knowledge/integrity.go already renders: a wikilink can
// resolve to a REAL FILE that is not a record of any declared type. R-8 has
// no identity to compare (Resolve/AsFunc report unresolved, matching
// CompareRelationUnresolved), but a caller with schema context — checking
// FR-034's "resolves, but to the wrong type" — needs to see that the file
// DID resolve, which ResolveIdentity alone still reports.
func TestRelationResolver_ResolvedButNotARecord(t *testing.T) {
	r, _ := openResolverFixture(t)
	link := records.Wikilink{Target: "loose", Raw: "[[loose]]"}

	if id, ok := r.Resolve(link); ok {
		t.Fatalf("Resolve([[loose]]) = (%q, true), want ok=false — the file has no record identity", id)
	}
	identity, ok := r.ResolveIdentity(link)
	if !ok {
		t.Fatalf("ResolveIdentity([[loose]]) did not resolve the FILE at all, but Notes/loose.md exists")
	}
	if identity.HasIdentity() {
		t.Fatalf("HasIdentity() = true for a note declaring no record type: %+v", identity)
	}
	if identity.Path != "Notes/loose.md" {
		t.Errorf("Path = %q, want Notes/loose.md", identity.Path)
	}
}

// TestRelationResolver_NilIsSafe — a resolver missing either dependency
// answers "not resolved" rather than panicking (ADR-068 O-5's posture
// applied to the resolver's own missing wiring, not only to a missing
// target).
func TestRelationResolver_NilIsSafe(t *testing.T) {
	var nilResolver *RelationResolver
	if id, ok := nilResolver.Resolve(records.Wikilink{Target: "x"}); ok {
		t.Fatalf("nil *RelationResolver.Resolve = (%q, true), want ok=false", id)
	}
	if fn := nilResolver.AsFunc(); fn != nil {
		t.Fatalf("nil *RelationResolver.AsFunc() returned a non-nil func")
	}

	noStore := NewRelationResolver(context.Background(), knowledge.NewNoteIndex(nil), nil)
	if id, ok := noStore.Resolve(records.Wikilink{Target: "x"}); ok {
		t.Fatalf("Resolve with no store = (%q, true), want ok=false", id)
	}

	noNotes := NewRelationResolver(context.Background(), nil, nil)
	if id, ok := noNotes.Resolve(records.Wikilink{Target: "x"}); ok {
		t.Fatalf("Resolve with no NoteIndex = (%q, true), want ok=false", id)
	}
}

// countingStore wraps a real propindex.Store and counts Candidates calls, so
// the memo can be proven to actually memoise rather than merely returning the
// right answer by chance.
type countingStore struct {
	propindex.Store
	calls int
}

func (c *countingStore) Candidates(ctx context.Context, sel propindex.Selector, visit func(propindex.Candidate) (propindex.Verdict, error)) error {
	c.calls++
	return c.Store.Candidates(ctx, sel, visit)
}

// TestRelationResolver_MemoisesWithinOneResolver proves the memo: resolving
// the SAME target twice through the SAME resolver instance issues exactly
// ONE store lookup, not two. A relation is commonly many-valued and a corpus
// commonly repeats one target across many source records (every deal
// pointing at one company) — resolving each occurrence independently would
// turn one company into thousands of redundant point lookups when grouping
// or joining.
func TestRelationResolver_MemoisesWithinOneResolver(t *testing.T) {
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build")
	}
	ctx := context.Background()
	real, err := propindex.Open(ctx, filepath.Join(t.TempDir(), "properties.db"), propindex.Options{})
	if err != nil {
		t.Fatalf("propindex.Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := real.Close(); cerr != nil {
			t.Errorf("closing the store: %v", cerr)
		}
	})
	if err := real.UpsertNote(ctx, propindex.NoteRows{
		Path: "Companies/Acme Ltd.md", Kind: propindex.KindNote,
		RecordType: "company", RecordID: "CO-0001", SourceHash: "bbbb",
	}); err != nil {
		t.Fatalf("UpsertNote: %v", err)
	}
	counting := &countingStore{Store: real}
	notes := knowledge.NewNoteIndex([]string{"Companies/Acme Ltd.md"})
	r := NewRelationResolver(ctx, notes, counting)

	link := records.Wikilink{Target: "Acme Ltd", Raw: "[[Acme Ltd]]"}
	for i := 0; i < 5; i++ {
		id, ok := r.Resolve(link)
		if !ok || id != "CO-0001" {
			t.Fatalf("call %d: Resolve = (%q, %v), want (CO-0001, true)", i, id, ok)
		}
	}
	if counting.calls != 1 {
		t.Errorf("Candidates was called %d times resolving the same target 5 times, want 1 — the memo is not memoising", counting.calls)
	}
}

// TestRelationResolver_StoreErrorIsReportedAsUnresolved is ADR-068 O-5
// applied to an internal failure, not only to a genuinely absent target: we
// deliberately do not distinguish the causes at the boolean seam
// records.RelationResolver exposes, but a store failure must not panic or
// hang the query — it degrades to "not resolved", same as a target that was
// never there.
func TestRelationResolver_StoreErrorIsReportedAsUnresolved(t *testing.T) {
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build")
	}
	ctx := context.Background()
	notes := knowledge.NewNoteIndex([]string{"Companies/Acme Ltd.md"})
	r := NewRelationResolver(ctx, notes, &failingStore{})

	if id, ok := r.Resolve(records.Wikilink{Target: "Acme Ltd", Raw: "[[Acme Ltd]]"}); ok {
		t.Fatalf("Resolve over a failing store = (%q, true), want ok=false", id)
	}
}

type failingStore struct{ propindex.Store }

func (*failingStore) Candidates(context.Context, propindex.Selector, func(propindex.Candidate) (propindex.Verdict, error)) error {
	return errors.New("simulated store failure")
}

// TestRelationResolver_ExactPathNotJustPrefix proves identityAt does not
// trust the LIKE prefix alone: a second indexed path that merely STARTS WITH
// the resolved path must never be mistaken for it. Point-lookup-by-prefix is
// an implementation choice (propindex exposes no point lookup by exact
// path); this pins the exactness the choice depends on.
func TestRelationResolver_ExactPathNotJustPrefix(t *testing.T) {
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build")
	}
	ctx := context.Background()
	store, err := propindex.Open(ctx, filepath.Join(t.TempDir(), "properties.db"), propindex.Options{})
	if err != nil {
		t.Fatalf("propindex.Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("closing the store: %v", cerr)
		}
	})
	rows := []propindex.NoteRows{
		{Path: "Companies/Acme.md", Kind: propindex.KindNote, RecordType: "company", RecordID: "CO-0001", SourceHash: "aaaa"},
		// Shares "Companies/Acme.md" as a literal string PREFIX. Not a real
		// vault shape (a real collection cannot nest a file under a file),
		// but the store has no field enforcing that, and the resolver's
		// correctness must not rest on the store enforcing it either.
		{Path: "Companies/Acme.md.bak", Kind: propindex.KindNote, RecordType: "company", RecordID: "CO-9999", SourceHash: "bbbb"},
	}
	for _, r := range rows {
		if err := store.UpsertNote(ctx, r); err != nil {
			t.Fatalf("UpsertNote(%s): %v", r.Path, err)
		}
	}
	notes := knowledge.NewNoteIndex([]string{"Companies/Acme.md", "Companies/Acme.md.bak"})
	r := NewRelationResolver(ctx, notes, store)

	id, ok := r.Resolve(records.Wikilink{Target: "Acme.md", Raw: "[[Acme.md]]"})
	if !ok {
		t.Fatalf("Resolve([[Acme.md]]) did not resolve")
	}
	if id != "CO-0001" {
		t.Fatalf("Resolve([[Acme.md]]) = %q, want CO-0001 (the EXACT match) — "+
			"a same-prefixed sibling must never be substituted for it", id)
	}
}
