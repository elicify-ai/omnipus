package gateway

import (
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func r1CleanupHoldRemoveFault(t *testing.T, path string) (func(), error) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0) // Real native handle deliberately withholds delete sharing.
	require.NoError(t, err)
	var once sync.Once
	clear := func() { once.Do(func() { require.NoError(t, windows.CloseHandle(handle)) }) }
	t.Cleanup(clear)
	require.ErrorIs(t, os.Remove(path), windows.ERROR_SHARING_VIOLATION, "instrument: actual native sharing refusal, never chmod-as-Windows")
	require.FileExists(t, path)
	return clear, windows.ERROR_SHARING_VIOLATION
}
