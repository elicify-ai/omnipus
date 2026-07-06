//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — tests for the dark-launched device-pairing feature flag
// (Sandbox.Experimental.DevicePairingEnabled).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestHandleAbout_DevicePairingEnabledField verifies that GET /api/v1/about
// reflects the dark-launched flag: false by default, true when the operator
// enables it.
func TestHandleAbout_DevicePairingEnabledField(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/about", nil)
	api.HandleAbout(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["device_pairing_enabled"],
		"device_pairing_enabled must be false by default")

	api.agentLoop.GetConfig().Sandbox.Experimental.DevicePairingEnabled = true
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/about", nil)
	api.HandleAbout(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.Equal(t, true, resp2["device_pairing_enabled"],
		"device_pairing_enabled must reflect the enabled config")
}

// TestHandleDevicePairingResponse_DisabledByDefault_NoPanic verifies that the
// WS device_pairing_response handler is a safe no-op when the feature flag is
// disabled (default) — it must not panic even with a nil pairingStore.
func TestHandleDevicePairingResponse_DisabledByDefault_NoPanic(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	h := &WSHandler{agentLoop: api.agentLoop}

	require.NotPanics(t, func() {
		h.handleDevicePairingResponse("some-device", "approve")
	})
}
