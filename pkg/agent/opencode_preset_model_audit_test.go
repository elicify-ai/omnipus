package agent

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q-OC1=A audit addition (team-lead, 2026-11-19): preset_flags_dropped is
// conditional on an actual preset-model removal, contains only removed
// options, and obeys PresetAuditFilter. The boundary is the real spawn audit
// writer plus real Settings resolution, config filter and JSONL logger; no
// audit/filter mock and no CLI process or paid model is needed at this boundary.
func TestOpenCodePresetModelAudit_RunAuditNamesOnlyDroppedOptionsAndFiltersThem(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "filter_on", false: "filter_off"}[enabled], func(t *testing.T) {
			for _, cli := range []struct{ name, model string }{
				{"claude-code", "sonnet"}, {"codex", "gpt-5-codex"}, {"opencode", "openrouter/deepseek/deepseek-v4.1-flash"},
			} {
				t.Run(cli.name, func(t *testing.T) {
					f := t8RunFixture(t)
					f.setProgram(t, cli.name, opencodeTrustedProgram(t, cli.name))
					require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
						cfg.Tools.FilterSensitiveData = enabled
						return nil
					}))
					const survivor = "--fixture-kept audit-kept-token --dangerously-bypass-approvals-and-sandbox"
					const conflict = "--model person/audit-model --model=" + t11bAuditSecret + " " + survivor
					cases := []struct {
						name, model, preset string
						dropped             bool
					}{
						{"own_model_conflict", cli.model, conflict, true},
						{"no_own_model", "", conflict, false},
						{"non_model_preset", cli.model, survivor, false},
						{"empty_preset", cli.model, "", false},
					}
					if cli.name == "opencode" {
						cases = append(cases, struct {
							name, model, preset string
							dropped             bool
						}{"bare_model_not_emitted", "bare-model", conflict, false})
					}
					for i, tc := range cases {
						t.Run(tc.name, func(t *testing.T) {
							t8SetPreset(t, f, cli.name, tc.preset)
							f.al.GetConfig().RegisterSensitiveValues([]string{t11bAuditSecret})
							require.NotContains(t, f.al.GetConfig().SensitiveDataReplacer().Replace(t11bAuditSecret), t11bAuditSecret, "instrument: the real filter knows the synthetic secret")
							require.Equal(t, enabled, f.al.GetConfig().Tools.IsFilterSensitiveDataEnabled())
							ed := &runExternalCLISubTurnState{al: f.al, agent: &AgentInstance{ID: "preset-audit-" + tc.name}, agentModel: tc.model}
							require.NoError(t, ed.resolveRunProgram(cli.name), "instrument: resolve the real saved program and preset")
							ed.auditRunSpawn(t.Context(), cli.name)
							records := t11bRunAuditRecords(t, f)
							require.Len(t, records, i+1, "instrument: exactly one real run-audit record per writer call")
							rec := records[i]
							assert.Equal(t, "preset-audit-"+tc.name, rec.Fields["agent_id"])
							value, present := rec.Fields["preset_flags_dropped"]
							if !tc.dropped {
								assert.False(t, present, "Q-OC1=A: omit the field entirely when no preset model was removed; value=%v", value)
								return
							}
							require.True(t, present, "Q-OC1=A: run audit must name the preset model options removed for the configured model")
							raw, err := json.Marshal(value)
							require.NoError(t, err)
							text := string(raw)
							assert.Contains(t, text, "--model")
							assert.Contains(t, text, "person/audit-model", "benign removed model remains identifiable")
							for _, kept := range []string{"--fixture-kept", "audit-kept-token", "--dangerously-bypass-approvals-and-sandbox"} {
								assert.NotContains(t, text, kept, "the dropped-options field must never copy surviving preset text")
							}
							if enabled {
								assert.NotContains(t, text, t11bAuditSecret, "the NEW audit field must pass through PresetAuditFilter too")
							} else {
								assert.Contains(t, text, "--model="+t11bAuditSecret, "filter OFF retains the removed option as typed")
							}
						})
					}
					f.al.GetConfig().RegisterSensitiveValues(nil)
				})
			}
		})
	}
}
