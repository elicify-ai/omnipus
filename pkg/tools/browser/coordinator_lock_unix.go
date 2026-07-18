//go:build unix

package browser

// coordinator_lock_unix.go — the Unix (Linux/macOS/BSD) shared-Chrome
// single-launch lock (CRIT-001). Uses a non-blocking advisory flock: the
// removed net.Listen(":9223") bind was the pre-pipe atomic guard, and the CDP
// pipe has no port, so a cross-process flock lockfile takes its place. flock is
// released automatically by the kernel when the holding process dies, so a
// crashed prior gateway never leaves a stale lock that wedges the next launch.

import (
	"os"

	"golang.org/x/sys/unix"
)

// acquireLaunchLock attempts a non-blocking exclusive flock on the lockfile at
// path. Returns (file, true, nil) when the lock is taken — the caller MUST keep
// file open for the coordinator's lifetime and pass it to releaseLaunchLock to
// release. Returns (nil, false, nil) when the lock is already held by another
// live open file description (this or another process). Returns (nil, false,
// err) on an unexpected filesystem error.
func acquireLaunchLock(path string) (*os.File, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	// flock locks bind to the open file description, so two separate open() calls
	// contend even within one process — the coordinator's in-process single-flight
	// (c.launching) and this cross-process lock are complementary, not redundant.
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if err == unix.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, err
	}
	return f, true, nil
}

// releaseLaunchLock releases the flock and closes the file. Safe on nil. The
// lockfile itself is intentionally left on disk (an unheld flock file is inert;
// the next acquireLaunchLock opens and re-locks it).
func releaseLaunchLock(f *os.File) {
	if f == nil {
		return
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	_ = f.Close()
}
