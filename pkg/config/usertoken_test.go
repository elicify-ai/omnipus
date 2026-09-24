package config

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// hashBody bcrypt-hashes the secret body of a raw token, mirroring how the
// gateway stores token-set entry hashes (SEC-1 / UAT #399).
func hashBody(t *testing.T, raw string) BcryptHash {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(TokenSecret(raw)), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt body hash: %v", err)
	}
	return BcryptHash(h)
}

// hashFull bcrypt-hashes the FULL raw token, mirroring how a legacy single
// token_hash was stored.
func hashFull(t *testing.T, raw string) BcryptHash {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt full hash: %v", err)
	}
	return BcryptHash(h)
}

// TestVerifyToken_IDIndexedMatch proves the fast path: an ID-tagged token is
// verified against the matching set entry.
func TestVerifyToken_IDIndexedMatch(t *testing.T) {
	tok := "omnipus_11112222_aaaabbbbcccc"
	u := &UserConfig{
		Username: "alice",
		Tokens: []TokenEntry{
			{ID: "99998888", Hash: hashBody(t, "omnipus_99998888_other")},
			{ID: "11112222", Hash: hashBody(t, tok)},
		},
	}
	if err := u.VerifyToken(tok); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

// TestVerifyToken_WrongBodySameID rejects a forged token that copies a valid ID
// but carries the wrong secret body.
func TestVerifyToken_WrongBodySameID(t *testing.T) {
	u := &UserConfig{
		Tokens: []TokenEntry{
			{ID: "11112222", Hash: hashBody(t, "omnipus_11112222_realbody")},
		},
	}
	if err := u.VerifyToken("omnipus_11112222_FORGEDbody"); err == nil {
		t.Fatal("expected mismatch for forged body with copied ID, got nil")
	}
}

// TestVerifyToken_MultipleConcurrentTokens proves the core SEC-1 guarantee:
// several tokens coexist and each authenticates independently.
func TestVerifyToken_MultipleConcurrentTokens(t *testing.T) {
	t1 := "omnipus_aaaa1111_body1"
	t2 := "omnipus_bbbb2222_body2"
	t3 := "omnipus_cccc3333_body3"
	u := &UserConfig{
		Tokens: []TokenEntry{
			{ID: "aaaa1111", Hash: hashBody(t, t1)},
			{ID: "bbbb2222", Hash: hashBody(t, t2)},
			{ID: "cccc3333", Hash: hashBody(t, t3)},
		},
	}
	for _, tok := range []string{t1, t2, t3} {
		if err := u.VerifyToken(tok); err != nil {
			t.Errorf("token %q should verify, got %v", tok, err)
		}
	}
	if err := u.VerifyToken("omnipus_dddd4444_nope"); err == nil {
		t.Error("unknown token must not verify")
	}
}

// TestVerifyToken_LegacySingleToken proves backward compatibility: a record
// with only the legacy TokenHash (no Tokens set) still authenticates.
func TestVerifyToken_LegacySingleToken(t *testing.T) {
	legacy := "omnipus_0123456789abcdef" // legacy single-segment token
	u := &UserConfig{TokenHash: hashFull(t, legacy)}
	if err := u.VerifyToken(legacy); err != nil {
		t.Fatalf("legacy token must verify, got %v", err)
	}
	if err := u.VerifyToken("omnipus_ffffffffffffffff"); err == nil {
		t.Fatal("wrong legacy token must not verify")
	}
}

// TestVerifyToken_NoTokens reports ErrNoHashSet when the user holds nothing.
func TestVerifyToken_NoTokens(t *testing.T) {
	u := &UserConfig{Username: "empty"}
	if err := u.VerifyToken("omnipus_1111_x"); !errors.Is(err, ErrNoHashSet) {
		t.Fatalf("expected ErrNoHashSet, got %v", err)
	}
	if err := u.VerifyToken(""); !errors.Is(err, ErrNoHashSet) {
		t.Fatalf("expected ErrNoHashSet for empty token, got %v", err)
	}
}

// withCompareCount installs a bcrypt-compare counter for the rest of the test.
// Hash generation does not go through the compare, so calling this before
// hashBody/hashFull does not inflate the count.
func withCompareCount(t *testing.T) *int {
	t.Helper()
	n := 0
	restore := SetCompareHashAndPasswordForTest(func(hashed, pw []byte) error {
		n++
		return bcrypt.CompareHashAndPassword(hashed, pw)
	})
	t.Cleanup(restore)
	return &n
}

// TestVerifyTokenAgainst_IDMissDoesNotBcryptScan: an id that is not in the set
// is a mismatch, even when another entry was hashed from the same secret body
// and even when the legacy single hash would match the full presented string.
// The old fallthrough accepted both; that is the CPU-exhaustion path.
func TestVerifyTokenAgainst_IDMissDoesNotBcryptScan(t *testing.T) {
	stored := "omnipus_bbbbbbbb_samebody"
	presented := "omnipus_aaaaaaaa_samebody"
	tokens := []TokenEntry{{ID: "bbbbbbbb", Hash: hashBody(t, stored)}}
	legacy := hashFull(t, presented) // old fallthrough would accept this
	n := withCompareCount(t)

	err := VerifyTokenAgainst(tokens, legacy, presented)
	if err == nil {
		t.Fatal("id miss must not match another entry's body or the legacy hash")
	}
	if !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		t.Fatalf("err = %v, want bcrypt mismatch", err)
	}
	if *n != 0 {
		t.Fatalf("bcrypt compares = %d, want 0 (id miss must not scan)", *n)
	}
}

// TestVerifyTokenAgainst_IDHitIsOneCompare: a matching id verifies that one
// hash and does not walk the rest of the set.
func TestVerifyTokenAgainst_IDHitIsOneCompare(t *testing.T) {
	tok := "omnipus_11112222_aaaabbbbcccc"
	tokens := []TokenEntry{
		{ID: "99998888", Hash: hashBody(t, "omnipus_99998888_other")},
		{ID: "11112222", Hash: hashBody(t, tok)},
		{ID: "77776666", Hash: hashBody(t, "omnipus_77776666_later")},
	}
	n := withCompareCount(t)
	if err := VerifyTokenAgainst(tokens, "", tok); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
	if *n != 1 {
		t.Fatalf("bcrypt compares = %d, want 1", *n)
	}
}

// TestVerifyTokenAgainst_LegacyNoIDScansAndAccepts: a token with no embedded id
// still walks the set (and stops at the first match). A wrong one is rejected
// only after every entry has been tried.
func TestVerifyTokenAgainst_LegacyNoIDScansAndAccepts(t *testing.T) {
	first := "omnipus_" + strings.Repeat("1", 64)
	second := "omnipus_" + strings.Repeat("2", 64)
	wrong := "omnipus_" + strings.Repeat("3", 64)
	tokens := []TokenEntry{
		{Hash: hashFull(t, first)},
		{Hash: hashFull(t, second)},
	}
	n := withCompareCount(t)
	if err := VerifyTokenAgainst(tokens, "", second); err != nil {
		t.Fatalf("legacy token must verify, got %v", err)
	}
	if *n != 2 {
		t.Fatalf("matching legacy compares = %d, want 2", *n)
	}
	*n = 0
	if err := VerifyTokenAgainst(tokens, "", wrong); err == nil {
		t.Fatal("wrong legacy token must not verify")
	}
	if *n != 2 {
		t.Fatalf("rejected legacy compares = %d, want 2", *n)
	}
}

func TestHasActiveToken(t *testing.T) {
	if (&UserConfig{}).HasActiveToken() {
		t.Error("empty user must not have an active token")
	}
	if !(&UserConfig{TokenHash: "x"}).HasActiveToken() {
		t.Error("legacy token_hash counts as active")
	}
	if !(&UserConfig{Tokens: []TokenEntry{{ID: "a", Hash: "h"}}}).HasActiveToken() {
		t.Error("token set entry counts as active")
	}
}
