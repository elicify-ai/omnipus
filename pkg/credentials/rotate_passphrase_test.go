// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for Store.RotateWithPassphrase / rotateFull — a destructive,
// irreversible operation (SEC-23c): a failed or half-done rotation can
// permanently strand every stored credential behind an unrecoverable key.
//
// Before this file, RotateWithPassphrase, rotateFull, and the sibling Rotate
// had zero test coverage anywhere in the repo (repo-wide grep for
// "RotateWithPassphrase", ".Rotate(", and "rotateFull" in *_test.go returned
// no hits).
//
// Traces to: docs/internal/BRD/Omnipus BRD.md:142 (SEC-23c — Credential key
// rotation) + pkg/credentials/store.go RotateWithPassphrase (lines 304-315)
// and rotateFull (lines 320-355).

package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRotateWithPassphrase_Success verifies the full happy-path contract:
// rotating to a new passphrase re-encrypts every credential, the OLD
// passphrase can no longer decrypt anything, the NEW passphrase can, and
// every credential's plaintext is byte-identical to before rotation.
//
// BDD: Given a store unlocked with an initial passphrase holding several
// credentials with varied plaintext (differentiation across values), When
// RotateWithPassphrase is called with a new passphrase, Then the on-disk file
// changes, the same store instance still decrypts everything correctly, a
// fresh store unlocked with the OLD passphrase fails to decrypt (ErrWrongKey),
// and a fresh store unlocked with the NEW passphrase decrypts everything to
// the original plaintext.
//
// Traces to: pkg/credentials/store.go RotateWithPassphrase (lines 304-315),
// rotateFull (lines 320-355)
func TestRotateWithPassphrase_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	// Given — a store unlocked with an initial passphrase, holding several
	// credentials with varied plaintext content.
	store := NewStore(path)
	require.NoError(t, store.UnlockWithPassphrase("old-correct-horse-battery"))

	creds := map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-original-value-123",
		"OPENAI_API_KEY":    "sk-openai-original-456",
		"EMPTY_VALUE":       "",
		"UNICODE_VALUE":     "パスワード🔑rotate-me",
	}
	for name, val := range creds {
		require.NoError(t, store.Set(name, val), "seeding credential %q must succeed", name)
	}

	fileBefore, err := os.ReadFile(path)
	require.NoError(t, err)

	// When — rotate to a new passphrase.
	err = store.RotateWithPassphrase("new-correct-horse-battery-staple")
	require.NoError(t, err, "RotateWithPassphrase must succeed against an unlocked store")

	// Then — the on-disk file must actually have changed (new salt, new
	// ciphertexts/nonces) — proves this isn't a no-op that returns nil without
	// doing real work.
	fileAfter, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotEqual(t, string(fileBefore), string(fileAfter),
		"credentials.json must be rewritten by rotation (new salt + re-encrypted ciphertexts)")

	// Then — every credential is still readable, byte-identical, through the
	// SAME store instance without re-unlocking (rotateFull updates s.key in
	// place per the doc comment).
	for name, want := range creds {
		got, getErr := store.Get(name)
		require.NoError(t, getErr, "Get(%q) after rotation must succeed on the same store instance", name)
		assert.Equal(t, want, got, "credential %q must be byte-identical after rotation (same instance)", name)
	}

	// Then — a FRESH Store instance pointed at the same file, unlocked with
	// the OLD passphrase, must NOT be able to read the data back correctly.
	// UnlockWithPassphrase always "succeeds" in the sense that it sets *a* key
	// (it has no way to validate a passphrase without attempting a decrypt) —
	// the real proof the old passphrase no longer works is that the derived
	// key fails AES-GCM authentication against the re-encrypted ciphertext.
	oldStore := NewStore(path)
	require.NoError(t, oldStore.UnlockWithPassphrase("old-correct-horse-battery"))
	_, err = oldStore.Get("ANTHROPIC_API_KEY")
	require.Error(t, err, "the OLD passphrase must no longer decrypt credentials after rotation")
	assert.ErrorIs(t, err, ErrWrongKey,
		"the OLD passphrase must fail with ErrWrongKey (ciphertext was re-encrypted under a new key/salt)")

	// Then — a FRESH Store instance unlocked with the NEW passphrase reads
	// everything back correctly, matching the original plaintext exactly.
	newStore := NewStore(path)
	require.NoError(t, newStore.UnlockWithPassphrase("new-correct-horse-battery-staple"))
	for name, want := range creds {
		got, err := newStore.Get(name)
		require.NoError(t, err, "NEW passphrase must decrypt %q after rotation", name)
		assert.Equal(t, want, got, "credential %q read via NEW passphrase must match original plaintext", name)
	}
}

// TestRotateWithPassphrase_LockedStore_ReturnsErrStoreLockedAndFileUnchanged
// verifies that rotating a locked store (s.key == nil) is rejected with
// ErrStoreLocked and makes NO changes to the on-disk file whatsoever.
//
// BDD: Given a credentials.json file already populated by a separate,
// unlocked store instance, and a FRESH Store instance pointed at the same
// file that has never been unlocked, When RotateWithPassphrase is called,
// Then ErrStoreLocked is returned and the file's bytes and mtime are
// unchanged.
//
// Traces to: pkg/credentials/store.go rotateFull — `if s.key == nil { return
// ErrStoreLocked }` guard (line 323-325), which fires before loadFileInternal
// or saveFileNoLock are ever reached.
func TestRotateWithPassphrase_LockedStore_ReturnsErrStoreLockedAndFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	// Given — existing credentials on disk, written by a separate unlocked
	// store instance.
	seed := NewStore(path)
	require.NoError(t, seed.UnlockWithPassphrase("seed-passphrase"))
	require.NoError(t, seed.Set("SOME_KEY", "some-value"))

	fileBefore, err := os.ReadFile(path)
	require.NoError(t, err)
	statBefore, err := os.Stat(path)
	require.NoError(t, err)

	// A fresh Store instance pointed at the same file, never unlocked.
	locked := NewStore(path)
	require.True(t, locked.IsLocked(), "a freshly constructed Store must start locked")

	// When
	err = locked.RotateWithPassphrase("does-not-matter")

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStoreLocked, "RotateWithPassphrase on a locked store must return ErrStoreLocked")

	fileAfter, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(fileBefore), string(fileAfter),
		"credentials.json must be byte-for-byte unchanged when rotation is rejected on a locked store")

	statAfter, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, statBefore.ModTime(), statAfter.ModTime(),
		"credentials.json mtime must be unchanged — rotateFull must return before ever touching the file")

	// The seed data must still be readable via the original, correctly
	// unlocked instance — proving nothing was corrupted by the rejected call.
	val, err := seed.Get("SOME_KEY")
	require.NoError(t, err)
	assert.Equal(t, "some-value", val)
}

// TestRotateWithPassphrase_EmptyPassphrase_RejectedBeforeFileIO verifies that
// an empty new passphrase is rejected immediately, before any salt generation
// or file I/O — proven by the file being byte-for-byte unchanged.
//
// BDD: Given an unlocked store with existing credentials, When
// RotateWithPassphrase("") is called, Then an error mentioning "passphrase
// must not be empty" is returned and credentials.json is untouched.
//
// Traces to: pkg/credentials/store.go RotateWithPassphrase — `if
// newPassphrase == "" { return fmt.Errorf(...) }` guard (line 305-307), which
// fires before generating newSalt or calling rotateFull.
func TestRotateWithPassphrase_EmptyPassphrase_RejectedBeforeFileIO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	store := NewStore(path)
	require.NoError(t, store.UnlockWithPassphrase("some-passphrase"))
	require.NoError(t, store.Set("KEEP_ME", "keep-me-value"))

	fileBefore, err := os.ReadFile(path)
	require.NoError(t, err)

	err = store.RotateWithPassphrase("")

	require.Error(t, err, "empty passphrase must be rejected")
	assert.Contains(t, err.Error(), "passphrase must not be empty")

	fileAfter, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(fileBefore), string(fileAfter),
		"credentials.json must be untouched — empty passphrase must be rejected before any salt "+
			"generation or file I/O (RotateWithPassphrase returns before ever calling rotateFull)")

	// The store must still be usable under the original key — rotation never
	// started.
	val, err := store.Get("KEEP_ME")
	require.NoError(t, err)
	assert.Equal(t, "keep-me-value", val)
}

// TestRotateWithPassphrase_NoTempFileSurvives is lightweight evidence for the
// atomic-write contract: rotateFull's save path goes through
// fileutil.WriteFileAtomic (via saveFileNoLock), which creates a ".tmp-*"
// sibling file in the same directory and renames it over the target. After a
// successful rotation, no leftover temp file should remain.
//
// A true crash-mid-write simulation (kill -9 partway through the rename)
// would require an actual subprocess and is not attempted here — it would be
// slow and inherently flaky in this environment. This test instead checks the
// cheap, deterministic invariant: on the successful path, cleanup leaves no
// ".tmp-*" artifact and the target file exists.
//
// Traces to: pkg/credentials/store.go saveFileNoLock (line 393-405) +
// pkg/fileutil/file.go WriteFileAtomic (temp-file + atomic rename pattern)
func TestRotateWithPassphrase_NoTempFileSurvives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	store := NewStore(path)
	require.NoError(t, store.UnlockWithPassphrase("initial-pass"))
	require.NoError(t, store.Set("A", "a-value"))
	require.NoError(t, store.Set("B", "b-value"))

	require.NoError(t, store.RotateWithPassphrase("rotated-pass"))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), ".tmp-"),
			"no leftover atomic-write temp file must survive a successful rotation, found %q", e.Name())
	}

	_, err = os.Stat(path)
	require.NoError(t, err, "credentials.json must exist after a successful rotation")
}
