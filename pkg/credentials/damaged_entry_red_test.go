// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Founder addition 2026-10-15 (WU-4): reading one damaged credential entry
// returns a clear error and never panics; every other entry stays readable;
// startup and reload surface that damage as a visible, scoped failure.
//
// Oracles, not the current store implementation:
//   - founder addition (this brief): wrong nonce length. Malformed Base64
//     already returns a named error on this base (observed green, not kept
//     here — a green test is not a RED receipt).
//   - docs/troubleshooting.md "A connection shows a credential error": a
//     malformed entry encoding is a visible failure, and an entry that cannot
//     be read while another entry still decrypts stays scoped to that entry
//     (the sibling stays readable; this is not a whole-vault failure)
//   - AES-GCM nonce size is 12 bytes (NIST SP 800-38D / crypto/cipher.NewGCM).
//     A decoded nonce of any other length is the "wrong nonce length" case.
package credentials

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const (
	damagedEntryGoodName  = "GOOD_REF"
	damagedEntryBadName   = "BAD_REF"
	damagedEntryGoodValue = "good-plain-value"
	damagedEntryBadValue  = "bad-plain-value"
	// gcmNonceLen is the AES-GCM nonce length. A damaged entry uses a
	// different length on purpose.
	gcmNonceLen = 12
)

func unlockedDamagedEntryStore(t *testing.T) *Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)

	store := NewStore(home + "/credentials.json")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	if err := store.UnlockWithKey(key); err != nil {
		t.Fatalf("UnlockWithKey: %v", err)
	}
	if err := store.Set(damagedEntryGoodName, damagedEntryGoodValue); err != nil {
		t.Fatalf("Set %s: %v", damagedEntryGoodName, err)
	}
	if err := store.Set(damagedEntryBadName, damagedEntryBadValue); err != nil {
		t.Fatalf("Set %s: %v", damagedEntryBadName, err)
	}
	return store
}

// rewriteDamagedNonce replaces BAD_REF's nonce with valid Base64 of the
// wrong length (8 bytes, not the 12-byte AES-GCM nonce) and leaves GOOD_REF
// untouched.
func rewriteDamagedNonce(t *testing.T, store *Store, nonce []byte) {
	t.Helper()
	path := store.Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var sf storeFile
	if unmarshalErr := json.Unmarshal(raw, &sf); unmarshalErr != nil {
		t.Fatalf("parse store: %v", unmarshalErr)
	}
	entry, ok := sf.Credentials[damagedEntryBadName]
	if !ok {
		t.Fatalf("fixture: %s missing after Set", damagedEntryBadName)
	}
	entry.Nonce = base64.StdEncoding.EncodeToString(nonce)
	sf.Credentials[damagedEntryBadName] = entry
	rewritten, marshalErr := json.Marshal(sf)
	if marshalErr != nil {
		t.Fatalf("marshal store: %v", marshalErr)
	}
	if writeErr := os.WriteFile(path, rewritten, 0o600); writeErr != nil {
		t.Fatalf("write store: %v", writeErr)
	}
}

func assertClearDamagedEntryError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("reading the damaged entry returned no error — founder addition requires a clear error, never a value and never a panic")
	}
	msg := err.Error()
	if !strings.Contains(msg, damagedEntryBadName) {
		t.Fatalf("damaged-entry error %q must name %s (docs/troubleshooting.md: the failure names the credential reference)", msg, damagedEntryBadName)
	}
	if strings.Contains(msg, damagedEntryBadValue) || strings.Contains(msg, damagedEntryGoodValue) {
		t.Fatalf("damaged-entry error leaked a stored value: %q", msg)
	}
	lower := strings.ToLower(msg)
	if !strings.Contains(lower, "nonce") && !strings.Contains(lower, "base64") && !strings.Contains(lower, "malformed") {
		t.Fatalf("damaged-entry error %q is not a clear description of a wrong nonce or malformed Base64", msg)
	}
}

func assertSiblingStillReadable(t *testing.T, store *Store) {
	t.Helper()
	got, err := store.Get(damagedEntryGoodName)
	if err != nil {
		t.Fatalf("sibling %s must stay readable while %s is damaged, got %v (docs/troubleshooting.md: another stored entry still decrypts)", damagedEntryGoodName, damagedEntryBadName, err)
	}
	if got != damagedEntryGoodValue {
		t.Fatalf("sibling value = %q, want %q", got, damagedEntryGoodValue)
	}
}

// TestGet_WrongNonceLength_ReturnsClearError_SiblingStaysReadable covers the
// founder addition's read path: a wrong nonce length is a clear error, the
// server does not panic, and the other entry still opens.
func TestGet_WrongNonceLength_ReturnsClearError_SiblingStaysReadable(t *testing.T) {
	store := unlockedDamagedEntryStore(t)
	rewriteDamagedNonce(t, store, make([]byte, gcmNonceLen-4))

	_, err := store.Get(damagedEntryBadName)
	assertClearDamagedEntryError(t, err)
	assertSiblingStillReadable(t, store)

	// The same read through the public single-ref resolver (boot and reload
	// consumers call this) must be the same clear error, not a panic.
	_, err = ResolveRef(store, damagedEntryBadName)
	assertClearDamagedEntryError(t, err)
	got, err := ResolveRef(store, damagedEntryGoodName)
	if err != nil || got != damagedEntryGoodValue {
		t.Fatalf("ResolveRef sibling = %q, %v; want %q, nil", got, err, damagedEntryGoodValue)
	}
}

// TestHealthCheck_DamagedEntry_VisibleScopedFailure is the startup and reload
// probe. docs/troubleshooting.md and docs/connectors.md: a malformed entry
// encoding is a visible failure even when another entry decrypts, and it
// stays scoped — the sibling entry remains readable and this is not reported
// as "no entry in the vault could be decrypted".
func TestHealthCheck_DamagedEntry_VisibleScopedFailure(t *testing.T) {
	store := unlockedDamagedEntryStore(t)
	rewriteDamagedNonce(t, store, make([]byte, gcmNonceLen-4))

	// Sibling readability is the scoped half and must hold whether or not the
	// probe itself returns an error.
	assertSiblingStillReadable(t, store)

	err := store.HealthCheck()
	if err == nil {
		t.Fatal("HealthCheck returned success while BAD_REF has a wrong-length nonce — docs/troubleshooting.md and docs/connectors.md require a visible failure for a malformed entry encoding, not a silent startup")
	}
	msg := err.Error()
	if !strings.Contains(msg, damagedEntryBadName) {
		t.Fatalf("startup failure %q must name %s so the operator can fix that entry (docs/troubleshooting.md)", msg, damagedEntryBadName)
	}
	if strings.Contains(msg, damagedEntryBadValue) || strings.Contains(msg, damagedEntryGoodValue) {
		t.Fatalf("startup failure leaked a stored value: %q", msg)
	}
	// Whole-vault wording would mean nothing in the file can be read. A
	// sibling that still decrypts must not be classified that way.
	if strings.Contains(strings.ToLower(msg), "no store entry could be decrypted") {
		t.Fatalf("startup classified a single damaged entry as a whole-vault failure (%q) even though %s still decrypts", msg, damagedEntryGoodName)
	}
}
