//go:build !windows

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R11: actual registered smoke route and actual harmless native process. Count
// every executable start, not only work calls: --version must not evade proof.
func TestCombinedProgramLocation_IncompleteMount_SmokeNoSpawn_Q37_R11(t *testing.T) {
	for _, fault := range []string{"unreadable_record", "malformed_json", "dropped_entry"} {
		t.Run(fault, func(t *testing.T) {
			f := t8SettingsAPI(t)
			// Request-edge fixture identity, like postExecutorSmokeTest: separate limiter keys.
			chain := f.handler
			f.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.RemoteAddr = t.Name() + ":0"
				chain.ServeHTTP(w, r)
			})
			ws := seedLibraryWorkspace(t, f.api, "Combined actual smoke refusal")
			program, workMarker := t11bSmokePresetProgram(t)
			allMarker := workMarker + ".all"
			script, err := os.ReadFile(program)
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(string(script), "#!/bin/sh\n"))
			quoted := "'" + strings.ReplaceAll(allMarker, "'", "'\\''") + "'"
			instrumented := strings.Replace(string(script), "#!/bin/sh\n", "#!/bin/sh\nprintf 'start\\n' >> "+quoted+"\n", 1)
			require.NoError(t, os.WriteFile(program, []byte(instrumented), 0o755))
			body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: program}, gen.CliPreset{})
			w := f.call(t, http.MethodPut, t8SettingsRoute, body, "person")
			require.Equal(t, http.StatusOK, w.Code, "control: real person saves the program: %s", w.Body.String())
			request := gen.ExecutorSmokeTestRequest{Cli: gen.ExternalCliToolCodex}
			control := f.call(t, http.MethodPost, "/api/v1/agents/executor-smoke-test", request, "person")
			require.Equal(t, http.StatusOK, control.Code, "registered smoke route: %s", control.Body.String())
			var initial gen.ExecutorSmokeTestResponse
			require.NoError(t, json.Unmarshal(control.Body.Bytes(), &initial))
			require.True(t, initial.Ok, "instrument: real native diagnostic completes: %+v", initial)
			require.NotNil(t, initial.ResponseText)
			require.Equal(t, "2", *initial.ResponseText)
			require.Nil(t, initial.Error)
			beforeWork, err := os.ReadFile(workMarker)
			require.NoError(t, err)
			require.NotEmpty(t, beforeWork)
			beforeAll, err := os.ReadFile(allMarker)
			require.NoError(t, err)
			require.NotEmpty(t, beforeAll, "instrument: all-start recorder ran at least once")
			t11bDamageSettingsMountRecord(t, f.api.homePath, ws, fault)
			t.Run("refusal_Q37", func(t *testing.T) {
				refused := f.call(t, http.MethodPost, "/api/v1/agents/executor-smoke-test", request, "person")
				require.Equal(t, http.StatusOK, refused.Code)
				var out gen.ExecutorSmokeTestResponse
				require.NoError(t, json.Unmarshal(refused.Body.Bytes(), &out))
				assert.False(t, out.Ok)
				assert.Nil(t, out.ResponseText)
				require.NotNil(t, out.Error)
				assert.Regexp(t, `^This agent cannot run: `, *out.Error)
				assert.Contains(t, *out.Error, "location_unverifiable")
				assert.Contains(t, *out.Error, t11combined.LocationUnverifiableCause)
				assert.Contains(t, *out.Error, "Details are in the server log.")
				assert.NotContains(t, *out.Error, "Omnipus could not read the list of mounted folders")
				assert.NotContains(t, *out.Error, "agent_writable_location")
				afterWork, readErr := os.ReadFile(workMarker)
				require.NoError(t, readErr)
				assert.Equal(t, beforeWork, afterWork, "no real native work process after a bad record")
				afterAll, readErr := os.ReadFile(allMarker)
				require.NoError(t, readErr)
				assert.Equal(t, beforeAll, afterAll, "no real process start, including --version")
			})
			record, err := workspace.MountStorePath(f.api.homePath, ws)
			require.NoError(t, err)
			require.NoError(t, os.Remove(record))
			recovery := f.call(t, http.MethodPost, "/api/v1/agents/executor-smoke-test", request, "person")
			require.Equal(t, http.StatusOK, recovery.Code)
			var after gen.ExecutorSmokeTestResponse
			require.NoError(t, json.Unmarshal(recovery.Body.Bytes(), &after))
			require.True(t, after.Ok, "control: same actual executable works after removal: %+v", after)
			require.NotNil(t, after.ResponseText)
			require.Equal(t, "2", *after.ResponseText)
		})
	}
}
