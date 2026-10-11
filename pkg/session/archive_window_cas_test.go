// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

// S3 H4 / QA-CHECK Q03: commitWindow's "the window changed while staging"
// refusal lost its only tests when pkg/memory/window_concurrent_test.go was
// deleted with the retired dense snapshot API. The comparison and the
// memory.ErrWindowChanged sentinel survived into the canonical archive backend,
// so the contract is re-homed here against the CURRENT API.
//
// Oracles (memory/window.go::ErrWindowChanged and
// archive_backend.go::commitWindow's two independent guards):
//   - a `before` snapshot that is no longer current is refused with the SPECIFIC
//     ErrWindowChanged sentinel, never a generic cursor error;
//   - the refusal changes nothing: a concurrent append is never lost and a
//     refused restore applies no Skip move;
//   - the companion ordering: an accurate `before` restores, and a later append
//     still lands on the restored state.
//
// Asserting the SENTINEL and not merely "some error" is what gives these tests
// their power: commitWindow has a second, independent Count guard that also
// refuses a stale restore, but with a different, non-sentinel error. A caller
// doing errors.Is(err, ErrWindowChanged) — the only documented way to detect
// "the window changed concurrently" — would silently misclassify it.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// casAppendModel appends one model record through the checked model seam so it
// takes a real ordinal, exactly as a turn does.
func casAppendModel(t *testing.T, store *UnifiedStore, key string, msg providers.Message) {
	t.Helper()
	_, _, err := store.AppendModelMessage(context.Background(), key, ModelAppend{
		Message:        msg,
		ViewMembership: ViewMembershipBoth,
		Source:         EntrySource{Kind: "user"},
		AgentID:        "mia",
	})
	require.NoError(t, err, "fixture: checked model append")
}

func casNewStore(t *testing.T) (*UnifiedStore, context.Context) {
	t.Helper()
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err, "fixture: open a real archive store")
	t.Cleanup(func() { _ = store.Close() })
	return store, context.Background()
}

func casSeed(t *testing.T, store *UnifiedStore, key string) []providers.Message {
	t.Helper()
	seed := []providers.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
	}
	for _, m := range seed {
		casAppendModel(t, store, key, m)
	}
	return seed
}

// TestArchiveWindow_ConcurrentAppendWins_StaleRestoreRefused is the append-wins
// ordering: a real append lands between the snapshot and the restore attempt.
func TestArchiveWindow_ConcurrentAppendWins_StaleRestoreRefused(t *testing.T) {
	store, ctx := casNewStore(t)
	const key = "cw-cas-append-wins"
	seed := casSeed(t, store, key)

	before, err := store.WindowView(ctx, key)
	require.NoError(t, err)
	require.Equal(t, len(seed), before.State.Count, "precondition: the snapshot sees every seeded record")
	require.Zero(t, before.State.Skip, "precondition: nothing is evicted yet")

	// The plausible metadata-only slide (Skip 0 -> 1) a caller would compute
	// from the now-stale snapshot.
	restoreTarget := before.State.Clone()
	restoreTarget.Skip = 1

	appendDone := make(chan struct{})
	go func() {
		defer close(appendDone)
		store.AddFullMessage(key, providers.Message{Role: "assistant", Content: "concurrent append"})
	}()
	<-appendDone

	restoreErr := store.RestoreWindow(ctx, key, before.State, restoreTarget)
	require.Error(t, restoreErr, "a restore against a stale snapshot must be refused")
	require.ErrorIs(t, restoreErr, memory.ErrWindowChanged,
		"the refusal must carry the documented ErrWindowChanged sentinel, not a generic cursor error")

	after, err := store.WindowView(ctx, key)
	require.NoError(t, err)
	require.Equal(t, len(seed)+1, after.State.Count, "the concurrent append must not be lost")
	require.Len(t, after.Live, len(seed)+1, "the archive must retain the concurrent append")
	require.Zero(t, after.State.Skip, "a refused restore must not apply its Skip move")
}

// TestArchiveWindow_ConcurrentSkipMove_StaleRestoreRefused isolates the
// snapshot COMPARISON: a concurrent commit moves Skip without changing Count,
// so only the saved-snapshot comparison can refuse the stale restore.
func TestArchiveWindow_ConcurrentSkipMove_StaleRestoreRefused(t *testing.T) {
	store, ctx := casNewStore(t)
	const key = "cw-cas-skip-moved"
	casSeed(t, store, key)

	before, err := store.WindowView(ctx, key)
	require.NoError(t, err)

	// A concurrent caller advances the window by one record.
	moved := before.State.Clone()
	moved.Skip = 1
	require.NoError(t, store.CommitWindow(ctx, key, before.State, moved),
		"fixture: the concurrent commit must land")

	// The stale caller still holds `before` and tries a restore that changes
	// nothing about Count — only the saved-snapshot comparison can see this.
	stale := before.State.Clone()
	stale.Skip = 2
	restoreErr := store.RestoreWindow(ctx, key, before.State, stale)
	require.Error(t, restoreErr,
		"a restore built from a superseded snapshot must be refused even when Count still matches")
	require.ErrorIs(t, restoreErr, memory.ErrWindowChanged,
		"the refusal must carry the ErrWindowChanged sentinel")

	after, err := store.WindowView(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 1, after.State.Skip, "the refused restore must not move Skip")
	require.Equal(t, before.State.Count, after.State.Count, "the refused restore must not change Count")
}

// TestArchiveWindow_AccurateSnapshotRestores_ThenAppendLands is the companion
// ordering: serialized correctly, no corruption, in either order.
func TestArchiveWindow_AccurateSnapshotRestores_ThenAppendLands(t *testing.T) {
	store, ctx := casNewStore(t)
	const key = "cw-cas-restore-wins"
	seed := casSeed(t, store, key)

	before, err := store.WindowView(ctx, key)
	require.NoError(t, err)
	restoreTarget := before.State.Clone()
	restoreTarget.Skip = 1

	require.NoError(t, store.RestoreWindow(ctx, key, before.State, restoreTarget),
		"an accurate `before` snapshot must restore")

	store.AddFullMessage(key, providers.Message{Role: "assistant", Content: "append after restore"})

	after, err := store.WindowView(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 1, after.State.Skip, "the won restore's Skip move must be applied")
	require.Equal(t, len(seed)+1, after.State.Count, "the post-restore append must land")
	require.Len(t, after.Live, len(seed), "the live view holds the records after the moved Skip")
}
