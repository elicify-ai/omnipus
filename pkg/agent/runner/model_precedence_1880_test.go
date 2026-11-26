package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1880, founder 2026-11-18 Q2=B: Omnipus's configured model wins over
// Additional CLI arguments. This supersedes the old last-option-wins rule;
// Settings PresetArgs are a different surface and intentionally not tested here.
// Claude --help documents --model only. Codex --help and opencode run --help
// document -m as well. No actual CLI/model invocation is needed by these tests.
func TestOpenCodeFix1880_AdditionalModelOptionsCannotOverrideOmnipus(t *testing.T) {
	for _, driver := range []struct {
		name       string
		model      string
		build      func(RunOptions) []string
		shortModel bool
	}{
		{name: "opencode", model: "openrouter/deepseek/deepseek-v4.1-flash", build: BuildOpencodeArgs, shortModel: true},
		{name: "claude", model: "sonnet", build: BuildClaudeArgs},
		{name: "codex", model: "gpt-5-codex", build: BuildCodexArgs, shortModel: true},
	} {
		t.Run(driver.name, func(t *testing.T) {
			cases := []struct {
				name string
				args []string
			}{
				{name: "long_separate", args: []string{"--model", "person/model-one"}},
				{name: "long_equals", args: []string{"--model=person/model-two"}},
				{name: "repeated", args: []string{"--model", "person/model-one", "--model=person/model-two", "--model", "person/model-three"}},
			}
			if driver.shortModel {
				cases = append(cases, struct {
					name string
					args []string
				}{name: "short_separate", args: []string{"-m", "person/model-one"}}, struct {
					name string
					args []string
				}{name: "mixed_aliases", args: []string{"-m", "person/model-one", "--model=person/model-two", "--model", "person/model-three"}})
			}
			for _, nativeID := range []string{"", "original-native-conversation-1880"} {
				phase := "initial_run"
				if nativeID != "" {
					phase = "same_conversation_resume"
				}
				t.Run(phase, func(t *testing.T) {
					for _, tc := range cases {
						t.Run(tc.name, func(t *testing.T) {
							extra := append([]string{"--fixture-before", "kept-before"}, tc.args...)
							extra = append(extra, "--fixture-after", "kept-after")
							opts := RunOptions{
								Model: driver.model, MaxTurns: 7, CLIArgs: extra,
								Input: "--model this is task text, not a model option", resumeNativeID: nativeID,
							}
							argv := driver.build(opts)
							assert.Equal(t, []string{driver.model}, opencodeFix1880ModelValues(t, argv),
								"Q2=B: exactly ONE model option must remain, carrying Omnipus's value; argv=%q", argv)
							// A model-option remover must consume each associated value,
							// not leave it behind as a stray positional prompt.
							for _, arg := range argv {
								assert.False(t, strings.Contains(arg, "person/model-"), "remove the person's option AND its value; argv=%q", argv)
							}
							start := slices.Index(argv, "--fixture-before")
							require.NotEqual(t, -1, start, "unrelated arguments must not be erased")
							require.GreaterOrEqual(t, len(argv)-start, 4)
							assert.Equal(t, []string{"--fixture-before", "kept-before", "--fixture-after", "kept-after"}, argv[start:start+4],
								"preserve the exact ordered unrelated arguments around removed model options; argv=%q", argv)
							if driver.name == "opencode" {
								require.GreaterOrEqual(t, len(argv), 2)
								assert.Equal(t, []string{"--", opts.Input}, argv[len(argv)-2:], "task text is not a CLI model option")
							}
						})
					}
				})
			}
		})
	}
}

// Read only actual options, never task text after the end-of-options marker.
// This independent observer normalises the three spellings explicitly listed
// by Q2; it does not call the production argument filter to derive an oracle.
func opencodeFix1880ModelValues(t *testing.T, argv []string) []string {
	t.Helper()
	var values []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			break
		}
		switch {
		case arg == "--model" || arg == "-m":
			require.Less(t, i+1, len(argv), "a model option cannot be left without its value; argv=%q", argv)
			i++
			values = append(values, argv[i])
		case strings.HasPrefix(arg, "--model="):
			values = append(values, strings.TrimPrefix(arg, "--model="))
		}
	}
	return values
}

// PIN: Q2 must not erase an ordinary configured model or unrelated options.
// These explicit argv baselines pin the already-shipped, unrelated driver flag
// layout; they are characterization controls, not RED proof of the new rule.
func TestOpenCodeFix1880_NoOverridePreservesDriverCommandControl(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(RunOptions) []string
		want  []string
	}{
		{name: "opencode", build: BuildOpencodeArgs, want: []string{
			"run", "--format", "json", "--model", "provider/omnipus-model", "--dangerously-skip-permissions", "--fixture-option", "kept", "--", "task",
		}},
		{name: "claude", build: BuildClaudeArgs, want: []string{
			"-p", "--output-format", "stream-json", "--verbose", "--no-chrome", "--model", "provider/omnipus-model", "--dangerously-skip-permissions", "--max-turns", "7", "--fixture-option", "kept",
		}},
		{name: "codex", build: BuildCodexArgs, want: []string{
			"--ask-for-approval", "never", "exec", "--json", "--sandbox", "workspace-write", "--skip-git-repo-check", "--color", "never", "-m", "provider/omnipus-model", "--fixture-option", "kept", "-",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := tc.build(RunOptions{Model: "provider/omnipus-model", MaxTurns: 7, Input: "task", CLIArgs: []string{"--fixture-option", "kept"}})
			assert.Equal(t, tc.want, argv)
		})
	}
}
