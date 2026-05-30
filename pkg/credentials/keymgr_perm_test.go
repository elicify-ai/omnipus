// Permission tests for the master.key loader (v0.2 #155 item 2).
//
// Verifies the strict 0600 contract documented in keymgr.go::loadKeyFile:
//
//   - 0600  — accepted (canonical SSH-key threat model)
//   - 0644  — refused with "must have mode 0600" error
//   - 0640  — refused (group-read leak path)
//   - 0660  — refused (group-write leak path)
//   - 0700  — refused (owner-execute on a credential file is a smell)
//   - symlink with 0777 perms → 0600 target — accepted (target-mode wins)
//   - symlink with 0777 perms → 0644 target — refused (target perm is unsafe)
//
// The test exercises Unlock through the OMNIPUS_KEY_FILE env-var path so the
// production code path is the same one operators hit on a real boot. It does
// NOT call loadKeyFile directly — that would be testing a private helper and
// could mask a regression where the env-var glue is what's broken.

package credentials_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// validHexKey is a 64-character hex string (32 bytes = 256-bit key).
// Content is irrelevant for these tests — the perm check fires before
// any decoding so a pattern of repeated bytes is fine.
const validHexKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// permCheckSupported reports whether the host filesystem actually honors
// the requested permission bits when WriteFile creates the file. WSL on
// NTFS, FAT32-mounted dirs, and some CI containers mask perms. When the
// stat-back result does not include the requested bit pattern, the test
// can do nothing useful — skip rather than producing a confusing pass.
func permCheckSupported(t *testing.T, dir string) bool {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Windows POSIX perm semantics are weak; the check would be no-op.
		return false
	}
	probe := filepath.Join(dir, ".perm-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return false
	}
	defer os.Remove(probe)
	info, err := os.Stat(probe)
	if err != nil {
		return false
	}
	// If the OS masked the world-read bit we can't trust the test.
	return info.Mode().Perm()&0o044 != 0
}

// TestMasterKeyPerm_0600_Loads exercises the canonical happy path: mode 0600
// is the SSH-key convention and must be accepted by Unlock.
func TestMasterKeyPerm_0600_Loads(t *testing.T) {
	tmpDir := t.TempDir()
	if !permCheckSupported(t, tmpDir) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}

	keyPath := filepath.Join(tmpDir, "master.key")
	if err := os.WriteFile(keyPath, []byte(validHexKey), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	t.Setenv("OMNIPUS_KEY_FILE", keyPath)
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	if err := credentials.Unlock(store); err != nil {
		t.Fatalf("Unlock with mode 0600 must succeed; got: %v", err)
	}
}

// TestMasterKeyPerm_0644_Refused exercises the documented rejection path.
// 0644 (world-readable) defeats encryption-at-rest and must abort boot.
func TestMasterKeyPerm_0644_Refused(t *testing.T) {
	tmpDir := t.TempDir()
	if !permCheckSupported(t, tmpDir) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}

	keyPath := filepath.Join(tmpDir, "master.key")
	if err := os.WriteFile(keyPath, []byte(validHexKey), 0o644); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	t.Setenv("OMNIPUS_KEY_FILE", keyPath)
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	err := credentials.Unlock(store)
	if err == nil {
		t.Fatal("Unlock with mode 0644 must fail; got nil error")
	}
	if !strings.Contains(err.Error(), "0600") {
		t.Errorf("error must mention 0600 to guide the operator; got: %v", err)
	}
	if !strings.Contains(err.Error(), "refusing to load") &&
		!strings.Contains(err.Error(), "must have mode") {
		t.Errorf("error must clearly indicate refusal; got: %v", err)
	}
}

// TestMasterKeyPerm_0640_Refused covers a common misconfiguration: an
// operator who runs the gateway in a group with shared credential mounts
// might leave 0640 thinking "only my group can read this". That still
// crosses the trust boundary for a master key — refused.
func TestMasterKeyPerm_0640_Refused(t *testing.T) {
	tmpDir := t.TempDir()
	if !permCheckSupported(t, tmpDir) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}

	keyPath := filepath.Join(tmpDir, "master.key")
	if err := os.WriteFile(keyPath, []byte(validHexKey), 0o640); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	t.Setenv("OMNIPUS_KEY_FILE", keyPath)
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	err := credentials.Unlock(store)
	if err == nil {
		t.Fatal("Unlock with mode 0640 must fail; got nil error")
	}
	if !strings.Contains(err.Error(), "0600") {
		t.Errorf("error must mention 0600; got: %v", err)
	}
}

// TestMasterKeyPerm_0660_Refused covers a group-write variant. Group-write
// on a credential file means anyone in the group can replace the key,
// substituting an attacker-controlled key for the gateway's. Refused.
func TestMasterKeyPerm_0660_Refused(t *testing.T) {
	tmpDir := t.TempDir()
	if !permCheckSupported(t, tmpDir) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}

	keyPath := filepath.Join(tmpDir, "master.key")
	if err := os.WriteFile(keyPath, []byte(validHexKey), 0o660); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	t.Setenv("OMNIPUS_KEY_FILE", keyPath)
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	err := credentials.Unlock(store)
	if err == nil {
		t.Fatal("Unlock with mode 0660 must fail; got nil error")
	}
}

// TestMasterKeyPerm_0700_Refused: owner-execute on a credential file is
// nonsensical and almost always a chmod typo (operator meant 0o600 and
// hit the wrong digit). Refused — strict mask requires exactly 0600.
func TestMasterKeyPerm_0700_Refused(t *testing.T) {
	tmpDir := t.TempDir()
	if !permCheckSupported(t, tmpDir) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}

	keyPath := filepath.Join(tmpDir, "master.key")
	if err := os.WriteFile(keyPath, []byte(validHexKey), 0o700); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	t.Setenv("OMNIPUS_KEY_FILE", keyPath)
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	err := credentials.Unlock(store)
	if err == nil {
		t.Fatal("Unlock with mode 0700 must fail; got nil error")
	}
}

// TestMasterKeyPerm_SymlinkToMode0600_Loads exercises the symlink-to-0600
// case. Linux symlinks always report 0o777 in their own metadata regardless
// of what chmod is invoked on the symlink itself; what matters for security
// is the target's perms. The Lstat-then-Stat sequence in loadKeyFile must
// allow this case so deployments using Vault-managed key file symlinks are
// not broken.
func TestMasterKeyPerm_SymlinkToMode0600_Loads(t *testing.T) {
	tmpDir := t.TempDir()
	if !permCheckSupported(t, tmpDir) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}

	target := filepath.Join(tmpDir, "real-master.key")
	if err := os.WriteFile(target, []byte(validHexKey), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(tmpDir, "master.key")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// On Linux the symlink's own perms (Lchmod) are not commonly settable
	// and the kernel ignores them anyway. Just verify Lstat reports it as
	// a symlink and the target is mode 0600 — that's the security-relevant
	// state.
	lInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if lInfo.Mode()&os.ModeSymlink == 0 {
		t.Skipf("filesystem did not preserve symlink semantics; cannot exercise the case")
	}

	t.Setenv("OMNIPUS_KEY_FILE", link)
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	if err := credentials.Unlock(store); err != nil {
		t.Fatalf("Unlock via symlink to 0600 target must succeed; got: %v", err)
	}
}

// TestMasterKeyPerm_SymlinkToMode0644_Refused exercises the dangerous case:
// a symlink that points at a 0644 target. The strict mode check follows
// the symlink (via os.Stat) and must refuse based on the TARGET's mode,
// not the link's own metadata.
func TestMasterKeyPerm_SymlinkToMode0644_Refused(t *testing.T) {
	tmpDir := t.TempDir()
	if !permCheckSupported(t, tmpDir) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}

	target := filepath.Join(tmpDir, "leaky-master.key")
	if err := os.WriteFile(target, []byte(validHexKey), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(tmpDir, "master.key")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	t.Setenv("OMNIPUS_KEY_FILE", link)
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	err := credentials.Unlock(store)
	if err == nil {
		t.Fatal("Unlock via symlink to 0644 target must fail; got nil error")
	}
	if !strings.Contains(err.Error(), "0600") {
		t.Errorf("error must mention 0600; got: %v", err)
	}
}

// TestMasterKeyPerm_DanglingSymlink_Refused ensures a symlink pointing at
// a non-existent target is refused with a clear error rather than panicking
// or silently auto-generating a fresh key.
func TestMasterKeyPerm_DanglingSymlink_Refused(t *testing.T) {
	tmpDir := t.TempDir()

	link := filepath.Join(tmpDir, "master.key")
	if err := os.Symlink(filepath.Join(tmpDir, "does-not-exist"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	t.Setenv("OMNIPUS_KEY_FILE", link)
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	err := credentials.Unlock(store)
	if err == nil {
		t.Fatal("Unlock via dangling symlink must fail; got nil error")
	}
	// The error must be specific enough to identify a stat/file-missing
	// problem rather than the generic "no master key available" path.
	if !errors.Is(err, os.ErrNotExist) &&
		!strings.Contains(err.Error(), "stat") &&
		!strings.Contains(err.Error(), "no such") {
		t.Errorf("error must reflect missing target; got: %v", err)
	}
}
