//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

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

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
)

// promptGuardPUT is a test helper that issues an authenticated admin PUT to
// HandlePromptGuard with the given level value.
func promptGuardPUT(t *testing.T, api *restAPI, level string) *httptest.ResponseRecorder {
	t.Helper()
	payload := `{"level":"` + level + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/prompt-guard", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.HandlePromptGuard(w, r)
	return w
}

// TestHandlePromptGuard_AllThreeLevelsAccepted verifies that "low", "medium",
// and "high" each return 200 with saved:true and requires_restart:false.
func TestHandlePromptGuard_AllThreeLevelsAccepted(t *testing.T) {
	for _, level := range []string{"low", "medium", "high"} {
		t.Run(level, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			w := promptGuardPUT(t, api, level)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, true, resp["saved"])
			assert.Equal(t, false, resp["requires_restart"])
			assert.Equal(t, level, resp["applied_level"])
		})
	}
}

// TestHandlePromptGuard_InvalidLevelRejected verifies that case-variant and
// unknown values are rejected with 400.
func TestHandlePromptGuard_InvalidLevelRejected(t *testing.T) {
	cases := []struct {
		name  string
		level string
	}{
		{"unknown", "extreme"},
		{"case-variant uppercase", "HIGH"},
		{"empty string", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			payload := `{"level":"` + tc.level + `"}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/security/prompt-guard", strings.NewReader(payload))
			r.Header.Set("Content-Type", "application/json")
			r = withAdminRole(r)
			api.HandlePromptGuard(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		})
	}
}

// TestHandlePromptGuard_HotReload verifies that the PUT response carries
// requires_restart:false on successful hot-reload (no restart needed).
func TestHandlePromptGuard_HotReload(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := promptGuardPUT(t, api, "high")

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["requires_restart"], "requires_restart must be false for hot-reload endpoint")
}

// TestHandlePromptGuard_AuthenticatedCallerSucceeds verifies that an
// authenticated caller can complete PUT /api/v1/security/prompt-guard
// end-to-end: the response reflects the requested change (not a hardcoded
// value) and the change is durably persisted (not a no-op). This endpoint's
// sibling files (rest_rate_limits_test.go, rest_retention_test.go,
// rest_skill_trust_test.go) each carried an equivalent "authenticated caller
// succeeds" proof after their RBAC-era *_NonAdmin403 tests were reconciled
// for the single-user model; this file's counterpart was dropped outright
// with no replacement. This test closes that gap: two different levels are
// PUT in sequence on the same api instance, and each is confirmed via GET,
// so a stub/hardcoded handler (always returns the same applied_level) or a
// no-op handler (never persists) would fail here.
func TestHandlePromptGuard_AuthenticatedCallerSucceeds(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// First change: low.
	w1 := promptGuardPUT(t, api, "low")
	require.Equal(t, http.StatusOK, w1.Code, "PUT must succeed: %s", w1.Body.String())

	var resp1 map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	assert.Equal(t, true, resp1["saved"])
	assert.Equal(t, "low", resp1["applied_level"])

	getReq1 := httptest.NewRequest(http.MethodGet, "/api/v1/security/prompt-guard", nil)
	getW1 := httptest.NewRecorder()
	api.HandlePromptGuard(getW1, getReq1)
	require.Equal(t, http.StatusOK, getW1.Code)

	var getResp1 map[string]any
	require.NoError(t, json.Unmarshal(getW1.Body.Bytes(), &getResp1))
	assert.Equal(t, "low", getResp1["level"], "GET must reflect the persisted low level")

	// Second change: high — a DIFFERENT value from the first PUT. If the
	// handler were hardcoded or a no-op, this response/GET would still read
	// "low".
	w2 := promptGuardPUT(t, api, "high")
	require.Equal(t, http.StatusOK, w2.Code, "PUT must succeed: %s", w2.Body.String())

	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.Equal(t, true, resp2["saved"])
	assert.Equal(t, "high", resp2["applied_level"])
	assert.NotEqual(t, resp1["applied_level"], resp2["applied_level"],
		"applied_level must differ between two different PUT bodies — proves the response isn't hardcoded")

	getReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/security/prompt-guard", nil)
	getW2 := httptest.NewRecorder()
	api.HandlePromptGuard(getW2, getReq2)
	require.Equal(t, http.StatusOK, getW2.Code)

	var getResp2 map[string]any
	require.NoError(t, json.Unmarshal(getW2.Body.Bytes(), &getResp2))
	assert.Equal(t, "high", getResp2["level"],
		"GET must reflect the updated persisted level, proving the second PUT wasn't a no-op")
}

// TestHandlePromptGuard_MethodNotAllowed verifies that POST and DELETE return 405.
func TestHandlePromptGuard_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, "/api/v1/security/prompt-guard", nil)
			api.HandlePromptGuard(w, r)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

// TestHandlePromptGuard_EmitsAuditEntry verifies that a successful PUT emits a
// security_setting_change audit record with resource="sandbox.prompt_injection_level".
func TestHandlePromptGuard_EmitsAuditEntry(t *testing.T) {
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := context.WithValue(context.Background(), ctxkey.UserContextKey{},
		&config.UserConfig{Username: "admin"})

	err = audit.EmitSecuritySettingChange(
		ctx,
		logger,
		"sandbox.prompt_injection_level",
		"medium",
		"high",
	)
	require.NoError(t, err)
	require.NoError(t, logger.Close())

	data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "sandbox.prompt_injection_level")
	assert.Contains(t, content, "security_setting_change")
	assert.Contains(t, content, "high")
}

// TestHandlePromptGuard_PersistsCorrectJSONPath verifies that after a PUT with
// level="high", config.json on disk has sandbox.prompt_injection_level=="high".
func TestHandlePromptGuard_PersistsCorrectJSONPath(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := promptGuardPUT(t, api, "high")
	require.Equal(t, http.StatusOK, w.Code, "PUT must succeed: %s", w.Body.String())

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	sandboxDisk, _ := onDisk["sandbox"].(map[string]any)
	require.NotNil(t, sandboxDisk, "sandbox section must be present in config.json")
	assert.Equal(t, "high", sandboxDisk["prompt_injection_level"],
		"sandbox.prompt_injection_level must equal the PUT value")
}
