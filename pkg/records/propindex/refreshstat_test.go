// Omnipus — spec FR-136: the metadata-only stat refresh, and the four things it
// must not do.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// RefreshNoteStat is the correction a content-unchanged sync skip can afford.
// `git checkout`, rsync, `touch` and an iCloud resync all move a file's mtime
// while leaving its bytes identical, so a sync that skips on hash equality
// alone freezes `file.mtime` at the last CONTENT change — and
// `sort by file.mtime desc`, the commonest Bases view there is, then returns a
// plausible, stable, WRONG ordering with no error anywhere.
//
// EVERY ASSERTION BELOW IS ABOUT SOMETHING THE METHOD MUST *NOT* DO, and that
// is deliberate. "The mtime is now right" is satisfiable by re-indexing the
// whole note, which is exactly the expensive behaviour this exists to avoid —
// so a test that only checked the mtime would pass against the bug.
// ---------------------------------------------------------------------------

// statAt stamps a known stat on a note. Supplied rather than read off a real
// file, because a test asserting what the store round-trips must control the
// value it round-trips.
func statAt(rows NoteRows, size int64, mtime time.Time, ctime time.Time) NoteRows {
	rows.Size = size
	rows.MtimeNanos = mtime.UnixNano()
	rows.CtimeNanos, rows.HasCtime = ctime.UnixNano(), true
	return rows
}

func soleCandidate(t *testing.T, store Store, sel Selector) Candidate {
	t.Helper()
	got := collect(t, store, sel)
	if len(got) != 1 {
		t.Fatalf("expected exactly one candidate, got %d", len(got))
	}
	return got[0]
}

// TestRefreshNoteStat_AppliesTheChangeAndReportsIt.
//
// The bool is load-bearing: SyncStats counts the trues, and without a truthful
// one "the mtime is right" cannot be told apart from "the mtime is right
// because we re-indexed the whole note".
func TestRefreshNoteStat_AppliesTheChangeAndReportsIt(t *testing.T) {
	store, _ := openIndex(t, Options{})
	ctx := context.Background()
	first := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	ctime := time.Date(2020, 5, 5, 5, 5, 5, 0, time.UTC)
	rows := statAt(taggedNote(t), 100, first, ctime)
	mustUpsert(t, store, rows)

	later := time.Date(2026, 8, 31, 8, 30, 0, 500, time.UTC)
	changed, err := store.RefreshNoteStat(ctx, rows.Path, 250, later.UnixNano(), 0, false)
	if err != nil {
		t.Fatalf("RefreshNoteStat: %v", err)
	}
	if !changed {
		t.Fatal("FR-136: the stat changed and RefreshNoteStat reported no change. The bool is what a " +
			"caller COUNTS, so a false here makes a metadata-only refresh indistinguishable from " +
			"nothing having happened — and that is the bug this method exists to make visible")
	}

	got := soleCandidate(t, store, Selector{RecordType: "plant"}).File
	if !got.ModTime.Equal(later) {
		t.Errorf("file.mtime is %s after the refresh, want %s (nanoseconds included — the column "+
			"holds ISO-8601 text and a renderer that dropped the fraction would tie every note "+
			"edited in the same second)",
			got.ModTime.Format(time.RFC3339Nano), later.Format(time.RFC3339Nano))
	}
	if got.Size != 250 {
		t.Errorf("file.size is %d after the refresh, want 250", got.Size)
	}
}

// TestRefreshNoteStat_TouchesNothingButTheStat.
//
// source_hash above all. Moving it would forge agreement with the text index
// that D16.5's whole write ordering exists to detect the ABSENCE of — an index
// reporting "these two agree" because this method said so rather than because
// the same bytes were seen twice.
func TestRefreshNoteStat_TouchesNothingButTheStat(t *testing.T) {
	store, _ := openIndex(t, Options{})
	ctx := context.Background()
	ctime := time.Date(2020, 5, 5, 5, 5, 5, 0, time.UTC)
	rows := statAt(taggedNote(t), 100, time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), ctime)
	mustUpsert(t, store, rows)

	sel := Selector{RecordType: "plant"}
	before := soleCandidate(t, store, sel)
	beforeTags := tagsOf(t, store, sel)
	beforeLinks, beforeEmbeds := linksOf(t, store, sel)
	if len(beforeTags) == 0 || len(beforeLinks) == 0 || len(before.PropOrder) == 0 {
		t.Fatalf("the fixture carries no child rows to preserve (tags=%d links=%d props=%d); "+
			"this test would pass by having nothing to lose",
			len(beforeTags), len(beforeLinks), len(before.PropOrder))
	}

	if _, err := store.RefreshNoteStat(ctx, rows.Path,
		999, time.Date(2026, 8, 31, 8, 30, 0, 0, time.UTC).UnixNano(), 0, false); err != nil {
		t.Fatalf("RefreshNoteStat: %v", err)
	}

	after := soleCandidate(t, store, sel)
	if after.SourceHash != before.SourceHash {
		t.Errorf("RefreshNoteStat moved source_hash from %q to %q. A metadata refresh must never "+
			"claim the content was re-read: the freshness comparison would then report agreement "+
			"it has not earned", before.SourceHash, after.SourceHash)
	}
	if len(after.PropOrder) != len(before.PropOrder) {
		t.Errorf("the property rows changed: %d before, %d after. A refresh does not re-parse",
			len(before.PropOrder), len(after.PropOrder))
	}
	if got := tagsOf(t, store, sel); len(got) != len(beforeTags) {
		t.Errorf("the tag rows changed: %v before, %v after", beforeTags, got)
	}
	gotLinks, gotEmbeds := linksOf(t, store, sel)
	if len(gotLinks) != len(beforeLinks) || len(gotEmbeds) != len(beforeEmbeds) {
		t.Errorf("the link rows changed: %v/%v before, %v/%v after",
			beforeLinks, beforeEmbeds, gotLinks, gotEmbeds)
	}

	// ctime is written at index time and is NOT refreshable from the walk,
	// which carries size and mtime only. It must therefore SURVIVE a refresh
	// rather than be cleared by one: three columns are written, two refreshed.
	if !after.File.HasBirthTime || !after.File.BirthTime.Equal(ctime) {
		t.Errorf("the refresh cleared file.ctime (has=%v, %s), want %s preserved",
			after.File.HasBirthTime, after.File.BirthTime, ctime)
	}
}

// TestRefreshNoteStat_ReportsNoChangeWhenNothingChanged.
//
// An identical refresh must report false. A caller counting refreshes would
// otherwise report one on every pass over a vault nobody has touched — which
// makes the count useless for the one thing it is for.
func TestRefreshNoteStat_ReportsNoChangeWhenNothingChanged(t *testing.T) {
	store, _ := openIndex(t, Options{})
	ctx := context.Background()
	mtime := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	rows := statAt(taggedNote(t), 100, mtime, time.Date(2020, 5, 5, 5, 5, 5, 0, time.UTC))
	mustUpsert(t, store, rows)

	changed, err := store.RefreshNoteStat(ctx, rows.Path, 100, mtime.UnixNano(), 0, false)
	if err != nil {
		t.Fatalf("RefreshNoteStat: %v", err)
	}
	if changed {
		t.Error("FR-136: re-applying the SAME stat reported a change")
	}

	// Each half on its own, so a comparison that only looked at one column is
	// caught. A store that compared mtime alone would report no change for a
	// file that was truncated in place within the same nanosecond — and one
	// that compared size alone would miss every `touch`.
	if changed, err = store.RefreshNoteStat(ctx, rows.Path, 101, mtime.UnixNano(), 0, false); err != nil {
		t.Fatalf("RefreshNoteStat (size only): %v", err)
	} else if !changed {
		t.Error("a size-only change was not reported; the comparison is ignoring the size column")
	}
	if changed, err = store.RefreshNoteStat(ctx, rows.Path, 101, mtime.Add(time.Hour).UnixNano(), 0, false); err != nil {
		t.Fatalf("RefreshNoteStat (mtime only): %v", err)
	} else if !changed {
		t.Error("an mtime-only change was not reported; the comparison is ignoring the mtime column")
	}
}

// TestRefreshNoteStat_UnknownPathIsANoOpNotAnError.
//
// The vault is the source of truth and the index is allowed to be behind it —
// the same posture DeleteNote takes for the same reason. It must also not
// CREATE a row: a note the store has never parsed has no properties, no tags
// and no links, and a bare stat row for it would be a candidate with nothing in
// it appearing in answers.
func TestRefreshNoteStat_UnknownPathIsANoOpNotAnError(t *testing.T) {
	store, _ := openIndex(t, Options{})
	ctx := context.Background()
	mustUpsert(t, store, taggedNote(t))
	beforeCount := len(collect(t, store, Selector{}))

	changed, err := store.RefreshNoteStat(ctx, "garden/never-indexed.md", 1, time.Now().UnixNano(), 0, false)
	if err != nil {
		t.Fatalf("RefreshNoteStat on a path the store does not hold returned an error: %v", err)
	}
	if changed {
		t.Error("RefreshNoteStat claimed to change a note the store does not hold")
	}
	if got := len(collect(t, store, Selector{})); got != beforeCount {
		t.Errorf("refreshing an unknown path created a row: %d candidates before, %d after. "+
			"A stat row with no properties, tags or links would be an empty candidate in answers",
			beforeCount, got)
	}

	// An empty path is a CALLER BUG, not a vault fact, and is refused rather
	// than silently treated as "no such note".
	if _, err := store.RefreshNoteStat(ctx, "", 1, 1, 0, false); err == nil {
		t.Error("RefreshNoteStat accepted an empty path; a path is the note's identity here and an " +
			"empty one is a defect in the caller, not a note that happens to be missing")
	}
}
