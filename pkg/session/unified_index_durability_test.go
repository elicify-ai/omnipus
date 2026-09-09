// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 fix-wave (FIX-1) — durability regression coverage for the FR-097
// in-memory parent index and the FR-097a prune pass.
//
// Three independent reviewers (one with an empirical warm/cold-process
// test) found that u4IndexAddChild (unified.go) had exactly ONE caller in
// the tree — the WRITE path (u5WriteIdentityLocked) — while BOTH
// cache-population paths (loadMetaCacheLocked at construction, and
// readMetaLocked's cache-miss branch) populated metaCache directly without
// ever wiring parentIndex/childToParent. ChildCount has no fallback scan, so
// after ANY gateway restart every parent's child_count silently reported 0
// even though every child's ParentSessionID was correct on disk — HTTP 200,
// no log, no counter, and the delegated child permanently unreachable via
// the SessionTree UI (no expand chevron).
//
// A second defect let ListSessions' FR-097a prune pass evict a session
// created concurrently AFTER its os.ReadDir snapshot, permanently losing
// that session's cache-only-dirty stats delta (D12) and its parent-index
// edge with no re-add path.
//
// A third defect let lifecycle_index.go's ensureWarm latch a TRANSIENT scan
// failure (e.g. one EMFILE during a fan-out burst) forever via sync.Once,
// degrading every subsequent parent-durable-key lookup to root-only for the
// remaining process lifetime with no recovery short of a restart.
//
// Per binding Rule 4 ("every negative/zero-count assertion needs a positive
// lower bound"), every test below pairs its failure-mode assertion with an
// explicit positive control proving the mechanism actually ran, not that it
// vacuously passed.
package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------
// Defect 1a: TestUnifiedStore_ColdStartRebuildsParentIndex
// ---------------------------------------------------------------------

// TestUnifiedStore_ColdStartRebuildsParentIndex is the exact scenario a
// reviewer proved empirically with a warm/cold-process test: WARM,
// ChildCount(parent) == 1; COLD (a brand-new *UnifiedStore over the SAME
// directory, simulating a gateway restart), ChildCount(parent) must STILL
// be 1 — the child's ParentSessionID is correct on disk either way, so the
// only thing that can differ is whether loadMetaCacheLocked rebuilt the
// in-memory index from it.
//
// This test MUST fail against the pre-fix code (cold ChildCount == 0,
// because loadMetaCacheLocked populated metaCache directly with no
// u4IndexAddChild call) — verified before applying the fix, per this
// project's investigation discipline.
func TestUnifiedStore_ColdStartRebuildsParentIndex(t *testing.T) {
	baseDir := t.TempDir()

	warm, err := NewUnifiedStore(baseDir)
	require.NoError(t, err, "NewUnifiedStore must succeed")

	parent, err := warm.NewSession(SessionTypeChat, "", "agent-parent")
	require.NoError(t, err)
	child, err := warm.NewSession(SessionTypeDelegate, "", "agent-child")
	require.NoError(t, err)
	require.NotEqual(t, parent.ID, child.ID, "FR-074 corollary: parent and child ids must be distinct")

	require.NoError(t, warm.SetMeta(child.ID, MetaPatch{ParentSessionID: &parent.ID}))

	// Positive control (Rule 4): the edge is real and observable on the
	// SAME (warm) store instance before we ever touch a second one.
	require.Equal(t, 1, warm.ChildCount(parent.ID),
		"positive control: warm ChildCount must be 1 before the cold-start check has any meaning")

	// Sanity: the field itself round-trips through disk regardless of the
	// index bug — isolates "index not rebuilt" from "field not persisted".
	onDiskChild, err := warm.GetMeta(child.ID)
	require.NoError(t, err)
	require.Equal(t, parent.ID, onDiskChild.ParentSessionID,
		"sanity: ParentSessionID must be persisted to meta.json regardless of the index bug")

	require.NoError(t, warm.Close())

	// COLD: a brand-new UnifiedStore over the exact same baseDir, simulating
	// a gateway restart. This exercises loadMetaCacheLocked's boot-time
	// scan, not any live-instance mutation.
	cold, err := NewUnifiedStore(baseDir)
	require.NoError(t, err, "NewUnifiedStore must succeed on a pre-existing directory")
	t.Cleanup(func() { _ = cold.Close() }) // see issue #634

	gotChild, err := cold.GetMeta(child.ID)
	require.NoError(t, err)
	assert.Equal(t, parent.ID, gotChild.ParentSessionID,
		"ParentSessionID must survive a full store reload from disk (this alone was never broken)")

	assert.Equal(t, 1, cold.ChildCount(parent.ID),
		"COLD ChildCount after a simulated restart must be 1 — the parent->child edge is durable on "+
			"disk but was silently unreachable via the in-memory index before the Defect-1 fix "+
			"(loadMetaCacheLocked never called u4IndexAddChild)")
}

// ---------------------------------------------------------------------
// Defect 1b: TestUnifiedStore_CacheMissRepopulatesParentIndex
// ---------------------------------------------------------------------

// TestUnifiedStore_CacheMissRepopulatesParentIndex targets readMetaLocked's
// cache-miss branch specifically (the second of the two broken
// cache-population paths), independent of the cold-start scenario above.
//
// EvictSessionMeta deliberately does NOT touch the parent index (see its
// own doc comment in unified_stats_flush.go — eviction != deletion, the
// session stays a live index member). So to isolate readMetaLocked's own
// contribution, this test also clears the in-memory index directly
// (white-box, same package) after the eviction — reproducing exactly the
// state a cache-miss self-heal must repair on its own: metaCache empty AND
// parentIndex/childToParent not yet reflecting this child, with only
// meta.json on disk to reconstruct from.
func TestUnifiedStore_CacheMissRepopulatesParentIndex(t *testing.T) {
	store := newTestStore(t)

	parent, err := store.NewSession(SessionTypeChat, "", "agent-parent")
	require.NoError(t, err)
	child, err := store.NewSession(SessionTypeDelegate, "", "agent-child")
	require.NoError(t, err)
	require.NotEqual(t, parent.ID, child.ID)

	require.NoError(t, store.SetMeta(child.ID, MetaPatch{ParentSessionID: &parent.ID}))

	// Positive control: the edge exists before we force the cache-miss path.
	require.Equal(t, 1, store.ChildCount(parent.ID), "positive control: edge must exist before the forced cache miss")

	// Force exactly the state readMetaLocked's cache-miss branch must
	// recover from: no cache entry, no index entry, meta.json on disk only.
	store.EvictSessionMeta(child.ID)
	store.cacheMu.Lock()
	delete(store.parentIndex, parent.ID)
	delete(store.childToParent, child.ID)
	store.cacheMu.Unlock()

	require.Equal(t, 0, store.ChildCount(parent.ID), "test setup: index must be genuinely cleared before the re-read")

	// GetMeta's cache-miss path (readMetaLocked) must now compose the
	// session from disk AND repopulate the index, not just the cache.
	got, err := store.GetMeta(child.ID)
	require.NoError(t, err, "GetMeta must self-heal from disk on a cache miss")
	assert.Equal(t, parent.ID, got.ParentSessionID, "sanity: the field itself must still be readable from disk")

	assert.Equal(t, 1, store.ChildCount(parent.ID),
		"ChildCount after the cache-miss re-read must be 1 — readMetaLocked's cache-miss branch must "+
			"call u4IndexAddChild exactly like u5WriteIdentityLocked does on the write path")
}

// ---------------------------------------------------------------------
// Defect 2: TestUnifiedStore_ListSessionsPruneSurvivesConcurrentCreate
// ---------------------------------------------------------------------

// TestUnifiedStore_ListSessionsPruneSurvivesConcurrentCreate reproduces the
// reviewer's G1/G2 scenario deterministically via the listSessionsPruneRaceBarrierFn
// seam (fires right after ListSessions' os.ReadDir snapshot, before onDisk
// is built from it — see the seam's own doc comment in unified.go): a
// session minted, with a cache-only-dirty stats delta, entirely AFTER the
// snapshot the running ListSessions call is working from must not be
// pruned as "stale", and must not lose its dirty stats or its parent-index
// edge in the process.
func TestUnifiedStore_ListSessionsPruneSurvivesConcurrentCreate(t *testing.T) {
	store := newTestStore(t)

	parent, err := store.NewSession(SessionTypeChat, "", "agent-parent")
	require.NoError(t, err)

	// A pre-existing session so ListSessions' ReadDir snapshot captures a
	// non-empty directory, matching the "400 dirs" shape of the reported
	// scenario without needing anywhere near that many fixtures.
	existing, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	origBarrier := listSessionsPruneRaceBarrierFn
	t.Cleanup(func() { listSessionsPruneRaceBarrierFn = origBarrier })

	var raced *UnifiedMeta
	listSessionsPruneRaceBarrierFn = func() {
		// Simulate G2: CreateSessionWithID + AppendTranscript landing AFTER
		// this ListSessions call's os.ReadDir already returned — the raced
		// session's directory did not exist (and the pre-captured "entries"
		// slice cannot retroactively include it) at snapshot time, exactly
		// reproducing the reported race window.
		m, cerr := store.NewSession(SessionTypeDelegate, "", "agent-race")
		require.NoError(t, cerr)
		require.NoError(t, store.SetMeta(m.ID, MetaPatch{ParentSessionID: &parent.ID}))
		// AppendTranscript's counter bump goes cache-only-dirty (D12,
		// u6MarkStatsDirtyLocked) — this is the "900 tokens/$0.02" the
		// report describes as permanently lost.
		require.NoError(t, store.AppendTranscript(m.ID, TranscriptEntry{
			Role: "assistant", Tokens: 900, Cost: 0.02,
		}))
		raced = m
	}

	results, listErr := store.ListSessions()
	require.NoError(t, listErr, "no panic or deadlock from the interleaved create")
	listSessionsPruneRaceBarrierFn = origBarrier

	require.NotNil(t, raced, "the barrier must have run and created the raced session")

	ids := make([]string, 0, len(results))
	for _, m := range results {
		ids = append(ids, m.ID)
	}
	// Positive control: the pre-existing session must still be present —
	// proves this ListSessions call actually did real reconcile work.
	assert.Contains(t, ids, existing.ID, "positive control: the pre-existing session must be listed")

	assert.Contains(t, ids, raced.ID,
		"a session created strictly after ListSessions' ReadDir snapshot must not be pruned as stale")

	// Its dirty stats must have survived intact — not reset by an
	// evict-then-resurrect cycle.
	gotStats, err := store.GetMeta(raced.ID)
	require.NoError(t, err, "the raced session must still be readable after ListSessions returns")
	assert.Equal(t, 900, gotStats.Stats.TokensTotal,
		"the raced session's cache-only-dirty token count must survive the prune pass intact")
	assert.InDelta(t, 0.02, gotStats.Stats.Cost, 1e-9,
		"the raced session's cache-only-dirty cost must survive the prune pass intact")

	// Its FR-097 parent-index edge must not have been dropped either.
	assert.Equal(t, 1, store.ChildCount(parent.ID),
		"the raced session's parent-index edge must survive the prune pass — u4IndexEvict must never "+
			"have run for a session whose directory exists on disk")

	// A subsequent call must also see it, proving the store was left in a
	// consistent, non-corrupted state (not merely "this one call got lucky").
	final, err := store.ListSessions()
	require.NoError(t, err)
	finalIDs := make([]string, 0, len(final))
	for _, m := range final {
		finalIDs = append(finalIDs, m.ID)
	}
	assert.Contains(t, finalIDs, raced.ID)
}

// ---------------------------------------------------------------------
// Defect 3: TestLifecycleParentIndex_EnsureWarmRetriesAfterFailure
// ---------------------------------------------------------------------

// TestLifecycleParentIndex_EnsureWarmRetriesAfterFailure reproduces a
// transient scan failure (e.g. the reported EMFILE during a fan-out burst)
// by pointing a *LifecycleStore at a path that is a REGULAR FILE, not a
// directory: os.ReadDir on that path fails with a genuine (non-NotExist)
// error, exactly like scanSessionIDs' error branch. The failure must NOT be
// latched — clearing the obstruction and retrying must succeed and populate
// the index, proving warmed is only set on a successful scan.
func TestLifecycleParentIndex_EnsureWarmRetriesAfterFailure(t *testing.T) {
	base := t.TempDir()
	storeDir := filepath.Join(base, "lifecycle")

	// Block storeDir with a regular file so os.ReadDir(storeDir) fails with
	// a real (non-NotExist) error — deterministic, no permission tricks
	// that could be bypassed by a root-privileged test runner.
	require.NoError(t, os.WriteFile(storeDir, []byte("not a directory"), 0o600))

	s := NewLifecycleStore(storeDir)

	_, err := s.List(LifecycleFilter{ParentDurableKey: "parent-x"})
	require.Error(t, err, "List must surface the scan failure on the first (blocked) attempt")

	// Clear the obstruction and persist a real parent/child pair.
	require.NoError(t, os.Remove(storeDir))
	require.NoError(t, s.Persist(&LifecycleRecord{
		SessionID: "child-x", State: LifecycleQueued,
		OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: "parent-x",
		WorkspaceID: "ws-1", AgentID: "ray",
	}))

	// The retry must succeed now — a failed warm must never be latched
	// forever the way sync.Once would have latched it.
	recs, err := s.List(LifecycleFilter{ParentDurableKey: "parent-x"})
	require.NoError(t, err, "ensureWarm must retry after a prior failure, not return the same stale error forever")
	require.Len(t, recs, 1, "positive lower bound: the retry must actually find the real child, not just avoid erroring")
	assert.Equal(t, "child-x", recs[0].SessionID)

	// A further call must keep succeeding (the now-successful warm is
	// memoized, matching the "keep the success path warm-once" requirement)
	// without needing to re-scan — persisting a SECOND child via the
	// Persist-time incremental path (add(), not a rescan) must also be
	// visible.
	require.NoError(t, s.Persist(&LifecycleRecord{
		SessionID: "child-y", State: LifecycleQueued,
		OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: "parent-x",
		WorkspaceID: "ws-1", AgentID: "ava",
	}))
	recs2, err := s.List(LifecycleFilter{ParentDurableKey: "parent-x"})
	require.NoError(t, err)
	assert.Len(t, recs2, 2, "post-warm Persist-time maintenance must still add new children without a rescan")
}

// TestLifecycleParentIndex_EnsureWarmMissingDirectoryIsNotAnError pins the
// documented exception: a MISSING lifecycle directory (fresh install) is
// NOT a failure to retry — scanSessionIDs returns (nil, nil) for
// os.IsNotExist, and ensureWarm must treat that as a successful (empty)
// warm, not as the failure case Defect 3 fixes.
func TestLifecycleParentIndex_EnsureWarmMissingDirectoryIsNotAnError(t *testing.T) {
	base := t.TempDir()
	storeDir := filepath.Join(base, "does-not-exist-yet")

	s := NewLifecycleStore(storeDir)

	recs, err := s.List(LifecycleFilter{ParentDurableKey: "parent-x"})
	require.NoError(t, err, "a missing lifecycle directory must warm successfully with an empty index")
	assert.Empty(t, recs)

	// Positive control: once a real record exists, the (already-warmed,
	// now empty) index still picks it up via Persist-time maintenance.
	require.NoError(t, s.Persist(&LifecycleRecord{
		SessionID: "child-z", State: LifecycleQueued,
		OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: "parent-x",
		WorkspaceID: "ws-1", AgentID: "ray",
	}))
	recs2, err := s.List(LifecycleFilter{ParentDurableKey: "parent-x"})
	require.NoError(t, err)
	require.Len(t, recs2, 1, "positive control: a session persisted after the empty warm must still be found")
	assert.Equal(t, "child-z", recs2[0].SessionID)
}
