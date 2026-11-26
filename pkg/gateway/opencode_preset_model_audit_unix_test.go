//go:build !windows

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q-OC1=A audit addition (team-lead, 2026-11-19). Real Settings PUT, smoke
// handler, saved executable, recorded process argv and JSONL audit. Expected
// presence and contents come from the ruling, not the implementation's output.
func TestOpenCodePresetModelAudit_SmokeAuditNamesOnlyDroppedOptionsAndFiltersThem(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "filter_on", false: "filter_off"}[enabled], func(t *testing.T) {
			api, f, _ := t11bSettingsAPI(t)
			t11bGateAuditFilter(t, api, enabled)
			program, marker := t11bSmokePresetProgram(t)
			const survivor = "--fixture-kept audit-kept-token --dangerously-bypass-approvals-and-sandbox"
			const conflict = "--model person/audit-model --model=" + t11bGateNewSecret + " " + survivor
			for i, tc := range []struct {
				name, model, preset string
				dropped             bool
			}{
				{"own_model_conflict", "gpt-5-codex", conflict, true},
				{"no_own_model", "", conflict, false},
				{"non_model_preset", "gpt-5-codex", survivor, false},
				{"empty_preset", "gpt-5-codex", "", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: program, PresetArgs: tc.preset}, gen.CliPreset{})
					w := t11bPut(t, f, body)
					require.Equal(t, http.StatusOK, w.Code, "real Settings save: %s", w.Body.String())
					t11bGateAssertFilter(t, api, enabled)
					workerArgs := "--model worker-model"
					raw, err := json.Marshal(gen.ExecutorSmokeTestRequest{Cli: gen.ExternalCliToolCodex, Model: &tc.model, CliArgs: &workerArgs})
					require.NoError(t, err)
					code, resp := postExecutorSmokeTest(t, api, string(raw))
					require.Equal(t, http.StatusOK, code)
					require.True(t, resp.Ok, "instrument: actual saved program completed: %+v", resp)
					require.NotNil(t, resp.ResponseText)
					assert.Equal(t, "2", *resp.ResponseText)
					calls := t11bSmokeRecordedCalls(t, marker)
					require.Len(t, calls, i+1, "instrument: every request really launched the saved program")
					_, err = api.auditor.Verify(t.Context())
					require.NoError(t, err)
					entries := readAuditEntries(t, f.auditDir, audit.EventExecutorSmokeTest)
					require.Len(t, entries, i+1, "instrument: exactly one real smoke audit per invocation")
					details, ok := entries[i]["details"].(map[string]any)
					require.True(t, ok)
					assert.Equal(t, "codex", details["cli"])
					assert.Equal(t, program, details["binary"])
					value, present := details["preset_flags_dropped"]
					if !tc.dropped {
						assert.False(t, present, "Q-OC1=A: omit the field when no preset option was removed; value=%v", value)
						return
					}
					require.True(t, present, "Q-OC1=A: smoke audit must name the preset model options removed for the configured model")
					encoded, err := json.Marshal(value)
					require.NoError(t, err)
					text := string(encoded)
					assert.Contains(t, text, "--model")
					assert.Contains(t, text, "person/audit-model", "keep the benign removed model identifiable")
					for _, kept := range []string{"--fixture-kept", "audit-kept-token", "--dangerously-bypass-approvals-and-sandbox"} {
						assert.NotContains(t, text, kept, "the dropped-options field must not copy the surviving preset text")
					}
					if enabled {
						assert.NotContains(t, text, t11bGateNewSecret, "the NEW audit field must pass through PresetAuditFilter")
					} else {
						assert.Contains(t, text, "--model="+t11bGateNewSecret, "filter OFF retains the removed option as typed")
					}
				})
			}
		})
	}
}
