package browser

import (
	"runtime"
	"testing"
)

// newCapabilityTestManager builds a bare *BrowserManager (no ssrf/no live
// registry — this file only ever calls CaptureVideoCapability/VideoCapability
// on it, never a tool) with a ProfileDir under t.TempDir(), for
// CaptureVideoCapability's ADR-048 tests below to seed a fake Chrome build at
// the EXACT InstallRoot() the manager itself will resolve to.
func newCapabilityTestManager(t *testing.T) *BrowserManager {
	t.Helper()
	return &BrowserManager{
		cfg: BrowserConfig{ProfileDir: t.TempDir() + "/profiles/default"},
	}
}

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
// classification.
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
	}

	scenarios := []scenario{
		{
			name:        "linux full-chrome installed -> video-capable",
			goos:        "linux",
			installFull: true,
			wantCapable: true,
		},
		{
			name:            "linux headless-shell only (no full chrome) -> not capable",
			goos:            "linux",
			installHeadless: true,
			wantCapable:     false,
		},
		{
			name:        "linux nothing installed -> not capable",
			goos:        "linux",
			wantCapable: false,
		},
		{
			name:        "macOS -> not capable (live-view requires linux)",
			goos:        "darwin",
			installFull: true,
			wantCapable: false,
		},
		{
			name:        "windows -> not capable (live-view requires linux)",
			goos:        "windows",
			installFull: true,
			wantCapable: false,
		},
		{
			name:        "android/termux -> not capable (live-view requires linux)",
			goos:        "android",
			installFull: true,
			wantCapable: false,
		},
		{
			name:            "linux both cached (full + headless-shell) -> video-capable",
			goos:            "linux",
			installFull:     true,
			installHeadless: true,
			wantCapable:     true,
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
			if !got.Capable && got.Reason == "" {
				t.Fatalf("expected a non-empty operator-facing Reason when not capable (O-3)")
			}
			if got.Capable && got.Reason != "" {
				t.Fatalf("Reason must be empty when Capable=true, got %q", got.Reason)
			}
		})
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
	if got.Reason == "" {
		t.Fatalf("expected a non-empty operator-facing Reason (O-3)")
	}
}

// TestCaptureVideoCapability_RequiresExtensionSeeded proves ADR-048
// condition 3: even when the BASE classification (linux + full-Chrome
// installed) is Capable=true, CaptureVideoCapability must degrade to
// not-capable when the capture extension was never seeded (ExtensionDir
// unset on the manager's config) — the capture path is certain to fail
// without it (defaultEncoderStarter's "no ExtensionDir configured" error).
func TestCaptureVideoCapability_RequiresExtensionSeeded(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	withCapabilitySeams(t, "linux")

	platform, err := cftPlatform()
	if err != nil {
		t.Fatalf("cftPlatform: %v", err)
	}
	// ExtensionDir deliberately left empty on the manager's config.
	mgr := newCapabilityTestManager(t)
	seedBuildBinary(t, mgr.InstallRoot(), "131.0.6778.108", platform, fullChromeBuild())

	got := mgr.CaptureVideoCapability()
	if got.Capable {
		t.Fatalf("CaptureVideoCapability = Capable=true with no ExtensionDir seeded, want not-capable")
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason")
	}
}

// TestCaptureVideoCapability_RequiresSharedContextEnabled proves the other
// half of ADR-048 condition 3: a fully-capable base classification + a
// seeded extension still must NOT report Capable=true when the coordinator's
// shared-default-context capture mode is disabled — capture is certain to
// fail (chrome.tabCapture cannot reach a tab in a CDP-created context).
func TestCaptureVideoCapability_RequiresSharedContextEnabled(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	withCapabilitySeams(t, "linux")

	platform, err := cftPlatform()
	if err != nil {
		t.Fatalf("cftPlatform: %v", err)
	}
	mgr := newCapabilityTestManager(t)
	mgr.cfg.ExtensionDir = t.TempDir()
	seedBuildBinary(t, mgr.InstallRoot(), "131.0.6778.108", platform, fullChromeBuild())

	coord := NewBrowserCoordinator(t.TempDir(), BrowserConfig{}, 30)
	// captureSharedContext left at its zero-value (false/disabled) — no
	// SetCaptureSharedContext(true) call.
	mgr.AttachSharedChrome(coord, "agent-cap-test")

	got := mgr.CaptureVideoCapability()
	if got.Capable {
		t.Fatalf("CaptureVideoCapability = Capable=true with shared-context capture disabled, want not-capable")
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason")
	}

	coord.SetCaptureSharedContext(true)
	got = mgr.CaptureVideoCapability()
	if !got.Capable {
		t.Fatalf("CaptureVideoCapability = Capable=false after enabling shared-context capture, want true (reason=%q)", got.Reason)
	}
}
