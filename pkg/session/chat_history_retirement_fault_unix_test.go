//go:build linux || darwin

package session

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func gate0HoldRetirementRemoveFault(t *testing.T, path string) (func(), error) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatal("BLOCKED: root bypasses the actual generation-directory permission fault required by F4/X; use a real non-root native test receipt")
	}
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))
	var once sync.Once
	clear := func() { once.Do(func() { require.NoError(t, os.Chmod(dir, info.Mode().Perm())) }) }
	t.Cleanup(clear)
	probe := os.Remove(path) // same actual object, while the logical reader pin is still held
	require.ErrorIs(t, probe, fs.ErrPermission, "instrument must prove a real OS removal failure before testing release propagation")
	require.FileExists(t, path)
	return clear, fs.ErrPermission
}
