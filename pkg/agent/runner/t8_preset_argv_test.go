package runner

// Oracle: tool-parity r17 rows 9.17, 9.18 and 9.19 at the driver boundary.
// The person's Settings preset is appended unfiltered and after the agent
// arguments; the agent arguments still go through the dangerous-flag filter.
// The pkg/agent tests prove the preset reaches RunOptions.PresetArgs from the
// real Settings row; this proves what the driver puts on argv.

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const t8BypassFlag = "--dangerously-bypass-approvals-and-sandbox"

func t8CountOf(argv []string, want string) int {
	n := 0
	for _, a := range argv {
		if a == want {
			n++
		}
	}
	return n
}

// 9.17: codex argv is driver flags, agent flags without the bypass, the preset
// with it, then the stdin hand-off.
func TestSpawn_ArgvOrderPresetUnfiltered_Codex(t *testing.T) {
	agentArgs := ParseCLIArgs(t8BypassFlag+" --profile agent", "t8-9-17")
	preset := ParseCLIArgs(t8BypassFlag, "t8-9-17")
	argv := NewCodexDriver(nil).buildArgs(RunOptions{Input: "task", CLIArgs: agentArgs, PresetArgs: preset})

	driverAt := slices.Index(argv, "workspace-write")
	agentAt := slices.Index(argv, "--profile")
	presetAt := slices.Index(argv, t8BypassFlag)
	require.GreaterOrEqual(t, driverAt, 0, "driver flag present: %v", argv)
	require.GreaterOrEqual(t, agentAt, 0, "agent flag present: %v", argv)
	require.GreaterOrEqual(t, presetAt, 0, "preset flag present: %v", argv)
	assert.Less(t, driverAt, agentAt, "driver flags come first")
	assert.Less(t, agentAt, presetAt, "agent flags come before the preset")
	assert.Equal(t, 1, t8CountOf(argv, t8BypassFlag), "the agent copy of the bypass flag is dropped, the preset copy kept")
	assert.Equal(t, "-", argv[len(argv)-1], "the stdin hand-off is last")
	assert.Equal(t, len(argv)-2, presetAt, "the preset is the last thing before the hand-off")
}

// 9.18, amended 2026-11-18 (#1880) and Q-OC1=A (2026-11-19): the
// worker's model option is removed. With no configured driver model in this
// fixture, the person's preset model remains unfiltered and last. The new
// preset-model regression separately covers an actually emitted own model.
func TestSpawn_ArgvOrderPresetUnfiltered_PresetFlagIsLast(t *testing.T) {
	for _, cli := range []string{"codex", "claude"} {
		t.Run(cli, func(t *testing.T) {
			opts := RunOptions{Input: "task", CLIArgs: []string{"--model", "b", "--profile", "worker"}, PresetArgs: []string{"--model", "a"}}
			var argv []string
			if cli == "codex" {
				argv = NewCodexDriver(nil).buildArgs(opts)
			} else {
				argv = NewClaudeDriver(nil).buildArgs(opts)
			}
			assert.NotContains(t, argv, "b", "#1880: the worker's model option and value are removed")
			require.True(t, containsSeq(argv, "--model", "a"), "without an own model, the preset model remains: %v", argv)
			assert.Equal(t, []string{"a"}, opencodeFix1880ModelValues(t, argv), "the preserved preset is the only model source")
			wantTail := []string{"--profile", "worker", "--model", "a"}
			if cli == "codex" {
				wantTail = append(wantTail, "-")
			}
			require.GreaterOrEqual(t, len(argv), len(wantTail))
			assert.Equal(t, wantTail, argv[len(argv)-len(wantTail):], "unrelated worker arguments precede the unchanged preset, immediately before any hand-off")
		})
	}
}

// 9.19: a preset is tokenised by the same parser as cli_args, so a quoted
// value stays one argv element.
func TestSpawn_PresetTokenisedLikeCLIArgs(t *testing.T) {
	const raw = `--flag "two words" --other 'x y'`
	tokens := ParseCLIArgs(raw, "t8-9-19")
	assert.Equal(t, []string{"--flag", "two words", "--other", "x y"}, tokens)
	argv := NewCodexDriver(nil).buildArgs(RunOptions{Input: "task", PresetArgs: tokens})
	assert.True(t, containsSeq(argv, "--flag", "two words"), "the quoted value is one argv element: %v", argv)
}
