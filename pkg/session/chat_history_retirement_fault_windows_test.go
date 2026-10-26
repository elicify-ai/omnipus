package session

import (
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func gate0HoldRetirementRemoveFault(t *testing.T, path string) (func(), error) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	// Permit ordinary read/write, deliberately withhold FILE_SHARE_DELETE.
	// This is a real native busy handle, not chmod pretending Windows unlink.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	var once sync.Once
	clear := func() { once.Do(func() { require.NoError(t, windows.CloseHandle(handle)) }) }
	t.Cleanup(clear)
	probe := os.Remove(path)
	require.ErrorIs(t, probe, windows.ERROR_SHARING_VIOLATION, "instrument must prove actual Windows sharing removal refusal")
	require.FileExists(t, path)
	return clear, windows.ERROR_SHARING_VIOLATION
}
