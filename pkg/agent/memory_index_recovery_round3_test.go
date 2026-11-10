package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/memrooms"
	memindex "github.com/elicify-ai/omnipus/pkg/memrooms/index"
	"github.com/stretchr/testify/require"
)

// Replace only the directory entry with an unusable regular file. Restore the
// original notes and EXACT failing-path timestamp; no note is written on repair.
func round3BlockDirectory(t *testing.T, dir string) (func(), time.Time) {
	t.Helper()
	backup := dir + ".repair-source"
	require.NoError(t, os.Rename(dir, backup))
	require.NoError(t, os.WriteFile(dir, []byte("temporarily not a directory"), 0o600))
	stamp := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(dir, stamp, stamp))
	_, err := os.ReadDir(dir)
	require.Error(t, err, "positive control: directory scan really fails")
	info, err := os.Stat(dir)
	require.NoError(t, err, "fault leaves metadata stat accessible")
	stamp = info.ModTime()
	return func() {
		t.Helper()
		require.NoError(t, os.Remove(dir))
		require.NoError(t, os.Rename(backup, dir))
		require.NoError(t, os.Chtimes(dir, stamp, stamp))
		repaired, statErr := os.Stat(dir)
		require.NoError(t, statErr)
		require.True(t, repaired.ModTime().Equal(stamp), "repair must not provide a new synchronization signal")
	}, stamp
}

func round3InitialIndexBuild(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	room := ms.PrivateRoom()
	round3WriteNote(t, room.MemoriesDir, "round3-existing", "EXISTING_NOTE")
	repair, _ := round3BlockDirectory(t, room.MemoriesDir)
	ri, err := memindex.OpenOrCreate(room)
	if ri != nil {
		t.Cleanup(func() { require.NoError(t, ri.Close()) })
	}
	require.Error(t, err, "failed initial population must not be returned as a ready empty index")
	require.Nil(t, ri, "a rejected initial handle cannot enter the shared registry")
	repair()
	recovered, err := memindex.OpenOrCreate(room)
	require.NoError(t, err, "rejected handle must have released its underlying file lock")
	t.Cleanup(func() { require.NoError(t, recovered.Close()) })
	hits, err := recovered.Search("roomnote", 20)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "round3-existing", hits[0].ID)
	require.Greater(t, hits[0].Score, 0.0)
}

func round3InitialStoreRetry(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	room := ms.PrivateRoom()
	round3WriteNote(t, room.MemoriesDir, "round3-existing", "EXISTING_NOTE")
	repair, _ := round3BlockDirectory(t, room.MemoriesDir)
	entries, err := ms.SearchEntries("roomnote", 20)
	require.Error(t, err)
	require.Empty(t, entries)
	require.NotContains(t, ms.indexCache, room.Root, "preflight must reject an unreadable directory before opening an index")
	_, indexErr := os.Stat(filepath.Join(room.Root, memindex.IndexSubdir))
	require.ErrorIs(t, indexErr, os.ErrNotExist, "unreadable source preflight must precede derived-index creation")
	_, synced := ms.indexedDirMtime[room.MemoriesDir]
	require.False(t, synced, "failed population cannot be recorded as synchronized")
	repair()
	entries, err = ms.SearchEntries("roomnote", 20)
	require.NoError(t, err)
	require.Equal(t, []string{"round3-existing"}, round3EntryIDs(entries), "repair + retry must find the original note without another write")
}

// A new teammate's store must not bless a process-shared handle whose last
// rebuild failed in another store. The same room has one registry handle.
func round3SharedHandleRetry(t *testing.T) {
	ms, home := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	room := ms.SharedRoom(ms.scopeForWorkspace("alpha"))
	require.NotNil(t, room)
	round3WriteNote(t, room.MemoriesDir, "round3-first", "FIRST_NOTE")
	entries, err := ms.SearchEntriesInScope("roomnote", 20, memrooms.RoomScopeShared, ms.scopeForWorkspace("alpha"))
	require.NoError(t, err)
	require.Equal(t, []string{"round3-first"}, round3EntryIDs(entries))
	round3WriteNote(t, room.MemoriesDir, "round3-added", "addedtoken")
	repair, _ := round3BlockDirectory(t, room.MemoriesDir)
	ms.indexMu.Lock()
	failed := ms.roomIndexLocked(*room)
	ms.indexMu.Unlock()
	require.Nil(t, failed)
	repair()
	teammate := NewMemoryStore(filepath.Join(home, "agents", "teammate"), home)
	t.Cleanup(teammate.Close)
	entries, err = teammate.SearchEntriesInScope("addedtoken", 20, memrooms.RoomScopeShared, teammate.scopeForWorkspace("alpha"))
	require.NoError(t, err)
	require.Equal(t, []string{"round3-added"}, round3EntryIDs(entries), "a new store must rebuild before recording the shared handle as synchronized")
}

func round3RefreshRetry(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	room := ms.PrivateRoom()
	round3WriteNote(t, room.MemoriesDir, "round3-first", "FIRST_NOTE")
	entries, err := ms.SearchEntries("roomnote", 20)
	require.NoError(t, err)
	require.Equal(t, []string{"round3-first"}, round3EntryIDs(entries))
	require.NotNil(t, ms.indexCache[room.Root])
	// This file exists before the fault. It must be recovered by rebuild, not
	// by a new write made after the operator repairs storage.
	round3WriteNote(t, room.MemoriesDir, "round3-added", "addedtoken")
	repair, failedStamp := round3BlockDirectory(t, room.MemoriesDir)
	ms.indexMu.Lock()
	failed := ms.roomIndexLocked(room)
	baseline, synced := ms.indexedDirMtime[room.MemoriesDir]
	ms.indexMu.Unlock()
	require.Nil(t, failed, "failed refresh must not expose a stale index as usable")
	require.False(t, synced && baseline.Equal(failedStamp), "failed rebuild cannot advance the sync baseline")
	repair()
	entries, err = ms.SearchEntries("addedtoken", 20)
	require.NoError(t, err)
	require.Equal(t, []string{"round3-added"}, round3EntryIDs(entries), "unchanged-time retry must rebuild rather than return false empty success")
	require.Equal(t, "roomnote addedtoken\n", entries[0].Content)
}
