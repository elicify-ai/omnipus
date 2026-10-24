package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Characterization of the TEST-SIDE filesystem instrument, not product
// acceptance: absent roots snapshot as empty; existing files retain actual
// native bytes/mode/mtime. This checks the guarded sentinel migration only.
func TestHistoryTreeSnapshot_AbsentAndExistingRoots(t *testing.T) {
	root := filepath.Join(t.TempDir(), "snapshot")
	require.Equal(t, map[string]historyFileFact{}, historyTree(t, root))
	require.NoError(t, os.Mkdir(root, 0o700))
	path := filepath.Join(root, "controlled.txt")
	require.NoError(t, os.WriteFile(path, []byte("controlled snapshot bytes"), 0o600))
	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	require.Equal(t, map[string]historyFileFact{
		"controlled.txt": {Bytes: "controlled snapshot bytes", Mode: info.Mode().Perm(), Mtime: info.ModTime().UnixNano()},
	}, historyTree(t, root), "real filesystem metadata is preserved; no missing-sentinel error swallowed as a false snapshot")
}
