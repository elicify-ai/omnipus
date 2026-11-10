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
	"sync"
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

	// A REAL interleaving, not an ordering (R1 delta T6): both operations are
	// released together on a barrier, so the scheduler decides who reaches the
	// CAS first. The oracle holds under EVERY interleaving:
	//   - the append is never lost;
	//   - whichever operation landed first wins, and the other is refused with
	//     the documented ErrWindowChanged sentinel.
	// The previous form joined the append goroutine BEFORE calling
	// RestoreWindow, which is a strictly serial ordering and could never
	// exercise the comparison the CAS exists for.
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var restoreErr error
	go func() {
		defer wg.Done()
		<-start
		store.AddFullMessage(key, providers.Message{Role: "assistant", Content: "concurrent append"})
	}()
	go func() {
		defer wg.Done()
		<-start
		restoreErr = store.RestoreWindow(ctx, key, before.State, restoreTarget)
	}()
	close(start)
	wg.Wait()

	after, err := store.WindowView(ctx, key)
	require.NoError(t, err)
	require.Equal(t, len(seed)+1, after.State.Count,
		"the concurrent append must not be lost, whichever operation reached the CAS first")

	if restoreErr == nil {
		// The restore reached the CAS first: it applied, and the append landed
		// after it — the window keeps the restore's Skip move, so the live view
		// is one slot shorter even though the archive still holds every record.
		require.Equal(t, 1, after.State.Skip,
			"a restore that won the race must have applied its Skip move")
		require.Len(t, after.Live, len(seed)+1-1,
			"the archive retains the append; the restore's own Skip move evicts one slot from the live view")
	} else {
		// The append won: the restore is refused, and it must say why.
		require.ErrorIs(t, restoreErr, memory.ErrWindowChanged,
			"the refusal must carry the documented ErrWindowChanged sentinel, not a generic cursor error")
		require.Zero(t, after.State.Skip, "a refused restore must not apply its Skip move")
		require.Len(t, after.Live, len(seed)+1, "the archive must retain the concurrent append")
	}
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

	// A REAL interleaving (R1 delta T6): both moves are built from the SAME
	// snapshot and released together, so they race for the CAS. Count never
	// changes, so only the saved-snapshot comparison can refuse the loser.
	moved := before.State.Clone()
	moved.Skip = 1
	stale := before.State.Clone()
	stale.Skip = 2

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var commitErr, restoreErr error
	go func() {
		defer wg.Done()
		<-start
		commitErr = store.CommitWindow(ctx, key, before.State, moved)
	}()
	go func() {
		defer wg.Done()
		<-start
		restoreErr = store.RestoreWindow(ctx, key, before.State, stale)
	}()
	close(start)
	wg.Wait()

	// Exactly one of the two may land: the winner matched the snapshot, so the
	// loser's CAS must be refused with the documented sentinel.
	winners := 0
	for _, err := range []error{commitErr, restoreErr} {
		if err == nil {
			winners++
			continue
		}
		require.ErrorIs(t, err, memory.ErrWindowChanged,
			"the loser of the snapshot CAS must carry ErrWindowChanged, got %v", err)
	}
	require.Equal(t, 1, winners,
		"exactly one move may land (commitErr=%v restoreErr=%v)", commitErr, restoreErr)

	after, err := store.WindowView(ctx, key)
	require.NoError(t, err)
	require.Equal(t, before.State.Count, after.State.Count, "neither move may change Count")
	require.Contains(t, []int{1, 2}, after.State.Skip,
		"Skip must be exactly the winner's move, got %d", after.State.Skip)
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
