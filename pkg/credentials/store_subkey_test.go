// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The two production info tags DeriveSubkey exists to serve, mirrored as
// literals so this package's tests pull in no other Omnipus package. Source of
// truth: pkg/audit AuditChainKeyInfo and pkg/plan IntentLogChainKeyInfo; if
// either constant changes, update the literal here to match.
const (
	testAuditChainInfo  = "omnipus-audit-chain-v1"
	testIntentChainInfo = "omnipus-intent-log-chain-v1"
)

// testMasterKey returns a deterministic throwaway master key whose every byte
// is b. These keys are test values only — never secrets.
func testMasterKey(b byte) []byte {
	return bytes.Repeat([]byte{b}, keyLen)
}

// rfc5869HKDFSHA256 computes HKDF-SHA256 straight from RFC 5869 using only the
// standard library's HMAC — deliberately NOT golang.org/x/crypto/hkdf, which
// is the code under test. A nil salt is defined by RFC 5869 §2.2 as a string
// of HashLen zero bytes; Expand is T(i) = HMAC(PRK, T(i-1) || info || i) with
// T(0) empty and i starting at 1. Re-deriving the construction here means the
// oracle is independent of the implementation it checks.
func rfc5869HKDFSHA256(ikm, info []byte, length int) []byte {
	extractor := hmac.New(sha256.New, make([]byte, sha256.Size))
	extractor.Write(ikm)
	prk := extractor.Sum(nil)

	okm := make([]byte, 0, length)
	var t []byte
	for i := byte(1); len(okm) < length; i++ {
		h := hmac.New(sha256.New, prk)
		h.Write(t)
		h.Write(info)
		h.Write([]byte{i})
		t = h.Sum(nil)
		okm = append(okm, t...)
	}
	return okm[:length]
}

// DeriveSubkey feeds BOTH tamper-evidence HMAC chains (audit log and plan
// intent log). Determinism is therefore not a nicety: the audit chain's
// verification replays old entries against the key derived today, so the same
// master key and info tag must yield byte-identical subkeys on every call —
// and across Store instances, because the derivation must depend only on
// (key, info), never on per-store state like the path or salt file.
func TestDeriveSubkey_DeterministicAcrossCallsAndStores(t *testing.T) {
	dir := t.TempDir()
	key := testMasterKey(0x42)

	storeA := NewStore(filepath.Join(dir, "a", "credentials.json"))
	require.NoError(t, storeA.UnlockWithKey(key))

	first, err := storeA.DeriveSubkey(testAuditChainInfo)
	require.NoError(t, err)
	var again []byte
	for i := 0; i < 3; i++ {
		again, err = storeA.DeriveSubkey(testAuditChainInfo)
		require.NoError(t, err)
		require.Equal(t, first, again, "repeated call %d must reproduce the subkey", i)
	}

	storeB := NewStore(filepath.Join(dir, "b", "credentials.json"))
	require.NoError(t, storeB.UnlockWithKey(key))
	fromB, err := storeB.DeriveSubkey(testAuditChainInfo)
	require.NoError(t, err)
	require.Equal(t, first, fromB, "a second store unlocked with the same key must derive the same subkey")

	require.Len(t, first, 32)
	require.NotEqual(t, key, first, "the master key itself must never be handed out as a subkey")
}

// Domain separation is the property both HMAC chains lean on: different info
// tags must yield different keys, so a compromise of one chain's key cannot
// forge the other chain. The near-miss pairs (version-suffixed tags, one-byte
// tags sharing a prefix) guard against derivations that truncate or anchor on
// a label prefix instead of binding the full info string.
func TestDeriveSubkey_DomainSeparationBetweenLabels(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.NoError(t, s.UnlockWithKey(testMasterKey(0x07)))

	derived := make(map[string][]byte)
	labels := []string{
		testAuditChainInfo,
		testIntentChainInfo,
		testAuditChainInfo + "-v2", // version bump = supported rotation path
		"a",
		"b",
	}
	for _, label := range labels {
		key, err := s.DeriveSubkey(label)
		require.NoError(t, err)
		require.Len(t, key, 32)
		derived[label] = key
	}

	for i, li := range labels {
		for j, lj := range labels {
			if j <= i {
				continue
			}
			require.NotEqualf(t, derived[li], derived[lj],
				"info tags %q and %q must not share a subkey", li, lj)
		}
	}

	// The production pair is the one that must never collide: audit chain vs
	// intent-log chain off the same master key.
	require.NotEqual(t, derived[testAuditChainInfo], derived[testIntentChainInfo],
		"audit-chain and intent-log keys must be cryptographically independent")
}

// A different master key must move every subkey: otherwise a key stolen from
// one install would derive valid chain keys for another. One flipped master-key
// byte is enough — HKDF-SHA256 is a PRF, so any input change must avalanche.
func TestDeriveSubkey_MasterKeySensitivity(t *testing.T) {
	dir := t.TempDir()
	key1 := testMasterKey(0x11)
	key2 := bytes.Clone(key1)
	key2[17] ^= 0x01 // exactly one master-key byte differs

	store1 := NewStore(filepath.Join(dir, "one", "credentials.json"))
	require.NoError(t, store1.UnlockWithKey(key1))
	store2 := NewStore(filepath.Join(dir, "two", "credentials.json"))
	require.NoError(t, store2.UnlockWithKey(key2))

	for _, label := range []string{testAuditChainInfo, testIntentChainInfo} {
		from1, err := store1.DeriveSubkey(label)
		require.NoError(t, err)
		from2, err := store2.DeriveSubkey(label)
		require.NoError(t, err)
		require.NotEqualf(t, from1, from2,
			"a one-byte master-key change must change the subkey for %q", label)
	}
}

// Both callers (audit chain, intent-log chain) consume the subkey as a
// 32-byte HMAC-SHA-256 key; DeriveSubkey's contract fixes the length at 32
// regardless of how long or exotic the info tag is.
func TestDeriveSubkey_OutputLengthIs32Bytes(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.NoError(t, s.UnlockWithKey(testMasterKey(0x99)))

	for _, label := range []string{
		"x",
		testAuditChainInfo,
		testIntentChainInfo,
		"omnipus-δ-intent-chain-v1", // non-ASCII exercises raw byte conversion
		strings.Repeat("long-", 60), // 300 chars
	} {
		key, err := s.DeriveSubkey(label)
		require.NoError(t, err)
		require.Lenf(t, key, 32, "info tag %q", label)
	}
}

// Pins the exact construction: HKDF-SHA256 per RFC 5869 with the master key as
// IKM, no salt, and the caller's info string as the only context. The expected
// value is recomputed from the RFC with the standard library's HMAC — a code
// path fully independent of golang.org/x/crypto/hkdf — so agreement proves the
// derivation matches the standard construction, not merely itself. This is
// what makes silent derivation changes (an added salt, a different hash, info
// framing) show up as test failures instead of silent key rotation.
func TestDeriveSubkey_MatchesRFC5869HKDFSHA256(t *testing.T) {
	key := testMasterKey(0xAB)

	s := NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.NoError(t, s.UnlockWithKey(key))

	for _, label := range []string{testAuditChainInfo, testIntentChainInfo, "arbitrary-tag"} {
		got, err := s.DeriveSubkey(label)
		require.NoError(t, err)
		want := rfc5869HKDFSHA256(key, []byte(label), 32)
		require.Equalf(t, want, got, "subkey for info %q must equal RFC 5869 HKDF-SHA256(nil salt)", label)
	}
}

// The empty-info guard fires before the lock check, so an empty tag is
// rejected with the info error even on a locked store — the code's actual
// ordering, pinned here so a reorder is a conscious change, not an accident.
func TestDeriveSubkey_EmptyInfoRejectedEvenWhenLocked(t *testing.T) {
	dir := t.TempDir()

	locked := NewStore(filepath.Join(dir, "locked", "credentials.json"))
	require.True(t, locked.IsLocked())
	out, err := locked.DeriveSubkey("")
	require.Error(t, err)
	require.Nil(t, out)
	require.NotErrorIs(t, err, ErrStoreLocked, "empty info must fail on the info guard, not the lock check")

	unlocked := NewStore(filepath.Join(dir, "unlocked", "credentials.json"))
	require.NoError(t, unlocked.UnlockWithKey(testMasterKey(0x05)))
	out, err = unlocked.DeriveSubkey("")
	require.Error(t, err)
	require.Nil(t, out)
	require.NotErrorIs(t, err, ErrStoreLocked)
}

// An unlocked store must gate every derivation behind ErrStoreLocked — the
// master key cannot exist for HKDF to consume until Unlock succeeds.
func TestDeriveSubkey_LockedStoreReturnsErrStoreLocked(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.True(t, s.IsLocked())

	out, err := s.DeriveSubkey(testAuditChainInfo)
	require.Nil(t, out)
	require.ErrorIs(t, err, ErrStoreLocked)

	// The same store, once unlocked, must derive fine — proving the earlier
	// failure was the lock, not the tag.
	require.NoError(t, s.UnlockWithKey(testMasterKey(0x33)))
	out, err = s.DeriveSubkey(testAuditChainInfo)
	require.NoError(t, err)
	require.Len(t, out, 32)
}

// There is no reachable "short master key" state: UnlockWithKey is the only
// direct key-injection path and it enforces exactly keyLen bytes, rejecting
// everything else and leaving the store locked. This pins that precondition
// rather than inventing a short-key contract DeriveSubkey does not have.
func TestDeriveSubkey_Non32ByteMasterKeyUnreachable(t *testing.T) {
	for _, length := range []int{0, 16, 31, 33, 64} {
		s := NewStore(filepath.Join(t.TempDir(), "credentials.json"))
		require.Errorf(t, s.UnlockWithKey(make([]byte, length)),
			"a %d-byte key must be rejected at unlock time", length)
		require.True(t, s.IsLocked(), "a rejected unlock must leave the store locked")

		out, err := s.DeriveSubkey(testAuditChainInfo)
		require.Nil(t, out)
		require.ErrorIsf(t, err, ErrStoreLocked,
			"a store that rejected a %d-byte key must still refuse to derive", length)
	}
}
