package gateway

// T11 §4 / FR-9, FR-12 and ExecutorConfig.program_warning (497b8e607):
// no saved Settings program refuses instead of falling back to PATH; a saved
// file is checked again on every request. These pin already-present resolver
// behavior, not a claim of new RED proof; independent mutation is for CHECK.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Literals come from the amended spec/contract, never NoPathText or
// CheckFailedText in the implementation under test.
const t11bCodexNoPath = "This agent cannot run: no program path is set for Codex. A person can set it in Settings → Third-party agents → Codex."

func t11bMissingProgramText(name, path string) string {
	return fmt.Sprintf("This agent cannot run: the program set for %s in Settings → Third-party agents does not pass its checks (not_found at %s). A person can fix it there.", name, path)
}

func t11bCodexOnPath(t *testing.T) string {
	t.Helper()
	program := t11bWriteExe(t, "codex")
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	t.Setenv("PATH", filepath.Dir(program))
	found, err := exec.LookPath("codex")
	require.NoError(t, err, "instrument: a real harmless default-name program is available")
	require.Equal(t, program, found, "instrument: PATH cannot select a host-installed CLI")
	return program
}

func t11bPreviewResponse(t *testing.T, api *restAPI) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(gen.ExecutorCommandPreviewRequest{Cli: gen.ExternalCliToolCodex})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-preview", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	return w
}

func TestPostAgentsExecutorPreview_NoSavedPath_RefusedWithRepairMessage(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv(config.EnvHome, t.TempDir())
	program := t11bCodexOnPath(t)

	w := t11bPreviewResponse(t, api)
	require.Equal(t, http.StatusBadRequest, w.Code, "a program on PATH is not a saved Settings path")
	var refusal gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refusal))
	assert.Equal(t, t11bCodexNoPath, refusal.Error)

	// Positive control: the same executable and handler succeed once the
	// person actually saves that path; no default-name fallback is necessary.
	t11bSaveProgram(t, api, "codex", program)
	code, preview := postExecutorPreview(t, api, gen.ExecutorCommandPreviewRequest{Cli: gen.ExternalCliToolCodex})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, program, preview.Binary)
}

func TestPostAgentsExecutorPreview_InvalidSavedPath_RefusedWithRepairMessage(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv(config.EnvHome, t.TempDir())
	program, err := filepath.EvalSymlinks(t11bWriteExe(t, "codex-saved"))
	require.NoError(t, err)
	t11bSaveProgram(t, api, "codex", program)
	control := t11bPreviewResponse(t, api)
	require.Equal(t, http.StatusOK, control.Code, "instrument: saved path is valid before its file disappears")
	require.NoError(t, os.Remove(program))

	w := t11bPreviewResponse(t, api)
	require.Equal(t, http.StatusBadRequest, w.Code, "the saved file is rechecked on every preview")
	var refusal gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refusal))
	assert.Equal(t, t11bMissingProgramText("Codex", program), refusal.Error)
}

// Only the external process edge is faked, so a broken refusal cannot launch a
// host CLI. Positive requests prove the very same factory and runner are live.
func t11bSmokeFactory(t *testing.T) (*capturingFakeDriver, *int) {
	t.Helper()
	fake := &capturingFakeDriver{}
	calls := 0
	original := smokeTestNewDriver
	smokeTestNewDriver = func(cli string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		assert.Equal(t, "codex", cli)
		calls++
		return fake, nil
	}
	t.Cleanup(func() { smokeTestNewDriver = original })
	return fake, &calls
}

func t11bAssertSmokeControl(t *testing.T, fake *capturingFakeDriver, result gen.ExecutorSmokeTestResponse, program string) {
	t.Helper()
	require.True(t, result.Ok)
	require.NotNil(t, result.ResponseText)
	assert.Equal(t, "2", *result.ResponseText)
	assert.Nil(t, result.Error)
	resolved, err := filepath.EvalSymlinks(program)
	require.NoError(t, err)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.True(t, fake.captured, "instrument: the positive request really reached Run")
	assert.Equal(t, resolved, fake.gotOpts.CLIPath)
}

func TestPostAgentsExecutorSmokeTest_NoSavedPath_RefusedWithRepairMessage(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv(config.EnvHome, t.TempDir())
	program := t11bCodexOnPath(t)
	fake, calls := t11bSmokeFactory(t)

	code, refusal := postExecutorSmokeTest(t, api, `{"cli":"codex"}`)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, refusal.Ok)
	require.NotNil(t, refusal.Error)
	assert.Equal(t, t11bCodexNoPath, *refusal.Error)
	assert.Nil(t, refusal.ResponseText)
	assert.Zero(t, *calls, "no saved Settings path means no driver creation, even with Codex on PATH")

	t11bSaveProgram(t, api, "codex", program)
	code, control := postExecutorSmokeTest(t, api, `{"cli":"codex"}`)
	require.Equal(t, http.StatusOK, code)
	t11bAssertSmokeControl(t, fake, control, program)
	assert.Equal(t, 1, *calls)
}

func TestPostAgentsExecutorSmokeTest_InvalidSavedPath_RefusedWithRepairMessage(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t.Setenv(config.EnvHome, t.TempDir())
	program, err := filepath.EvalSymlinks(t11bWriteExe(t, "codex-saved"))
	require.NoError(t, err)
	t11bSaveProgram(t, api, "codex", program)
	fake, calls := t11bSmokeFactory(t)
	code, control := postExecutorSmokeTest(t, api, `{"cli":"codex"}`)
	require.Equal(t, http.StatusOK, code)
	t11bAssertSmokeControl(t, fake, control, program)
	require.Equal(t, 1, *calls)
	require.NoError(t, os.Remove(program))

	code, refusal := postExecutorSmokeTest(t, api, `{"cli":"codex"}`)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, refusal.Ok)
	require.NotNil(t, refusal.Error)
	assert.Equal(t, t11bMissingProgramText("Codex", program), *refusal.Error)
	assert.Nil(t, refusal.ResponseText)
	assert.Equal(t, 1, *calls, "the invalid saved path is refused before any additional driver creation")
}
