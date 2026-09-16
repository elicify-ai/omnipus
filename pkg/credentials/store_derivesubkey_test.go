// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/plan"
)

// subkeyTestKey returns a deterministic 32-byte master key. DeriveSubkey is a
// pure function of (master key, info), so fixed keys keep these tests
// reproducible without touching crypto/rand.
func subkeyTestKey(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed ^ byte(i)
	}
	return key
}

// unlockedSubkeyStore returns a Store unlocked with key, backed by a fresh
// temp path. DeriveSubkey never touches disk, so the path is never read; it
// only proves the derivation ignores it.
func unlockedSubkeyStore(t *testing.T, key []byte) *credentials.Store {
	t.Helper()
	st := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.NoError(t, st.UnlockWithKey(key))
	return st
}

// TestDeriveSubkey_Deterministic proves the doc comment's stability claim:
// same master key + same info string yields the same subkey, on every call,
// from every Store instance unlocked with that key.
func TestDeriveSubkey_Deterministic(t *testing.T) {
	key := subkeyTestKey(0x5a)
	st := unlockedSubkeyStore(t, key)

	first, err := st.DeriveSubkey(audit.AuditChainKeyInfo)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		again, err := st.DeriveSubkey(audit.AuditChainKeyInfo)
		require.NoError(t, err)
		assert.True(t, bytes.Equal(first, again),
			"repeat call %d must reproduce the same subkey", i)
	}

	// A second Store instance, different path, same key: the derivation
	// depends only on (master key, info), never on instance state.
	other := unlockedSubkeyStore(t, key)
	fromOther, err := other.DeriveSubkey(audit.AuditChainKeyInfo)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(first, fromOther),
		"two stores unlocked with the same key must derive identical subkeys")
}

// TestDeriveSubkey_DomainSeparation proves the property both HMAC chains
// depend on: different info strings get different keys from one master key,
// so neither chain's key can forge the other's records.
func TestDeriveSubkey_DomainSeparation(t *testing.T) {
	// Guard the guard: if the two production info tags ever collapse to one
	// string, separation is gone by construction — fail with the real
	// problem's name instead of a confusing HKDF mismatch.
	require.NotEqual(t, audit.AuditChainKeyInfo, plan.IntentLogChainKeyInfo,
		"the audit and intent-log chain-key info tags must never be the same string")

	st := unlockedSubkeyStore(t, subkeyTestKey(0x11))
	auditKey, err := st.DeriveSubkey(audit.AuditChainKeyInfo)
	require.NoError(t, err)
	intentKey, err := st.DeriveSubkey(plan.IntentLogChainKeyInfo)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(auditKey, intentKey),
		"the audit chain and the intent-log chain must receive different keys: "+
			"a match would let either chain's key forge the other's HMAC records")

	// Separation is a property of the info tag, not of one particular key —
	// it must survive a master-key change.
	st2 := unlockedSubkeyStore(t, subkeyTestKey(0x22))
	auditKey2, err := st2.DeriveSubkey(audit.AuditChainKeyInfo)
	require.NoError(t, err)
	intentKey2, err := st2.DeriveSubkey(plan.IntentLogChainKeyInfo)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(auditKey2, intentKey2),
		"domain separation must hold under a different master key too")

	// Labels differing in exactly one byte must still separate — a KDF that
	// truncated or normalized its info tag would collide them.
	one, err := st.DeriveSubkey("subkey-label-v1")
	require.NoError(t, err)
	two, err := st.DeriveSubkey("subkey-label-v2")
	require.NoError(t, err)
	assert.False(t, bytes.Equal(one, two),
		"labels differing in a single byte must derive different subkeys")
}

// TestDeriveSubkey_MasterKeySensitivity proves the subkey tracks the master
// key: changing the key changes the derived material for the same label.
func TestDeriveSubkey_MasterKeySensitivity(t *testing.T) {
	const info = audit.AuditChainKeyInfo

	base := unlockedSubkeyStore(t, subkeyTestKey(0x01))
	baseKey, err := base.DeriveSubkey(info)
	require.NoError(t, err)

	// A key differing in a single byte must change the subkey.
	oneByteOff := subkeyTestKey(0x01)
	oneByteOff[31] ^= 0x01
	st := unlockedSubkeyStore(t, oneByteOff)
	derived, err := st.DeriveSubkey(info)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(baseKey, derived),
		"flipping one master-key byte must change the subkey")

	// A completely different key must too.
	st2 := unlockedSubkeyStore(t, subkeyTestKey(0xff))
	derived2, err := st2.DeriveSubkey(info)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(baseKey, derived2),
		"a different master key must derive a different subkey for the same label")
}

// TestDeriveSubkey_OutputLength pins the length both callers rely on: the
// gateway installs the audit subkey via audit.SetProcessChainKey and the
// intent-log subkey via plan.NewIntentLog, both documented as 32-byte keys.
func TestDeriveSubkey_OutputLength(t *testing.T) {
	st := unlockedSubkeyStore(t, subkeyTestKey(0x33))
	for _, info := range []string{
		audit.AuditChainKeyInfo,
		plan.IntentLogChainKeyInfo,
		"omnipus-audit-chain-v2", // rotated tag, still 32 bytes
		"a",                      // shortest legal label
	} {
		out, err := st.DeriveSubkey(info)
		require.NoError(t, err, "info=%q", info)
		assert.Len(t, out, 32, "callers require a 32-byte key: info=%q", info)
	}
}

// TestDeriveSubkey_Errors pins the real error contract read off the code:
// ErrStoreLocked on a locked store, a distinct non-empty-info error that
// fires BEFORE the lock check, and nil key material on every failure path.
func TestDeriveSubkey_Errors(t *testing.T) {
	t.Run("locked store returns ErrStoreLocked", func(t *testing.T) {
		st := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
		out, err := st.DeriveSubkey(audit.AuditChainKeyInfo)
		require.Error(t, err)
		assert.Nil(t, out)
		assert.ErrorIs(t, err, credentials.ErrStoreLocked)
	})

	t.Run("empty info is rejected even when unlocked", func(t *testing.T) {
		st := unlockedSubkeyStore(t, subkeyTestKey(0x44))
		out, err := st.DeriveSubkey("")
		require.Error(t, err)
		assert.Nil(t, out)
		assert.NotErrorIs(t, err, credentials.ErrStoreLocked,
			"the empty-info guard is its own failure, not a locked store")
	})

	t.Run("empty info wins over a locked store", func(t *testing.T) {
		// Pins the actual precedence: the info check runs before the lock
		// check, so even a locked store reports the empty-info error.
		st := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
		out, err := st.DeriveSubkey("")
		require.Error(t, err)
		assert.Nil(t, out)
		assert.NotErrorIs(t, err, credentials.ErrStoreLocked)
	})

	t.Run("short master key is unrepresentable", func(t *testing.T) {
		// DeriveSubkey can only ever see a nil key or a 32-byte key:
		// UnlockWithKey refuses anything else, so there is no "short key"
		// behaviour below the store to test — the rejection IS the edge.
		st := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
		require.Error(t, st.UnlockWithKey(make([]byte, 31)))
		require.Error(t, st.UnlockWithKey(nil))
		assert.True(t, st.IsLocked())
	})
}

// rfc5869HKDFSHA256 expands ikm into 32 bytes of output keying material per
// RFC 5869 with SHA-256 and an absent (all-zero, HashLen-byte) salt,
// implemented directly from the spec with crypto/hmac. It shares no code
// with golang.org/x/crypto/hkdf, so a wrong construction in DeriveSubkey —
// wrong salt default, swapped HMAC roles, mangled info — fails the
// comparison instead of echoing the implementation back.
func rfc5869HKDFSHA256(ikm, info []byte) []byte {
	// Extract: PRK = HMAC-SHA256(salt, IKM), salt = HashLen zero bytes.
	extractor := hmac.New(sha256.New, make([]byte, sha256.Size))
	extractor.Write(ikm)
	prk := extractor.Sum(nil)

	// Expand: T(1) = HMAC-SHA256(PRK, info || 0x01); 32 bytes need one block.
	expander := hmac.New(sha256.New, prk)
	expander.Write(info)
	expander.Write([]byte{0x01})
	return expander.Sum(nil)
}

// TestDeriveSubkey_MatchesRFC5869HKDF proves the derivation is genuinely
// HKDF-SHA256 as documented, against an oracle built from the RFC itself,
// and that it agrees with pkg/audit's sibling implementation.
func TestDeriveSubkey_MatchesRFC5869HKDF(t *testing.T) {
	key := subkeyTestKey(0x77)
	st := unlockedSubkeyStore(t, key)

	for _, info := range []string{
		audit.AuditChainKeyInfo,
		plan.IntentLogChainKeyInfo,
		"omnipus-subkey-selfcheck",
	} {
		got, err := st.DeriveSubkey(info)
		require.NoError(t, err, "info=%q", info)
		want := rfc5869HKDFSHA256(key, []byte(info))
		assert.True(t, bytes.Equal(want, got),
			"DeriveSubkey(%q) must equal RFC 5869 HKDF-SHA256(zero salt) over the master key", info)
	}

	// pkg/audit re-implements this derivation for its direct callers
	// (audit.DeriveAuditKey); the store path and the audit path must agree,
	// or chain keys would split across code paths.
	viaAudit, err := audit.DeriveAuditKey(key)
	require.NoError(t, err)
	viaStore, err := st.DeriveSubkey(audit.AuditChainKeyInfo)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(viaAudit, viaStore),
		"credentials.DeriveSubkey and audit.DeriveAuditKey must produce the same chain key")
}

// TestDeriveSubkey_NeverReturnsMasterKey proves the doc comment's core
// exposure claim: the master key never comes back as (or aliases) derived
// material, and corrupting a returned subkey cannot poison later calls.
func TestDeriveSubkey_NeverReturnsMasterKey(t *testing.T) {
	// A 32-byte all-zero master key is accepted by UnlockWithKey and is the
	// most degenerate key an unlocked store can hold — the derivation must
	// still yield unrelated material, never the key itself.
	zeroKey := make([]byte, 32)
	st := unlockedSubkeyStore(t, zeroKey)
	out, err := st.DeriveSubkey(audit.AuditChainKeyInfo)
	require.NoError(t, err)
	assert.Len(t, out, 32)
	assert.False(t, bytes.Equal(zeroKey, out),
		"a zero master key must not be echoed back as its own subkey")

	// For a normal key: the returned slice must not alias the master key or
	// any cached derivation — mutating it must not affect later calls.
	key := subkeyTestKey(0x88)
	st2 := unlockedSubkeyStore(t, key)
	first, err := st2.DeriveSubkey(audit.AuditChainKeyInfo)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(key, first),
		"the subkey must never equal the master key")
	for i := range first {
		first[i] = 0xff
	}
	want := rfc5869HKDFSHA256(key, []byte(audit.AuditChainKeyInfo))
	second, err := st2.DeriveSubkey(audit.AuditChainKeyInfo)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(want, second),
		"corrupting a previously returned subkey must not affect later derivations")
}
