//go:build darwin || linux

package media

import (
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// Force a real filesystem write failure, never a fake StoreBytes or file.
// RLIMIT_FSIZE affects only the isolated child; the parent test runner keeps
// its original resource limits and signal disposition.
func TestStoreBytes_WriteFailureRemovesStagedFile(t *testing.T) {
	const marker = "OMNIPUS_TEST_STORE_BYTES_WRITE_CHILD"
	if os.Getenv(marker) != t.Name() {
		self, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(self, "-test.v", "-test.count=1", "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
		cmd.Env = append(os.Environ(), marker+"="+t.Name())
		output, runErr := cmd.CombinedOutput()
		t.Logf("isolated real write failure:\n%s", output)
		require.NoError(t, runErr)
		require.Contains(t, string(output), "--- PASS: "+t.Name()+" (", "the child must execute its assertions")
		return
	}

	dir := t.TempDir()
	var before syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_FSIZE, &before))
	limit := before
	limit.Cur = 0 // a nonempty write must fail with EFBIG at the kernel boundary
	signal.Ignore(syscall.SIGXFSZ)
	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit))
	t.Cleanup(func() {
		require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &before))
	})

	store := &storeBytesRegistration{t: t}
	ref, err := StoreBytes(store, dir, "page-*", []byte("nonempty"), MediaMeta{}, "write-failure-scope")
	require.Empty(t, ref)
	require.ErrorIs(t, err, syscall.EFBIG, "the failure must come from the real write, not setup or registration")
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, "write", pathErr.Op)
	require.Zero(t, store.calls, "a failed write must never reach registration")
	_, statErr := os.Stat(pathErr.Path)
	require.ErrorIs(t, statErr, os.ErrNotExist, "write failure must unlink the actual failed staging path")
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	require.Empty(t, entries, "write failure leaves no staged bytes")
}
