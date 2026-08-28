// Omnipus — ADR-068 D16.5 / FR-020c: the write ordering and the divergence check.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestIndexNote_WritesSQLiteFirst is D16.5's ordering, asserted as an ORDER and
// not as a comment.
//
// Revision 6 specified bleve first and claimed both directions of failure were
// caught. They were not: a crash after bleve and before SQLite leaves the SQLite
// row and the manifest BOTH at the old hash, so they compare EQUAL and a stale
// answer is reported COMPLETE. That is the reachable case and it was the
// undetected one.
func TestIndexNote_WritesSQLiteFirst(t *testing.T) {
	store, _ := openIndex(t, Options{})
	var order []string

	rows := plantNote(t, 1, "growing")
	err := IndexNote(context.Background(), recordingStore{store, &order}, rows,
		func(context.Context) error { order = append(order, "bleve"); return nil },
		func(context.Context) error { order = append(order, "manifest"); return nil },
	)
	if err != nil {
		t.Fatalf("IndexNote: %v", err)
	}
	want := []string{"sqlite", "bleve", "manifest"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("D16.5: the write order was %v, want %v.\n"+
			"bleve-first makes the reachable failure undetectable: both the SQLite row and the "+
			"manifest stay at the old hash, they compare EQUAL, and a stale record is reported complete.",
			order, want)
	}
}

// TestIndexNote_AFailedSQLiteWriteStopsTheOtherTwo.
//
// If the first writer fails, the later writers must NOT run. Under this
// ordering, a SQLite failure with bleve proceeding anyway still DIVERGES and is
// still detected — but only because the two sides then differ. Stopping is
// cheaper and it keeps the false-positive rate down.
func TestIndexNote_AFailedSQLiteWriteStopsTheOtherTwo(t *testing.T) {
	boom := errors.New("the disk is full")
	var order []string
	err := IndexNote(context.Background(), failingStore{err: boom}, plantNote(t, 1, "growing"),
		func(context.Context) error { order = append(order, "bleve"); return nil },
		func(context.Context) error { order = append(order, "manifest"); return nil },
	)
	if !errors.Is(err, boom) {
		t.Fatalf("the store's error was lost: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("the later writers ran after the first failed: %v", order)
	}
	if !strings.Contains(err.Error(), "garden/plant-0001.md") {
		t.Errorf("the failure must name the note it was writing: %q", err)
	}
}

// TestIndexNote_AFailedTextWriteLeavesTheRowWrittenAndDetectable.
//
// This is D16.5's row "after SQLite, before bleve": the properties row is new,
// the text index is old, they DIFFER, and the divergence check flags it. The
// false positive — a record flagged possibly-stale while SQLite is actually
// AHEAD — is the direction to err in. A caveat on a correct answer is survivable;
// a record reported fresh while it is stale is the failure this design removes.
func TestIndexNote_AFailedTextWriteLeavesTheRowWrittenAndDetectable(t *testing.T) {
	store, _ := openIndex(t, Options{})
	rows := plantNote(t, 1, "growing")
	boom := errors.New("the text index rejected the document")

	err := IndexNote(context.Background(), store, rows,
		func(context.Context) error { return boom },
		func(context.Context) error {
			t.Error("the manifest was written after the text index failed")
			return nil
		},
	)
	if !errors.Is(err, boom) {
		t.Fatalf("the text writer's error was lost: %v", err)
	}

	got := collect(t, store, Selector{})
	if len(got) != 1 {
		t.Fatalf("the properties row must survive a later failure, got %d rows", len(got))
	}
	// The text index still holds the OLD bytes. That is what the check sees.
	staleTextHash := SourceHash([]byte("the previous version of this note"))
	if f := CompareFreshness(got[0].SourceHash, staleTextHash); f != FreshnessDisagree {
		t.Errorf("the divergence between the two indexes was not detected: %v", f)
	}
}

// TestFreshness_TheReasonSaysDisagree.
//
// The wording is normative and the reason is precision, not politeness: the
// comparison establishes DISAGREEMENT, not which side is behind. "The properties
// index is stale" claims a direction the mechanism cannot know, and it is wrong
// in exactly the case above, where SQLite is ahead.
func TestFreshness_TheReasonSaysDisagree(t *testing.T) {
	hashA := SourceHash([]byte("a"))
	hashB := SourceHash([]byte("b"))

	for _, tc := range []struct {
		name        string
		index, text string
		want        Freshness
	}{
		{"same bytes", hashA, hashA, FreshnessAgree},
		{"different bytes", hashA, hashB, FreshnessDisagree},
		{"the index holds no hash", "", hashA, FreshnessUnknown},
		{"the text index holds no hash", hashA, "", FreshnessUnknown},
		{"neither holds a hash", "", "", FreshnessUnknown},
	} {
		if got := CompareFreshness(tc.index, tc.text); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	reason := FreshnessDisagree.Reason()
	if !strings.Contains(reason, "disagree") {
		t.Errorf("the reason must say the two indexes disagree: %q", reason)
	}
	if strings.Contains(reason, "properties index is stale") {
		t.Errorf("the reason claims a direction the comparison cannot establish: %q", reason)
	}
	if FreshnessUnknown.Reason() == "" {
		t.Error("an unknown freshness must carry a reason: an empty hash is FLAGGED, never assumed fresh")
	}
	if FreshnessAgree.Reason() != "" {
		t.Error("agreement is not a problem and must not produce a problem-list entry")
	}
}

// TestSourceHash_MatchesTheManifestDefinition.
//
// The token is "the hex SHA-256 of the file's contents" — pkg/knowledge's
// ManifestEntry.Hash, not a second token with the same job. Two hashes computed
// two ways compare unequal forever, which would make every record permanently
// "divergent" and train the problem channel into noise.
func TestSourceHash_MatchesTheManifestDefinition(t *testing.T) {
	// The literal hex of SHA-256("") — pinned rather than recomputed, so this
	// test cannot agree with a broken implementation by using it.
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := SourceHash(nil); got != emptySHA256 {
		t.Errorf("SourceHash(nil) = %q, want %q", got, emptySHA256)
	}
	const abcSHA256 = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := SourceHash([]byte("abc")); got != abcSHA256 {
		t.Errorf("SourceHash(\"abc\") = %q, want %q", got, abcSHA256)
	}
	if len(SourceHash([]byte("x"))) != 64 {
		t.Error("the token must be 64 hex characters, the width the manifest stores")
	}
}

// TestIndexNote_RefusesAnEmptyPath — a row with no path can never be compared
// against a text-index document, so it can never be checked for freshness.
func TestIndexNote_RefusesAnEmptyPath(t *testing.T) {
	store, _ := openIndex(t, Options{})
	if err := IndexNote(context.Background(), store, NoteRows{}, nil, nil); err == nil {
		t.Error("a note with no path was accepted")
	}
	if err := IndexNote(context.Background(), nil, plantNote(t, 1, "growing"), nil, nil); err == nil {
		t.Error("a nil store was accepted")
	}
}

// --- test doubles -----------------------------------------------------------

type recordingStore struct {
	Store
	order *[]string
}

func (s recordingStore) UpsertNote(ctx context.Context, rows NoteRows) error {
	*s.order = append(*s.order, "sqlite")
	return s.Store.UpsertNote(ctx, rows)
}

type failingStore struct {
	Store
	err error
}

func (s failingStore) UpsertNote(context.Context, NoteRows) error { return s.err }
