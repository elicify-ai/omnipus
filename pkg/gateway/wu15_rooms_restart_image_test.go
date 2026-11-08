package gateway

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Freeze server-produced durable bytes while a scripted provider is blocked.
// After orderly test teardown drains every writer, restoring those same bytes
// emulates a process crash without inventing a private room/lifecycle format or
// letting a live goroutine outlive its test home. This is a filesystem-edge
// fixture, not a state-machine double. The directory is the disposable HOME.
//
// The walk races the gateway's LIVE atomic writers (the snapshot is taken
// mid-test by design — the provider is blocked, but the room ledger and
// session stores still flush). Every such write is a temp file that is
// created and renamed away microseconds later (fileutil.WriteFileAtomic's
// ".tmp-*", gitevidence's marker write inside .git), so an entry can vanish
// between WalkDir's enumeration and its read. That transient is not durable
// state — the renamed file IS captured under its final name — so a vanished
// mid-walk entry is skipped, and ONLY that: any other walk error stays loud.
// Skipping a file that was listed but gone before its bytes were read cannot
// mask a durable-state defect; it only declines to snapshot a file that, at
// no instant, existed in finished form. (CI hit this as a background writer's
// transient vanishing under the walk — the TempDir-cleanup family's
// mid-test face.)
func wu15DurableCrashImage(t *testing.T, home string) func() {
	t.Helper()
	files := map[string][]byte{}
	modes := map[string]fs.FileMode{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil // directory or entry vanished mid-walk: a transient, not durable state
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return nil // enumerated, then renamed/removed before Info: transient atomic-write temp
			}
			return statErr
		}
		require.True(t, info.Mode().IsRegular(), "SETUP: crash image fixture has only regular files")
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				return nil // vanished between Info and ReadFile: same transient
			}
			return readErr
		}
		rel, relErr := filepath.Rel(home, path)
		if relErr != nil {
			return relErr
		}
		files[rel], modes[rel] = data, info.Mode().Perm()
		return nil
	})
	require.NoError(t, err, "SETUP: snapshot actual durable home bytes")
	require.NotEmpty(t, files, "instrument: durable image is not empty")
	return func() {
		t.Helper()
		require.NoError(t, os.RemoveAll(home))
		require.NoError(t, os.MkdirAll(home, 0o700))
		for rel, data := range files {
			path := filepath.Join(home, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, data, modes[rel]))
		}
	}
}
