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

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
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

	// max_tool_iterations was omitted, so the preview must reflect the SAME
	// default a real run would apply (agent.DefaultExternalMaxTurns) — see
	// TestPostAgentsExecutorPreview_MaxToolIterations_DefaultsToExternalMaxTurns
	// for dedicated coverage of that behavior; this test's own wantArgv must
	// stay in sync with it rather than assuming MaxTurns:0.
	wantArgv := runner.BuildClaudeArgs(
		runner.RunOptions{Model: "sonnet", Input: "<prompt>", MaxTurns: agent.DefaultExternalMaxTurns},
	)
	assert.Equal(t, wantArgv, resp.Argv)
	assert.Equal(t, "claude", resp.Binary)
	assert.Equal(t, gen.Stdin, resp.PromptDelivery)
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
	assert.Equal(t, gen.Stdin, resp.PromptDelivery)
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
	assert.Equal(t, gen.PositionalArgumentAfter, resp.PromptDelivery)
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

// TestPostAgentsExecutorPreview_MaxToolIterations_DefaultsToExternalMaxTurns
// proves omitting max_tool_iterations previews with the SAME default a real
// external-CLI dispatch applies (agent.DefaultExternalMaxTurns, referenced
// directly — see runExternalCLISubTurn's "maxTurns <= 0 -> default"
// fallback in external_dispatch.go), instead of silently showing no
// --max-turns flag at all (the bug this test guards against).
func TestPostAgentsExecutorPreview_MaxToolIterations_DefaultsToExternalMaxTurns(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli": "claude-code",
	})
	require.Equal(t, http.StatusOK, code)

	wantFlag := fmt.Sprintf("--max-turns %d", agent.DefaultExternalMaxTurns)
	assert.Contains(t, resp.CommandLine, wantFlag,
		"omitted max_tool_iterations must preview with the real dispatch default; command_line=%q", resp.CommandLine)

	found := false
	for i, a := range resp.Argv {
		if a == "--max-turns" && i+1 < len(resp.Argv) &&
			resp.Argv[i+1] == fmt.Sprintf("%d", agent.DefaultExternalMaxTurns) {
			found = true
		}
	}
	assert.True(t, found, "argv must contain --max-turns %d; argv=%v", agent.DefaultExternalMaxTurns, resp.Argv)
}

// TestPostAgentsExecutorPreview_MaxToolIterations_ExplicitZero_AlsoDefaults
// proves an explicit zero ALSO previews with the default — mirroring
// runExternalCLISubTurn's "maxTurns <= 0" fallback (not just "field is nil").
func TestPostAgentsExecutorPreview_MaxToolIterations_ExplicitZero_AlsoDefaults(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":                 "claude-code",
		"max_tool_iterations": 0,
	})
	require.Equal(t, http.StatusOK, code)
	wantFlag := fmt.Sprintf("--max-turns %d", agent.DefaultExternalMaxTurns)
	assert.Contains(t, resp.CommandLine, wantFlag)
}

// TestPostAgentsExecutorPreview_MaxToolIterations_ExplicitValueRespected
// proves a positive explicit value overrides the default rather than always
// previewing with the fallback.
func TestPostAgentsExecutorPreview_MaxToolIterations_ExplicitValueRespected(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, resp := postExecutorPreview(t, api, map[string]any{
		"cli":                 "claude-code",
		"max_tool_iterations": 7,
	})
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, resp.CommandLine, "--max-turns 7")
	assert.NotContains(t, resp.CommandLine, fmt.Sprintf("--max-turns %d", agent.DefaultExternalMaxTurns))
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

// TestPostAgentsExecutorPreview_DoesNotShadowAgentLookup proves the
// "executor-preview" reserved path segment does not swallow requests for a
// real (unrelated) agent ID.
func TestPostAgentsExecutorPreview_DoesNotShadowAgentLookup(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/some-other-agent-id", nil)
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
