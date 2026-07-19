//go:build !unix

package browser

// coordinator_lock_other.go — the non-Unix (e.g. Windows/plan9/js/wasip1)
// shared-Chrome single-launch lock (CRIT-001). flock is unavailable off Unix,
// so an O_EXCL create is the atomic guard: exactly one process can create the
// lockfile. A stale lockfile left by a crashed prior process (O_EXCL, unlike
// flock, is NOT auto-released on death) is handled by the caller's
// marker+pidAlive staleness check in coordinator.go (takeLaunchLock), which
// removes it and retries once.

import "os"

// acquireLaunchLock attempts an atomic O_EXCL create of the lockfile at path.
// Returns (file, true, nil) when created (lock taken) — the caller MUST keep it
// open for the coordinator's lifetime and pass it to releaseLaunchLock, which
// removes the file. Returns (nil, false, nil) when the file already exists (held
// or stale). Returns (nil, false, err) on any other filesystem error.
func acquireLaunchLock(path string) (*os.File, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return f, true, nil
}

// releaseLaunchLock closes and removes the lockfile. Safe on nil. Removal is
// required off Unix because O_EXCL treats any lingering file as "held".
func releaseLaunchLock(f *os.File) {
	if f == nil {
		return
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
}
