//go:build !windows

package gateway

// Founder Q14 A, Q4/Q5/Q7 and T11 §4 Audit: Send a test message uses the
// person's SAVED preset, unfiltered, and audits a separately filtered copy.
// Actual Settings PUT, encrypted-store filter re-arm, smoke handler, Codex
// driver, harmless subprocess and audit JSONL. No driver/filter/auditor mock.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t11bSmokePresetProgram(t *testing.T) (string, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	program, marker := filepath.Join(dir, "saved-codex"), filepath.Join(dir, "actual-argv")
	quote := "'" + strings.ReplaceAll(marker, "'", "'\\''") + "'"
	script := "#!/bin/sh\nif [ \"$1\" = '--version' ]; then printf 'codex-cli t11b-test\\n'; exit 0; fi\n"
	script += "{ printf 'T11B-BEGIN\\n'; printf '%s\\n' \"$@\"; printf 'T11B-END\\n'; } >> " + quote + "\n"
	script += "printf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"t11b-smoke\"}'\n"
	script += "printf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"id\":\"answer\",\"type\":\"agent_message\",\"text\":\"2\"}}'\n"
	script += "printf '%s\\n' '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\n"
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	return program, marker
}

func TestPostAgentsExecutorSmokeTest_SavedPresetActuallyRunsAndAuditFollowsFilter_Q14(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "filter_on", false: "filter_off"}[enabled], func(t *testing.T) {
			api, f, _ := t11bSettingsAPI(t)
			t11bGateAuditFilter(t, api, enabled)
			program, marker := t11bSmokePresetProgram(t)
			const note = "person preset used; flag filter bypassed for preset parameters"
			preset := `--model "t11b smoke person model" --api-key ` + t11bGateNewSecret + ` --dangerously-bypass-approvals-and-sandbox`
			for i, text := range []string{preset, ""} {
				body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: program, PresetArgs: text}, gen.CliPreset{})
				w := t11bPut(t, f, body)
				require.Equal(t, http.StatusOK, w.Code, "real person saved parameters: %s", w.Body.String())
				t11bGateAssertFilter(t, api, enabled)
				t11bGateAssertDiskRow(t, api, program, text)
				agentArgs := "--dangerously-bypass-approvals-and-sandbox --model t11b-agent-model"
				raw, err := json.Marshal(gen.ExecutorSmokeTestRequest{Cli: gen.ExternalCliToolCodex, CliArgs: &agentArgs})
				require.NoError(t, err)
				code, resp := postExecutorSmokeTest(t, api, string(raw))
				require.Equal(t, http.StatusOK, code)
				require.True(t, resp.Ok, "instrument: the real bounded diagnostic completed: %+v", resp)
				require.NotNil(t, resp.ResponseText)
				assert.Equal(t, "2", *resp.ResponseText)
				assert.Nil(t, resp.Error)
				calls := t11bSmokeRecordedCalls(t, marker)
				require.Len(t, calls, i+1, "observe actual process invocations, not constructed options")
				args := calls[i]
				if text != "" {
					want := []string{"--model", "t11b-agent-model", "--model", "t11b smoke person model", "--api-key", t11bGateNewSecret, "--dangerously-bypass-approvals-and-sandbox", "-"}
					require.GreaterOrEqual(t, len(args), len(want))
					assert.Equal(t, want, args[len(args)-len(want):], "Q14/FR-10: person preset is unfiltered and last, including its bypass")
				} else {
					assert.NotContains(t, args, "t11b smoke person model")
					assert.NotContains(t, args, t11bGateNewSecret)
					assert.NotContains(t, args, "--dangerously-bypass-approvals-and-sandbox", "no person preset means the agent bypass stays filtered")
				}
				_, err = api.auditor.Verify(t.Context())
				require.NoError(t, err)
				entries := readAuditEntries(t, f.auditDir, audit.EventExecutorSmokeTest)
				require.Len(t, entries, i+1, "instrument: the real smoke audit record exists for every click")
				values := t8AuditScalars(entries[i])
				assert.Contains(t, values, "codex")
				assert.Contains(t, values, program)
				if text == "" {
					assert.NotContains(t, values, note, "no-preset control must not claim a person-preset bypass")
					continue
				}
				assert.Contains(t, values, note, "Q14: every diagnostic using a preset carries the literal use note")
				var recorded []string
				for _, value := range values {
					if strings.Contains(value, "t11b smoke person model") {
						recorded = append(recorded, value)
					}
				}
				if assert.Len(t, recorded, 1, "Q14: record the actual preset text, not only a CLI name") {
					assert.Contains(t, recorded[0], "--api-key")
					assert.Contains(t, recorded[0], "--dangerously-bypass-approvals-and-sandbox")
					if !enabled {
						assert.Equal(t, preset, recorded[0], "Q5 B: OFF records the text exactly as typed")
					}
				}
				if enabled {
					encoded, err := json.Marshal(entries[i])
					require.NoError(t, err)
					assert.NotContains(t, string(encoded), t11bGateNewSecret, "Q4: ON filters the audit copy, never the subprocess arguments")
				}
			}
		})
	}
}

func t11bSmokeRecordedCalls(t *testing.T, marker string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(marker)
	require.NoError(t, err, "instrument: the actual saved program wrote this evidence")
	blocks := strings.Split(string(raw), "T11B-BEGIN\n")[1:]
	calls := make([][]string, 0, len(blocks))
	for _, block := range blocks {
		require.True(t, strings.HasSuffix(block, "T11B-END\n"))
		calls = append(calls, strings.Split(strings.TrimSuffix(block, "\nT11B-END\n"), "\n"))
	}
	return calls
}
