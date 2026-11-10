//go:build windows

package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

// plantUnreadableTasks leaves tasks/ unlistable. Windows chmod only toggles
// the read-only attribute, so a mode of 0 would still let the sweep list the
// file. A protected deny entry for directory listing is an access check the
// walk actually fails.
func plantUnreadableTasks() error {
	tasks := "tasks"
	if err := os.Mkdir(tasks, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tasks, "hidden"), []byte("x"), 0o600); err != nil {
		return err
	}
	return setCurrentUserDirectoryAccess(tasks, windows.DENY_ACCESS, windows.FILE_LIST_DIRECTORY)
}

// restoreUnreadableTasks grants the current user full control again. The
// creator owns the directory, so the deny entry does not block this change.
func restoreUnreadableTasks(dir string) error {
	tasks := filepath.Join(dir, "tasks")
	if _, err := os.Lstat(tasks); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return setCurrentUserDirectoryAccess(tasks, windows.GRANT_ACCESS, windows.GENERIC_ALL)
}

func setCurrentUserDirectoryAccess(path string, mode windows.ACCESS_MODE, perms windows.ACCESS_MASK) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sid := user.User.Sid
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(sid)
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: perms,
		AccessMode:        mode,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
	// The SID lives in the token buffer. Keep both alive until the ACL call returns.
	runtime.KeepAlive(user)
	runtime.KeepAlive(sid)
	return err
}
