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

	t.Run("FR046_token_fragments_7_8_9_of_current_and_retired", func(t *testing.T) {
		// FR-046 (CHECK F14): PROPER substrings around the threshold, of a
		// current AND a retired token — 7 bytes is a valid label, 8 and 9
		// bytes are refused ("a substring of 8 or more characters of any
		// current or retired token").
		current := "tok_valid_12345"
		retired := "retired_tok_8899"
		for name, tok := range map[string]string{"current": current, "retired": retired} {
			frag7, frag8, frag9 := tok[:7], tok[:8], tok[:9]
			label, err := AccountLabel("github", frag7, []string{current, retired})
			require.NoError(t, err, "%s token: a 7-byte fragment (%q) is below the threshold", name, frag7)
			require.Equal(t, frag7, label, "%s token: the 7-byte fragment is the label", name)
			for _, frag := range []string{frag8, frag9} {
				fragmentLabel, fragmentErr := AccountLabel("github", frag, []string{current, retired})
				require.Error(t, fragmentErr, "%s token: an %d-byte fragment (%q) is refused", name, len(frag), frag)
				require.Empty(t, fragmentLabel, "%s token: no label is stored for %q", name, frag)
				require.NotContains(t, fragmentErr.Error(), tok, "%s token: the error never quotes the token", name)
			}
			// A fragment from the token's MIDDLE, not just its head.
			mid := tok[4:13]
			_, err = AccountLabel("github", mid, []string{current, retired})
			require.Error(t, err, "%s token: a middle fragment (%q) is refused", name, mid)
		}
	})

	t.Run("FR046_embedded_marker_and_fragment_in_longer_line", func(t *testing.T) {
		// The [FILTERED] marker EMBEDDED in a longer line refuses the label;
		// a token fragment of THRESHOLD OR MORE bytes embedded in innocent
		// text refuses too; the 7-byte neighbour embedded the same way is a
		// valid label (CHECK R12: the earlier case fed a 7-byte substring
		// ("tok_val") and called it an 8-byte refusal — a correct FR-046
		// would have failed that assertion).
		current := "tok_valid_12345"
		_, err := AccountLabel("github", "octocat [FILTERED] rest", nil)
		require.Error(t, err, "a line containing [FILTERED] is refused")
		frag8 := current[:8] // "tok_vali" — exactly 8 bytes, at the threshold
		require.Len(t, frag8, 8, "instrument: the refusal input is a real 8-byte fragment")
		label, err := AccountLabel("github", "user "+frag8+" done", []string{current})
		require.Error(t, err, "a line embedding an 8-byte fragment (%q) is refused", frag8)
		require.Empty(t, label, "no label is stored")
		frag9 := current[:9] // "tok_valid" — 9 bytes, above the threshold
		_, err = AccountLabel("github", "user "+frag9+" done", []string{current})
		require.Error(t, err, "a line embedding a 9-byte fragment (%q) is refused", frag9)
		// Acceptance twin (CHECK R12): the 7-byte fragment embedded in the
		// same innocent line is BELOW the threshold — a valid label.
		label, err = AccountLabel("github", "user tok_val done", []string{current})
		require.NoError(t, err, "a line embedding only a 7-byte fragment is below the threshold")
		require.Equal(t, "user tok_val done", label, "the whole printable line is the label")
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
		// 1–128 printable characters (CHECK F14: the full boundary ladder
		// 0, 1, 127, 128, 129).
		_, err := AccountLabel("github", "", nil)
		require.Error(t, err, "an empty first non-empty line cannot happen — an empty line is skipped, so a lone empty output has no label")
		_, err = AccountLabel("github", "a", nil)
		require.NoError(t, err, "1 printable character is accepted")
		_, err = AccountLabel("github", strings.Repeat("a", 127), nil)
		require.NoError(t, err, "127 printable characters are accepted")
		_, err = AccountLabel("github", strings.Repeat("a", 128), nil)
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
