//go:build !windows

package gateway

import (
	"errors"
	"os"
	"path/filepath"
)

// plantUnreadableTasks leaves tasks/ unlistable. Mode 0 removes the owner's
// directory-search bit on Unix, so the leak sweep's walk gets a permission
// error instead of the file inside.
func plantUnreadableTasks() error {
	if err := os.Mkdir("tasks", 0o755); err != nil {
		return err
	}
	if err := os.WriteFile("tasks/hidden", []byte("x"), 0o600); err != nil {
		return err
	}
	return os.Chmod("tasks", 0)
}

// restoreUnreadableTasks puts the directory back so the temp dir can be removed.
func restoreUnreadableTasks(dir string) error {
	err := os.Chmod(filepath.Join(dir, "tasks"), 0o755)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
