package credentials

// RED tests — landing/skills (T9): SensitiveNamedValues, the read-only
// names-and-values enumeration §7.4's save check consumes (#1455 first
// slice). Store-side legs of the §7.4 scenarios: the enumeration is
// faithful (any value length, OAuth sub-values named <NAME>.access_token),
// a locked store refuses with ErrStoreLocked, a damaged entry scopes to
// DamagedEntriesError with the sibling still readable, and a missing store
// file is an empty allowed enumeration.
// Traces to: skills-spec-r15.md §7.4, S-4.2–S-4.6 (store-side legs), §17.1
// row 13 (T-E10 input contract).
//
// RED expectation: SensitiveNamedValues is a scaffold that returns
// errRedNotImplemented — every assertion below fails on the baseline.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newUnlockedStoreWith builds an unlocked store with the given name/value
// pairs (mirrors damaged_entry_red_test.go's fixture shape).
func newUnlockedStoreWith(t *testing.T, pairs ...[2]string) *Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)

	store := NewStore(filepath.Join(home, "credentials.json"))
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	require.NoError(t, store.UnlockWithKey(key))
	for _, p := range pairs {
		require.NoError(t, store.Set(p[0], p[1]))
	}
	return store
}

// namedValuesIndex indexes the enumeration by name.
func namedValuesIndex(t *testing.T, store *Store) map[string]string {
	t.Helper()
	pairs, err := store.SensitiveNamedValues()
	require.NoError(t, err, "RED: SensitiveNamedValues not implemented (scaffold)")
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		m[p.Name] = p.Value
	}
	return m
}

// TestSensitiveNamedValues_EnumeratesFaithfully (S-4.2/S-4.3 store side).
// BDD: 4-byte values and 3-byte values are both enumerated faithfully — the
// length policy (≥ 4 bytes counts as a secret) belongs to the save check,
// never to the store's enumeration.
// Traces to: skills-spec-r15.md §7.4, S-4.2, S-4.3.
func TestSensitiveNamedValues_EnumeratesFaithfully(t *testing.T) {
	store := newUnlockedStoreWith(t,
		[2]string{"API_KEY", "abcd"},         // 4 bytes
		[2]string{"SHORT_REF", "abc"},        // 3 bytes — exempt at the check, still enumerated
		[2]string{"LONG_TOKEN", "sk-live-9"}, // ordinary length
	)
	idx := namedValuesIndex(t, store)

	assert.Equal(t, "abcd", idx["API_KEY"], "a 4-byte value enumerates")
	assert.Equal(t, "abc", idx["SHORT_REF"], "a 3-byte value enumerates too (the exemption is the check's)")
	assert.Equal(t, "sk-live-9", idx["LONG_TOKEN"], "the plaintext round-trips")
	assert.Len(t, idx, 3, "every stored secret enumerates")
}

// TestSensitiveNamedValues_OAuthSubValues (S-4.4 store side).
// BDD: an OAuth blob entry enumerates its access_token and refresh_token as
// <NAME>.access_token / <NAME>.refresh_token, never the raw blob name only.
// Traces to: skills-spec-r15.md §7.4, S-4.4.
func TestSensitiveNamedValues_OAuthSubValues(t *testing.T) {
	provider := OAuthEntryName("exa") // exa_oauth
	blob := `{"access_token":"at-1234","refresh_token":"rt-5678","account_id":"acc","expires_at":0}`
	store := newUnlockedStoreWith(t, [2]string{provider, blob})
	idx := namedValuesIndex(t, store)

	assert.Equal(t, "at-1234", idx[provider+".access_token"],
		"the access token enumerates under <NAME>.access_token (the save check refuses naming it)")
	assert.Equal(t, "rt-5678", idx[provider+".refresh_token"],
		"the refresh token enumerates under <NAME>.refresh_token")
	_, hasRaw := idx[provider]
	assert.False(t, hasRaw, "the raw blob is not separately enumerated under its entry name")
}

// TestSensitiveNamedValues_LockedStore (S-4.5 leg 1).
// BDD: a locked store refuses with ErrStoreLocked — the save check then
// fails closed with SECRET_CHECK_UNAVAILABLE.
// Traces to: skills-spec-r15.md §7.4, S-4.5.
func TestSensitiveNamedValues_LockedStore(t *testing.T) {
	unlocked := newUnlockedStoreWith(t, [2]string{"LOCKED_REF", "synthetic-locked-value"})
	require.Equal(t, map[string]string{"LOCKED_REF": "synthetic-locked-value"}, namedValuesIndex(t, unlocked),
		"fixture control: the persisted encrypted entry is readable before locking")
	path := unlocked.Path()
	require.FileExists(t, path, "a locked existing store is distinct from a missing file")
	unlocked.Close()
	require.True(t, unlocked.IsLocked(), "the fixture released its key")
	store := NewStore(path)
	require.True(t, store.IsLocked(), "reopening the persisted store does not unlock it")
	require.True(t, store.Exists(), "the new locked handle still has an existing store")
	before, readErr := os.ReadFile(path)
	require.NoError(t, readErr)

	pairs, err := store.SensitiveNamedValues()
	require.Error(t, err, "an existing store without its key must refuse enumeration")
	assert.True(t, errors.Is(err, ErrStoreLocked),
		"a locked store refuses with ErrStoreLocked (the save check maps it to SECRET_CHECK_UNAVAILABLE), got: %v", err)
	assert.Nil(t, pairs, "no values leak from a locked store")
	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, before, after, "a refused read never changes the encrypted store")
}

// TestSensitiveNamedValues_DamagedEntryScopes (S-4.5 leg 3).
// BDD: one damaged entry among readable ones returns the readable pairs
// plus a DamagedEntriesError naming the damaged entry — the save check
// fails closed with the reason and the readable names.
// Traces to: skills-spec-r15.md §7.4, S-4.5.
func TestSensitiveNamedValues_DamagedEntryScopes(t *testing.T) {
	store := newUnlockedStoreWith(t,
		[2]string{"GOOD_REF", "good-plain-value"},
		[2]string{"BAD_REF", "bad-plain-value"},
	)
	// Corrupt BAD_REF's nonce (8 bytes, not the 12-byte AES-GCM nonce).
	path := store.Path()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var sf storeFile
	require.NoError(t, unmarshalStoreFile(raw, &sf))
	entry, ok := sf.Credentials["BAD_REF"]
	require.True(t, ok, "fixture: BAD_REF present after Set")
	entry.Nonce = "AAAAAAAAAAA=" // valid Base64, wrong length
	sf.Credentials["BAD_REF"] = entry
	encoded, err := marshalStoreFile(&sf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0o600))

	pairs, err := store.SensitiveNamedValues()
	require.Error(t, err, "RED: the scaffold's error is not a DamagedEntriesError")
	var damaged *DamagedEntriesError
	require.True(t, errors.As(err, &damaged),
		"a damaged entry scopes to DamagedEntriesError, got: %T %v", err, err)
	assert.Contains(t, damaged.Names, "BAD_REF", "the damaged entry is named")

	idx := map[string]string{}
	for _, p := range pairs {
		idx[p.Name] = p.Value
	}
	assert.Equal(t, "good-plain-value", idx["GOOD_REF"],
		"the sibling entry stays readable alongside the scoped failure")
}

// TestSensitiveNamedValues_MissingStoreFile (S-4.6 store side).
// BDD: no store file → an empty enumeration, no error (the save check then
// commits without consulting anything).
// Traces to: skills-spec-r15.md §7.4, S-4.6.
func TestSensitiveNamedValues_MissingStoreFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	store := NewStore(filepath.Join(home, "credentials.json"))
	require.True(t, store.IsLocked(), "no key was supplied, but no encrypted data exists either")
	require.NoFileExists(t, store.Path(), "fixture control: the file is absent")

	pairs, err := store.SensitiveNamedValues()
	require.NoError(t, err, "a missing store file is an allowed empty enumeration")
	assert.Empty(t, pairs, "no values without a store file")
	assert.NoFileExists(t, store.Path(), "read-only enumeration does not create a store")
}

// unmarshalStoreFile/marshalStoreFile wrap the JSON round-trip for the
// damaged-entry fixture (in-package helpers; the storeFile shape is
// package-internal).
func unmarshalStoreFile(raw []byte, sf *storeFile) error {
	return json.Unmarshal(raw, sf)
}

func marshalStoreFile(sf *storeFile) ([]byte, error) {
	return json.Marshal(sf)
}
