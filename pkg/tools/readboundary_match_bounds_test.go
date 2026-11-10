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

// D7/FR-011 keeps1000 total hits and50 content rows per file. MV-14 filename
// hits are admitted separately but still consume the total. The authoritative
// header survives output clipping; the real probes also check actual content
// caps independently of a volume walk's potentially clipped visible rows.
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
		content := false
		if strings.HasSuffix(line, "  (name match)") {
			path = strings.TrimSuffix(line, "  (name match)")
		} else if hit := rbContentHitLine.FindStringSubmatch(line); hit != nil {
			path = hit[1]
			content = true
		}
		if path != "" {
			visible++ // both kinds consume the total match budget
			if content {
				perFile[path]++
				if perFile[path] > 50 {
					return fmt.Errorf("DS-5: one file exceeds the unchanged50-hit cap")
				}
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
	require.NoError(t, rbDS5MatchBoundError("51 match(es)\n/x  (name match)\n"+strings.Repeat("/x:1:  needle\n", 50), false))
	for _, tc := range []struct {
		name, input, want string
		matchLimitFirst   bool
	}{
		{"missing_header", "truncated: true (reason: max_matches)", "required authoritative match-count header is missing", true},
		{"total_over1000", strings.Replace(valid, "1000 match(es)", "1001 match(es)", 1), "match count 1001 violates total1000", true},
		{"false_max_matches", strings.Replace(valid, "1000 match(es)", "999 match(es)", 1), "match count 999 violates total1000", true},
		{"per_file_over50", "1000 match(es)\n" + strings.Repeat("/x:1:  needle\n", 51), "one file exceeds the unchanged50-hit cap", true},
		{"filename_and_content_share_total", "1 match(es)\n/x  (name match)\n/x:1:  needle\n", "visible hits exceed the authoritative count", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, rbDS5MatchBoundError(tc.input, tc.matchLimitFirst), tc.want)
		})
	}
}

// MV-14 and the unchanged scanFile cap admit one filename hit PLUS50 content
// rows. Only content rows consume the per-file50; all51 consume the total cap.
func TestReadBoundary_DS5FilenameAndContentAreCountedSeparately(t *testing.T) {
	f := newRBFixture(t)
	dir := filepath.Join(f.ws, "dual-kind")
	rbWrite(t, filepath.Join(dir, "needle_dual.txt"), strings.Repeat("needle_dual\n", 50))
	result := f.grepCall("needle_dual", rbStr(dir))
	require.False(t, result.IsError, result.ForLLM)
	require.Regexp(t, `(?m)^51 match\(es\)`, result.ForLLM, "one filename plus50 content lines")
	var names, contents int
	for _, line := range strings.Split(result.ForLLM, "\n") {
		if strings.HasSuffix(line, "  (name match)") {
			names++
		} else if rbContentHitLine.MatchString(line) {
			contents++
		}
	}
	require.Equal(t, 1, names, "positive control: actual filename hit is present")
	require.Equal(t, 50, contents, "positive control: all50 actual content rows are visible")
	require.NoError(t, rbDS5MatchBoundError(result.ForLLM, false), "filename hit must not false-fail the content-only per-file cap")
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
