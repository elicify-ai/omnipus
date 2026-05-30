package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newUnifiedStoreForTest(t *testing.T) *UnifiedStore {
	t.Helper()
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err, "NewUnifiedStore must succeed")
	return store
}

func createSessionFile(t *testing.T, store *UnifiedStore, sessionID, filename string, age time.Duration) string {
	t.Helper()
	sessionDir := filepath.Join(store.baseDir, sessionID)
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	filePath := filepath.Join(sessionDir, filename)
	require.NoError(t, os.WriteFile(filePath, []byte(`{"id":"test"}`+"\n"), 0o600))
	mtime := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(filePath, mtime, mtime))
	return filePath
}

// TestRetentionSweep_DeletesAgedFiles creates 3 session files aged 3/10/30 days,
// calls RetentionSweep(7), and asserts that only the 3-day file survives.
func TestRetentionSweep_DeletesAgedFiles(t *testing.T) {
	store := newUnifiedStoreForTest(t)

	recent := createSessionFile(t, store, "sess-a", "2026-04-20.jsonl", 3*24*time.Hour)
	stale1 := createSessionFile(t, store, "sess-b", "2026-04-12.jsonl", 10*24*time.Hour)
	stale2 := createSessionFile(t, store, "sess-c", "2026-03-23.jsonl", 30*24*time.Hour)

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	assert.Equal(t, 2, removed, "two files older than 7 days must be deleted")

	_, err = os.Stat(recent)
	assert.NoError(t, err, "recent file (3 days old) must survive RetentionSweep(7)")

	_, err = os.Stat(stale1)
	assert.True(t, os.IsNotExist(err), "10-day-old file must be deleted")

	_, err = os.Stat(stale2)
	assert.True(t, os.IsNotExist(err), "30-day-old file must be deleted")
}

// TestRetentionSweep_ZeroRetentionIsNoOp verifies that retentionDays <= 0
// is a no-op that returns (0, nil) without touching any file.
func TestRetentionSweep_ZeroRetentionIsNoOp(t *testing.T) {
	store := newUnifiedStoreForTest(t)

	filePath := createSessionFile(t, store, "sess-x", "2025-01-01.jsonl", 365*24*time.Hour)

	removed, err := store.RetentionSweep(0)
	require.NoError(t, err)
	assert.Equal(t, 0, removed, "RetentionSweep(0) must be a no-op")

	_, statErr := os.Stat(filePath)
	assert.NoError(t, statErr, "file must not be deleted when retentionDays == 0")
}

// TestRetentionSweep_EmptyStore verifies that sweeping an empty store returns (0, nil).
func TestRetentionSweep_EmptyStore(t *testing.T) {
	store := newUnifiedStoreForTest(t)

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	assert.Equal(t, 0, removed, "empty store must return 0 removed files")
}

// TestRetentionSweep_PartialDeleteFailureContinues verifies that when one file
// cannot be deleted the sweep continues and processes remaining files.
//
// We simulate an undeletable file by replacing a target .jsonl path with a
// directory of the same name (os.Remove on a non-empty directory fails).
func TestRetentionSweep_PartialDeleteFailureContinues(t *testing.T) {
	store := newUnifiedStoreForTest(t)

	// Create a stale file that can be deleted.
	deletable := createSessionFile(t, store, "sess-del", "2026-01-01.jsonl", 30*24*time.Hour)

	// Simulate an undeletable file: create a session directory, then put a
	// sub-directory where the .jsonl file would be so os.Remove fails.
	sessionDir := filepath.Join(store.baseDir, "sess-nodeleate")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	fakePath := filepath.Join(sessionDir, "2026-01-01.jsonl")
	require.NoError(t, os.MkdirAll(fakePath, 0o700)) // directory, not a file
	// Backdate the directory so WalkDir sees it as stale; we need its DirEntry to
	// report a .jsonl suffix — WalkDir reports the name of the entry, so this works.
	mtime := time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(fakePath, mtime, mtime))

	// Put a child inside so os.Remove fails with "directory not empty".
	require.NoError(t, os.WriteFile(filepath.Join(fakePath, "child"), []byte("x"), 0o600))

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err, "sweep must not abort on a partial delete failure")

	// The deletable file must be gone.
	_, statErr := os.Stat(deletable)
	assert.True(t, os.IsNotExist(statErr), "deletable file must be removed")

	// removed may be 1 (only the file that succeeded).
	assert.Equal(t, 1, removed, "only the successfully deleted file must be counted")
}

// TestRetentionSweep_RemovesEmptySessionDir verifies that after the per-file
// sweep deletes the only .jsonl in a session directory, the now-empty session
// directory itself is removed even though Linux bumps the directory's mtime
// to "now" the moment a child file is removed (which would defeat any
// post-deletion mtime-based check).
//
// Regression coverage for the retention:201 e2e failure: an aged session with
// a backdated meta.json and a backdated transcript.jsonl had its .jsonl swept
// but the sidecar metadata stayed behind, leaving a content-less ghost session
// in the listing.
func TestRetentionSweep_RemovesEmptySessionDir(t *testing.T) {
	store := newUnifiedStoreForTest(t)

	// Aged transcript that the sweep will delete.
	transcript := createSessionFile(t, store, "sess-aged", "2026-01-01.jsonl", 100*24*time.Hour)
	sessionDir := filepath.Dir(transcript)

	// Sidecar metadata in the same dir, also backdated. Mirrors what the e2e
	// fixture in tests/e2e/fixtures/aging.ts produces (meta.json next to a
	// backdated .jsonl).
	metaPath := filepath.Join(sessionDir, "meta.json")
	require.NoError(t, os.WriteFile(metaPath, []byte(`{"id":"sess-aged"}`), 0o600))
	mtime := time.Now().Add(-100 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(metaPath, mtime, mtime))

	removed, err := store.RetentionSweep(90)
	require.NoError(t, err)
	assert.Equal(t, 1, removed, "the aged .jsonl must be deleted")

	// The session directory itself must be gone — meta.json was the only
	// remaining file and there are no .jsonl transcripts left, so the dir
	// is junk by definition.
	_, statErr := os.Stat(sessionDir)
	assert.True(t, os.IsNotExist(statErr),
		"session directory must be removed when no .jsonl files remain (got: %v)", statErr)
}

// TestRetentionSweep_KeepsDirWithLiveTranscript verifies that a session
// directory with at least one fresh (not-aged) .jsonl is retained even when
// other .jsonl files in the same dir were aged and swept.
func TestRetentionSweep_KeepsDirWithLiveTranscript(t *testing.T) {
	store := newUnifiedStoreForTest(t)

	// One aged transcript (gets swept).
	createSessionFile(t, store, "sess-mixed", "2026-01-01.jsonl", 100*24*time.Hour)
	// One fresh transcript (must survive).
	freshPath := createSessionFile(t, store, "sess-mixed", "2026-05-01.jsonl", 5*24*time.Hour)
	sessionDir := filepath.Dir(freshPath)

	removed, err := store.RetentionSweep(90)
	require.NoError(t, err)
	assert.Equal(t, 1, removed, "only the aged .jsonl must be deleted")

	// Session directory must still exist because it has a live transcript.
	_, statErr := os.Stat(sessionDir)
	assert.NoError(t, statErr, "session dir must be retained when at least one .jsonl remains")

	// The fresh transcript must still be there.
	_, statErr = os.Stat(freshPath)
	assert.NoError(t, statErr, "fresh .jsonl must not be deleted")
}
