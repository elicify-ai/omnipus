//go:build !cgo && nogodmode

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests run ONLY under the hardened `nogodmode` build, where
// sandbox.GodModeAvailable is compiled to false. They prove the runtime god-mode
// switch is inert and the toggle endpoint refuses to enable god mode regardless
// of the --allow-god-mode boot flag.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGodMode_NoGodModeBuild_RejectsEnable proves that on a nogodmode build,
// POST enabled=true is rejected 403 even with allowGodMode=true and a valid
// step-up token — the build does not support god mode.
func TestGodMode_NoGodModeBuild_RejectsEnable(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.allowGodMode = true // boot flag set, but build compiled out god mode

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = withReAuthAdmin(t, api, req)
	w := httptest.NewRecorder()
	api.HandleGodMode(w, req)

	require.Equal(t, http.StatusForbidden, w.Code,
		"nogodmode build must reject enabling god mode; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "nogodmode",
		"error must mention the build was compiled with nogodmode")
	assert.False(t, api.agentLoop.GetConfig().Sandbox.GodMode,
		"god mode must never be persisted on a nogodmode build")
}

// TestGodMode_NoGodModeBuild_GETReportsUnavailable proves GET reports
// available=false on a nogodmode build.
func TestGodMode_NoGodModeBuild_GETReportsUnavailable(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.allowGodMode = true

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/god-mode", nil)
	getW := httptest.NewRecorder()
	api.HandleGodMode(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)
	assert.Contains(t, getW.Body.String(), `"available":false`,
		"GET must report available=false on a nogodmode build")
}
