package browser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// profileLaunchLockPath keeps the guard outside the tree DeleteProfile removes.
// Every caller derives it from the same immutable configured profile identity.
func profileLaunchLockPath(profileDir string) string {
	dir := filepath.Clean(profileDir)
	return filepath.Join(filepath.Dir(dir), "."+filepath.Base(dir)+"."+launchLockFileName)
}

// acquireProfileLaunchLock serializes current launch, trim, reconciliation and
// deletion callers. A held legacy in-profile lock also causes refusal. Older
// binaries do not honor the sibling lock, so this check cannot serialize a
// newly starting legacy gateway after the check has completed.
func acquireProfileLaunchLock(profileDir string) (*os.File, bool, error) {
	path := profileLaunchLockPath(profileDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, fmt.Errorf("cannot create launch lock directory: %w", err)
	}
	lock, acquired, err := acquireLaunchLock(path)
	if err != nil || !acquired {
		return lock, acquired, err
	}
	if err := checkLegacyLaunchLock(profileDir); err != nil {
		releaseLaunchLock(lock)
		return nil, false, err
	}
	return lock, true, nil
}

func checkLegacyLaunchLock(profileDir string) error {
	path := filepath.Join(profileDir, launchLockFileName)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("cannot inspect legacy launch lock %s: %w", path, err)
	}
	lock, acquired, err := acquireLaunchLock(path)
	if err != nil {
		return fmt.Errorf("cannot check legacy launch lock %s: %w", path, err)
	}
	if !acquired {
		return fmt.Errorf("legacy launch lock %s is held or unavailable; stop its older gateway before using this profile", path)
	}
	releaseLaunchLock(lock)
	return nil
}
