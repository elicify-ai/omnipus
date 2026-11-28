// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-102 (JSON-special token), S-103 (capture-cap
// fragment), S-104 (split across writes with a read between), S-130
// (interrupted inside a token, dataset D-9) — all through the B0 masking
// writer of invariant I-1, FR-020.
//
// Oracles from the SPEC (B0 "Primary, at the source"):
//   - protected fragments: every substring of 8 or more bytes of any token;
//   - every MAXIMAL run of bytes covered by protected fragments becomes ONE
//     "[FILTERED]" (whole token, token cut short, token tail, overlapping
//     repeats);
//   - hold-back: at most 7 bytes at stream end, only while they could still
//     start a protected fragment;
//   - Close (EOF, exit, kill, timeout, cancellation, read error) applies the
//     rule then releases the remaining ≤ 7 bytes as they are — seven bytes
//     are below the protection threshold, so a close can never release a
//     protected fragment.
//
// The sentinel is D-9's: sentinel_9f3c2a7e5b1d (L = 21 bytes).
//
// The per-stream, kill/timeout/read-error delivery halves of S-130 and the
// capture-cap instrumentation are NOT proved by this direct writer suite.
// Actual Attach -> cap is in sandbox; missing runtime paths remain individually
// loud in TestProgramMasking_RuntimeMissing, not inferred from these unit rows.

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const redSentinel = "sentinel_9f3c2a7e5b1d" // D-9: L = 21

// recordingSink records every byte the masking writer emits — the S-103
// instrument sink that sits BEFORE the capture cap.
type recordingSink struct{ b bytes.Buffer }

func (r *recordingSink) Write(p []byte) (int, error) { return r.b.Write(p) }

// recorded returns the first n bytes the sink received (the cap window).
func (r *recordingSink) recorded(n int) string {
	s := r.b.String()
	if len(s) > n {
		return s[:n]
	}
	return s
}

// all returns everything the sink ever received.
func (r *recordingSink) all() string { return r.b.String() }

// longestLeak returns the longest substring of at least 8 bytes of token that
// appears in out — the oracle for "no part of 8 or more characters appears".
func longestLeak(out, token string) string {
	best := ""
	for i := 0; i+8 <= len(token); i++ {
		for j := i + 8; j <= len(token); j++ {
			frag := token[i:j]
			if strings.Contains(out, frag) && len(frag) > len(best) {
				best = frag
			}
		}
	}
	return best
}

func requireNoLeak(t *testing.T, out, token string) {
	t.Helper()
	if leak := longestLeak(out, token); leak != "" {
		t.Fatalf("an 8-or-more-byte fragment of the token leaked: %q (output %q)", leak, out)
	}
}

func TestProgramMaskingWriter_Fragmentation(t *testing.T) {
	newMasked := func(tokens ...string) (*bytes.Buffer, io.WriteCloser) {
		buf := &bytes.Buffer{}
		return buf, NewMaskingWriter(buf, tokens)
	}

	t.Run("whole_token_becomes_one_filtered", func(t *testing.T) {
		// B0 rule: the whole token is one protected run → one [FILTERED].
		buf, w := newMasked(redSentinel)
		_, err := w.Write([]byte("before " + redSentinel + " after"))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		require.Equal(t, "before [FILTERED] after", buf.String(),
			"whole token → exactly one [FILTERED], neighbours untouched")
	})

	t.Run("repetition_and_two_runs", func(t *testing.T) {
		// D-4 row 4: two occurrences are both masked; adjacent copies are
		// ONE maximal covered run.
		buf, w := newMasked(redSentinel)
		_, err := w.Write([]byte(redSentinel + redSentinel))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		require.Equal(t, "[FILTERED]", buf.String(),
			"adjacent repeats form one maximal covered run → one [FILTERED]")

		buf2, w2 := newMasked(redSentinel)
		_, err = w2.Write([]byte("x " + redSentinel + " y " + redSentinel + " z"))
		require.NoError(t, err)
		require.NoError(t, w2.Close())
		require.Equal(t, "x [FILTERED] y [FILTERED] z", buf2.String(),
			"separated repeats → one [FILTERED] each")
	})

	t.Run("D9_row1_seven_bytes_then_close_may_appear_raw", func(t *testing.T) {
		// D-9 row 1: the first 7 bytes are below the 8-byte protection
		// threshold; they MAY appear, and no 8-or-more-byte fragment may.
		buf, w := newMasked(redSentinel)
		_, err := w.Write([]byte(redSentinel[:7]))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		out := buf.String()
		requireNoLeak(t, out, redSentinel)
		require.NotContains(t, out, "[FILTERED]",
			"7 bytes are below the threshold; masking them is allowed but not required")
	})

	t.Run("D9_row2_eight_bytes_then_close_is_filtered", func(t *testing.T) {
		// D-9 row 2: the first 8 bytes are a full protected fragment.
		buf, w := newMasked(redSentinel)
		_, err := w.Write([]byte(redSentinel[:8]))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		out := buf.String()
		require.Equal(t, "[FILTERED]", out, "8 covered bytes → [FILTERED] on close")
		requireNoLeak(t, out, redSentinel)
	})

	t.Run("D9_row8_middle_fragment_is_filtered", func(t *testing.T) {
		// D-9 row 8: bytes 5–14, a middle fragment, never a whole token.
		buf, w := newMasked(redSentinel)
		_, err := w.Write([]byte(redSentinel[5:15]))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		out := buf.String()
		require.Equal(t, "[FILTERED]", out, "a 10-byte middle fragment is protected")
		requireNoLeak(t, out, redSentinel)
	})

	t.Run("D9_row5_tail_is_filtered", func(t *testing.T) {
		// D-9 row 5: the last 20 bytes (L − 1), a token tail.
		buf, w := newMasked(redSentinel)
		_, err := w.Write([]byte(redSentinel[1:]))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		out := buf.String()
		require.Equal(t, "[FILTERED]", out, "a 20-byte token tail is one protected run")
		requireNoLeak(t, out, redSentinel)
	})

	t.Run("S104_timely_emission_through_a_pipe_while_open", func(t *testing.T) {
		testMaskingLivePipe(t)
	})

	t.Run("writer_unit_cap_window_records_no_fragment", func(t *testing.T) {
		// Helper-only cap window. Actual Attach -> recording sink -> real
		// cappedBuffer is TestProgramsAttach_ForegroundCaptureCap in sandbox.
		// This direct writer row alone is NOT the S-103 runtime instrument.
		sink := &recordingSink{}
		w := NewMaskingWriter(sink, []string{redSentinel})
		_, err := w.Write([]byte(strings.Repeat("a", 70) + redSentinel[:10]))
		require.NoError(t, err)
		// Every byte that entered the 80-byte capture cap window is clean.
		requireNoLeak(t, sink.recorded(80), redSentinel)
		_, err = w.Write([]byte(redSentinel[10:]))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		require.Contains(t, sink.all(), "[FILTERED]", "the completion masks the token")
		requireNoLeak(t, sink.all(), redSentinel)
	})

	t.Run("exhaustive_splits_and_prefix_overlap", func(t *testing.T) {
		// CHECK F13: every split point of the token across two writes, plus a
		// prefix-overlapping token pair (one token a prefix of the other).
		// For each split: no 8-or-more-byte fragment is ever readable, at
		// most 7 raw token bytes are emitted mid-stream, and the completion
		// shows [FILTERED].
		for i := 0; i <= len(redSentinel); i++ {
			buf, w := newMasked(redSentinel)
			_, err := w.Write([]byte(redSentinel[:i]))
			require.NoError(t, err)
			mid := buf.String()
			requireNoLeak(t, mid, redSentinel)
			require.LessOrEqual(t, len(mid), 7+len("[FILTERED]"),
				"split at %d: at most 7 raw bytes or one [FILTERED] are visible mid-stream", i)
			_, err = w.Write([]byte(redSentinel[i:]))
			require.NoError(t, err)
			require.NoError(t, w.Close())
			out := buf.String()
			require.Contains(t, out, "[FILTERED]", "split at %d: the completion masks the token", i)
			requireNoLeak(t, out, redSentinel)
		}
		// Prefix overlap: tokenB starts with tokenA; writing tokenB must mask
		// it whole and leak neither.
		tokenA := "tok_ab_123456"
		tokenB := tokenA + "XYZ"
		buf, w := newMasked(tokenA, tokenB)
		_, err := w.Write([]byte(tokenB))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		out := buf.String()
		require.Equal(t, "[FILTERED]", out, "a token that extends another is masked whole")
		requireNoLeak(t, out, tokenA)
		requireNoLeak(t, out, tokenB)
	})

	t.Run("S102_quote_and_backslash_token", func(t *testing.T) {
		// S-102: token `q"uo\te_tok_7731` is masked like any other; the
		// JSON-escaped spelling never materialises because the bytes never
		// reach the encoder unmasked.
		tok := "q\"uo\\te_tok_7731"
		require.GreaterOrEqual(t, len(tok), 8, "spec token is at least 8 bytes")
		buf, w := newMasked(tok)
		_, err := w.Write([]byte("tok=" + tok))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		out := buf.String()
		require.Equal(t, "tok=[FILTERED]", out)
		requireNoLeak(t, out, tok)
	})

	t.Run("retired_token_is_masked_too", func(t *testing.T) {
		// B0: protected fragments cover "any current or retired program
		// token" (FR-020).
		retired := "retired_tok_8899"
		current := redSentinel
		buf, w := newMasked(current, retired)
		_, err := w.Write([]byte(retired))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		out := buf.String()
		require.Equal(t, "[FILTERED]", out, "a retired token is masked")
		requireNoLeak(t, out, retired)
		requireNoLeak(t, out, current)
	})

	t.Run("innocent_output_passes_through", func(t *testing.T) {
		// B0 accepted-residual side: output that contains no protected
		// fragment is unchanged, across several writes and a close.
		buf, w := newMasked(redSentinel)
		_, err := w.Write([]byte("gh api user\n"))
		require.NoError(t, err)
		_, err = w.Write([]byte("octocat\nHTTP 200"))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		require.Equal(t, "gh api user\noctocat\nHTTP 200", buf.String(),
			"innocent output must pass through byte-identical (I-1 must not relabel)")
	})

	t.Run("multi_token_automaton_masks_either", func(t *testing.T) {
		// The writer matches over all 8-byte windows of ALL tokens: a short
		// token and a long token both mask, including back-to-back.
		short := "tok_valid_12345"
		buf, w := newMasked(short, redSentinel)
		_, err := w.Write([]byte(short + "|" + redSentinel))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		out := buf.String()
		require.Equal(t, "[FILTERED]|[FILTERED]", out,
			"two distinct tokens each become one [FILTERED]")
		requireNoLeak(t, out, short)
		requireNoLeak(t, out, redSentinel)
	})
}
