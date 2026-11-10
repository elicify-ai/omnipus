package promptcache_test

// reasoning_cap_log_test.go — spec D7 row 4 (effort-levels-spec-r10.md,
// "D7: usage numbers", S-43): reasoning 181 against output 180 shows thinking
// capped at 180, and one log line notes the inconsistency. Rows 1-3 (reasoning
// at or below the output) log nothing.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/promptcache"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

const reasoningCapMessage = "Provider reported more reasoning tokens than output tokens; thinking capped at the output count"

// captureLog routes the file logger to a temporary file for one call and
// returns the lines it wrote.
func captureLog(t *testing.T, call func()) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	require.NoError(t, logger.EnableFileLogging(path))
	t.Cleanup(logger.DisableFileLogging)
	call()
	logger.DisableFileLogging()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func capLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, reasoningCapMessage) {
			out = append(out, l)
		}
	}
	return out
}

func TestReasoningOverReport_CappedWithOneLogLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"chat completions", `{"prompt_tokens":10,"completion_tokens":180,"completion_tokens_details":{"reasoning_tokens":181}}`},
		{"responses", `{"input_tokens":10,"output_tokens":180,"output_tokens_details":{"reasoning_tokens":181}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var u protocoltypes.UsageInfo
			lines := captureLog(t, func() {
				var err error
				u, err = promptcache.NormalizeUsage("openrouter", []byte(tc.raw))
				require.NoError(t, err)
			})
			require.NotNil(t, u.ReasoningTokens, "a reported count is present")
			assert.Equal(t, 180, *u.ReasoningTokens, "thinking capped at the output count (D7 row 4)")
			assert.Equal(t, 180, u.CompletionTokens, "output unchanged")
			got := capLines(lines)
			require.Len(t, got, 1, "exactly one log line notes the inconsistency (D7 row 4); log: %v", lines)
			for _, want := range []string{`"provider":"openrouter"`, `"reasoning_tokens":181`, `"output_tokens":180`} {
				assert.Contains(t, got[0], want)
			}
		})
	}
}

func TestReasoningWithinOutput_NoCapLogLine(t *testing.T) {
	for _, n := range []string{"0", "120", "180"} {
		raw := `{"prompt_tokens":10,"completion_tokens":180,"completion_tokens_details":{"reasoning_tokens":` + n + `}}`
		lines := captureLog(t, func() {
			_, err := promptcache.NormalizeUsage("openrouter", []byte(raw))
			require.NoError(t, err)
		})
		assert.Empty(t, capLines(lines), "reasoning %s within output 180 logs nothing (D7 rows 1-3)", n)
	}
}
