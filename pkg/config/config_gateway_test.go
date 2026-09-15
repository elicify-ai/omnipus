// config_gateway_test.go: tests for gateway configuration: the Gateway section types and their helpers

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- moved from config.go tests 2026-09-15 ---

// TestVerifyTokenAgainst_MatchesCLITokenSlot proves the extracted
// VerifyTokenAgainst helper works directly against a Gateway.CLIToken slot
// (a single *TokenEntry, not a UserConfig) — the shape pkg/gateway's
// auth/websocket CLI-token check is expected to use.
func TestVerifyTokenAgainst_MatchesCLITokenSlot(t *testing.T) {
	hash := hashFull(t, "supersecretclitoken")

	cliToken := TokenEntry{ID: "cli01", Hash: hash}

	err := VerifyTokenAgainst([]TokenEntry{cliToken}, BcryptHash(""), "supersecretclitoken")
	assert.NoError(t, err, "correct raw token must verify against the CLIToken slot")

	err = VerifyTokenAgainst([]TokenEntry{cliToken}, BcryptHash(""), "wrong-token")
	assert.Error(t, err, "incorrect raw token must fail verification")

	err = VerifyTokenAgainst(nil, BcryptHash(""), "supersecretclitoken")
	assert.ErrorIs(t, err, ErrNoHashSet, "an empty token set must report ErrNoHashSet")
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
