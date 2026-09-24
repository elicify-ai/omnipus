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

// compareHashAndPassword is the compare BcryptHash.Verify runs. Tests swap it
// (SetCompareHashAndPasswordForTest) to count comparisons. Production keeps
// bcrypt.CompareHashAndPassword, which compares in constant time.
var compareHashAndPassword = bcrypt.CompareHashAndPassword

// SetCompareHashAndPasswordForTest replaces the compare used by
// BcryptHash.Verify and returns a restore function. The swap is not safe
// concurrently with other tests; do not call t.Parallel while it is installed.
func SetCompareHashAndPasswordForTest(fn func(hashedPassword, password []byte) error) func() {
	prev := compareHashAndPassword
	if fn == nil {
		compareHashAndPassword = bcrypt.CompareHashAndPassword
	} else {
		compareHashAndPassword = fn
	}
	return func() { compareHashAndPassword = prev }
}

// Verify reports whether plaintext bcrypt-hashes to this value.
// Returns ErrNoHashSet on empty receiver, or the bcrypt mismatch error
// from bcrypt.CompareHashAndPassword on hash mismatch.
//
// Constant-time comparison is bcrypt-internal. An empty hash returns before
// any compare, so it does not count as a bcrypt verification.
func (h BcryptHash) Verify(plaintext string) error {
	if h == "" {
		return ErrNoHashSet
	}
	return compareHashAndPassword([]byte(h), []byte(plaintext))
}
