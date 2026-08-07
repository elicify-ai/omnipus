// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U6 (Wave D) — coverage for W24 (the FR-061...FR-067 stats-flush
// throttle) and FR-067's config default/override contract (#105).
//
// Per binding Rule 1, every assertion below runs against a REAL *UnifiedStore
// rooted at a t.TempDir() and real on-disk files. Per the spec's "Corollary —
// distinct ids everywhere", every session id used is generated independently
// (NewSession), never reused across assertions in a way that could hide a
// mix-up. Test intervals are kept short (tens of milliseconds) via
// SetStatsFlushInterval so this file runs fast; production's real default is
// config.DefaultSessionStatsFlushInterval (5s), asserted directly below.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// u6NewTestStore is this file's self-contained store constructor (ownership
// Rule 6 prefix), mirroring u5NewTestStore/newTestStore — no ordering
// dependency on any other file's helper.
func u6NewTestStore(t *testing.T) *UnifiedStore {
	t.Helper()
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// ---------------------------------------------------------------------
// Test #20: TestStatsThrottle_NoFileWriteWithinInterval (BDD-65)
// ---------------------------------------------------------------------

func TestStatsThrottle_NoFileWriteWithinInterval(t *testing.T) {
	store := u6NewTestStore(t)
	store.SetStatsFlushInterval(300 * time.Millisecond)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	// Precondition (BDD-65/grill m-2): stats.json must already exist from a
	// PRIOR forced flush before the burst, or "unchanged" over a
	// non-existent path is undefined.
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "seed"}))
	require.NoError(t, store.FlushSessionStats(sessionID))

	statsPath := filepath.Join(store.BaseDir(), sessionID, "stats.json")
	before, err := os.Stat(statsPath)
	require.NoError(t, err)
	beforeBytes, err := os.ReadFile(statsPath)
	require.NoError(t, err)

	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	beforeTranscript, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)

	// K appends inside the (300ms) interval — well under it.
	const k = 5
	for i := 0; i < k; i++ {
		require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "burst", Tokens: 1}))
	}

	after, err := os.Stat(statsPath)
	require.NoError(t, err)
	afterBytes, err := os.ReadFile(statsPath)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime(), "stats.json mtime must be unchanged during a burst inside the flush interval")
	assert.Equal(t, beforeBytes, afterBytes, "stats.json content must be unchanged during a burst inside the flush interval")

	afterTranscript, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	beforeLines := countLines(beforeTranscript)
	afterLines := countLines(afterTranscript)
	assert.Equal(t, k, afterLines-beforeLines, "transcript.jsonl must grow by exactly one line per append")

	// Negative control (grill C-4): after the interval elapses with no
	// other action, stats.json DOES become current — proving "unchanged"
	// above was not satisfied by "never written at all".
	time.Sleep(3 * store.StatsFlushInterval())
	finalBytes, err := os.ReadFile(statsPath)
	require.NoError(t, err)
	assert.NotEqual(t, string(beforeBytes), string(finalBytes), "stats.json must become current after the interval elapses")
}

func countLines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------
// Test #21: TestStatsThrottle_ExactCountersAfterInterval (BDD-66)
// ---------------------------------------------------------------------

func TestStatsThrottle_ExactCountersAfterInterval(t *testing.T) {
	store := u6NewTestStore(t)
	store.SetStatsFlushInterval(80 * time.Millisecond)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	const k = 7
	for i := 0; i < k; i++ {
		require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "x", Tokens: 3}))
	}

	time.Sleep(4 * store.StatsFlushInterval())

	got, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, k*3, got.Stats.TokensIn, "no lost or double-counted delta")
	assert.Equal(t, k*3, got.Stats.TokensTotal)
	assert.Equal(t, k, got.Stats.MessageCount)

	// Assert the ON-DISK file itself (not just the cache) matches exactly —
	// re-open a second store instance over the same baseDir.
	reopened, err := NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	reGot, err := reopened.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, k*3, reGot.Stats.TokensIn, "stats.json on disk must equal the sum of appended deltas exactly")
}

// ---------------------------------------------------------------------
// Test #22: TestStatsThrottle_ForcedFlushPoints (BDD-67)
// ---------------------------------------------------------------------

func TestStatsThrottle_ForcedFlushPoints(t *testing.T) {
	t.Run("SetMeta_Status", func(t *testing.T) {
		store := u6NewTestStore(t)
		store.SetStatsFlushInterval(time.Hour) // long enough that only a forced point can flush within this test
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.AppendTranscript(meta.ID, TranscriptEntry{Role: "user", Content: "x", Tokens: 4}))

		status := StatusArchived
		require.NoError(t, store.SetMeta(meta.ID, MetaPatch{Status: &status}))

		statsPath := filepath.Join(store.BaseDir(), meta.ID, "stats.json")
		_, statErr := os.Stat(statsPath)
		require.NoError(t, statErr, "a SetMeta carrying a Status patch must force-flush pending stats")

		reopened, err := NewUnifiedStore(store.BaseDir())
		require.NoError(t, err)
		t.Cleanup(func() { _ = reopened.Close() })
		got, err := reopened.GetMeta(meta.ID)
		require.NoError(t, err)
		assert.Equal(t, 4, got.Stats.TokensIn)
	})

	t.Run("DeleteSession", func(t *testing.T) {
		store := u6NewTestStore(t)
		store.SetStatsFlushInterval(time.Hour)
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.AppendTranscript(meta.ID, TranscriptEntry{Role: "user", Content: "x", Tokens: 4}))

		// The session is about to be deleted, so "stats.json is current" is
		// asserted INDIRECTLY: the forced flush must not error out the
		// delete, and the dirty mark must not survive the deletion (else a
		// stray periodic tick would try to write into a directory that no
		// longer exists).
		require.NoError(t, store.DeleteSession(meta.ID))
		store.cacheMu.RLock()
		_, stillDirty := store.dirtyStats[meta.ID]
		store.cacheMu.RUnlock()
		assert.False(t, stillDirty, "DeleteSession must drop the dirty-set entry (FR-064 step 5)")
	})

	t.Run("Close", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewUnifiedStore(dir)
		require.NoError(t, err)
		store.SetStatsFlushInterval(time.Hour)
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.AppendTranscript(meta.ID, TranscriptEntry{Role: "user", Content: "x", Tokens: 9}))

		require.NoError(t, store.Close())

		statsPath := dir + "/" + meta.ID + "/stats.json"
		_, statErr := os.Stat(statsPath)
		require.NoError(t, statErr, "UnifiedStore.Close must force-flush pending stats before returning")

		reopened, err := NewUnifiedStore(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = reopened.Close() })
		got, err := reopened.GetMeta(meta.ID)
		require.NoError(t, err)
		assert.Equal(t, 9, got.Stats.TokensIn)
	})

	t.Run("FlushSessionStats_U17bSeam", func(t *testing.T) {
		// FR-064's 4th forced-flush point (child CloseSession teardown) is
		// owned by U17b (pkg/agent/session_end.go, Wave E) and is expected
		// to call this exported seam. Covered here directly since that
		// caller does not exist yet.
		store := u6NewTestStore(t)
		store.SetStatsFlushInterval(time.Hour)
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.AppendTranscript(meta.ID, TranscriptEntry{Role: "user", Content: "x", Tokens: 2}))

		require.NoError(t, store.FlushSessionStats(meta.ID))
		statsPath := filepath.Join(store.BaseDir(), meta.ID, "stats.json")
		_, statErr := os.Stat(statsPath)
		require.NoError(t, statErr)

		// Idempotent: a second call with nothing dirty is a no-op, not an error.
		require.NoError(t, store.FlushSessionStats(meta.ID))
	})
}

// ---------------------------------------------------------------------
// Test #23: TestEventWrites_NotThrottled (BDD-68)
// ---------------------------------------------------------------------

func TestEventWrites_NotThrottled(t *testing.T) {
	store := u6NewTestStore(t)
	store.SetStatsFlushInterval(time.Hour) // prove these land WITHOUT any flush interval elapsing
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	goalCond := "ship-it"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{GoalCondition: &goalCond, GoalRoundsUsed: intPtr(1)}))
	goalOnDisk, err := u5ReadGoalFile(filepath.Join(store.BaseDir(), sessionID))
	require.NoError(t, err)
	assert.Equal(t, 1, goalOnDisk.GoalRoundsUsed, "/goal round's GoalRoundsUsed must be on disk immediately")

	loopMode := "interval"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{LoopMode: &loopMode, LoopRunCount: intPtr(2)}))
	loopOnDisk, err := u5ReadLoopFile(filepath.Join(store.BaseDir(), sessionID))
	require.NoError(t, err)
	assert.Equal(t, 2, loopOnDisk.LoopRunCount, "/loop tick's LoopRunCount must be on disk immediately")

	status := StatusArchived
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{Status: &status}))
	identOnDisk, err := u5ReadIdentityFile(filepath.Join(store.BaseDir(), sessionID))
	require.NoError(t, err)
	assert.Equal(t, StatusArchived, identOnDisk.Status, "a status transition must be on disk immediately")

	title := "new title"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{Title: &title}))
	identOnDisk2, err := u5ReadIdentityFile(filepath.Join(store.BaseDir(), sessionID))
	require.NoError(t, err)
	assert.Equal(t, "new title", identOnDisk2.Title, "a title change must be on disk immediately")
}

func intPtr(i int) *int { return &i }

// ---------------------------------------------------------------------
// Test #89: TestStatsThrottle_UnforcedFlushConverges (BDD-93, AC-22
// scenario 7) — THE test that fails against a never-started flusher.
// ---------------------------------------------------------------------

// TestStatsThrottle_UnforcedFlushConverges is the ONE assertion in this
// spec that a store whose periodic flusher goroutine never started cannot
// satisfy — every other throttle test (burst-unchanged, forced-flush
// points, event writes) is satisfiable by a store that has flushed NOTHING,
// ever (grill C-4). No SetMeta, no DeleteSession, no Close, no test-driven
// tick: one append and nothing else, and stats.json must still become
// current after the interval elapses on the real clock. See this file's
// package doc comment / this test's red-run evidence in the U6 dispatch
// report for the demonstration that a stubbed-out startStatsFlusher call
// makes this test fail while every other throttle test in this file still
// passes.
func TestStatsThrottle_UnforcedFlushConverges(t *testing.T) {
	store := u6NewTestStore(t)
	store.SetStatsFlushInterval(60 * time.Millisecond)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	// ONE append. NOTHING else — no SetMeta, no DeleteSession, no Close, no
	// manual flush call, no test-driven tick.
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "solo", Tokens: 6}))

	statsPath := filepath.Join(store.BaseDir(), sessionID, "stats.json")
	_, statErrBefore := os.Stat(statsPath)
	require.True(t, os.IsNotExist(statErrBefore), "stats.json must not exist yet — the append is still only in-memory (FR-061)")

	// More than one flush interval elapses on the REAL clock.
	time.Sleep(3 * store.StatsFlushInterval())

	data, err := os.ReadFile(statsPath)
	require.NoError(t, err, "stats.json must exist and be current with NO external trigger at all — this is the property that fails on a dead flusher")
	var onDisk u5StatsFile
	require.NoError(t, json.Unmarshal(data, &onDisk))
	assert.Equal(t, 6, onDisk.TokensIn)
}

// ---------------------------------------------------------------------
// Test #90: TestStatsCache_FieldGroupIsolationUnderInterleavedWriters (BDD-94)
// ---------------------------------------------------------------------

func TestStatsCache_FieldGroupIsolationUnderInterleavedWriters(t *testing.T) {
	store := u6NewTestStore(t)
	store.SetStatsFlushInterval(time.Hour) // only the forced flush at the end should persist stats

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	// K appends recorded, unflushed — deltas exist only in the cache.
	const k = 4
	for i := 0; i < k; i++ {
		require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "x", Tokens: 5}))
	}

	// A /goal round and a Status transition — each of which touches the
	// cache via u5WriteGoalLocked/u5WriteIdentityLocked, which mutate ONLY
	// their own field group in place (never `metaCache[id] = meta.Clone()`
	// wholesale) — so neither must clobber the still-pending Stats delta.
	goalCond := "mid-flight-goal"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{GoalCondition: &goalCond}))
	status := StatusActive
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{Status: &status}))

	// The Status patch above forces a flush (FR-064) — assert it captured
	// the full, uncorrupted K-append delta, not a partial or zeroed one.
	onDisk, err := u5ReadStatsFile(filepath.Join(store.BaseDir(), sessionID))
	require.NoError(t, err)
	assert.Equal(t, k*5, onDisk.TokensIn, "stats.json must equal K's exact deltas — zero lost, zero double-counted")

	goalOnDisk, err := u5ReadGoalFile(filepath.Join(store.BaseDir(), sessionID))
	require.NoError(t, err)
	assert.Equal(t, "mid-flight-goal", goalOnDisk.GoalCondition, "goal.json must carry the goal writer's own value")

	identOnDisk, err := u5ReadIdentityFile(filepath.Join(store.BaseDir(), sessionID))
	require.NoError(t, err)
	assert.Equal(t, StatusActive, identOnDisk.Status, "meta.json must carry the identity writer's own value")
}

// ---------------------------------------------------------------------
// Test #105: TestFlushInterval_ConfigKeyDefaultAndOverride
// ---------------------------------------------------------------------

func TestFlushInterval_ConfigKeyDefaultAndOverride(t *testing.T) {
	store := u6NewTestStore(t)

	// The key exists and a fresh store defaults to config's own 5s constant
	// (imported directly, not duplicated — see unified_stats_flush.go's
	// startStatsFlusher doc comment).
	assert.Equal(t, config.DefaultSessionStatsFlushInterval, store.StatsFlushInterval())
	assert.Equal(t, 5*time.Second, store.StatsFlushInterval())

	// A non-default value is honoured end to end: override, append, wait
	// less than the OLD default but more than the new value, and confirm
	// the flush actually happened on the NEW interval.
	store.SetStatsFlushInterval(50 * time.Millisecond)
	assert.Equal(t, 50*time.Millisecond, store.StatsFlushInterval())

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	require.NoError(t, store.AppendTranscript(meta.ID, TranscriptEntry{Role: "user", Content: "x", Tokens: 1}))

	time.Sleep(200 * time.Millisecond) // several multiples of 50ms; far less than the 5s default
	_, statErr := os.Stat(filepath.Join(store.BaseDir(), meta.ID, "stats.json"))
	require.NoError(t, statErr, "a non-default flush interval must be honoured end to end, not just stored")
}

// ---------------------------------------------------------------------
// Concurrency sanity: SetStatsFlushInterval / flushAllDirtyStats /
// AppendTranscript may all be invoked concurrently without racing
// (informs confidence in the -race gate the dispatch requires separately).
// ---------------------------------------------------------------------

func TestStatsThrottle_ConcurrentAppendsAndIntervalChangesDoNotRace(t *testing.T) {
	store := u6NewTestStore(t)
	store.SetStatsFlushInterval(20 * time.Millisecond)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "x", Tokens: 1})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			store.SetStatsFlushInterval(time.Duration(5+i) * time.Millisecond)
			time.Sleep(time.Millisecond)
		}
	}()
	wg.Wait()

	time.Sleep(100 * time.Millisecond)
	got, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, 50, got.Stats.TokensIn)
}
