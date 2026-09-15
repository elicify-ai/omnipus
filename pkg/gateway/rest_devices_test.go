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
