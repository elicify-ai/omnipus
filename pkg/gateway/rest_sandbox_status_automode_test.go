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

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// ADR-092 review finding A: GET /api/v1/security/sandbox-status must carry
// the three inputs the chat badge and composer switch read —
// kernel_sandbox_active, auto_approve_effective, god_mode_active — or the SPA
// shows "Ask" even where Auto is on and a kernel sandbox is enforcing.

func getSandboxStatusBody(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	api.HandleSandboxStatus(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

// withTurnPolicyBase registers (or clears) the process-wide per-turn policy
// base for the duration of the test — the value the agent loop reads to
// decide Auto vs Ask — and restores "none" afterwards.
func withTurnPolicyBase(t *testing.T, installed bool) {
	t.Helper()
	if installed {
		sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: t.TempDir()})
	} else {
		sandbox.RegisterTurnPolicyBase(nil)
	}
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
}

func TestHandleSandboxStatus_ReportsAutoApproveInputs(t *testing.T) {
	t.Run("Auto on, kernel sandbox enforcing", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.agentLoop.GetConfig().Sandbox.AutoApprove = true
		withTurnPolicyBase(t, true)
		require.True(t, sandbox.TurnPolicyBaseInstalled())

		body := getSandboxStatusBody(t, api)
		assert.Equal(t, true, body["kernel_sandbox_active"])
		assert.Equal(t, true, body["auto_approve_effective"])
		assert.Equal(t, false, body["god_mode_active"])
	})

	t.Run("Auto on, no kernel sandbox", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.agentLoop.GetConfig().Sandbox.AutoApprove = true
		withTurnPolicyBase(t, false)

		body := getSandboxStatusBody(t, api)
		assert.Equal(t, false, body["kernel_sandbox_active"], "must be present and false, not absent")
		assert.Equal(t, true, body["auto_approve_effective"],
			"the global default is reported as configured, not ANDed with the kernel predicate")
	})

	t.Run("Auto off", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.agentLoop.GetConfig().Sandbox.AutoApprove = false
		withTurnPolicyBase(t, true)

		body := getSandboxStatusBody(t, api)
		assert.Equal(t, false, body["auto_approve_effective"], "must be present and false, not absent")
		assert.Equal(t, true, body["kernel_sandbox_active"])
	})

	t.Run("God Mode active", func(t *testing.T) {
		if !sandbox.GodModeAvailable {
			t.Skip("requires GodModeAvailable=true (default build)")
		}
		api := newTestRestAPIWithHome(t)
		api.allowGodMode = true
		api.agentLoop.GetConfig().Sandbox.GodMode = true
		withTurnPolicyBase(t, true)

		body := getSandboxStatusBody(t, api)
		assert.Equal(t, true, body["god_mode_active"])
	})

	t.Run("God Mode persisted but not available this boot", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		api.allowGodMode = false
		api.agentLoop.GetConfig().Sandbox.GodMode = true

		body := getSandboxStatusBody(t, api)
		assert.Equal(t, false, body["god_mode_active"], "an armed-but-inert switch is not active")
	})
}
