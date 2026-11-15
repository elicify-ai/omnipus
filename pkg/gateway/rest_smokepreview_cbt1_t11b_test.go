// T11-B §4 X4 / C-B1 / FR-12: neither request may choose a free cli_path.
// Real handlers and generated carriers; a fake external-driver edge prevents
// any host CLI/model execution, including when the rejection regresses.
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// Inject the forbidden member only at the raw malformed-request boundary;
// never add a handwritten request struct that outlives C-B1 regeneration.
func t11bWithForbiddenPath(t *testing.T, request any) string {
	t.Helper()
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &object))
	path, err := json.Marshal(filepath.Join(t.TempDir(), "does-not-exist"))
	require.NoError(t, err)
	object["cli_path"] = path
	raw, err = json.Marshal(object)
	require.NoError(t, err)
	return string(raw)
}

func TestSmokeAndPreview_RejectFreePath(t *testing.T) {
	for _, validate := range []bool{false, true} {
		for _, endpoint := range []string{"executor-preview", "executor-smoke-test"} {
			t.Run(endpoint+"/validate="+strconv.FormatBool(validate), func(t *testing.T) {
				api, cleanup := newTestRestAPI(t)
				defer cleanup()
				api.agentLoop.GetConfig().Gateway.ValidateInbound = validate
				factoryCalls := 0
				original := smokeTestNewDriver
				smokeTestNewDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
					factoryCalls++
					return &capturingFakeDriver{}, nil
				}
				t.Cleanup(func() { smokeTestNewDriver = original })
				var body string
				if endpoint == "executor-preview" {
					body = t11bWithForbiddenPath(t, gen.ExecutorCommandPreviewRequest{Cli: gen.ExternalCliToolCodex})
				} else {
					body = t11bWithForbiddenPath(t, gen.ExecutorSmokeTestRequest{Cli: gen.ExternalCliToolCodex})
				}
				r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+endpoint, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.RemoteAddr = t.Name() + ":0"
				w := httptest.NewRecorder()
				api.HandleAgents(w, r)
				require.Equal(t, http.StatusBadRequest, w.Code, "C-B1: cli_path is forbidden in BOTH validation modes; body=%s", w.Body.String())
				var refusal gen.ErrorResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refusal))
				require.Contains(t, refusal.Error, "cli_path", "the refusal must identify the forbidden field, not pass because no Settings path is saved")
				require.Zero(t, factoryCalls, "free path refused before creating a runner")
			})
		}
	}
}

func TestCommandPreview_WithoutFreePathUsesSavedSettingsPath(t *testing.T) {
	api, mux, _ := t11bSettingsAPI(t)
	path := t11bWriteExe(t, "codex-saved-preview")
	body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: path}, gen.CliPreset{})
	w := t11bPut(t, mux, body)
	require.Equal(t, http.StatusOK, w.Code, "save prerequisite: %s", w.Body.String())
	request := gen.ExecutorCommandPreviewRequest{Cli: gen.ExternalCliToolCodex}
	code, preview := postExecutorPreview(t, api, request)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, path, preview.Binary, "C-B1: preview without cli_path reads Settings, not $PATH or a per-agent path")
}

func TestSmokeTest_WithoutFreePathUsesSavedSettingsPath(t *testing.T) {
	api, mux, _ := t11bSettingsAPI(t)
	path := t11bWriteExe(t, "codex-saved-smoke")
	body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: path}, gen.CliPreset{})
	w := t11bPut(t, mux, body)
	require.Equal(t, http.StatusOK, w.Code, "save prerequisite: %s", w.Body.String())
	fake := &capturingFakeDriver{}
	original := smokeTestNewDriver
	smokeTestNewDriver = func(cli string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		require.Equal(t, "codex", cli)
		return fake, nil
	}
	t.Cleanup(func() { smokeTestNewDriver = original })
	request := gen.ExecutorSmokeTestRequest{Cli: gen.ExternalCliToolCodex}
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	code, result := postExecutorSmokeTest(t, api, string(raw))
	require.Equal(t, http.StatusOK, code)
	require.True(t, result.Ok, "fake process yields an actual output; body error=%v", result.Error)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.True(t, fake.captured, "smoke test must actually reach its external process boundary")
	want, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	require.Equal(t, want, fake.gotOpts.CLIPath, "C-B1: smoke uses the resolved saved program, never $PATH")
}
