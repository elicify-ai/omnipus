// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the one-time migration of a pre-name-binding credential store
// (format version 1, entries sealed with a nil AES-GCM AAD) to the name-bound
// format (version 2, every entry sealed with aadFor(name)).
//
// Traces to: pkg/credentials/store.go migrateLegacyLocked, UnlockWithKey,
// UnlockWithPassphrase, loadFileInternal; squad BG brief requirements 1-5.

package credentials

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/argon2"
)

// The pre-binding format version, as the released v0.1.0 binary wrote it.
// Stated as a literal (not the package constant) so the test's oracle is the
// on-disk history, not the implementation under test.
const onDiskLegacyVersion = 1

// sealWith seals plaintext under key with the given AAD — nil reproduces the
// pre-binding writer exactly.
func sealWith(t *testing.T, key []byte, plaintext string, aad []byte) encEntry {
	t.Helper()
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, nonceLen)
	_, err = io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	return encEntry{
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, []byte(plaintext), aad)),
	}
}

// writeLegacyStore writes a version-1 store whose entries are all sealed with
// a nil AAD under key — byte-for-byte the shape a v0.1.0 install has on disk.
func writeLegacyStore(t *testing.T, path string, key []byte, values map[string]string) {
	t.Helper()
	salt := make([]byte, saltLen)
	_, err := io.ReadFull(rand.Reader, salt)
	require.NoError(t, err)
	writeLegacyStoreWithSalt(t, path, key, salt, values)
}

func writeLegacyStoreWithSalt(t *testing.T, path string, key, salt []byte, values map[string]string) {
	t.Helper()
	sf := &storeFile{
		Version:     onDiskLegacyVersion,
		Salt:        base64.StdEncoding.EncodeToString(salt),
		Credentials: make(map[string]encEntry, len(values)),
	}
	for name, v := range values {
		sf.Credentials[name] = sealWith(t, key, v, nil)
	}
	aadTestWriteFile(t, path, sf)
}

func mustReadBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func legacyValues() map[string]string {
	return map[string]string{
		"openai_API_KEY":          "sk-openai-legacy-0001",
		"anthropic_API_KEY":       "sk-ant-legacy-0002",
		"channel_telegram_token":  "123456:telegram-legacy",
		"EMPTY_VALUE":             "",
		"UNICODE_VALUE":           "パスワード🔑",
		"openrouter_OAUTH":        `{"access":"tok","refresh":"r"}`,
		"SHARED_VALUE_ONE":        "identical-plaintext",
		"SHARED_VALUE_TWO":        "identical-plaintext",
		"mcp_github_GITHUB_TOKEN": "ghp_legacy",
	}
}

// TestMigrate_LegacyStoreMigratesAndAllValuesReadable is the upgrade case the
// founder asked for: a v0.1.0 store unlocks under the same key and every value
// reads back, with no re-entry. The file must end at the name-bound version
// with every entry opening under aadFor(name) and NOT under a nil AAD.
func TestMigrate_LegacyStoreMigratesAndAllValuesReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x61)
	want := legacyValues()
	writeLegacyStore(t, path, key, want)

	store := NewStore(path)
	require.NoError(t, store.UnlockWithKey(key), "unlocking a legacy store must migrate it, not fail")
	assert.False(t, store.IsLocked())

	for name, v := range want {
		got, err := store.Get(name)
		require.NoError(t, err, "Get(%q) after migration", name)
		assert.Equal(t, v, got, "value of %q must survive migration", name)
	}

	sf := aadTestReadFile(t, path)
	assert.Equal(t, 2, sf.Version, "a migrated store must record the name-bound format version")
	require.Len(t, sf.Credentials, len(want), "migration must neither drop nor add entries")

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	for name, e := range sf.Credentials {
		nonce, nErr := base64.StdEncoding.DecodeString(e.Nonce)
		require.NoError(t, nErr)
		ct, cErr := base64.StdEncoding.DecodeString(e.Ciphertext)
		require.NoError(t, cErr)
		_, nilErr := gcm.Open(nil, nonce, ct, nil)
		assert.Error(t, nilErr, "entry %q must no longer open with a nil AAD", name)
		plain, boundErr := gcm.Open(nil, nonce, ct, []byte("omnipus-credential-v1:"+name))
		require.NoError(t, boundErr, "entry %q must open under its own name binding", name)
		assert.Equal(t, want[name], string(plain))
	}

	_, statErr := os.Stat(path + ".migrated")
	assert.NoError(t, statErr, "a completed migration must be recorded next to the store")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the migrated store must stay 0600")
}

// TestMigrate_PassphraseUnlockMigrates covers unlock mode 5: the key is derived
// from the legacy file's own salt, the migration re-keys under a fresh salt,
// and the same passphrase must keep working afterwards.
func TestMigrate_PassphraseUnlockMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	salt := make([]byte, saltLen)
	_, err := io.ReadFull(rand.Reader, salt)
	require.NoError(t, err)
	key := argon2.IDKey([]byte("legacy-passphrase"), salt, argonTime, argonMemory, argonThreads, keyLen)
	writeLegacyStoreWithSalt(t, path, key, salt, map[string]string{"A": "value-a", "B": "value-b"})

	store := NewStore(path)
	require.NoError(t, store.UnlockWithPassphrase("legacy-passphrase"))
	got, err := store.Get("A")
	require.NoError(t, err)
	assert.Equal(t, "value-a", got)

	// Changed by the security review of 5a1a48f45 (item 1b): this used to
	// assert the salt is KEPT. The migration now re-keys under a fresh salt so
	// pre-upgrade entries stop opening; what must hold is that the SAME
	// passphrase keeps working, asserted just below.
	assert.NotEqual(t, base64.StdEncoding.EncodeToString(salt), aadTestReadFile(t, path).Salt,
		"a passphrase-mode migration re-keys under a fresh salt")

	again := NewStore(path)
	require.NoError(t, again.UnlockWithPassphrase("legacy-passphrase"))
	got, err = again.Get("B")
	require.NoError(t, err)
	assert.Equal(t, "value-b", got)
}

// TestMigrate_SecondUnlockDoesNotMigrateAgain proves the migration is one-time:
// a second unlock of the migrated store performs no write at all. Nonces are
// random, so any re-seal would change the bytes.
func TestMigrate_SecondUnlockDoesNotMigrateAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x62)
	writeLegacyStore(t, path, key, map[string]string{"A": "value-a", "B": "value-b"})

	first := NewStore(path)
	require.NoError(t, first.UnlockWithKey(key))
	afterFirst := mustReadBytes(t, path)

	var writes int
	orig := writeFileAtomicFn
	writeFileAtomicFn = func(p string, data []byte, perm os.FileMode) error {
		writes++
		return orig(p, data, perm)
	}
	t.Cleanup(func() { writeFileAtomicFn = orig })

	second := NewStore(path)
	require.NoError(t, second.UnlockWithKey(key))
	assert.Equal(t, 0, writes, "unlocking an already-migrated store must not write")
	assert.Equal(t, afterFirst, mustReadBytes(t, path), "the migrated file must be byte-identical after a second unlock")

	got, err := second.Get("B")
	require.NoError(t, err)
	assert.Equal(t, "value-b", got)
}

// TestMigrate_SwapInMigratedStoreIsRejected: once migrated, the #85 swap
// defence holds exactly as for a store born name-bound.
func TestMigrate_SwapInMigratedStoreIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x63)
	writeLegacyStore(t, path, key, map[string]string{"SECRET_A": "value-a", "SECRET_B": "value-b"})

	store := NewStore(path)
	require.NoError(t, store.UnlockWithKey(key))

	sf := aadTestReadFile(t, path)
	sf.Credentials["SECRET_A"], sf.Credentials["SECRET_B"] = sf.Credentials["SECRET_B"], sf.Credentials["SECRET_A"]
	aadTestWriteFile(t, path, sf)

	for _, name := range []string{"SECRET_A", "SECRET_B"} {
		got, err := store.Get(name)
		require.Error(t, err, "a swapped entry in a migrated store must not open")
		assert.Empty(t, got)
		var authErr *EntryAuthError
		require.ErrorAs(t, err, &authErr)
		assert.Equal(t, name, authErr.Name)
	}

	// A fresh unlock does not "re-migrate" its way around the swap either:
	// the file is at the name-bound version, so the legacy path is unreachable.
	fresh := NewStore(path)
	require.NoError(t, fresh.UnlockWithKey(key))
	_, err := fresh.Get("SECRET_A")
	var authErr *EntryAuthError
	require.ErrorAs(t, err, &authErr, "a fresh unlock must still reject the swap")
}

// TestMigrate_DowngradedVersionFieldCannotLaunderASwap: an attacker who swaps
// two entries of a MIGRATED store and rewrites "version" back to 1 must not be
// able to push the swap through the migration path. The name-bound entries do
// not open under a nil AAD, and they do not open under the name they were
// moved to, so the unlock refuses and the file is left untouched.
func TestMigrate_DowngradedVersionFieldCannotLaunderASwap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x64)
	writeLegacyStore(t, path, key, map[string]string{"SECRET_A": "value-a", "SECRET_B": "value-b"})
	require.NoError(t, NewStore(path).UnlockWithKey(key))
	// Remove the marker too, so this test isolates the cryptographic defence
	// from the replay record (which TestMigrate_ReplayOfLegacyBackupIsRefused covers).
	require.NoError(t, os.Remove(path+".migrated"))

	sf := aadTestReadFile(t, path)
	sf.Version = onDiskLegacyVersion
	sf.Credentials["SECRET_A"], sf.Credentials["SECRET_B"] = sf.Credentials["SECRET_B"], sf.Credentials["SECRET_A"]
	aadTestWriteFile(t, path, sf)
	before := mustReadBytes(t, path)

	store := NewStore(path)
	err := store.UnlockWithKey(key)
	require.Error(t, err, "a downgraded store carrying swapped name-bound entries must not migrate")
	assert.True(t, store.IsLocked(), "a refused migration must leave the store locked")
	assert.ErrorIs(t, err, ErrWrongKey, "the refusal must keep the fatal classification")
	var authErr *EntryAuthError
	require.ErrorAs(t, err, &authErr)
	assert.Contains(t, err.Error(), "SECRET_A")
	assert.Contains(t, err.Error(), "SECRET_B")
	assert.Equal(t, before, mustReadBytes(t, path), "a refused migration must not touch the file")
}

// TestMigrate_SwapInLegacyStoreIsLaunderedResidualRisk documents, rather than
// hides, the one thing migration cannot do: a nil-AAD ciphertext carries no
// name, so a swap made to a LEGACY file is indistinguishable from the genuine
// layout and is re-sealed under the name it was moved to. This test pins that
// behaviour so the residual risk is visible; the report and docs/security.md
// state it. The window is the lifetime of the current secret, not one unlock
// — see TestMigrate_WholeOldCopyRestoreIsResidualUntilSecretChanges.
func TestMigrate_SwapInLegacyStoreIsLaunderedResidualRisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x65)
	writeLegacyStore(t, path, key, map[string]string{"SECRET_A": "value-a", "SECRET_B": "value-b"})

	sf := aadTestReadFile(t, path)
	sf.Credentials["SECRET_A"], sf.Credentials["SECRET_B"] = sf.Credentials["SECRET_B"], sf.Credentials["SECRET_A"]
	aadTestWriteFile(t, path, sf)

	store := NewStore(path)
	require.NoError(t, store.UnlockWithKey(key),
		"a pre-migration swap is not detectable: the legacy format has no name binding to check")
	a, err := store.Get("SECRET_A")
	require.NoError(t, err)
	assert.Equal(t, "value-b", a, "RESIDUAL RISK: the swap made before migration is carried into the bound format")

	// A swap made to the MIGRATED file is rejected (see also
	// TestMigrate_SwapInMigratedStoreIsRejected).
	sf = aadTestReadFile(t, path)
	sf.Credentials["SECRET_A"], sf.Credentials["SECRET_B"] = sf.Credentials["SECRET_B"], sf.Credentials["SECRET_A"]
	aadTestWriteFile(t, path, sf)
	_, err = store.Get("SECRET_A")
	var authErr *EntryAuthError
	require.ErrorAs(t, err, &authErr, "a swap after migration must be rejected")
}

// TestMigrate_ReplayOfLegacyBackupIsRefused closes the replay path: an
// attacker who kept a copy of the pre-upgrade legacy file (encrypted under the
// SAME key) swaps entries in it and restores it after the install migrated.
// Without a record of the migration this would re-run the migration and
// launder the swap. The install's migration record makes it a hard refusal.
func TestMigrate_ReplayOfLegacyBackupIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x66)
	writeLegacyStore(t, path, key, map[string]string{"SECRET_A": "value-a", "SECRET_B": "value-b"})
	backup := mustReadBytes(t, path)

	require.NoError(t, NewStore(path).UnlockWithKey(key))

	var sf storeFile
	require.NoError(t, json.Unmarshal(backup, &sf))
	sf.Credentials["SECRET_A"], sf.Credentials["SECRET_B"] = sf.Credentials["SECRET_B"], sf.Credentials["SECRET_A"]
	aadTestWriteFile(t, path, &sf)
	restored := mustReadBytes(t, path)

	store := NewStore(path)
	err := store.UnlockWithKey(key)
	require.Error(t, err, "a legacy file appearing after this install migrated must be refused")
	assert.ErrorIs(t, err, ErrLegacyStoreAfterMigration)
	assert.True(t, store.IsLocked())
	assert.Equal(t, restored, mustReadBytes(t, path), "the refused file must not be rewritten")
	assert.Contains(t, err.Error(), ".migrated", "the message must name the record so an operator can act deliberately")
}

// TestMigrate_FailedEntryLeavesStoreUnchanged covers requirement 3: one entry
// that fails even the legacy read means NO entry is migrated, the file is
// byte-identical, the store stays locked, and the error names the entry and
// keeps the fatal (ErrWrongKey) classification the boot path relies on.
func TestMigrate_FailedEntryLeavesStoreUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x67)
	writeLegacyStore(t, path, key, map[string]string{"GOOD_ONE": "v1", "GOOD_TWO": "v2", "BROKEN_ENTRY": "v3"})

	sf := aadTestReadFile(t, path)
	e := sf.Credentials["BROKEN_ENTRY"]
	raw, err := base64.StdEncoding.DecodeString(e.Ciphertext)
	require.NoError(t, err)
	raw[0] ^= 0x01
	e.Ciphertext = base64.StdEncoding.EncodeToString(raw)
	sf.Credentials["BROKEN_ENTRY"] = e
	aadTestWriteFile(t, path, sf)
	before := mustReadBytes(t, path)

	store := NewStore(path)
	err = store.UnlockWithKey(key)
	require.Error(t, err)
	assert.True(t, store.IsLocked(), "a refused migration must leave the store locked")
	assert.Equal(t, before, mustReadBytes(t, path), "no half-migration: the file must be byte-identical")
	_, statErr := os.Stat(path + ".migrated")
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "a refused migration must not be recorded as done")

	var authErr *EntryAuthError
	require.ErrorAs(t, err, &authErr, "the failing entry must surface as an EntryAuthError")
	assert.Equal(t, "BROKEN_ENTRY", authErr.Name)
	assert.ErrorIs(t, err, ErrWrongKey, "fatal classification must be preserved")
	var migErr *MigrationError
	require.ErrorAs(t, err, &migErr)
	assert.Equal(t, []string{"BROKEN_ENTRY"}, migErr.Failed, "only the broken entry is named")
	assert.Equal(t, 3, migErr.Total)
	assert.NotContains(t, err.Error(), "GOOD_ONE", "entries that opened fine must not be blamed")
	for _, secret := range []string{"v1", "v2", "v3"} {
		assert.NotContains(t, err.Error(), "\""+secret+"\"", "no secret value may appear in the error")
	}

	// Store-wide error, gateway-boot shape: every read is refused while locked.
	_, getErr := store.Get("GOOD_ONE")
	assert.ErrorIs(t, getErr, ErrStoreLocked)
}

// TestMigrate_WrongKeyRefusedAndNamesEveryEntry: a wrong master key fails every
// entry; the migration writes nothing and the error lets an operator see the
// whole-store scope (every entry named), which is the wrong-key signature.
func TestMigrate_WrongKeyRefusedAndNamesEveryEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	writeLegacyStore(t, path, aadTestKey(0x68), map[string]string{"A": "a", "B": "b"})
	before := mustReadBytes(t, path)

	store := NewStore(path)
	err := store.UnlockWithKey(aadTestKey(0x99))
	require.Error(t, err)
	assert.True(t, store.IsLocked())
	assert.Equal(t, before, mustReadBytes(t, path))
	var migErr *MigrationError
	require.ErrorAs(t, err, &migErr)
	assert.Equal(t, []string{"A", "B"}, migErr.Failed)
	assert.Contains(t, err.Error(), "master key", "when every entry fails the message must point at the key")
}

// TestMigrate_CrashDuringWriteLeavesOldFile simulates the process dying before
// the atomic rename lands: the store write fails, and the original legacy file
// must be intact (never a mix), the migration unrecorded, and the next unlock
// must complete it cleanly.
func TestMigrate_CrashDuringWriteLeavesOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x69)
	want := map[string]string{"A": "value-a", "B": "value-b", "C": "value-c"}
	writeLegacyStore(t, path, key, want)
	before := mustReadBytes(t, path)

	var storeWrites int
	orig := writeFileAtomicFn
	writeFileAtomicFn = func(p string, data []byte, perm os.FileMode) error {
		storeWrites++
		// Leave a stray temp file as a crashed writer would, then die before
		// the rename.
		require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(p), ".tmp-crashed"), data[:len(data)/2], 0o600))
		return errors.New("simulated crash before rename")
	}
	err := NewStore(path).UnlockWithKey(key)
	writeFileAtomicFn = orig

	require.Error(t, err, "a failed migration write must fail the unlock")
	assert.Equal(t, 1, storeWrites, "the whole migrated store must be written in ONE atomic write")
	assert.Equal(t, before, mustReadBytes(t, path), "a crash mid-migration must leave the old file, never a mix")
	_, statErr := os.Stat(path + ".migrated")
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "a crashed migration must not be recorded")

	store := NewStore(path)
	require.NoError(t, store.UnlockWithKey(key), "the next unlock must complete the migration")
	for name, v := range want {
		got, err := store.Get(name)
		require.NoError(t, err)
		assert.Equal(t, v, got)
	}
}

// TestMigrate_MixedLegacyAndBoundEntries covers an install that ran a
// pre-release build carrying the name binding without the version bump: its
// file says version 1 but some entries are already name-bound. Both kinds
// migrate; a bound entry opens only under its own name, so accepting it adds
// no swap surface.
func TestMigrate_MixedLegacyAndBoundEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x6a)
	writeLegacyStore(t, path, key, map[string]string{"LEGACY_ONE": "legacy-value"})
	sf := aadTestReadFile(t, path)
	sf.Credentials["BOUND_ONE"] = sealWith(t, key, "bound-value", aadFor("BOUND_ONE"))
	aadTestWriteFile(t, path, sf)

	store := NewStore(path)
	require.NoError(t, store.UnlockWithKey(key))
	got, err := store.Get("LEGACY_ONE")
	require.NoError(t, err)
	assert.Equal(t, "legacy-value", got)
	got, err = store.Get("BOUND_ONE")
	require.NoError(t, err)
	assert.Equal(t, "bound-value", got)
}

// TestMigrate_BoundEntryMovedInLegacyStoreIsRefused: in a mixed version-1 file
// a name-bound entry moved to another name opens neither with a nil AAD nor
// with the new name, so migration refuses rather than launder it.
func TestMigrate_BoundEntryMovedInLegacyStoreIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x6b)
	writeLegacyStore(t, path, key, map[string]string{"LEGACY_ONE": "legacy-value"})
	sf := aadTestReadFile(t, path)
	sf.Credentials["VICTIM"] = sealWith(t, key, "bound-value", aadFor("ATTACKER_CHOSEN"))
	aadTestWriteFile(t, path, sf)
	before := mustReadBytes(t, path)

	err := NewStore(path).UnlockWithKey(key)
	var authErr *EntryAuthError
	require.ErrorAs(t, err, &authErr)
	assert.Equal(t, "VICTIM", authErr.Name)
	assert.Equal(t, before, mustReadBytes(t, path))
}

// TestMigrate_RotationAfterMigration covers requirement 5: both rotation
// entry points work on a migrated store and keep the binding.
func TestMigrate_RotationAfterMigration(t *testing.T) {
	for _, mode := range []string{"key", "passphrase"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credentials.json")
			key := aadTestKey(0x6c)
			want := map[string]string{"A": "value-a", "B": "value-b", "EMPTY": ""}
			writeLegacyStore(t, path, key, want)

			store := NewStore(path)
			require.NoError(t, store.UnlockWithKey(key))
			newKey := aadTestKey(0x6d)
			if mode == "key" {
				require.NoError(t, store.Rotate(newKey))
			} else {
				require.NoError(t, store.RotateWithPassphrase("rotated-passphrase"))
			}

			fresh := NewStore(path)
			if mode == "key" {
				require.NoError(t, fresh.UnlockWithKey(newKey))
			} else {
				require.NoError(t, fresh.UnlockWithPassphrase("rotated-passphrase"))
			}
			for name, v := range want {
				got, err := fresh.Get(name)
				require.NoError(t, err, "Get(%q) after rotation", name)
				assert.Equal(t, v, got)
			}
			assert.Equal(t, 2, aadTestReadFile(t, path).Version)

			sf := aadTestReadFile(t, path)
			sf.Credentials["A"], sf.Credentials["B"] = sf.Credentials["B"], sf.Credentials["A"]
			aadTestWriteFile(t, path, sf)
			_, err := fresh.Get("A")
			var authErr *EntryAuthError
			require.ErrorAs(t, err, &authErr, "a swap after rotation must still be rejected")
		})
	}
}

// TestMigrate_LegacyFileAfterUnlockIsRefusedEverywhere: if a legacy file
// replaces the store while it is unlocked, no operation may read it through a
// legacy path or silently stamp it with the new version. Every path refuses.
func TestMigrate_LegacyFileAfterUnlockIsRefusedEverywhere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x6e)
	store := NewStore(path)
	require.NoError(t, store.UnlockWithKey(key))
	require.NoError(t, store.Set("A", "value-a"))

	writeLegacyStore(t, path, key, map[string]string{"A": "legacy-a", "B": "legacy-b"})
	before := mustReadBytes(t, path)

	_, err := store.Get("A")
	assert.ErrorIs(t, err, ErrLegacyStoreFormat, "Get")
	assert.ErrorIs(t, store.Set("C", "c"), ErrLegacyStoreFormat, "Set")
	assert.ErrorIs(t, store.Delete("A"), ErrLegacyStoreFormat, "Delete")
	_, err = store.List()
	assert.ErrorIs(t, err, ErrLegacyStoreFormat, "List")
	assert.ErrorIs(t, store.Rotate(aadTestKey(0x6f)), ErrLegacyStoreFormat, "Rotate")
	assert.ErrorIs(t, store.RotateWithPassphrase("p"), ErrLegacyStoreFormat, "RotateWithPassphrase")
	assert.Equal(t, before, mustReadBytes(t, path), "no path may rewrite a legacy file outside the migration")
}

// TestMigrate_UnknownVersionIsRefused: a file from a newer binary (or a
// hand-edited version) is neither read nor rewritten.
func TestMigrate_UnknownVersionIsRefused(t *testing.T) {
	for _, v := range []int{0, 3, 99} {
		path := filepath.Join(t.TempDir(), "credentials.json")
		key := aadTestKey(0x70)
		writeLegacyStore(t, path, key, map[string]string{"A": "a"})
		sf := aadTestReadFile(t, path)
		sf.Version = v
		aadTestWriteFile(t, path, sf)
		before := mustReadBytes(t, path)

		store := NewStore(path)
		err := store.UnlockWithKey(key)
		require.Error(t, err, "version %d must be refused", v)
		assert.ErrorIs(t, err, ErrUnsupportedStoreVersion)
		assert.True(t, store.IsLocked())
		assert.Equal(t, before, mustReadBytes(t, path))
	}
}

// TestMigrate_FreshStoreIsWrittenAtCurrentVersion: a brand-new store is born
// name-bound, never passes through the legacy version, and needs no record.
func TestMigrate_FreshStoreIsWrittenAtCurrentVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := NewStore(path)
	require.NoError(t, store.UnlockWithPassphrase("fresh-passphrase"))
	assert.Equal(t, 2, aadTestReadFile(t, path).Version, "the salt-only file must be born at the current version")
	require.NoError(t, store.Set("A", "a"))
	assert.Equal(t, 2, aadTestReadFile(t, path).Version)
	_, statErr := os.Stat(path + ".migrated")
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "a store that never was legacy has no migration record")
}

// TestMigrate_ConcurrentUnlocksMigrateOnce: two processes' worth of stores
// unlocking the same legacy file at once must serialize on the sidecar lock —
// both succeed and every value reads back. Run under -race. This test alone
// does NOT prove the lock (it stays green with the lock replaced by a no-op);
// TestMigrate_SecondMigratorWaitsForTheLock does.
func TestMigrate_ConcurrentUnlocksMigrateOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x71)
	want := legacyValues()
	writeLegacyStore(t, path, key, want)

	const n = 6
	stores := make([]*Store, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		stores[i] = NewStore(path)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = stores[i].UnlockWithKey(key)
		}(i)
	}
	wg.Wait()
	for i := range n {
		require.NoError(t, errs[i], "concurrent unlock %d", i)
		for name, v := range want {
			got, err := stores[i].Get(name)
			require.NoError(t, err)
			assert.Equal(t, v, got)
		}
	}
	assert.Equal(t, 2, aadTestReadFile(t, path).Version)
}

// captureHandler records slog records for the audit assertions.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) find(msg string) (map[string]string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message != msg {
			continue
		}
		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		return attrs, true
	}
	return nil, false
}

// findWith returns the first record with message msg whose key attribute
// equals val.
func (h *captureHandler) findWith(msg, key, val string) (map[string]string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message != msg {
			continue
		}
		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		if attrs[key] == val {
			return attrs, true
		}
	}
	return nil, false
}

func (h *captureHandler) all() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var b bytes.Buffer
	for _, r := range h.records {
		b.WriteString(r.Message)
		r.Attrs(func(a slog.Attr) bool {
			b.WriteString(" " + a.Key + "=" + a.Value.String())
			return true
		})
		b.WriteString("\n")
	}
	return b.String()
}

// TestMigrate_AuditRecordsCountOnly covers requirement 4: the migration and a
// refused migration are audit-logged with counts, never secret values and
// never entry names.
func TestMigrate_AuditRecordsCountOnly(t *testing.T) {
	h := &captureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x72)
	want := map[string]string{"NAME_ALPHA": "secret-alpha-value", "NAME_BETA": "secret-beta-value"}
	writeLegacyStore(t, path, key, want)
	require.NoError(t, NewStore(path).UnlockWithKey(key))

	attrs, ok := h.find("credentials.store_migrated")
	require.True(t, ok, "a migration must emit an audit record; got:\n%s", h.all())
	assert.Equal(t, "credentials.store_migrated", attrs["event"])
	assert.Equal(t, "allow", attrs["decision"])
	assert.Equal(t, "2", attrs["entries"])
	assert.Equal(t, "1", attrs["from_version"])
	assert.Equal(t, "2", attrs["to_version"])

	// Refused migration.
	path2 := filepath.Join(t.TempDir(), "credentials.json")
	writeLegacyStore(t, path2, key, want)
	require.Error(t, NewStore(path2).UnlockWithKey(aadTestKey(0x73)))
	attrs, ok = h.find("credentials.store_migration_refused")
	require.True(t, ok, "a refused migration must emit an audit record; got:\n%s", h.all())
	assert.Equal(t, "deny", attrs["decision"])
	assert.Equal(t, "2", attrs["failed_entries"])
	assert.Equal(t, "2", attrs["entries"])

	logged := h.all()
	names := make([]string, 0, len(want))
	for name, v := range want {
		assert.NotContains(t, logged, v, "no secret value may be logged")
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		assert.False(t, strings.Contains(logged, name), "audit records carry counts, not entry names (%s)", name)
	}
}

// --- Review follow-ups (Opus security review of 5a1a48f45) -----------------

// legacyPassphraseStore writes a v1 store keyed from passphrase + a fresh salt
// and returns that salt.
func legacyPassphraseStore(t *testing.T, path, passphrase string, values map[string]string) []byte {
	t.Helper()
	salt := make([]byte, saltLen)
	_, err := io.ReadFull(rand.Reader, salt)
	require.NoError(t, err)
	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, keyLen)
	writeLegacyStoreWithSalt(t, path, key, salt, values)
	return salt
}

// TestMigrate_PassphraseMigrationUsesFreshSalt (review item 1b): in passphrase
// mode the migration re-keys under a FRESH salt, so the post-migration key no
// longer opens entries from a pre-migration copy. An attacker crafting a v1
// file must then either keep the current salt — and the old entries fail — or
// restore the old copy wholesale (see the residual-risk test below).
func TestMigrate_PassphraseMigrationUsesFreshSalt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	const pass = "fresh-salt-passphrase"
	oldSalt := legacyPassphraseStore(t, path, pass, map[string]string{"SECRET_A": "value-a", "SECRET_B": "value-b"})
	oldCopy := aadTestReadFile(t, path) // attacker keeps this

	store := NewStore(path)
	require.NoError(t, store.UnlockWithPassphrase(pass))
	for name, want := range map[string]string{"SECRET_A": "value-a", "SECRET_B": "value-b"} {
		got, err := store.Get(name)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	migrated := aadTestReadFile(t, path)
	assert.NotEqual(t, base64.StdEncoding.EncodeToString(oldSalt), migrated.Salt,
		"a passphrase-mode migration must re-key under a fresh salt")

	// The same passphrase still opens the migrated store.
	again := NewStore(path)
	require.NoError(t, again.UnlockWithPassphrase(pass))
	got, err := again.Get("SECRET_B")
	require.NoError(t, err)
	assert.Equal(t, "value-b", got)

	// Crafted v1 file: the old copy's (swapped) entries under the CURRENT salt,
	// migration record deleted. The pre-migration entries no longer open.
	require.NoError(t, os.Remove(path+".migrated"))
	crafted := &storeFile{Version: onDiskLegacyVersion, Salt: migrated.Salt, Credentials: map[string]encEntry{
		"SECRET_A": oldCopy.Credentials["SECRET_B"],
		"SECRET_B": oldCopy.Credentials["SECRET_A"],
	}}
	aadTestWriteFile(t, path, crafted)
	before := mustReadBytes(t, path)
	err = NewStore(path).UnlockWithPassphrase(pass)
	var migErr *MigrationError
	require.ErrorAs(t, err, &migErr, "pre-migration entries must not open under the post-migration key")
	assert.Equal(t, before, mustReadBytes(t, path))
}

// TestMigrate_WholeOldCopyRestoreIsResidualUntilSecretChanges pins the real
// window (review item 1a): as long as the secret (master key or passphrase)
// is unchanged, an attacker with write access who kept a pre-migration copy
// can swap entries in it, delete the migration record, and have the next
// unlock launder the swap. Changing the secret closes it; both are asserted.
func TestMigrate_WholeOldCopyRestoreIsResidualUntilSecretChanges(t *testing.T) {
	t.Run("passphrase", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials.json")
		const pass = "window-passphrase"
		legacyPassphraseStore(t, path, pass, map[string]string{"SECRET_A": "value-a", "SECRET_B": "value-b"})
		oldCopy := aadTestReadFile(t, path)
		oldCopy.Credentials["SECRET_A"], oldCopy.Credentials["SECRET_B"] = oldCopy.Credentials["SECRET_B"], oldCopy.Credentials["SECRET_A"]

		store := NewStore(path)
		require.NoError(t, store.UnlockWithPassphrase(pass))

		// Same passphrase: RESIDUAL RISK — the swapped old copy re-migrates.
		require.NoError(t, os.Remove(path+".migrated"))
		aadTestWriteFile(t, path, oldCopy)
		relaundered := NewStore(path)
		require.NoError(t, relaundered.UnlockWithPassphrase(pass),
			"RESIDUAL RISK: an old copy with its own salt re-migrates while the passphrase is unchanged")
		got, err := relaundered.Get("SECRET_A")
		require.NoError(t, err)
		assert.Equal(t, "value-b", got)

		// After a passphrase change the same old copy no longer opens.
		require.NoError(t, relaundered.RotateWithPassphrase("new-window-passphrase"))
		require.NoError(t, os.Remove(path+".migrated"))
		aadTestWriteFile(t, path, oldCopy)
		err = NewStore(path).UnlockWithPassphrase("new-window-passphrase")
		var migErr *MigrationError
		require.ErrorAs(t, err, &migErr, "after a passphrase change the old copy must be refused")
	})
	t.Run("key", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "credentials.json")
		key := aadTestKey(0x74)
		writeLegacyStore(t, path, key, map[string]string{"SECRET_A": "value-a", "SECRET_B": "value-b"})
		oldCopy := aadTestReadFile(t, path)
		oldCopy.Credentials["SECRET_A"], oldCopy.Credentials["SECRET_B"] = oldCopy.Credentials["SECRET_B"], oldCopy.Credentials["SECRET_A"]

		store := NewStore(path)
		require.NoError(t, store.UnlockWithKey(key))

		require.NoError(t, os.Remove(path+".migrated"))
		aadTestWriteFile(t, path, oldCopy)
		relaundered := NewStore(path)
		require.NoError(t, relaundered.UnlockWithKey(key),
			"RESIDUAL RISK: while the master key is unchanged an old copy re-migrates")

		newKey := aadTestKey(0x75)
		require.NoError(t, relaundered.Rotate(newKey))
		require.NoError(t, os.Remove(path+".migrated"))
		aadTestWriteFile(t, path, oldCopy)
		err := NewStore(path).UnlockWithKey(newKey)
		var migErr *MigrationError
		require.ErrorAs(t, err, &migErr, "after a master-key rotation the old copy must be refused")
	})
}

// TestMigrate_DeliveredKeyAuditCarriesNoEntryNames (review item 3): when the
// consume-once delivered key cannot open the store, the
// credentials.master_key_load audit record states the category and count, not
// the entry names that the returned error carries for the operator.
func TestMigrate_DeliveredKeyAuditCarriesNoEntryNames(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-store", true: "legacy-store"}[legacy], func(t *testing.T) {
			home := t.TempDir()
			storePath := filepath.Join(home, "credentials.json")
			rightKey := aadTestKey(0x76)
			names := map[string]string{"SENSITIVE_NAME_ALPHA": "v-alpha", "SENSITIVE_NAME_BETA": "v-beta"}
			if legacy {
				writeLegacyStore(t, storePath, rightKey, names)
			} else {
				seeded := NewStore(storePath)
				require.NoError(t, seeded.UnlockWithKey(rightKey))
				for n, v := range names {
					require.NoError(t, seeded.Set(n, v))
				}
			}

			deliveryDir := filepath.Join(home, "delivery")
			require.NoError(t, os.MkdirAll(deliveryDir, 0o700))
			delivered := filepath.Join(deliveryDir, "master-key")
			require.NoError(t, os.WriteFile(delivered,
				[]byte("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"), 0o600))
			t.Setenv(EnvMasterKey, "")
			t.Setenv(EnvKeyFile, "")
			t.Setenv(EnvMasterKeySource, delivered)

			h := &captureHandler{}
			prev := slog.Default()
			slog.SetDefault(slog.New(h))
			t.Cleanup(func() { slog.SetDefault(prev) })

			err := Unlock(NewStore(storePath))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "SENSITIVE_NAME_", "the operator-facing error still names the entry")

			attrs, ok := h.findWith("credentials.master_key_load", "decision", "deny")
			require.True(t, ok, "the refusal must be audited; got:\n%s", h.all())
			assert.Equal(t, "master_key_wrong_key", attrs["policy_rule"])
			assert.NotContains(t, attrs["detail"], "SENSITIVE_NAME_", "audit detail must not carry entry names")
			assert.NotContains(t, h.all(), "SENSITIVE_NAME_", "no log record may carry entry names")
			assert.NotContains(t, h.all(), "v-alpha")
		})
	}
}

// TestMigrate_SecondMigratorWaitsForTheLock (review item 2): the first
// migrator is parked inside its store write while holding the sidecar lock.
// The second may do its lock-free version probe, but must then neither read
// the file under the lock nor write until the first finishes — and the store
// must be written exactly once. Without the lock, the second migrator reads
// the still-legacy file, re-seals it and writes a second time.
func TestMigrate_SecondMigratorWaitsForTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := aadTestKey(0x77)
	want := map[string]string{"A": "value-a", "B": "value-b"}
	writeLegacyStore(t, path, key, want)

	var mu sync.Mutex
	reads, writes := 0, 0
	entered := make(chan struct{})
	release := make(chan struct{})

	origRead, origWrite := readStoreFileFn, writeFileAtomicFn
	readStoreFileFn = func(p string) ([]byte, error) {
		mu.Lock()
		reads++
		mu.Unlock()
		return origRead(p)
	}
	writeFileAtomicFn = func(p string, data []byte, perm os.FileMode) error {
		mu.Lock()
		writes++
		n := writes
		mu.Unlock()
		if n == 1 {
			close(entered)
			<-release
		}
		return origWrite(p, data, perm)
	}
	t.Cleanup(func() { readStoreFileFn, writeFileAtomicFn = origRead, origWrite })

	first, second := NewStore(path), NewStore(path)
	errs := make(chan error, 2)
	go func() { errs <- first.UnlockWithKey(key) }()
	<-entered // first holds the lock, parked in its write; it has read twice

	secondDone := make(chan struct{})
	go func() {
		errs <- second.UnlockWithKey(key)
		close(secondDone)
	}()

	// Give the second migrator ample time to misbehave if the lock is absent.
	select {
	case <-secondDone:
		t.Fatal("the second migrator finished while the first still held the lock")
	case <-time.After(300 * time.Millisecond):
	}
	mu.Lock()
	assert.Equal(t, 3, reads, "while the first migrator holds the lock, the second may only do its lock-free probe")
	assert.Equal(t, 1, writes, "the second migrator must not write while the first holds the lock")
	mu.Unlock()

	close(release)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	mu.Lock()
	assert.Equal(t, 1, writes, "the store must be written exactly once")
	mu.Unlock()
	for _, s := range []*Store{first, second} {
		for name, v := range want {
			got, err := s.Get(name)
			require.NoError(t, err)
			assert.Equal(t, v, got)
		}
	}
}

// TestMigrate_PassphraseLoserOfConcurrentMigrationRederives: two passphrase
// unlocks race on one legacy file. The winner re-keys under ITS fresh salt;
// the loser waited on the lock with a key derived from the OLD salt and must
// re-derive from the salt now on disk, or it would install a key that opens
// nothing.
func TestMigrate_PassphraseLoserOfConcurrentMigrationRederives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	const pass = "race-passphrase"
	legacyPassphraseStore(t, path, pass, map[string]string{"A": "value-a"})

	var once sync.Once
	entered := make(chan struct{})
	release := make(chan struct{})
	origWrite := writeFileAtomicFn
	writeFileAtomicFn = func(p string, data []byte, perm os.FileMode) error {
		parked := false
		once.Do(func() { parked = true })
		if parked {
			close(entered)
			<-release
		}
		return origWrite(p, data, perm)
	}
	t.Cleanup(func() { writeFileAtomicFn = origWrite })

	winner, loser := NewStore(path), NewStore(path)
	errs := make(chan error, 2)
	go func() { errs <- winner.UnlockWithPassphrase(pass) }()
	<-entered
	go func() { errs <- loser.UnlockWithPassphrase(pass) }()
	// Let the loser finish its two Argon2id derivations and block on the lock.
	time.Sleep(1500 * time.Millisecond)
	close(release)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)

	for _, s := range []*Store{winner, loser} {
		got, err := s.Get("A")
		require.NoError(t, err, "both unlocks must hold the key for the salt now on disk")
		assert.Equal(t, "value-a", got)
	}
}
