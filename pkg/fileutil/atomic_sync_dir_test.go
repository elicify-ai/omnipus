package fileutil

// RED pack T1 — strict dir-sync atomic write (prerequisite E).
//
// COMPILE-BLOCKED RED, stated honestly: fileutil.WriteFileAtomicSyncDir
// (new exported function per the correction) does not exist at this pin
// (a2eb202dedfb3…). Until it lands, this file breaks the pkg/fileutil test
// build with "undefined: WriteFileAtomicSyncDir" — the compile failure is
// the first RED signal and is not a behavioral red.
//
// Oracle (specification, not implementation):
//   - a-boot-durability-correction-20261002T1130 REPORT §3: the variant is
//     step-identical to WriteFileAtomic except that the dir-durability step
//     (os.Open(dir) then dirFile.Sync()) returns BOTH errors wrapped,
//     naming dir and step — no warn-only branch remains.
//   - §7 / F8 (darwin live probe): in a chmod 0o300 directory the whole
//     write path (CreateTemp → write → Sync → rename) succeeds while
//     os.Open(dir) fails EACCES — the open-failure leg is deterministically
//     reachable with an ordinary filesystem fixture, no production hook.
//   - Dispatch brief: the variant MUST return an error naming that
//     directory, while warn-only WriteFileAtomic stays unchanged (pinned by
//     atomic_sync_dir_warnpin_test.go::TestWriteFileAtomic_…).
//
// Known gap (correction §7, stated not papered over): the sync-FAILS-after-
// open leg has no userspace fault injection and is NOT unit-tested here;
// its coverage is structural review plus optional platform CI.
//
// Mutation targets for CHECK (not run in RED): copy WriteFileAtomic's
// warn-only block into the variant (T1 dies), return nil before the dir
// step (T1 dies), skip the dir step outright (T1 dies).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWriteFileAtomicSyncDir_UnopenableDirForSyncReturnsErrorNamingDir (T1):
// with the directory made unopenable (0o300 — owner write+execute, no read)
// the data write and rename succeed but the directory-open-for-sync step
// must fail the write visibly, with the error naming that directory. The
// target file nonetheless carries the payload, proving the failure is
// exactly the dir-sync step (F8).
func TestWriteFileAtomicSyncDir_UnopenableDirForSyncReturnsErrorNamingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("correction §7: chmod 0o300 is a read-only-bit no-op on Windows; the Windows legs are platform-only CI")
	}
	if os.Geteuid() == 0 {
		t.Skip("correction §7: root sees no EACCES on a 0o300 directory; the fixture cannot force the open failure")
	}
	dir := filepath.Join(t.TempDir(), "epoch-dir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	t.Cleanup(func() {
		// Restore read access so t.TempDir()'s removal can descend into the
		// directory (the brief's restoration requirement).
		_ = os.Chmod(dir, 0o700)
	})
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatalf("chmod fixture dir to 0o300: %v", err)
	}

	const payload = `{"boot_epoch":1}`
	target := filepath.Join(dir, "boot_epoch.json")
	err := WriteFileAtomicSyncDir(target, []byte(payload), 0o600)
	if err == nil {
		t.Fatal("WriteFileAtomicSyncDir must fail visibly when the directory cannot be opened for sync, got nil error (correction §3: no warn-only branch)")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("error must name the directory %q, got: %v", dir, err)
	}

	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("target must exist after the dir-sync refusal (F8: write+rename succeeded first): %v", readErr)
	}
	if string(data) != payload {
		t.Fatalf("target content = %q, want %q", data, payload)
	}
}
