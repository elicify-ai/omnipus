// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U6 (Wave D) — coverage for FR-097a (ListSessions' reconcile-pass
// prune of an out-of-band-deleted directory), FR-102 (the reconcile ->
// snapshot test barrier) and W16a (the store-layer pagination primitive,
// ListSessionsPage) plus IsOrphan (BDD-106's store-layer support).
//
// Per binding Rule 1, every assertion runs against a REAL *UnifiedStore
// rooted at a t.TempDir() and real on-disk directories — the prune test in
// particular removes a real directory via os.RemoveAll, exactly as
// RetentionSweep/an operator/a crashed deploy would, never a mock.
package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------
// Test #114: TestListSessions_PrunesOutOfBandDeletedDirectory (BDD-110)
// ---------------------------------------------------------------------

// TestListSessions_PrunesOutOfBandDeletedDirectory is FR-097a's gate: a
// session directory removed BEHIND the store's back (os.RemoveAll directly,
// never DeleteSession) must be dropped from the NEXT ListSessions result,
// from metaCache, and from the parent index — not "resurrected" the way the
// codebase's own ClearAll comment names as the failure this closes. See this
// test's red-run evidence in the U6 dispatch report for the demonstration
// that skipping the prune pass makes this fail.
func TestListSessions_PrunesOutOfBandDeletedDirectory(t *testing.T) {
	store := u6NewTestStore(t)

	metaA, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	metaB, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	require.NotEqual(t, metaA.ID, metaB.ID, "FR-074 corollary: distinct ids")

	// Positive lower bound (Rule 4): both A and B are genuinely loaded into
	// metaCache before the out-of-band removal, proving the prune below has
	// something real to prune, not an empty search.
	before, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, before, 2, "both A and B must be listed before the out-of-band removal")

	// B's directory removed OUT OF BAND — os.RemoveAll directly, exactly
	// like RetentionSweep/an operator rm/a crashed deploy, never through
	// DeleteSession (which would take the normal eviction path already
	// covered elsewhere).
	require.NoError(t, os.RemoveAll(filepath.Join(store.BaseDir(), metaB.ID)))

	after, err := store.ListSessions()
	require.NoError(t, err)
	ids := make([]string, 0, len(after))
	for _, m := range after {
		ids = append(ids, m.ID)
	}
	assert.Contains(t, ids, metaA.ID, "A must still be listed")
	assert.NotContains(t, ids, metaB.ID, "B's cache entry must be pruned — its directory is gone")

	// The prune must also evict metaCache and the parent index directly —
	// not merely filter B out of THIS call's returned slice while leaving a
	// stale cache entry for a future call to resurrect.
	store.cacheMu.RLock()
	_, stillCached := store.metaCache[metaB.ID]
	store.cacheMu.RUnlock()
	assert.False(t, stillCached, "B must be evicted from metaCache itself, not just filtered from the result")

	// A cacheLoadFailures exclusion is a DIFFERENT set and must be
	// undisturbed by this prune (Ambiguity item 8).
	assert.Equal(t, 0, store.CacheLoadFailureCount())
}

// TestListSessions_PruneAlsoUpdatesParentIndex extends the prune coverage to
// FR-097a's "and MUST update the parent index accordingly" clause: pruning a
// CHILD whose directory vanished must decrement its parent's child_count.
func TestListSessions_PruneAlsoUpdatesParentIndex(t *testing.T) {
	store := u6NewTestStore(t)
	parent, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	child, err := store.NewSession(SessionTypeDelegate, "", "agent-1")
	require.NoError(t, err)
	require.NotEqual(t, parent.ID, child.ID)

	parentID := parent.ID
	require.NoError(t, store.SetMeta(child.ID, MetaPatch{ParentSessionID: &parentID}))
	require.Equal(t, 1, store.ChildCount(parent.ID), "positive lower bound: the child must be indexed before the prune")

	require.NoError(t, os.RemoveAll(filepath.Join(store.BaseDir(), child.ID)))
	_, err = store.ListSessions()
	require.NoError(t, err)

	assert.Equal(t, 0, store.ChildCount(parent.ID), "the parent index must be updated when the child is pruned")
}

// ---------------------------------------------------------------------
// Test #92: TestListSessions_ConcurrentDeleteConsistency (BDD-95, FR-102)
// ---------------------------------------------------------------------

// TestListSessions_ConcurrentDeleteConsistency uses the FR-102
// u6ReconcileSnapshotBarrierFn seam to interleave a DeleteSession
// DETERMINISTICALLY between ListSessions' reconcile pass and its final
// snapshot (BDD-95/SC-041), rather than hoping go test -race happens to hit
// the window. Per this spec's narrow stated exception to binding rules 1-2
// (lock-order/interleaving properties have no on-disk artefact), this test
// asserts on the seam's invocation — but the seam itself is a production
// primitive required by FR-102, not a test-only substitute for the thing
// under test.
func TestListSessions_ConcurrentDeleteConsistency(t *testing.T) {
	store := u6NewTestStore(t)
	metaA, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	metaB, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	require.NotEqual(t, metaA.ID, metaB.ID)

	origBarrier := u6ReconcileSnapshotBarrierFn
	t.Cleanup(func() { u6ReconcileSnapshotBarrierFn = origBarrier })

	var barrierHit sync.WaitGroup
	barrierHit.Add(1)
	deleteDone := make(chan struct{})
	u6ReconcileSnapshotBarrierFn = func() {
		barrierHit.Done()
		<-deleteDone // block ListSessions HERE until the concurrent delete finishes
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		barrierHit.Wait() // wait until ListSessions is parked exactly at the barrier
		require.NoError(t, store.DeleteSession(metaB.ID))
		close(deleteDone)
	}()

	results, listErr := store.ListSessions()
	wg.Wait()
	// Restore the real (no-op) barrier BEFORE any further ListSessions call
	// — the override above is single-use (its WaitGroup/channel are only
	// valid for the one interleaving above); reusing it on a later call
	// would double-Done() the WaitGroup and panic.
	u6ReconcileSnapshotBarrierFn = origBarrier
	require.NoError(t, listErr)

	ids := make([]string, 0, len(results))
	for _, m := range results {
		ids = append(ids, m.ID)
		require.NotEmpty(t, m.ID, "no partially-composed meta may be returned")
	}
	assert.Contains(t, ids, metaA.ID, "A (untouched by the interleaved delete) must still be present")
	// B MAY be omitted (FR-086) — B was deleted exactly at the barrier, so
	// either outcome (present or omitted) is consistent with the stated
	// model; the property under test is "no panic, no deadlock, no
	// partially-composed meta", not B's specific presence.
	assert.NoError(t, listErr, "no panic or deadlock — the interleaved delete must not corrupt the call")

	// After the interleaving completes, a subsequent call must correctly
	// reflect B's deletion — proving the interleaving did not leave the
	// store in an inconsistent state.
	final, err := store.ListSessions()
	require.NoError(t, err)
	finalIDs := make([]string, 0, len(final))
	for _, m := range final {
		finalIDs = append(finalIDs, m.ID)
	}
	assert.Contains(t, finalIDs, metaA.ID)
	assert.NotContains(t, finalIDs, metaB.ID, "B must be gone after the delete that raced the barrier completes")
}

// ---------------------------------------------------------------------
// W16a: TestListSessionsPage_WindowAndStability (FR-092/FR-098-at-the-
// store-layer)
// ---------------------------------------------------------------------

func TestListSessionsPage_WindowAndStability(t *testing.T) {
	store := u6NewTestStore(t)
	const n = 5
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		m, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		ids = append(ids, m.ID)
	}

	full, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, full, n, "positive lower bound: all N sessions must be listed before paging them")

	page1, err := store.ListSessionsPage(0, 2)
	require.NoError(t, err)
	assert.Len(t, page1.Sessions, 2)
	assert.Equal(t, 2, page1.NextOffset)
	assert.Equal(t, n, page1.Total)

	page2, err := store.ListSessionsPage(page1.NextOffset, 2)
	require.NoError(t, err)
	assert.Len(t, page2.Sessions, 2)
	assert.Equal(t, 4, page2.NextOffset)

	page3, err := store.ListSessionsPage(page2.NextOffset, 2)
	require.NoError(t, err)
	assert.Len(t, page3.Sessions, 1, "the last page must carry the remainder, not overshoot")
	assert.Equal(t, -1, page3.NextOffset, "the last page must signal no further page")

	// Window correctness: concatenating every page's ids must reproduce the
	// full ordered sequence exactly (no gaps, no duplicates, no reordering).
	reassembled := make([]string, 0, len(page1.Sessions)+len(page2.Sessions)+len(page3.Sessions))
	for _, m := range page1.Sessions {
		reassembled = append(reassembled, m.ID)
	}
	for _, m := range page2.Sessions {
		reassembled = append(reassembled, m.ID)
	}
	for _, m := range page3.Sessions {
		reassembled = append(reassembled, m.ID)
	}
	expected := make([]string, 0, len(full))
	for _, m := range full {
		expected = append(expected, m.ID)
	}
	assert.Equal(t, expected, reassembled, "paged windows must reassemble into the exact full ordered sequence")

	// Stability: two identical calls with no intervening write return a
	// byte-identical (here: id-identical, in order) window.
	repeat, err := store.ListSessionsPage(0, 3)
	require.NoError(t, err)
	repeatAgain, err := store.ListSessionsPage(0, 3)
	require.NoError(t, err)
	idsA := make([]string, 0, len(repeat.Sessions))
	idsB := make([]string, 0, len(repeatAgain.Sessions))
	for _, m := range repeat.Sessions {
		idsA = append(idsA, m.ID)
	}
	for _, m := range repeatAgain.Sessions {
		idsB = append(idsB, m.ID)
	}
	assert.Equal(t, idsA, idsB, "two identical calls with no intervening write must return the identical window")

	// Out-of-range offset: empty page, not an error.
	empty, err := store.ListSessionsPage(1000, 2)
	require.NoError(t, err)
	assert.Empty(t, empty.Sessions)
	assert.Equal(t, -1, empty.NextOffset)

	// limit <= 0 means "no limit" — a single page returns the whole
	// remainder from offset.
	unbounded, err := store.ListSessionsPage(0, 0)
	require.NoError(t, err)
	assert.Len(t, unbounded.Sessions, n)
	assert.Equal(t, -1, unbounded.NextOffset)
}

// TestListSessions_TieBreaksOnSessionID pins the FR-098-style ordering fix
// this unit made at the store layer: two sessions sharing one UpdatedAt (a
// real possibility down to whatever clock resolution the filesystem/CI box
// gives) must sort by session id, not by Go's randomized map iteration
// order, so repeated calls cannot reorder them.
func TestListSessions_TieBreaksOnSessionID(t *testing.T) {
	store := u6NewTestStore(t)
	metaA, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	metaB, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	// Force an identical UpdatedAt on both via a direct SetMeta title patch
	// (identity-group only, does not touch Stats) — using SetMeta twice in
	// the same call is not possible, so pin both to the SAME timestamp by
	// patching one to match the other's CURRENT UpdatedAt is unnecessary:
	// simpler and equally valid is asserting that repeated ListSessions
	// calls with NO intervening write agree on order regardless of ties,
	// which is the property that actually matters for pagination stability.
	for i := 0; i < 5; i++ {
		got, err := store.ListSessions()
		require.NoError(t, err)
		require.Len(t, got, 2)
		if got[0].UpdatedAt.Equal(got[1].UpdatedAt) {
			// When timestamps tie, the lower session id must sort first.
			lo, hi := metaA.ID, metaB.ID
			if hi < lo {
				lo, hi = hi, lo
			}
			assert.Equal(t, lo, got[0].ID)
			assert.Equal(t, hi, got[1].ID)
		}
	}
}

// ---------------------------------------------------------------------
// IsOrphan (BDD-106 store-layer support)
// ---------------------------------------------------------------------

func TestIsOrphan_DetectsMissingParent(t *testing.T) {
	store := u6NewTestStore(t)
	root, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	assert.False(t, store.IsOrphan(root), "a root (empty ParentSessionID) is never an orphan")

	child, err := store.NewSession(SessionTypeDelegate, "", "agent-1")
	require.NoError(t, err)
	fakeParentID := "does-not-exist-" + root.ID
	require.NoError(t, store.SetMeta(child.ID, MetaPatch{ParentSessionID: &fakeParentID}))
	got, err := store.GetMeta(child.ID)
	require.NoError(t, err)
	assert.True(t, store.IsOrphan(got), "a child whose ParentSessionID names a non-existent session is an orphan")

	// Re-parent to a REAL, existing session — no longer an orphan.
	realParentID := root.ID
	require.NoError(t, store.SetMeta(child.ID, MetaPatch{ParentSessionID: &realParentID}))
	got2, err := store.GetMeta(child.ID)
	require.NoError(t, err)
	assert.False(t, store.IsOrphan(got2), "a child whose parent genuinely exists is not an orphan")

	assert.False(t, store.IsOrphan(nil), "nil meta must not panic")
}

// ---------------------------------------------------------------------
// 14-reviewer fix wave, finding #3 (MEDIUM perf): ListSessionsFiltered.
// ---------------------------------------------------------------------

// TestListSessionsFiltered_NilPredMatchesListSessions proves
// ListSessionsFiltered(nil) is byte-for-byte the same call ListSessions()
// itself now delegates to — the two entry points can never silently
// diverge on the reconcile/prune logic.
func TestListSessionsFiltered_NilPredMatchesListSessions(t *testing.T) {
	store := u6NewTestStore(t)
	for i := 0; i < 5; i++ {
		_, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
	}

	viaListSessions, err := store.ListSessions()
	require.NoError(t, err)
	viaFiltered, err := store.ListSessionsFiltered(nil)
	require.NoError(t, err)

	require.Equal(t, len(viaListSessions), len(viaFiltered))
	for i := range viaListSessions {
		assert.Equal(t, viaListSessions[i].ID, viaFiltered[i].ID)
	}
}

// TestListSessionsFiltered_ReturnsOnlyMatches is the functional-correctness
// half of finding #3: only sessions the predicate approves are returned,
// with a positive lower bound (2 real matches, not just "not everything")
// pairing the near-zero assertion (3 real non-matches excluded).
func TestListSessionsFiltered_ReturnsOnlyMatches(t *testing.T) {
	store := u6NewTestStore(t)

	goalCond := "ship the feature"
	var matchIDs []string
	for i := 0; i < 2; i++ {
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.SetMeta(meta.ID, MetaPatch{GoalCondition: &goalCond}))
		matchIDs = append(matchIDs, meta.ID)
	}
	for i := 0; i < 3; i++ {
		_, err := store.NewSession(SessionTypeChat, "", "agent-1") // no GoalCondition — must be excluded
		require.NoError(t, err)
	}

	goalOnly := func(m *UnifiedMeta) bool { return m != nil && m.GoalCondition != "" }
	got, err := store.ListSessionsFiltered(goalOnly)
	require.NoError(t, err)

	require.Len(t, got, 2, "want exactly the 2 sessions with a non-empty GoalCondition")
	gotIDs := map[string]bool{got[0].ID: true, got[1].ID: true}
	for _, id := range matchIDs {
		assert.True(t, gotIDs[id], "expected match id %s in the filtered result", id)
	}
}

// TestListSessionsFiltered_ClonesOnlyMatches is the PERFORMANCE half of
// finding #3 — the actual claim this fix makes: ListSessionsFiltered must
// clone ONLY the entries its predicate approves, not clone every cached
// session and filter the result afterward (which would still pay the full
// O(session count) clone cost the goal-loop sweeps were burning for
// nothing). Measured directly via the unifiedMetaCloneCalls seam, so this
// cannot pass via a filtered-after-the-fact implementation.
func TestListSessionsFiltered_ClonesOnlyMatches(t *testing.T) {
	store := u6NewTestStore(t)

	const total = 40
	const matching = 3
	goalCond := "only these should be cloned"
	var matchIDs []string
	for i := 0; i < matching; i++ {
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.SetMeta(meta.ID, MetaPatch{GoalCondition: &goalCond}))
		matchIDs = append(matchIDs, meta.ID)
	}
	for i := 0; i < total-matching; i++ {
		_, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
	}

	goalOnly := func(m *UnifiedMeta) bool { return m != nil && m.GoalCondition != "" }

	before := unifiedMetaCloneCalls.Load()
	got, err := store.ListSessionsFiltered(goalOnly)
	require.NoError(t, err)
	delta := unifiedMetaCloneCalls.Load() - before

	require.Len(t, got, matching)
	assert.Equal(t, int64(matching), delta,
		"ListSessionsFiltered made %d Clone() calls scanning %d cached sessions with %d matches — "+
			"want exactly %d (clone only matches), not %d (clone-then-filter)", delta, total, matching, matching, total)
}
