// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FR-006 / DEL-12 non-destructive rollback — the append-only exclusion oracle.
//
// The RED oracle (sessioncore_u2_archive_test.go::
// TestSessionCoreU2_RollbackNeverRewritesRetainedArchiveBytes) proves the
// retained bytes survive a rollback. This file proves the OTHER half of the
// same requirement: the aborted span is excluded from the model window, AND a
// later valid append lands above that span and is visible again. Every
// expected value comes from the FR-006 sentence, never from the implementation:
//
//	"Clear/correction/rollback/projection MUST append effects or move
//	 view/window metadata, never rewrite retained bytes."

package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// modelWindowContents returns the content of every message in the live model
// window (GetHistory — what a provider request is built from).
func modelWindowContents(t *testing.T, store *JSONLStore, key string) []string {
	t.Helper()
	msgs, err := store.GetHistory(context.Background(), key)
	require.NoError(t, err)
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Content)
	}
	return out
}

func TestRollbackWindow_ExcludesAbortedSpanAndKeepsLaterAppends(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	const key = "sess-span"

	require.NoError(t, store.AddMessage(ctx, key, "user", "m1"))
	require.NoError(t, store.AddMessage(ctx, key, "user", "m2"))
	snap, err := store.SnapshotWindow(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 2, snap.State.Count, "instrument control: snapshot sees 2 records")

	// The aborted turn appends m3 and m4 — the span to exclude.
	require.NoError(t, store.AddMessage(ctx, key, "assistant", "m3"))
	require.NoError(t, store.AddMessage(ctx, key, "tool", "m4"))

	rawPath := filepath.Join(dir, key+".jsonl")
	before, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	require.Equal(t, 4, lineCount(before), "instrument control: 4 records on disk before rollback")

	require.NoError(t, store.RollbackWindow(ctx, key, snap.State))

	// The retained bytes survive byte-for-byte (append-only).
	after, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(after), string(before)),
		"FR-006: rollback must never rewrite retained archive bytes")

	// The aborted span is excluded from the model window.
	require.Equal(t, []string{"m1", "m2"}, modelWindowContents(t, store, key),
		"FR-006: the aborted span must be excluded from the model window")

	// A LATER valid append must be visible again, above the excluded span — the
	// aborted span neither resurrects nor blocks new content.
	require.NoError(t, store.AddMessage(ctx, key, "user", "m5"))
	require.Equal(t, []string{"m1", "m2", "m5"}, modelWindowContents(t, store, key),
		"FR-006: a later valid append must be visible after an aborted span")

	// ReadArchive still returns every byte, including the excluded span.
	full, err := store.ReadArchive(ctx, key)
	require.NoError(t, err)
	require.Len(t, full, 5, "the archive is append-only: all five records stay on disk")
}

func TestRetractSpan_CoalescesOverlappingRanges(t *testing.T) {
	got := retractSpan(nil, 2, 4)
	require.Equal(t, []ArchiveSpan{{Start: 2, End: 4}}, got)
	got = retractSpan(got, 4, 6)
	require.Equal(t, []ArchiveSpan{{Start: 2, End: 6}}, got, "abutting spans coalesce")
	got = retractSpan(got, 1, 3)
	require.Equal(t, []ArchiveSpan{{Start: 1, End: 6}}, got, "overlapping spans coalesce")
	require.Nil(t, retractSpan(nil, 5, 5), "an empty span is a no-op")
}
