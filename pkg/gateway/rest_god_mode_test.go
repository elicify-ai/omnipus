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
// allowGodMode=true simulates a boot that was ALREADY available (legacy
// --allow-god-mode, or a prior UI-enable that has since restarted), so both
// toggles apply live and restart_required is false throughout.
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
		Enabled         bool `json:"enabled"`
		RestartRequired bool `json:"restart_required"`
	}
	require.NoError(t, json.Unmarshal(onW.Body.Bytes(), &onResp))
	assert.True(t, onResp.Enabled, "response must report enabled=true after enable")
	assert.False(t, onResp.RestartRequired, "already-available boot must apply live: restart_required must be false")
	assert.True(t, api.agentLoop.GetConfig().Sandbox.GodMode,
		"sandbox.god_mode must be persisted true after enable")
	assert.True(t, api.agentLoop.GetConfig().Sandbox.GodModeAllowed,
		"sandbox.god_mode_allowed must also be persisted true after enable")

	// GET reflects the new state.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/god-mode", nil)
	getW := httptest.NewRecorder()
	api.HandleGodMode(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)
	var getResp struct {
		Enabled   bool `json:"enabled"`
		Available bool `json:"available"`
		Supported bool `json:"supported"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &getResp))
	assert.True(t, getResp.Enabled, "GET must report enabled=true")
	assert.True(t, getResp.Supported, "GET must report supported=true on the default build")

	// Disable — the override must fully revert.
	offReq := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(`{"enabled":false}`))
	offReq.Header.Set("Content-Type", "application/json")
	offReq = withReAuthAdmin(t, api, offReq)
	offW := httptest.NewRecorder()
	api.HandleGodMode(offW, offReq)
	require.Equal(t, http.StatusOK, offW.Code, "disable must succeed; body: %s", offW.Body.String())

	var offResp struct {
		Enabled         bool `json:"enabled"`
		RestartRequired bool `json:"restart_required"`
	}
	require.NoError(t, json.Unmarshal(offW.Body.Bytes(), &offResp))
	assert.False(t, offResp.Enabled, "response must report enabled=false after disable")
	assert.False(t, offResp.RestartRequired, "disable must never require a restart")
	assert.False(t, api.agentLoop.GetConfig().Sandbox.GodMode,
		"sandbox.god_mode must be cleared after disable")
	assert.True(t, api.agentLoop.GetConfig().Sandbox.GodModeAllowed,
		"sandbox.god_mode_allowed must NOT be revoked by a disable (authorization persists)")
}

// TestGodMode_POST_EnableWhenUnavailable_PersistsAndRequiresRestart proves the
// UI-driven enablement flow (O14): enabling god mode when --allow-god-mode was
// NOT passed at boot (allowGodMode=false) is now PERMITTED — the build still
// supports it — and persists BOTH sandbox.god_mode_allowed=true and
// sandbox.god_mode=true, but returns restart_required=true because
// availability is frozen at boot and cannot take live effect in this process.
func TestGodMode_POST_EnableWhenUnavailable_PersistsAndRequiresRestart(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("requires GodModeAvailable=true (default build)")
	}
	api := newTestRestAPIWithHome(t)
	api.allowGodMode = false // boot flag not passed; not yet authorized this boot

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = withReAuthAdmin(t, api, req)
	w := httptest.NewRecorder()
	api.HandleGodMode(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"enabling is permitted whenever the build supports god mode, regardless of prior availability; body: %s", w.Body.String())

	var resp struct {
		Enabled         bool `json:"enabled"`
		RestartRequired bool `json:"restart_required"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Enabled, "response must report enabled=true (the config write succeeded)")
	assert.True(t, resp.RestartRequired, "response must report restart_required=true — this boot was not yet available")

	assert.True(t, api.agentLoop.GetConfig().Sandbox.GodMode,
		"sandbox.god_mode must be persisted true even though this boot cannot activate it live")
	assert.True(t, api.agentLoop.GetConfig().Sandbox.GodModeAllowed,
		"sandbox.god_mode_allowed must be persisted true — this is the authorization grant for the NEXT boot")

	// GET must still report the override as NOT active in this process: the
	// UI must never claim god mode is live when the boot-frozen atomic hasn't
	// caught up. This is the load-bearing guarantee behind restart_required.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/god-mode", nil)
	getW := httptest.NewRecorder()
	api.HandleGodMode(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)
	var getResp struct {
		Enabled   bool `json:"enabled"`
		Available bool `json:"available"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &getResp))
	assert.False(t, getResp.Available, "GET must still report available=false in THIS process (boot-frozen)")
	assert.False(t, getResp.Enabled, "GET must report enabled=false — the override has no live effect until restart")
}

// TestGodMode_POST_DisableAlwaysAllowed_EvenWhenUnavailable proves the
// fail-safe guarantee: disabling never requires availability and never
// requires a restart, even on a boot that was never authorized. This is what
// lets an operator always clear a stale persisted sandbox.god_mode=true.
func TestGodMode_POST_DisableAlwaysAllowed_EvenWhenUnavailable(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.allowGodMode = false

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	req = withReAuthAdmin(t, api, req)
	w := httptest.NewRecorder()
	api.HandleGodMode(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"disable must always succeed, even when unavailable; body: %s", w.Body.String())
	var resp struct {
		Enabled         bool `json:"enabled"`
		RestartRequired bool `json:"restart_required"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Enabled)
	assert.False(t, resp.RestartRequired, "disable must never require a restart")
	assert.False(t, api.agentLoop.GetConfig().Sandbox.GodMode)
}

// TestGodMode_GET_ReportsUnavailable proves GET reports available=false (and
// enabled=false) when the boot flag is off — the UI must never show the switch
// as on when it is inert. supported stays true (the default build compiles
// god mode in) — that is precisely what makes the toggle clickable.
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
		Supported bool `json:"supported"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))
	assert.False(t, resp.Available, "available must be false when boot flag is off")
	assert.False(t, resp.Enabled, "enabled must be false when unavailable")
	if sandbox.GodModeAvailable {
		assert.True(t, resp.Supported, "supported must be true on the default build even when unavailable")
	}
}
