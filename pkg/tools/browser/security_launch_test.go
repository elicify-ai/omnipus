// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// security_launch_test.go — round-2 test-analysis TC-G4/TC-G5 (FR-016, Test 3,
// SC-010, SC-017, EC-3). Hermetic security regression guards for the managed
// Chrome launch surface: no fixed CDP TCP port ever opens (SC-017/EC-3), the
// forbidden global media-grant flag never appears (FR-016), and the audio
// getUserMedia consent is scoped by construction to the encoder page's own
// unguessable origin, never a global/process-wide grant.
//
// These tests run in DEFAULT CI (no build tag) — no real Chrome is spawned in
// the managedExecAllocatorOpts-level tests; the composed-argv test spawns a
// trivial `sh` stub (never a browser) purely to observe what cdppipe actually
// hands to exec.Command, mirroring the existing hermetic pattern in
// cdppipe/allocator_test.go (TestLaunchStart_ModifyCmdCannotOverrideStderr).

package browser

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/cdppipe"
)

// ---- FR-016 / TC-G4: the forbidden global media-grant flag ----

// TestManagedExecOpts_ForbiddenFakeUIMediaStreamFlag_NeverPresent is the
// FR-016 negative-space guard: --use-fake-ui-for-media-stream would silently
// auto-grant getUserMedia to EVERY page in a managed Chrome instance,
// including a page the agent itself navigates to — defeating the whole
// origin-scoped-audio-grant design in encoder_launch.go (LaunchEncoderPage's
// doc comment explicitly forbids it, C-2/P-6). Table-driven (single entry)
// over the one managed Chrome process this package launches under the
// ADR-044 Amendment (single full-Chrome, encoder-as-tab) —
// managedExecAllocatorOpts, which now also hosts the WebCodecs encoder tab in
// its default browser context. There is no longer a separate dedicated
// encoder-browser cmdline to assert against.
//
// Traces to: docs/internal/specs/live-browser-video-streaming-spec.md FR-016 / Test 3.
func TestManagedExecOpts_ForbiddenFakeUIMediaStreamFlag_NeverPresent(t *testing.T) {
	cfg := BrowserConfig{ProfileDir: t.TempDir()}

	tests := []struct {
		name    string
		cmdline managedChromeCmdline
	}{
		{name: "agent+encoder full-Chrome launch", cmdline: managedExecAllocatorOpts(cfg, managedLaunchParams{})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, a := range tc.cmdline.Args {
				if a == "--use-fake-ui-for-media-stream" || strings.HasPrefix(a, "--use-fake-ui-for-media-stream=") {
					t.Fatalf(
						"FORBIDDEN flag --use-fake-ui-for-media-stream present in managed launch args: %v",
						tc.cmdline.Args,
					)
				}
			}
		})
	}
}

// ---- SC-017 / EC-3 / TC-G5: no fixed CDP TCP port ----

// TestManagedExecOpts_NoRemoteDebuggingPort complements the MAJ-001
// launch-args contract test in coordinator_test.go
// (TestManagedExecOpts_HeadlessShellNoPortNoDisplay) with a security-framed,
// standalone assertion: the single managed Chrome process this package
// launches — the agent's full-Chrome launch, which under the ADR-044
// Amendment also hosts the WebCodecs encoder tab — may never emit
// --remote-debugging-port. A fixed TCP CDP port would be a
// locally-reachable, unauthenticated control surface for a managed Chrome
// process (SC-017/EC-3). CDP flows exclusively over the cdppipe fd 3/4 pipe.
//
// Traces to: docs/internal/specs/live-browser-video-streaming-spec.md SC-017 / EC-3.
func TestManagedExecOpts_NoRemoteDebuggingPort(t *testing.T) {
	cfg := BrowserConfig{ProfileDir: t.TempDir()}

	tests := []struct {
		name    string
		cmdline managedChromeCmdline
	}{
		{name: "agent+encoder full-Chrome launch", cmdline: managedExecAllocatorOpts(cfg, managedLaunchParams{})},
	}

	for _, tc := range tests {
		for _, a := range tc.cmdline.Args {
			if strings.HasPrefix(a, "--remote-debugging-port") {
				t.Fatalf(
					"%s must never set --remote-debugging-port; got %v",
					tc.name, tc.cmdline.Args,
				)
			}
		}
	}
}

// TestSecurityLaunch_ComposedPipeArgv_HasPipeFlagNoTCPPort is the TC-G5
// default-run (tag-independent) proof of the actual argv cdppipe hands to
// exec.Command for a managed launch: it MUST contain --remote-debugging-pipe
// (selects the fd 3/4 NUL-framed transport) and MUST NOT contain
// --remote-debugging-port anywhere (SC-017/EC-3 — "no TCP CDP listener ever
// opens"). managedExecAllocatorOpts's own output (asserted above) never adds
// --remote-debugging-pipe itself — that flag is cdppipe's own contribution
// (buildArgs, cdppipe/allocator.go) — so this test observes the FULLY
// COMPOSED argv the same way a real launch would build it, via cdppipe's own
// exported NewPipeAllocator + PipeOptions.ModifyCmd seam (the same hermetic
// technique cdppipe/allocator_test.go already uses for its own package-local
// tests), rather than reaching into cdppipe's unexported `launch` type from
// this package.
//
// The stub execPath ("sh") is not a real Chrome and never speaks CDP, so
// NewPipeAllocator is EXPECTED to fail once it tries the liveness probe —
// this test only cares about the argv captured via ModifyCmd, which runs
// before Chrome (or, here, the stub) is even started.
func TestSecurityLaunch_ComposedPipeArgv_HasPipeFlagNoTCPPort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only fake binary (sh)")
	}
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH — cannot run the hermetic stub-binary capture")
	}

	cfg := BrowserConfig{ProfileDir: t.TempDir()}
	managedArgs := managedExecAllocatorOpts(cfg, managedLaunchParams{}).Args

	var captured []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, allocCancel, allocErr := cdppipe.NewPipeAllocator(ctx, shPath, cdppipe.PipeOptions{
		// "exit 0" so the stub process terminates immediately — it will never
		// perform a real CDP handshake, so NewPipeAllocator's liveness probe
		// is expected to fail; only the captured argv matters here.
		Args: append([]string{"-c", "exit 0"}, managedArgs...),
		ModifyCmd: func(cmd *exec.Cmd) {
			captured = append([]string{}, cmd.Args...)
		},
		DialTimeout: 500 * time.Millisecond,
		UserDataDir: t.TempDir(),
	})
	if allocCancel != nil {
		t.Cleanup(allocCancel)
	}
	if allocErr == nil {
		t.Fatal(
			"expected NewPipeAllocator to fail against a non-Chrome sh stub (no real CDP handshake) — " +
				"if this now succeeds, the fake-binary assumption this test relies on has changed",
		)
	}
	if captured == nil {
		t.Fatal("ModifyCmd never ran — cannot inspect the composed launch argv")
	}

	hasPipeFlag := false
	for _, a := range captured {
		if a == "--remote-debugging-pipe" {
			hasPipeFlag = true
		}
		if strings.HasPrefix(a, "--remote-debugging-port") {
			t.Fatalf("composed managed-launch argv must NEVER contain --remote-debugging-port; got %v", captured)
		}
		if a == "--use-fake-ui-for-media-stream" || strings.HasPrefix(a, "--use-fake-ui-for-media-stream=") {
			t.Fatalf("composed managed-launch argv must NEVER contain the forbidden fake-ui flag; got %v", captured)
		}
	}
	if !hasPipeFlag {
		t.Fatalf(
			"composed managed-launch argv must contain --remote-debugging-pipe (cdppipe's own flag); got %v",
			captured,
		)
	}
}

// ---- FR-016 audio-grant origin-scoping (config layer) ----

// TestEncoderLaunchCfg_ValidateRequiresOrigin_NoUnscopedGrantPossible is the
// FR-016 config-layer guard for the audio getUserMedia grant: LaunchEncoderPage
// grants audio-capture consent via
//
//	browser.SetPermission(...).WithOrigin(cfg.Origin).WithBrowserContextID(bid)
//
// (encoder_launch.go, LaunchEncoderPage) — i.e. the grant is ALWAYS scoped to
// BOTH a specific Origin string AND a specific browser context, never issued
// bare/globally. EncoderLaunchCfg.validate() makes Origin a hard precondition
// of any launch at all (an empty Origin fails BEFORE any CDP action runs), so
// a caller cannot construct a launch that grants audio without also supplying
// the scoping origin — there is no code path that reaches the
// browser.SetPermission call with an empty/global scope.
//
// Exercising the ACTUAL CDP grant call (that it lands on the exact origin and
// is invisible to a differently-navigated tab) requires a live headful Chrome
// target — that is out of hermetic reach here and is exactly what the gated
// real-headful-Chrome pipeline scaffold (live_video_e2e_test.go, TC-G1/G2,
// build-tagged browservideo_e2e) documents for the next increment. This test
// covers what IS reachable without real Chrome: the type-level precondition
// that makes an unscoped grant unconstructable through this API.
//
// Traces to: pkg/tools/browser/encoder_launch.go — SECURITY POSTURE doc
// comment (FR-016/MAJ-003) and EncoderLaunchCfg.validate().
func TestEncoderLaunchCfg_ValidateRequiresOrigin_NoUnscopedGrantPossible(t *testing.T) {
	base := EncoderLaunchCfg{
		Token:      "tok-1",
		WSURL:      "ws://127.0.0.1:5000/api/v1/browser/capture-ingest",
		StreamID:   "stream-1",
		VideoCodec: "avc1.4D4028",
		EncoderURL: "http://127.0.0.1:12345/enc/secret",
		Origin:     "http://127.0.0.1:12345",
		HasAudio:   true,
	}

	// Differentiation: a fully-populated cfg (with Origin) validates cleanly...
	if err := base.validate(); err != nil {
		t.Fatalf("expected a fully-populated cfg (Origin set) to validate, got: %v", err)
	}

	// ...but the exact same cfg with ONLY Origin cleared must be rejected —
	// proving Origin is independently load-bearing, not merely one of several
	// interchangeable required fields that happen to also be set.
	noOrigin := base
	noOrigin.Origin = ""
	if err := noOrigin.validate(); err == nil {
		t.Fatal(
			"expected validate() to reject an empty Origin — an audio grant must never be " +
				"issuable without an explicit scoping origin (FR-016)",
		)
	}

	// A completely empty cfg is rejected too (content check: the error names
	// the actual missing field, not a generic failure).
	zero := EncoderLaunchCfg{}
	if err := zero.validate(); err == nil {
		t.Fatal("expected validate() to reject a zero-value EncoderLaunchCfg")
	} else if !strings.Contains(err.Error(), "Token") {
		t.Fatalf("expected the zero-value validation error to name the first missing field (Token), got: %v", err)
	}
}

// TestLaunchEncoderPage_NilRootCtxRejectedBeforeAnyCDPAction proves
// LaunchEncoderPage fails closed (no CDP action attempted, no accidental
// bare/global grant) when handed a nil rootCtx — this is checked before
// cfg.validate() even runs, so it holds regardless of whether HasAudio is
// requested.
func TestLaunchEncoderPage_NilRootCtxRejectedBeforeAnyCDPAction(t *testing.T) {
	_, err := LaunchEncoderPage(nil, EncoderLaunchCfg{ //nolint:staticcheck // deliberate nil to exercise the guard
		Token:      "tok-1",
		WSURL:      "ws://127.0.0.1:5000/api/v1/browser/capture-ingest",
		StreamID:   "stream-1",
		VideoCodec: "avc1.4D4028",
		EncoderURL: "http://127.0.0.1:12345/enc/secret",
		Origin:     "http://127.0.0.1:12345",
		HasAudio:   true,
	})
	if err == nil {
		t.Fatal("expected LaunchEncoderPage(nil rootCtx, ...) to fail closed")
	}
	if !strings.Contains(err.Error(), "rootCtx") {
		t.Fatalf("expected the error to name rootCtx as the cause, got: %v", err)
	}
}
