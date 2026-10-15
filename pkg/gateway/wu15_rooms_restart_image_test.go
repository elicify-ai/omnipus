package gateway

import (
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
func wu15DurableCrashImage(t *testing.T, home string) func() {
	t.Helper()
	files := map[string][]byte{}
	modes := map[string]fs.FileMode{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		require.True(t, info.Mode().IsRegular(), "SETUP: crash image fixture has only regular files")
		data, readErr := os.ReadFile(path)
		if readErr != nil {
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
