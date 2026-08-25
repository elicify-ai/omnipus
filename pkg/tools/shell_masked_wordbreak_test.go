// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Boundary coverage for the ONE decision the word-scan loops in
// pathUseClassifier make that nothing else in the suite reaches: a shell
// word-break character that is QUOTED (masked) sitting next to one that is not.
//
// Both loops are guarded by the same two-term condition — "this byte is masked,
// OR it is not a word break" — and the interesting row of that condition is
// masked AND a word break, i.e. the quoted space in `cat "a b"/etc/passwd`.
// The existing read/write-guard tests never reach it: the only quoted word
// break they carry (`sh -c "cat /etc/passwd"`) is rejected at the head check
// (`sh` is not on readOnlyShellCommands), which returns before wordStart is
// ever called.
//
// Oracle: POSIX word splitting, as restated in wordStart's own contract — a
// quoted word-break character is DATA, not a boundary, which is what keeps
// `cat "/etc/my file"` a single argument. Expected offsets below are counted
// off the command strings by hand against that rule, never read off the
// classifier.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPathUseClassifier_WordStart_QuotedWordBreakDoesNotEndWord exercises all
// four combinations of (masked, is-word-break) that the backward word scan can
// encounter, plus the segStart floor.
func TestPathUseClassifier_WordStart_QuotedWordBreakDoesNotEndWord(t *testing.T) {
	cases := []struct {
		name     string
		cmd      string
		idx      int
		segStart int
		want     int
		why      string
	}{
		{
			// `cat ab/etc/passwd`
			//  0123456
			// Scanning back from 6: 'b' and 'a' are ordinary (not masked, not
			// breaks) so the scan continues; the space at 3 is an unquoted
			// break and stops it.
			name:     "unquoted break ends the word",
			cmd:      `cat ab/etc/passwd`,
			idx:      6,
			segStart: 0,
			want:     4,
			why:      "rows (unmasked, not-break) and (unmasked, break)",
		},
		{
			// `cat "a b"/etc/passwd`
			//  0123456789
			// Byte 6 is a space INSIDE double quotes. It is a word-break
			// character and it is masked, so it must not end the word: the
			// whole quoted token belongs to the same argument as the path.
			// Only the unquoted space at 3 ends it.
			name:     "quoted break does not end the word",
			cmd:      `cat "a b"/etc/passwd`,
			idx:      9,
			segStart: 0,
			want:     4,
			why:      "row (masked, break) — the decisive one; also (masked, not-break) on the quotes",
		},
		{
			// `echo hi;cat "a b"/etc/passwd`
			//  0123456789...
			// Same shape, but inside the second segment. The quoted space at
			// 14 must not end the word, and the unquoted space at 11 must.
			name:     "quoted break does not end the word, mid-command segment",
			cmd:      `echo hi;cat "a b"/etc/passwd`,
			idx:      17,
			segStart: 8,
			want:     12,
			why:      "the same decisive row with a non-zero segment start",
		},
		{
			name:     "scan never runs below segStart",
			cmd:      `cat /etc/passwd`,
			idx:      4,
			segStart: 4,
			want:     4,
			why:      "the floor is the segment start, even mid-word",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newPathUseClassifier(tc.cmd)
			require.True(t, c.classifiable, "fixture command must be tokenisable: %s", tc.cmd)
			got := c.wordStart(tc.idx, tc.segStart)
			require.Equal(t, tc.want, got,
				"wordStart(%d, %d) over %q (%s)", tc.idx, tc.segStart, tc.cmd, tc.why)
		})
	}
}

// TestGuardCommand_QuotedRedirectTargetIsReadWhole pins the same decision in
// the OTHER loop that makes it — the redirect-target token scan behind rule 6.
//
// A redirect target is only allowed to sit beside a granted read when the scan
// can see the whole target literally. If a quoted space were treated as a token
// boundary, `> "out $X.txt"` would be read as the target `"out`, which contains
// no expansion character, and the guard would hand out a read exemption next to
// a write it cannot locate — the exact inference ADR-068 §5 forbids.
func TestGuardCommand_QuotedRedirectTargetIsReadWhole(t *testing.T) {
	tool, cwd := guardFixture(t)

	t.Run("expansion in the second half of a quoted target blocks", func(t *testing.T) {
		cmd := `cat /etc/hosts > "out $X.txt"`
		got := tool.guardCommand(context.Background(), cmd, cwd)
		require.NotEmpty(t, got,
			"the write target is hidden behind $X, so no read exemption may be granted beside it: %s", cmd)
		require.Contains(t, got, "path outside working dir",
			"must be refused as an unproven outside path, not for some unrelated reason")
	})

	// Control: the identical shape with a fully literal quoted target stays
	// allowed. Without this, the assertion above would also pass if the guard
	// simply blocked every quoted redirect target.
	t.Run("fully literal quoted target still allows the read", func(t *testing.T) {
		cmd := `cat /etc/hosts > "out file.txt"`
		got := tool.guardCommand(context.Background(), cmd, cwd)
		require.Empty(t, got,
			"the target is literal and inside the working dir, so the read of /etc/hosts is provable: %s", cmd)
	})
}
