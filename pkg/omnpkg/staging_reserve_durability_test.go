package omnpkg_test

// TestStagingReserve_SyncsNewFolderEntries covers review warning U1 (#1094
// S1a): Reserve creates the staging folder (and, on the first upload, its
// backups/.staging ancestors). The journal written there later syncs only its
// own folder, so the new folder's entry in its parent must be synced when the
// folder is created; otherwise a power cut can lose the whole folder, journal
// included, while later import writes survive. A failed sync refuses the
// reservation and leaves no staging folder behind.
//
// The sync is observed and faulted through fileutil's strict-sync seam, which
// is process-wide: no t.Parallel.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
)

func TestStagingReserve_SyncsNewFolderEntries(t *testing.T) {
	t.Run("each folder Reserve creates is synced in its parent", func(t *testing.T) {
		root := t.TempDir()
		base := filepath.Join(root, "backups", ".staging")
		var synced []string
		t.Cleanup(fileutil.SetDirSyncFaultForTest(func(dir string) error {
			synced = append(synced, dir)
			return nil
		}))
		reg := omnpkg.NewStagingRegistry(base, time.Now)

		_, err := reg.Reserve()
		require.NoError(t, err)
		require.ElementsMatch(t, []string{base, filepath.Dir(base), root}, synced,
			"first upload: the staging folder, .staging and backups are all new")

		synced = nil
		_, err = reg.Reserve()
		require.NoError(t, err)
		require.Equal(t, []string{base}, synced, "later uploads: only the staging folder is new")
	})

	t.Run("a failed sync refuses the reservation and leaves no staging folder", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), ".staging")
		require.NoError(t, os.MkdirAll(base, 0o700))
		syncErr := errors.New("sync failed: disk did not flush")
		t.Cleanup(fileutil.SetDirSyncFaultForTest(func(dir string) error {
			if dir == base {
				return syncErr
			}
			return nil
		}))
		reg := omnpkg.NewStagingRegistry(base, time.Now)

		_, err := reg.Reserve()
		require.ErrorIs(t, err, syncErr)
		entries, readErr := os.ReadDir(base)
		require.NoError(t, readErr)
		require.Empty(t, entries, "the staging folder whose entry could not be synced is removed")
	})
}
