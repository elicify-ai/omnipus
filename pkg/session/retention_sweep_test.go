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
	// See issue #634 / u2NewTestStore's doc comment (unified_api_adr057_test.go):
	// without this, the store's background stats-flusher goroutine (started
	// unconditionally by the constructor) outlives this test and can pollute
	// a later test's FR-101 lock-recorder trace.
	t.Cleanup(func() { _ = store.Close() })
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

// createContextFile creates a .context/<sessionID>.jsonl file with the given
// age, mirroring what UnifiedStore writes for the context archive.
func createContextFile(t *testing.T, store *UnifiedStore, sessionID string, age time.Duration) string {
	t.Helper()
	contextDir := filepath.Join(store.baseDir, ".context")
	require.NoError(t, os.MkdirAll(contextDir, 0o700))
	filePath := filepath.Join(contextDir, sessionID+".jsonl")
	require.NoError(t, os.WriteFile(filePath, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o600))
	mtime := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(filePath, mtime, mtime))
	return filePath
}

// TestRetention_SweepsIdleContext_SparesActive (T20) verifies FR-012 / US-7:
//
//	(a) A session whose .context/<id>.jsonl is older than the retention cutoff IS swept.
//	(b) A session written "now" (fresh mtime) is SPARED even though it has a .context/ archive.
//
// The ModTime-based logic that spares active sessions is the same mechanism used
// for regular transcript .jsonl files — no special .context/ exemption applies.
func TestRetention_SweepsIdleContext_SparesActive(t *testing.T) {
	store := newUnifiedStoreForTest(t)

	const retentionDays = 90

	// (a) Idle session: context archive is 100 days old — beyond the 90-day window.
	// Also create a matching transcript file that is equally aged so the session dir
	// gets swept too; this exercises the full sweep including context cleanup.
	idleContextPath := createContextFile(t, store, "sess-idle", 100*24*time.Hour)
	createSessionFile(t, store, "sess-idle", "2026-01-01.jsonl", 100*24*time.Hour)

	// (b) Active session: context archive has a fresh mtime (written "now").
	activeContextPath := createContextFile(t, store, "sess-active", 0)
	// The active session also has a fresh transcript.
	createSessionFile(t, store, "sess-active", "2026-05-01.jsonl", 0)

	removed, err := store.RetentionSweep(retentionDays)
	require.NoError(t, err)

	// (a) The idle context archive must be gone.
	_, statErr := os.Stat(idleContextPath)
	assert.True(t, os.IsNotExist(statErr),
		"idle .context/<id>.jsonl (100d old) must be swept at the %d-day retention window", retentionDays)

	// (b) The active context archive must survive.
	_, statErr = os.Stat(activeContextPath)
	assert.NoError(t, statErr,
		"active .context/<id>.jsonl (fresh mtime) must be spared by the sweep")

	// The idle session transcript was also swept; the removed count must be >= 2
	// (the transcript + context file for the idle session, at minimum).
	assert.GreaterOrEqual(t, removed, 2,
		"sweep must count both the idle transcript and idle context archive as removed")
}

// createContextMetaFile creates a .context/<sessionID>.meta.json file alongside
// a context .jsonl, mirroring the sidecar written by memory.JSONLStore.
func createContextMetaFile(t *testing.T, store *UnifiedStore, sessionID string, age time.Duration) string {
	t.Helper()
	contextDir := filepath.Join(store.baseDir, ".context")
	require.NoError(t, os.MkdirAll(contextDir, 0o700))
	metaPath := filepath.Join(contextDir, sessionID+".meta.json")
	require.NoError(t, os.WriteFile(metaPath,
		[]byte(`{"key":"`+sessionID+`","skip":0,"count":3}`+"\n"), 0o600))
	mtime := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(metaPath, mtime, mtime))
	return metaPath
}

// TestRetention_ContextMetaRemovedWithJsonl (T21) verifies that when an aged
// .context/<key>.jsonl is swept, its sibling .context/<key>.meta.json (which
// holds the Skip/Count offset for memory.JSONLStore) is removed at the same
// time. Without this, a recycled session key would read a stale Skip/Count
// against a now-empty .jsonl and produce a phantom offset.
//
// Also asserts that the .meta.json of a SPARED (active) context .jsonl is
// left untouched.
func TestRetention_ContextMetaRemovedWithJsonl(t *testing.T) {
	store := newUnifiedStoreForTest(t)

	const retentionDays = 90

	// --- aged session: both .jsonl and .meta.json are old ---
	agedJSONL := createContextFile(t, store, "ctx-aged", 100*24*time.Hour)
	agedMeta := createContextMetaFile(t, store, "ctx-aged", 100*24*time.Hour)

	// --- active session: .jsonl and .meta.json are fresh ---
	activeJSONL := createContextFile(t, store, "ctx-active", 0)
	activeMeta := createContextMetaFile(t, store, "ctx-active", 0)

	removed, err := store.RetentionSweep(retentionDays)
	require.NoError(t, err)
	// Only the aged .jsonl counts toward the removed tally (meta removal is
	// a side-effect, not an independently counted file).
	assert.Equal(t, 1, removed, "only the aged .context/<key>.jsonl is counted as removed")

	// Aged .jsonl must be gone.
	_, err = os.Stat(agedJSONL)
	assert.True(t, os.IsNotExist(err), "aged .context/<key>.jsonl must be swept")

	// Sibling .meta.json for the aged .jsonl must also be gone.
	_, err = os.Stat(agedMeta)
	assert.True(t, os.IsNotExist(err),
		"aged .context/<key>.meta.json must be removed alongside its .jsonl")

	// Active .jsonl must survive.
	_, err = os.Stat(activeJSONL)
	assert.NoError(t, err, "active .context/<key>.jsonl must not be swept")

	// Active .meta.json must also survive.
	_, err = os.Stat(activeMeta)
	assert.NoError(t, err, "active .context/<key>.meta.json must not be removed")
}
