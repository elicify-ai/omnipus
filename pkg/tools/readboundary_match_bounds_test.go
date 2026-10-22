package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var rbDS5MatchHeader = regexp.MustCompile(`(?m)^([0-9]+) match\(es\)`)

// D7/FR-011, DS-5 row5: every admitted search keeps 1000 total/50 per-file.
// The authoritative header survives the output cap. Visible per-file rows are
// also checked; the real controlled single-file probe below checks the actual
// per-file cap independently of a volume walk's potentially clipped output.
func rbDS5MatchBoundError(out string, matchLimitFirst bool) error {
	header := rbDS5MatchHeader.FindStringSubmatch(out)
	if len(header) != 2 {
		return fmt.Errorf("DS-5: required authoritative match-count header is missing")
	}
	total, err := strconv.Atoi(header[1])
	if err != nil {
		return fmt.Errorf("DS-5: invalid match count: %w", err)
	}
	if total > 1000 || (matchLimitFirst && total != 1000) {
		return fmt.Errorf("DS-5: match count %d violates total1000 or truthful max_matches", total)
	}
	perFile := map[string]int{}
	visible := 0
	for _, line := range strings.Split(out, "\n") {
		path := ""
		if strings.HasSuffix(line, "  (name match)") {
			path = strings.TrimSuffix(line, "  (name match)")
		} else if hit := rbContentHitLine.FindStringSubmatch(line); hit != nil {
			path = hit[1]
		}
		if path != "" {
			visible++
			perFile[path]++
			if perFile[path] > 50 {
				return fmt.Errorf("DS-5: one file exceeds the unchanged50-hit cap")
			}
		}
	}
	if visible > total {
		return fmt.Errorf("DS-5: visible hits exceed the authoritative count")
	}
	return nil
}

func TestReadBoundary_DS5MatchCountInstrument(t *testing.T) {
	// Unit inputs for the assertion instrument, never fake product proof.
	valid := "1000 match(es) — truncated: true (reason: max_matches)\n/x:1:  needle\n"
	require.NoError(t, rbDS5MatchBoundError(valid, true))
	require.NoError(t, rbDS5MatchBoundError("0 match(es) — truncated: true (reason: deadline)\n", false))
	for _, tc := range []struct{ name, input, want string }{
		{"missing_header", "truncated: true (reason: max_matches)", "required authoritative match-count header is missing"},
		{"total_over1000", strings.Replace(valid, "1000 match(es)", "1001 match(es)", 1), "match count 1001 violates total1000"},
		{"false_max_matches", strings.Replace(valid, "1000 match(es)", "999 match(es)", 1), "match count 999 violates total1000"},
		{"per_file_over50", "1000 match(es)\n" + strings.Repeat("/x:1:  needle\n", 51), "one file exceeds the unchanged50-hit cap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, rbDS5MatchBoundError(tc.input, true), tc.want)
		})
	}
}

func TestReadBoundary_DS5MatchCaps(t *testing.T) {
	f := newRBFixture(t)
	dir := filepath.Join(f.ws, "match-caps")
	for i := 0; i < 21; i++ {
		rbWrite(t, filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), strings.Repeat("DS5_BOUND\n", 51))
	}
	// Each file contains51 true hits: exactly50 must survive the unchanged
	// per-file cap. Twenty-one such files exceed the unchanged1000 total cap.
	file := filepath.Join(dir, "f00.txt")
	single := f.grepCall("DS5_BOUND", rbStr(file))
	require.False(t, single.IsError, single.ForLLM)
	require.Regexp(t, `(?m)^50 match\(es\)`, single.ForLLM)
	require.NoError(t, rbDS5MatchBoundError(single.ForLLM, false))
	many := f.grepCall("DS5_BOUND", rbStr(dir))
	require.False(t, many.IsError, many.ForLLM)
	require.Equal(t, "max_matches", rbTruncation(many.ForLLM), "real controlled data reaches the DS-5 total bound first")
	require.NoError(t, rbDS5MatchBoundError(many.ForLLM, true))
}
