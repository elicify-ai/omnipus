// websocket_replay_test.go: tests for attach to a session and replay its history.

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from websocket.go tests 2026-09-15 ---

// ─────────────────────────────────────────────────────────────────────────────
// Work item A — applySinceCursor
// ─────────────────────────────────────────────────────────────────────────────

// TestApplySinceCursor_NilSince verifies that a nil since returns all entries.
func TestApplySinceCursor_NilSince(t *testing.T) {
	entries := []session.TranscriptEntry{
		{Timestamp: time.Now()},
		{Timestamp: time.Now().Add(time.Second)},
	}
	got := applySinceCursor(context.Background(), "sid", nil, entries, nil)
	assert.Len(t, got, 2, "nil since must return all entries")
}

// TestApplySinceCursor_EmptySince verifies that an empty string since returns all entries.
func TestApplySinceCursor_EmptySince(t *testing.T) {
	entries := []session.TranscriptEntry{
		{Timestamp: time.Now()},
	}
	empty := ""
	got := applySinceCursor(context.Background(), "sid", &empty, entries, nil)
	assert.Len(t, got, 1, "empty since must return all entries")
}

// TestApplySinceCursor_ValidCursor verifies that entries at or before the cursor are filtered out.
func TestApplySinceCursor_ValidCursor(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	entries := []session.TranscriptEntry{
		{ID: "before", Timestamp: base.Add(-time.Second)},    // before cursor — skip
		{ID: "at", Timestamp: base},                          // at cursor — skip (<=)
		{ID: "after1", Timestamp: base.Add(time.Second)},     // after cursor — keep
		{ID: "after2", Timestamp: base.Add(2 * time.Second)}, // after cursor — keep
	}
	cursorStr := base.Format(time.RFC3339Nano)
	got := applySinceCursor(context.Background(), "sid", &cursorStr, entries, nil)
	require.Len(t, got, 2, "only entries after cursor must be returned")
	assert.Equal(t, "after1", got[0].ID)
	assert.Equal(t, "after2", got[1].ID)
}

// TestApplySinceCursor_FutureCursor verifies that a future cursor returns an empty slice.
func TestApplySinceCursor_FutureCursor(t *testing.T) {
	entries := []session.TranscriptEntry{
		{Timestamp: time.Now().Add(-time.Hour)},
		{Timestamp: time.Now().Add(-30 * time.Minute)},
	}
	future := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	got := applySinceCursor(context.Background(), "sid", &future, entries, nil)
	assert.Empty(t, got, "future cursor must return empty slice")
}

// TestApplySinceCursor_InvalidCursor verifies that a malformed since string falls through
// to a full replay (returns original entries unchanged).
func TestApplySinceCursor_InvalidCursor(t *testing.T) {
	entries := []session.TranscriptEntry{
		{Timestamp: time.Now()},
		{Timestamp: time.Now().Add(time.Second)},
	}
	bad := "not-a-timestamp"
	got := applySinceCursor(context.Background(), "sid", &bad, entries, nil)
	assert.Len(t, got, 2, "invalid cursor must return all entries (full replay fallback)")
}

// TestApplySinceCursor_RFC3339Fallback verifies that RFC3339 (no sub-second precision)
// is accepted when RFC3339Nano fails.
func TestApplySinceCursor_RFC3339Fallback(t *testing.T) {
	base := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
	entries := []session.TranscriptEntry{
		{ID: "old", Timestamp: base.Add(-time.Minute)},
		{ID: "new", Timestamp: base.Add(time.Minute)},
	}
	// RFC3339 format has no nanoseconds — RFC3339Nano parse would fail.
	cursor := base.UTC().Format(time.RFC3339)
	got := applySinceCursor(context.Background(), "sid", &cursor, entries, nil)
	require.Len(t, got, 1)
	assert.Equal(t, "new", got[0].ID, "RFC3339 cursor must filter correctly")
}
