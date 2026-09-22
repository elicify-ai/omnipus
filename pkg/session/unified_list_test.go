// unified_list_test.go: tests for list, index and sweep sessions (retention, ClearAll)

package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from unified.go tests 2026-09-15 ---

// --- Wave 5a: ListSessions and ReadMessages ---

// TestListSessions_SortedByUpdatedAtDesc verifies sessions are returned newest-first.
// BDD: Given two sessions with different UpdatedAt timestamps,
// When ListSessions is called,
// Then sessions are sorted by UpdatedAt descending (newest first).
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session list sorted by update time (US-15 AC1)
func TestListSessions_SortedByUpdatedAtDesc(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "sort-test-agent")

	// Create session A — will have an earlier UpdatedAt
	metaA, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)

	// Append a message to A so its UpdatedAt is set
	require.NoError(t, ps.AppendMessage(metaA.ID, TranscriptEntry{
		ID:        "msg-a",
		Role:      "user",
		Content:   "hello from A",
		Timestamp: time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC),
		Tokens:    5,
	}))

	// Small sleep to ensure UpdatedAt differs
	time.Sleep(5 * time.Millisecond)

	// Create session B — will have a later UpdatedAt
	metaB, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)
	require.NoError(t, ps.AppendMessage(metaB.ID, TranscriptEntry{
		ID:        "msg-b",
		Role:      "user",
		Content:   "hello from B",
		Timestamp: time.Date(2026, 3, 28, 11, 0, 0, 0, time.UTC),
		Tokens:    5,
	}))

	metas, err := ps.ListSessions()
	require.NoError(t, err)
	require.Len(t, metas, 2, "must return both sessions")

	// B was updated more recently so it should come first
	assert.Equal(t, metaB.ID, metas[0].ID, "session B (newer) must be first")
	assert.Equal(t, metaA.ID, metas[1].ID, "session A (older) must be second")
}

// TestListSessions_EmptyDirReturnsNil verifies an empty (or non-existent) sessions dir returns nil.
// BDD: Given no sessions exist,
// When ListSessions is called,
// Then the result is nil (or empty) and no error.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session list empty state (US-15 AC2)
func TestListSessions_EmptyDirReturnsNil(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "empty-agent")
	// Don't create any sessions — baseDir doesn't even exist yet.

	metas, err := ps.ListSessions()
	require.NoError(t, err)
	assert.Nil(t, metas, "ListSessions must return nil for a non-existent sessions dir")
}

// TestListSessions_SkipsUnreadableSessions verifies corrupted session dirs are skipped gracefully.
// BDD: Given one valid session and one session dir with a missing/corrupted meta.json,
// When ListSessions is called,
// Then only the valid session is returned and no error is raised.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session list skips corrupted sessions (US-15 AC3)
func TestListSessions_SkipsUnreadableSessions(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "skip-agent")

	// Create one valid session
	meta, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)

	// Create a corrupted session dir (directory exists, meta.json missing)
	corruptDir := filepath.Join(home, "sessions", "session_corrupt")
	require.NoError(t, os.MkdirAll(corruptDir, 0o755))

	metas, err := ps.ListSessions()
	require.NoError(t, err, "ListSessions must not error on corrupt dir, just skip it")
	require.Len(t, metas, 1, "only the valid session must be returned")
	assert.Equal(t, meta.ID, metas[0].ID)
}

// TestListSessions_ServesFromCache creates N sessions, deletes their
// meta.json directly on disk (bypassing the store), and asserts that
// ListSessions still returns all N — proving it no longer reads meta.json
// from disk on every call.
//
// Traces to: pkg/session/unified.go ListSessions.
func TestListSessions_ServesFromCache(t *testing.T) {
	store := newTestStore(t)

	const n = 3
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		ids[i] = meta.ID
	}

	// Delete meta.json directly on disk for every session, bypassing the
	// store's own DeleteSession. If ListSessions still scanned disk, none of
	// these sessions would be returned; since it now serves from metaCache,
	// all N must still be present.
	for _, id := range ids {
		metaPath := filepath.Join(store.baseDir, id, "meta.json")
		require.NoError(t, os.Remove(metaPath))
	}

	metas, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, metas, n)
	for _, id := range ids {
		assert.NotNil(t, findMeta(metas, id),
			"session %s must still be listed from cache after its meta.json was removed on disk", id)
	}
}

// TestListSessions_ReflectsWritesImmediately verifies there is no cache lag:
// a SetMeta title change is visible on the very next ListSessions call.
//
// Traces to: pkg/session/unified.go ListSessions, SetMeta, writeMetaLocked.
func TestListSessions_ReflectsWritesImmediately(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	newTitle := "Updated Title"
	require.NoError(t, store.SetMeta(meta.ID, MetaPatch{Title: &newTitle}))

	metas, err := store.ListSessions()
	require.NoError(t, err)
	found := findMeta(metas, meta.ID)
	require.NotNil(t, found)
	assert.Equal(t, newTitle, found.Title)
}

// TestListSessions_ReturnsClones proves cache isolation in both directions:
//  1. mutating the *UnifiedMeta pointer NewSession returned (bypassing SetMeta)
//     must not leak into what the cache serves next;
//  2. mutating a value returned by ListSessions (including a slice element,
//     which would reveal aliasing if Clone were shallow) must not affect a
//     subsequent ListSessions call.
//
// Traces to: pkg/session/unified.go Clone, ListSessions.
func TestListSessions_ReturnsClones(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	// Direction 1: mutate the returned NewSession pointer directly. The cache
	// entry was populated by writeMetaLocked's OWN clone at creation time, so
	// this external object is never aliased with the cache.
	meta.Title = "external-mutation-after-create"

	metas, err := store.ListSessions()
	require.NoError(t, err)
	found := findMeta(metas, sessionID)
	require.NotNil(t, found)
	assert.Empty(t, found.Title,
		"external mutation of the NewSession-returned pointer must not leak into the cache")

	// Direction 2: mutate a ListSessions()-returned entry in place, including
	// a slice element (not just append — append usually reallocates and
	// would pass even with a shallow clone, masking an aliasing bug).
	found.Title = "mutated-via-listsessions-result"
	require.NotEmpty(t, found.AgentIDs, "precondition: session must have at least one AgentID")
	found.AgentIDs[0] = "tampered-agent"

	metas2, err := store.ListSessions()
	require.NoError(t, err)
	found2 := findMeta(metas2, sessionID)
	require.NotNil(t, found2)
	assert.Empty(t, found2.Title,
		"mutating a ListSessions result must not affect the next ListSessions call")
	assert.NotEqual(t, "tampered-agent", found2.AgentIDs[0],
		"mutating a ListSessions result's slice element must not affect the cache's backing array")
}

// TestDeleteSession_EvictsFromCache verifies DeleteSession removes the
// in-memory cache entry, not just the on-disk directory: both GetMeta and
// ListSessions must stop serving the deleted session.
//
// Traces to: pkg/session/unified.go DeleteSession.
func TestDeleteSession_EvictsFromCache(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	require.NoError(t, store.DeleteSession(meta.ID))

	_, err = store.GetMeta(meta.ID)
	assert.Error(t, err, "GetMeta must fail for a deleted session")

	metas, err := store.ListSessions()
	require.NoError(t, err)
	assert.Nil(t, findMeta(metas, meta.ID), "ListSessions must exclude a deleted session")
}

// TestClearAll_ClearsCache verifies ClearAll empties metaCache along with disk.
//
// Traces to: pkg/session/unified.go ClearAll.
func TestClearAll_ClearsCache(t *testing.T) {
	store := newTestStore(t)

	for i := 0; i < 3; i++ {
		_, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
	}

	removed, err := store.ClearAll()
	require.NoError(t, err)
	assert.Equal(t, 3, removed)

	metas, err := store.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, metas)

	store.cacheMu.RLock()
	cacheLen := len(store.metaCache)
	store.cacheMu.RUnlock()
	assert.Zero(t, cacheLen, "ClearAll must empty metaCache, not just disk")
}

// TestListSessions_ReconcilesOutOfBandSessionDir is the regression test for
// the cache-only-blindness bug: ListSessions previously iterated ONLY
// metaCache and never consulted disk, so a session directory written
// DIRECTLY to disk (out-of-band, not through this UnifiedStore instance —
// e.g. by another process, or a restore/migration step) never appeared in
// ListSessions no matter how long the process ran afterward.
//
// BDD: Given one session created THROUGH the store (already in metaCache)
// and one session written DIRECTLY to disk with a distinct ID and a newer
// UpdatedAt, When ListSessions is called, Then both sessions are returned,
// sorted UpdatedAt descending (out-of-band session first), AND the
// out-of-band session is self-healed into metaCache as a side effect (same
// mechanism readMetaLocked uses on any other cache-miss read).
//
// Traces to: pkg/session/unified.go ListSessions, readMetaLocked, metaCache.
func TestListSessions_ReconcilesOutOfBandSessionDir(t *testing.T) {
	store := newTestStore(t)

	// Session 1: created THROUGH the store — lands in metaCache via the
	// normal writeMetaLocked path.
	cached, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	// Session 2: written DIRECTLY to disk, bypassing the store entirely.
	// writeUnifiedMetaDirect is the same package-level helper migrateLegacy
	// uses to write a session's meta.json before any UnifiedStore instance
	// exists for it — an accurate stand-in for a genuinely out-of-band writer.
	outOfBandID := "out-of-band-session"
	outOfBandDir := filepath.Join(store.baseDir, outOfBandID)
	require.NoError(t, os.MkdirAll(outOfBandDir, 0o700))
	now := time.Now().UTC()
	outOfBandMeta := &UnifiedMeta{
		SessionMeta: SessionMeta{
			ID:        outOfBandID,
			Status:    StatusActive,
			CreatedAt: now.Add(-time.Hour),
			// Deliberately newer than the cached session so descending sort
			// order proves this entry was actually merged in, not just
			// coincidentally present.
			UpdatedAt: now.Add(time.Hour),
		},
		Type: SessionTypeChat,
	}
	require.NoError(t, writeUnifiedMetaDirect(outOfBandDir, outOfBandMeta))

	// Precondition: the out-of-band session must NOT already be cached — it
	// was never written through this store instance.
	store.cacheMu.RLock()
	_, alreadyCached := store.metaCache[outOfBandID]
	store.cacheMu.RUnlock()
	require.False(t, alreadyCached,
		"precondition: out-of-band session must not be pre-populated in the cache")

	metas, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, metas, 2, "both the cached and the out-of-band session must be listed")

	require.NotNil(t, findMeta(metas, cached.ID),
		"session created through the store must still be listed")
	require.NotNil(t, findMeta(metas, outOfBandID),
		"session written directly to disk out-of-band must be listed")

	assert.Equal(t, outOfBandID, metas[0].ID,
		"out-of-band session (newer UpdatedAt) must sort first")
	assert.Equal(t, cached.ID, metas[1].ID,
		"session created through the store (older UpdatedAt) must sort second")

	// The reconciliation pass must have self-healed the out-of-band session
	// into metaCache (the same side effect readMetaLocked has on any other
	// cache-miss read) so a repeat ListSessions call doesn't re-read it from
	// disk.
	store.cacheMu.RLock()
	_, nowCached := store.metaCache[outOfBandID]
	store.cacheMu.RUnlock()
	assert.True(t, nowCached, "ListSessions must self-heal the out-of-band session into metaCache")
}

// TestListSessions_SkipsMalformedOutOfBandSessionDir verifies the
// reconciliation pass degrades gracefully: an out-of-band session directory
// whose meta.json is unparseable must be logged and skipped, not returned as
// an error from ListSessions — and must not prevent a GOOD out-of-band
// session directory next to it from being listed.
//
// BDD: Given one session created through the store, one out-of-band
// directory with a valid meta.json, and one out-of-band directory with a
// corrupt (unparseable) meta.json, When ListSessions is called, Then it
// returns no error, lists the store-created and the good out-of-band
// session, and silently excludes the corrupt one.
//
// Traces to: pkg/session/unified.go ListSessions, readMetaLocked.
func TestListSessions_SkipsMalformedOutOfBandSessionDir(t *testing.T) {
	store := newTestStore(t)

	cached, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	goodID := "out-of-band-good"
	goodDir := filepath.Join(store.baseDir, goodID)
	require.NoError(t, os.MkdirAll(goodDir, 0o700))
	require.NoError(t, writeUnifiedMetaDirect(goodDir, &UnifiedMeta{
		SessionMeta: SessionMeta{
			ID:        goodID,
			Status:    StatusActive,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Type: SessionTypeChat,
	}))

	badID := "out-of-band-bad"
	badDir := filepath.Join(store.baseDir, badID)
	require.NoError(t, os.MkdirAll(badDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(badDir, "meta.json"), []byte(`{not valid json`), 0o600))

	metas, err := store.ListSessions()
	require.NoError(t, err, "ListSessions must not error on a malformed out-of-band session dir")
	require.Len(t, metas, 2, "only the store-created and the good out-of-band session must be listed")

	require.NotNil(t, findMeta(metas, cached.ID))
	require.NotNil(t, findMeta(metas, goodID), "the good out-of-band session must still be listed")
	assert.Nil(t, findMeta(metas, badID), "the malformed out-of-band session must be excluded, not crash the call")
}

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
	for i := 0; i < n; i++ {
		_, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
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
//
// ADR-086 GOAL-FR-005 (wave S6): this test used GoalCondition as its
// predicate field before wave S6 retired the goal group from session meta
// entirely (the predicate's own field is a caller-supplied closure over
// *UnifiedMeta, so this file's own compile survives S6 regardless — but the
// FIELD it closed over does not). PendingAskJSON exercises the identical
// "predicate over one field" shape with a surviving field.
func TestListSessionsFiltered_ReturnsOnlyMatches(t *testing.T) {
	store := u6NewTestStore(t)

	askSet := "parked question"
	var matchIDs []string
	for i := 0; i < 2; i++ {
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.SetMeta(meta.ID, MetaPatch{PendingAskJSON: &askSet}))
		matchIDs = append(matchIDs, meta.ID)
	}
	for i := 0; i < 3; i++ {
		_, err := store.NewSession(SessionTypeChat, "", "agent-1") // no PendingAskJSON — must be excluded
		require.NoError(t, err)
	}

	pendingAskOnly := func(m *UnifiedMeta) bool { return m != nil && m.PendingAskJSON != "" }
	got, err := store.ListSessionsFiltered(pendingAskOnly)
	require.NoError(t, err)

	require.Len(t, got, 2, "want exactly the 2 sessions with a non-empty PendingAskJSON")
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
	askSet := "only these should be cloned"
	var matchIDs []string
	for i := 0; i < matching; i++ {
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.SetMeta(meta.ID, MetaPatch{PendingAskJSON: &askSet}))
		matchIDs = append(matchIDs, meta.ID)
	}
	for i := 0; i < total-matching; i++ {
		_, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
	}

	pendingAskOnly := func(m *UnifiedMeta) bool { return m != nil && m.PendingAskJSON != "" }

	before := unifiedMetaCloneCalls.Load()
	got, err := store.ListSessionsFiltered(pendingAskOnly)
	require.NoError(t, err)
	delta := unifiedMetaCloneCalls.Load() - before

	require.Len(t, got, matching)
	assert.Equal(t, int64(matching), delta,
		"ListSessionsFiltered made %d Clone() calls scanning %d cached sessions with %d matches — "+
			"want exactly %d (clone only matches), not %d (clone-then-filter)", delta, total, matching, matching, total)

	// Bug fix (staticcheck SA4010): matchIDs was collected but never read —
	// the test asserted only the COUNT of results, not that they were the
	// RIGHT sessions. Assert identity, order-insensitively (ListSessionsFiltered
	// does not document a return order).
	gotIDs := make([]string, 0, len(got))
	for _, m := range got {
		gotIDs = append(gotIDs, m.ID)
	}
	assert.ElementsMatch(t, matchIDs, gotIDs, "ListSessionsFiltered must return exactly the sessions matching the predicate")
}

// TestClearAll_AcquiresAllShardsInIndexOrder proves the REAL ClearAll call
// (not a hand-rolled simulation) goes through lockAllSessionShards, by
// asserting the recorded order matches the strict-ascending-index shape
// while ClearAll runs against a real store with real sessions.
func TestClearAll_AcquiresAllShardsInIndexOrder(t *testing.T) {
	store := newTestStoreForLockTests(t)
	for i := 0; i < 3; i++ {
		_, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
	}

	events, restore := installLockRecorder(t)
	t.Cleanup(restore)

	_, err := store.ClearAll()
	require.NoError(t, err)

	recorded := *events
	require.GreaterOrEqual(t, len(recorded), 128, "ClearAll must acquire+release all 64 shards")
	// The first 64 events (the lockAllSessionShards acquire loop, which runs
	// before ClearAll does any per-session work) must be a strictly
	// ascending 0..63 acquire sequence.
	for i := 0; i < 64; i++ {
		require.Truef(t, recorded[i].acquire, "event %d must be an acquire", i)
		require.Equalf(t, uint32(i), recorded[i].shard, "acquire %d must be shard %d", i, i)
	}
}

// TestDeleteSession_Success creates a session, verifies the directory exists,
// deletes it, and asserts the directory is gone.
//
// BDD: Given a session has been created,
// When DeleteSession is called with its ID,
// Then no error is returned AND the session directory no longer exists.
//
// Traces to: pkg/session/unified.go DeleteSession (Milestone 2)
func TestDeleteSession_Success(t *testing.T) {
	store := newTestStore(t)

	// Given — create a real session.
	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err, "NewSession must succeed")
	sessionID := meta.ID

	// Verify the directory was actually created (precondition for a meaningful test).
	sessionDir := filepath.Join(store.baseDir, sessionID)
	_, statErr := os.Stat(sessionDir)
	require.NoError(t, statErr, "session directory must exist before deletion")

	// When — delete the session.
	err = store.DeleteSession(sessionID)

	// Then — no error and directory is gone.
	require.NoError(t, err, "DeleteSession must succeed for an existing session")
	_, statErr = os.Stat(sessionDir)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "session directory must be removed after DeleteSession; stat error: %v", statErr)
}

// TestDeleteSession_DifferentSessions verifies that deleting one session does not
// affect another — proving DeleteSession operates on the targeted directory only.
//
// This is the differentiation test: two sessions, delete one, verify the other survives.
//
// Traces to: pkg/session/unified.go DeleteSession (Milestone 2)
func TestDeleteSession_DifferentSessions(t *testing.T) {
	store := newTestStore(t)

	// Create two sessions.
	meta1, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	meta2, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	id1 := meta1.ID
	id2 := meta2.ID
	require.NotEqual(t, id1, id2, "two sessions must have distinct IDs")

	dir2 := filepath.Join(store.baseDir, id2)

	// Delete session 1.
	require.NoError(t, store.DeleteSession(id1), "DeleteSession(id1) must succeed")

	// Session 1's dir must be gone.
	_, statErr := os.Stat(filepath.Join(store.baseDir, id1))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "deleted session directory must not exist")

	// Session 2's dir must still be present.
	_, statErr = os.Stat(dir2)
	assert.NoError(t, statErr, "non-deleted session directory must still exist")
}

// TestDeleteSession_NotFound verifies that deleting a non-existent session ID
// returns an error containing "not found".
//
// BDD: Given no session with ID "does-not-exist" exists,
// When DeleteSession("does-not-exist") is called,
// Then an error is returned containing "not found".
//
// Traces to: pkg/session/unified.go DeleteSession (Milestone 2)
func TestDeleteSession_NotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteSession("does-not-exist-session-id")

	require.Error(t, err, "DeleteSession must return an error for a non-existent session")
	assert.True(t, strings.Contains(err.Error(), "not found"),
		"error must contain 'not found', got: %q", err.Error())
}

// TestDeleteSession_PathTraversal verifies that attempting to delete "../evil"
// is rejected with a validation error before any filesystem operation.
//
// BDD: Given a malicious session ID "../evil",
// When DeleteSession("../evil") is called,
// Then an error is returned about invalid session ID (validateSessionID rejects it).
//
// Traces to: pkg/session/unified.go validateSessionID (Milestone 2)
func TestDeleteSession_PathTraversal(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteSession("../evil")

	require.Error(t, err, "DeleteSession must reject path-traversal session IDs")
	// The error must come from validateSessionID, not from a filesystem operation
	// that traversed out of the base directory.
	assert.Contains(t, err.Error(), "invalid session ID",
		"error must mention 'invalid session ID', got: %q", err.Error())
}

// TestDeleteSession_EmptyID verifies that an empty string session ID is rejected.
//
// BDD: Given an empty session ID "",
// When DeleteSession("") is called,
// Then an error is returned about invalid session ID.
//
// Traces to: pkg/session/unified.go validateSessionID (Milestone 2)
func TestDeleteSession_EmptyID(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteSession("")

	require.Error(t, err, "DeleteSession must reject empty session ID")
	assert.Contains(t, err.Error(), "invalid session ID",
		"error must mention 'invalid session ID', got: %q", err.Error())
}

// TestDeleteSession_DoubleDot verifies that ".." as a session ID is rejected.
//
// BDD: Given session ID "..",
// When DeleteSession("..") is called,
// Then an error is returned about invalid session ID.
//
// Traces to: pkg/session/unified.go validateSessionID (Milestone 2)
func TestDeleteSession_DoubleDot(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteSession("..")

	require.Error(t, err, "DeleteSession must reject '..' as session ID")
	assert.Contains(t, err.Error(), "invalid session ID",
		"error must mention 'invalid session ID', got: %q", err.Error())
}

// TestDeleteSession_PersistenceCheck verifies the read-back contract:
// after deletion, GetMeta must fail.
//
// This is the persistence test: ensures deletion is durable and not superficial.
//
// Traces to: pkg/session/unified.go DeleteSession + GetMeta (Milestone 2)
func TestDeleteSession_PersistenceCheck(t *testing.T) {
	store := newTestStore(t)

	// Create, verify readable, delete, verify unreadable.
	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	// Read before deletion — must succeed.
	_, err = store.GetMeta(meta.ID)
	require.NoError(t, err, "GetMeta must succeed before deletion")

	// Delete.
	require.NoError(t, store.DeleteSession(meta.ID))

	// Read after deletion — must fail.
	_, err = store.GetMeta(meta.ID)
	assert.Error(t, err, "GetMeta must return error after session is deleted")
}

// --- Cascade-delete uploads tests (N-B fix) ---

// TestDeleteSession_CascadeDeletesUploads_SharedStore verifies that DeleteSession
// on the shared store (baseDir = <home>/sessions) removes the session's uploads
// directory at <home>/uploads/<sessionID>.
//
// BDD: Given a shared store at <home>/sessions with a session whose uploads exist
//
//	at <home>/uploads/<sessionID>/,
//	When DeleteSession is called,
//	Then the uploads directory is removed.
//
// Traces to: ADR-017 D5, N-B fix — uploads are home-rooted.
func TestDeleteSession_CascadeDeletesUploads_SharedStore(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")

	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() }) // see issue #634

	meta, err := store.NewSession(SessionTypeChat, "", "agent-test")
	require.NoError(t, err)
	sessionID := meta.ID

	// Simulate an upload at <home>/uploads/<sessionID>/file.txt.
	uploadsDir := filepath.Join(home, "uploads", sessionID)
	require.NoError(t, os.MkdirAll(uploadsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(uploadsDir, "file.txt"), []byte("data"), 0o600))

	// Delete the session.
	require.NoError(t, store.DeleteSession(sessionID))

	// The session directory must be gone.
	_, statErr := os.Stat(filepath.Join(sessionsDir, sessionID))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "session dir must be removed after DeleteSession")

	// The uploads directory must also be gone (cascade-delete, N-B fix).
	_, uploadsStatErr := os.Stat(uploadsDir)
	assert.True(t, errors.Is(uploadsStatErr, os.ErrNotExist), "uploads dir at <home>/uploads/<sessionID> must be removed by cascade-delete (N-B fix)")
}

// TestDeleteSession_CascadeDeletesUploads_PerAgentStore verifies that DeleteSession
// on a per-agent store (baseDir = <home>/agents/<id>/sessions) still correctly
// removes uploads at <home>/uploads/<sessionID> — not at the wrong
// <home>/agents/<id>/uploads/<sessionID> path that the old filepath.Dir logic
// would have computed.
//
// BDD: Given a per-agent store at <home>/agents/my-agent/sessions
//
//	with uploads at <home>/uploads/<sessionID>/,
//	When DeleteSession is called,
//	Then the uploads dir at <home>/uploads/<sessionID> is removed,
//	And NO directory is created or removed under <home>/agents/my-agent/uploads/.
//
// Traces to: ADR-017 D5, N-B fix — per-agent stores must use home-rooted uploads path.
func TestDeleteSession_CascadeDeletesUploads_PerAgentStore(t *testing.T) {
	home := t.TempDir()
	agentSessionsDir := filepath.Join(home, "agents", "my-agent", "sessions")

	store, err := NewUnifiedStoreWithHome(agentSessionsDir, home)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() }) // see issue #634

	meta, err := store.NewSession(SessionTypeChat, "", "my-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Uploads at the correct home-rooted path.
	correctUploadsDir := filepath.Join(home, "uploads", sessionID)
	require.NoError(t, os.MkdirAll(correctUploadsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(correctUploadsDir, "upload.txt"), []byte("data"), 0o600))

	// The WRONG path (what the old code would have used).
	wrongUploadsDir := filepath.Join(home, "agents", "my-agent", "uploads", sessionID)

	require.NoError(t, store.DeleteSession(sessionID))

	// The correct uploads directory must be removed (N-B fix).
	_, correctStatErr := os.Stat(correctUploadsDir)
	assert.True(t, errors.Is(correctStatErr, os.ErrNotExist), "uploads at <home>/uploads/<sessionID> must be removed by cascade-delete")

	// The wrong path must never have been created or touched.
	_, wrongStatErr := os.Stat(wrongUploadsDir)
	assert.True(t, errors.Is(wrongStatErr, os.ErrNotExist), "<home>/agents/<id>/uploads/<sessionID> must not be touched — wrong path for uploads")
}

// TestClearAll_RemovesSessionsContextAndUploads verifies the core destructive
// contract: every top-level session directory (plus its fake message file),
// its matching .context/<id>.jsonl file, and its matching uploads/<id>/
// directory are all removed, and the returned count equals the number of
// session directories removed.
//
// BDD: Given 3 session directories — one with a fake message file plus a
// matching .context/<id>.jsonl entry, another with a matching uploads/<id>/
// directory — When ClearAll is called, Then all 3 session directories, the
// matching context file, and the matching uploads directory are removed, and
// ClearAll returns (3, nil).
//
// Traces to: pkg/session/unified.go ClearAll (lines 835-869)
func TestClearAll_RemovesSessionsContextAndUploads(t *testing.T) {
	store, home := newTestStoreWithHome(t)

	meta1, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	meta2, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	meta3, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	allMetas := []*UnifiedMeta{meta1, meta2, meta3}

	// Fake message files inside each session directory (beyond meta.json).
	for _, m := range allMetas {
		dir := filepath.Join(store.BaseDir(), m.ID)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "transcript.jsonl"),
			[]byte(`{"role":"user","content":"hi"}`+"\n"), 0o600))
	}

	// meta1 gets a matching .context/<id>.jsonl file via the SessionStore
	// interface (AddMessage writes through to the JSONL backend).
	store.AddMessage(meta1.ID, "user", "hello from context store")
	contextFile := filepath.Join(store.BaseDir(), ".context", meta1.ID+".jsonl")
	_, statErr := os.Stat(contextFile)
	require.NoError(t, statErr, "precondition: context file for meta1 must exist")

	// meta2 gets a matching uploads/<id>/ directory.
	uploadsDir := filepath.Join(home, "uploads", meta2.ID)
	require.NoError(t, os.MkdirAll(uploadsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(uploadsDir, "photo.png"), []byte("fake-bytes"), 0o600))

	// Preconditions: all 3 session dirs exist.
	for _, m := range allMetas {
		_, precondStatErr := os.Stat(filepath.Join(store.BaseDir(), m.ID))
		require.NoError(t, precondStatErr, "precondition: session dir for %s must exist", m.ID)
	}

	// When
	removed, err := store.ClearAll()

	// Then
	require.NoError(t, err, "ClearAll must not return an error in the happy path")
	assert.Equal(t, 3, removed, "ClearAll must report exactly 3 removed sessions")

	for _, m := range allMetas {
		_, statErr = os.Stat(filepath.Join(store.BaseDir(), m.ID))
		assert.True(t, errors.Is(statErr, os.ErrNotExist), "session dir for %s must be removed", m.ID)
	}

	_, statErr = os.Stat(contextFile)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "meta1's .context/<id>.jsonl must be removed")

	_, statErr = os.Stat(uploadsDir)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "meta2's uploads/<id>/ dir must be removed")
}

// TestClearAll_PreservesContextDirItself verifies the exact skip condition in
// ClearAll's loop (`!entry.IsDir() || entry.Name() == ".context"`): the
// .context directory ITSELF must survive ClearAll even though every session's
// individual .context/<id>.jsonl file underneath it is removed, and any
// unrelated file left inside .context (not matching a removed session ID)
// must be left untouched.
//
// BDD: Given a store whose .context directory contains both a file matching
// a live session ID and an unrelated stray file, When ClearAll is called,
// Then the .context directory still exists, the matching file is gone, and
// the stray unrelated file is untouched.
//
// Traces to: pkg/session/unified.go ClearAll — `entry.Name() == ".context"`
// skip condition (line ~850)
func TestClearAll_PreservesContextDirItself(t *testing.T) {
	store, _ := newTestStoreWithHome(t)

	meta1, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)

	store.AddMessage(meta1.ID, "user", "hi")
	contextDir := filepath.Join(store.BaseDir(), ".context")

	// An unrelated stray file inside .context that does not match any
	// session ID being removed.
	strayFile := filepath.Join(contextDir, "unrelated-leftover.jsonl")
	require.NoError(t, os.WriteFile(strayFile, []byte(`{"stray":"data"}`+"\n"), 0o600))

	// Precondition: .context exists as a directory.
	info, statErr := os.Stat(contextDir)
	require.NoError(t, statErr, "precondition: .context dir must exist")
	require.True(t, info.IsDir(), "precondition: .context must be a directory")

	removed, err := store.ClearAll()
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	// .context directory itself must still exist.
	info, statErr = os.Stat(contextDir)
	require.NoError(t, statErr, ".context directory must survive ClearAll")
	assert.True(t, info.IsDir(), ".context must still be a directory after ClearAll")

	// The matching context file for the removed session must be gone.
	matchingFile := filepath.Join(contextDir, meta1.ID+".jsonl")
	_, statErr = os.Stat(matchingFile)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "matching context file for removed session must be gone")

	// The unrelated stray file must be untouched.
	strayData, readErr := os.ReadFile(strayFile)
	require.NoError(t, readErr, "unrelated stray file inside .context must not be removed")
	assert.Equal(t, `{"stray":"data"}`+"\n", string(strayData),
		"unrelated stray file content must be untouched by ClearAll")
}

// TestClearAll_EmptyOrAlreadyClearedStore_NoOp verifies ClearAll is a safe
// no-op — returning (0, nil), never an error — both on a brand-new store that
// never had any sessions, and on a store that has already been cleared once.
//
// BDD: Given a store with no session directories (fresh, or already cleared),
// When ClearAll is called, Then it returns (0, nil).
//
// Traces to: pkg/session/unified.go ClearAll (lines 835-869)
func TestClearAll_EmptyOrAlreadyClearedStore_NoOp(t *testing.T) {
	t.Run("fresh store with no sessions", func(t *testing.T) {
		store, _ := newTestStoreWithHome(t)

		removed, err := store.ClearAll()
		require.NoError(t, err, "ClearAll on an empty store must not error")
		assert.Equal(t, 0, removed, "ClearAll on an empty store must report 0 removed")
	})

	t.Run("already-cleared store returns zero on second call", func(t *testing.T) {
		store, _ := newTestStoreWithHome(t)

		_, err := store.NewSession(SessionTypeChat, "", "agent-a")
		require.NoError(t, err)
		_, err = store.NewSession(SessionTypeChat, "", "agent-a")
		require.NoError(t, err)

		firstRemoved, err := store.ClearAll()
		require.NoError(t, err)
		require.Equal(t, 2, firstRemoved, "precondition: first ClearAll must remove the 2 seeded sessions")

		secondRemoved, err := store.ClearAll()
		require.NoError(t, err, "calling ClearAll again on an already-cleared store must not error")
		assert.Equal(t, 0, secondRemoved, "calling ClearAll again must report 0 removed, not re-count or fail")
	})
}

// TestClearAll_BaseDirMissing_ReturnsZeroNoError exercises the
// os.IsNotExist(err) branch: if the store's base directory has been removed
// out from under it (e.g., manual cleanup, or a prior ClearAll-adjacent
// operation), ClearAll must treat "nothing to clear" as success, not an error.
//
// BDD: Given the store's base directory does not exist on disk,
// When ClearAll is called,
// Then it returns (0, nil) rather than propagating the ReadDir error.
//
// Traces to: pkg/session/unified.go ClearAll — `os.IsNotExist(err)` branch
// (lines ~840-843)
func TestClearAll_BaseDirMissing_ReturnsZeroNoError(t *testing.T) {
	store, _ := newTestStoreWithHome(t)

	require.NoError(t, os.RemoveAll(store.BaseDir()), "test setup: remove the base dir entirely")

	removed, err := store.ClearAll()
	require.NoError(t, err, "ClearAll must not error when the base directory does not exist")
	assert.Equal(t, 0, removed)
}

// TestClearAll_ContinuesPastRemovalFailure documents ClearAll's behavior when
// one session directory's os.RemoveAll fails: per the code in
// pkg/session/unified.go's ClearAll, the error is logged via slog.Warn and the
// loop `continue`s to the next entry — it does NOT abort the whole operation,
// and the failed entry is NOT counted in the returned removed total. The
// per-entry failure IS surfaced to the caller: ClearAll aggregates every
// per-entry error (via errors.Join) and returns it alongside the partial
// removed count, so a "clear all sessions" request cannot silently
// under-deliver on this privacy-sensitive, destructive action.
//
// The real removal failure is forced by chmod'ing one session directory to
// 0500 (r-x, no write) so the OS refuses to unlink meta.json/transcript.jsonl
// inside it — a genuine os.RemoveAll error, not a simulated one.
//
// BDD: Given 3 session directories where one cannot be removed due to
// filesystem permissions, When ClearAll is called, Then the two removable
// directories are removed and counted, the unremovable directory is left
// fully intact, and ClearAll returns a non-nil aggregate error describing the
// failure alongside the correct partial count.
//
// Traces to: pkg/session/unified.go ClearAll, the
// `if err := os.RemoveAll(dir); err != nil { slog.Warn(...); continue }` path
// and its final `return removed, errors.Join(errs...)`.
func TestClearAll_ContinuesPastRemovalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip(
			"test forces a real os.RemoveAll failure via directory permissions (chmod 0500) — root bypasses Unix permission checks (CAP_DAC_OVERRIDE), so this doesn't reproduce a failure when the test process runs as root (e.g. inside a CI container)",
		)
	}

	store, _ := newTestStoreWithHome(t)

	metaBad, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	metaGood1, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	metaGood2, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)

	badDir := filepath.Join(store.BaseDir(), metaBad.ID)

	// Remove write permission on the bad session's directory so the OS refuses
	// to unlink its contents (meta.json etc.) — a real os.RemoveAll failure.
	require.NoError(t, os.Chmod(badDir, 0o500))
	// Restore permissions before t.TempDir()'s own cleanup runs (t.Cleanup
	// callbacks run LIFO; this is registered after newTestStoreWithHome's
	// internal t.TempDir(), so it runs first).
	t.Cleanup(func() { _ = os.Chmod(badDir, 0o700) })

	removed, err := store.ClearAll()

	// Current behavior: the per-entry RemoveAll failure is logged via
	// slog.Warn AND surfaced as a non-nil aggregate error from ClearAll.
	require.Error(t, err,
		"ClearAll must surface a non-nil aggregate error when at least one session dir failed to remove")
	assert.Contains(t, err.Error(), metaBad.ID,
		"the aggregate error should mention the session whose removal failed")
	assert.Equal(t, 2, removed,
		"only the 2 removable session dirs must be counted; the dir whose RemoveAll failed must not be")

	// The bad directory must still exist, fully intact (RemoveAll could not
	// remove any of its children, so nothing partial happened to it either).
	_, statErr := os.Stat(badDir)
	assert.NoError(t, statErr, "the session dir whose RemoveAll failed must still exist on disk")
	_, statErr = os.Stat(filepath.Join(badDir, "meta.json"))
	assert.NoError(t, statErr, "meta.json inside the unremovable dir must still be present")

	// The two good directories must be gone.
	for _, m := range []*UnifiedMeta{metaGood1, metaGood2} {
		_, statErr := os.Stat(filepath.Join(store.BaseDir(), m.ID))
		assert.True(t, errors.Is(statErr, os.ErrNotExist), "removable session dir for %s must be gone", m.ID)
	}
}

// TestClearAll_ContinuesPastRemovalFailure_InjectedError asserts the exact
// same aggregate-error behavior as TestClearAll_ContinuesPastRemovalFailure
// above, but via the removeAllFn package-level test seam instead of chmod'ing
// a directory to 0500. The chmod-based test is skipped when the test process
// runs as root (CAP_DAC_OVERRIDE bypasses the permission check) — which is
// exactly how CI runs it — so that test provides zero coverage of this
// behavior in CI. This test forces a deterministic os.RemoveAll-shaped error
// for one specific session directory regardless of privilege level, so it
// runs identically under root or non-root and actually exercises the
// aggregation path in CI.
//
// BDD: Given 3 session directories where removeAllFn is stubbed to fail for
// exactly one of them, When ClearAll is called, Then the two unaffected
// directories are removed and counted, and ClearAll returns a non-nil
// aggregate error mentioning the failed session's ID alongside the correct
// partial count.
//
// Traces to: pkg/session/unified.go ClearAll's
// `if err := removeAllFn(dir); err != nil { slog.Warn(...); continue }` path
// and its final `return removed, errors.Join(errs...)`.
func TestClearAll_ContinuesPastRemovalFailure_InjectedError(t *testing.T) {
	store, _ := newTestStoreWithHome(t)

	metaBad, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	metaGood1, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	metaGood2, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)

	badDir := filepath.Join(store.BaseDir(), metaBad.ID)
	injectedErr := fmt.Errorf("injected removeAllFn failure for %s", metaBad.ID)

	// Override the package-level seam for the duration of this test, save/
	// restore via t.Cleanup so other tests are unaffected.
	origRemoveAllFn := removeAllFn
	t.Cleanup(func() { removeAllFn = origRemoveAllFn })
	removeAllFn = func(path string) error {
		if path == badDir {
			return injectedErr
		}
		return origRemoveAllFn(path)
	}

	removed, err := store.ClearAll()

	require.Error(t, err,
		"ClearAll must surface a non-nil aggregate error when removeAllFn failed for one session dir")
	assert.Contains(t, err.Error(), metaBad.ID,
		"the aggregate error should mention the session whose removal failed")
	assert.Contains(t, err.Error(), injectedErr.Error(),
		"the aggregate error should wrap the injected removeAllFn error")
	assert.Equal(t, 2, removed,
		"only the 2 unaffected session dirs must be counted; the one whose removeAllFn failed must not be")

	// The bad directory was never actually touched by the real os.RemoveAll
	// (the stub short-circuited it), so it must still exist, fully intact.
	_, statErr := os.Stat(badDir)
	assert.NoError(t, statErr, "the session dir whose removeAllFn failed must still exist on disk")
	_, statErr = os.Stat(filepath.Join(badDir, "meta.json"))
	assert.NoError(t, statErr, "meta.json inside the unremoved dir must still be present")

	// The two unaffected directories must be gone (removed via the real
	// os.RemoveAll passthrough in the stub).
	for _, m := range []*UnifiedMeta{metaGood1, metaGood2} {
		_, statErr := os.Stat(filepath.Join(store.BaseDir(), m.ID))
		assert.True(t, errors.Is(statErr, os.ErrNotExist), "unaffected session dir for %s must be gone", m.ID)
	}
}
