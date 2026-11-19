package omnpkg_test

// RED T-2 — TestNameSuffix (spec TDD plan order 13).
// Spec: FR-032 (name clash handling as in Dataset D6, decided at apply time);
// Edge cases: 100-character name that clashes → the base is shortened so the
// result is exactly 100 characters; non-Latin names kept, clash matching
// ignores letter case and surrounding spaces only; "Omnipus" is a clash;
// BDD "Name clash gets a suffix". D6 row 9 (a 101-character incoming name →
// element_not_creatable field: name) is a create rule and is asserted by the
// shared-create tests (T-3), not by the suffix resolver.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

func hundredAs(n int) string { return strings.Repeat("A", n) }

func TestNameSuffix(t *testing.T) {
	cases := []struct {
		name     string
		existing []string
		incoming string
		want     string
	}{
		{"D6-1 no clash keeps the name", nil, "Writer", "Writer"},
		{"D6-2 exact clash gets (imported)", []string{"Writer"}, "Writer", "Writer (imported)"},
		{"D6-3 clash ignores surrounding spaces and case", []string{"writer "}, "Writer", "Writer (imported)"},
		{"D6-4 second clash gets (imported 2)", []string{"Writer", "Writer (imported)"}, "Writer", "Writer (imported 2)"},
		{"D6-5 ninety-eighth clash gets (imported 99)",
			append([]string{"Writer", "Writer (imported)"},
				suffixedRange(2, 98)...), "Writer", "Writer (imported 99)"},
		{"D6-10 Omnipus is a clash", []string{"Omnipus"}, "omnipus", "omnipus (imported)"},
		{"D6-11 Mia clashes case-insensitively", []string{"Mia"}, "Mia", "Mia (imported)"},
		{"D6-12 non-Latin name gets the suffix", []string{"作家"}, "作家", "作家 (imported)"},
		{"D6-8 a 100-character name without clash stays 100", nil, hundredAs(100), hundredAs(100)},
		{"D6-7 a 100-character name that clashes shortens to exactly 100",
			[]string{hundredAs(100)}, hundredAs(100), hundredAs(89) + " (imported)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := omnpkg.ResolveImportName(tc.incoming, tc.existing)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.LessOrEqual(t, len([]rune(got)), omnpkg.MaxAgentNameLen, "the final name stays within 100 characters")
		})
	}

	t.Run("D6-7 the shortened name is exactly 100 characters", func(t *testing.T) {
		got, err := omnpkg.ResolveImportName(hundredAs(100), []string{hundredAs(100)})
		require.NoError(t, err)
		require.Len(t, got, 100, "BDD: the result is exactly 100 characters")
		require.True(t, strings.HasSuffix(got, " (imported)"))
	})

	t.Run("D6-6 suffixes exhausted refused as name_suffix_exhausted", func(t *testing.T) {
		existing := append([]string{"Writer", "Writer (imported)"}, suffixedRange(2, 99)...)
		_, err := omnpkg.ResolveImportName("Writer", existing)
		wantCode(t, err, omnpkg.CodeNameSuffixExhausted)
	})
}

// suffixedRange builds "Writer (imported N)" for N in [from, to].
func suffixedRange(from, to int) []string {
	out := make([]string, 0, to-from+1)
	for n := from; n <= to; n++ {
		out = append(out, "Writer (imported "+strconv.Itoa(n)+")")
	}
	return out
}
