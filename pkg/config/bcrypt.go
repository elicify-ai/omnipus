// Package config: BcryptHash named-string type so password / token /
// session-token hashes carry their semantic at the type level rather than
// being indistinguishable from arbitrary strings.

package config

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// BcryptHash is the bcrypt-encoded form of a credential (password, bearer
// token, or session token). Stored on disk as a string; verified at the
// type via Verify() rather than by ad-hoc bcrypt.CompareHashAndPassword
// calls scattered through callers.
//
// The defined string type allows zero-cost conversion to/from string for
// JSON marshaling and existing callsites that need the raw bytes (e.g.
// []byte(hash) for one-off compares), while making typed function
// signatures self-documenting.
type BcryptHash string

// ErrNoHashSet is returned by Verify when the field is the empty string.
// Callers typically translate this to 401 Unauthorized — the user has no
// stored hash, so no password / token / cookie can match.
var ErrNoHashSet = errors.New("config: hash not set")

// IsZero reports whether the hash is empty (no credential stored).
func (h BcryptHash) IsZero() bool {
	return h == ""
}

// String returns the underlying string. Useful for explicit conversions
// where a callsite wants to make the cast visible.
func (h BcryptHash) String() string {
	return string(h)
}

// Verify reports whether plaintext bcrypt-hashes to this value.
// Returns ErrNoHashSet on empty receiver, or the bcrypt mismatch error
// from bcrypt.CompareHashAndPassword on hash mismatch.
//
// Constant-time comparison is bcrypt-internal.
func (h BcryptHash) Verify(plaintext string) error {
	if h == "" {
		return ErrNoHashSet
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(plaintext))
}
