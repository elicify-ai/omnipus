// Tests for Unlock mode 0 — OMNIPUS_MASTER_KEY_SOURCE, consume-once delivery
// of a platform-delivered master key (IN-12).
//
// The property under test is narrow and unusually consequential: after a
// successful boot the plaintext key must exist ONLY in the instance's memory.
// That means two assertions on every happy path, not one —
//
//   - the delivery file is GONE (consumed), and
//   - $OMNIPUS_HOME/master.key was NEVER created (nothing persisted).
//
// The second one is the whole reason the mode exists, so it is asserted
// explicitly everywhere rather than left implied.
//
// The failure paths matter just as much. Mode 0 must fail CLOSED: an error
// here may never fall through to mode 4 (auto-generate), which would mint a
// different key and strand the tenant's existing encrypted data behind a key
// nobody has. Every negative test therefore asserts three things — an error
// came back, the store is still locked, and no master.key appeared.
//
// Style follows keymgr_perm_test.go: exercise the public Unlock() so the code
// path is the one a real boot takes, reuse its permCheckSupported guard for
// filesystems that mask permission bits, and skip loudly rather than letting a
// test pass without exercising anything.

package credentials_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// deliveredHexKey is the key the "platform" hands to the instance: 64 hex
// characters = 32 bytes = one AES-256 key. Deliberately different from
// keymgr_perm_test.go's validHexKey so priority tests can tell them apart.
const deliveredHexKey = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

// otherHexKey stands in for a key provisioned by a different mode (e.g. the
// OMNIPUS_MASTER_KEY env var). Used to prove which key actually unlocked.
const otherHexKey = "0f0e0d0c0b0a0908070605040302010f0e0d0c0b0a09080706050403020100ff"

// clearOtherKeyModes blanks the env vars for modes 1 and 2 so a test can be
// certain mode 0 (or the absence of any mode) is what it is exercising, and
// so an ambient OMNIPUS_* var in the developer's shell cannot mask a failure.
func clearOtherKeyModes(t *testing.T) {
	t.Helper()
	t.Setenv("OMNIPUS_MASTER_KEY", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")
}

// deliveryPoint creates a delivery directory under dir and writes hexKey into
// it at the requested mode, mimicking a platform mounting the key at boot.
// Returns the delivery directory and the key file path. The delivery point is
// deliberately NOT the Omnipus home directory — a real one is a separate
// (tmpfs) mount, and keeping them apart is what lets the tests assert that
// $OMNIPUS_HOME/master.key was never written.
func deliveryPoint(t *testing.T, dir, hexKey string, mode os.FileMode) (string, string) {
	t.Helper()
	deliveryDir := filepath.Join(dir, "delivery")
	require.NoError(t, os.MkdirAll(deliveryDir, 0o700), "create delivery dir")
	keyPath := filepath.Join(deliveryDir, "master-key")
	require.NoError(t, os.WriteFile(keyPath, []byte(hexKey), mode), "write delivered key")
	return deliveryDir, keyPath
}

// requireNoPersistedKey asserts that $OMNIPUS_HOME/master.key does not exist.
// This is the assertion mode 0 exists for: nothing may be written at rest,
// neither by mode 0 itself nor by a fall-through into mode 4.
func requireNoPersistedKey(t *testing.T, homeDir string) {
	t.Helper()
	persisted := filepath.Join(homeDir, "master.key")
	_, err := os.Stat(persisted)
	require.ErrorIs(t, err, os.ErrNotExist,
		"mode 0 must never persist a master key at %s (found one, or an unexpected stat error)", persisted)
}

// requireConsumed asserts the delivery file is gone.
func requireConsumed(t *testing.T, keyPath string) {
	t.Helper()
	_, err := os.Stat(keyPath)
	require.ErrorIs(t, err, os.ErrNotExist,
		"delivered key at %s must be removed after a successful unlock", keyPath)
}

// storeUnlocksWithKey reports whether credentials.json at storePath can be
// decrypted with hexKey. This is the oracle for "which key was actually
// used": a value written by the unlocked store must come back out when a
// brand-new store is unlocked with the key we believe was delivered.
func storeUnlocksWithKey(t *testing.T, storePath, hexKey, name, want string) bool {
	t.Helper()
	raw, err := hex.DecodeString(hexKey)
	require.NoError(t, err, "decode expected key")
	probe := credentials.NewStore(storePath)
	require.NoError(t, probe.UnlockWithKey(raw), "unlock probe store with expected key")
	got, err := probe.Get(name)
	if err != nil {
		return false
	}
	return got == want
}

// TestMasterKeySource_HappyPath_ConsumesAndPersistsNothing is the core case:
// the platform delivers a key, the instance unlocks with it, the delivery file
// disappears, and nothing is left on disk.
func TestMasterKeySource_HappyPath_ConsumesAndPersistsNothing(t *testing.T) {
	home := t.TempDir()
	if !permCheckSupported(t, home) {
		t.Skip("filesystem masks permission bits — perm-sensitive key delivery is not meaningful here")
	}
	_, keyPath := deliveryPoint(t, home, deliveredHexKey, 0o600)

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", keyPath)

	storePath := filepath.Join(home, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store), "Unlock with a delivered 0600 key must succeed")

	// 1. The store is genuinely usable, not merely "no error returned".
	assert.False(t, store.IsLocked(), "store must be unlocked after a delivered-key boot")
	require.NoError(t, store.Set("openrouter_api_key", "sk-test-value"), "unlocked store must accept a write")

	// 2. The key that unlocked it is the DELIVERED one — proven by decrypting
	//    what it wrote with an independently-decoded copy of that key.
	assert.True(t,
		storeUnlocksWithKey(t, storePath, deliveredHexKey, "openrouter_api_key", "sk-test-value"),
		"the delivered key must be the key the store was unlocked with")

	// 3. The delivery point was consumed.
	requireConsumed(t, keyPath)

	// 4. Nothing was persisted. This is the property the whole mode exists for.
	requireNoPersistedKey(t, home)
}

// TestMasterKeySource_TakesPriorityOverEnvVarKey pins mode 0 ahead of mode 1.
// A platform that delivers a key must win over any OMNIPUS_MASTER_KEY left in
// the environment — otherwise the env-var key (the one the custody model
// rejects) would silently be the one in force.
func TestMasterKeySource_TakesPriorityOverEnvVarKey(t *testing.T) {
	home := t.TempDir()
	if !permCheckSupported(t, home) {
		t.Skip("filesystem masks permission bits — perm-sensitive key delivery is not meaningful here")
	}
	_, keyPath := deliveryPoint(t, home, deliveredHexKey, 0o600)

	t.Setenv("OMNIPUS_KEY_FILE", "")
	t.Setenv("OMNIPUS_MASTER_KEY", otherHexKey)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", keyPath)

	storePath := filepath.Join(home, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store), "Unlock must succeed")
	require.NoError(t, store.Set("token", "delivered-wins"), "unlocked store must accept a write")

	assert.True(t,
		storeUnlocksWithKey(t, storePath, deliveredHexKey, "token", "delivered-wins"),
		"mode 0 must take priority: the DELIVERED key must be in force, not OMNIPUS_MASTER_KEY")
	assert.False(t,
		storeUnlocksWithKey(t, storePath, otherHexKey, "token", "delivered-wins"),
		"the OMNIPUS_MASTER_KEY value must NOT be the key in force")

	requireConsumed(t, keyPath)
	requireNoPersistedKey(t, home)
}

// TestMasterKeySource_Perm0644_Refused: the delivered key must satisfy the
// same strict 0600 contract as every other key file. A world-readable key on
// the delivery mount is exactly the leak the mode is meant to prevent.
func TestMasterKeySource_Perm0644_Refused(t *testing.T) {
	home := t.TempDir()
	if !permCheckSupported(t, home) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}
	_, keyPath := deliveryPoint(t, home, deliveredHexKey, 0o644)

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", keyPath)

	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	err := credentials.Unlock(store)

	require.Error(t, err, "a 0644 delivered key must be refused")
	assert.Contains(t, err.Error(), "0600", "error must name the required mode so an operator can fix it")
	assert.Contains(t, err.Error(), "OMNIPUS_MASTER_KEY_SOURCE", "error must name the mode that failed")
	assert.Contains(t, err.Error(), keyPath, "error must name the offending path")

	// Fail closed: no fall-through to auto-generate, and the store stays shut.
	assert.True(t, store.IsLocked(), "store must remain locked after a refused delivery")
	requireNoPersistedKey(t, home)

	// A key we refused to use must not be consumed — deleting it would destroy
	// the operator's only copy on the way to failing the boot anyway.
	_, statErr := os.Stat(keyPath)
	assert.NoError(t, statErr, "a refused delivery must not be deleted")
}

// TestMasterKeySource_SymlinkToMode0600_Loads: loadKeyFile deliberately allows
// a symlink whose TARGET is 0600 (Vault-managed key files are commonly
// symlinks). Mode 0 inherits that rule unchanged.
//
// It also pins the documented caveat: os.Remove unlinks the symlink, not its
// target. A delivery point that symlinks into durable storage leaves the
// target's plaintext behind — which is why the doc comment says the delivery
// point should be a real file on a tmpfs mount.
// TestMasterKeySource_Symlink_Refused: a symlinked delivery point is refused
// outright, and — the part that matters — the key it points at is left intact.
//
// This test previously asserted the opposite: that a symlink to a 0600 target
// LOADS, and it documented "removal unlinks the symlink, not its target" as a
// caveat. That caveat was the whole hole. Consuming a symlink deletes the link
// and leaves the plaintext key readable at its target, while every log line and
// audit record says the key was consumed — so the single guarantee this mode
// exists to provide would be silently false, in the one case an operator is
// least likely to check.
func TestMasterKeySource_Symlink_Refused(t *testing.T) {
	home := t.TempDir()
	deliveryDir, target := deliveryPoint(t, home, deliveredHexKey, 0o600)

	link := filepath.Join(deliveryDir, "master-key-link")
	require.NoError(t, os.Symlink(target, link), "create symlink")
	lInfo, err := os.Lstat(link)
	require.NoError(t, err, "lstat link")
	if lInfo.Mode()&os.ModeSymlink == 0 {
		t.Skip("filesystem did not preserve symlink semantics; cannot exercise the case")
	}

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", link)

	storePath := filepath.Join(home, "credentials.json")
	store := credentials.NewStore(storePath)
	err = credentials.Unlock(store)
	require.Error(t, err, "a symlinked delivery point must be refused")
	assert.Contains(t, err.Error(), "symlink",
		"the error must name the actual cause so an operator is not sent to chmod")
	assert.True(t, store.IsLocked(), "a refused delivery must leave the store locked")

	// Neither the link nor its target may be touched: we refused, so there is
	// nothing to consume, and destroying the operator's key would be worse
	// than the hole we are closing.
	_, linkErr := os.Lstat(link)
	assert.NoError(t, linkErr, "the symlink must survive a refusal")
	_, targetErr := os.Stat(target)
	assert.NoError(t, targetErr, "the target key must survive a refusal")

	requireNoPersistedKey(t, home)
}

// TestMasterKeySource_SymlinkToMode0644_Refused: following the symlink must
// refuse on the TARGET's mode, not the link's own (always-0777) metadata.
func TestMasterKeySource_SymlinkToMode0644_Refused(t *testing.T) {
	home := t.TempDir()
	if !permCheckSupported(t, home) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}
	deliveryDir, target := deliveryPoint(t, home, deliveredHexKey, 0o644)

	link := filepath.Join(deliveryDir, "master-key-link")
	require.NoError(t, os.Symlink(target, link), "create symlink")

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", link)

	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	err := credentials.Unlock(store)

	require.Error(t, err, "delivery via a symlink to a 0644 target must be refused")
	// Refused for being a symlink, which is checked before the mode is ever
	// read. The mode is a second reason it would have failed; we never get
	// that far, and asserting on "0600" here would be asserting the wrong
	// mechanism.
	assert.Contains(t, err.Error(), "symlink", "error must name the actual cause")
	assert.True(t, store.IsLocked(), "store must remain locked")
	requireNoPersistedKey(t, home)
}

// TestMasterKeySource_NotADirectory_Refused: a delivery point that is a
// directory (a mount that never got its file written into it) must be refused
// by the regular-file check rather than producing a confusing read error.
func TestMasterKeySource_Directory_Refused(t *testing.T) {
	home := t.TempDir()
	deliveryDir := filepath.Join(home, "delivery")
	require.NoError(t, os.MkdirAll(deliveryDir, 0o700))

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", deliveryDir)

	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	err := credentials.Unlock(store)

	require.Error(t, err, "a directory delivery point must be refused")
	assert.Contains(t, err.Error(), "regular file", "error must explain what is wrong with the path")
	assert.True(t, store.IsLocked(), "store must remain locked")
	requireNoPersistedKey(t, home)
}

// TestMasterKeySource_InvalidHex_Refused: a truncated or corrupt delivery is
// fatal, not a reason to fall back.
func TestMasterKeySource_InvalidHex_Refused(t *testing.T) {
	home := t.TempDir()
	_, keyPath := deliveryPoint(t, home, "not-a-valid-hex-key", 0o600)

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", keyPath)

	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	err := credentials.Unlock(store)

	require.Error(t, err, "a malformed delivered key must be refused")
	assert.Contains(t, err.Error(), "master key", "error must identify what failed to parse")
	assert.True(t, store.IsLocked(), "store must remain locked")
	requireNoPersistedKey(t, home)
}

// TestMasterKeySource_Missing_FatalNoAutoGenerate is the most important
// negative test. The instance looks completely fresh — no credentials.json, no
// master.key — which is exactly the state in which mode 4 auto-generates. If
// mode 0 ever fell through, the instance would mint a DIFFERENT key and the
// tenant's existing encrypted data would be unreachable forever.
func TestMasterKeySource_Missing_FatalNoAutoGenerate(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "delivery", "master-key")

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", missing)

	storePath := filepath.Join(home, "credentials.json")
	store := credentials.NewStore(storePath)
	err := credentials.Unlock(store)

	require.Error(t, err, "a missing delivery must be fatal")
	assert.Contains(t, err.Error(), missing, "error must name the path that was expected")
	assert.Contains(t, err.Error(), "OMNIPUS_MASTER_KEY_SOURCE", "error must name the failing mode")

	// The three fall-through tripwires.
	assert.True(t, store.IsLocked(), "store must remain LOCKED — a fall-through to mode 4 would have unlocked it")
	requireNoPersistedKey(t, home)
	_, statErr := os.Stat(storePath)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "no credentials.json may be created by a failed delivery")
}

// TestMasterKeySource_ConflictsWithPersistedKey: a delivered key AND an
// existing $OMNIPUS_HOME/master.key is an ambiguity we refuse to resolve by
// guessing. Picking either one silently risks stranding data.
func TestMasterKeySource_ConflictsWithPersistedKey(t *testing.T) {
	home := t.TempDir()
	_, keyPath := deliveryPoint(t, home, deliveredHexKey, 0o600)

	persisted := filepath.Join(home, "master.key")
	require.NoError(t, os.WriteFile(persisted, []byte(otherHexKey), 0o600), "write pre-existing master.key")

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", keyPath)

	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	err := credentials.Unlock(store)

	require.Error(t, err, "two candidate keys must be refused, not silently resolved")
	assert.Contains(t, err.Error(), keyPath, "error must name the delivered key")
	assert.Contains(t, err.Error(), persisted, "error must name the persisted key")
	assert.True(t, store.IsLocked(), "store must remain locked while the ambiguity stands")

	// Neither file may be destroyed on the way out — the operator needs both
	// to work out which one is stale.
	_, statErr := os.Stat(keyPath)
	assert.NoError(t, statErr, "the delivered key must survive a refusal")
	_, statErr = os.Stat(persisted)
	assert.NoError(t, statErr, "the persisted key must survive a refusal")
}

// removalBlockedByReadOnlyParent probes whether this host actually enforces
// "cannot unlink from a read-only directory". Running as root, and some
// container/CI filesystems, ignore it — in which case the removal-failure test
// cannot exercise anything and must skip rather than pass vacuously.
func removalBlockedByReadOnlyParent(t *testing.T, dir string) bool {
	t.Helper()
	probeDir := filepath.Join(dir, "ro-probe")
	require.NoError(t, os.MkdirAll(probeDir, 0o700), "create probe dir")
	probe := filepath.Join(probeDir, "victim")
	require.NoError(t, os.WriteFile(probe, []byte("x"), 0o600), "write probe file")
	require.NoError(t, os.Chmod(probeDir, 0o500), "chmod probe dir read-only")

	removeErr := os.Remove(probe)

	require.NoError(t, os.Chmod(probeDir, 0o700), "restore probe dir perms")
	require.NoError(t, os.RemoveAll(probeDir), "clean up probe dir")
	return removeErr != nil
}

// TestMasterKeySource_RemovalFailure_UnlockStillSucceeds: a read-only secret
// mount is a legitimate platform choice. We cannot consume the file there, and
// that must be a WARN about plaintext left at rest — never a failed boot.
func TestMasterKeySource_RemovalFailure_UnlockStillSucceeds(t *testing.T) {
	home := t.TempDir()
	if !permCheckSupported(t, home) {
		t.Skip("filesystem masks permission bits — cannot construct a read-only delivery mount here")
	}
	if !removalBlockedByReadOnlyParent(t, home) {
		t.Skip("this host allows unlinking from a read-only directory (running as root, or a permissive filesystem) — " +
			"a removal failure cannot be forced, so the test would assert nothing")
	}

	deliveryDir, keyPath := deliveryPoint(t, home, deliveredHexKey, 0o600)

	// Make the delivery point unwritable so os.Remove fails. Restore perms in
	// a cleanup registered AFTER t.TempDir()'s own, so it runs first (LIFO)
	// and TempDir's RemoveAll still succeeds.
	require.NoError(t, os.Chmod(deliveryDir, 0o500), "make delivery dir read-only")
	t.Cleanup(func() {
		if err := os.Chmod(deliveryDir, 0o700); err != nil {
			t.Errorf("restore delivery dir perms: %v", err)
		}
	})

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", keyPath)

	storePath := filepath.Join(home, "credentials.json")
	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store),
		"a delivery point we cannot write to must NOT fail the boot — it is a WARN, not a fatal error")

	assert.False(t, store.IsLocked(), "store must be unlocked despite the failed removal")
	require.NoError(t, store.Set("token", "removal-failed"))
	assert.True(t,
		storeUnlocksWithKey(t, storePath, deliveredHexKey, "token", "removal-failed"),
		"the delivered key must still be the key in force")

	// The honest, uncomfortable half: the plaintext key is still there.
	_, statErr := os.Stat(keyPath)
	assert.NoError(t, statErr, "the un-removable delivery file is expected to remain (that is what the WARN reports)")

	// Even so, nothing was persisted into the Omnipus home.
	requireNoPersistedKey(t, home)
}

// TestMasterKeySource_RedeliveryAcrossBoots simulates a restart. Because mode 0
// persists nothing, the platform MUST deliver the key again — and when it does,
// the data written during the first boot must still decrypt. This is the
// consequence documented on Unlock, asserted rather than assumed.
func TestMasterKeySource_RedeliveryAcrossBoots(t *testing.T) {
	home := t.TempDir()
	if !permCheckSupported(t, home) {
		t.Skip("filesystem masks permission bits — perm-sensitive key delivery is not meaningful here")
	}
	storePath := filepath.Join(home, "credentials.json")
	clearOtherKeyModes(t)

	// --- Boot 1: key delivered, secret written. ---
	_, keyPath := deliveryPoint(t, home, deliveredHexKey, 0o600)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", keyPath)

	boot1 := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(boot1), "boot 1 must unlock")
	require.NoError(t, boot1.Set("telegram_token", "123:abc"), "boot 1 must write a secret")
	requireConsumed(t, keyPath)
	requireNoPersistedKey(t, home)

	// --- Boot 2 without a re-delivery: the instance has no key at all. ---
	// This is the intended consequence of persisting nothing.
	boot2 := credentials.NewStore(storePath)
	err := credentials.Unlock(boot2)
	require.Error(t, err, "with nothing persisted and nothing re-delivered, boot 2 must fail rather than invent a key")
	assert.True(t, boot2.IsLocked(), "boot 2 must remain locked")
	requireNoPersistedKey(t, home)

	// --- Boot 3: the platform re-delivers the same key. ---
	_, keyPath2 := deliveryPoint(t, home, deliveredHexKey, 0o600)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", keyPath2)

	boot3 := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(boot3), "boot 3 must unlock from the re-delivered key")
	got, err := boot3.Get("telegram_token")
	require.NoError(t, err, "boot 3 must read the secret written during boot 1")
	assert.Equal(t, "123:abc", got, "re-delivery of the same key must make boot 1's data readable again")

	requireConsumed(t, keyPath2)
	requireNoPersistedKey(t, home)
}

// TestMasterKeySource_Unset_LeavesLadderUnchanged is a guard on the other five
// modes: adding mode 0 must not change what happens when its env var is unset.
// With OMNIPUS_MASTER_KEY_SOURCE empty and a 0600 key at OMNIPUS_KEY_FILE,
// mode 2 must still be what fires — and mode 2 does NOT consume its file.
// TestMasterKeySource_WrongKeyForExistingStore_RefusedAndPreserved is the
// highest-severity case this mode has.
//
// UnlockWithKey validates length and nothing else, so without a decryption
// probe ANY 32 bytes unlock "successfully" — and the delivery file is then
// deleted. A stale or wrong-tenant key would leave the instance running,
// unable to read a single existing credential, while the only copy of the key
// that WOULD have worked is gone. Writes would then append entries under the
// wrong key, leaving one file holding two key generations, where a later boot
// with the correct key silently cannot read the newer half.
//
// So: refuse, and leave the delivery file in place so the right key can be
// delivered.
func TestMasterKeySource_WrongKeyForExistingStore_RefusedAndPreserved(t *testing.T) {
	home := t.TempDir()
	storePath := filepath.Join(home, "credentials.json")

	// An existing store, encrypted under the RIGHT key, holding one entry.
	rightKey, err := hex.DecodeString(deliveredHexKey)
	require.NoError(t, err)
	seeded := credentials.NewStore(storePath)
	require.NoError(t, seeded.UnlockWithKey(rightKey))
	require.NoError(t, seeded.Set("existing", "secret-value"))

	// Deliver a DIFFERENT, well-formed 32-byte key.
	const wrongHexKey = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	_, deliveredPath := deliveryPoint(t, home, wrongHexKey, 0o600)

	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", deliveredPath)

	store := credentials.NewStore(storePath)
	err = credentials.Unlock(store)

	require.Error(t, err, "a key that cannot decrypt the existing store must be refused")
	assert.True(t, store.IsLocked(), "a refused delivery must leave the store locked")

	// The delivery file MUST survive — destroying it is the damage.
	_, statErr := os.Stat(deliveredPath)
	assert.NoError(t, statErr,
		"the delivery file must be preserved on refusal, so the correct key can still be delivered")

	// And the existing data must still open with the right key.
	assert.True(t,
		storeUnlocksWithKey(t, storePath, deliveredHexKey, "existing", "secret-value"),
		"the existing store must remain readable with its real key")

	requireNoPersistedKey(t, home)
}

// TestMasterKeySource_CorrectKeyForExistingStore_Accepted is the other half:
// the probe must not reject a key that genuinely works, or re-delivery across
// boots would break on the second boot.
func TestMasterKeySource_CorrectKeyForExistingStore_Accepted(t *testing.T) {
	home := t.TempDir()
	storePath := filepath.Join(home, "credentials.json")

	rightKey, err := hex.DecodeString(deliveredHexKey)
	require.NoError(t, err)
	seeded := credentials.NewStore(storePath)
	require.NoError(t, seeded.UnlockWithKey(rightKey))
	require.NoError(t, seeded.Set("existing", "secret-value"))

	_, deliveredPath := deliveryPoint(t, home, deliveredHexKey, 0o600)
	clearOtherKeyModes(t)
	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", deliveredPath)

	store := credentials.NewStore(storePath)
	require.NoError(t, credentials.Unlock(store),
		"the correct key must still be accepted against a non-empty store")

	got, err := store.Get("existing")
	require.NoError(t, err)
	assert.Equal(t, "secret-value", got)

	requireConsumed(t, deliveredPath)
	requireNoPersistedKey(t, home)
}

func TestMasterKeySource_Unset_LeavesLadderUnchanged(t *testing.T) {
	home := t.TempDir()
	if !permCheckSupported(t, home) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}
	_, keyPath := deliveryPoint(t, home, deliveredHexKey, 0o600)

	t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", "")
	t.Setenv("OMNIPUS_MASTER_KEY", "")
	t.Setenv("OMNIPUS_KEY_FILE", keyPath)

	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store), "mode 2 must still work")
	assert.False(t, store.IsLocked(), "store must be unlocked")

	// The distinguishing behaviour: mode 2 PERSISTS by leaving the file alone.
	_, statErr := os.Stat(keyPath)
	assert.NoError(t, statErr, "OMNIPUS_KEY_FILE must NOT be consumed — only OMNIPUS_MASTER_KEY_SOURCE consumes")
}

// TestMasterKeySource_ErrorMessagesAreActionable checks the operator-facing
// half of fail-closed: an error that does not say which path and which mode
// failed leaves a hosted operator with a dead instance and no lead.
func TestMasterKeySource_ErrorMessagesAreActionable(t *testing.T) {
	home := t.TempDir()
	if !permCheckSupported(t, home) {
		t.Skip("filesystem masks permission bits — perm tests are not meaningful here")
	}

	tests := []struct {
		name        string
		setup       func(t *testing.T, home string) string // returns the source path
		wantPhrases []string
	}{
		{
			name: "missing file",
			setup: func(t *testing.T, home string) string {
				t.Helper()
				return filepath.Join(home, "delivery", "absent-key")
			},
			wantPhrases: []string{"OMNIPUS_MASTER_KEY_SOURCE", "absent-key"},
		},
		{
			name: "wrong perms",
			setup: func(t *testing.T, home string) string {
				t.Helper()
				_, p := deliveryPoint(t, home, deliveredHexKey, 0o640)
				return p
			},
			wantPhrases: []string{"OMNIPUS_MASTER_KEY_SOURCE", "0600", "refusing to load"},
		},
		{
			name: "ambiguous with persisted key",
			setup: func(t *testing.T, home string) string {
				t.Helper()
				_, p := deliveryPoint(t, home, deliveredHexKey, 0o600)
				require.NoError(t, os.WriteFile(filepath.Join(home, "master.key"), []byte(otherHexKey), 0o600))
				return p
			},
			wantPhrases: []string{"OMNIPUS_MASTER_KEY_SOURCE", "master.key", "refusing to guess"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			caseHome := t.TempDir()
			sourcePath := tc.setup(t, caseHome)

			clearOtherKeyModes(t)
			t.Setenv("OMNIPUS_MASTER_KEY_SOURCE", sourcePath)

			store := credentials.NewStore(filepath.Join(caseHome, "credentials.json"))
			err := credentials.Unlock(store)
			require.Error(t, err, "this case must fail closed")
			for _, phrase := range tc.wantPhrases {
				assert.Contains(t, err.Error(), phrase, "error must be actionable; got: %v", err)
			}
			assert.True(t, store.IsLocked(), "store must remain locked")
		})
	}
}
