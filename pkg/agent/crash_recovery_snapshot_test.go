//go:build goolm && stdjson

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrashRecovery_SnapshotRestoresBytesWithoutChangingAnchor(t *testing.T) {
	home, snapshot := t.TempDir(), t.TempDir()
	anchor := filepath.Join(home, "workspace")
	require.NoError(t, os.Mkdir(anchor, 0700))
	path := filepath.Join(anchor, "effect.txt")
	require.NoError(t, os.WriteFile(path, []byte("captured\n"), 0600))
	original, err := os.Stat(anchor)
	require.NoError(t, err)
	require.NoError(t, crashCopyTree(home, snapshot))
	copied, err := os.Stat(filepath.Join(snapshot, "workspace"))
	require.NoError(t, err)
	require.False(t, os.SameFile(original, copied), "positive instrument control: a separately created directory has a different identity")
	require.NoError(t, os.WriteFile(path, []byte("post-cut tail that must not survive\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(anchor, "post-cut.json"), []byte("extra"), 0600))
	require.NoError(t, crashRestoreTree(snapshot, home))
	restored, err := os.Stat(anchor)
	require.NoError(t, err)
	assert.True(t, os.SameFile(original, restored), "simulated process restart must not manufacture a changed target anchor")
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "captured\n", string(bytes))
	_, err = os.Stat(filepath.Join(anchor, "post-cut.json"))
	assert.True(t, os.IsNotExist(err), "writes after the captured crash cut must not survive restore")
}
