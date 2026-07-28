// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

func requestWithBypass(bypass bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	cfg := &config.Config{}
	cfg.Gateway.DevModeBypass = bypass
	ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, cfg)
	return r.WithContext(ctx)
}

// TestRequireNotBypass_BypassOn_Returns503 verifies that when
// gateway.dev_mode_bypass=true the gate short-circuits with 503 and the
// wrapped handler is never invoked.
func TestRequireNotBypass_BypassOn_Returns503(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	middleware.RequireNotBypass(inner).ServeHTTP(w, requestWithBypass(true))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.False(t, called, "inner handler must not be called when bypass is on")

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.True(t, strings.Contains(body["error"], "dev_mode_bypass"),
		"error body must mention dev_mode_bypass, got %q", body["error"])
}

// TestRequireNotBypass_BypassOff_CallsNext verifies that when
// gateway.dev_mode_bypass=false the request is forwarded to the wrapped
// handler unchanged.
func TestRequireNotBypass_BypassOff_CallsNext(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	w := httptest.NewRecorder()
	middleware.RequireNotBypass(inner).ServeHTTP(w, requestWithBypass(false))

	assert.True(t, called, "inner handler must be called when bypass is off")
	assert.Equal(t, http.StatusTeapot, w.Code,
		"response from inner handler must pass through unchanged")
}

// TestRequireNotBypass_NilConfig_Returns503 verifies the defensive fail-closed
// behavior: if configSnapshotMiddleware did not run (no cfg in context) we
// must not assume bypass is off — we return 503.
func TestRequireNotBypass_NilConfig_Returns503(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest(http.MethodGet, "/", nil) // no config in ctx
	w := httptest.NewRecorder()
	middleware.RequireNotBypass(inner).ServeHTTP(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.False(t, called, "inner handler must not be called when config is missing")
}
