// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-105 (TestProgramAccountLabel — the check printed the
// token), FR-046 (the label rule).
//
// Oracle from FR-046: the account label is derived ONLY from the masked first
// non-empty stdout line, 1–128 printable characters. If that line contains
// [FILTERED], or is a substring of 8 or more characters of any current or
// retired token, the check ends check_failed with the label-carries-token
// message and NO label. S-105: neither the value nor [FILTERED] may be stored
// as a label.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramAccountLabel(t *testing.T) {
	t.Run("valid_first_non_empty_line_becomes_the_label", func(t *testing.T) {
		// S-01's shape: the check prints the login on a later line after
		// curl chatter; the first NON-empty line is the label.
		label, err := AccountLabel("github", "\n\noctocat\nHTTP 200", []string{"tok_valid_12345"})
		require.NoError(t, err)
		require.Equal(t, "octocat", label)
	})

	t.Run("S105_line_is_the_masked_marker", func(t *testing.T) {
		// S-105: custom Acme, check word printenv ACME_TOKEN — the check
		// printed the token itself; the masked line is [FILTERED].
		tok := "acme_tok_123456"
		label, err := AccountLabel("Acme", "[FILTERED]\n", []string{tok})
		require.Error(t, err, "a [FILTERED] line ends the check check_failed")
		require.Contains(t, err.Error(), "printed the token",
			"the label-carries-token message is the spec text")
		require.Contains(t, err.Error(), "Acme", "the message names the program")
		require.Empty(t, label, "no label may be stored")
		require.NotContains(t, err.Error(), tok, "the error must not quote the token")
	})

	t.Run("FR046_line_contains_an_8_byte_fragment_of_a_retired_token", func(t *testing.T) {
		// FR-046: a label that is a substring of 8 or more characters of any
		// current or RETIRED token is refused.
		retired := "retired_tok_8899"
		label, err := AccountLabel("github", retired, []string{"tok_valid_12345", retired})
		require.Error(t, err, "an 8-or-more-byte fragment of a retired token is refused")
		require.Empty(t, label, "no label may be stored")
	})

	t.Run("short_overlap_below_8_bytes_is_a_valid_label", func(t *testing.T) {
		// FR-046's threshold: fewer than 8 overlapping characters are NOT a
		// token fragment — "sentinel" (8 bytes) would be, 7 is not.
		tok := "sentinel_9f3c2a7e5b1d"
		label, err := AccountLabel("github", "sentine", []string{tok})
		require.NoError(t, err, "a 7-byte overlap is below the protection threshold")
		require.Equal(t, "sentine", label)
	})

	t.Run("FR046_label_length_bounds", func(t *testing.T) {
		// 1–128 printable characters.
		_, err := AccountLabel("github", strings.Repeat("a", 128), nil)
		require.NoError(t, err, "128 printable characters are accepted")
		_, err = AccountLabel("github", strings.Repeat("a", 129), nil)
		require.Error(t, err, "129 characters are refused")
		_, err = AccountLabel("github", "\t\n\x01", nil)
		require.Error(t, err, "non-printable characters are refused")
	})

	t.Run("empty_output_has_no_label", func(t *testing.T) {
		label, err := AccountLabel("github", "\n \n", nil)
		require.Error(t, err, "no non-empty line → no label (the check failed)")
		require.Empty(t, label)
	})
}
