// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/argon2"
)

// TestArgon2idKeyDerivation verifies Argon2id produces a deterministic 32-byte key
// with the correct parameters (time=3, mem=64MB, parallelism=4).
// Traces to: wave1-core-foundation-spec.md Scenario: Store and retrieve a credential (FR-006)
func TestArgon2idKeyDerivation(t *testing.T) {
	// Verify Argon2id parameters are correct by testing against a known reference.
	passphrase := "correcthorsebatterystaple"
	salt := make([]byte, 32)
	for i := range salt {
		salt[i] = byte(i) // deterministic salt for test
	}

	// Call the exported function.
	key := DeriveKeyFromPassphrase(passphrase, salt)

	// Verify key length is 32 bytes (256 bits).
	assert.Equal(t, 32, len(key), "derived key must be 32 bytes")

	// Verify determinism: same inputs → same key.
	key2 := DeriveKeyFromPassphrase(passphrase, salt)
	assert.Equal(t, key, key2, "Argon2id must be deterministic for same passphrase+salt")

	// Verify parameters: check against a manually computed reference
	// using the exact parameters specified in the BRD (time=3, mem=64MB, par=4).
	reference := argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 4, 32)
	assert.Equal(t, reference, key, "Argon2id parameters must be time=3, mem=64MB, parallelism=4")

	// Different passphrases must produce different keys.
	keyDiff := DeriveKeyFromPassphrase("different", salt)
	assert.NotEqual(t, key, keyDiff, "different passphrases must produce different keys")

	// Different salts must produce different keys.
	salt2 := make([]byte, 32)
	for i := range salt2 {
		salt2[i] = 0xFF
	}
	keySalt := DeriveKeyFromPassphrase(passphrase, salt2)
	assert.NotEqual(t, key, keySalt, "different salts must produce different keys")
}

// TestAES256GCMEncryptDecrypt verifies the encrypt→decrypt round trip for known plaintext.
// Traces to: wave1-core-foundation-spec.md Scenario: Store and retrieve a credential (FR-005)
func TestAES256GCMEncryptDecrypt(t *testing.T) {
	key := make([]byte, keyLen)
	for i := range key {
		key[i] = byte(i + 1)
	}

	tests := []struct {
		name      string
		plaintext string
	}{
		{"normal API key", "sk-ant-api03-very-long-key-abcdefghijklmnopqrstuvwxyz0123456789"},
		{"empty string", ""},
		{"short value", "abc"},
		{"unicode content", "パスワード🔑"},
		{"binary-like content", "\x00\xff\x00\xff"},
		{"200 char key", "sk-ant-api03-" + string(make([]byte, 187))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Traces to: wave1-core-foundation-spec.md Dataset: Credential Encryption Inputs rows 8,9,10
			entry, err := encrypt(key, []byte(tc.plaintext))
			require.NoError(t, err, "encrypt must not fail")
			assert.NotEmpty(t, entry.Nonce, "nonce must be populated")
			assert.NotEmpty(t, entry.Ciphertext, "ciphertext must be populated")

			plaintext, err := decrypt(key, entry)
			require.NoError(t, err, "decrypt must not fail")
			assert.Equal(t, tc.plaintext, plaintext, "decrypted value must match original")
		})
	}
}

// TestAES256GCMDecryptWrongKey verifies decryption with wrong key returns auth error.
// Traces to: wave1-core-foundation-spec.md Scenario: Wrong master key fails decryption (US-3 AC3)
func TestAES256GCMDecryptWrongKey(t *testing.T) {
	rightKey := make([]byte, keyLen)
	for i := range rightKey {
		rightKey[i] = 0xAA
	}
	wrongKey := make([]byte, keyLen)
	for i := range wrongKey {
		wrongKey[i] = 0xBB
	}

	entry, err := encrypt(rightKey, []byte("secret-value"))
	require.NoError(t, err)

	_, err = decrypt(wrongKey, entry)
	assert.ErrorIs(t, err, ErrWrongKey, "decryption with wrong key must return ErrWrongKey")
}

// TestCredentialStoreFileFormat verifies the JSON structure matches the spec.
// Traces to: wave1-core-foundation-spec.md Scenario: Store and retrieve a credential (US-3 AC1, FR-008)
func TestCredentialStoreFileFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store := NewStore(path)

	key := make([]byte, keyLen)
	for i := range key {
		key[i] = byte(i + 1)
	}
	require.NoError(t, store.UnlockWithKey(key))
	require.NoError(t, store.Set("ANTHROPIC_API_KEY", "sk-ant-test"))

	// Read and verify JSON structure.
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw), "credentials.json must be valid JSON")

	// Check top-level keys.
	assert.Contains(t, raw, "version", "must have 'version' key")
	assert.Contains(t, raw, "salt", "must have 'salt' key")
	assert.Contains(t, raw, "credentials", "must have 'credentials' key")

	// Check version is 1.
	var version int
	require.NoError(t, json.Unmarshal(raw["version"], &version))
	assert.Equal(t, 1, version, "version must be 1")

	// Check credentials has the correct nested structure.
	var creds map[string]map[string]string
	require.NoError(t, json.Unmarshal(raw["credentials"], &creds))
	assert.Contains(t, creds, "ANTHROPIC_API_KEY")
	assert.Contains(t, creds["ANTHROPIC_API_KEY"], "nonce", "credential entry must have 'nonce'")
	assert.Contains(t, creds["ANTHROPIC_API_KEY"], "ciphertext", "credential entry must have 'ciphertext'")
}

// TestMasterKeyFromHex verifies hexToKey decodes a valid 64-char hex string.
// Traces to: wave1-core-foundation-spec.md Scenario: Master key from environment variable (US-4 AC1)
func TestMasterKeyFromHex(t *testing.T) {
	// 64 hex chars = 32 bytes = 256 bits.
	hexStr := strings.Repeat("aa", 32)

	key, err := hexToKey(hexStr)
	require.NoError(t, err, "valid 64-char hex must decode without error")
	assert.Equal(t, 32, len(key), "decoded key must be 32 bytes")

	expected := make([]byte, 32)
	for i := range expected {
		expected[i] = 0xAA
	}
	assert.Equal(t, expected, key)
}

// TestMasterKeyFromHexInvalid verifies hexToKey rejects invalid inputs.
// Traces to: wave1-core-foundation-spec.md Scenario: Invalid master key values (US-4, Dataset rows 6,7)
func TestMasterKeyFromHexInvalid(t *testing.T) {
	tests := []struct {
		name   string
		hexKey string
	}{
		{"too short (62 chars)", strings.Repeat("aa", 31)},
		{"non-hex chars", strings.Repeat("gg", 32)},
		{"empty string", ""},
		{"odd length", "abc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Traces to: wave1-core-foundation-spec.md Dataset: Credential Encryption Inputs rows 6,7
			_, err := hexToKey(tc.hexKey)
			assert.Error(t, err, "invalid hex key must return error")
		})
	}
}

// TestKeyFilePermissionCheck verifies loadKeyFile rejects world-readable files.
// Traces to: wave1-core-foundation-spec.md Scenario: Key file with bad permissions is rejected (US-4 AC3)
func TestKeyFilePermissionCheck(t *testing.T) {
	validHex := strings.Repeat("aa", 32)

	t.Run("accepts 0600 file", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "keyfile")
		require.NoError(t, err)
		_, err = f.WriteString(validHex)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		require.NoError(t, os.Chmod(f.Name(), 0o600))

		key, err := loadKeyFile(f.Name())
		require.NoError(t, err, "0600 file must be accepted")
		assert.Equal(t, 32, len(key))
	})

	t.Run("rejects 0644 file", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "keyfile")
		require.NoError(t, err)
		_, err = f.WriteString(validHex)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		require.NoError(t, os.Chmod(f.Name(), 0o644))

		_, err = loadKeyFile(f.Name())
		assert.Error(t, err, "0644 file must be rejected (world-readable)")
	})

	t.Run("rejects 0666 file", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "keyfile")
		require.NoError(t, err)
		_, err = f.WriteString(validHex)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		require.NoError(t, os.Chmod(f.Name(), 0o666))

		_, err = loadKeyFile(f.Name())
		assert.Error(t, err, "0666 file must be rejected")
	})
}

// TestKeyProvisioningOrder verifies env var → key file → passphrase priority.
// Traces to: wave1-core-foundation-spec.md Scenario: Master key from environment variable (US-4 AC1, AC2)
func TestKeyProvisioningOrder(t *testing.T) {
	validHex := strings.Repeat("cc", 32)

	t.Run("OMNIPUS_MASTER_KEY takes priority", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials.json")
		store := NewStore(path)

		t.Setenv(EnvMasterKey, validHex)
		t.Setenv(EnvKeyFile, "") // ensure key file is not set

		err := Unlock(store)
		require.NoError(t, err)
		assert.False(t, store.IsLocked(), "store must be unlocked via OMNIPUS_MASTER_KEY")
	})

	t.Run("OMNIPUS_KEY_FILE used when no master key env", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials.json")
		store := NewStore(path)

		// Create a 0600 key file.
		keyFilePath := filepath.Join(dir, "keyfile")
		require.NoError(t, os.WriteFile(keyFilePath, []byte(validHex), 0o600))

		t.Setenv(EnvMasterKey, "")
		t.Setenv(EnvKeyFile, keyFilePath)

		err := Unlock(store)
		require.NoError(t, err)
		assert.False(t, store.IsLocked(), "store must be unlocked via OMNIPUS_KEY_FILE")
	})

	t.Run("bad key file returns error (explicit OMNIPUS_KEY_FILE failure is fatal)", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "credentials.json")
		store := NewStore(path)

		// Create a 0644 key file (bad permissions — will fail loadKeyFile).
		keyFilePath := filepath.Join(dir, "keyfile")
		require.NoError(t, os.WriteFile(keyFilePath, []byte(validHex), 0o644))

		t.Setenv(EnvMasterKey, "")
		t.Setenv(EnvKeyFile, keyFilePath)

		// Explicit OMNIPUS_KEY_FILE failure must error — not fall through silently.
		// Headless deployments cannot recover from a bad key file; falling through
		// would hang waiting for a TTY that doesn't exist.
		err := Unlock(store)
		require.Error(t, err, "Unlock must error when explicit OMNIPUS_KEY_FILE fails")
		assert.Contains(t, err.Error(), "OMNIPUS_KEY_FILE")
		assert.True(t, store.IsLocked(), "store must remain locked when key file is bad")
	})
}

// TestUnlock_AutoGeneratesOnFreshInstall verifies that Unlock mints a fresh
// master key and writes it to $OMNIPUS_HOME/master.key when:
//   - no OMNIPUS_MASTER_KEY / OMNIPUS_KEY_FILE env is set
//   - no default master.key file exists
//   - no credentials.json exists (truly fresh install)
//
// This closes the headless first-run chicken-and-egg gap: a new user on a
// cloud VPS can start the gateway with zero configuration and still end up
// with a functioning encrypted credential store.
//
// Follow-up boots must also pick up the auto-generated key via the same
// default-key-file probe (Unlock mode 3), without regenerating.
func TestUnlock_AutoGeneratesOnFreshInstall(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	keyPath := filepath.Join(dir, DefaultKeyFileName)

	t.Setenv(EnvMasterKey, "")
	t.Setenv(EnvKeyFile, "")

	store := NewStore(credPath)
	require.NoError(t, Unlock(store), "Unlock on fresh install must auto-generate a master key")
	assert.False(t, store.IsLocked(), "store must be unlocked after auto-generation")

	// Key file must exist on disk with 0600 permissions.
	info, err := os.Stat(keyPath)
	require.NoError(t, err, "master.key must be written to $OMNIPUS_HOME")
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"auto-generated master.key must have mode 0600")

	// File contents must be 64 hex characters (32 bytes encoded).
	keyBytes, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	assert.Len(t, keyBytes, 64, "master.key must contain 64 hex characters")

	// The store must be usable: we can Set + Get a credential immediately.
	require.NoError(t, store.Set("TEST_KEY", "test-value"))
	v, err := store.Get("TEST_KEY")
	require.NoError(t, err)
	assert.Equal(t, "test-value", v, "auto-generated key must round-trip a credential")

	// A second Unlock call on the same store (e.g. on the next gateway boot)
	// must pick up the existing master.key via mode 3, NOT regenerate.
	store2 := NewStore(credPath)
	require.NoError(t, Unlock(store2), "second Unlock must load existing master.key")
	assert.False(t, store2.IsLocked())
	v2, err := store2.Get("TEST_KEY")
	require.NoError(t, err, "second unlock must decrypt entries written under the same key")
	assert.Equal(t, "test-value", v2)

	// Sanity: the key file content must not have changed.
	keyBytes2, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	assert.Equal(t, keyBytes, keyBytes2, "second boot must not regenerate master.key")
}

// TestUnlock_SkipsAutoGenerateWhenStoreExists verifies that auto-generate
// does NOT fire when credentials.json already exists but no key is available.
// This is the "operator lost their key" scenario: regenerating would strand
// the encrypted data. Unlock must return an error so the operator notices.
func TestUnlock_SkipsAutoGenerateWhenStoreExists(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")

	// Seed a fake credentials.json so Exists() returns true.
	require.NoError(t, os.WriteFile(credPath,
		[]byte(`{"version":1,"salt":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","credentials":{}}`),
		0o600))

	t.Setenv(EnvMasterKey, "")
	t.Setenv(EnvKeyFile, "")

	store := NewStore(credPath)
	err := Unlock(store)
	require.Error(t, err, "Unlock must not auto-generate when credentials.json already exists")
	assert.Contains(t, err.Error(), "no master key")
	assert.True(t, store.IsLocked())

	// master.key must NOT have been created — we refuse to strand existing data.
	_, statErr := os.Stat(filepath.Join(dir, DefaultKeyFileName))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "master.key must not exist after refused auto-generate")
}

// TestCredentialStoreIntegration verifies full set-encrypt-persist-load-decrypt cycle.
// Traces to: wave1-core-foundation-spec.md Scenario: Store and retrieve a credential (US-3 AC1, AC2)
func TestCredentialStoreIntegration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	key := make([]byte, keyLen)
	for i := range key {
		key[i] = byte(i + 7)
	}

	// Store credentials.
	store1 := NewStore(path)
	require.NoError(t, store1.UnlockWithKey(key))
	require.NoError(t, store1.Set("ANTHROPIC_API_KEY", "sk-ant-test-value"))
	require.NoError(t, store1.Set("OPENAI_API_KEY", "sk-openai-test"))

	// File must exist on disk.
	_, err := os.Stat(path)
	require.NoError(t, err, "credentials.json must exist after Set")

	// Load from a fresh store (simulates restart).
	store2 := NewStore(path)
	require.NoError(t, store2.UnlockWithKey(key))

	val, err := store2.Get("ANTHROPIC_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-test-value", val, "decrypted value must match original")

	val2, err := store2.Get("OPENAI_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "sk-openai-test", val2)

	// List must return sorted names.
	names, err := store2.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}, names)
}

// TestCredentialMissingRef verifies missing credential returns NotFoundError.
// Traces to: wave1-core-foundation-spec.md Scenario: Missing credential ref fails provider init (US-11 AC2)
func TestCredentialMissingRef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store := NewStore(path)

	key := make([]byte, keyLen)
	require.NoError(t, store.UnlockWithKey(key))

	_, err := store.Get("NONEXISTENT_KEY")
	require.Error(t, err)

	var notFound *NotFoundError
	assert.ErrorAs(t, err, &notFound, "missing credential must return NotFoundError")
	assert.Equal(t, "NONEXISTENT_KEY", notFound.Name)
}

// TestStoreLocked verifies locked store returns ErrStoreLocked.
// Traces to: wave1-core-foundation-spec.md Scenario: Credential store locked (US-11 AC3)
func TestStoreLocked(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "credentials.json"))
	// Store is locked by default.
	assert.True(t, store.IsLocked())

	_, err := store.Get("anything")
	assert.ErrorIs(t, err, ErrStoreLocked)

	err = store.Set("anything", "value")
	assert.ErrorIs(t, err, ErrStoreLocked)
}

// TestArgon2idEmptyPassphraseRejected verifies empty passphrase is rejected.
// Traces to: wave1-core-foundation-spec.md FR-026
func TestArgon2idEmptyPassphraseRejected(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "credentials.json"))

	err := store.UnlockWithPassphrase("")
	assert.Error(t, err, "empty passphrase must be rejected")
}

// TestHexKeyWrongLength verifies the exported error path for UnlockWithKey.
// Traces to: wave1-core-foundation-spec.md Dataset: Credential Encryption Inputs row 6
func TestHexKeyWrongLength(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "credentials.json"))

	shortKey := []byte("tooshort")
	err := store.UnlockWithKey(shortKey)
	assert.Error(t, err, "key shorter than 32 bytes must be rejected")
}
