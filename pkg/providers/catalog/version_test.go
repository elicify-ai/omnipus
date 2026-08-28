package catalog

// Version tests — FR-002's version rule (vYYYY.M.D[.N], leading v
// mandatory, compared numerically) and the DS-7 ordering outline.
//
// Traces to: US-3.AC6 (version ordering across boundaries), F-01, A-6.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVersion_DateSemver is T7: the DS-7 outline. Rows 1–4 cross a day,
// month, year and patch boundary and must order numerically (a lexical
// compare gets rows 1–3 wrong); row 5 is the regression direction; row 6
// is the no-`v` rejection.
func TestVersion_DateSemver(t *testing.T) {
	rows := []struct {
		current string
		pulled  string
		expect  string // applied | regressed | rejected
	}{
		{"v2026.8.9", "v2026.8.10", "applied"},
		{"v2026.9.30", "v2026.10.1", "applied"},
		{"v2026.12.31", "v2027.1.1", "applied"},
		{"v2026.8.22", "v2026.8.22.1", "applied"},
		{"v2026.8.10", "v2026.8.9", "regressed"},
		{"v2026.8.22", "2026.8.23", "rejected"},
	}
	for _, row := range rows {
		t.Run(row.current+"->"+row.pulled, func(t *testing.T) {
			cur, err := ParseVersion(row.current)
			require.NoError(t, err)
			pulled, err := ParseVersion(row.pulled)
			if row.expect == "rejected" {
				require.Error(t, err, "a version without the leading v must be rejected")
				assert.ErrorIs(t, err, ErrInvalidVersion)
				return
			}
			require.NoError(t, err)
			switch row.expect {
			case "applied":
				assert.Equal(t, 1, pulled.Compare(cur), "pulled must sort after current")
				assert.Equal(t, -1, cur.Compare(pulled))
			case "regressed":
				assert.Equal(t, -1, pulled.Compare(cur), "pulled must sort before current")
				assert.Equal(t, 1, cur.Compare(pulled))
			}
		})
	}
}

// TestVersion_Equal_And_String: the optional .N is a plain fourth numeric
// component with absent == 0, so "v2026.8.22" and "v2026.8.22.0" compare
// equal (DS-4 row 8 — an equal pull is a permitted no-op, never a
// regression). String() round-trips the raw input; the zero Version is
// distinguishable from any parsed one.
func TestVersion_Equal_And_String(t *testing.T) {
	a, err := ParseVersion("v2026.8.22")
	require.NoError(t, err)
	b, err := ParseVersion("v2026.8.22.0")
	require.NoError(t, err)
	assert.Equal(t, 0, a.Compare(b))
	assert.Equal(t, 0, b.Compare(a))
	assert.Equal(t, "v2026.8.22", a.String())
	assert.Equal(t, "v2026.8.22.0", b.String())
	assert.False(t, a.IsZero())
	assert.True(t, Version{}.IsZero())
}

// TestVersion_RejectsMalformed pins the regex edges of FR-002: the year is
// exactly four digits, month and day one or two, the suffix digits only;
// no semver prerelease, no trailing garbage, no leading zeros padding the
// year to five digits, no empty string.
func TestVersion_RejectsMalformed(t *testing.T) {
	for _, s := range []string{
		"",
		"v",
		"v2026",
		"v2026.8",
		"v2026.8.",
		"v2026.08.22-rc1",
		"v2026.8.22.x",
		"v20261.8.22",
		"v2026.123.1",
		"V2026.8.22",
		"2026-08-22",
		"v1.2.3",
		" v2026.8.22",
	} {
		_, err := ParseVersion(s)
		assert.ErrorIs(t, err, ErrInvalidVersion, "input %q", s)
	}
}
