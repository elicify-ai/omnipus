// config_gateway.go: Gateway configuration: the Gateway section types and their helpers

package config

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// TokenIDFromRaw extracts the embedded non-secret ID prefix from a raw bearer
// token of the form "omnipus_<id>_<body>". Returns "" when the token is not in
// the ID-tagged form (e.g. a legacy "omnipus_<hex>" token or an env token),
// signaling callers to fall back to scanning the whole set.
func TokenIDFromRaw(raw string) string {
	const prefix = "omnipus_"
	if !strings.HasPrefix(raw, prefix) {
		return ""
	}
	rest := raw[len(prefix):]
	idx := strings.IndexByte(rest, '_')
	if idx <= 0 {
		// No second underscore → legacy "omnipus_<hex>" form with no ID.
		return ""
	}
	return rest[:idx]
}

// TokenSecret returns the substring of a raw bearer token that is bcrypt-hashed
// to produce a token-set entry's Hash.
//
// SEC-1 / bcrypt 72-byte limit: an ID-tagged token "omnipus_<id>_<body>" is
// 81 bytes — past bcrypt's 72-byte input ceiling, beyond which bytes are
// silently ignored. Because the ID prefix is NON-SECRET routing metadata, we
// bcrypt only the secret <body> (the 256-bit entropy). Generation and
// verification MUST agree on this, so both go through TokenSecret. A legacy
// token with no ID is returned whole (its stored hash was computed over the
// full "omnipus_<hex>" string, which is exactly 72 bytes).
func TokenSecret(raw string) string {
	if id := TokenIDFromRaw(raw); id != "" {
		// Strip "omnipus_<id>_" leaving the secret body.
		return raw[len("omnipus_")+len(id)+1:]
	}
	return raw
}

// VerifyTokenAgainst checks raw against the given token set (and, for
// backward compatibility, a single legacy hash). Returns nil on a match.
//
// Extracted from the (*UserConfig) VerifyToken method so any token-bearing
// config shape — the (now singular) human account's UserConfig.Tokens, or
// the standalone Gateway.CLIToken slot — can be verified without needing a
// full UserConfig. SEC-1: it first parses the embedded ID prefix and, when
// present, verifies against ONLY the matching entry's hash (constant-time
// bcrypt compare over the secret body). When the ID is absent (legacy token)
// it scans every token entry and the legacy single hash. Returns nil on a
// match, ErrNoHashSet when tokens is empty and legacyHash is zero, or the
// bcrypt mismatch error otherwise.
func VerifyTokenAgainst(tokens []TokenEntry, legacyHash BcryptHash, raw string) error {
	if raw == "" {
		return ErrNoHashSet
	}
	if len(tokens) == 0 && legacyHash.IsZero() {
		return ErrNoHashSet
	}

	secret := TokenSecret(raw)

	// Fast path: direct index by embedded ID prefix.
	if id := TokenIDFromRaw(raw); id != "" {
		for i := range tokens {
			if tokens[i].ID == id {
				return tokens[i].Hash.Verify(secret)
			}
		}
		// ID present but no matching entry — fall through to a full scan so a
		// race (entry just appended/evicted) or a colliding legacy token still
		// gets a fair chance, then report mismatch.
	}

	// Scan the full token set (legacy token, or ID lookup miss).
	for i := range tokens {
		if tokens[i].Hash.Verify(secret) == nil {
			return nil
		}
	}
	// Legacy single-token field — its hash was computed over the FULL raw token.
	if !legacyHash.IsZero() && legacyHash.Verify(raw) == nil {
		return nil
	}
	return bcrypt.ErrMismatchedHashAndPassword
}

// VerifyToken reports whether raw matches any active bearer token for this user.
//
// SEC-1: it first parses the embedded ID prefix and, when present, verifies
// against ONLY the matching entry's hash (constant-time bcrypt compare over the
// secret body). When the ID is absent (legacy token) it scans every token entry
// and the legacy single TokenHash. Returns nil on a match, ErrNoHashSet when the
// user holds no tokens at all, or the bcrypt mismatch error otherwise.
func (u *UserConfig) VerifyToken(raw string) error {
	return VerifyTokenAgainst(u.Tokens, u.TokenHash, raw)
}

// HasActiveToken reports whether the user holds at least one live bearer token
// (either in the new Tokens set or the legacy TokenHash field).
func (u *UserConfig) HasActiveToken() bool {
	return len(u.Tokens) > 0 || !u.TokenHash.IsZero()
}

// VerifyCLIToken checks raw against the machine-only CLI bearer credential
// (g.CLIToken), the decoupled counterpart of UserConfig.VerifyToken.
// Nil-safe: when no CLI token has been minted yet (g.CLIToken == nil) it
// returns the same ErrNoHashSet that VerifyTokenAgainst returns for an empty
// token set, so callers don't need their own nil check before calling this —
// replacing the `if cfg.Gateway.CLIToken != nil { ... }` guard that was
// previously duplicated at every call site.
func (g *GatewayConfig) VerifyCLIToken(raw string) error {
	if g.CLIToken == nil {
		return ErrNoHashSet
	}
	return VerifyTokenAgainst([]TokenEntry{*g.CLIToken}, "", raw)
}

// SetUserTokenHash sets the token hash for a user identified by username.
func (c *Config) SetUserTokenHash(username, token string) error {
	for i := range c.Gateway.Users {
		if c.Gateway.Users[i].Username == username {
			hash, err := bcryptHash(token)
			if err != nil {
				return fmt.Errorf("bcrypt hash failed: %w", err)
			}
			c.Gateway.Users[i].TokenHash = BcryptHash(hash)
			return nil
		}
	}
	return fmt.Errorf("user %q not found", username)
}

// bcryptHash creates a bcrypt hash of the input string.
func bcryptHash(input string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(input), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}
