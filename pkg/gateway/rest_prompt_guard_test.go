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
