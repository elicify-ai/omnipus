package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The stale-lock sweep removes exactly the class of file an earlier run leaked
// (empty *.lock under entities/) and nothing else: a lock with content, a
// non-lock file and a file elsewhere must all survive, so the guard still reports
// them.
func TestRemoveStaleLeakedLocks_RemovesOnlyEmptyLockFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "entities")
	agents := filepath.Join(root, "agents")
	require.NoError(t, os.MkdirAll(agents, 0o755))
	stale := filepath.Join(agents, "ava.lock")
	withContent := filepath.Join(agents, "jim.lock")
	notALock := filepath.Join(agents, "mia.json")
	require.NoError(t, os.WriteFile(stale, nil, 0o600))
	require.NoError(t, os.WriteFile(withContent, []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(notALock, nil, 0o600))

	removed := removeStaleLeakedLocks(root)

	require.Equal(t, []string{stale}, removed)
	_, err := os.Stat(stale)
	require.True(t, errors.Is(err, os.ErrNotExist), "the empty lock file is removed")
	for _, keep := range []string{withContent, notALock} {
		_, keepErr := os.Stat(keep)
		require.NoError(t, keepErr, "%s must survive: only empty *.lock files are swept", keep)
	}
	_, err = os.Stat(agents)
	require.NoError(t, err, "a directory that still holds files is kept")
}

func TestRemoveStaleLeakedLocks_MissingRootIsANoOp(t *testing.T) {
	require.Empty(t, removeStaleLeakedLocks(filepath.Join(t.TempDir(), "entities")))
}
