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

	// Simulate boot selecting a kernel-capable Landlock backend that then
	// rejects the ruleset on Apply — the exact scenario
	// degradeAfterLandlockFailure exists for.
	stub := &linuxBackendRejectingApply{
		name:       "landlock-v1",
		abiVersion: 1,
		applyErr:   fmt.Errorf("%w: invalid argument", sandbox.ErrLandlockRulesetRejected),
	}

	// Baseline: install that kernel-capable backend as the loop's selection, so
	// the pre-degrade state is the SAME on every host. The precondition this
	// test needs is "the endpoint currently reports a kernel backend that is
	// not the fallback"; without one, the post-degrade assertions below could
	// be satisfied by an endpoint that never noticed the swap.
	//
	// This used to read the host's real SelectBackend() result and assert the
	// baseline carried NO abi_version — using "no ABI" as a stand-in for "not
	// a Landlock backend". That stand-in only holds on a host without
	// Landlock: it is true on a macOS dev machine (Seatbelt is kernel-level
	// and simply has no versioned ABI) and false on the Linux CI runner, which
	// reported its genuine landlock-v7 / abi_version:7 and tripped the setup
	// guard (release run 35778464975, "baseline unexpectedly reports
	// abi_version"). Nothing was fabricated there — the expectation was wrong,
	// and it was wrong on exactly the one platform that can run this code for
	// real. Injecting the backend removes the host from the equation.
	api.agentLoop.SetSandboxBackend(stub)
	preBody := doSandboxStatus(t, api)
	if got := preBody["backend"]; got != "landlock-v1" {
		t.Fatalf(`test setup: baseline "backend" = %v, want "landlock-v1": %v`, got, preBody)
	}
	if got := preBody["kernel_level"]; got != true {
		t.Fatalf(`test setup: baseline "kernel_level" = %v, want true: %v`, got, preBody)
	}
	if got := preBody["abi_version"]; got != float64(1) {
		t.Fatalf(`test setup: baseline "abi_version" = %v, want 1: %v`, got, preBody)
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
