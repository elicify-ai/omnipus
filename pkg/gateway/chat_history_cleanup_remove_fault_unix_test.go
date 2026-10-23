//go:build linux || darwin

package gateway

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func r1CleanupHoldRemoveFault(t *testing.T, path string) (func(), error) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatal("BLOCKED: root bypasses actual retired-generation directory permission fault — R1-1/pin X require a non-root native remove-denial receipt")
	}
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))
	var once sync.Once
	restoreRemove := func() { once.Do(func() { require.NoError(t, os.Chmod(dir, info.Mode().Perm())) }) }
	t.Cleanup(restoreRemove)
	require.ErrorIs(t, os.Remove(path), fs.ErrPermission, "instrument: real native remove fails while the pinned retired file remains")
	require.FileExists(t, path)
	return restoreRemove, fs.ErrPermission
}
