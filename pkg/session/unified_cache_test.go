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
//	Scenario (concurrency, -race): SetMeta/SwitchAgent (RMW) interleaved with ListSessions/GetMeta
//	Scenario (MB-1 regression): a FAILED SetMeta must not corrupt the cache — GetMeta/ListSessions
//	  keep returning the OLD (disk-matching) value, never the attempted-but-unpersisted one
//	Scenario: loadMetaCacheLocked (boot-time cache seed) happy path — a fresh store over N
//	  pre-existing valid sessions lists all N
//	Scenario (MB-2 regression): loadMetaCacheLocked excludes a corrupt session and counts it via
//	  CacheLoadFailureCount, without failing store construction
//	Scenario (MB-3 regression): Clone() deep-copies every reference-kind field UnifiedMeta has —
//	  a reflection-driven guard that fails if a future field is added without also being cloned
//	Scenario (out-of-band reconciliation regression): ListSessions lists a session directory
//	  written directly to disk after construction, bypassing the store entirely, by lazily
//	  self-healing it into metaCache — without any per-file disk read for sessions already cached
//	Scenario (out-of-band reconciliation, MB-2-style): a malformed out-of-band session directory
//	  is skipped gracefully (logged, not fatal) while a good out-of-band session next to it is
//	  still listed
//
// Traces to: pkg/session/unified.go (metaCache, Clone, readMetaLocked,
// writeMetaLocked, ListSessions, GetMeta, GetOrCreateScheduledSession,
// DeleteSession, ClearAll, loadMetaCacheLocked, CacheLoadFailureCount) and
// pkg/session/retention_sweep.go (companion fix).

package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	store.cacheMu.Lock()
	delete(store.metaCache, meta.ID)
	store.cacheMu.Unlock()

	got2, err := store.GetMeta(meta.ID)
	require.NoError(t, err, "GetMeta must self-heal from disk on a cache miss")
	assert.Equal(t, meta.ID, got2.ID)

	store.cacheMu.RLock()
	_, cached := store.metaCache[meta.ID]
	store.cacheMu.RUnlock()
	assert.True(t, cached, "GetMeta must repopulate the cache after a disk self-heal")
}

// TestNewChannelSession_PostCreateMutationDoesNotLeakIntoCache guards against
// the specific aliasing mistake called out in Clone's doc comment:
// NewChannelSession mutates meta.PeerID/meta.Title on the pointer NewSession
// returned, without holding any lock, before its own writeMetaLocked call. A
// caller that goes on to mutate the object NewChannelSession itself returned
// (bypassing SetMeta) must never be able to corrupt the cache either.
//
// Traces to: pkg/session/unified.go NewChannelSession.
func TestNewChannelSession_PostCreateMutationDoesNotLeakIntoCache(t *testing.T) {
	store := newTestStore(t)

	const originalTitle = "Original Title"
	meta, err := store.NewChannelSession("telegram", "telegram", "peer-1", "agent-1", originalTitle)
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
// os.RemoveAll WITHOUT ever touching cacheMu/metaCache. Without the companion
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
	require.True(t, errors.Is(statErr, os.ErrNotExist), "swept session directory must be removed from disk")

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
// concurrently; run with -race to catch any metaCache/cacheMu misuse.
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
// mutates its meta pointer's PeerID/Title outside any lock before its own
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
			if _, err := store.NewChannelSession("telegram", "telegram", peer, "agent-1", "Title-"+peer); err != nil {
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
// concurrently; run with -race to catch any metaCache/cacheMu misuse around
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

// TestConcurrentSetMetaSwitchAgentWhileReading is the -race test-gap fix
// (pr-test sev3): the existing concurrency trio above covers
// NewSession/NewChannelSession/DeleteSession racing ListSessions, but none of
// them exercise the specific read-modify-write pattern readMetaLocked's doc
// comment calls out — SetMeta/SwitchAgent mutate the value readMetaLocked
// returns and then call writeMetaLocked. This interleaves SetMeta and
// SwitchAgent (both RMW callers) with ListSessions and GetMeta (both readers)
// across many sessions; run with -race to catch any metaCache/cacheMu misuse
// on this path specifically.
//
// Traces to: pkg/session/unified.go readMetaLocked, SetMeta, SwitchAgent,
// ListSessions, GetMeta.
func TestConcurrentSetMetaSwitchAgentWhileReading(t *testing.T) {
	store := newTestStore(t)
	const n = 10
	const iterations = 50

	ids := make([]string, n)
	for i := 0; i < n; i++ {
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		ids[i] = meta.ID
	}

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			id := ids[i%n]
			title := fmt.Sprintf("title-%d", i)
			if err := store.SetMeta(id, MetaPatch{Title: &title}); err != nil {
				t.Errorf("SetMeta failed: %v", err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		agents := []string{"agent-1", "agent-2"}
		for i := 0; i < iterations; i++ {
			id := ids[i%n]
			newAgent := agents[i%2]
			if err := store.SwitchAgent(id, newAgent); err != nil && !errors.Is(err, ErrAlreadyActive) {
				t.Errorf("SwitchAgent failed: %v", err)
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
			// flag if a read ever aliased a value an RMW writer was
			// concurrently mutating in place.
			for _, m := range metas {
				_ = m.Title
				_ = m.ActiveAgentID
				_ = len(m.AgentIDs)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			id := ids[i%n]
			m, err := store.GetMeta(id)
			if err != nil {
				t.Errorf("GetMeta failed: %v", err)
				continue
			}
			_ = m.Title
			_ = m.ActiveAgentID
		}
	}()

	wg.Wait()
}

// TestNewUnifiedStore_LoadMetaCache_HappyPath is the restart-path happy-path
// test gap (pr-test sev7): loadMetaCacheLocked (the boot-time cache seed) had
// zero coverage before this fix round. Constructs a store, creates N
// sessions, discards that store instance (simulating a prior process run),
// then constructs a FRESH UnifiedStore over the SAME baseDir and asserts
// ListSessions returns all N — proving the boot-time cache seed actually
// works, not just the live-instance mutation paths the rest of this file
// covers.
//
// Traces to: pkg/session/unified.go loadMetaCacheLocked.
func TestNewUnifiedStore_LoadMetaCache_HappyPath(t *testing.T) {
	baseDir := t.TempDir()

	first, err := NewUnifiedStore(baseDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() }) // see issue #634

	const n = 4
	ids := make([]string, n)
	var meta *UnifiedMeta
	for i := 0; i < n; i++ {
		meta, err = first.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		ids[i] = meta.ID
	}

	// Fresh store instance over the same baseDir — exercises
	// loadMetaCacheLocked's boot-time scan, not any live-instance mutation.
	second, err := NewUnifiedStore(baseDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() }) // see issue #634

	metas, err := second.ListSessions()
	require.NoError(t, err)
	require.Len(t, metas, n, "a fresh store must seed metaCache with every pre-existing session")
	for _, id := range ids {
		assert.NotNil(t, findMeta(metas, id), "session %s must be present after the boot-time cache seed", id)
	}
	assert.Zero(t, second.CacheLoadFailureCount(), "no sessions were corrupt — failure count must be 0")
}

// TestNewUnifiedStore_LoadMetaCache_CorruptSessionExcludedAndCounted is the
// MB-2 regression guard: a session whose meta.json is unreadable/unparseable
// at construction time must be excluded from metaCache (and therefore from
// ListSessions/GetMeta) without failing store construction, AND the failure
// must be counted via CacheLoadFailureCount so the condition is
// assertable/observable rather than a silent, permanent gap.
//
// BDD: Given one session directory with a valid meta.json and one with a
// corrupt (unparseable) meta.json, When a fresh UnifiedStore is constructed
// over that baseDir, Then ListSessions returns only the good session,
// GetMeta fails for the bad one, and CacheLoadFailureCount()==1.
//
// Traces to: pkg/session/unified.go loadMetaCacheLocked, CacheLoadFailureCount.
func TestNewUnifiedStore_LoadMetaCache_CorruptSessionExcludedAndCounted(t *testing.T) {
	baseDir := t.TempDir()

	goodDir := filepath.Join(baseDir, "good-session")
	require.NoError(t, os.MkdirAll(goodDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(goodDir, "meta.json"),
		[]byte(`{"id":"good-session","status":"active"}`),
		0o600,
	))

	badDir := filepath.Join(baseDir, "bad-session")
	require.NoError(t, os.MkdirAll(badDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(badDir, "meta.json"),
		[]byte(`{not valid json`),
		0o600,
	))

	store, err := NewUnifiedStore(baseDir)
	require.NoError(t, err, "NewUnifiedStore must succeed despite one corrupt session dir")
	t.Cleanup(func() { _ = store.Close() }) // see issue #634

	metas, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, metas, 1, "only the good session must be present in the cache")
	assert.Equal(t, "good-session", metas[0].ID)

	assert.Equal(t, 1, store.CacheLoadFailureCount(),
		"the corrupt session must be counted as a cache-load failure")

	_, err = store.GetMeta("bad-session")
	assert.Error(t, err, "the corrupt session must not be servable via GetMeta either")
}

// TestClone_GuardsEveryReferenceField is the MB-3 regression guard: Clone()
// is a hand-maintained enumeration of UnifiedMeta's current reference fields
// (Partitions, AgentIDs, CompactionSummaries, Stats.ByModel) with nothing
// forcing a FUTURE slice/map/pointer field added to
// UnifiedMeta/SessionMeta/SessionStats to also be cloned there — a silent
// aliasing bug otherwise.
//
// This test walks UnifiedMeta's fields (including the embedded
// SessionMeta/SessionStats) via reflection and requires every slice/map/
// pointer/chan/func/interface-kind field it finds to be in the explicit
// "known" allowlist below — hand-verified to be exercised by a mutate-clone-
// mutate-assert check further down. Adding a new reference field to
// UnifiedMeta/SessionMeta/SessionStats without also adding it to Clone() AND
// to this test's known/exercised list makes this test FAIL via the
// t.Fatalf below, so it can never silently go uncovered.
//
// Scope caveat: the walker recurses only into the nested structs present today
// (the embedded SessionMeta and the named SessionStats). A new field whose type
// is some OTHER nested struct holding its own slices/maps would be treated as
// scalar-like and NOT descended into — so this guard covers any new DIRECT
// slice/map/pointer field, not an arbitrarily-nested one. If a new nested
// struct type is added, extend the recursion below to match.
//
// Traces to: pkg/session/unified.go Clone.
func TestClone_GuardsEveryReferenceField(t *testing.T) {
	original := &UnifiedMeta{
		SessionMeta: SessionMeta{
			ID:         "meta-1",
			Partitions: []string{"p1", "p2"},
			AgentIDs:   []string{"agent-a", "agent-b"},
			CompactionSummaries: map[string]string{
				"agent-a": "summary-a",
			},
			Stats: SessionStats{
				ByModel: map[string]ModelTokens{
					"model-a": {Total: 10},
				},
			},
		},
		Type: SessionTypeChat,
	}

	type refField struct {
		path string
		kind reflect.Kind
	}
	var refFields []refField
	var walk func(rt reflect.Type, prefix string)
	walk = func(rt reflect.Type, prefix string) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			switch f.Type.Kind() {
			case reflect.Struct:
				switch {
				case f.Anonymous && f.Type == reflect.TypeOf(SessionMeta{}):
					// UnifiedMeta embeds SessionMeta anonymously — its fields
					// are PROMOTED, so recurse WITHOUT adding "SessionMeta."
					// to the path (matches how Partitions/AgentIDs/etc. are
					// actually addressed: original.Partitions, not
					// original.SessionMeta.Partitions).
					walk(f.Type, prefix)
				case f.Type == reflect.TypeOf(SessionStats{}):
					// SessionMeta.Stats is a named (non-anonymous) nested
					// struct field, so it DOES contribute its own name
					// ("Stats.") to the path.
					walk(f.Type, prefix+f.Name+".")
				default:
					// Any other nested struct (e.g. time.Time) is treated as
					// scalar-like for this test's purposes.
				}
			case reflect.Slice, reflect.Map, reflect.Pointer, reflect.Chan, reflect.Func, reflect.Interface:
				refFields = append(refFields, refField{path: prefix + f.Name, kind: f.Type.Kind()})
			}
		}
	}
	walk(reflect.TypeOf(UnifiedMeta{}), "")

	known := map[string]bool{
		"Partitions":          true,
		"AgentIDs":            true,
		"CompactionSummaries": true,
		"Stats.ByModel":       true,
	}
	for _, rf := range refFields {
		if !known[rf.path] {
			t.Fatalf(
				"Clone-guard test does not know how to exercise new reference field %q (kind %v) — "+
					"add it to Clone() AND to this test's known/exercised-field list",
				rf.path, rf.kind,
			)
		}
	}
	for path := range known {
		found := false
		for _, rf := range refFields {
			if rf.path == path {
				found = true
				break
			}
		}
		assert.True(
			t,
			found,
			"known-field %q listed in this test no longer exists on UnifiedMeta — update the test",
			path,
		)
	}

	clone := original.Clone()

	clone.Partitions[0] = "MUTATED"
	assert.Equal(t, "p1", original.Partitions[0],
		"mutating clone.Partitions must not affect original.Partitions")
	clone.Partitions = append(clone.Partitions, "p3")
	assert.Len(t, original.Partitions, 2,
		"appending to clone.Partitions must not affect original.Partitions length")

	clone.AgentIDs[0] = "MUTATED"
	assert.Equal(t, "agent-a", original.AgentIDs[0],
		"mutating clone.AgentIDs must not affect original.AgentIDs")

	clone.CompactionSummaries["agent-a"] = "MUTATED"
	assert.Equal(t, "summary-a", original.CompactionSummaries["agent-a"],
		"mutating clone.CompactionSummaries must not affect original.CompactionSummaries")
	clone.CompactionSummaries["agent-new"] = "added-only-to-clone"
	_, foundInOriginal := original.CompactionSummaries["agent-new"]
	assert.False(t, foundInOriginal,
		"adding a key to clone.CompactionSummaries must not affect original.CompactionSummaries")

	cloneEntry := clone.Stats.ByModel["model-a"]
	cloneEntry.Total = 999
	clone.Stats.ByModel["model-a"] = cloneEntry
	assert.Equal(t, 10, original.Stats.ByModel["model-a"].Total,
		"mutating clone.Stats.ByModel must not affect original.Stats.ByModel")
}
