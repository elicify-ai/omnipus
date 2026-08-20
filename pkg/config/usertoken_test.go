package config

import (
	"errors"
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

func TestTokenIDFromRaw(t *testing.T) {
	cases := map[string]string{
		"omnipus_deadbeef_aabbccdd": "deadbeef", // id-tagged
		"omnipus_aabbccdd":          "",         // legacy single-segment
		"omnipus_":                  "",         // degenerate
		"random-token":              "",         // non-omnipus
		"":                          "",
	}
	for raw, want := range cases {
		if got := TokenIDFromRaw(raw); got != want {
			t.Errorf("TokenIDFromRaw(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestTokenSecret(t *testing.T) {
	// ID-tagged token: secret is the body only.
	if got := TokenSecret("omnipus_deadbeef_aabbcc"); got != "aabbcc" {
		t.Errorf("TokenSecret(id-tagged) = %q, want %q", got, "aabbcc")
	}
	// Legacy token: secret is the whole string.
	if got := TokenSecret("omnipus_aabbcc"); got != "omnipus_aabbcc" {
		t.Errorf("TokenSecret(legacy) = %q, want whole", got)
	}
	// The bcrypt input must stay <= 72 bytes for a real-sized token.
	full := "omnipus_deadbeef_" + // 17 bytes prefix+id+sep
		"00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff" // 64 hex
	if n := len(TokenSecret(full)); n != 64 {
		t.Errorf("TokenSecret body length = %d, want 64 (must be <=72 for bcrypt)", n)
	}
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

// TestVerifyToken_IDMissFallsBackToScan: an id-tagged token whose ID is not in
// the set is still found by the scan fallback when its body matches an entry.
func TestVerifyToken_IDMissFallsBackToScan(t *testing.T) {
	// Same body bytes but the stored entry has a different ID than presented.
	body := "omnipus_AAAA_sharedbody"
	u := &UserConfig{
		Tokens: []TokenEntry{
			{ID: "BBBB", Hash: hashBody(t, body)}, // ID differs from presented
		},
	}
	// Presented token: body is "sharedbody" (same), ID "AAAA" not present.
	if err := u.VerifyToken(body); err != nil {
		t.Fatalf("scan fallback should match on body, got %v", err)
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
