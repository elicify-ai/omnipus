// T11-B §4 / FR-9, FR-10 contract capsules. T8's resolver, preset assembly
// and preset audit writer are absent in this checkout. Each test therefore
// fails with BLOCKED, not a skip, compile error, mock implementation or false
// behavioural proof. The SAME test author must bind the seams to real T8
// entry points when that backend lands; production code is never changed here.
package runner_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Internal test seam, not a wire-format type. The adapter must set up actual
// Settings and a legacy agent value, then call the real run-path resolver.
type settingsRunPath func(cli, savedPath, presetArgs, legacyCLIPath string) (usedPath, usedPreset string, err error)

func loadT11BRunPath(t *testing.T) settingsRunPath {
	t.Helper()
	t.Fatal("BLOCKED: T8 saved-settings run-path resolver not implemented — required by T11 §4 / FR-9; legacy per-agent cli_path must be ignored")
	return nil
}

// Internal test seam, not a fake implementation. argv is the actual command's
// arguments including its prompt hand-off. notes selects preset-use audit
// entries only, not unrelated run audit events. sensitiveValues must be
// registered with the real sensitive-data filter before the run.
type presetRun func(cli, presetArgs string, agentArgs, sensitiveValues []string) (argv, notes []string)

func loadT11BPresetRun(t *testing.T) presetRun {
	t.Helper()
	t.Fatal("BLOCKED: T8 preset assembly and preset-use audit not implemented — required by T11 §4 / FR-10; bind this test to the real run, not a test-side assembler")
	return nil
}

func TestNoSavedPath_RunRefusedWithRepairMessage(t *testing.T) {
	resolve := loadT11BRunPath(t)
	path, preset, err := resolve("codex", "", "", "/legacy/codex")
	// T8 ExecutorConfig.program_warning's specified exact run refusal;
	// team-lead ruling 2026-10-31 aligns T11's shorthand sentence to it.
	require.EqualError(t, err, "This agent cannot run: no program path is set for Codex. A person can set it in Settings -> Third-party agents -> Codex.")
	require.Equal(t, "", path, "a refused run resolves no executable, including the legacy path")
	require.Equal(t, "", preset, "a refused run applies no preset")
}

func TestSavedPath_RunUsesSettingsNotLegacyPath(t *testing.T) {
	resolve := loadT11BRunPath(t)
	path, preset, err := resolve("codex", "/person/bin/codex", "--max-turns 40", "/legacy/codex")
	require.NoError(t, err)
	require.Equal(t, "/person/bin/codex", path)
	require.Equal(t, "--max-turns 40", preset)
}

func TestPresetParams_AppliedLastUnfiltered_AgentFlagsFiltered(t *testing.T) {
	run := loadT11BPresetRun(t)
	const bypass = "--dangerously-bypass-approvals-and-sandbox"
	argv, _ := run("codex", bypass+" --model gpt-5-codex", []string{bypass, "--add-dir", "/allowed-extra"}, nil)
	// T11 run order plus CliPreset.preset_args: presets come AFTER agent args
	// but BEFORE the prompt hand-off. Codex uses trailing '-' for stdin.
	wantTail := []string{"--add-dir", "/allowed-extra", bypass, "--model", "gpt-5-codex", "-"}
	require.Greater(t, len(argv), len(wantTail), "driver flags must precede the agent and preset blocks")
	require.Equal(t, wantTail, argv[len(argv)-len(wantTail):], "driver -> filtered agent -> unfiltered preset -> prompt")
	count := 0
	for _, arg := range argv {
		if arg == bypass {
			count++
		}
	}
	require.Equal(t, 1, count, "agent's bypass is dropped; exactly the person's copy survives")
}

func TestPresetAudit_RecordsPresetUse(t *testing.T) {
	run := loadT11BPresetRun(t)
	const secret = "t11b-registered-sensitive-value"
	for iteration := 0; iteration < 2; iteration++ {
		_, notes := run("codex", "--api-key "+secret+" --model gpt-5-codex", nil, []string{secret})
		require.Len(t, notes, 1, "one preset note EACH run, not once per process or twice per run")
		require.Contains(t, notes[0], "person preset used; flag filter bypassed for preset parameters")
		require.NotContains(t, notes[0], secret, "registered sensitive parameter value must be redacted")
		require.Contains(t, notes[0], "gpt-5-codex", "benign parameter text remains; dropping all text is not filtering it")
	}
}

func TestPresetAudit_NoPresetProducesNoPresetNote(t *testing.T) {
	run := loadT11BPresetRun(t)
	_, notes := run("codex", "", []string{"--add-dir", "/allowed-extra"}, nil)
	require.Empty(t, notes, "no person-preset note when only agent flags are applied")
}
