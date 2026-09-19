// rest_status_test.go: tests for doctor, app state, version, devices, activity, storage stats

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// TestHandleActivity_ReturnsWrappedResponseNoWarning verifies GET
// /api/v1/activity returns the wire-contract ActivityEventsResponse shape
// ({"events": [...]}) — not a bare JSON array — when there is nothing to warn
// about, and that the warning key is absent (contracts/components/schemas/
// ActivityEventsResponse.yaml: warning is optional).
func TestHandleActivity_ReturnsWrappedResponseNoWarning(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop: al,
		taskStore: task.New(tmpDir + "/tasks"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/activity", nil)
	api.HandleActivity(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	events, hasEvents := resp["events"]
	require.True(t, hasEvents,
		"response must be the ActivityEventsResponse object shape ({\"events\": [...]}), not a bare array")
	assert.NotNil(t, events, "events must be a non-null (possibly empty) array")
	_, hasWarning := resp["warning"]
	assert.False(t, hasWarning, "warning key must be absent when there is nothing to warn about")
}

// TestHandleActivity_PartialFailure_SurfacesWarning verifies the Backend-High
// fix: when session listing partially fails for one agent, the warning
// computed internally (sessionWarning in HandleActivity) is returned to the
// caller via ActivityEventsResponse.Warning instead of being discarded after
// only a slog.Warn call.
func TestHandleActivity_PartialFailure_SurfacesWarning(t *testing.T) {
	if os.Getuid() == 0 {
		// Root bypasses DAC, so chmod 0o000 does not prevent os.ReadDir. This
		// mirrors pkg/agent/list_all_sessions_test.go's TestListAllSessions_PartialErrors.
		t.Skip("permission-based failure injection is ineffective under root; run as non-root")
	}
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "agent-broken", Name: "Broken Agent", Home: tmpDir},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// Wire a broken UnifiedStore for "agent-broken": create the store then
	// remove all read permission on its base dir so ListSessions fails,
	// forcing ListAllSessions to return a partial error for this agent.
	brokenBaseDir := t.TempDir()
	brokenStore, err := session.NewUnifiedStore(brokenBaseDir)
	require.NoError(t, err, "NewUnifiedStore(agent-broken)")
	require.NoError(t, os.Chmod(brokenBaseDir, 0o000), "chmod brokenBaseDir")
	t.Cleanup(func() { _ = os.Chmod(brokenBaseDir, 0o700) }) // restore for temp-dir cleanup

	brokenAgent, ok := al.GetRegistry().GetAgent("agent-broken")
	require.True(t, ok, "agent-broken must be registered")
	brokenAgent.Sessions = brokenStore

	api := &restAPI{
		agentLoop: al,
		taskStore: task.New(tmpDir + "/tasks"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/activity", nil)
	api.HandleActivity(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"a partial session-listing failure must not fail the whole request; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	warning, _ := resp["warning"].(string)
	assert.NotEmpty(t, warning,
		"warning must be surfaced to the caller (previously discarded after only a slog.Warn call)")
	assert.Contains(t, warning, "agent-broken", "warning should identify the affected agent")
	_, hasEvents := resp["events"]
	assert.True(t, hasEvents, "events key must still be present alongside the warning")
}

// TestHandleDevices_DisabledByDefault verifies that GET /api/v1/devices
// returns 404 when Sandbox.Experimental.DevicePairingEnabled is false (the
// default) — the device-pairing feature is dark-launched.
func TestHandleDevices_DisabledByDefault(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	rec := httptest.NewRecorder()
	api.HandleDevices(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code,
		"GET /api/v1/devices must 404 when device pairing is not enabled (default)")
}

// TestHandleDevices_EnabledReturnsEmptyArrays verifies that once the flag is
// enabled, GET /api/v1/devices returns 200 with the (still-stub) empty arrays.
func TestHandleDevices_EnabledReturnsEmptyArrays(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.agentLoop.GetConfig().Sandbox.Experimental.DevicePairingEnabled = true

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	rec := httptest.NewRecorder()
	api.HandleDevices(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body, "pending")
	assert.Contains(t, body, "paired")
}

// --- E4: Stub/new endpoint tests ---

// TestHandleStateGET verifies GET /api/v1/state returns 200 with onboarding_complete field.
// BDD: Given a fresh install,
// When GET /api/v1/state is called,
// Then 200 with {"onboarding_complete": false}.
// Traces to: wave5b-system-agent-spec.md — Scenario: App state endpoint (E4)
func TestHandleStateGET(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	api.HandleState(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, hasField := resp["onboarding_complete"]
	assert.True(t, hasField, "response must contain 'onboarding_complete' field")
	assert.Equal(t, false, resp["onboarding_complete"], "fresh install must have onboarding_complete=false")
}

// TestHandleStateGET_IdentityModeByEdition pins ADR-0010 / login-and-
// onboarding-spec.md §2.2: `identity.mode` mirrors config.EditionAuthMode(),
// which is DERIVED from the stamped config.Edition, never read from a
// request or a config key. core -> local; hosted -> platform. Uses the
// withEdition-style save/restore pattern (withEditionForBootTest, defined in
// boot_order_test.go) rather than mutating config.Edition permanently.
func TestHandleStateGET_IdentityModeByEdition(t *testing.T) {
	cases := []struct {
		edition      string
		wantMode     string
		wantEditionF string
	}{
		{config.EditionCore, "local", config.EditionCore},
		{config.EditionHosted, "platform", config.EditionHosted},
		{config.EditionDesktop, "platform", config.EditionDesktop},
	}
	for _, tc := range cases {
		t.Run(tc.edition, func(t *testing.T) {
			withEditionForBootTest(t, tc.edition)
			api := newTestRestAPIWithHome(t)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
			api.HandleState(w, r)

			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			identity, ok := resp["identity"].(map[string]any)
			require.True(t, ok, "response must contain an 'identity' object")
			assert.Equal(t, tc.wantMode, identity["mode"], "edition %q must map to mode %q", tc.edition, tc.wantMode)
			assert.Equal(t, tc.wantEditionF, identity["edition"], "identity.edition must mirror config.Edition")
		})
	}
}

// TestHandleStateGET_IdentitySignedInVsNot pins the second half of §2.2:
// `identity.signed_in` and `identity.account`/`identity.blocked_reason`
// reflect THIS request's own context, not a global. GET /api/v1/state is
// registered withOptionalAuth, so a non-nil *config.UserConfig under
// UserContextKey is exactly what "signed in" means here.
func TestHandleStateGET_IdentitySignedInVsNot(t *testing.T) {
	withEditionForBootTest(t, config.EditionCore)

	t.Run("signed out", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
		api.HandleState(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		identity := resp["identity"].(map[string]any)
		assert.Equal(t, false, identity["signed_in"])
		assert.Equal(t, "signed_out", identity["blocked_reason"])
		_, hasAccount := identity["account"]
		assert.False(t, hasAccount, "identity.account must be absent when signed_in is false")
	})

	t.Run("signed in", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
		user := &config.UserConfig{Username: "daniel@elicify.ai"}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, user))
		api.HandleState(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		identity := resp["identity"].(map[string]any)
		assert.Equal(t, true, identity["signed_in"])
		_, hasBlockedReason := identity["blocked_reason"]
		assert.False(t, hasBlockedReason, "identity.blocked_reason must be absent when signed_in is true")
		account, ok := identity["account"].(map[string]any)
		require.True(t, ok, "identity.account must be present when signed_in is true")
		assert.Equal(t, "daniel@elicify.ai", account["label"])
		assert.Equal(t, "d•••@elicify.ai", account["email_masked"])
		assert.Nil(t, account["org"])
	})
}

// TestHandleStatePATCH verifies PATCH /api/v1/state returns 200.
// Traces to: wave5b-system-agent-spec.md — E4: state update
func TestHandleStatePATCH(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/state", strings.NewReader(`{"onboarding_complete":true}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleState(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["onboarding_complete"], "PATCH must return onboarding_complete=true")
}

// TestHandleStatusGET verifies GET /api/v1/status returns 200 with expected fields.
// BDD: Given the gateway is running,
// When GET /api/v1/status is called,
// Then 200 with online=true, agent_count (int), version (string).
// Traces to: wave5b-system-agent-spec.md — E4: status endpoint
func TestHandleStatusGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	api.HandleStatus(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp gen.GatewayStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Online, "online must be true")
	assert.GreaterOrEqual(t, resp.AgentCount, 1, "agent_count must be ≥1 (system agent)")
	// version may be "dev" in tests — just check it's not empty when Version var is set
}

// TestHandleStorageStatsGET verifies GET /api/v1/storage/stats returns 200 with
// workspace_size_bytes field.
// Traces to: wave5b-system-agent-spec.md — E4: storage stats endpoint
func TestHandleStorageStatsGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/storage/stats", nil)
	api.HandleStorageStats(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, hasField := resp["workspace_size_bytes"]
	assert.True(t, hasField, "response must contain 'workspace_size_bytes' field")
}

// --- E6: Add TestHandleDoctorPOST ---

// TestHandleDoctorPOST verifies POST /api/v1/doctor returns 200 with
// score (int), issues (array), and checked_at (string).
// BDD: Given the gateway is running,
// When POST /api/v1/doctor is called,
// Then 200 with a diagnostic result containing score, issues, and checked_at.
// Traces to: wave5b-system-agent-spec.md — E6: doctor POST endpoint
func TestHandleDoctorPOST(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/doctor", nil)
	api.HandleDoctor(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// score must be a number.
	score, hasScore := resp["score"]
	assert.True(t, hasScore, "POST /doctor must return 'score'")
	assert.IsType(t, float64(0), score, "score must be a number")

	// issues must be an array (may be empty).
	issues, hasIssues := resp["issues"]
	assert.True(t, hasIssues, "POST /doctor must return 'issues'")
	assert.IsType(t, []any{}, issues, "issues must be an array")

	// checked_at must be a non-empty string.
	checkedAt, hasCheckedAt := resp["checked_at"]
	assert.True(t, hasCheckedAt, "POST /doctor must return 'checked_at'")
	assert.IsType(t, "", checkedAt, "checked_at must be a string")
	assert.NotEmpty(t, checkedAt, "checked_at must not be empty")
}

// --- HandleDoctor tests ---

// TestHandleDoctorReturnsOK verifies GET /api/v1/doctor returns 200 with status "ok".
// BDD: Given the gateway is running,
// When GET /api/v1/doctor is called,
// Then the response has status 200 and top-level status "ok".
// Traces to: wave5a-wire-ui-spec.md — Scenario: Doctor endpoint returns health status (US-16 AC1)
func TestHandleDoctorReturnsOK(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/doctor", nil)
	api.HandleDoctor(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Status string         `json:"status"`
		Checks map[string]any `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp.Status)
	assert.Contains(t, resp.Checks, "gateway")
	assert.Contains(t, resp.Checks, "agent_loop")
	assert.Contains(t, resp.Checks, "session_store")
	assert.Contains(t, resp.Checks, "go_runtime")
}

// TestHandleDoctorMethodNotAllowed verifies that methods other than GET and POST return 405.
// POST is allowed (returns diagnostic result without checks). GET returns full detail.
// Traces to: wave5a-wire-ui-spec.md — Dataset: Doctor endpoint — method validation
func TestHandleDoctorMethodNotAllowed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/doctor", nil)
	api.HandleDoctor(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleVersion_ResponseShape verifies that GET /version returns a body that
// unmarshal-cleanly maps to gen.VersionResponse.
//
// BDD:
//
//	Given a running gateway,
//	When GET /api/v1/version is called,
//	Then the response is 200 and the body has "version" and "build_sha" fields.
//
// Traces to: rest.go HandleVersion handler — wire shape must match VersionResponse.
func TestHandleVersion_ResponseShape(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)

	api.HandleVersion(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	// The response body must unmarshal into gen.VersionResponse.
	var resp gen.VersionResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response must unmarshal into gen.VersionResponse — wire shape must match contract")

	// version field must be non-empty (either "dev" or a semver string).
	assert.NotEmpty(t, resp.Version, "VersionResponse.Version must be non-empty")
	// build_sha field must be non-empty (either "dev" or a git SHA).
	assert.NotEmpty(t, resp.BuildSha, "VersionResponse.BuildSha must be non-empty")
}

// TestGetUserContext_HappyPath verifies that GET /user-context returns
// gen.UserContextResponse with empty content when USER.md does not exist.
//
// BDD:
//
//	Given a workspace with no USER.md file,
//	When GET /api/v1/user-context is called,
//	Then 200 with {"content": ""}.
//
// Traces to: rest.go getUserContext — returns empty string when USER.md is absent.
func TestGetUserContext_HappyPath(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/user-context", nil)
	r = withAdminRole(r)

	api.HandleUserContext(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.UserContextResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response must unmarshal into gen.UserContextResponse")
	assert.Equal(t, "", resp.Content,
		"content must be empty string when USER.md does not exist")
}

// TestPutUserContext_HappyPath verifies that PUT /user-context writes content
// and GET /user-context returns the same content.
//
// BDD:
//
//	Given a workspace with no USER.md,
//	When PUT /api/v1/user-context with {"content": "Hello, world!"},
//	Then 200 with {"content": "Hello, world!"}.
//	And GET /api/v1/user-context returns {"content": "Hello, world!"}.
//
// Traces to: rest.go putUserContext — persists content to USER.md.
func TestPutUserContext_HappyPath(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	const testContent = "Hello, world! — user context test."

	// PUT with content.
	putBody := `{"content":"` + testContent + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/user-context", strings.NewReader(putBody))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.HandleUserContext(w, r)

	require.Equal(t, http.StatusOK, w.Code, "PUT must return 200: %s", w.Body.String())
	var putResp gen.UserContextResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &putResp))
	assert.Equal(t, testContent, putResp.Content,
		"PUT response content must echo the written content")

	// GET must return the same content (persistence test).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/user-context", nil)
	r2 = withAdminRole(r2)
	api.HandleUserContext(w2, r2)

	require.Equal(t, http.StatusOK, w2.Code)
	var getResp gen.UserContextResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &getResp))
	assert.Equal(t, testContent, getResp.Content,
		"GET must return the content written by PUT (persistence test)")
}

// TestPutUserContext_ValidateInbound_400OnMissingContent verifies that with
// validate_inbound=true, PUT /user-context without the "content" field returns 400.
//
// BDD:
//
//	Given validate_inbound=true,
//	When PUT /api/v1/user-context with body {},
//	Then 400 with schema error referencing UserContextRequest.
//
// Traces to: rest.go putUserContext — decodeAndValidate with "UserContextRequest" schema.
func TestPutUserContext_ValidateInbound_400OnMissingContent(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	// {} is missing the required "content" field.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/user-context", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.HandleUserContext(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"missing required 'content' field must return 400 when validate_inbound=true")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "UserContextRequest",
		"error message must reference the schema name")
}
