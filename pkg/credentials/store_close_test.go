// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// allZero reports whether every byte of b is zero.
func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// The store holds the derived key for as long as its owner needs it, then
// overwrites it. Without that, the key survives in the heap until a collection
// that may never come — and a core dump, a swap file, or a debugger attached
// days later still reads it.
func TestStoreClose_WipesKeyMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := NewStore(path)
	require.NoError(t, s.UnlockWithKey(bytes.Repeat([]byte{0xAB}, keyLen)))

	// The array the store owns, captured while it still holds the key. Close
	// must overwrite THIS array, not merely drop the reference to it.
	held := s.key
	require.False(t, allZero(held), "precondition: the store holds non-zero key material")

	s.Close()

	require.True(t, allZero(held),
		"the store's key array must be overwritten on Close, got %x", held)
	require.True(t, s.IsLocked(), "Close must leave the store locked")
	require.Nil(t, s.key, "Close must drop the store's reference to the key")
}

// Close overwrites the copy the store owns. Wiping a caller's slice would be a
// surprising side effect — and it is the reason every unlock path copies rather
// than aliases.
func TestStoreClose_WipesOnlyTheStoresOwnCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := NewStore(path)
	caller := bytes.Repeat([]byte{0xCD}, keyLen)
	require.NoError(t, s.UnlockWithKey(caller))

	s.Close()

	require.Equal(t, bytes.Repeat([]byte{0xCD}, keyLen), caller,
		"Close must wipe the store's copy, leaving the caller's slice untouched")
}

// Rotation replaces the key. The replaced array is unreachable from the store
// afterwards, so if it is not overwritten at that moment nothing will ever
// overwrite it — it stays in the heap for the life of the process.
func TestStoreRotate_WipesTheReplacedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := NewStore(path)
	require.NoError(t, s.UnlockWithKey(bytes.Repeat([]byte{0x11}, keyLen)))
	require.NoError(t, s.Set("ROTATE_TOKEN", "value"))

	replaced := s.key
	require.False(t, allZero(replaced), "precondition: the store holds non-zero key material")

	require.NoError(t, s.Rotate(bytes.Repeat([]byte{0x22}, keyLen)))

	require.True(t, allZero(replaced),
		"the replaced key array must be overwritten during rotation, got %x", replaced)
	require.False(t, allZero(s.key), "the store must hold the NEW key after rotation")

	// Rotation must still have done its real job.
	got, err := s.Get("ROTATE_TOKEN")
	require.NoError(t, err)
	require.Equal(t, "value", got)
}

// Close is called from deferred shutdown paths and from every CLI command, so a
// second call must be harmless — including on a store that was never unlocked.
func TestStoreClose_IsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := NewStore(path)
	require.NoError(t, s.UnlockWithKey(bytes.Repeat([]byte{0x33}, keyLen)))

	require.NotPanics(t, func() { s.Close() }, "first Close")
	require.NotPanics(t, func() { s.Close() }, "second Close is a no-op")
	require.True(t, s.IsLocked())

	require.NotPanics(t, func() { NewStore(path).Close() },
		"Close on a never-unlocked store is safe")
}

// A closed store must refuse to operate. Zeroing the key without clearing the
// field would leave every later call decrypting with 32 zero bytes — which
// reports ErrWrongKey, blaming the operator's master key for a bug of ours.
func TestStoreClose_UseAfterCloseFailsCleanly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := NewStore(path)
	require.NoError(t, s.UnlockWithKey(bytes.Repeat([]byte{0x44}, keyLen)))
	require.NoError(t, s.Set("AFTER_CLOSE_TOKEN", "value"))

	s.Close()

	_, err := s.Get("AFTER_CLOSE_TOKEN")
	require.ErrorIs(t, err, ErrStoreLocked, "Get after Close must report a locked store")
	require.ErrorIs(t, s.Set("X", "y"), ErrStoreLocked, "Set after Close must report a locked store")
	require.ErrorIs(t, s.Delete("AFTER_CLOSE_TOKEN"), ErrStoreLocked,
		"Delete after Close must report a locked store")

	_, err = s.DeriveSubkey("test-close-info")
	require.ErrorIs(t, err, ErrStoreLocked, "DeriveSubkey after Close must report a locked store")
}
