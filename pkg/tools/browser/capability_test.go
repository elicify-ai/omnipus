package browser

import (
	"runtime"
	"testing"
)

// withCapabilitySeams overrides goosForCapability, XvfbAvailableProbe, and
// PulseAudioAvailableProbe for the duration of the test, restoring the
// previous values on cleanup — lets the DS-5 table below exercise every
// platform row (including macOS/Windows) regardless of the host actually
// running the test.
func withCapabilitySeams(t *testing.T, goos string, xvfb, pulse bool) {
	t.Helper()
	prevGOOS, prevXvfb, prevPulse := goosForCapability, XvfbAvailableProbe, PulseAudioAvailableProbe
	goosForCapability = goos
	XvfbAvailableProbe = func() bool { return xvfb }
	PulseAudioAvailableProbe = func() bool { return pulse }
	t.Cleanup(func() {
		goosForCapability = prevGOOS
		XvfbAvailableProbe = prevXvfb
		PulseAudioAvailableProbe = prevPulse
	})
}

// TestVideoCapability_DS5Table exercises the DS-5 stack-detection /
// capability decision table (Test 15, 17, 18) end to end: for every {GOOS,
// installed build(s), Xvfb, PulseAudio} combination in the spec's dataset,
// ClassifyVideoCapability must produce the DS-5-specified classification.
func TestVideoCapability_DS5Table(t *testing.T) {
	// cftPlatform() only resolves on GOOS/GOARCH combinations the CfT feed
	// actually ships; skip if this host itself can't resolve one (matches
	// the installer tests' own skip pattern), since ClassifyVideoCapability
	// calls the real cftPlatform() internally once the linux branch is
	// reached.
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	type scenario struct {
		name            string
		goos            string
		installFull     bool
		installHeadless bool
		xvfb            bool
		pulse           bool
		wantCapable     bool
		wantAudio       bool
	}

	scenarios := []scenario{
		{
			name:        "linux full-chrome verified + Xvfb up + PulseAudio up -> video-capable, audio yes",
			goos:        "linux",
			installFull: true,
			xvfb:        true,
			pulse:       true,
			wantCapable: true,
			wantAudio:   true,
		},
		{
			name:        "linux full-chrome ok + Xvfb up + PulseAudio absent -> video-capable, audio no (silent)",
			goos:        "linux",
			installFull: true,
			xvfb:        true,
			pulse:       false,
			wantCapable: true,
			wantAudio:   false,
		},
		{
			name:        "linux full-chrome ok + Xvfb absent -> not capable",
			goos:        "linux",
			installFull: true,
			xvfb:        false,
			pulse:       true,
			wantCapable: false,
		},
		{
			name:            "linux headless-shell only (no full chrome) -> not capable",
			goos:            "linux",
			installHeadless: true,
			xvfb:            true,
			pulse:           true,
			wantCapable:     false,
		},
		{
			name:        "linux nothing installed -> not capable",
			goos:        "linux",
			xvfb:        true,
			pulse:       true,
			wantCapable: false,
		},
		{
			name:        "macOS -> not capable (M-3, Xvfb/PulseAudio are linux-only)",
			goos:        "darwin",
			installFull: true,
			xvfb:        true,
			pulse:       true,
			wantCapable: false,
		},
		{
			name:        "windows -> not capable (M-3)",
			goos:        "windows",
			installFull: true,
			xvfb:        true,
			pulse:       true,
			wantCapable: false,
		},
		{
			name:            "linux both cached (full + headless-shell) + Xvfb up + PulseAudio up -> video-capable (prefer full), F-08",
			goos:            "linux",
			installFull:     true,
			installHeadless: true,
			xvfb:            true,
			pulse:           true,
			wantCapable:     true,
			wantAudio:       true,
		},
	}

	platform, err := cftPlatform()
	if err != nil {
		t.Fatalf("cftPlatform: %v", err)
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			root := t.TempDir()
			if sc.installFull {
				seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())
			}
			if sc.installHeadless {
				seedBuildBinary(t, root, "131.0.6778.108", platform, headlessShellBuild())
			}
			withCapabilitySeams(t, sc.goos, sc.xvfb, sc.pulse)

			got := ClassifyVideoCapability(root)
			if got.Capable != sc.wantCapable {
				t.Fatalf("Capable = %v, want %v (reason: %q)", got.Capable, sc.wantCapable, got.Reason)
			}
			if got.Capable && got.AudioAvailable != sc.wantAudio {
				t.Fatalf("AudioAvailable = %v, want %v", got.AudioAvailable, sc.wantAudio)
			}
			if !got.Capable && got.Reason == "" {
				t.Fatalf("expected a non-empty operator-facing Reason when not capable (O-3)")
			}
		})
	}
}

// TestVideoCapability_ReasonNeverEmptyOnNotCapable is a narrow O-3 guard: the
// end-user string must stay generic (FR-007), which this package enforces by
// construction (capability.go never returns a Capable=false value with an
// empty Reason) — assert that invariant directly against the real host GOOS
// so it's checked even outside the seamed table above.
func TestVideoCapability_ReasonNeverEmptyOnNotCapable(t *testing.T) {
	root := t.TempDir()
	got := ClassifyVideoCapability(root)
	if !got.Capable && got.Reason == "" {
		t.Fatalf("Capable=false must always carry an operator-facing Reason (O-3); GOOS=%s", runtime.GOOS)
	}
}
