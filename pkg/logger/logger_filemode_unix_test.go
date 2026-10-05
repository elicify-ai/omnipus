//go:build !windows

package logger

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestEnableFileLogging_FileMode(t *testing.T) {
	// Umask is process-wide, so this test must not run in parallel. A fixed
	// 0022 mask leaves a regression to group/world-readable 0644 observable.
	previousUmask := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(previousUmask) })

	t.Run("new_file_private_to_owner", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "new.log")
		t.Cleanup(DisableFileLogging)

		if err := EnableFileLogging(path); err != nil {
			t.Fatalf("enable logging for new file: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat newly created log file: %v", err)
		}
		// The security requirement is owner-only read/write, exactly 0600.
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("new log file permissions = %04o, want 0600 (private to the owner)", got)
		}
	})

	t.Run("existing_file_preserves_mode", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "existing.log")
		t.Cleanup(DisableFileLogging)

		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("create existing log file with 0644 permissions: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat existing log file before enabling logging: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Fatalf("existing log file setup permissions = %04o, want 0644", got)
		}

		if err = EnableFileLogging(path); err != nil {
			t.Fatalf("enable logging for existing file: %v", err)
		}
		info, err = os.Stat(path)
		if err != nil {
			t.Fatalf("stat existing log file after enabling logging: %v", err)
		}
		// Creation-only privacy must preserve an existing file's 0644 mode.
		if got := info.Mode().Perm(); got != 0o644 {
			t.Errorf("existing log file permissions = %04o, want 0644 (preserve existing mode)", got)
		}
	})
}
