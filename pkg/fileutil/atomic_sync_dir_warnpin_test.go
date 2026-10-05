package fileutil

// Green pin T1b — the warn-only posture of the EXISTING WriteFileAtomic
// (prerequisite E; correction §7: "authorable today against existing API").
//
// This test pins today's specified behavior so the strict variant's
// difference is observable: on the same fixture where
// WriteFileAtomicSyncDir must fail (atomic_sync_dir_test.go), WriteFileAtomic
// must KEEP returning nil while silently swallowing the directory-sync
// failure (pkg/fileutil/file.go::WriteFileAtomic dir-sync block). It is a
// green pin with an independent spec source (the correction's contract that
// WriteFileAtomic is untouched, 60 production caller files, zero blast
// radius — correction §3/§8), not a characterization guess. If this goes
// red after the variant lands, the implementation tightened the OLD
// function too — a blast-radius violation.
//
// Runs today at pin a2eb202dedfb3… (no new symbols); local execution not
// claimed in RED — CI and CHECK's runs are the authority.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWriteFileAtomic_WarnOnlyPostureUnchangedOnUnopenableDir (T1b): with
// the directory unopenable (0o300), the whole write path INCLUDING the
// swallowed dir-sync step succeeds and returns nil.
func TestWriteFileAtomic_WarnOnlyPostureUnchangedOnUnopenableDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("same fixture limit as T1: chmod 0o300 is a read-only-bit no-op on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root sees no EACCES on a 0o300 directory; the fixture cannot force the swallowed failure")
	}
	dir := filepath.Join(t.TempDir(), "epoch-dir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700)
	})
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatalf("chmod fixture dir to 0o300: %v", err)
	}

	const payload = `{"boot_epoch":1}`
	target := filepath.Join(dir, "boot_epoch.json")
	if err := WriteFileAtomic(target, []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic must keep its warn-only posture (dir-sync failure swallowed), got error: %v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("target must exist after the warn-only write: %v", readErr)
	}
	if string(data) != payload {
		t.Fatalf("target content = %q, want %q", data, payload)
	}
}
