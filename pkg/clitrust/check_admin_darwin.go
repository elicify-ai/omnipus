//go:build darwin

package clitrust

import (
	"os"
	"syscall"
)

// macOSApplicationsDir and macOSAdminGID describe the one group-writable
// folder Check CLI trusts (founder decision 2026-10-30, "B: Allow
// /Applications"): macOS ships /Applications as root:admin, mode 775, so
// every administrator account can install apps there. Exactly this folder is
// trusted, and only in that shipped shape — owned by root, group admin
// (gid 80), and not writable by other users. Nothing else group-writable is
// trusted, and a folder further down (inside an .app) is checked normally.
const (
	macOSApplicationsDir = "/Applications"
	macOSAdminGID        = 80
)

// isTrustedAdminFolder reports whether dir is /Applications in its shipped
// root:admin, not-other-writable shape. checkStep then accepts its
// group-write bit; every other rule still applies.
func isTrustedAdminFolder(dir string, di os.FileInfo) bool {
	if dir != macOSApplicationsDir {
		return false
	}
	st, ok := di.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return st.Uid == 0 && st.Gid == macOSAdminGID && di.Mode().Perm()&0o002 == 0
}
