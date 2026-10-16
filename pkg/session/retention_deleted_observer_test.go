// Omnipus — #1329 round-2 retention deletion notification regressions
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetentionSweepSessionDeletedObserver traces to security F1 in
// pq2-sec-full-report.md and the 2026-10-17 team-lead dispatch: notify cleanup
// for confirmed whole-session removals, strictly AFTER every store shard is
// released. This is a real filesystem/store/callback test, not a source scan
// or a timeout-based lock-order guess. GREEN/mutation proof is deferred to CHECK.
func TestRetentionSweepSessionDeletedObserver(t *testing.T) {
	t.Run("removed_sessions_notify_once_after_all_shards_unlock", retentionDeletedCaseAfterUnlock)
	t.Run("partial_pruning_does_not_notify_session_deletion", retentionDeletedCasePartialPrune)
	t.Run("protected_main_does_not_notify_session_deletion", retentionDeletedCaseMainSurvives)
	for _, days := range []int{-1, 0} { // retentionDays <= 0 is the documented disabled boundary.
		name := "zero_retention_does_not_notify"
		if days < 0 {
			name = "negative_retention_does_not_notify"
		}
		t.Run(name, func(t *testing.T) { retentionDeletedCaseDisabled(t, days) })
	}
}

func retentionDeletedNewChat(t *testing.T, store *UnifiedStore) string {
	t.Helper()
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	return meta.ID
}

func retentionDeletedCaseAfterUnlock(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	first := retentionDeletedNewChat(t, store)
	second := retentionDeletedNewChat(t, store)
	survivor := retentionDeletedNewChat(t, store)
	// 30 days is comfortably older than the chosen 7-day retention window;
	// these are equivalence classes, not a wall-clock cutoff race.
	createSessionFile(t, store, first, "transcript.jsonl", 30*24*time.Hour)
	createSessionFile(t, store, second, "transcript.jsonl", 30*24*time.Hour)

	// Positive instrument control: these exact mutexes detect the real
	// sweep's all-shard hold. TryLock checks actual ownership, not an event
	// recorder whose bookkeeping could precede the physical unlock.
	unlock := store.lockAllSessionShards()
	for shard := range store.sessionLocks.locks {
		if store.sessionLocks.locks[shard].TryLock() {
			store.sessionLocks.locks[shard].Unlock()
			t.Errorf("instrument: shard %d did not detect the real all-shard hold", shard)
		}
	}
	unlock()

	var notified []string
	store.SetSessionDeletedObserver(func(id string) {
		notified = append(notified, id)
		_, err := os.Stat(filepath.Join(store.BaseDir(), id))
		assert.ErrorIs(t, err, os.ErrNotExist, "cleanup must run only after successful directory deletion: %s", id)
		for shard := range store.sessionLocks.locks {
			if store.sessionLocks.locks[shard].TryLock() {
				store.sessionLocks.locks[shard].Unlock()
			} else {
				t.Errorf("retention deletion observer ran while store shard %d was still locked; registry cleanup must be outside ALL store shard locks", shard)
			}
		}
		if store.cacheMu.TryLock() {
			store.cacheMu.Unlock()
		} else {
			t.Error("retention deletion observer ran under cacheMu; cleanup must be outside store locks")
		}
	})

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 2, removed, "instrument: exactly the two aged transcripts must be removed")
	for _, id := range []string{first, second} {
		_, statErr := os.Stat(filepath.Join(store.BaseDir(), id))
		require.ErrorIs(t, statErr, os.ErrNotExist, "instrument: the session must REALLY be deleted before checking its notification")
	}
	assert.ElementsMatch(t, []string{first, second}, notified,
		"each successfully removed session must notify once; a retained session must never notify")
	_, err = store.GetMeta(survivor)
	require.NoError(t, err, "the recent session must remain readable")

	removed, err = store.RetentionSweep(7)
	require.NoError(t, err)
	assert.Zero(t, removed, "a second sweep must remove nothing else")
	assert.ElementsMatch(t, []string{first, second}, notified, "a later sweep must not re-notify an already deleted session")
}

func retentionDeletedCasePartialPrune(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	id := retentionDeletedNewChat(t, store)
	aged := createSessionFile(t, store, id, "transcript.jsonl", 30*24*time.Hour)
	fresh := createSessionFile(t, store, id, "recent.jsonl", 0)
	var notified []string
	store.SetSessionDeletedObserver(func(id string) { notified = append(notified, id) })

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	assert.Equal(t, 1, removed, "the aged file must really be pruned")
	_, err = os.Stat(aged)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(fresh)
	require.NoError(t, err, "the fresh transcript must keep its session alive")
	_, err = store.GetMeta(id)
	require.NoError(t, err)
	assert.Empty(t, notified, "pruning one old file is NOT whole-session deletion; its pending questions must not be cleared")
}

func retentionDeletedCaseMainSurvives(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	main, err := store.GetOrCreateMainSession("ws-retention-1329", "mia")
	require.NoError(t, err)
	transcript := createSessionFile(t, store, main.ID, "transcript.jsonl", 30*24*time.Hour)
	var notified []string
	store.SetSessionDeletedObserver(func(id string) { notified = append(notified, id) })

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	assert.Zero(t, removed, "a protected main's live transcript must not be removed")
	_, err = os.Stat(transcript)
	require.NoError(t, err)
	assert.Empty(t, notified, "an undeleted main must never trigger question cleanup")
}

func retentionDeletedCaseDisabled(t *testing.T, days int) {
	store := newUnifiedStoreForTest(t)
	id := retentionDeletedNewChat(t, store)
	transcript := createSessionFile(t, store, id, "transcript.jsonl", 30*24*time.Hour)
	var notified []string
	store.SetSessionDeletedObserver(func(id string) { notified = append(notified, id) })

	removed, err := store.RetentionSweep(days)
	require.NoError(t, err)
	assert.Zero(t, removed)
	_, err = os.Stat(transcript)
	require.NoError(t, err, "disabled retention must leave the aged session intact")
	assert.Empty(t, notified, "disabled retention must not clear any question state")
}
