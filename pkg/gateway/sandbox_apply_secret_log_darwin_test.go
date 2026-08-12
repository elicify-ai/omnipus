//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Darwin-only half of the secret-set boot-log tests.
//
// Split out because sandbox.NewSeatbeltBackend is declared in a //go:build
// darwin file, so referencing it from an untagged test compiles on macOS and
// FAILS TO BUILD on Linux — which is exactly what happened: the test was
// written and verified on a Mac, and Linux CI caught it at vet/lint.
//
// The platform-independent cases (fallback backend, a Landlock-shaped fake
// under enforce and under permissive, and the model appearing in both log
// variants) stay in the untagged file and run everywhere.

package gateway

import (
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestApplySandbox_SecretSetLog_Darwin exercises the macOS branch with the
// real SeatbeltBackend where available, covering:
//   - mode=permissive degrades to application-level enforcement (Seatbelt has
//     no audit-only mode) → UNPROTECTED, not seatbelt_deny.
//   - mode=enforce actually installs a profile → protected/seatbelt_deny, and
//     sandbox.applied carries "model" (finding 4, macOS emission site).
//
// Skips on non-darwin or when sandbox-exec is unavailable, matching the
// pattern in sandbox_apply_darwin_test.go.
func TestApplySandbox_SecretSetLog_Darwin(t *testing.T) {
	backend := sandbox.NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host, or not darwin")
	}

	t.Run("permissive degrades to UNPROTECTED, not seatbelt_deny", func(t *testing.T) {
		entries := captureSlogJSON(t)
		cfg := &config.Config{}
		cfg.Sandbox.Mode = "permissive"

		tmp, err := os.CreateTemp(t.TempDir(), "stderr")
		if err != nil {
			t.Fatalf("temp stderr: %v", err)
		}
		defer func() { _ = tmp.Close() }()

		_, err = applySandbox(SandboxApplyOptions{
			Cfg:      cfg,
			HomePath: t.TempDir(),
			Backend:  sandbox.NewSeatbeltBackend(),
			GetEnv:   func(string) string { return "" },
			Stderr:   tmp,
		})
		if err != nil {
			t.Fatalf("applySandbox: %v", err)
		}

		captured := entries()
		if protected := findEvent(captured, "sandbox.secret_set.protected"); protected != nil {
			t.Errorf("mode=permissive on Seatbelt must not claim protection (no profile is installed); got %v", protected)
		}
		if findEvent(captured, "sandbox.secret_set.UNPROTECTED") == nil {
			t.Errorf("expected sandbox.secret_set.UNPROTECTED under mode=permissive; captured events: %v", captured)
		}
	})

	t.Run("enforce actually installs a profile → protected/seatbelt_deny", func(t *testing.T) {
		entries := captureSlogJSON(t)
		cfg := &config.Config{}
		cfg.Sandbox.Mode = "enforce"

		fresh := sandbox.NewSeatbeltBackend()
		_, err := applySandbox(SandboxApplyOptions{
			Cfg:      cfg,
			HomePath: t.TempDir(),
			Backend:  fresh,
			GetEnv:   func(string) string { return "" },
		})
		if err != nil {
			t.Fatalf("applySandbox: %v", err)
		}
		if !fresh.PolicyApplied() {
			t.Fatal("precondition failed: enforce must install a kernel profile")
		}

		captured := entries()
		if unprotected := findEvent(captured, "sandbox.secret_set.UNPROTECTED"); unprotected != nil {
			t.Errorf("mode=enforce with an installed Seatbelt profile must not log UNPROTECTED; got %v", unprotected)
		}
		protected := findEvent(captured, "sandbox.secret_set.protected")
		if protected == nil {
			t.Fatalf("expected sandbox.secret_set.protected; captured events: %v", captured)
		}
		if protected["mechanism"] != "seatbelt_deny" {
			t.Errorf("mechanism = %v, want seatbelt_deny", protected["mechanism"])
		}

		applied := findEvent(captured, "sandbox.applied")
		if applied == nil {
			t.Fatal("expected sandbox.applied to be logged")
		}
		if applied["model"] == nil || applied["model"] == "" {
			t.Errorf("sandbox.applied (macOS emission site) is missing \"model\" (ADR-060 section:10.1); got %v", applied)
		}
	})
}
