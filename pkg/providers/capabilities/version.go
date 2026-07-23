package capabilities

// version semantics — semver-aware comparator for catalog version strings.
//
// The catalog version is an opaque trackable identifier used to gate
// Refresh-time regression checks (a pulled catalog with a version below
// the current version is rejected, last-known-good retained). The
// version string MAY be a semver tag (e.g. "v10.0.0") or a date-based
// tracking string (e.g. "2026-07-23"); the embedded seed uses the
// latter.
//
// Prior code compared version strings lexically, which produced a real
// bug — "v10" sorted BELOW "v2" because '1' < '2' at the second
// character. Version normalizes this for semver-parseable strings
// (numeric major/minor/patch comparison) while falling back to lexical
// comparison for non-semver strings. The lexical fallback is correct
// for ISO-date strings ("2026-07-22" < "2026-07-23" both numerically
// and lexically).

import (
	"strconv"
	"strings"
)

// Version is a parsed version string. Semver-parseable strings
// (matching `vMAJOR[.MINOR[.PATCH[-PRERELEASE]]]`) compare numerically
// across major/minor/patch and then by prerelease tag; non-semver
// strings fall back to lexical comparison of the raw string.
//
// The two-way split is intentional:
//   - semver compare is the bug fix (v10 > v2).
//   - lexical fallback preserves the chronological invariant for
//     date-based tracking strings (which the embedded seed uses), and
//     keeps the existing test that asserts v1-older < v2-current
//     working unchanged.
type Version struct {
	raw        string
	major      uint64
	minor      uint64
	patch      uint64
	prerelease string
	isSemver   bool
}

// ParseVersion parses a version string into a Version. Never returns
// an error today — the error return is reserved for future strict-mode
// validation. A Version is always returned (even on garbage input) so
// callers can compare without a separate "valid?" check.
func ParseVersion(s string) (Version, error) {
	v := Version{raw: s}
	if major, rest, ok := parseSemverPrefix(s); ok {
		if minor, patch, pre, ok := parseSemverRest(rest); ok {
			v.major = major
			v.minor = minor
			v.patch = patch
			v.prerelease = pre
			v.isSemver = true
		}
	}
	return v, nil
}

// String returns the raw version string.
func (v Version) String() string { return v.raw }

// IsSemver reports whether the version parsed as semver.
func (v Version) IsSemver() bool { return v.isSemver }

// Compare returns -1 if v < other, 0 if equal, +1 if v > other.
// Two semver-parseable versions compare numerically; any other pair
// compares lexically by raw string.
func (v Version) Compare(other Version) int {
	if v.isSemver && other.isSemver {
		if c := cmpUint64(v.major, other.major); c != 0 {
			return c
		}
		if c := cmpUint64(v.minor, other.minor); c != 0 {
			return c
		}
		if c := cmpUint64(v.patch, other.patch); c != 0 {
			return c
		}
		// No prerelease sorts after any prerelease (semver §11).
		if v.prerelease == other.prerelease {
			return 0
		}
		if v.prerelease == "" {
			return 1
		}
		if other.prerelease == "" {
			return -1
		}
		return strings.Compare(v.prerelease, other.prerelease)
	}
	return strings.Compare(v.raw, other.raw)
}

// parseSemverPrefix parses the leading "v<MAJOR>" component. Returns
// (major, rest, ok). When ok is false, raw is returned untouched.
func parseSemverPrefix(s string) (uint64, string, bool) {
	if !strings.HasPrefix(s, "v") {
		return 0, s, false
	}
	rest := s[1:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, s, false
	}
	major, err := strconv.ParseUint(rest[:end], 10, 64)
	if err != nil {
		return 0, s, false
	}
	return major, rest[end:], true
}

// parseSemverRest parses ".MINOR[.PATCH[-PRERELEASE]]". Returns
// (minor, patch, prerelease, ok). ok=false means the rest is not a
// valid semver tail — the caller should NOT mark the version as semver.
//
// Accepted forms:
//   - ""              -> v1 (MAJOR.0.0 equivalent)
//   - ".MINOR"        -> v1.2 (MAJOR.MINOR.0 equivalent)
//   - ".MINOR.PATCH"  -> v1.2.3
//   - ".MINOR.PATCH-PRE" -> v1.2.3-rc1
//
// The prerelease suffix is taken verbatim after the single leading
// dash; semver §11's identifier-by-identifier ordering is reduced to
// lexical here for simplicity (the catalog's prerelease tags are
// operator release tags, not in-the-wild semver traffic).
func parseSemverRest(s string) (uint64, uint64, string, bool) {
	if s == "" {
		return 0, 0, "", true
	}
	if !strings.HasPrefix(s, ".") {
		return 0, 0, "", false
	}
	rest := s[1:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, 0, "", false
	}
	minor, err := strconv.ParseUint(rest[:end], 10, 64)
	if err != nil {
		return 0, 0, "", false
	}
	rest = rest[end:]
	if rest == "" {
		return minor, 0, "", true
	}
	if !strings.HasPrefix(rest, ".") {
		return 0, 0, "", false
	}
	rest = rest[1:]
	end = 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, 0, "", false
	}
	patch, err := strconv.ParseUint(rest[:end], 10, 64)
	if err != nil {
		return 0, 0, "", false
	}
	rest = rest[end:]
	if rest == "" {
		return minor, patch, "", true
	}
	if !strings.HasPrefix(rest, "-") {
		return 0, 0, "", false
	}
	pre := rest[1:]
	if pre == "" {
		return 0, 0, "", false
	}
	return minor, patch, pre, true
}

// cmpUint64 returns -1/0/+1 for a<b/a==b/a>b.
func cmpUint64(a, b uint64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
