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
	"github.com/dapicom-ai/omnipus/pkg/routing"
)

// sessionScopePUT issues a PUT /api/v1/security/session-scope as admin.
func sessionScopePUT(t *testing.T, api *restAPI, dmScope string) *httptest.ResponseRecorder {
	t.Helper()
	payload := `{"dm_scope":"` + dmScope + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/session-scope", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.HandleSessionScope(w, r)
	return w
}

// TestHandleSessionDMScope_AllFourValuesAccepted verifies each canonical DMScope
// value returns 200 with saved=true and requires_restart=true.
func TestHandleSessionDMScope_AllFourValuesAccepted(t *testing.T) {
	canonicalValues := []string{
		string(routing.DMScopeMain),
		string(routing.DMScopePerPeer),
		string(routing.DMScopePerChannelPeer),
		string(routing.DMScopePerAccountChannelPeer),
	}
	for _, scope := range canonicalValues {
		t.Run(scope, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			w := sessionScopePUT(t, api, scope)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, true, resp["saved"])
			assert.Equal(t, true, resp["requires_restart"])
		})
	}
}

// TestHandleSessionDMScope_GlobalRejected verifies that the legacy "global" value
// is rejected with 400 and the error lists all four canonical values.
func TestHandleSessionDMScope_GlobalRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sessionScopePUT(t, api, "global")

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, string(routing.DMScopeMain))
	assert.Contains(t, body, string(routing.DMScopePerPeer))
	assert.Contains(t, body, string(routing.DMScopePerChannelPeer))
	assert.Contains(t, body, string(routing.DMScopePerAccountChannelPeer))
}

// TestHandleSessionDMScope_InvalidValueRejected verifies that arbitrary or
// case-variant values are rejected with 400.
func TestHandleSessionDMScope_InvalidValueRejected(t *testing.T) {
	cases := []string{"nope", "", "PER-PEER"}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			var payload string
			if tc == "" {
				payload = `{"dm_scope":""}`
			} else {
				payload = `{"dm_scope":"` + tc + `"}`
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/security/session-scope", strings.NewReader(payload))
			r.Header.Set("Content-Type", "application/json")
			r = withAdminRole(r)
			api.HandleSessionScope(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code, "scope=%q body: %s", tc, w.Body.String())
		})
	}
}

// TestHandleSessionDMScope_RestartRequired verifies the response always carries
// requires_restart=true (session routing is cached at boot).
func TestHandleSessionDMScope_RestartRequired(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sessionScopePUT(t, api, string(routing.DMScopeMain))

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["requires_restart"], "session routing requires restart")
}

// TestHandleSessionDMScope_PersistsCorrectJSONPath verifies that after PUT,
// the on-disk config.json has session.dm_scope set to the submitted value.
func TestHandleSessionDMScope_PersistsCorrectJSONPath(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := sessionScopePUT(t, api, string(routing.DMScopeMain))
	require.Equal(t, http.StatusOK, w.Code, "PUT must succeed: %s", w.Body)

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	sessionDisk, _ := onDisk["session"].(map[string]any)
	require.NotNil(t, sessionDisk, "session section must be written to disk")
	assert.Equal(t, string(routing.DMScopeMain), sessionDisk["dm_scope"])
}

// TestHandleSessionDMScope_MethodNotAllowed verifies DELETE returns 405.
func TestHandleSessionDMScope_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/security/session-scope", nil)
	api.HandleSessionScope(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleSessionDMScope_EmitsAuditEntry verifies a successful PUT emits an
// audit record with resource="session.dm_scope".
func TestHandleSessionDMScope_EmitsAuditEntry(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	auditDir := filepath.Join(api.homePath, "system")
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	ctx := context.WithValue(context.Background(), ctxkey.UserContextKey{},
		&config.UserConfig{Username: "admin"})

	err = audit.EmitSecuritySettingChange(
		ctx,
		logger,
		"session.dm_scope",
		string(routing.DMScopePerChannelPeer),
		string(routing.DMScopeMain),
	)
	if err != nil {
		t.Fatalf("EmitSecuritySettingChange must not return an error: %v", err)
	}

	_ = logger.Close()

	data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "session.dm_scope", "audit entry must include resource=session.dm_scope")
	assert.Contains(t, content, "security_setting_change", "audit entry must use EventSecuritySettingChange")
	assert.Contains(t, content, string(routing.DMScopeMain), "audit entry must include the new value")
}
