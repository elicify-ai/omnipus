// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for UnifiedStore's in-memory metaCache (Mechanism B).
//
// Root cause under test: ListSessions previously did an O(N) os.ReadDir plus
// a per-session os.ReadFile+json.Unmarshal under the store's single lock on
// every call — contended by every write path too. The fix adds a metaCache
// (RWMutex read/write split) so ListSessions/GetMeta/GetOrCreateScheduledSession
// serve from memory, with writeMetaLocked as the single invalidation point and
// explicit eviction on DeleteSession/ClearAll/RetentionSweep.
//
// BDD scenarios:
//
//	Scenario: ListSessions serves from cache even when meta.json is gone from disk
//	Scenario: ListSessions reflects a write immediately (no cache lag)
//	Scenario: ListSessions returns independent clones (isolation both directions)
//	Scenario: GetMeta hits the cache, and self-heals + repopulates on a cache miss
//	Scenario: DeleteSession evicts the cache entry
//	Scenario: ClearAll empties the cache
//	Scenario: NewChannelSession's post-create field mutation never leaks into the cache
//	Scenario: RetentionSweep evicts a swept session from the cache (companion-fix regression)
//	Scenario: GetOrCreateScheduledSession's second call serves from cache
//	Scenario (concurrency, -race): list-while-writing, NewChannelSession-while-listing, delete-while-listing
//
// Traces to: pkg/session/unified.go (metaCache, Clone, readMetaLocked,
// writeMetaLocked, ListSessions, GetMeta, GetOrCreateScheduledSession,
// DeleteSession, ClearAll) and pkg/session/retention_sweep.go (companion fix).

package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findMeta returns the entry in metas whose ID matches id, or nil if absent.
func findMeta(metas []*UnifiedMeta, id string) *UnifiedMeta {
	for _, m := range metas {
		if m.ID == id {
			return m
		}
	}
	return nil
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

// TestGetMeta_CacheHitAndDiskSelfHeal verifies GetMeta's fast (cache-hit) path
// and its cache-miss disk self-heal, which must also repopulate the cache.
//
// Traces to: pkg/session/unified.go GetMeta, readMetaLocked.
func TestGetMeta_CacheHitAndDiskSelfHeal(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	// Cache-hit path.
	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, meta.ID, got.ID)

	// Force a cache miss (white-box, same package) while leaving meta.json on
	// disk intact, then confirm GetMeta self-heals via disk and repopulates
	// the cache.
	store.mu.Lock()
	delete(store.metaCache, meta.ID)
	store.mu.Unlock()

	got2, err := store.GetMeta(meta.ID)
	require.NoError(t, err, "GetMeta must self-heal from disk on a cache miss")
	assert.Equal(t, meta.ID, got2.ID)

	store.mu.RLock()
	_, cached := store.metaCache[meta.ID]
	store.mu.RUnlock()
	assert.True(t, cached, "GetMeta must repopulate the cache after a disk self-heal")
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

	store.mu.RLock()
	cacheLen := len(store.metaCache)
	store.mu.RUnlock()
	assert.Zero(t, cacheLen, "ClearAll must empty metaCache, not just disk")
}

// TestNewChannelSession_PostCreateMutationDoesNotLeakIntoCache guards against
// the specific aliasing mistake called out in Clone's doc comment:
// NewChannelSession mutates meta.PeerID/meta.Title on the pointer NewSession
// returned, without holding us.mu, before its own writeMetaLocked call. A
// caller that goes on to mutate the object NewChannelSession itself returned
// (bypassing SetMeta) must never be able to corrupt the cache either.
//
// Traces to: pkg/session/unified.go NewChannelSession.
func TestNewChannelSession_PostCreateMutationDoesNotLeakIntoCache(t *testing.T) {
	store := newTestStore(t)

	const originalTitle = "Original Title"
	meta, err := store.NewChannelSession("telegram", "peer-1", "agent-1", originalTitle)
	require.NoError(t, err)
	require.Equal(t, originalTitle, meta.Title)

	// Caller mutates the returned pointer directly, bypassing SetMeta.
	meta.Title = "MUTATED-AFTER-RETURN"

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, originalTitle, got.Title,
		"a caller-side mutation of NewChannelSession's returned pointer must not leak into the cache")
}

// TestRetentionSweep_EvictsRemovedSessionsFromCache is the regression test for
// the companion fix in retention_sweep.go: RetentionSweep's second pass
// removes an emptied session directory (including meta.json) via
// os.RemoveAll WITHOUT ever touching us.mu/metaCache. Without the companion
// fix, GetMeta would keep serving the stale cached entry forever (a phantom
// session) even though the directory is gone from disk. This test FAILS
// without that fix.
//
// Traces to: pkg/session/retention_sweep.go RetentionSweep (companion fix).
func TestRetentionSweep_EvictsRemovedSessionsFromCache(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	// Age transcript.jsonl beyond the retention window so RetentionSweep's
	// first pass removes it, which then makes the session dir empty of
	// .jsonl files and triggers the second pass's whole-directory removal.
	transcriptPath := filepath.Join(store.baseDir, sessionID, "transcript.jsonl")
	oldTime := time.Now().Add(-10 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(transcriptPath, oldTime, oldTime))

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, removed, 1)

	// The session directory (and its meta.json) must be gone from disk.
	_, statErr := os.Stat(filepath.Join(store.baseDir, sessionID))
	require.True(t, os.IsNotExist(statErr), "swept session directory must be removed from disk")

	// Regression check: GetMeta must NOT still serve this session from a
	// stale cache entry.
	_, err = store.GetMeta(sessionID)
	assert.Error(t, err,
		"GetMeta must fail for a session RetentionSweep removed — cache must be evicted, not stale")

	metas, err := store.ListSessions()
	require.NoError(t, err)
	assert.Nil(t, findMeta(metas, sessionID),
		"ListSessions must not include a session RetentionSweep removed")
}

// TestGetOrCreateScheduledSession_SecondCallServesFromCache creates a
// scheduled session, removes its meta.json directly from disk, then verifies
// a second GetOrCreateScheduledSession call still succeeds (via the
// cache-first existence check) and returns the SAME session rather than
// silently recreating a new one.
//
// Traces to: pkg/session/unified.go GetOrCreateScheduledSession.
func TestGetOrCreateScheduledSession_SecondCallServesFromCache(t *testing.T) {
	store := newTestStore(t)
	id := "sched-cache-test"

	meta1, err := store.GetOrCreateScheduledSession(id, "owner-1")
	require.NoError(t, err)
	require.Equal(t, id, meta1.ID)

	metaPath := filepath.Join(store.baseDir, id, "meta.json")
	require.NoError(t, os.Remove(metaPath))

	meta2, err := store.GetOrCreateScheduledSession(id, "owner-1")
	require.NoError(t, err)
	assert.Equal(t, id, meta2.ID)
	assert.Equal(t, meta1.CreatedAt, meta2.CreatedAt,
		"second call must return the SAME session (served from cache), not a freshly recreated one")
}

// TestConcurrentListWhileWriting runs NewSession and ListSessions
// concurrently; run with -race to catch any metaCache/us.mu misuse.
//
// Traces to: pkg/session/unified.go ListSessions, NewSession (RWMutex split).
func TestConcurrentListWhileWriting(t *testing.T) {
	store := newTestStore(t)
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if _, err := store.NewSession(SessionTypeChat, "", "agent-writer"); err != nil {
				t.Errorf("NewSession failed: %v", err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if _, err := store.ListSessions(); err != nil {
				t.Errorf("ListSessions failed: %v", err)
			}
		}
	}()
	wg.Wait()

	metas, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, metas, iterations)
}

// TestConcurrentNewChannelSessionWhileListing runs NewChannelSession (which
// mutates its meta pointer's PeerID/Title outside us.mu before its own
// writeMetaLocked call) concurrently with ListSessions. This is the specific
// scenario that would surface a "cache stored the live, still-mutating
// pointer instead of a clone" mistake as a -race failure.
//
// Traces to: pkg/session/unified.go NewChannelSession, ListSessions, Clone.
func TestConcurrentNewChannelSessionWhileListing(t *testing.T) {
	store := newTestStore(t)
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			peer := fmt.Sprintf("peer-%d", i)
			if _, err := store.NewChannelSession("telegram", peer, "agent-1", "Title-"+peer); err != nil {
				t.Errorf("NewChannelSession failed: %v", err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			metas, err := store.ListSessions()
			if err != nil {
				t.Errorf("ListSessions failed: %v", err)
				continue
			}
			// Touch every returned meta's fields so -race has something to
			// flag if the cache ever aliased NewChannelSession's mutating
			// pointer instead of an independent clone.
			for _, m := range metas {
				_ = len(m.AgentIDs)
				_ = m.Title
				_ = m.PeerID
			}
		}
	}()
	wg.Wait()
}

// TestConcurrentDeleteWhileListing runs DeleteSession and ListSessions
// concurrently; run with -race to catch any metaCache/us.mu misuse around
// eviction.
//
// Traces to: pkg/session/unified.go DeleteSession, ListSessions.
func TestConcurrentDeleteWhileListing(t *testing.T) {
	store := newTestStore(t)
	const n = 30

	ids := make([]string, n)
	for i := 0; i < n; i++ {
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		ids[i] = meta.ID
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for _, id := range ids {
			if err := store.DeleteSession(id); err != nil {
				t.Errorf("DeleteSession(%s) failed: %v", id, err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := store.ListSessions(); err != nil {
				t.Errorf("ListSessions failed: %v", err)
			}
		}
	}()
	wg.Wait()

	metas, err := store.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, metas, "all sessions must be deleted by the end of the concurrent run")
}
