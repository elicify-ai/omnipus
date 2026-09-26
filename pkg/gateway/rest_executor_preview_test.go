// rest_executor_preview_test.go — coverage for POST /api/v1/agents/executor-preview
// (rest_executor_preview.go). Proves the endpoint computes argv via the REAL
// per-driver buildArgs() (through the runner package's cross-package export)
// rather than a hand-maintained approximation, that a dangerous cli_args
// token is dropped with a reason instead of silently applied, and that
// opencode's model_dropped_reason fires exactly when BuildOpencodeArgs itself
// would omit --model.

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// postExecutorPreview issues POST /api/v1/agents/executor-preview with the
// given JSON body (already marshaled by the caller) and decodes a 200
// response. Returns the raw status code and, on success, the decoded body.
func postExecutorPreview(t *testing.T, api *restAPI, body any) (int, gen.ExecutorCommandPreviewResponse) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-preview", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	var resp gen.ExecutorCommandPreviewResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w.Code, resp
}

// TestPostAgentsExecutorPreview_Claude_HappyPath asserts the exact expected
// argv/command_line for a claude-code preview, cross-checked against the
// REAL runner.BuildClaudeArgs output for the same RunOptions shape (the
// endpoint must never diverge from it).
func TestPostAgentsExecutorPreview_Claude_HappyPath(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":   "claude-code",
		"model": "sonnet",
	})
	require.Equal(t, http.StatusOK, code)

	// max_tool_iterations was omitted, so the preview must show the
	// resolver's effective value = the global limit (#904 D4, FR-004). This
	// harness saves no global, so the global in force is the shipped default
	// 200 (spec Dataset "Saved global" row 2 / D13) — see
	// TestPostAgentsExecutorPreview_MaxToolIterations_DefaultsToGlobalLimit.
	wantArgv := runner.BuildClaudeArgs(
		runner.RunOptions{Model: "sonnet", Input: "<prompt>", MaxTurns: mtiShippedDefaultGlobal},
	)
	assert.Equal(t, wantArgv, resp.Argv)
	assert.Equal(t, "claude", resp.Binary)
	assert.Equal(t, gen.ExecutorCommandPreviewResponsePromptDeliveryStdin, resp.PromptDelivery)
	assert.Empty(t, resp.DroppedArgs)
	assert.Nil(t, resp.ModelDroppedReason)

	wantCmd := "claude"
	for _, a := range wantArgv {
		wantCmd += " " + a
	}
	assert.Equal(t, wantCmd, resp.CommandLine)

	// Prompt token itself must never appear in argv for claude-code (stdin
	// delivery) — the "<prompt>" placeholder is inert for this driver.
	for _, a := range resp.Argv {
		assert.NotEqual(t, "<prompt>", a)
	}
}

// TestPostAgentsExecutorPreview_Codex_HappyPath mirrors the claude-code
// happy-path test for codex, including the cli_path override reflected in
// "binary" and the flag-ordering guarantee (--ask-for-approval before exec).
func TestPostAgentsExecutorPreview_Codex_HappyPath(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":      "codex",
		"model":    "gpt-5-codex",
		"cli_path": "/usr/local/bin/codex",
	})
	require.Equal(t, http.StatusOK, code)

	wantArgv := runner.BuildCodexArgs(runner.RunOptions{
		Model: "gpt-5-codex", CLIPath: "/usr/local/bin/codex", Input: "<prompt>",
	})
	assert.Equal(t, wantArgv, resp.Argv)
	assert.Equal(t, "/usr/local/bin/codex", resp.Binary)
	assert.Equal(t, gen.ExecutorCommandPreviewResponsePromptDeliveryStdin, resp.PromptDelivery)
	assert.Empty(t, resp.DroppedArgs)
	assert.Nil(t, resp.ModelDroppedReason)
}

// TestPostAgentsExecutorPreview_Opencode_HappyPath mirrors the happy-path
// test for opencode, including the trailing "--" + "<prompt>" placeholder
// tokens the response schema documents as the real prompt's position.
func TestPostAgentsExecutorPreview_Opencode_HappyPath(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":   "opencode",
		"model": "anthropic/claude-3-5-sonnet",
	})
	require.Equal(t, http.StatusOK, code)

	wantArgv := runner.BuildOpencodeArgs(runner.RunOptions{
		Model: "anthropic/claude-3-5-sonnet", Input: "<prompt>",
	})
	assert.Equal(t, wantArgv, resp.Argv)
	assert.Equal(t, "opencode", resp.Binary)
	assert.Equal(t, gen.ExecutorCommandPreviewResponsePromptDeliveryPositionalArgumentAfter, resp.PromptDelivery)
	assert.Nil(t, resp.ModelDroppedReason)

	require.NotEmpty(t, resp.Argv)
	assert.Equal(
		t,
		"<prompt>",
		resp.Argv[len(resp.Argv)-1],
		"the placeholder prompt must be the last argv token for opencode",
	)
	assert.Equal(
		t,
		"--",
		resp.Argv[len(resp.Argv)-2],
		"the -- end-of-options separator must immediately precede the prompt placeholder",
	)
}

// TestPostAgentsExecutorPreview_DangerousCLIArgDropped_WithReason proves a
// denylisted cli_args token (a REDUNDANT --dangerously-skip-permissions for
// claude, issue #488: the driver now passes this flag unconditionally itself)
// is deduplicated out of the operator-supplied cli_args and shows up in
// dropped_args with a non-empty reason, while the driver's own single copy of
// the flag still legitimately appears in argv exactly once — the exact
// requirement driving this endpoint.
func TestPostAgentsExecutorPreview_DangerousCLIArgDropped_WithReason(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":      "claude-code",
		"cli_args": "--add-dir /tmp/x --dangerously-skip-permissions",
	})
	require.Equal(t, http.StatusOK, code)

	occurrences := 0
	for _, a := range resp.Argv {
		if a == "--dangerously-skip-permissions" {
			occurrences++
		}
	}
	assert.Equal(
		t,
		1,
		occurrences,
		"the driver's own unconditional copy must appear exactly once; the operator's redundant copy must be deduplicated; argv=%v",
		resp.Argv,
	)
	require.Len(t, resp.DroppedArgs, 1)
	assert.Equal(t, "--dangerously-skip-permissions", resp.DroppedArgs[0].Flag)
	assert.NotEmpty(t, resp.DroppedArgs[0].Reason, "dropped_args entry must carry a non-empty reason")

	// The benign token must still be kept.
	found := false
	for i, a := range resp.Argv {
		if a == "--add-dir" && i+1 < len(resp.Argv) && resp.Argv[i+1] == "/tmp/x" {
			found = true
		}
	}
	assert.True(t, found, "benign cli_args token must be preserved in argv; argv=%v", resp.Argv)
}

// TestPostAgentsExecutorPreview_Opencode_ModelDroppedReason proves a
// non-"provider/model"-shaped model previewed for opencode populates
// model_dropped_reason and omits --model from argv, matching what
// BuildOpencodeArgs itself does internally.
func TestPostAgentsExecutorPreview_Opencode_ModelDroppedReason(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":   "opencode",
		"model": "gpt-5-codex",
	})
	require.Equal(t, http.StatusOK, code)

	require.NotNil(t, resp.ModelDroppedReason)
	assert.NotEmpty(t, *resp.ModelDroppedReason)
	for _, a := range resp.Argv {
		assert.NotEqual(t, "--model", a, "--model must be absent when the model is not provider/model-shaped")
	}
}

// TestPostAgentsExecutorPreview_ClaudeCode_ModelDroppedReasonNeverSet proves
// claude-code (which accepts any non-empty model string as-is) never sets
// model_dropped_reason, even for a value that would fail opencode's shape
// check.
func TestPostAgentsExecutorPreview_ClaudeCode_ModelDroppedReasonNeverSet(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":   "claude-code",
		"model": "not-provider-shaped",
	})
	require.Equal(t, http.StatusOK, code)
	assert.Nil(t, resp.ModelDroppedReason)
	assert.Contains(t, resp.Argv, "not-provider-shaped")
}

// mtiShippedDefaultGlobal is the shipped default global limit (#904 spec
// FR-001: "range 1–1000, shipped default 200"; D13: a missing saved global
// runs as 200). executorDefaultsTestAPI saves no global.
const mtiShippedDefaultGlobal = 200

// argvTurnCap returns the value following --max-turns in argv, or "".
func mtiArgvTurnCap(argv []string) string {
	for i, a := range argv {
		if a == "--max-turns" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// TestPostAgentsExecutorPreview_MaxToolIterations_DefaultsToGlobalLimit —
// #904 Scenario "Worker preview equals runtime with no own value" (D4): an
// omitted max_tool_iterations previews the global limit in force (200 here),
// the value a real run passes — never the retired 50
// (DefaultExternalMaxTurns is removed by FR-004).
func TestPostAgentsExecutorPreview_MaxToolIterations_DefaultsToGlobalLimit(t *testing.T) {
	mtiUnsetEnv(t)
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli": "claude-code",
	})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, fmt.Sprint(mtiShippedDefaultGlobal), mtiArgvTurnCap(resp.Argv),
		"omitted max_tool_iterations must preview the global limit; argv=%v", resp.Argv)
	assert.Contains(t, resp.CommandLine, fmt.Sprintf("--max-turns %d", mtiShippedDefaultGlobal))
}

// TestPostAgentsExecutorPreview_MaxToolIterations_ExplicitZero_Refused —
// #904 FR-006: 0 is outside 1–1000 on every writable field including the
// executor preview (ExecutorCommandPreviewRequest minimum: 1); it no longer
// means "use the default".
func TestPostAgentsExecutorPreview_MaxToolIterations_ExplicitZero_Refused(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	for _, v := range []int{0, 1001} {
		code, _ := postExecutorPreview(t, api, map[string]any{
			"cli":                 "claude-code",
			"max_tool_iterations": v,
		})
		assert.Equal(t, http.StatusBadRequest, code, "max_tool_iterations=%d must be refused (1–1000)", v)
	}
}

// TestPostAgentsExecutorPreview_MaxToolIterations_ExplicitValueRespected —
// #904 Scenario "Worker preview equals runtime with own value": an own
// value below the global previews as itself.
func TestPostAgentsExecutorPreview_MaxToolIterations_ExplicitValueRespected(t *testing.T) {
	mtiUnsetEnv(t)
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":                 "claude-code",
		"max_tool_iterations": 30,
	})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "30", mtiArgvTurnCap(resp.Argv))
}

// TestPostAgentsExecutorPreview_MaxToolIterations_AboveGlobalCapped — #904
// contract row ExecutorCommandPreviewRequest: "The server previews the
// resolver's effective value (min(global, value))". 300 with global 200
// previews 200, exactly what a run of that (capped-and-flagged) worker passes.
func TestPostAgentsExecutorPreview_MaxToolIterations_AboveGlobalCapped(t *testing.T) {
	mtiUnsetEnv(t)
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":                 "claude-code",
		"max_tool_iterations": 300,
	})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, fmt.Sprint(mtiShippedDefaultGlobal), mtiArgvTurnCap(resp.Argv))
}

// TestPostAgentsExecutorPreview_UnknownCLI_400 proves an unsupported cli
// value is rejected with 400, not a panic or a silently-empty argv.
func TestPostAgentsExecutorPreview_UnknownCLI_400(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, _ := postExecutorPreview(t, api, map[string]any{"cli": "gemini-cli"})
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestPostAgentsExecutorPreview_MissingCLI_400 proves an empty/absent cli
// field is rejected with 400 rather than defaulting to some CLI.
func TestPostAgentsExecutorPreview_MissingCLI_400(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, _ := postExecutorPreview(t, api, map[string]any{"model": "sonnet"})
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestPostAgentsExecutorPreview_InvalidJSON_400 proves a malformed body is
// rejected with 400 and a helpful error, not a 500.
func TestPostAgentsExecutorPreview_InvalidJSON_400(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-preview", bytes.NewReader([]byte("{not json")))
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestPostAgentsExecutorPreview_MethodNotAllowed proves GET is rejected —
// this is a stateless computation endpoint, POST-only.
func TestPostAgentsExecutorPreview_MethodNotAllowed(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/executor-preview", nil)
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
