//go:build browservideo_e2e

// Diagnostic for the live-browser video boot-wiring (ADR-044). Runs ONLY under
// the browservideo_e2e tag on a host with the real display stack (Xvfb +
// PulseAudio + a full-Chrome install), e.g. the ci-omnipus worker — never in the
// default suite or the dev pod. It reproduces exactly what gateway boot's
// bootLiveBrowserVideo B4 evaluates — start the Xvfb sidecar, flip the display
// probe, ensure a full-Chrome build, then ClassifyVideoCapability — and prints
// the Capable/Reason so a wire failure is visible in test output rather than
// buried in a filtered log. Point DIAG_INSTALL_ROOT at a cached chromium install
// to skip the ~130MB download.
package browser

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestDiag_VideoCapabilityOnRealHost(t *testing.T) {
	root := os.Getenv("DIAG_INSTALL_ROOT")
	if root == "" {
		root = t.TempDir()
	}
	t.Logf("install_root=%q GOOS-capability=%q", root, goosForCapability)

	d := NewDisplaySidecar(DisplayConfig{})
	startErr := d.Start(context.Background())
	t.Logf("XVFB: Start err=%v Healthy=%v Display=%q", startErr, d.Healthy(), d.Display())
	if startErr == nil {
		defer d.Stop()
		DisplaySidecarHealthyProbe = d.Healthy
	}

	// Audio is optional (videoOnly is still Capable) but exercise it too.
	if acfg, aerr := DefaultAudioConfig(); aerr == nil {
		acfg.SocketDir = t.TempDir()
		a := NewAudioSidecar(acfg)
		if serr := a.Start(context.Background()); serr == nil {
			defer a.Stop()
			time.Sleep(3 * time.Second) // let bring-up finish
			PulseAudioAvailableProbe = a.Healthy
			t.Logf("AUDIO: Healthy=%v", a.Healthy())
		} else {
			t.Logf("AUDIO: Start err=%v", serr)
		}
	} else {
		t.Logf("AUDIO: DefaultAudioConfig err=%v", aerr)
	}

	path, cerr := EnsureChromium(context.Background(), root)
	t.Logf("CHROME: EnsureChromium path=%q err=%v", path, cerr)

	vc := ClassifyVideoCapability(root)
	t.Logf("CAPABILITY: Capable=%v AudioAvailable=%v Reason=%q AudioReason=%q",
		vc.Capable, vc.AudioAvailable, vc.Reason, vc.AudioReason)

	if !vc.Capable {
		t.Errorf("NOT video-capable on this real host — reason: %s", vc.Reason)
	}
}
