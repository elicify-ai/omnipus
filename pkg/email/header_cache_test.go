package email

// RED pack — W2 bounded in-memory header cache and folder counts (oracle:
// spec only, never the implementation).
//
// Every expected value derives from the specification, written down before any
// implementation existed (none compiles yet — the expected RED state for a
// Wave-C RED pack):
//
//   - w2 spec §3.9 (50/role cardinality, 5-minute freshness, 30-minute
//     post-close retention, 4 MiB global budget, pair+generation+role keying,
//     newest-unfiltered-pages-only purpose, counts block R-3.9-C1..C4),
//     §3.10 (publication revision R-3.10-1..5), §3.11 (invalidation table),
//     §5 US-8/US-9/US-14, §6 H-1..H-6/V-1..V-5/C-1..C-3, §7.1 test rows,
//     §7.2 DT-3/DT-4, §11 CX-5/CX-9/CX-11/CX-12/CX-13/CX-19.
//   - ADR-20261001 P1.1 (founder-set numbers), P2.2 memory row (never
//     truncate a subject/address; eviction never touches live work), P2.3
//     invalidation table, "Event refresh / no closed polling" and
//     "Publication ordering (I-02)" test-strategy rows.
//
// The frozen §4.1 surfaces under test: HeaderCache.Get(Scope, role) (Page,
// Metadata, bool), .Put(Scope, role, capturedRevision, rows) error,
// .GetCounts(Scope) (Counts, Metadata, bool), .PutCounts(Scope,
// capturedRevision, counts) error, .InvalidateFolder, .MarkPairRemoved,
// .PanelOpened, .PanelClosed; Metadata (source live|memory|none,
// last_validated_at, stale, refresh_needed, publication_revision, notice_code).
// Constructors are the proposed construction point; the clock and the
// current-revision source are the two seams the spec's own test plan demands
// (DT-3 "fake clock"; register row 10's captured-vs-current comparison — the
// counter itself is w5-integration's, so the test injects it at this edge).

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// fakeClock is the injected clock the freshness/retention boundaries require
// (DT-3). Real sleeps are forbidden here by docs/internal/false-green-patterns.md
// section 3: the properties are discrete, so the clock moves discretely.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// Advance moves the clock by mins minutes and secs seconds — the discrete
// boundary stepper for the 5-minute freshness and 30-minute retention edges.
func (c *fakeClock) Advance(mins, secs int) {
	c.now = c.now.Add(time.Duration(mins)*time.Minute + time.Duration(secs)*time.Second)
}

func makeRows(count, uidStart int, epoch uint32) []MailRow {
	rows := make([]MailRow, count)
	for i := range rows {
		rows[i] = MailRow{
			UID:         uint32(uidStart + i),
			UIDValidity: epoch,
			Subject:     fmt.Sprintf("row-%d", uidStart+i),
			Date:        time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		}
	}
	return rows
}

func uidSet(rows []MailRow) map[uint32]bool {
	set := make(map[uint32]bool, len(rows))
	for _, r := range rows {
		set[r.UID] = true
	}
	return set
}

// TestHeaderCache_WarmHitServesNewestFifty — H-1, H-5, US-8.1/US-8.5.
// Oracle: a fresh snapshot of 80 headers caches EXACTLY the newest 50 per
// role (§3.9 cardinality, founder-set), served newest-first with
// source=memory and the recorded validation time; the live list is never
// shortened to fit (that face is view-side; the cache face is "exactly the
// newest 50 and nothing else").
func TestHeaderCache_WarmHitServesNewestFifty(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	rev := uint64(7)
	clock := &fakeClock{now: base}
	hc := NewHeaderCache(clock.Now, func(Scope) Revision { return Revision(rev) })
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}

	if err := hc.Put(scope, FolderSent, Revision(rev), makeRows(80, 1, 100)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	page, md, found := hc.Get(scope, FolderSent)
	if !found {
		t.Fatal("a fresh snapshot must be served from memory (H-1)")
	}
	if len(page) != 50 {
		t.Fatalf("cache holds %d rows, want exactly the newest 50 (§3.9 cardinality)", len(page))
	}
	if page[0].UID != 80 {
		t.Fatalf("newest-first violated: first row UID = %d, want 80", page[0].UID)
	}
	want := uidSet(makeRows(50, 31, 100)) // UIDs 31..80
	if !reflect.DeepEqual(uidSet(page), want) {
		t.Fatal("cache must hold exactly the newest 50 UIDs (31..80) — nothing more, nothing less (H-5)")
	}
	if md.Source != "memory" {
		t.Fatalf("source = %q, want %q (US-8.1: rendered from memory)", md.Source, "memory")
	}
	if md.LastValidatedAt == nil || !md.LastValidatedAt.Equal(base) {
		t.Fatalf("metadata must carry the recorded validation time %v, got %+v", base, md.LastValidatedAt)
	}
	if md.Stale {
		t.Fatal("a fresh snapshot must not be labelled stale")
	}
	if md.PublicationRevision != Revision(rev) {
		t.Fatalf("metadata revision = %v, want %v — the value the data was produced under (§3.10)", md.PublicationRevision, rev)
	}
}

// TestHeaderCache_FreshnessBoundaryAtFiveMinutes — DT-3 freshness boundary,
// H-1/H-2, US-8.1/US-8.2.
// Oracle: "older than 5 minutes" is the founder-set stale threshold — at
// 4:59 and at exactly 5:00 the rows are fresh; at 5:01 the SAME rows are
// still served immediately (display first) but labelled stale with
// refresh_needed set, and the validation timestamp is NOT reset by serving.
func TestHeaderCache_FreshnessBoundaryAtFiveMinutes(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	rev := uint64(1)
	clock := &fakeClock{now: base}
	hc := NewHeaderCache(clock.Now, func(Scope) Revision { return Revision(rev) })
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := hc.Put(scope, FolderSent, Revision(rev), makeRows(10, 1, 100)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	clock.Advance(4, 59)
	if _, md, found := hc.Get(scope, FolderSent); !found || md.Stale || md.RefreshNeeded {
		t.Fatalf("at 4:59 the snapshot is fresh: found=%v stale=%v refresh_needed=%v (DT-3)", found, md.Stale, md.RefreshNeeded)
	}

	clock.Advance(0, 1) // exactly 5:00 — not yet "older than 5 minutes"
	if _, md, found := hc.Get(scope, FolderSent); !found || md.Stale {
		t.Fatalf("at exactly 5:00 the snapshot is still fresh (boundary, DT-3): found=%v stale=%v", found, md.Stale)
	}

	clock.Advance(0, 1) // 5:01 — one second past the threshold
	page, md, found := hc.Get(scope, FolderSent)
	if !found || len(page) != 10 {
		t.Fatalf("stale rows must still be served immediately (display first, US-8.2): found=%v rows=%d", found, len(page))
	}
	if !md.Stale || !md.RefreshNeeded {
		t.Fatalf("at 5:01 the row set must be labelled stale + refresh_needed (US-8.2): stale=%v refresh_needed=%v", md.Stale, md.RefreshNeeded)
	}
	if md.LastValidatedAt == nil || !md.LastValidatedAt.Equal(base) {
		t.Fatalf("serving stale rows must never reset the validation timestamp (US-8.2): want %v got %+v", base, md.LastValidatedAt)
	}
}

// TestHeaderCache_RetentionDropsAfterThirtyMinutesClosed — H-4, US-8.4, CX-11.
// Oracle: the retention clock starts at panel close; entries drop 30 minutes
// after it. A read within the window must NOT extend it (CX-11's
// read-extends-retention mutation); reopening resets the clock going forward
// but resurrects nothing; while the panel stays OPEN there is no drop.
func TestHeaderCache_RetentionDropsAfterThirtyMinutesClosed(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	rev := uint64(1)
	clock := &fakeClock{now: base}
	hc := NewHeaderCache(clock.Now, func(Scope) Revision { return Revision(rev) })
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := hc.Put(scope, FolderSent, Revision(rev), makeRows(5, 1, 100)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// While open, time alone drops nothing (the clock starts at close).
	hc.PanelOpened(scope)
	clock.Advance(120, 0)
	if _, _, found := hc.Get(scope, FolderSent); !found {
		t.Fatal("an open panel's entries must not drop by elapsed time alone (§3.9: panel close starts the clock)")
	}

	hc.PanelClosed(scope)
	clock.Advance(29, 0)
	if _, _, found := hc.Get(scope, FolderSent); !found {
		t.Fatal("entries must survive 29 minutes past close (H-4)")
	}
	clock.Advance(0, 59) // 29:59 past close
	if _, _, found := hc.Get(scope, FolderSent); !found {
		t.Fatal("entries must survive 29:59 past close (DT-3 retention boundary)")
	}
	clock.Advance(0, 2) // 30:01 past close
	if _, _, found := hc.Get(scope, FolderSent); found {
		t.Fatal("entries must be dropped 30 minutes after the panel was last open (§3.9/H-4)")
	}

	// CX-11 variant: repeated reads at 29 minutes must not immortalize the
	// entries — the reads above happened inside the window, and the 30:01
	// drop still fired. Now verify a reopen does not resurrect.
	hc.PanelOpened(scope)
	if _, _, found := hc.Get(scope, FolderSent); found {
		t.Fatal("reopening resets the retention clock going forward; it must not resurrect dropped entries (H-4: the reopen fetches live)")
	}
}

// TestHeaderCache_BudgetOverflowIsVisibleNotTruncated — CX-12, DT-3 budget
// boundary, US-8 budget rule.
// Oracle: the 4 MiB global reusable-metadata budget (founder Q3=A) refuses
// an overflowing publication with the TYPED budget error (§4.1 freezes
// ErrCacheBudgetExceeded) and a visible live-only outcome — never truncated
// fields, never a partial publication of the refused rows.
func TestHeaderCache_BudgetOverflowIsVisibleNotTruncated(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	rev := uint64(1)
	clock := &fakeClock{now: base}
	hc := NewHeaderCache(clock.Now, func(Scope) Revision { return Revision(rev) })

	huge := func(n int) []MailRow {
		rows := make([]MailRow, n)
		for i := range rows {
			rows[i] = MailRow{
				UID:         uint32(i + 1),
				UIDValidity: 100,
				Subject:     makeSubject(400 << 10), // 400 KiB per row
			}
		}
		return rows
	}

	// Just under: 8 × 400 KiB = 3.2 MiB fits inside 4 MiB.
	under := Scope{PairID: "pair-under", Generation: "gen-1"}
	if err := hc.Put(under, FolderSent, Revision(rev), huge(8)); err != nil {
		t.Fatalf("8 rows of 400 KiB (3.2 MiB) must fit the 4 MiB budget, got %v", err)
	}
	if page, _, found := hc.Get(under, FolderSent); !found || len(page) != 8 || len(page[0].Subject) != 400<<10 {
		t.Fatalf("under-budget rows must be stored whole: found=%v rows=%d subjectLen=%d", found, len(page), len(page[0].Subject))
	}

	// Just over: 11 × 400 KiB = 4.4 MiB overflows.
	over := Scope{PairID: "pair-over", Generation: "gen-1"}
	err := hc.Put(over, FolderSent, Revision(rev), huge(11))
	if !errors.Is(err, ErrCacheBudgetExceeded) {
		t.Fatalf("overflow must fail with the typed ErrCacheBudgetExceeded (CX-12/§4.1), got %v", err)
	}
	if page, _, found := hc.Get(over, FolderSent); found {
		t.Fatalf("a refused publication must publish nothing — no truncated or partial rows may be served, got %d rows", len(page))
	}

	// DT-3: one pathological row among normal ones is the same visible
	// refusal — a single field is never silently truncated to fit.
	mixed := makeRows(49, 1, 100)
	pathological := MailRow{UID: 50, UIDValidity: 100, Subject: makeSubject(5 << 20)}
	mixed = append(mixed, pathological)
	if err := hc.Put(Scope{PairID: "pair-mixed", Generation: "gen-1"}, FolderSent, Revision(rev), mixed); !errors.Is(err, ErrCacheBudgetExceeded) {
		t.Fatalf("a single oversized row must refuse the publication visibly (DT-3 oversized subject envelope), got %v", err)
	}
	if page, _, found := hc.Get(Scope{PairID: "pair-mixed", Generation: "gen-1"}, FolderSent); found && len(page) == 50 && len(page[49].Subject) < 5<<20 {
		t.Fatal("a truncated subject was served — truncation is forbidden even when the rest of the set fits (ADR P2.2)")
	}
}

func makeSubject(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

// TestHeaderCache_UidValidityChangeDiscardsEpoch — V-1, US-9.1, §3.11
// UIDVALIDITY row.
// Oracle: a UIDVALIDITY change discards EVERY cached header and count for the
// old epoch before any new row publishes; an old UID is never reused against
// the new epoch (CX-4 family: the epoch itself is never fabricated — an
// unknown epoch is nullable state, tested at the envelope in
// TestCacheFile_RoundTripPreservesAllFields).
func TestHeaderCache_UidValidityChangeDiscardsEpoch(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	rev := uint64(1)
	clock := &fakeClock{now: base}
	hc := NewHeaderCache(clock.Now, func(Scope) Revision { return Revision(rev) })
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}

	oldRows := []MailRow{
		{UID: 999, UIDValidity: 100, Subject: "old-epoch-999"},
		{UID: 998, UIDValidity: 100, Subject: "old-epoch-998"},
	}
	if err := hc.Put(scope, FolderSent, Revision(rev), oldRows); err != nil {
		t.Fatalf("Put (old epoch): %v", err)
	}
	if err := hc.PutCounts(scope, Revision(rev), Counts{Total: 2, Unseen: 2, UIDValidity: 100}); err != nil {
		t.Fatalf("PutCounts (old epoch): %v", err)
	}

	hc.InvalidateFolder(scope, FolderSent)

	if page, _, found := hc.Get(scope, FolderSent); found || len(page) != 0 {
		t.Fatalf("old-epoch headers must be discarded (V-1): found=%v rows=%d", found, len(page))
	}
	if _, _, found := hc.GetCounts(scope); found {
		t.Fatal("old-epoch counts must be discarded with the headers (V-1: 'every cached header, page cursor and count')")
	}

	newRows := []MailRow{
		{UID: 1, UIDValidity: 200, Subject: "new-epoch-1"},
		{UID: 2, UIDValidity: 200, Subject: "new-epoch-2"},
	}
	if err := hc.Put(scope, FolderSent, Revision(rev), newRows); err != nil {
		t.Fatalf("Put (new epoch): %v", err)
	}
	page, _, found := hc.Get(scope, FolderSent)
	if !found || len(page) != 2 {
		t.Fatalf("new-epoch rows must publish: found=%v rows=%d", found, len(page))
	}
	for _, row := range page {
		if row.UID == 999 || row.UID == 998 {
			t.Fatalf("old-epoch UID %d survived the epoch change — an old UID is never reused against a new epoch (V-1)", row.UID)
		}
		if row.UIDValidity != 200 {
			t.Fatalf("row carries stale epoch %d, want 200", row.UIDValidity)
		}
	}
}

// TestHeaderCache_SupersededReadPublishesNothing — V-2, CX-9, R-3.10-3,
// FR-W2-16.
// Oracle (the I-02 ordering rule, cache layers): a read that captured
// revision N and finishes after the revision advanced publishes NOTHING —
// memory rows, counts, timestamps, revision metadata. The invalidation-only
// mutation (delete-then-refill without the revision fence) dies here: the
// stale data must not reappear and the newer revision's data must stand.
func TestHeaderCache_SupersededReadPublishesNothing(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	rev := uint64(1)
	clock := &fakeClock{now: base}
	hc := NewHeaderCache(clock.Now, func(Scope) Revision { return Revision(rev) })
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}

	v1 := makeRows(3, 1, 100)
	if err := hc.Put(scope, FolderSent, Revision(1), v1); err != nil {
		t.Fatalf("Put v1 under current revision 1: %v", err)
	}
	if err := hc.PutCounts(scope, Revision(1), Counts{Total: 3, Unseen: 3, UIDValidity: 100}); err != nil {
		t.Fatalf("PutCounts v1: %v", err)
	}

	rev = 2 // the mark-read and its post-mutation refresh advanced the revision

	staleRows := makeRows(3, 50, 100) // the pre-mutation read's data, finishing late
	if err := hc.Put(scope, FolderSent, Revision(1), staleRows); !errors.Is(err, ErrStalePublication) {
		t.Fatalf("a superseded read must refuse to publish (R-3.10-3/CX-9), got %v", err)
	}
	if err := hc.PutCounts(scope, Revision(1), Counts{Total: 99, UIDValidity: 100}); !errors.Is(err, ErrStalePublication) {
		t.Fatalf("a superseded count publication must refuse, got %v", err)
	}

	page, md, found := hc.Get(scope, FolderSent)
	if !found || !reflect.DeepEqual(page, v1) {
		t.Fatalf("v1 rows must stand untouched after the superseded read: found=%v page=%+v", found, page)
	}
	if md.PublicationRevision != Revision(1) {
		t.Fatalf("metadata revision = %v, want 1 — the stale read advances nothing", md.PublicationRevision)
	}
	if md.LastValidatedAt == nil || !md.LastValidatedAt.Equal(base) {
		t.Fatalf("validation timestamp must not move for a refused publication: want %v got %+v", base, md.LastValidatedAt)
	}
	counts, _, cfound := hc.GetCounts(scope)
	if !cfound || counts.Total != 3 {
		t.Fatalf("v1 counts must stand untouched: found=%v counts=%+v", cfound, counts)
	}
}

// TestHeaderCache_IsolationByPairGenerationAndRemoval — CX-5, grill I-01's
// cache face, V-3/V-4 memory faces, §3.9 isolation row.
// Oracle: entries are keyed by the full pair + generation + role — never by
// account alone. Two pairs on one account never see each other's rows; a
// bumped generation never sees the old generation's rows (reconfiguration
// deletes rather than migrates, V-4); a removed pair serves nothing and its
// late writes cannot recreate entries (V-3).
func TestHeaderCache_IsolationByPairGenerationAndRemoval(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	rev := uint64(1)
	clock := &fakeClock{now: base}
	hc := NewHeaderCache(clock.Now, func(Scope) Revision { return Revision(rev) })

	pairA := Scope{PairID: "pair-A", Generation: "gen-1"}
	if err := hc.Put(pairA, FolderSent, Revision(rev), makeRows(2, 1, 100)); err != nil {
		t.Fatalf("Put pair A: %v", err)
	}

	// Two pairs configured on one account (same server credentials) — the
	// account is invisible to the key: pair B sees nothing of pair A (CX-5).
	pairB := Scope{PairID: "pair-B", Generation: "gen-1"}
	if page, _, found := hc.Get(pairB, FolderSent); found || len(page) != 0 {
		t.Fatalf("pair B must never see pair A's rows (CX-5/I-01): found=%v rows=%d", found, len(page))
	}

	// One pair across a reconfiguration — the new generation starts empty.
	pairAgen2 := Scope{PairID: "pair-A", Generation: "gen-2"}
	if page, _, found := hc.Get(pairAgen2, FolderSent); found || len(page) != 0 {
		t.Fatalf("the new generation must not be served the old generation's rows (V-4): found=%v rows=%d", found, len(page))
	}

	// Removal: the memory cache serves nothing and refuses resurrection (V-3).
	hc.MarkPairRemoved(pairA)
	if page, _, found := hc.Get(pairA, FolderSent); found || len(page) != 0 {
		t.Fatalf("a removed pair's cache must serve nothing (V-3/§3.11): found=%v rows=%d", found, len(page))
	}
	if err := hc.Put(pairA, FolderSent, Revision(rev), makeRows(2, 10, 100)); err == nil {
		t.Fatal("a late write for a removed pair must be refused — late completions cannot recreate cache state (V-3)")
	}
	if _, _, found := hc.Get(pairA, FolderSent); found {
		t.Fatal("the refused late write must not have landed")
	}
}

// TestFolderCounts_FreshCountsServedWithoutDialAndNoTimer — C-1, C-2,
// US-14.1/14.2, R-3.9-C3, CX-19.
// Oracle: counts younger than 5 minutes are served with their fresh metadata
// (the trigger's zero-live-dial face — the caller dials only when the
// metadata says stale); with NO trigger for an hour the stored counts stand
// UNCHANGED and merely labelled stale — a repeating timer would have
// refreshed them (CX-19's timer mutation dies here); manual Refresh
// (PutCounts) is accepted at any age.
func TestFolderCounts_FreshCountsServedWithoutDialAndNoTimer(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	rev := uint64(1)
	clock := &fakeClock{now: base}
	hc := NewHeaderCache(clock.Now, func(Scope) Revision { return Revision(rev) })
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := hc.PutCounts(scope, Revision(rev), Counts{Total: 12, Unseen: 4, UIDValidity: 100}); err != nil {
		t.Fatalf("PutCounts: %v", err)
	}

	clock.Advance(4, 59)
	counts, md, found := hc.GetCounts(scope)
	if !found || counts.Total != 12 || counts.Unseen != 4 {
		t.Fatalf("counts 4:59 old must be served as-is (C-1): found=%v counts=%+v", found, counts)
	}
	if md.Stale {
		t.Fatal("counts 4:59 old are fresh (C-1 boundary)")
	}

	// One hour with zero trigger calls: no timer refreshed anything (C-3:
	// counts advance only on the four triggers; CX-19 kills the ticker).
	clock.Advance(60, 0)
	counts, md, found = hc.GetCounts(scope)
	if !found || counts.Total != 12 {
		t.Fatalf("with no trigger the stored counts stand unchanged (R-3.9-C3): found=%v counts=%+v", found, counts)
	}
	if !md.Stale || !md.RefreshNeeded {
		t.Fatalf("aged counts must be labelled stale + refresh_needed for the next trigger: stale=%v refresh_needed=%v", md.Stale, md.RefreshNeeded)
	}
	if md.LastValidatedAt == nil || !md.LastValidatedAt.Equal(base) {
		t.Fatalf("no timer may advance the counts' timestamp: want %v got %+v", base, md.LastValidatedAt)
	}

	// Manual Refresh is the explicit human action: always accepted (C-2).
	clock.Advance(0, 1)
	if err := hc.PutCounts(scope, Revision(rev), Counts{Total: 13, Unseen: 0, UIDValidity: 100}); err != nil {
		t.Fatalf("manual Refresh must refresh regardless of age (C-2): %v", err)
	}
	counts, md, _ = hc.GetCounts(scope)
	if counts.Total != 13 || md.Stale || md.LastValidatedAt.Equal(base) {
		t.Fatalf("manual Refresh replaces counts and timestamp: counts=%+v md=%+v", counts, md)
	}
}

// TestFolderCounts_StaleBoundaryAndFailedRefreshPreserves — C-2, C-3,
// US-14.2/14.3.
// Oracle: past 5:01 the counts are stale for the next trigger; a FAILED
// refresh (no PutCounts ever arrives) keeps the previous counts with the
// timestamp unchanged — never rendered as checked-empty; a superseded
// count publication refuses (§3.10) and prior counts stand.
func TestFolderCounts_StaleBoundaryAndFailedRefreshPreserves(t *testing.T) {
	base := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	rev := uint64(1)
	clock := &fakeClock{now: base}
	hc := NewHeaderCache(clock.Now, func(Scope) Revision { return Revision(rev) })
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := hc.PutCounts(scope, Revision(1), Counts{Total: 7, Unseen: 1, UIDValidity: 100}); err != nil {
		t.Fatalf("PutCounts: %v", err)
	}

	clock.Advance(5, 1)
	_, md, found := hc.GetCounts(scope)
	if !found || !md.Stale || !md.RefreshNeeded {
		t.Fatalf("counts at 5:01 must be labelled stale for the next trigger (C-2 boundary): found=%v md=%+v", found, md)
	}

	// The refresh FAILS: no publication arrives. Prior counts stand, the
	// timestamp is unchanged (C-3), and nothing renders as a fabricated empty.
	clock.Advance(3, 0)
	counts, md, found := hc.GetCounts(scope)
	if !found || counts.Total != 7 || counts.Unseen != 1 {
		t.Fatalf("a failed refresh keeps prior counts (C-3): found=%v counts=%+v", found, counts)
	}
	if !md.LastValidatedAt.Equal(base) {
		t.Fatalf("a failed refresh never resets the counts' timestamp (C-3): want %v got %v", base, md.LastValidatedAt)
	}

	// The superseded variant: the revision advanced while the refresh ran.
	rev = 2
	if err := hc.PutCounts(scope, Revision(1), Counts{Total: 42, UIDValidity: 100}); !errors.Is(err, ErrStalePublication) {
		t.Fatalf("a superseded count publication must refuse (C-3/§3.10), got %v", err)
	}
	counts, _, _ = hc.GetCounts(scope)
	if counts.Total != 7 {
		t.Fatalf("the refused publication must not have landed, got total %d want 7", counts.Total)
	}
}
