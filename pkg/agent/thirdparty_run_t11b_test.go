package agent

// T11-B §4 / FR-9, FR-10: the original five capsules now drive real Settings,
// legacy worker config, delegation and audit. They live here because the real
// dispatch's process-edge factory is private to pkg/agent. No resolver, argv
// assembler, audit writer or sensitive-data filter is replaced by a test fake.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const t11bPresetNote = "person preset used; flag filter bypassed for preset parameters"

// Capture options only after the real delegate dispatch reaches Run, then join
// that run before another fixture/config change or an audit-file read.
func t11bDispatchOptions(t *testing.T, f *l11274Fixture, id string) runner.RunOptions {
	t.Helper()
	fake, _ := l11274DriverSeam(t, f)
	result := l11274Delegate(t, f, id, "T11-B real Settings/preset run", true)
	require.NotNil(t, result)
	require.False(t, result.IsError, "delegation refused: %s", result.ForLLM)
	select {
	case opts := <-fake.runs:
		fake.Cancel()
		f.al.drainSteeredTurns(30 * time.Second)
		return opts
	case <-time.After(5 * time.Second):
		t.Fatal("the real delegation never reached the external process boundary")
	}
	return runner.RunOptions{}
}

func t11bSeedCodexWorker(t *testing.T, f *l11274Fixture, id, args, legacyPath string) {
	t.Helper()
	l11274SeedWorker(t, f, id, &config.ExecutorConfig{
		Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIArgs: args, CLIPath: legacyPath,
	})
}

func TestNoSavedPath_RunRefusedWithRepairMessage(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", "")
	legacy := l11274Executable(t, f.home, "codex", true)
	t.Setenv("PATH", filepath.Dir(legacy)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t11bSeedCodexWorker(t, f, "t11b-no-path", "", legacy)

	refusal := t8NoRunReachedDriver(t, f, "t11b-no-path")
	// Exact oracle from T11 §4 / ExecutorConfig.program_warning; never call
	// the production refusal-text function to construct the expected sentence.
	require.Equal(t, "This agent cannot run: no program path is set for Codex. A person can set it in Settings → Third-party agents → Codex.", refusal)
}

func TestSavedPath_RunUsesSettingsNotLegacyPath(t *testing.T) {
	f := t8RunFixture(t)
	program := l11274Executable(t, f.home, "codex", true)
	legacy := l11274Executable(t, f.home, "codex", true)
	f.setProgram(t, "codex", program)
	t8SetPreset(t, f, "codex", "--max-turns 40")
	t11bSeedCodexWorker(t, f, "t11b-saved-path", "", legacy)

	opts := t11bDispatchOptions(t, f, "t11b-saved-path")
	resolved, err := filepath.EvalSymlinks(program)
	require.NoError(t, err)
	require.Equal(t, resolved, opts.CLIPath, "only the resolved Settings program reaches the real driver")
	require.NotEqual(t, legacy, opts.CLIPath)
	require.Equal(t, []string{"--max-turns", "40"}, opts.PresetArgs)
}

func TestPresetParams_AppliedLastUnfiltered_AgentFlagsFiltered(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", l11274Executable(t, f.home, "codex", true))
	const bypass = "--dangerously-bypass-approvals-and-sandbox"
	t8SetPreset(t, f, "codex", bypass+" --model gpt-5-codex")
	t11bSeedCodexWorker(t, f, "t11b-preset-order", bypass+" --add-dir /allowed-extra", "")

	opts := t11bDispatchOptions(t, f, "t11b-preset-order")
	argv := runner.BuildCodexArgs(opts) // actual dispatched options, not a test-side assembler
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

// Read the real production audit logger after all dispatches have joined. The
// reader never redacts or synthesizes notes; malformed records fail loudly.
func t11bRunAuditRecords(t *testing.T, f *l11274Fixture) []audit.Record {
	t.Helper()
	return t11bReadRunAudit(t, f.al.AuditLogger(), filepath.Join(f.home, "system", "audit.jsonl"))
}

func t11bReadRunAudit(t *testing.T, logger *audit.Logger, path string) []audit.Record {
	t.Helper()
	require.NotNil(t, logger, "instrument: audit must actually be enabled")
	// Verify flushes the logger under its mutex before its read. We use that
	// public flush boundary only; this test makes no HMAC-chain verdict and
	// leaves the logger open for its owner's normal shutdown.
	_, err := logger.Verify(t.Context())
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var records []audit.Record
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var rec audit.Record
		require.NoError(t, json.Unmarshal(line, &rec), "malformed real audit record")
		if rec.Event == audit.EventExternalCLIRun {
			records = append(records, rec)
		}
	}
	return records
}

func TestPresetAudit_RecordsPresetUse(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", l11274Executable(t, f.home, "codex", true))
	const secret = "t11b-registered-sensitive-value"
	t8SetPreset(t, f, "codex", "--api-key "+secret+" --model gpt-5-codex")
	f.al.GetConfig().RegisterSensitiveValues([]string{secret})
	t.Cleanup(func() { f.al.GetConfig().RegisterSensitiveValues(nil) })
	// Instrument the real registered-value filter, without applying it to
	// anything returned by the audit reader.
	require.NotContains(t, f.al.GetConfig().SensitiveDataReplacer().Replace(secret), secret)

	ids := []string{"t11b-preset-first", "t11b-preset-second"}
	for _, id := range ids {
		t11bSeedCodexWorker(t, f, id, "", "")
		opts := t11bDispatchOptions(t, f, id)
		require.Equal(t, []string{"--api-key", secret, "--model", "gpt-5-codex"}, opts.PresetArgs,
			"instrument: the real run applies the preset whose audit text must be filtered")
	}
	records := t11bRunAuditRecords(t, f)
	require.Len(t, records, 2, "one real run audit event EACH run, not once per process or twice per run")
	for _, id := range ids {
		var own []audit.Record
		for _, rec := range records {
			if rec.Fields["agent_id"] == id {
				own = append(own, rec)
			}
		}
		require.Len(t, own, 1, "one preset event for %s", id)
		raw, err := json.Marshal(own[0])
		require.NoError(t, err)
		assert.Equal(t, t11bPresetNote, own[0].Fields["note"], "T11 §4 Audit / FR-10 / U-6: every applied preset, even without a dangerous flag")
		assert.Contains(t, string(raw), t11bPresetNote)
		assert.NotContains(t, string(raw), secret, "registered sensitive parameter value must be filtered in the real audit event")
		preset, ok := own[0].Fields["preset_args"].(string)
		require.True(t, ok, "audit preserves the parameter text as a string")
		assert.NotContains(t, preset, secret, "T11 §4 Audit: preset_args passes through the sensitive-data filter")
		assert.Contains(t, preset, "gpt-5-codex", "benign parameter text remains; dropping all text is not filtering it")
	}
}

func TestPresetAudit_NoPresetProducesNoPresetNote(t *testing.T) {
	f := t8RunFixture(t)
	f.setProgram(t, "codex", l11274Executable(t, f.home, "codex", true))
	// Positive control uses the SAME writer/reader. A missing preset-note
	// implementation cannot make the negative assertion pass vacuously.
	t8SetPreset(t, f, "codex", "--model gpt-5-codex")
	t11bSeedCodexWorker(t, f, "t11b-audit-positive", "", "")
	_ = t11bDispatchOptions(t, f, "t11b-audit-positive")
	t8SetPreset(t, f, "codex", "")
	t11bSeedCodexWorker(t, f, "t11b-audit-negative", "--add-dir /allowed-extra", "")
	opts := t11bDispatchOptions(t, f, "t11b-audit-negative")
	require.Equal(t, []string{"--add-dir", "/allowed-extra"}, opts.CLIArgs)
	require.Empty(t, opts.PresetArgs)

	records := t11bRunAuditRecords(t, f)
	require.Len(t, records, 2)
	seen := map[string]int{}
	for _, rec := range records {
		id, ok := rec.Fields["agent_id"].(string)
		require.True(t, ok)
		seen[id]++
		switch id {
		case "t11b-audit-positive":
			assert.Equal(t, t11bPresetNote, rec.Fields["note"], "positive control: applied preset produces its required note")
		case "t11b-audit-negative":
			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), t11bPresetNote, "no person-preset note when only agent flags are applied")
		default:
			t.Fatalf("unexpected external run audit identity: %v", rec.Fields["agent_id"])
		}
	}
	require.Equal(t, map[string]int{"t11b-audit-positive": 1, "t11b-audit-negative": 1}, seen,
		"both the positive and negative controls must actually have run and emitted an audit event")
}
