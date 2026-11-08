package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Oracle: chief's no-op activity requirement. A different person cannot adopt
// an already-owned main, and must not rewrite meta.json, even with the same
// bytes. A fixed old filesystem mtime makes a redundant write observable
// without sleeps or assumptions about how quickly two calls can complete.
func TestMainOwnerActivity_DifferentOwnerNoOpPreservesFileBytesAndModTime(t *testing.T) {
	store := newTestStore(t)
	main, err := requireMainOwnerCreator(t, store).GetOrCreateMainSessionOwned("owner-workspace", "mia", "alice")
	require.NoError(t, err, "seed a main already owned by alice")
	require.Equal(t, "alice", main.Owner, "fixture must already have the winning owner")
	metaPath := filepath.Join(store.BaseDir(), main.ID, "meta.json")

	// Arbitrary whole-second historical fixture time: a rewrite to today's
	// clock cannot hide behind filesystem timestamp resolution.
	oldModTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(metaPath, oldModTime, oldModTime), "install a distinguishable pre-call mtime")
	beforeInfo, err := os.Stat(metaPath)
	require.NoError(t, err, "stat metadata before the no-op")
	require.True(t, oldModTime.Equal(beforeInfo.ModTime()), "filesystem must preserve the fixture mtime for this check to detect a write")
	beforeBytes, err := os.ReadFile(metaPath)
	require.NoError(t, err, "read exact metadata bytes before the no-op")

	adopted, err := requireMainOwnerAdopter(t, store).AdoptMainSessionOwner(main.ID, "bob")
	require.NoError(t, err, "a different person's adoption of an owned main is a harmless no-op")
	require.Equal(t, false, adopted, "bob must not report a second adoption")
	afterBytes, err := os.ReadFile(metaPath)
	require.NoError(t, err, "metadata file still exists after the no-op")
	require.Equal(t, beforeBytes, afterBytes, "no-op adoption cannot change any meta.json byte")
	afterInfo, err := os.Stat(metaPath)
	require.NoError(t, err, "stat metadata after the no-op")
	require.True(t, beforeInfo.ModTime().Equal(afterInfo.ModTime()), "no-op adoption must not rewrite meta.json, even if bytes would be identical")
	require.Equal(t, "alice", mainOwnerReopenMeta(t, store, main.ID).Owner, "the original owner remains durable")
}
