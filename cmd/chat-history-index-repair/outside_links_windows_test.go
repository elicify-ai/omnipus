//go:build windows

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// repairSetLinkTimes sets an entry's own modification time without following
// a symbolic link: the reparse point itself is opened, not its target.
func repairSetLinkTimes(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	h, err := windows.CreateFile(p, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	require.NoError(t, err)
	defer func() { require.NoError(t, windows.CloseHandle(h)) }()
	ft := windows.NsecToFiletime(mtime.UnixNano())
	require.NoError(t, windows.SetFileTime(h, nil, &ft, &ft))
}

// Windows has no named pipes in the file system namespace; no fixture uses one.
func repairMakeFIFO(t *testing.T, path string) {
	t.Helper()
	t.Fatalf("named pipe fixture %s is not available on Windows", path)
}
