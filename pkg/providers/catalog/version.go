package catalog

// Version — the catalog document's release version (FR-002, F-01, A-6).
//
// The assembly repository tags each daily release `vYYYY.M.D[.N]` (e.g.
// `v2026.8.22`, or `v2026.8.22.1` for a same-day re-release). The leading
// `v` is mandatory; the four components compare numerically, so
// `v2026.8.9 < v2026.8.10` and `v2026.9.30 < v2026.10.1` — the orderings a
// lexical compare of the raw string gets wrong. An absent `.N` is 0.
//
// The refresh transaction (T067-04) uses Compare for the anti-downgrade
// gate (US-3.AC6): a pulled document whose Version sorts below the served
// one is rejected with reason=regressed; an equal one is a permitted no-op.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrInvalidVersion is wrapped by ParseVersion on any string that does not
// match vYYYY.M.D[.N]. ParseDocument folds it into ErrInvalid (the
// `version` field is then reported as reason=invalid, DS-7 row 6).
var ErrInvalidVersion = errors.New("catalog: invalid version")

// versionRe is FR-002's version rule, anchored: exactly four year digits,
// one or two month and day digits, an optional numeric fourth component.
var versionRe = regexp.MustCompile(`^v\d{4}\.\d{1,2}\.\d{1,2}(\.\d+)?$`)

// Version is a parsed, comparable catalog version. The zero Version is
// "no version" (IsZero) and is never produced by a successful ParseVersion.
type Version struct {
	raw   string
	parts [4]uint64 // year, month, day, n
}

// ParseVersion parses s against vYYYY.M.D[.N]. Anything else — no leading
// v, a semver tag, an ISO date, trailing garbage, surrounding whitespace —
// returns ErrInvalidVersion.
func ParseVersion(s string) (Version, error) {
	if !versionRe.MatchString(s) {
		return Version{}, fmt.Errorf("%w: %q must match vYYYY.M.D[.N]", ErrInvalidVersion, s)
	}
	v := Version{raw: s}
	for i, field := range strings.Split(s[1:], ".") {
		n, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			// Unreachable after the regex match except for a fourth
			// component too long for uint64; still a malformed version.
			return Version{}, fmt.Errorf("%w: %q component %d: %v", ErrInvalidVersion, s, i, err)
		}
		v.parts[i] = n
	}
	return v, nil
}

// String returns the raw version string as published (e.g. "v2026.8.22").
func (v Version) String() string { return v.raw }

// IsZero reports whether v is the zero Version (never parsed).
func (v Version) IsZero() bool { return v.raw == "" }

// Compare returns -1 if v < other, 0 if equal, +1 if v > other, comparing
// year, month, day and n numerically in that order.
func (v Version) Compare(other Version) int {
	for i := range v.parts {
		switch {
		case v.parts[i] < other.parts[i]:
			return -1
		case v.parts[i] > other.parts[i]:
			return 1
		}
	}
	return 0
}
