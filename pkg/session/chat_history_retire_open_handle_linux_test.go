//go:build linux

package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 6a cut Windows failure (CI runs 41003574602 and 41036777451, six
// retention/read-cut tests): on the expiry path, reconcileHistoryOwned
// compacted into a fresh generation but kept the OLD generation's file handle
// open until it returned, then retired (removed) that old file. Linux removes
// an open file; Windows refuses with a sharing violation, which surfaced as
// storage_error. Oracle (OS-neutral): when retention retires old generations,
// the store itself holds no handle to any generation file except the one it
// keeps. Linux /proc/self/fd shows the process's real open handles.

// heldGenerationFiles lists the generation files of gens, other than keep,
// that this process has open.
func heldGenerationFiles(t *testing.T, gens, keep string) []string {
	t.Helper()
	fds, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	var held []string
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name()))
		if err != nil {
			continue // the directory's own fd closed while listing
		}
		if filepath.Dir(target) == gens && filepath.Base(target) != keep {
			held = append(held, filepath.Base(target))
		}
	}
	return held
}

func TestChatHistoryRetention_RetiresNoGenerationTheStoreStillHoldsOpen(t *testing.T) {
	s, sid := newEffectsSession(t)
	oldTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	buildChatHistoryDayFixture(t, s, sid, []chatHistoryFixtureDay{
		historyDayRows("2026-01-01", "expired", 1, &oldTime),
		historyDayRows("2026-01-02", "current", 1, nil),
	})
	dir := filepath.Join(s.BaseDir(), sid)
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	gens := filepath.Join(historyIndexPath(resolved), "generations")
	before, err := historyLoadCheckpoint(context.Background(), dir, sid)
	require.NoError(t, err)

	// Positive control: the instrument sees a real open generation handle.
	f, err := os.Open(filepath.Join(gens, before.File))
	require.NoError(t, err)
	require.Equal(t, []string{before.File}, heldGenerationFiles(t, gens, ""), "instrument must see an open generation file")
	require.NoError(t, f.Close())
	require.Empty(t, heldGenerationFiles(t, gens, ""), "instrument must see the handle closed")

	var checked int
	var held []string
	old := chatHistoryPublicationHook
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if old != nil {
			if hookErr := old(e); hookErr != nil {
				return hookErr
			}
		}
		// root_installed is the last point before the old generations are
		// retired: the new checkpoint is published and names the kept file.
		if e.SessionID == sid && e.Operation == "retention" && e.Point == "root_installed" {
			checked++
			cp, loadErr := historyLoadCheckpoint(context.Background(), dir, sid)
			require.NoError(t, loadErr)
			require.NotEqual(t, before.File, cp.File, "SETUP: compaction writes a fresh generation file")
			held = append(held, heldGenerationFiles(t, gens, cp.File)...)
		}
		return nil
	}
	t.Cleanup(func() { chatHistoryPublicationHook = old })

	n, err := s.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 1, n, "SETUP: one expired partition is removed")
	require.Equal(t, 1, checked, "positive control: the retention publication ran")
	require.Empty(t, held, "the store still held the old generation open when retiring it (Windows refuses that removal)")
	_, statErr := os.Stat(filepath.Join(gens, before.File))
	require.ErrorIs(t, statErr, os.ErrNotExist, "the old generation is retired")
}
