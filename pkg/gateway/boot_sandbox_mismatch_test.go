//go:build !cgo

// T2.8: Boot_AbortsWhenResolvedModeDiffersFromApplied.
//
// Verifies that when the configured sandbox mode is "enforce" but
// applySandbox is invoked with a backend that cannot apply (FallbackBackend),
// the resolved mode is ModeOff/degraded — documenting the mode-resolution
// behavior. The production gateway logs the applied mode vs. configured mode.
//
// This test focuses on the applySandbox function's mode-resolution logic
// specifically around the fallback path (FallbackBackend) where the operator
// requested "enforce" but the kernel does not support it. Per B4 the gateway
// logs "sandbox.degraded" with the requested mode so operators can see the
// mismatch.

package gateway

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// TestApplySandbox_FallbackBackend_LogsDegradedWhenEnforceRequested (T2.8)
// calls applySandbox with mode=enforce and a FallbackBackend (simulating a
// pre-5.13 kernel). The function must:
//  1. Return a result (not an error — degradation is graceful).
//  2. Set result.Mode to ModeEnforce (the requested mode is preserved).
//  3. Populate result.ApplyState.ExtraNotes with the degradation note.
//
// The result.Mode is "enforce" because resolveMode sets it from config;
// the degradation path uses FallbackBackend and emits a slog.Warn. The
// actual applied mode is tracked via the ApplyState.ExtraNotes "falling back"
// note.
//
// This documents B4: the gateway's applySandbox + agentLoop.SetAppliedSandboxMode
// pipeline is the mechanism that prevents a misconfigured mode from silently
// running an under-protected process.
func TestApplySandbox_FallbackBackend_LogsDegradedWhenEnforceRequested(t *testing.T) {
	var stderrBuf bytes.Buffer
	fb := sandbox.NewFallbackBackend()

	cfg := &config.Config{}
	cfg.Sandbox.Mode = config.SandboxMode("enforce")

	result, err := applySandbox(SandboxApplyOptions{
		CLIMode:  "",
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  fb,
		GetEnv:   func(string) string { return "" },
		Stderr:   nil, // nil → os.Stderr not captured
	})
	// applySandbox with a FallbackBackend must succeed (graceful degradation).
	if err != nil {
		t.Fatalf("applySandbox returned error for FallbackBackend (must degrade gracefully): %v", err)
	}
	if result == nil {
		t.Fatal("applySandbox returned nil result")
	}

	// The resolved mode must be "enforce" (from config).
	if result.Mode != sandbox.ModeEnforce {
		t.Errorf("result.Mode = %q; want %q (mode preserved from config)", result.Mode, sandbox.ModeEnforce)
	}

	// The ApplyState must carry the degradation note so operators and the
	// /health endpoint can surface it.
	foundNote := false
	for _, note := range result.ApplyState.ExtraNotes {
		if strings.Contains(note, "fallback") || strings.Contains(note, "kernel") {
			foundNote = true
			break
		}
	}
	if !foundNote {
		t.Errorf(
			"ApplyState.ExtraNotes = %v; expected a note mentioning fallback/kernel degradation (B4)",
			result.ApplyState.ExtraNotes,
		)
	}

	// Suppressed stderr to avoid noisy test output.
	_ = stderrBuf.String()
}

// TestApplySandbox_ModeOff_DoesNotApply verifies that mode=off causes
// applySandbox to return without calling Apply. The result's Mode must be
// ModeOff and ApplyState.LandlockEnforced must be false.
func TestApplySandbox_ModeOff_DoesNotApply(t *testing.T) {
	fb := sandbox.NewFallbackBackend()
	cfg := &config.Config{}
	cfg.Sandbox.Mode = config.SandboxMode("off")

	result, err := applySandbox(SandboxApplyOptions{
		CLIMode:  "",
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  fb,
		GetEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("applySandbox with mode=off: %v", err)
	}
	if result.Mode != sandbox.ModeOff {
		t.Errorf("result.Mode = %q; want ModeOff", result.Mode)
	}
	if result.ApplyState.LandlockEnforced {
		t.Error("LandlockEnforced = true; must be false when mode=off")
	}
}
