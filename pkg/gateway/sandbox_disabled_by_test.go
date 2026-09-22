// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestSandboxHealth_DisabledByOnlyWhenOff pins the contract SandboxApplyResult
// states in its own field doc: DisabledBy identifies the source of a Mode=Off
// decision and is "Empty when Mode is enforce or permissive".
//
// It was populated unconditionally from resolveMode's SOURCE return, so a
// sandbox that was fully enforcing still advertised that something had
// disabled it. Measured live on a GitHub runner with Landlock ABI v7
// (2026-08-19): /health returned backend=landlock-v7, landlock_enforced=true,
// seccomp_enforced=true AND disabled_by="config" together. An operator reading
// that would go looking for a setting to change while the sandbox was already
// doing exactly what they asked.
//
// The same constant appeared on a kernel with no Landlock (Fly, ENOSYS), where
// the true reason was known and logged as kernel_too_old_or_non_linux — so the
// field was wrong whether or not enforcement was live.
func TestSandboxHealth_DisabledByOnlyWhenOff(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     sandbox.Mode
		wantKept bool
	}{
		{"enforce never reports a disabler", sandbox.ModeEnforce, false},
		{"permissive never reports a disabler", sandbox.ModePermissive, false},
		{"off reports what turned it off", sandbox.ModeOff, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &SandboxApplyResult{Mode: tc.mode, BackendName: "landlock-v7"}
			if tc.mode == sandbox.ModeOff {
				result.DisabledBy = "config"
			}

			srv := &fakeSandboxHealthSetter{}
			registerSandboxHealthCheck(srv, result)
			if srv.fn == nil {
				t.Fatal("registerSandboxHealthCheck did not install an info func")
			}
			info := srv.fn()

			_, present := info["disabled_by"]
			if present != tc.wantKept {
				t.Errorf("disabled_by present = %v, want %v (mode=%s, info=%v)",
					present, tc.wantKept, tc.mode, info)
			}
			// Positive lower bound: the map must still describe the sandbox,
			// or this test would pass against a builder that emitted nothing.
			if got := info["mode"]; got != string(tc.mode) {
				t.Errorf("mode = %v, want %q", got, tc.mode)
			}
			if got := info["backend"]; got != "landlock-v7" {
				t.Errorf("backend = %v, want landlock-v7", got)
			}
		})
	}
}

// TestSandboxHealth_AppliedFalseOnFallback pins the one clause that keeps
// /health's "applied" field honest after a degrade: a process whose backend
// was swapped to "fallback" reports applied=false even when the operator asked
// for enforce mode. BackendName is what distinguishes real kernel enforcement
// from a degraded boot — Mode stays "enforce" through the degrade and cannot
// tell them apart.
func TestSandboxHealth_AppliedFalseOnFallback(t *testing.T) {
	cases := []struct {
		name        string
		mode        sandbox.Mode
		backendName string
		wantApplied bool
	}{
		{"enforce_with_landlock_is_applied", sandbox.ModeEnforce, "landlock-v7", true},
		{"enforce_degraded_to_fallback_is_not_applied", sandbox.ModeEnforce, "fallback", false},
		{"permissive_degraded_to_fallback_is_not_applied", sandbox.ModePermissive, "fallback", false},
		{"off_is_not_applied", sandbox.ModeOff, "fallback", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := &SandboxApplyResult{Mode: tc.mode, BackendName: tc.backendName}
			srv := &fakeSandboxHealthSetter{}
			registerSandboxHealthCheck(srv, result)
			if srv.fn == nil {
				t.Fatal("registerSandboxHealthCheck did not install an info func")
			}
			info := srv.fn()
			if got := info["applied"]; got != tc.wantApplied {
				t.Errorf("applied = %v, want %v (mode=%s backend=%s)", got, tc.wantApplied, tc.mode, tc.backendName)
			}
		})
	}
}

type fakeSandboxHealthSetter struct{ fn func() map[string]any }

func (f *fakeSandboxHealthSetter) SetSandboxInfoFunc(fn func() map[string]any) { f.fn = fn }
