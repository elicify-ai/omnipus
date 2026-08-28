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

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Wave 4 REST endpoint tests — SEC-26 rate limits status.
//
// These tests exercise:
//   - GET /api/v1/security/rate-limits — disabled state (no config)
//   - GET /api/v1/security/rate-limits — enabled state (config set)
//   - PUT /api/v1/security/rate-limits — rejects retired daily_cost_cap_usd
//     field with HTTP 400 (ADR-053 D12 — token budget is the sole brake).
//   - Non-GET methods return 405

// TestHandleRateLimits_Disabled returns enabled=false when no rate-limit
// config is set (the default state).
func TestHandleRateLimits_Disabled(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/rate-limits", nil)
	api.HandleRateLimits(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	assert.Equal(t, false, body["enabled"], "enabled must be false when no limits configured")
}

// TestHandleRateLimits_Enabled returns enabled=true and the configured values
// when rate_limits are set in config.
func TestHandleRateLimits_Enabled(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			RateLimits: config.OmnipusRateLimitsConfig{
				MaxAgentLLMCallsPerHour:    50,
				MaxAgentToolCallsPerMinute: 15,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/rate-limits", nil)
	api.HandleRateLimits(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	assert.Equal(t, true, body["enabled"], "enabled must be true when any limit is configured")
	assert.Equal(t, float64(50), body["max_agent_llm_calls_per_hour"], "llm calls per hour must match config")
	assert.Equal(t, float64(15), body["max_agent_tool_calls_per_minute"], "tool calls per minute must match config")
}

// TestHandleRateLimits_RejectsRetiredUSDField pins the wire-shape invariant
// for issue #540: PUT /api/v1/security/rate-limits must reject the retired
// daily_cost_cap_usd field with HTTP 400 (token budget is the sole brake
// per ADR-053 D12 — operators get a clear error, not a silent no-op).
func TestHandleRateLimits_RejectsRetiredUSDField(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := putRateLimits(t, api, `{"daily_cost_cap_usd": 5.0}`)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"daily_cost_cap_usd is retired per ADR-053 D12 — must 400, never silent no-op")
	assert.Contains(t, w.Body.String(), "daily_cost_cap_usd",
		"error body must name the retired field so operators find the migration path")
	assert.Contains(t, w.Body.String(), "D12",
		"error body must cite the ADR that retired the field")
	// SEC: raw field value must NOT be echoed into the 400 body — see
	// rest_rate_limits.go's daily_cost_cap_usd rejection comment.
	assert.NotContains(t, w.Body.String(), "5.0",
		"rejection must not echo raw field value (MaxBytesReader/1MB body)")
}

// TestHandleRateLimitsWave4_MethodNotAllowed returns 405 for unsupported methods.
// PUT and GET are handled by HandleRateLimits in rest_rate_limits.go; POST and
// DELETE remain unsupported.
func TestHandleRateLimitsWave4_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, "/api/v1/security/rate-limits", nil)
			api.HandleRateLimits(w, r)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}
