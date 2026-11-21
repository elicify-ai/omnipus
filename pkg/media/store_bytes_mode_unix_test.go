//go:build darwin || linux

package media

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

const storeBytesModeChild = "OMNIPUS_TEST_STOREBYTES_MODE_CHILD"

func storeBytesModeProcess(t *testing.T) ([]byte, error) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, "-test.v", "-test.count=1", "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	cmd.Env = append(os.Environ(), storeBytesModeChild+"="+t.Name())
	return cmd.CombinedOutput()
}

// StoreBytes documents "dir (created 0700 if needed)". Check both fresh
// directory levels. Umask is process-global, so change it ONLY in this child:
// a restrictive inherited mask must not hide a 0755 regression as 0700.
func TestStoreBytes_CreatesPrivateDirectory(t *testing.T) {
	if os.Getenv(storeBytesModeChild) != t.Name() {
		output, err := storeBytesModeProcess(t)
		t.Logf("isolated directory-mode check:\n%s", output)
		require.NoError(t, err)
		require.Contains(t, string(output), "--- PASS: "+t.Name()+" (")
		return
	}
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	store := newTestStore(t)
	parent := filepath.Join(t.TempDir(), "fresh")
	dir := filepath.Join(parent, "staging")
	ref, err := StoreBytes(store, dir, "page-*", []byte("mode receipt"), MediaMeta{}, "mode-scope")
	require.NoError(t, err)
	path, err := store.Resolve(ref)
	require.NoError(t, err)
	require.FileExists(t, path)
	storeBytesAssertDirectoryMode(t, parent)
	storeBytesAssertDirectoryMode(t, dir)
}

// Calibrate the SAME permission assertion with a real 0755 directory, never a
// production-code mutant. The deliberately failing child must fail by name and
// exact mode, not by setup/compilation errors. A separate direct run saves RED.
func TestStoreBytes_DirectoryModeInstrument(t *testing.T) {
	if os.Getenv(storeBytesModeChild) == t.Name() {
		old := syscall.Umask(0)
		defer syscall.Umask(old)
		dir := filepath.Join(t.TempDir(), "public-control")
		require.NoError(t, os.Mkdir(dir, 0755))
		storeBytesAssertDirectoryMode(t, dir)
		return
	}
	output, err := storeBytesModeProcess(t)
	t.Logf("expected failing 0755 assertion control:\n%s", output)
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 1, exit.ExitCode())
	require.Contains(t, string(output), "StoreBytes directory permissions: got 0755, want 0700")
	require.Contains(t, string(output), "--- FAIL: "+t.Name()+" (")
}

func storeBytesAssertDirectoryMode(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "StoreBytes staging path must be a directory")
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("StoreBytes directory permissions: got %04o, want 0700", got)
	}
}
