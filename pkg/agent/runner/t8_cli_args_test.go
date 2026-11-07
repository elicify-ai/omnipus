package runner

// Oracle: tool-parity r17 D-5 rows 1–4, 8–9. D-5 explicitly names the
// existing runtime filter as its drop oracle. These isolate that boundary,
// not the new update_agent result or Settings-preset integration.
import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCliArgs_D5_RuntimeFilterBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, cli, raw    string
		argv, kept, flags []string
	}{
		{"D5.1 unsafe sandbox", "codex", "--sandbox danger-full-access --model gpt-5", []string{"--sandbox", "danger-full-access", "--model", "gpt-5"}, []string{"--model", "gpt-5"}, []string{"--sandbox danger-full-access"}},
		{"D5.2 narrow sandbox", "codex", "--sandbox read-only", []string{"--sandbox", "read-only"}, []string{"--sandbox", "read-only"}, nil},
		{"D5.3 wrong output format", "claude-code", "--output-format json", []string{"--output-format", "json"}, []string{}, []string{"--output-format json"}},
		{"D5.4 quoted model", "claude-code", `--model "sonnet x"`, []string{"--model", "sonnet x"}, []string{"--model", "sonnet x"}, nil},
		{"D5.8 shell text is literal", "codex", "a; rm -rf /", []string{"a;", "rm", "-rf", "/"}, []string{"a;", "rm", "-rf", "/"}, nil},
		{"D5.9 result argv retains value", "codex", "--api-key sk-live-123456789", []string{"--api-key", "sk-live-123456789"}, []string{"--api-key", "sk-live-123456789"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := ParseCLIArgs(tc.raw, "t8-d5")
			require.Equal(t, tc.argv, argv, "literal argv, not shell execution")
			kept, dropped := FilterDangerousCLIArgsDetailed(FilterKey(tc.cli), argv)
			assert.Equal(t, tc.kept, kept, "D-5 exact filtered tail")
			var flags []string
			for _, d := range dropped {
				flags = append(flags, d.Flag)
				assert.NotEmpty(t, d.Reason, "the specified dropped-at-run reason is required")
			}
			assert.Equal(t, tc.flags, flags)
			assert.Equal(t, tc.argv, argv, "filter cannot mutate parsed input")
		})
	}
}
