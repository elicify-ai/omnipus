// Omnipus - Ultra-lightweight personal AI gateway
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This test drives a boot-time Landlock degrade and pins that the
// authenticated GET /api/v1/security/sandbox-status endpoint reports the
// degraded FallbackBackend, not the kernel-capable backend selected at
// construction. It performs the same two calls gateway.go's
// validateAndApplySandbox makes after applySandbox returns —
// SetAppliedSandboxMode then SetSandboxBackend(result.Backend) — so the
// endpoint's structured fields (backend, kernel_level, abi_version,
// policy_applied) cannot drift from what the boot actually applied.

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// TestHandleSandboxStatus_ReflectsBackendAfterLandlockDegrade drives
// applySandbox with a stub Linux backend whose ApplyWithMode rejects the
// ruleset (mirroring linuxBackendRejectingApply in
// sandbox_apply_linux_fallback_test.go), then performs the SAME two calls
// gateway.go's validateAndApplySandbox makes right after applySandbox
// returns: SetAppliedSandboxMode and SetSandboxBackend. The status endpoint
// must report the degraded FallbackBackend, not the original stub.
func TestHandleSandboxStatus_ReflectsBackendAfterLandlockDegrade(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Baseline: before any applySandbox/SetSandboxBackend call, the status
	// endpoint reports whatever backend NewAgentLoop selected at construction
	// (this test host's real SelectBackend() result — fallback on a non-Linux
	// dev machine, and never a fabricated ABI claim).
	preBody := doSandboxStatus(t, api)
	if _, hasABI := preBody["abi_version"]; hasABI {
		t.Fatalf("test setup: baseline unexpectedly reports abi_version: %v", preBody)
	}

	// Simulate boot selecting a kernel-capable Landlock backend that then
	// rejects the ruleset on Apply — the exact scenario
	// degradeAfterLandlockFailure exists for.
	stub := &linuxBackendRejectingApply{
		name:       "landlock-v1",
		abiVersion: 1,
		applyErr:   fmt.Errorf("%w: invalid argument", sandbox.ErrLandlockRulesetRejected),
	}
	cfg := &config.Config{}
	cfg.Sandbox.Mode = "enforce"

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  stub,
		GetEnv:   func(string) string { return "" },
	})
	require.NoError(t, err, "applySandbox must degrade gracefully, not error")
	require.Equal(t, "fallback", result.BackendName, "test setup: applySandbox must have degraded to fallback")

	// This is the exact glue gateway.go::validateAndApplySandbox performs
	// after applySandbox returns (SetAppliedSandboxMode, then
	// SetSandboxBackend — see the comment above that call site).
	api.agentLoop.SetAppliedSandboxMode(result.Mode)
	api.agentLoop.SetSandboxBackend(result.Backend)
	api.sandboxResult = result

	postBody := doSandboxStatus(t, api)
	if got := postBody["backend"]; got != "fallback" {
		t.Errorf(`status endpoint "backend" = %v, want "fallback" — still reporting the pre-degrade kernel backend`, got)
	}
	if got := postBody["kernel_level"]; got != false {
		t.Errorf(`status endpoint "kernel_level" = %v, want false — a degraded process must not claim kernel-level enforcement`, got)
	}
	if v, hasABI := postBody["abi_version"]; hasABI {
		t.Errorf(`status endpoint reports "abi_version" = %v after a degrade to FallbackBackend — no ABI version should be claimed`, v)
	}
	if got := postBody["policy_applied"]; got != false {
		t.Errorf(`status endpoint "policy_applied" = %v, want false — Apply() never succeeded on the kernel backend`, got)
	}
}

func doSandboxStatus(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/sandbox-status", nil)
	api.HandleSandboxStatus(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}
