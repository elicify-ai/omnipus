//go:build !windows

package agent

// Founder Q12 A / S3; T11 FR-9/10 and FR-043. Actual retained session,
// startAndDrain, vendor driver and exec. Stub programs report native IDs and
// record their OWN argv; neither Resume nor RunOptions is faked.
// Architect closure allows current settings OR a visible incompatible-settings
// refusal before any further process. A silent new conversation is never valid.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t11bNativeCLI struct {
	cli, nativeLine, doneLine string
	resumeTokens              []string
}

func t11bNativeCLIs() []t11bNativeCLI {
	return []t11bNativeCLI{
		{"claude-code", `{"type":"system","subtype":"init","session_id":"t11b-native-session"}`,
			`{"type":"result","subtype":"success","result":"ok"}`, []string{"--resume", "t11b-native-session"}},
		{"codex", `{"type":"thread.started","thread_id":"t11b-native-thread"}`,
			`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`, []string{"resume", "t11b-native-thread"}},
		{"opencode", "", `{"type":"session.complete","session_id":"t11b-native-session"}`, []string{"--session", "t11b-native-session"}},
	}
}

func t11bNativeProgram(t *testing.T, spec t11bNativeCLI, label string) (string, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	program, marker := filepath.Join(dir, label), filepath.Join(dir, label+".calls")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	// Even a --version probe executes arbitrary code in this file. Record ALL
	// starts separately so a stale probe cannot hide behind a visible refusal.
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + quote(marker+".all") + "\n"
	script += "if [ \"$1\" = '--version' ]; then printf 't11b-fixture-version\\n'; exit 0; fi\n"
	script += "{ printf 'T11B-BEGIN\\n'; printf '%s\\n' \"$@\"; printf 'T11B-END\\n'; } >> " + quote(marker) + "\n"
	if spec.nativeLine != "" {
		script += "printf '%s\\n' " + quote(spec.nativeLine) + "\n"
	}
	script += "printf '%s\\n' " + quote(spec.doneLine) + "\n"
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	return program, marker
}

func t11bNativeCalls(t *testing.T, path string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // the caller explicitly asserts the required process count
	}
	require.NoError(t, err)
	var calls [][]string
	for _, block := range strings.Split(string(raw), "T11B-BEGIN\n")[1:] {
		require.True(t, strings.HasSuffix(block, "T11B-END\n"), "instrument: complete process-written argv record")
		calls = append(calls, strings.Split(strings.TrimSuffix(block, "\nT11B-END\n"), "\n"))
	}
	return calls
}

func t11bSaveRunSettings(t *testing.T, al *AgentLoop, cli, path, preset string) {
	t.Helper()
	require.NoError(t, al.MutateConfig(func(cfg *config.Config) error {
		if cfg.Sandbox.ThirdPartyAgents.CLIs == nil {
			cfg.Sandbox.ThirdPartyAgents.CLIs = make(map[string]config.ThirdPartyCLIConfig)
		}
		cfg.Sandbox.ThirdPartyAgents.CLIs[cli] = config.ThirdPartyCLIConfig{Path: path, PresetArgs: preset}
		return config.SaveConfig(filepath.Join(omnipusHome(), "config.json"), cfg)
	}))
}

func t11bResumeChangedSettings(t *testing.T, spec t11bNativeCLI, change string) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, spec.cli, "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	programA, markerA := t11bNativeProgram(t, spec, "program-A")
	programB, markerB := t11bNativeProgram(t, spec, "program-B")
	const oldPreset = "--model t11b-old-person-model"
	const newPreset = "--model t11b-current-person-model"
	t11bSaveRunSettings(t, al, spec.cli, programA, oldPreset)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second) // existing real-run fixture bound
	defer cancel()
	_, err := runExternalCLISubTurn(ctx, al, ts, "initial instruction")
	require.NoError(t, err, "instrument: first native conversation succeeds")
	first := t11bNativeCalls(t, markerA)
	require.Len(t, first, 1, "instrument: program A really executed")
	require.Contains(t, first[0], "t11b-old-person-model", "instrument: the prior preset really reached the process")
	require.Empty(t, t11bNativeCalls(t, markerB))
	allFirstStarts, err := os.ReadFile(markerA + ".all")
	require.NoError(t, err, "instrument: every start, including version probes, is recorded")
	path, preset := programA, newPreset
	if change == "program" {
		path = programB
	}
	if change == "remove-preset" {
		preset = ""
	}
	t11bSaveRunSettings(t, al, spec.cli, path, preset)
	ts.opts.ExternalCLIResume = true
	_, resumeErr := runExternalCLISubTurn(ctx, al, ts, "new continuation instruction")
	afterA, afterB := t11bNativeCalls(t, markerA), t11bNativeCalls(t, markerB)
	if resumeErr != nil || change == "program" {
		allAfterStarts, readErr := os.ReadFile(markerA + ".all")
		require.NoError(t, readErr)
		assert.Equal(t, string(allFirstStarts), string(allAfterStarts),
			"S3: do not execute stale program A even for --version or before refusing the continuation")
	}
	if resumeErr != nil {
		assert.NoFileExists(t, markerB+".all", "a refused continuation starts neither replacement program nor its version probe")
		// Not any error: require a visible settings/configuration-change refusal
		// and NO attempted stale/current subprocess. Do not invent exact prose.
		assert.Regexp(t, `(?i)(settings|configuration|program|parameters|preset).*(chang|differ|incompat)|(?i)(chang|differ|incompat).*(settings|configuration|program|parameters|preset)`, resumeErr.Error())
		assert.Equal(t, first, afterA, "a refusal must not execute the prior file again")
		assert.Empty(t, afterB, "refuse before trying a replacement program")
		return
	}
	var resumed []string
	if change == "program" {
		assert.Equal(t, first, afterA, "S3: a resumed run must not launch old program A after Settings selected B")
		require.Len(t, afterB, 1, "S3: inspect the actual process that ran, not only the checked settings")
		resumed = afterB[0]
	} else {
		require.Len(t, afterA, 2, "parameter-only change still resumes the actual saved program")
		assert.Empty(t, afterB)
		resumed = afterA[1]
	}
	assert.NotContains(t, resumed, "t11b-old-person-model", "S3: old parameters must never survive a changed/removed Settings preset")
	if preset != "" {
		assert.Contains(t, resumed, "t11b-current-person-model")
	} else {
		assert.NotContains(t, resumed, "t11b-current-person-model", "removal does not invent a replacement preset")
	}
	assert.Contains(t, strings.Join(resumed, "\x00"), strings.Join(spec.resumeTokens, "\x00"),
		"FR-043: current settings must preserve the SAME vendor-native conversation, not silently start fresh")
}

func TestExternalRunResume_ChangedProgramNeverExecutesPriorFile_S3(t *testing.T) {
	for _, spec := range t11bNativeCLIs() {
		t.Run(spec.cli, func(t *testing.T) { t11bResumeChangedSettings(t, spec, "program") })
	}
}

func TestExternalRunResume_ChangedPresetNeverUsesPriorParameters_S3(t *testing.T) {
	for _, spec := range t11bNativeCLIs() {
		t.Run(spec.cli, func(t *testing.T) { t11bResumeChangedSettings(t, spec, "preset") })
	}
}

func TestExternalRunResume_RemovedPresetNeverUsesPriorParameters_S3(t *testing.T) {
	for _, spec := range t11bNativeCLIs() {
		t.Run(spec.cli, func(t *testing.T) { t11bResumeChangedSettings(t, spec, "remove-preset") })
	}
}
