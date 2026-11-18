package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const t11bAuditSecret = "t11b-registered-sensitive-value"

// The token is deliberately a dangerous flag's =value form: filter_reason
// repeats that token, so masking preset_args alone is not sufficient. T11 §4
// Audit and founder Q5 B / Q6 A (2026-11-06) require filtering in both fields
// while tools.filter_sensitive_data is ON; OFF retains typed parameter text.
const t11bSensitiveBypass = "--dangerously-bypass-approvals-and-sandbox=" + t11bAuditSecret

func t11bSensitivePresetAudit(t *testing.T, filterEnabled bool) audit.Record {
	t.Helper()
	f := t8RunFixture(t)
	f.setProgram(t, "codex", l11274Executable(t, f.home, "codex", true))
	t8SetPreset(t, f, "codex", t11bSensitiveBypass+" --model gpt-5-codex")
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Tools.FilterSensitiveData = filterEnabled
		return nil
	}))
	f.al.GetConfig().RegisterSensitiveValues([]string{t11bAuditSecret})
	t.Cleanup(func() { f.al.GetConfig().RegisterSensitiveValues(nil) })
	require.NotContains(t, f.al.GetConfig().SensitiveDataReplacer().Replace(t11bAuditSecret), t11bAuditSecret,
		"instrument: the real registered-value filter knows the synthetic credential")
	if !filterEnabled {
		require.Equal(t, t11bAuditSecret, f.al.GetConfig().FilterSensitiveData(t11bAuditSecret),
			"instrument: the ordinary text-filter switch really is off")
	}
	t11bSeedCodexWorker(t, f, "t11b-audit-reason", "", "")
	opts := t11bDispatchOptions(t, f, "t11b-audit-reason")
	require.Equal(t, []string{t11bSensitiveBypass, "--model", "gpt-5-codex"}, opts.PresetArgs,
		"the worker still gets the person's parameters as typed; only the audit copy is filtered")
	records := t11bRunAuditRecords(t, f)
	require.Len(t, records, 1)
	return records[0]
}

func t11bAssertFilteredPresetAudit(t *testing.T, rec audit.Record) {
	t.Helper()
	assert.Equal(t, t11bPresetNote, rec.Fields["note"])
	assert.Equal(t, true, rec.Fields["filter_bypassed_by_person_preset"], "instrument: the preset has a token whose filter reason is recorded")
	preset, ok := rec.Fields["preset_args"].(string)
	require.True(t, ok)
	assert.NotContains(t, preset, t11bAuditSecret, "preset_args must not expose the registered value")
	assert.Contains(t, preset, "gpt-5-codex", "benign parameter text is retained")
	reasons, ok := rec.Fields["filter_reason"].([]any)
	require.True(t, ok, "the real event must contain filter_reason; absence is not redaction")
	require.Len(t, reasons, 1, "one bypass token produces one reason")
	reason, ok := reasons[0].(string)
	require.True(t, ok)
	assert.Contains(t, reason, "--dangerously-bypass-approvals-and-sandbox", "the nonsensitive flag explanation remains useful")
	assert.NotContains(t, reason, t11bAuditSecret, "filter_reason repeats the preset token and needs its OWN filtering")
	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), t11bAuditSecret, "no field in this audit entry may leak the registered value")
}

func TestPresetAudit_FilterReasonRedactsRegisteredValue(t *testing.T) {
	t11bAssertFilteredPresetAudit(t, t11bSensitivePresetAudit(t, true))
}

// Founder Q5 B / Q6 A, relayed by team-lead 2026-11-06: the switch governs
// BOTH preset_args and filter_reason. OFF records the text as typed. This
// supersedes the earlier provisional no-clear-text oracle; it is not inferred
// from production output.
func TestPresetAudit_FilterSwitchOffRecordsTypedText(t *testing.T) {
	rec := t11bSensitivePresetAudit(t, false)
	assert.Equal(t, t11bPresetNote, rec.Fields["note"])
	assert.Equal(t, t11bSensitiveBypass+" --model gpt-5-codex", rec.Fields["preset_args"],
		"founder Q5 B: with the switch off, the audit records the parameters as typed")
	assert.Equal(t, true, rec.Fields["filter_bypassed_by_person_preset"])
	reasons, ok := rec.Fields["filter_reason"].([]any)
	require.True(t, ok)
	require.Len(t, reasons, 1)
	reason, ok := reasons[0].(string)
	require.True(t, ok)
	assert.Contains(t, reason, t11bSensitiveBypass, "founder Q6 A: filter_reason obeys the same switch as preset_args")
}

// Owner design, relayed by team-lead 2026-11-06, NOT a founder ruling: with
// no config use the explicit unavailable-filter sentinel. A real delegation
// cannot be arranged honestly without config. This narrower writer test calls
// actual auditRunSpawn and a real JSONL logger, with no fake filter/RunOptions.
func TestPresetAudit_NoConfigWithholdsPresetText(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RedactEnabled: true, AuditLogRequested: true})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logger.Close()) })
	al := &AgentLoop{auditLogger: logger}
	require.Nil(t, al.GetConfig(), "instrument: this writer really has no config")
	ed := &runExternalCLISubTurnState{
		al: al, agent: &AgentInstance{ID: "t11b-audit-no-config"},
		presetText: t11bSensitiveBypass + " --model gpt-5-codex",
		presetArgs: []string{t11bSensitiveBypass, "--model", "gpt-5-codex"},
	}
	ed.auditRunSpawn(t.Context(), "codex")
	records := t11bReadRunAudit(t, logger, filepath.Join(dir, "audit.jsonl"))
	require.Len(t, records, 1)
	const withheld = "[not recorded: the sensitive-data filter is unavailable]"
	assert.Equal(t, t11bPresetNote, records[0].Fields["note"])
	assert.Equal(t, withheld, records[0].Fields["preset_args"], "owner design: make unavailable filtering visible, not clear text")
	reasons, ok := records[0].Fields["filter_reason"].([]any)
	require.True(t, ok)
	require.Equal(t, []any{withheld}, reasons, "no-config withholding also covers the repeated parameter in filter_reason")
	raw, err := json.Marshal(records[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), t11bAuditSecret)
}
