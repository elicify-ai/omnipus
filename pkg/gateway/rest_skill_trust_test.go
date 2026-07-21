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

	"github.com/dapicom-ai/omnipus/pkg/logger"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
)

// withNonAdminRole injects config.UserRoleUser into the request context,
// simulating an authenticated non-admin user.
func withNonAdminRole(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), RoleContextKey{}, config.UserRoleUser)
	return r.WithContext(ctx)
}

// skillTrustPUT is a helper that issues a PUT /api/v1/security/skill-trust
// with the given level as admin and returns the response recorder.
func skillTrustPUT(t *testing.T, api *restAPI, level string) *httptest.ResponseRecorder {
	t.Helper()
	payload := `{"level":"` + level + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/skill-trust", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.HandleSkillTrust(w, r)
	return w
}

// TestHandleSkillTrust_AllThreeValuesAccepted verifies each canonical value
// returns 200 with saved=true and requires_restart=false.
func TestHandleSkillTrust_AllThreeValuesAccepted(t *testing.T) {
	for _, level := range []string{"block_unverified", "warn_unverified", "allow_all"} {
		t.Run(level, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			w := skillTrustPUT(t, api, level)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, true, resp["saved"])
			assert.Equal(t, false, resp["requires_restart"])
			assert.Equal(t, level, resp["applied_level"])
		})
	}
}

// TestHandleSkillTrust_UppercaseRejected verifies that BLOCK_UNVERIFIED (all-caps)
// returns 400 and the error message lists all three canonical values.
func TestHandleSkillTrust_UppercaseRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := skillTrustPUT(t, api, "BLOCK_UNVERIFIED")

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, "block_unverified", "error must list canonical value block_unverified")
	assert.Contains(t, body, "warn_unverified", "error must list canonical value warn_unverified")
	assert.Contains(t, body, "allow_all", "error must list canonical value allow_all")
}

// TestHandleSkillTrust_MixedCaseRejected verifies that Block_Unverified returns 400.
func TestHandleSkillTrust_MixedCaseRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := skillTrustPUT(t, api, "Block_Unverified")

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestHandleSkillTrust_InvalidValueRejected verifies that an arbitrary unknown
// string returns 400.
func TestHandleSkillTrust_InvalidValueRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := skillTrustPUT(t, api, "nope")

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestHandleSkillTrust_EmptyValueRejected verifies that an empty string returns 400.
func TestHandleSkillTrust_EmptyValueRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	payload := `{"level":""}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/skill-trust", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.HandleSkillTrust(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestHandleSkillTrust_HotReload verifies that the response contains requires_restart=false,
// confirming skill_trust is a hot-reload setting.
func TestHandleSkillTrust_HotReload(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := skillTrustPUT(t, api, "warn_unverified")

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["requires_restart"], "skill_trust is a hot-reload setting")
}

// TestHandleSkillTrust_NonAdmin403 verifies that a non-admin authenticated user
// receives 403 when attempting PUT.
func TestHandleSkillTrust_NonAdmin403(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	payload := `{"level":"warn_unverified"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/skill-trust", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r = withNonAdminRole(r)
	// Route through RequireAdmin as adminWrap does at registration time — the
	// inner handler no longer re-wraps it.
	middleware.RequireAdmin(http.HandlerFunc(api.HandleSkillTrust)).ServeHTTP(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code, "non-admin must receive 403")
}

// TestHandleSkillTrust_MethodNotAllowed verifies that POST and DELETE return 405.
func TestHandleSkillTrust_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, "/api/v1/security/skill-trust", nil)
			api.HandleSkillTrust(w, r)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

// TestHandleSkillTrust_EmitsAuditEntry verifies that a successful PUT emits an
// audit record via EmitSecuritySettingChange with resource="sandbox.skill_trust".
func TestHandleSkillTrust_EmitsAuditEntry(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	auditDir := filepath.Join(api.homePath, "system")
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := context.WithValue(context.Background(), ctxkey.UserContextKey{},
		&config.UserConfig{Username: "admin"})
	ctx = context.WithValue(ctx, RoleContextKey{}, config.UserRoleAdmin)

	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/skill-trust",
		strings.NewReader(`{"level":"block_unverified"}`))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(ctx)

	err = audit.EmitSecuritySettingChange(
		r.Context(),
		logger,
		"sandbox.skill_trust",
		"warn_unverified",
		"block_unverified",
	)
	require.NoError(t, err, "EmitSecuritySettingChange must not return an error")

	_ = logger.Close()

	data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "sandbox.skill_trust", "audit entry must include resource=sandbox.skill_trust")
	assert.Contains(t, content, "security_setting_change", "audit entry must use EventSecuritySettingChange")
	assert.Contains(t, content, "block_unverified", "audit entry must include the new value")
}

// TestHandleSkillTrust_PersistsToDisk verifies that a successful PUT actually
// writes sandbox.skill_trust to config.json on disk (write+read round-trip).
func TestHandleSkillTrust_PersistsToDisk(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := skillTrustPUT(t, api, "block_unverified")
	require.Equal(t, http.StatusOK, w.Code, "PUT must succeed: %s", w.Body)

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	sandboxDisk, _ := onDisk["sandbox"].(map[string]any)
	require.NotNil(t, sandboxDisk, "sandbox section must be written to disk")
	assert.Equal(t, "block_unverified", sandboxDisk["skill_trust"])
}

// TestHandleSkillTrust_AllowAllWarning verifies that level=="allow_all" includes
// the advisory warning in the response body.
func TestHandleSkillTrust_AllowAllWarning(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := skillTrustPUT(t, api, "allow_all")

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	warning, hasWarning := resp["warning"]
	assert.True(t, hasWarning, "allow_all response must include a warning field")
	warnStr, ok := warning.(string)
	assert.True(t, ok, "warning must be a string")
	assert.Contains(t, warnStr, "allow_all", "warning must reference the level")
}

// TestHandleSkillTrust_GET_DefaultLevel verifies GET returns the configured level.
func TestHandleSkillTrust_GET_DefaultLevel(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/skill-trust", nil)
	api.HandleSkillTrust(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	level, ok := resp["level"]
	assert.True(t, ok, "GET response must include level field")
	assert.NotEmpty(t, level, "level must not be empty")
}
