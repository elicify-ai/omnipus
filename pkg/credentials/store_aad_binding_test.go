// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the AAD name binding on stored credential entries (issue #85b):
// every ciphertext is sealed and opened with its entry name as AES-GCM
// additional authenticated data, so a ciphertext carrying one entry's value
// cannot be moved under another entry's name and still decrypt.
//
// Traces to: pkg/credentials/store.go aadFor, encrypt, decrypt.

package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aadTestKey returns a deterministic 32-byte key, distinct per seed.
func aadTestKey(seed byte) []byte {
	key := make([]byte, keyLen)
	for i := range key {
		key[i] = seed ^ byte(i+1)
	}
	return key
}

// aadTestStore returns an unlocked store backed by a temp credentials.json.
func aadTestStore(t *testing.T, seed byte) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := NewStore(path)
	require.NoError(t, s.UnlockWithKey(aadTestKey(seed)))
	return s, path
}

// aadTestReadFile parses the raw store file.
func aadTestReadFile(t *testing.T, path string) *storeFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var sf storeFile
	require.NoError(t, json.Unmarshal(raw, &sf))
	return &sf
}

// aadTestWriteFile persists a mutated store file, bypassing the Store API —
// this is what an attacker with write access to credentials.json does.
func aadTestWriteFile(t *testing.T, path string, sf *storeFile) {
	t.Helper()
	raw, err := json.MarshalIndent(sf, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}

// TestAADBinding_CiphertextMovedBetweenNamesFailsToOpen is the issue's exact
// scenario: entry A's {nonce, ciphertext} is copied verbatim onto entry B's
// name and Get("B") must refuse to return A's value.
func TestAADBinding_CiphertextMovedBetweenNamesFailsToOpen(t *testing.T) {
	store, path := aadTestStore(t, 0x11)
	require.NoError(t, store.Set("SECRET_A", "value-for-a"))
	require.NoError(t, store.Set("SECRET_B", "value-for-b"))

	sf := aadTestReadFile(t, path)
	sf.Credentials["SECRET_B"] = sf.Credentials["SECRET_A"]
	aadTestWriteFile(t, path, sf)

	got, err := store.Get("SECRET_B")
	require.Error(t, err, "a ciphertext moved under another name must not decrypt")
	assert.Empty(t, got, "no plaintext may leak from a swapped entry")

	var authErr *EntryAuthError
	require.ErrorAs(t, err, &authErr, "the failure must be the named entry-auth error")
	assert.Equal(t, "SECRET_B", authErr.Name, "the error must name the entry that was swapped into")
	assert.Contains(t, err.Error(), `"SECRET_B"`, "the message must name the entry")

	// The untouched neighbour still opens — the swap is entry-scoped, not a
	// store-wide key failure, which is what makes this diagnosis possible.
	kept, err := store.Get("SECRET_A")
	require.NoError(t, err, "entry A was not touched and must still open")
	assert.Equal(t, "value-for-a", kept)
}

// TestAADBinding_TamperedEntryFailsToOpen covers in-place edits of the stored
// envelope: a flipped ciphertext bit, a flipped nonce bit, and a truncated
// ciphertext. Every one must fail authentication rather than decrypt to
// something else.
func TestAADBinding_TamperedEntryFailsToOpen(t *testing.T) {
	tamper := map[string]func(*encEntry){
		"flipped ciphertext bit": func(e *encEntry) {
			raw, err := base64.StdEncoding.DecodeString(e.Ciphertext)
			if err != nil || len(raw) == 0 {
				t.Fatalf("ciphertext must decode: %v", err)
			}
			raw[0] ^= 0x01
			e.Ciphertext = base64.StdEncoding.EncodeToString(raw)
		},
		"flipped nonce bit": func(e *encEntry) {
			raw, err := base64.StdEncoding.DecodeString(e.Nonce)
			if err != nil || len(raw) == 0 {
				t.Fatalf("nonce must decode: %v", err)
			}
			raw[0] ^= 0x80
			e.Nonce = base64.StdEncoding.EncodeToString(raw)
		},
		"truncated ciphertext": func(e *encEntry) {
			raw, err := base64.StdEncoding.DecodeString(e.Ciphertext)
			if err != nil || len(raw) < 2 {
				t.Fatalf("ciphertext must decode to at least 2 bytes: %v", err)
			}
			e.Ciphertext = base64.StdEncoding.EncodeToString(raw[:len(raw)-1])
		},
		"ciphertext not base64": func(e *encEntry) {
			e.Ciphertext = "!!!not-base64!!!"
		},
	}

	for name, mutate := range tamper {
		t.Run(name, func(t *testing.T) {
			store, path := aadTestStore(t, 0x22)
			require.NoError(t, store.Set("TAMPER_ME", "genuine-value"))

			sf := aadTestReadFile(t, path)
			entry := sf.Credentials["TAMPER_ME"]
			mutate(&entry)
			sf.Credentials["TAMPER_ME"] = entry
			aadTestWriteFile(t, path, sf)

			got, err := store.Get("TAMPER_ME")
			require.Error(t, err, "a tampered entry must not decrypt")
			assert.Empty(t, got, "no plaintext may leak from a tampered entry")
			assert.Contains(t, err.Error(), "TAMPER_ME",
				"the error must name the entry that failed, got: %v", err)
		})
	}
}

// TestAADBinding_RoundTripStillWorks is the control: name binding must not
// break ordinary storage, including an empty value and non-ASCII content, and
// two entries holding the SAME value must each still open under their own name.
func TestAADBinding_RoundTripStillWorks(t *testing.T) {
	store, _ := aadTestStore(t, 0x33)

	cases := map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
		"OPENROUTER_KEY":    "sk-or-v1-0123456789",
		"EMPTY_VALUE":       "",
		"UNICODE_VALUE":     "パスワード🔑",
		"SHARED_VALUE_ONE":  "identical-plaintext",
		"SHARED_VALUE_TWO":  "identical-plaintext",
	}
	for name, value := range cases {
		require.NoError(t, store.Set(name, value), "Set(%q) must succeed", name)
	}
	for name, want := range cases {
		got, err := store.Get(name)
		require.NoError(t, err, "Get(%q) must succeed for its own name", name)
		assert.Equal(t, want, got, "Get(%q) must return the value stored under that name", name)
	}
}

// TestAADBinding_LegacyNilAADCiphertextIsRejected pins the greenfield break:
// an entry sealed the old way (nil AAD) under the SAME master key does not
// open, proving no "try the name, else fall back to no AAD" read path exists.
func TestAADBinding_LegacyNilAADCiphertextIsRejected(t *testing.T) {
	store, path := aadTestStore(t, 0x44)
	require.NoError(t, store.Set("KEEP_ME", "current-format"))

	// Seal a value exactly as the pre-binding code did: same key, nil AAD.
	block, err := aes.NewCipher(aadTestKey(0x44))
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, nonceLen)
	_, err = io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	legacy := gcm.Seal(nil, nonce, []byte("legacy-value"), nil)

	sf := aadTestReadFile(t, path)
	sf.Credentials["LEGACY_ENTRY"] = encEntry{
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(legacy),
	}
	aadTestWriteFile(t, path, sf)

	got, err := store.Get("LEGACY_ENTRY")
	require.Error(t, err, "a nil-AAD entry must not open against a name-bound store")
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), "LEGACY_ENTRY", "the error must name the entry")

	// The name-bound entry beside it is unaffected, so the break is per-entry
	// and diagnosable, not a silently dead store.
	current, err := store.Get("KEEP_ME")
	require.NoError(t, err)
	assert.Equal(t, "current-format", current)
}

// TestAADBinding_RotationResealsEveryEntryUnderItsOwnName verifies rotation
// keeps the binding: after RotateWithPassphrase every entry opens under its
// own name, and a swap performed AFTER rotation is still rejected — which
// only holds if rotation re-seals each entry with that entry's name as AAD.
func TestAADBinding_RotationResealsEveryEntryUnderItsOwnName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store := NewStore(path)
	require.NoError(t, store.UnlockWithPassphrase("old-passphrase-for-aad"))

	creds := map[string]string{
		"ENTRY_ONE":   "value-one",
		"ENTRY_TWO":   "value-two",
		"ENTRY_THREE": "",
		"ENTRY_FOUR":  "パスワード🔑",
	}
	for name, value := range creds {
		require.NoError(t, store.Set(name, value))
	}

	require.NoError(t, store.RotateWithPassphrase("new-passphrase-for-aad"))

	for name, want := range creds {
		got, err := store.Get(name)
		require.NoError(t, err, "Get(%q) after rotation must succeed", name)
		assert.Equal(t, want, got, "entry %q must survive rotation", name)
	}

	// A fresh store on the new passphrase must also read them all back, and a
	// swap on the rotated file must still be refused.
	fresh := NewStore(path)
	require.NoError(t, fresh.UnlockWithPassphrase("new-passphrase-for-aad"))
	for name, want := range creds {
		got, err := fresh.Get(name)
		require.NoError(t, err, "fresh store on new passphrase must read %q", name)
		assert.Equal(t, want, got)
	}

	sf := aadTestReadFile(t, path)
	sf.Credentials["ENTRY_TWO"] = sf.Credentials["ENTRY_ONE"]
	aadTestWriteFile(t, path, sf)

	got, err := fresh.Get("ENTRY_TWO")
	require.Error(t, err, "a swap made after rotation must still be rejected")
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), "ENTRY_TWO")
}

// TestAADBinding_ErrorIsDistinctFromWrongPassphraseWording checks what an
// operator is told. A failed GCM tag is one bit for three causes — wrong key,
// wrong name binding, edited bytes — so the error must name the entry and
// enumerate the causes instead of asserting a wrong passphrase. What DOES
// separate them is scope, and that is asserted here too: a swapped entry
// leaves its neighbours readable, a wrong master key kills every entry.
func TestAADBinding_ErrorIsDistinctFromWrongPassphraseWording(t *testing.T) {
	store, path := aadTestStore(t, 0x55)
	require.NoError(t, store.Set("SECRET_A", "value-for-a"))
	require.NoError(t, store.Set("SECRET_B", "value-for-b"))

	sf := aadTestReadFile(t, path)
	sf.Credentials["SECRET_B"] = sf.Credentials["SECRET_A"]
	aadTestWriteFile(t, path, sf)

	_, swappedErr := store.Get("SECRET_B")
	require.Error(t, swappedErr)

	// The user-facing message must not blame the passphrase: that wording
	// belongs to a whole-store key failure, not to one entry that moved.
	assert.NotContains(t, swappedErr.Error(), "wrong master key",
		"a swapped entry must not be reported as a wrong-passphrase failure, got: %v", swappedErr)
	assert.Contains(t, swappedErr.Error(), "SECRET_B")
	assert.True(t,
		strings.Contains(swappedErr.Error(), "authentication") ||
			strings.Contains(swappedErr.Error(), "tampered") ||
			strings.Contains(swappedErr.Error(), "moved"),
		"the message must describe an entry-authentication failure, got: %v", swappedErr)

	// Classification is preserved for callers that only know the sentinel, so
	// gateway boot still treats this as store-wide-and-fatal.
	assert.ErrorIs(t, swappedErr, ErrWrongKey,
		"the entry-auth error must still classify as a decryption failure")

	// Scope discriminator: a swap is entry-scoped.
	_, neighbourErr := store.Get("SECRET_A")
	require.NoError(t, neighbourErr, "the unswapped entry must still open")

	// Scope discriminator: a wrong master key fails EVERY entry.
	wrongKeyStore := NewStore(path)
	require.NoError(t, wrongKeyStore.UnlockWithKey(aadTestKey(0x99)))
	_, aErr := wrongKeyStore.Get("SECRET_A")
	_, bErr := wrongKeyStore.Get("SECRET_B")
	require.Error(t, aErr, "a wrong master key must fail entry A")
	require.Error(t, bErr, "a wrong master key must fail entry B")
	assert.True(t, errors.Is(aErr, ErrWrongKey) && errors.Is(bErr, ErrWrongKey))
	var entryAuth *EntryAuthError
	assert.ErrorAs(t, aErr, &entryAuth, "a wrong key surfaces as an entry-auth failure too")
	assert.Equal(t, "SECRET_A", entryAuth.Name, "naming which entry was being read")
}
