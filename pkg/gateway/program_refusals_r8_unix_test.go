//go:build darwin || linux

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder Q48 A / wide R5: inspect the whole refused body at each real route,
// not just one JSON field. Each case first proves its same positive route works.
func TestR8_ProgramLocationRefusalsHaveNoClientDiagnostics_Q48(t *testing.T) {
	for _, surface := range []string{"check_cli", "command_preview", "smoke_test", "runner_test"} {
		t.Run(surface, func(t *testing.T) {
			f, program, marker := r8PersonProgram(t)
			driverCalls := 0
			if surface == "smoke_test" {
				original := smokeTestNewDriver
				smokeTestNewDriver = func(cli string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
					require.Equal(t, "claude-code", cli)
					driverCalls++
					return &capturingFakeDriver{}, nil
				}
				t.Cleanup(func() { smokeTestNewDriver = original })
			}
			route := "/api/v1/agents/r8-diagnostic-worker/runner/test"
			var request any
			switch surface {
			case "check_cli":
				route = "/api/v1/system/cli-validate"
				request = gen.CliValidateRequest{Cli: gen.ExternalCliToolClaudeCode, CliPath: program}
			case "command_preview":
				route = "/api/v1/agents/executor-preview"
				request = gen.ExecutorCommandPreviewRequest{Cli: gen.ExternalCliToolClaudeCode}
			case "smoke_test":
				route = "/api/v1/agents/executor-smoke-test"
				request = gen.ExecutorSmokeTestRequest{Cli: gen.ExternalCliToolClaudeCode}
			}
			control := f.call(t, http.MethodPost, route, request, "person")
			require.Equal(t, http.StatusOK, control.Code, "instrument: positive route works: %s", control.Body.String())
			switch surface {
			case "check_cli":
				var result gen.CliValidateResponse
				require.NoError(t, json.Unmarshal(control.Body.Bytes(), &result))
				require.Equal(t, gen.CliValidateResponseReasonOk, result.Reason)
				require.Equal(t, "8.1.0", deref(result.Version))
				require.FileExists(t, marker, "instrument: the actual checked version program ran")
			case "runner_test":
				var result gen.RunnerTestResponse
				require.NoError(t, json.Unmarshal(control.Body.Bytes(), &result))
				require.True(t, result.Ok)
				require.Equal(t, "8.1.0", deref(result.CliVersion))
				require.FileExists(t, marker)
			case "smoke_test":
				var result gen.ExecutorSmokeTestResponse
				require.NoError(t, json.Unmarshal(control.Body.Bytes(), &result))
				require.True(t, result.Ok)
				require.Equal(t, "2", deref(result.ResponseText))
				require.Equal(t, 1, driverCalls, "instrument: the actual process-edge seam was reached")
			case "command_preview":
				var result gen.ExecutorCommandPreviewResponse
				require.NoError(t, json.Unmarshal(control.Body.Bytes(), &result))
				require.Equal(t, program, result.Binary)
			}
			require.NoError(t, os.Remove(program))
			w := f.call(t, http.MethodPost, route, request, "person")
			var text string
			switch surface {
			case "check_cli":
				require.Equal(t, http.StatusOK, w.Code)
				var result gen.CliValidateResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
				assert.False(t, result.Ok)
				assert.Equal(t, gen.CliValidateResponseReasonPathNotAllowed, result.Reason)
				assert.Nil(t, result.Version)
				assert.Nil(t, result.ResolvedPath)
				text = deref(result.Detail)
			case "command_preview":
				require.Equal(t, http.StatusBadRequest, w.Code)
				var result gen.ErrorResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
				text = result.Error
			case "smoke_test":
				require.Equal(t, http.StatusOK, w.Code)
				var result gen.ExecutorSmokeTestResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
				assert.False(t, result.Ok)
				assert.Nil(t, result.ResponseText)
				assert.Equal(t, 1, driverCalls, "refusal must precede any further driver creation")
				text = deref(result.Error)
			case "runner_test":
				require.Equal(t, http.StatusOK, w.Code)
				var result gen.RunnerTestResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
				assert.False(t, result.Ok)
				assert.Equal(t, gen.RunnerTestResponseReasonMissingBinary, result.Reason)
				assert.Nil(t, result.CliVersion)
				text = result.Message
			}
			r8AssertRefusalClass(t, text, "not_found")
			r8AssertCleanDiagnosticBody(t, w.Body.Bytes(), program, filepath.Dir(program),
				"r8-private-folder-marker", "r8-private-program-marker")
		})
	}
}
