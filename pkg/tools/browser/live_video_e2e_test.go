//go:build browservideo_e2e

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// live_video_e2e_test.go — GATED SKELETON for the NEXT increment's
// real-headful-Chrome pipeline E2E (round-2 test-analysis TC-G1/TC-G2; spec
// docs/internal/specs/live-browser-video-streaming-spec.md R7 Test 24, Test
// 23, Test 26). This file NEVER runs in default CI — it is behind the
// browservideo_e2e build tag, which no `make test` / `go test ./...` target
// carries. Reason: the operator's round-2 decision (r2-ledger.md KEY
// DECISION) is Option B — this increment ships the feature DORMANT
// (browser.DisplaySidecarHealthyProbe defaults false, no Xvfb/PulseAudio
// sidecar wired into any boot sequence — see capability.go's doc comment).
// There is no live sidecar to drive yet, so a real run of this test would
// have nothing real to exercise.
//
// PURPOSE of this file existing now, unrun:
//  1. Document EXACTLY what the next increment's real e2e must do, in code
//     (not just prose), so the shape survives to whoever wires the sidecars.
//  2. Compile-check against the REAL seams (NewDisplaySidecar,
//     SetDisplaySidecar, coordinator.Register, LaunchEncoderPage,
//     StartCapture) so a signature drift in any of them is caught by CI's
//     `go build -tags ...,browservideo_e2e` step even before the sidecar
//     wiring lands — see CLAUDE.md's verify step for this exact build
//     invocation.
//  3. Be an HONEST scaffold: every test function below calls t.Skip() before
//     doing anything live. A green run of this file must mean the real
//     pipeline ran end to end — never a vacuously-skipped stub reported as
//     if it were a passing e2e. Do NOT remove the Skip calls without
//     actually wiring Xvfb/PulseAudio at boot AND adding a real
//     canvas/keyframe-decode assertion (see STEP 6's TODO below for why that
//     specific assertion cannot live in this package at all).
//
// SCOPE NOTE: this file lives in package `browser` (pkg/tools/browser), which
// the gateway package (pkg/gateway) imports — so this package can never
// import pkg/gateway (import cycle). That means the WS-level concepts
// ("attach a video-capable viewer", "the SPA's canvas decodes a keyframe")
// are NOT expressible as compiling Go code here; they are covered by
// step-by-step TODOs instead of faked API calls. What IS expressible here —
// and is written as real (skipped) code below — is everything this package
// itself owns: the Xvfb/PulseAudio sidecars, the coordinator's headful
// launch, the encoder-page CDP launch, and screencast capture.

package browser

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// coldStartBudget is the spec's Test 24 cold-start bound: elapsed time from
// viewer attach to the first decodable keyframe reaching the viewer.
// TODO(next increment): confirm the exact number against
// docs/internal/specs/live-browser-video-streaming-spec.md R7 Test 24 (this
// placeholder is a reasonable guess, not a verified spec value) before this
// budget is used in a real assertion.
const coldStartBudget = 5 * time.Second

// TestLiveVideoE2E_HeadfulPipeline_AttachDriveDetach is the TC-G1/G2 / spec
// Test 24 scaffold: start Xvfb+PulseAudio sidecars, wire them into a real
// BrowserCoordinator via SetDisplaySidecar, launch the encoder page and start
// screencast capture on a real headful Chrome, observe a keyframe arrive
// within the cold-start budget, drive a CDP input event into the agent's own
// tab (the package-`browser`-reachable proxy for "take control -> Input.
// dispatch"), then tear everything down cleanly.
//
// STATUS: SKELETON ONLY — see the file doc comment above for why every
// assertion below is currently unreached.
func TestLiveVideoE2E_HeadfulPipeline_AttachDriveDetach(t *testing.T) {
	t.Skip("SCAFFOLD for the next increment: Xvfb/PulseAudio sidecars are not wired into any boot " +
		"sequence this increment (Option B — see capability.go's DisplaySidecarHealthyProbe doc). " +
		"Un-skip only once gateway boot actually starts+wires the sidecars AND a real canvas/keyframe " +
		"decode assertion exists (see STEP 6 below). TC-G1/TC-G2, spec Test 24.")

	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skipf("Xvfb not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// STEP 1: start the Xvfb virtual-display sidecar (FR-021).
	display := NewDisplaySidecar(DisplayConfig{})
	if err := display.Start(ctx); err != nil {
		t.Fatalf("start Xvfb sidecar: %v", err)
	}
	t.Cleanup(display.Stop)
	if !display.Healthy() {
		t.Fatal("Xvfb sidecar reported unhealthy immediately after Start")
	}

	// STEP 2: start the PulseAudio sidecar (FR-022/FR-023, spec Test 26's
	// audio leg).
	audioCfg, err := DefaultAudioConfig()
	if err != nil {
		t.Fatalf("DefaultAudioConfig: %v", err)
	}
	audio := NewAudioSidecar(audioCfg)
	if audioErr := audio.Start(ctx); audioErr != nil {
		t.Fatalf("start PulseAudio sidecar: %v", audioErr)
	}
	t.Cleanup(audio.Stop)

	// STEP 3: wire the display sidecar into a REAL coordinator BEFORE any
	// launch. SetDisplaySidecar is the exact seam the round-2 architect
	// review (AR-C1/AR-H2) found with zero production callers — this
	// scaffold is what a real caller looks like, and this is also what flips
	// videoLaunchMode from headless-shell to headful in coordinator.go.
	coordinator := NewBrowserCoordinator(t.TempDir(), BrowserConfig{
		Enabled:     true,
		ProfileDir:  t.TempDir(),
		MaxTabs:     5,
		PageTimeout: 30 * time.Second,
	}, 10)
	coordinator.SetDisplaySidecar(display)
	t.Cleanup(coordinator.Shutdown)

	// STEP 4: register an agent through the coordinator — with a healthy
	// display sidecar wired, this should launch HEADFUL Chrome (no
	// --headless), not the pre-video headless-shell default.
	mgr, err := NewBrowserManager(BrowserConfig{Enabled: true, ProfileDir: t.TempDir()}, security.NewSSRFChecker(nil))
	if err != nil {
		t.Fatalf("NewBrowserManager: %v", err)
	}
	agentRootCtx, _, err := coordinator.Register(ctx, "e2e-agent", mgr)
	if err != nil {
		t.Fatalf("coordinator.Register: %v", err)
	}
	agentTabCtx, err := mgr.Session("e2e-session")
	if err != nil {
		t.Fatalf("mgr.Session: %v", err)
	}

	// STEP 5: launch the encoder page (component M) and start screencast
	// capture (component L) on the agent's own tab — the two pieces of the
	// video pipeline THIS package directly owns. A real test wires onFrame
	// to an ingest handler; here it just observes the first frame's arrival
	// for the cold-start-budget proxy in STEP 6.
	frameArrived := make(chan struct{}, 1)
	captureDriver, err := StartCapture(
		agentTabCtx,
		CaptureOptions{Format: "jpeg", Quality: 60, MaxWidth: 1280, MaxHeight: 720},
		func(jpeg []byte, seq uint32, tsMillis uint64) {
			select {
			case frameArrived <- struct{}{}:
			default:
			}
		},
	)
	if err != nil {
		t.Fatalf("StartCapture: %v", err)
	}
	t.Cleanup(captureDriver.Stop)

	// TODO(next increment): also call LaunchEncoderPage(agentRootCtx, cfg)
	// here with a real EncoderLaunchCfg (token/WSURL/StreamID/EncoderURL/
	// Origin from a real loopback listener) once this scaffold is promoted
	// to a real test — omitted here because it requires a real capture-
	// ingest WS server (pkg/gateway) running, which this package cannot
	// import (see the file doc comment's import-cycle note). The real e2e
	// likely needs to run this alongside a real gateway.BrowserVideoOrchestrator,
	// or drive the actual gateway binary over its real WS endpoint — matching
	// the pattern execute_e2e_test.go / tab_adoption_e2e_test.go already use
	// in this package for the non-video CDP surface.
	_ = agentRootCtx

	// STEP 6: assert a decodable keyframe reaches the viewer's canvas within
	// coldStartBudget. TODO(next increment): NO assertion exists yet for the
	// actual spec Test 24 claim ("the SPA decodes and paints a keyframe") —
	// that is fundamentally a browser-JS-side (WebCodecs VideoDecoder +
	// <canvas>) assertion, unreachable from Go in this package. Two real
	// options for the next increment: (a) a Go-side proxy — confirm a
	// keyframe-flagged EncodedChunk reaches the ingest/relay layer within
	// budget (proves bytes moved, not that they were decoded/painted); (b) a
	// real browser-side check via the webapp-testing Playwright skill against
	// the SPA's <canvas> (proves the full claim). This scaffold only proves
	// the weaker Go-side proxy is reachable: a capture frame arrives at all.
	select {
	case <-frameArrived:
	case <-time.After(coldStartBudget):
		t.Fatal("no capture frame arrived within the cold-start budget")
	}

	// STEP 7: take control and assert CDP Input.dispatch reaches the real
	// page — this IS expressible in this package (driving CDP on the agent's
	// own tab is exactly what this package does elsewhere, e.g. tabs.go).
	// TODO(next increment): the real "take control" arbitration is a
	// WS-level browser_control(action=take) concept owned by the gateway
	// (out of this package's reach, per the file doc comment) — this step
	// only proves the CDP mechanics underneath it work, not the full
	// control-arbitration contract.
	if err := chromedp.Run(agentTabCtx, input.DispatchMouseEvent(input.MousePressed, 10, 10)); err != nil {
		t.Fatalf("Input.dispatch via CDP: %v", err)
	}

	// STEP 8: detach and assert clean teardown — mirrors
	// pkg/gateway/browser_stream_test.go's hermetic teardown assertions
	// (encoder tab closed, capture stopped, stream removed), but against the
	// REAL pipeline this time. Handled by the t.Cleanup calls registered
	// above (captureDriver.Stop, coordinator.Shutdown); a real version of
	// this test should additionally assert the encoder tab's Done() channel
	// fires and the loopback listener actually stops accepting connections.
}
