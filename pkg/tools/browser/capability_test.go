package browser

import (
	"runtime"
	"testing"
)

// withCapabilitySeams overrides goosForCapability for the duration of the
// test, restoring the previous value on cleanup — lets the platform-matrix
// rows below exercise every platform (including macOS/Windows) regardless of
// the host actually running the test.
func withCapabilitySeams(t *testing.T, goos string) {
	t.Helper()
	prev := goosForCapability
	goosForCapability = goos
	t.Cleanup(func() { goosForCapability = prev })
}

// TestVideoCapability_DS5Table exercises the WebRTC capability-decision
// table end to end: for every {GOOS, installed build(s)} combination in the
// dataset, ClassifyVideoCapability must produce the specified
// classification. Unlike the superseded WebCodecs-relay design, WebRTC
// tabCapture delivers video AND audio together (WV1 spike Q2) — every
// Capable=true row here must land on VideoAndAudioLevel, never
// VideoOnlyLevel.
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
		wantCapable     bool
		wantLevel       CapabilityLevel
	}

	scenarios := []scenario{
		{
			name:        "linux full-chrome installed -> video-capable, audio included",
			goos:        "linux",
			installFull: true,
			wantCapable: true,
			wantLevel:   VideoAndAudioLevel,
		},
		{
			name:            "linux headless-shell only (no full chrome) -> not capable",
			goos:            "linux",
			installHeadless: true,
			wantCapable:     false,
			wantLevel:       NotCapableLevel,
		},
		{
			name:        "linux nothing installed -> not capable",
			goos:        "linux",
			wantCapable: false,
			wantLevel:   NotCapableLevel,
		},
		{
			name:        "macOS -> not capable (live-view requires linux)",
			goos:        "darwin",
			installFull: true,
			wantCapable: false,
			wantLevel:   NotCapableLevel,
		},
		{
			name:        "windows -> not capable (live-view requires linux)",
			goos:        "windows",
			installFull: true,
			wantCapable: false,
			wantLevel:   NotCapableLevel,
		},
		{
			name:        "android/termux -> not capable (live-view requires linux)",
			goos:        "android",
			installFull: true,
			wantCapable: false,
			wantLevel:   NotCapableLevel,
		},
		{
			name:            "linux both cached (full + headless-shell) -> video-capable, audio included",
			goos:            "linux",
			installFull:     true,
			installHeadless: true,
			wantCapable:     true,
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
			withCapabilitySeams(t, sc.goos)

			got := ClassifyVideoCapability(root)
			if got.Capable != sc.wantCapable {
				t.Fatalf("Capable = %v, want %v (reason: %q)", got.Capable, sc.wantCapable, got.Reason)
			}
			// WebRTC tabCapture always delivers video+audio together — every
			// capable host must report AudioAvailable too.
			if got.Capable != got.AudioAvailable {
				t.Fatalf("AudioAvailable = %v, want %v (WebRTC tabCapture always includes audio)", got.AudioAvailable, got.Capable)
			}
			if !got.Capable && got.Reason == "" {
				t.Fatalf("expected a non-empty operator-facing Reason when not capable (O-3)")
			}
			// Level must never disagree with the derived Capable/
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
			// Reason must no longer double as the audio-absent explanation
			// when Capable is true.
			if got.Capable && got.Reason != "" {
				t.Fatalf("Reason must be empty when Capable=true, got %q", got.Reason)
			}
			// ClassifyVideoCapability never produces VideoOnlyLevel today —
			// WebRTC tabCapture has no video-without-audio outcome — so
			// AudioReason must always be empty.
			if got.AudioReason != "" {
				t.Fatalf("expected empty AudioReason (WebRTC tabCapture never yields video-only), got %q", got.AudioReason)
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

// TestVideoCapability_ReasonNeverEmptyOnNotCapable is a narrow guard: the
// end-user string must stay generic, which this package enforces by
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

// TestVideoCapability_NotCapable_WithoutFullChromeInstalled is the
// regression guard: on a linux host with NOTHING installed under
// installRoot yet, classification must stay NotCapable — it must never
// download anything itself (ClassifyVideoCapability only inspects disk).
func TestVideoCapability_NotCapable_WithoutFullChromeInstalled(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	root := t.TempDir()

	withCapabilitySeams(t, "linux")

	got := ClassifyVideoCapability(root)
	if got.Capable {
		t.Fatalf("expected NotCapable with no full-Chrome build installed, got Capable=true (reason=%q)", got.Reason)
	}
	if got.Level != NotCapableLevel {
		t.Fatalf("expected NotCapableLevel, got %v", got.Level)
	}
	if got.Reason == "" {
		t.Fatalf("expected a non-empty operator-facing Reason (O-3)")
	}
}
