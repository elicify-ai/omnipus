// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenarios S-02, S-09 (TestProgramToken_ValueRules), S-99 (the
// chat-card row of D-1) — FR-003. Every expected value and message is the
// SPEC's Machine-Verifiable Constraints table and dataset D-1 verbatim; the
// message strings are part of the contract.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// d1Rows is dataset D-1 [S1] (rows 1–9 and 13; rows 10 and 12 are REST
// faces of the save route and have their own rows there —
// TestProgramCredential_SaveCheckStates' D1_row10_body_over_the_cap_
// answered_413 and D1_row12_stale_expected_generation_conflict (CHECK F17
// added them after this comment wrongly claimed they were already covered);
// row 11's chat-card minimum is covered with the card tests).
var d1Rows = []struct {
	name    string
	input   string
	wantErr string // empty = accepted
	wantVal string // expected stored value when accepted
}{
	{name: "D1_row1_empty", input: "", wantErr: "Enter a token."},
	{name: "D1_row2_min_minus_1", input: "abc1234", wantErr: "The token is too short. A token has at least 8 characters."},
	{name: "D1_row3_min_8_bytes", input: "abcd1234", wantVal: "abcd1234"},
	{name: "min_plus_1_9_bytes", input: "abcd12345", wantVal: "abcd12345"},
	{name: "D1_row6_surrounding_whitespace_trimmed", input: "  tok_valid_12345\n", wantVal: "tok_valid_12345"},
	{name: "D1_row7_inner_break", input: "tok_a\ntok_b_123", wantErr: "The token contains a line break. Paste the token on one line."},
	{name: "D1_row8_nul", input: "tok\x00valid123", wantErr: "The token contains a character that cannot be stored."},
	{name: "D1_row9_utf8_bytes_counted", input: "tökén_ünïcode", wantVal: "tökén_ünïcode"},
	{name: "D1_row13_json_special_characters", input: "q\"uo\\te_tok_7731", wantVal: "q\"uo\\te_tok_7731"},
}

func TestProgramToken_ValueRules(t *testing.T) {
	for _, row := range d1Rows {
		t.Run(row.name, func(t *testing.T) {
			got, err := ValidateTokenValue(row.input)
			if row.wantErr != "" {
				require.Error(t, err, "input %q must be refused (D-1)", row.input)
				require.Contains(t, err.Error(), row.wantErr,
					"the refusal must carry the spec message")
				require.Empty(t, got, "a refused token must not return a value to store")
				return
			}
			require.NoError(t, err, "input %q must be accepted (D-1)", row.input)
			require.Equal(t, row.wantVal, got, "the stored value must be the spec's")
		})
	}

	t.Run("D1_row4_max_16384_bytes_accepted", func(t *testing.T) {
		// D-1 row 4: a token of exactly 16,384 bytes is saved.
		token := strings.Repeat("a", 16384)
		got, err := ValidateTokenValue(token)
		require.NoError(t, err)
		require.Equal(t, token, got)
	})

	t.Run("max_minus_1_16383_bytes_accepted", func(t *testing.T) {
		token := strings.Repeat("a", 16383)
		got, err := ValidateTokenValue(token)
		require.NoError(t, err)
		require.Equal(t, token, got)
	})

	t.Run("D1_row5_max_plus_1_refused", func(t *testing.T) {
		// D-1 row 5: 16,385 bytes is refused with the spec message.
		_, err := ValidateTokenValue(strings.Repeat("a", 16385))
		require.Error(t, err)
		require.Contains(t, err.Error(), "The token is too long. A token has at most 16,384 bytes.")
	})

	t.Run("S02_seven_byte_token_nothing_saved", func(t *testing.T) {
		// S-02: `abc1234` is refused; GitHub stays not_connected. The
		// validator returns an error (nothing to store); the REST row's
		// "nothing saved" half is covered by the gateway slice.
		_, err := ValidateTokenValue("abc1234")
		require.Error(t, err)
	})

	t.Run("S09_length_counts_bytes_not_characters", func(t *testing.T) {
		// D-1 row 9's rule: bytes counted. "tök_12345" is 7 runes but
		// 9 bytes — accepted. Four "ä" are 4 runes but 8 bytes — at the
		// minimum by bytes.
		got, err := ValidateTokenValue("tök_12345")
		require.NoError(t, err)
		require.Equal(t, "tök_12345", got)
		multibyte := strings.Repeat("ä", 4)
		got2, err2 := ValidateTokenValue(multibyte)
		require.NoError(t, err2, "8 BYTES are the minimum even in 4 characters (bytes counted)")
		require.Equal(t, multibyte, got2)
		sevenBytesMultibyte := strings.Repeat("ä", 3) + "x" // 7 bytes
		_, err3 := ValidateTokenValue(sevenBytesMultibyte)
		require.Error(t, err3, "7 bytes are refused even in 4 characters")
	})
}
