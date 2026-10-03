// Tests for the FR-036 / B-62 tool_result_share_fraction REST contract matrix.
//
// Coverage pack, not a RED step: the handler already implements the contract
// (read first-hand — rest_context_settings.go::decodeContextSettingsUpdate
// rejects null / non-number / NaN / Inf / f<=0 / f>1 with a message naming the
// field and the valid interval, DisallowUnknownFields rejects unknown and the
// retired absolute_trigger_chars, and ::putContextSettings persists, then
// invokes TriggerReload and turns a failed reload into a visible 500). The
// pr-test-analyzer F1 gap was that nothing beyond share_fraction 0 was
// asserted server-side. Every expectation below is derived from FR-036 / B-62
// (docs/internal/specs/adr-066-context-overflow-spec.md), never from observed
// handler output: reject null, strings, booleans, zero, negatives, >1,
// malformed/nonfinite JSON, unknown fields and the retired field with 400
// naming the field (share values also the valid interval 0 < f <= 1); numeric
// 1 is valid; 0.125 round-trips verbatim; omission is unchanged; valid writes
// persist and invoke the existing TriggerReload; a failed reload is visible
// after the write is persisted (FR-036's persist-then-invoke ordering).

package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// shareMatrixPut PUTs one raw JSON body through the real handler and returns
// the status and body verbatim.
func shareMatrixPut(t *testing.T, api *restAPI, body string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/context", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleContextSettings(w, r)
	return w.Code, w.Body.String()
}

// shareMatrixOnDisk reads the context section the PUT must have persisted.
func shareMatrixOnDisk(t *testing.T, home string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	ctxSection, ok := onDisk["context"].(map[string]any)
	require.True(t, ok, "config.json must carry a context section: %s", string(raw))
	return ctxSection
}

// TestContextSettings_PutShareFractionMatrix is the B-62 runtime rejection
// vocabulary plus the valid boundary and round-trip cases. Rejections assert
// HTTP 400 naming the offending field; share-value rejections also assert the
// valid interval appears, per FR-036's "field validation names the field and
// valid interval". A failure in the valid cases is a live defect report, not
// an expected RED.
func TestContextSettings_PutShareFractionMatrix(t *testing.T) {
	rejections := []struct {
		name         string
		body         string
		wantField    string
		wantInterval string // optional: FR-036 requires the interval for share-value rejections
	}{
		{"null value", `{"tool_result_share_fraction":null}`, "tool_result_share_fraction", "0 < f"},
		{"string value", `{"tool_result_share_fraction":"0.5"}`, "tool_result_share_fraction", "0 < f"},
		{"boolean value", `{"tool_result_share_fraction":true}`, "tool_result_share_fraction", "0 < f"},
		{"negative value", `{"tool_result_share_fraction":-0.5}`, "tool_result_share_fraction", "0 < f"},
		{"above one", `{"tool_result_share_fraction":1.5}`, "tool_result_share_fraction", "0 < f"},
		{"nonfinite overflow literal", `{"tool_result_share_fraction":1e999}`, "tool_result_share_fraction", "0 < f"},
		{"malformed json literal", `{"tool_result_share_fraction":NaN}`, "tool_result_share_fraction", "0 < f"},
		{"unknown field", `{"nonsense_key":1}`, "nonsense_key", ""},
		{"retired field has no alias", `{"absolute_trigger_chars":400000}`, "absolute_trigger_chars", ""},
	}
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newContextSettingsAPI(t)
			code, body := shareMatrixPut(t, api, tc.body)
			require.Equal(t, http.StatusBadRequest, code, "PUT body: %s", tc.body)
			// The wire body is the ErrorResponse JSON; Go's marshaller escapes
			// '<' as <, so assert against the DECODED error text — which
			// also pins the ErrorResponse shape itself.
			var errResp struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &errResp), "body: %s", body)
			require.NotEmpty(t, errResp.Error, "a 400 must carry a nonempty error message")
			assert.Contains(t, errResp.Error, tc.wantField, "the 400 must name the offending field (FR-036)")
			if tc.wantInterval != "" {
				assert.Contains(t, errResp.Error, tc.wantInterval, "the 400 must name the valid interval (FR-036)")
			}
		})
	}

	t.Run("wholly malformed body is a visible 400", func(t *testing.T) {
		api, _ := newContextSettingsAPI(t)
		code, body := shareMatrixPut(t, api, `not-json`)
		require.Equal(t, http.StatusBadRequest, code, "body: %s", body)
		assert.Contains(t, body, "invalid JSON", "a malformed body must be a visible error, not silent success")
	})

	t.Run("valid boundary one is accepted and persisted", func(t *testing.T) {
		api, home := newContextSettingsAPI(t)
		code, body := shareMatrixPut(t, api, `{"tool_result_share_fraction":1}`)
		require.Equal(t, http.StatusOK, code, "body: %s", body)
		var resp gen.ContextSettings
		require.NoError(t, json.Unmarshal([]byte(body), &resp))
		assert.Equal(t, float64(1), resp.ToolResultShareFraction, "f=1 is valid (FR-036)")
		assert.EqualValues(t, 1, shareMatrixOnDisk(t, home)["tool_result_share_fraction"])
	})

	t.Run("twelve and a half percent round-trips as 0.125", func(t *testing.T) {
		api, home := newContextSettingsAPI(t)
		code, body := shareMatrixPut(t, api, `{"tool_result_share_fraction":0.125}`)
		require.Equal(t, http.StatusOK, code, "body: %s", body)
		var resp gen.ContextSettings
		require.NoError(t, json.Unmarshal([]byte(body), &resp))
		assert.Equal(t, 0.125, resp.ToolResultShareFraction, "B-62: 12.5% round-trips as 0.125 without conversion drift")
		assert.EqualValues(t, 0.125, shareMatrixOnDisk(t, home)["tool_result_share_fraction"])
	})

	t.Run("omission leaves the share unchanged", func(t *testing.T) {
		api, _ := newContextSettingsAPI(t)
		code, body := shareMatrixPut(t, api, `{"mcp_result_cap":40000}`)
		require.Equal(t, http.StatusOK, code, "body: %s", body)
		var resp gen.ContextSettings
		require.NoError(t, json.Unmarshal([]byte(body), &resp))
		assert.Equal(t, config.DefaultToolResultShareFraction, resp.ToolResultShareFraction,
			"B-62: omission is unchanged — an unrelated partial write must not touch the share")
	})
}

// TestContextSettings_PutInvokesReloadAndWait pins FR-036's reload contract:
// "Valid writes persist and invoke the existing TriggerReload". The harness
// agentLoop ships no reloadFunc (TriggerReload would answer
// ErrReloadNotConfigured, treated as a confirmed no-op), so these tests
// install a real closure through the exported agent.AgentLoop.SetReloadFunc —
// the reload seam itself stays real; nothing is mocked.
func TestContextSettings_PutInvokesReloadAndWait(t *testing.T) {
	t.Run("valid write invokes the reload exactly once", func(t *testing.T) {
		api, home := newContextSettingsAPI(t)
		calls := 0
		api.agentLoop.SetReloadFunc(func() error {
			calls++
			return nil
		})
		code, body := shareMatrixPut(t, api, `{"tool_result_share_fraction":0.125}`)
		require.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.Equal(t, 1, calls, "B-62: a valid write must invoke TriggerReload")
		assert.EqualValues(t, 0.125, shareMatrixOnDisk(t, home)["tool_result_share_fraction"])
	})

	t.Run("failed reload is visible after the write is persisted", func(t *testing.T) {
		api, home := newContextSettingsAPI(t)
		api.agentLoop.SetReloadFunc(func() error {
			return errors.New("matrix: simulated reload failure")
		})
		code, body := shareMatrixPut(t, api, `{"tool_result_share_fraction":1}`)
		require.Equal(t, http.StatusInternalServerError, code,
			"B-62: save/reload errors are visible — a failed reload must not be reported as a plain 200")
		assert.Contains(t, body, "reload", "the error must tell the operator the reload failed")
		// FR-036's ordering: the write persists BEFORE the reload is invoked,
		// so the value is on disk even though the reload failed.
		assert.EqualValues(t, 1, shareMatrixOnDisk(t, home)["tool_result_share_fraction"])
	})
}
