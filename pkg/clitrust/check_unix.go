//go:build !windows

package clitrust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// checkPathShape is the Unix shape rule: the path must be absolute.
func checkPathShape(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%q is not an absolute path; enter the full path starting with /", path)
	}
	return nil
}

// fileOwner returns the owning uid of fi, or ok=false when the platform does
// not expose it (then the caller refuses rather than guesses).
func fileOwner(fi os.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}

// trustedOwner reports whether uid can only be root or the gateway's own
// effective user — the two accounts allowed to own anything on the way to a
// program the gateway starts.
func trustedOwner(uid uint32) bool {
	return uid == 0 || int(uid) == os.Geteuid()
}

// checkStep is called for every step of the link-chain walk: dir is a folder
// already reached (fully resolved) and next is the entry inside it that the
// walk continues through — a folder, a link or the final file. Another user
// must not be able to replace next:
//
//   - dir must be owned by root or the gateway user (its owner can always
//     rename or replace what it holds);
//   - dir must not be writable by its group or other users, unless it is a
//     sticky folder (such as /tmp) and next itself is owned by root or the
//     gateway user — the sticky bit stops others renaming or deleting an
//     entry they do not own;
//   - the one group-writable exception is macOS /Applications in its shipped
//     root:admin shape (isTrustedAdminFolder, founder decision 2026-10-30);
//     it must still not be writable by other users.
func checkStep(dir, next string, nextInfo os.FileInfo) error {
	di, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("the folder %q cannot be read: %w", dir, err)
	}
	owner, ok := fileOwner(di)
	if !ok {
		return fmt.Errorf("the owner of the folder %q cannot be determined", dir)
	}
	if !trustedOwner(owner) {
		return fmt.Errorf("the folder %q belongs to another user (uid %d), who could replace %q; only folders owned by root or the user running Omnipus are allowed", dir, owner, next)
	}
	perm := di.Mode().Perm()
	if perm&0o022 == 0 {
		return nil
	}
	if perm&0o002 == 0 && isTrustedAdminFolder(dir, di) {
		return nil
	}
	if di.Mode()&os.ModeSticky != 0 {
		childOwner, ok := fileOwner(nextInfo)
		if ok && trustedOwner(childOwner) {
			return nil
		}
		return fmt.Errorf("%q sits in the shared folder %q and belongs to another user, who could replace it", next, dir)
	}
	return fmt.Errorf("the folder %q is writable by its group or by other users, who could replace %q", dir, next)
}

// checkFinalFile requires a regular file owned by root or the gateway user,
// not writable by its group or by other users, that the gateway's effective
// user can execute (faccessat with AT_EACCESS). The folders on the way to it
// were checked by checkStep during the walk.
func checkFinalFile(final string) error {
	fi, err := os.Lstat(final)
	if err != nil {
		return fmt.Errorf("%q cannot be read: %w", final, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", final)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%q is writable by its group or by other users", final)
	}
	owner, ok := fileOwner(fi)
	if !ok {
		return fmt.Errorf("the owner of %q cannot be determined", final)
	}
	if !trustedOwner(owner) {
		return fmt.Errorf("%q belongs to another user (uid %d); only programs owned by root or the user running Omnipus are allowed", final, owner)
	}
	if err := unix.Faccessat(unix.AT_FDCWD, final, unix.X_OK, unix.AT_EACCESS); err != nil {
		if errors.Is(err, unix.EACCES) {
			return fmt.Errorf("%q is not executable for the user running Omnipus", final)
		}
		return fmt.Errorf("%q could not be checked as executable: %w", final, err)
	}
	return nil
}
