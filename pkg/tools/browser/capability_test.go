package browser

import (
	"runtime"
	"testing"
)

// withCapabilitySeams overrides goosForCapability, DisplaySidecarHealthyProbe,
// and PulseAudioAvailableProbe for the duration of the test, restoring the
// previous values on cleanup — lets the DS-5 table below exercise every
// platform row (including macOS/Windows) regardless of the host actually
// running the test. sidecarHealthy stands in for DisplaySidecarHealthyProbe
// (AR-C1/GC-1/GC-2's Option-B gate) — NOT an Xvfb-binary-on-PATH probe; the
// table below seams it directly to exercise every DS-5 row without needing a
// live, wired sidecar.
func withCapabilitySeams(t *testing.T, goos string, sidecarHealthy, pulse bool) {
	t.Helper()
	prevGOOS, prevSidecar, prevPulse := goosForCapability, DisplaySidecarHealthyProbe, PulseAudioAvailableProbe
	goosForCapability = goos
	DisplaySidecarHealthyProbe = func() bool { return sidecarHealthy }
	PulseAudioAvailableProbe = func() bool { return pulse }
	t.Cleanup(func() {
		goosForCapability = prevGOOS
		DisplaySidecarHealthyProbe = prevSidecar
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
		sidecarHealthy  bool // seams DisplaySidecarHealthyProbe (AR-C1/GC-1/GC-2), not an Xvfb-on-PATH probe
		pulse           bool
		wantCapable     bool
		wantAudio       bool
		wantLevel       CapabilityLevel
	}

	scenarios := []scenario{
		{
			name:           "linux full-chrome verified + display sidecar healthy + PulseAudio up -> video-capable, audio yes",
			goos:           "linux",
			installFull:    true,
			sidecarHealthy: true,
			pulse:          true,
			wantCapable:    true,
			wantAudio:      true,
			wantLevel:      VideoAndAudioLevel,
		},
		{
			name:           "linux full-chrome ok + display sidecar healthy + PulseAudio absent -> video-capable, audio no (silent)",
			goos:           "linux",
			installFull:    true,
			sidecarHealthy: true,
			pulse:          false,
			wantCapable:    true,
			wantAudio:      false,
			wantLevel:      VideoOnlyLevel,
		},
		{
			name:           "linux full-chrome ok + display sidecar NOT healthy -> not capable (Option-B dormant gate)",
			goos:           "linux",
			installFull:    true,
			sidecarHealthy: false,
			pulse:          true,
			wantCapable:    false,
			wantLevel:      NotCapableLevel,
		},
		{
			name:            "linux headless-shell only (no full chrome) -> not capable",
			goos:            "linux",
			installHeadless: true,
			sidecarHealthy:  true,
			pulse:           true,
			wantCapable:     false,
			wantLevel:       NotCapableLevel,
		},
		{
			name:           "linux nothing installed -> not capable",
			goos:           "linux",
			sidecarHealthy: true,
			pulse:          true,
			wantCapable:    false,
			wantLevel:      NotCapableLevel,
		},
		{
			name:           "macOS -> not capable (M-3, Xvfb/PulseAudio are linux-only)",
			goos:           "darwin",
			installFull:    true,
			sidecarHealthy: true,
			pulse:          true,
			wantCapable:    false,
			wantLevel:      NotCapableLevel,
		},
		{
			name:           "windows -> not capable (M-3)",
			goos:           "windows",
			installFull:    true,
			sidecarHealthy: true,
			pulse:          true,
			wantCapable:    false,
			wantLevel:      NotCapableLevel,
		},
		{
			name:            "linux both cached (full + headless-shell) + display sidecar healthy + PulseAudio up -> video-capable (prefer full), F-08",
			goos:            "linux",
			installFull:     true,
			installHeadless: true,
			sidecarHealthy:  true,
			pulse:           true,
			wantCapable:     true,
			wantAudio:       true,
			wantLevel:       VideoAndAudioLevel,
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
			withCapabilitySeams(t, sc.goos, sc.sidecarHealthy, sc.pulse)

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
			// TD5: Level must never disagree with the derived Capable/
			// AudioAvailable booleans — it is the exhaustive, non-overloaded
			// source of truth the flag pair is derived from.
			if got.Level != sc.wantLevel {
				t.Fatalf("Level = %v, want %v", got.Level, sc.wantLevel)
			}
			if (got.Level != NotCapableLevel) != got.Capable {
				t.Fatalf("Level %v disagrees with Capable=%v (illegal state)", got.Level, got.Capable)
			}
			if (got.Level == VideoAndAudioLevel) != got.AudioAvailable {
				t.Fatalf("Level %v disagrees with AudioAvailable=%v (illegal state)", got.Level, got.AudioAvailable)
			}
			// TD5: Reason must no longer double as the audio-absent
			// explanation when Capable is true — that lives in AudioReason.
			if got.Capable && got.Reason != "" {
				t.Fatalf("Reason must be empty when Capable=true (de-overloaded by TD5), got %q", got.Reason)
			}
			if got.Level == VideoOnlyLevel && got.AudioReason == "" {
				t.Fatalf("expected a non-empty AudioReason when video-capable but audio unavailable")
			}
			if got.Level != VideoOnlyLevel && got.AudioReason != "" {
				t.Fatalf("expected an empty AudioReason outside VideoOnlyLevel, got %q", got.AudioReason)
			}
		})
	}
}

// TestCapabilityLevel_String_IsStable pins CapabilityLevel.String()'s output
// so it stays log/telemetry-friendly and doesn't silently drift.
func TestCapabilityLevel_String_IsStable(t *testing.T) {
	cases := map[CapabilityLevel]string{
		NotCapableLevel:    "not_capable",
		VideoOnlyLevel:     "video_only",
		VideoAndAudioLevel: "video_and_audio",
	}
	for level, want := range cases {
		if got := level.String(); got != want {
			t.Fatalf("CapabilityLevel(%d).String() = %q, want %q", level, got, want)
		}
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

// TestVideoCapability_DormantByDefault_OptionB is the AR-C1/GC-1/GC-2
// coherent-dormant regression guard: with DisplaySidecarHealthyProbe left
// UNTOUCHED (the shipped default, defaultDisplaySidecarHealthy, which always
// returns false), classification must stay NotCapable — even on a linux host
// with a full Chrome build already installed. This proves the SHIPPED
// DEFAULT is dormant, not just a seamed test override; it must never regress
// back into reading an Xvfb-binary-on-PATH signal (a host merely having the
// xvfb/pulseaudio OS packages installed must never flip this true on its
// own).
func TestVideoCapability_DormantByDefault_OptionB(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Fatalf("cftPlatform: %v", err)
	}
	root := t.TempDir()
	seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())

	// Force goos=linux (the real test host may not be linux) but deliberately
	// leave DisplaySidecarHealthyProbe and PulseAudioAvailableProbe alone —
	// the whole point is to exercise the package's REAL default, not a seam.
	prevGOOS := goosForCapability
	goosForCapability = "linux"
	t.Cleanup(func() { goosForCapability = prevGOOS })

	got := ClassifyVideoCapability(root)
	if got.Capable {
		t.Fatalf(
			"expected NotCapable by default (Option-B dormant gate) even with full Chrome installed, got Capable=true (reason=%q)",
			got.Reason,
		)
	}
	if got.Level != NotCapableLevel {
		t.Fatalf("expected NotCapableLevel by default, got %v", got.Level)
	}
	if got.Reason == "" {
		t.Fatalf("expected a non-empty operator-facing Reason (O-3)")
	}
}
