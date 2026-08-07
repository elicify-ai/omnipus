// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wave 3 REST endpoint tests — SEC-28.
//
// These tests exercise the operator-facing surface added in Wave 3:
//   - GET /api/v1/security/exec-proxy-status
//
// HandlePromptGuard tests are in rest_prompt_guard_test.go.

// TestHandleExecProxyStatus_Disabled returns enabled=false, running=false
// when cfg.Tools.Exec.EnableProxy is not set. This is the default state
// operators start in.
func TestHandleExecProxyStatus_Disabled(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/exec-proxy-status", nil)
	api.HandleExecProxyStatus(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	assert.Equal(t, false, body["enabled"], "enabled must be false by default")
	assert.Equal(t, false, body["running"], "running must be false when no proxy")
	_, hasAddr := body["address"]
	assert.False(t, hasAddr, "address must be omitted when proxy is not running")
}

// TestHandleExecProxyStatus_MethodNotAllowed returns 405 for POST/PUT/DELETE.
// The status endpoint is read-only.
func TestHandleExecProxyStatus_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, "/api/v1/security/exec-proxy-status", nil)
			api.HandleExecProxyStatus(w, r)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}
