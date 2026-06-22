//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// TestGodMode_POST_RequiresStepUp proves the toggle CANNOT be flipped without a
// password re-auth consent token: an admin request that lacks the X-Reauth-Token
// header is rejected 403 and nothing is persisted.
func TestGodMode_POST_RequiresStepUp(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("requires GodModeAvailable=true (default build)")
	}
	api := newTestRestAPIWithHome(t)
	api.allowGodMode = true

	body := `{"enabled":true}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// Admin user + role, but NO re-auth token.
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleGodMode(w, r)

	require.Equal(t, http.StatusForbidden, w.Code,
		"toggling god mode without step-up must be 403; body: %s", w.Body.String())

	// Confirm god mode did not turn on.
	assert.False(t, api.agentLoop.GetConfig().Sandbox.GodMode,
		"god mode must remain off when step-up is missing")
}

// TestGodMode_POST_TogglesOnAndOff_WithStepUp proves the override applies and
// fully reverts: enabling persists sandbox.god_mode=true and a subsequent GET
// reports enabled=true; disabling clears it and GET reports enabled=false.
func TestGodMode_POST_TogglesOnAndOff_WithStepUp(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("requires GodModeAvailable=true (default build)")
	}
	api := newTestRestAPIWithHome(t)
	api.allowGodMode = true

	// Enable.
	onReq := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(`{"enabled":true}`))
	onReq.Header.Set("Content-Type", "application/json")
	onReq = withReAuthAdmin(t, api, onReq)
	onW := httptest.NewRecorder()
	api.HandleGodMode(onW, onReq)
	require.Equal(t, http.StatusOK, onW.Code, "enable must succeed; body: %s", onW.Body.String())

	var onResp struct {
		Enabled   bool `json:"enabled"`
		Available bool `json:"available"`
	}
	require.NoError(t, json.Unmarshal(onW.Body.Bytes(), &onResp))
	assert.True(t, onResp.Enabled, "response must report enabled=true after enable")
	assert.True(t, onResp.Available, "response must report available=true")
	assert.True(t, api.agentLoop.GetConfig().Sandbox.GodMode,
		"sandbox.god_mode must be persisted true after enable")

	// GET reflects the new state.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/god-mode", nil)
	getW := httptest.NewRecorder()
	api.HandleGodMode(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)
	var getResp struct {
		Enabled bool `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &getResp))
	assert.True(t, getResp.Enabled, "GET must report enabled=true")

	// Disable — the override must fully revert.
	offReq := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(`{"enabled":false}`))
	offReq.Header.Set("Content-Type", "application/json")
	offReq = withReAuthAdmin(t, api, offReq)
	offW := httptest.NewRecorder()
	api.HandleGodMode(offW, offReq)
	require.Equal(t, http.StatusOK, offW.Code, "disable must succeed; body: %s", offW.Body.String())
	assert.False(t, api.agentLoop.GetConfig().Sandbox.GodMode,
		"sandbox.god_mode must be cleared after disable")
}

// TestGodMode_POST_Unavailable_WhenAllowFlagOff proves that enabling god mode is
// rejected 403 when --allow-god-mode was NOT passed at boot (allowGodMode=false),
// even with a valid step-up token. Turning it ON requires availability.
func TestGodMode_POST_Unavailable_WhenAllowFlagOff(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.allowGodMode = false // boot flag not passed

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = withReAuthAdmin(t, api, req)
	w := httptest.NewRecorder()
	api.HandleGodMode(w, req)

	require.Equal(t, http.StatusForbidden, w.Code,
		"enabling god mode without availability must be 403; body: %s", w.Body.String())
	assert.False(t, api.agentLoop.GetConfig().Sandbox.GodMode,
		"god mode must not be persisted when unavailable")
}

// TestGodMode_GET_ReportsUnavailable proves GET reports available=false (and
// enabled=false) when the boot flag is off — the UI must never show the switch
// as on when it is inert.
func TestGodMode_GET_ReportsUnavailable(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.allowGodMode = false

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/god-mode", nil)
	getW := httptest.NewRecorder()
	api.HandleGodMode(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	var resp struct {
		Enabled   bool `json:"enabled"`
		Available bool `json:"available"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))
	assert.False(t, resp.Available, "available must be false when boot flag is off")
	assert.False(t, resp.Enabled, "enabled must be false when unavailable")
}
