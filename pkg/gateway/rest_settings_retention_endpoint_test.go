// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_settings_retention_endpoint_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order rows 2–5:
//   - TestRetentionSettings_Ranges (D-1 / D-2, validate_inbound on and off)
//   - TestRetentionSettings_ReadsOutOfRangeStoredValue (FR-046)
//   - TestMemorySettings_RefusesRetentionFields (FR-045, review MAJ-004)
//   - TestSecurityRetentionRoute_Removed (FR-047)
//
// Oracle: docs/internal/specs/settings-redesign-spec.md — Machine-Verifiable
// Constraints, Datasets D-1 and D-2, and the BDD scenarios "The server
// enforces the ranges", "A hand-edited value outside the range is shown and
// blocks Save", "The memory settings refuse retention fields whatever the
// validation setting", "The old retention route is gone". Every expected
// status and message comes from the spec, never from the code.
//
// Requests go through the REAL production route registration
// (registerAdditionalEndpoints — the red-team real-mux pattern), so a
// missing route is a loud 404 per test, never a compile error: RED today is
// "GET/PUT /api/v1/settings/retention not implemented — required by FR-041/
// FR-042".

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

// retentionTestToken is the bearer token the harness authenticates with
// (OMNIPUS_BEARER_TOKEN env path of checkBearerAuth).
const retentionTestToken = "settings-retention-test-token"

// retentionSectionAPI is a restAPI with a home, a ValidateInbound switch and
// the REAL production route registration on a serve mux, so the tests drive
// the same chain a request in production takes.
type retentionSectionAPI struct {
	api *restAPI
	mux *http.ServeMux
	cfg *config.Config
}

func newRetentionSectionAPI(t *testing.T, validateInbound bool) *retentionSectionAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", retentionTestToken)
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, ValidateInbound: validateInbound},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return &retentionSectionAPI{api: api, mux: mux, cfg: cfg}
}

// do sends one authenticated request through the real mux. The config
// snapshot is injected into the request context exactly as
// configSnapshotMiddleware does in production (RequireNotBypass reads it).
func (h *retentionSectionAPI) do(method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, reader)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+retentionTestToken)
	r = r.WithContext(withRetentionConfigSnapshot(r.Context(), h.cfg))
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	return w
}

func withRetentionConfigSnapshot(ctx context.Context, cfg *config.Config) context.Context {
	return context.WithValue(ctx, ctxkey.ConfigContextKey{}, cfg)
}

// errorBody decodes a non-2xx ErrorResponse into its wire map.
func retentionErrorBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body),
		"the error body must be a JSON object (ErrorResponse); got: %s", w.Body.String())
	return body
}

// writeSeedConfig replaces the home's config.json with doc and asserts it
// still loads.
func writeSeedConfig(t *testing.T, h *retentionSectionAPI, doc string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(h.api.homePath, "config.json"), []byte(doc), 0o600))
	_, err := config.LoadConfig(filepath.Join(h.api.homePath, "config.json"))
	require.NoError(t, err, "the seeded config.json must load")
}

func retentionConfigPath(h *retentionSectionAPI) string {
	return filepath.Join(h.api.homePath, "config.json")
}

// TestRetentionSettings_RouteRegistered pins reachability (spec "Reachability"
// row /settings/retention): the route exists on the real mux behind auth. RED
// today: 404 because no handler is registered — the loud, per-test statement
// that GET/PUT /api/v1/settings/retention is not implemented (FR-041/FR-043).
func TestRetentionSettings_RouteRegistered(t *testing.T) {
	h := newRetentionSectionAPI(t, true)

	w := h.do(http.MethodGet, "/api/v1/settings/retention", "")
	require.NotEqual(t, http.StatusNotFound, w.Code,
		"BLOCKED RED: GET /api/v1/settings/retention is not implemented — required by FR-041 (GET returns RetentionSettings); got %d", w.Code)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// Unauthenticated request must be refused — the route is behind auth.
	anon := httptest.NewRequest(http.MethodGet, "/api/v1/settings/retention", nil)
	wAnon := httptest.NewRecorder()
	h.mux.ServeHTTP(wAnon, anon)
	require.Equal(t, http.StatusUnauthorized, wAnon.Code,
		"the retention read must stay behind withAuth (FR-043); body=%s", wAnon.Body.String())
}

// TestRetentionSettings_ReadsDefaults pins FR-041's fresh-install shape: all
// four fields present, defaults 90 / 180 / false / false. RED today: 404.
func TestRetentionSettings_ReadsDefaults(t *testing.T) {
	h := newRetentionSectionAPI(t, true)

	w := h.do(http.MethodGet, "/api/v1/settings/retention", "")
	require.Equal(t, http.StatusOK, w.Code,
		"BLOCKED RED: GET /api/v1/settings/retention is not implemented — required by FR-041; body=%s", w.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	// The read shape (RetentionSettings) has no range: all four fields are
	// required members of every answer.
	for _, field := range []string{"session_days", "memory_retros_days", "memory_retros_forever", "keep_everything_forever"} {
		require.Contains(t, got, field, "RetentionSettings must always carry %q (FR-041)", field)
	}
	// Fresh-install defaults (S-Q13 A): 90 / 180 / off / off.
	assert.Equal(t, float64(90), got["session_days"], "default chat history is 90 days")
	assert.Equal(t, float64(180), got["memory_retros_days"], "default memory is 180 days")
	assert.Equal(t, false, got["memory_retros_forever"], "Keep forever defaults to off")
	assert.Equal(t, false, got["keep_everything_forever"], "the legacy switch defaults to off")
}

// TestRetentionSettings_Ranges — order 2, Datasets D-1 and D-2, with
// validate_inbound on and off (FR-042: the handler checks every range, type
// and the empty body itself, whatever the flag is).
func TestRetentionSettings_Ranges(t *testing.T) {
	// d1 rows are D-1 (session_days); d2 rows are D-2 (memory fields).
	// wantStatus/wantMsgFragment encode the spec's Machine-Verifiable
	// Constraints; storedWant asserts the values after a refused write.
	type row struct {
		name       string
		body       string
		wantStatus int
		wantMsg    string // exact or fragment of the 400 message ("" = accept)
	}
	rows := []row{
		// D-1
		{"D1 row 2 session_days 0 is below the range", `{"session_days":0}`, 400, "session_days must be a whole number from 1 to 365"},
		{"D1 row 3 session_days 1 is the minimum", `{"session_days":1}`, 200, ""},
		{"D1 row 4 session_days 90 default", `{"session_days":90}`, 200, ""},
		{"D1 row 5 session_days 365 is the maximum", `{"session_days":365}`, 200, ""},
		{"D1 row 6 session_days 366 is above the range", `{"session_days":366}`, 400, "session_days must be a whole number from 1 to 365"},
		{"D1 row 7 session_days -1 is refused", `{"session_days":-1}`, 400, "session_days must be a whole number from 1 to 365"},
		{"D1 row 8 session_days 30.5 is not a whole number", `{"session_days":30.5}`, 400, "session_days must be a whole number from 1 to 365"},
		{"D1 row 9 session_days as a JSON string is refused", `{"session_days":"30"}`, 400, "session_days"},
		{"D1 row 11 session_days null is refused", `{"session_days":null}`, 400, "session_days"},
		// D-2
		{"D2 row 1 days 0 is below the range", `{"memory_retros_days":0}`, 400, "memory_retros_days must be a whole number from 1 to 730"},
		{"D2 row 2 days 1 is the minimum", `{"memory_retros_days":1}`, 200, ""},
		{"D2 row 3 days 180 default", `{"memory_retros_days":180}`, 200, ""},
		{"D2 row 4 days 730 is the maximum", `{"memory_retros_days":730}`, 200, ""},
		{"D2 row 5 days 731 is above the range", `{"memory_retros_days":731}`, 400, "memory_retros_days must be a whole number from 1 to 730"},
		{"D2 row 6 forever true with no days stores forever", `{"memory_retros_forever":true}`, 200, ""},
		{"D2 row 7 days are validated whenever sent, also while forever is on", `{"memory_retros_forever":true,"memory_retros_days":999}`, 400, "memory_retros_days must be a whole number from 1 to 730"},
		{"D2 row 8 forever as a string is refused", `{"memory_retros_forever":"true"}`, 400, "memory_retros_forever must be true or false"},
		{"D2 row 9 keep_everything_forever is read-only", `{"keep_everything_forever":true}`, 400, "keep_everything_forever"},
		{"D2 row 10 empty body", `{}`, 400, "at least one field required"},
		{"D2 row 11 unknown field", `{"extra":1}`, 400, "extra"},
		{"D2 row 13 forever null is refused", `{"memory_retros_forever":null}`, 400, "memory_retros_forever must be true or false"},
	}

	for _, validate := range []bool{true, false} {
		t.Run("validate_inbound_"+map[bool]string{true: "on", false: "off"}[validate], func(t *testing.T) {
			for _, tc := range rows {
				t.Run(tc.name, func(t *testing.T) {
					h := newRetentionSectionAPI(t, validate)
					w := h.do(http.MethodPut, "/api/v1/settings/retention", tc.body)
					if tc.wantMsg == "" {
						require.Equal(t, http.StatusOK, w.Code,
							"BLOCKED RED: PUT /api/v1/settings/retention is not implemented — required by FR-042/FR-043; body=%s", w.Body.String())
						// A 200 answers the read shape.
						var got map[string]any
						require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
						for _, field := range []string{"session_days", "memory_retros_days", "memory_retros_forever", "keep_everything_forever"} {
							require.Contains(t, got, field, "the 200 must answer RetentionSettings with all four fields")
						}
						return
					}
					require.Equal(t, tc.wantStatus, w.Code,
						"BLOCKED RED: PUT /api/v1/settings/retention is not implemented — required by FR-042 (got %d); body=%s", w.Code, w.Body.String())
					body := retentionErrorBody(t, w)
					errMsg, _ := body["error"].(string)
					require.Contains(t, errMsg, tc.wantMsg,
						"the 400 must name the field and its range; got: %s", errMsg)
				})
			}
		})
	}
}

// TestRetentionSettings_RangeRefusalWritesNothing pins the refusal semantics:
// a 400 leaves the stored values untouched (D-1 row 2: "0 meant default 90
// before; no longer accepted on write" — the stored config must not change).
func TestRetentionSettings_RangeRefusalWritesNothing(t *testing.T) {
	for _, validate := range []bool{true, false} {
		t.Run("validate_inbound_"+map[bool]string{true: "on", false: "off"}[validate], func(t *testing.T) {
			h := newRetentionSectionAPI(t, validate)
			seed := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"storage":{"retention":{"session_days":30,"memory_retros_days":180}}}`
			writeSeedConfig(t, h, seed)
			before, err := os.ReadFile(retentionConfigPath(h))
			require.NoError(t, err)

			w := h.do(http.MethodPut, "/api/v1/settings/retention", `{"session_days":366}`)
			require.Equal(t, http.StatusBadRequest, w.Code,
				"BLOCKED RED: PUT /api/v1/settings/retention is not implemented — required by FR-042; body=%s", w.Body.String())
			after, err := os.ReadFile(retentionConfigPath(h))
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "a refused write must change nothing")
		})
	}
}

// TestRetentionSettings_ReadsOutOfRangeStoredValue — order 3, FR-046: the
// read schema has no range, so a hand-edited 500 is returned as stored (the
// screen shows it with the range error and blocks Save until fixed).
func TestRetentionSettings_ReadsOutOfRangeStoredValue(t *testing.T) {
	h := newRetentionSectionAPI(t, true)
	seed := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"storage":{"retention":{"session_days":500,"memory_retros_days":1000}}}`
	writeSeedConfig(t, h, seed)

	w := h.do(http.MethodGet, "/api/v1/settings/retention", "")
	require.Equal(t, http.StatusOK, w.Code,
		"BLOCKED RED: GET /api/v1/settings/retention is not implemented — required by FR-046 (GET returns a hand-edited value as stored); body=%s", w.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, float64(500), got["session_days"], "a hand-edited 500 must be returned as stored, not clamped and not an error")
	assert.Equal(t, float64(1000), got["memory_retros_days"], "a hand-edited 1000 must be returned as stored")
	assert.Equal(t, false, got["keep_everything_forever"], "the legacy switch stays off")

	// Repair: after entering 365 (in range) and saving, GET returns 365.
	wPut := h.do(http.MethodPut, "/api/v1/settings/retention", `{"session_days":365}`)
	require.Equal(t, http.StatusOK, wPut.Code,
		"BLOCKED RED: PUT /api/v1/settings/retention is not implemented — required by FR-042; body=%s", wPut.Body.String())
	wGet := h.do(http.MethodGet, "/api/v1/settings/retention", "")
	require.Equal(t, http.StatusOK, wGet.Code)
	var repaired map[string]any
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &repaired))
	assert.Equal(t, float64(365), repaired["session_days"], "the repaired value must read back 365")
}

// TestMemorySettings_RefusesRetentionFields — order 4, FR-045, review
// MAJ-004: PUT /settings/memory carrying session_days or memory_retros_days
// answers 400 naming the field and changes NOTHING — including the other
// fields of that body — whatever gateway.validate_inbound is.
func TestMemorySettings_RefusesRetentionFields(t *testing.T) {
	bodies := []struct {
		name         string
		body         string
		wantInErrMsg string
	}{
		{"session_days only", `{"session_days":30}`, "session_days"},
		{"memory_retros_days only", `{"memory_retros_days":365}`, "memory_retros_days"},
		{"mixed body: recap field plus session_days", `{"auto_recap_enabled": false, "session_days": 30}`, "session_days"},
		{"mixed body: recap field plus memory_retros_days", `{"auto_recap_enabled": false, "memory_retros_days": 365}`, "memory_retros_days"},
	}
	for _, validate := range []bool{true, false} {
		t.Run("validate_inbound_"+map[bool]string{true: "on", false: "off"}[validate], func(t *testing.T) {
			for _, tc := range bodies {
				t.Run(tc.name, func(t *testing.T) {
					h := newRetentionSectionAPI(t, validate)
					seed := `{"version":1,"agents":{"defaults":{"auto_recap_enabled":true},"list":[]},"providers":[],"storage":{"retention":{"session_days":90,"memory_retros_days":180}}}`
					writeSeedConfig(t, h, seed)
					before, err := os.ReadFile(retentionConfigPath(h))
					require.NoError(t, err)

					w := h.do(http.MethodPut, "/api/v1/settings/memory", tc.body)
					require.Equal(t, http.StatusBadRequest, w.Code,
						"RED: PUT /settings/memory still accepts retention fields — required by FR-045 to refuse them (got %d); body=%s", w.Code, w.Body.String())
					errMsg, _ := retentionErrorBody(t, w)["error"].(string)
					require.Contains(t, errMsg, tc.wantInErrMsg,
						"the 400 must name the retired field; got: %s", errMsg)

					// Nothing changed — config.json byte-identical, so the
					// recap setting in the body was not applied either.
					after, err := os.ReadFile(retentionConfigPath(h))
					require.NoError(t, err)
					require.Equal(t, string(before), string(after),
						"the refusal must change nothing, including the other fields of the body")
				})
			}
		})
	}
}

// TestSecurityRetentionRoute_Removed — order 5, FR-047: GET and PUT
// /security/retention answer 404 after the route is removed;
// POST /security/retention/sweep keeps working unchanged. RED today: both
// GET and PUT still answer 200/400 (the route exists).
func TestSecurityRetentionRoute_Removed(t *testing.T) {
	h := newRetentionSectionAPI(t, true)

	wGet := h.do(http.MethodGet, "/api/v1/security/retention", "")
	require.Equal(t, http.StatusNotFound, wGet.Code,
		"RED: GET /security/retention still exists (got %d) — FR-047 removes it; body=%s", wGet.Code, wGet.Body.String())

	wPut := h.do(http.MethodPut, "/api/v1/security/retention", `{"session_days":30}`)
	require.Equal(t, http.StatusNotFound, wPut.Code,
		"RED: PUT /security/retention still exists (got %d) — FR-047 removes it; body=%s", wPut.Code, wPut.Body.String())

	// The sweep stays (FR-047: "POST /security/retention/sweep MUST keep
	// working unchanged"). The exact sweep outcome (200/409/500 depending on
	// stores) is the sweep tests' own subject, pinned by the existing
	// rest_retention sweep cases that must stay green; here the oracle is
	// only that the route itself is not removed.
	wSweep := h.do(http.MethodPost, "/api/v1/security/retention/sweep", "")
	require.NotEqual(t, http.StatusNotFound, wSweep.Code,
		"the sweep route must stay registered (FR-047); got %d, body=%s", wSweep.Code, wSweep.Body.String())
}
