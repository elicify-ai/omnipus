// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_dormant_integration_test.go — Option-A (ADR-044) regression guard
// for the "no full-Chrome encoder build installed yet" dormant posture. Under
// Option A, video-capable means linux + a full-Chrome build already present
// under installRoot for the dedicated live-view encoder browser (see
// capability.go's ClassifyVideoCapability doc) — the agent's own browser is
// always chrome-headless-shell regardless of this classification, and no
// virtual-display or audio sidecar is involved anywhere in the decision (that
// whole apparatus was deleted with the Option-A rearchitecture). "Dormant"
// here simply means: nothing is installed at installRoot yet, so
// ClassifyVideoCapability returns NotCapable and the installer/live-view path
// must behave exactly as it does for any other NotCapable host — the
// live-browser viewer falls back to the existing JPEG browser_screencast
// path.
//
// browser_stream_test.go's existing suite only ever wires a FAKE classifier
// (capable() or an inline notCapable closure) — it proves the orchestrator's
// LOGIC is correct given a capability answer, but never proves the
// orchestrator is actually wired to the REAL browser.ClassifyVideoCapability
// default in a way that keeps the whole pipeline coherently off. That is the
// gap this file closes: it exercises the REAL classifier (never a fake) and
// asserts the orchestrator-level consequence — no encoder launched, no
// capture started, no token minted, and the viewer gets the same generic
// unavailable signal it would from a fake NotCapable classifier.
//
// (The classifier's OWN behavior when nothing is installed, including the
// "NotCapable without a full-Chrome build present" case, is already covered
// directly in pkg/tools/browser/capability_test.go's
// TestVideoCapability_NotCapable_WithoutFullChromeInstalled — this file does
// not duplicate that; it covers the gateway orchestration layer's
// integration with the real classifier instead.)

package gateway

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// TestBrowserVideo_Dormant_OptionA_RealClassifier_NoEncoderNoLiveStream is the
// end-to-end (orchestrator + real classifier) regression guard for the
// Option-A dormant posture: with nothing installed under installRoot yet, a
// viewer attach through the REAL production wiring (Classify left nil so
// newOrchestrator installs browser.ClassifyVideoCapability itself, exactly as
// RegisterBrowserVideo's real callers get it) must never launch an encoder
// page, never start capture, never mint an ingest token, and must leave the
// viewer in the generic unavailable state.
func TestBrowserVideo_Dormant_OptionA_RealClassifier_NoEncoderNoLiveStream(t *testing.T) {
	installRoot := filepath.Join(t.TempDir(), "chromium") // nothing installed at this path
	capab := browser.ClassifyVideoCapability(installRoot)
	if capab.Capable {
		t.Fatalf(
			"expected the real classifier to report NotCapable with the sidecar unwired, got Capable=true (reason=%q)",
			capab.Reason,
		)
	}

	ing := newFakeIngest()
	launch := &fakeLauncher{}
	capb := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}
	o := newOrchestrator(BrowserVideoDeps{
		InstallRoot: installRoot,
		Config: BrowserVideoConfig{
			IngestWSURL: "ws://127.0.0.1:5000/api/v1/browser/capture-ingest",
		},
		// Classify deliberately left nil: newOrchestrator's default wiring
		// (o.classify = browser.ClassifyVideoCapability) is exactly what
		// RegisterBrowserVideo's real production callers get — this test
		// exercises THAT wiring, not a fake.
		LaunchEncoder: launch.launch,
		StartCapture:  capb.start,
		EncoderServer: srv,
	})
	o.ingest = ing

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		AgentCtx:  context.Background(),
		VideoCaps: []string{"avc1.4D401E", "vp8"},
		AudioCaps: []string{"opus"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if h != nil {
		t.Fatal("expected a nil handle — a dormant (NotCapable) install must never start a live video stream")
	}

	// The video pipeline never engaged at all.
	if launch.count() != 0 {
		t.Fatalf("expected NO encoder page launch on a dormant install, got %d", launch.count())
	}
	if capb.count() != 0 {
		t.Fatalf("expected NO capture start on a dormant install, got %d", capb.count())
	}
	if ing.mintCount() != 0 {
		t.Fatalf("expected NO ingest token minted on a dormant install, got %d", ing.mintCount())
	}

	// The viewer still gets a coherent, generic unavailable signal (never a
	// silent hang) — this is what lets the SPA fall back to the existing
	// JPEG browser_screencast path instead of waiting forever for video that
	// will never start.
	f := findStatusError(t, wc)
	if f.Message == nil || *f.Message != browserVideoUnavailableMessage {
		t.Fatalf("expected the generic unavailable message, got %v", f.Message)
	}
}

// TestBrowserVideo_Dormant_vs_Capable_ClassifierIsTheDecidingFactor is a
// differentiation test: with EVERY other seam held identical (same fakes,
// same config), swapping ONLY the classify function between the real dormant
// default and a fake capable() flips the outcome from "no stream" to "stream
// starts." This proves the assertions above are actually caused by the
// classifier's Capable=false — not some unrelated short-circuit (kill-switch,
// empty codec negotiation, etc.) that would make TestBrowserVideo_Dormant_*
// pass for the wrong reason.
func TestBrowserVideo_Dormant_vs_Capable_ClassifierIsTheDecidingFactor(t *testing.T) {
	attach := func(classify classifyFunc, agentID string) (*VideoViewerHandle, *fakeLauncher, *fakeCaptureStarter) {
		ing := newFakeIngest()
		launch := &fakeLauncher{}
		capb := &fakeCaptureStarter{}
		srv := &fakeEncoderServer{}
		o := newTestOrchestrator(classify, ing, launch.launch, capb.start, srv)

		wc := newTestVideoConn()
		h, err := o.AttachViewer(AttachParams{
			WC: wc, AgentID: agentID, SessionID: "sess-" + agentID, ViewerID: "v-" + agentID,
			AgentCtx:  context.Background(),
			VideoCaps: []string{"avc1.4D401E", "vp8"},
		})
		if err != nil {
			t.Fatalf("AttachViewer error (%s): %v", agentID, err)
		}
		return h, launch, capb
	}

	dormantHandle, dormantLaunch, dormantCapture := attach(browser.ClassifyVideoCapability, "agent-dormant")
	if dormantHandle != nil {
		t.Fatal("real dormant classifier: expected nil handle")
	}
	if dormantLaunch.count() != 0 || dormantCapture.count() != 0 {
		t.Fatalf(
			"real dormant classifier: expected no stream machinery; launch=%d capture=%d",
			dormantLaunch.count(), dormantCapture.count(),
		)
	}

	capableHandle, capableLaunch, capableCapture := attach(capable(), "agent-capable")
	if capableHandle == nil {
		t.Fatal("fake capable() classifier: expected a live stream")
	}
	defer capableHandle.Detach()
	if capableLaunch.count() != 1 || capableCapture.count() != 1 {
		t.Fatalf(
			"fake capable() classifier: expected exactly one stream to start; launch=%d capture=%d",
			capableLaunch.count(), capableCapture.count(),
		)
	}
}
