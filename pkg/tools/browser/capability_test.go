package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
// It also PINS darwinAudioVerified to false. Every caller of this helper
// asserts the per-OS gate itself — "an OS that has not had its own audio
// verification is not capable" — which is a statement about the GATE, not
// about whichever value the production default happens to hold. Leaving it
// unpinned made these tests silently depend on that default: when the darwin
// AC-1 spike was verified on 2026-08-12 and the default flipped to true, five
// of them failed, having never intended to assert anything about darwin's
// verification status in the first place.
//
// Pinning keeps them testing the gate. The verified-darwin case is covered
// separately and explicitly by capability_phase3_test.go's withDarwinAudioSeam.
func withCapabilitySeams(t *testing.T, goos string) {
	t.Helper()
	prev := goosForCapability
	goosForCapability = goos
	t.Cleanup(func() { goosForCapability = prev })
	prevAudio := darwinAudioVerified
	darwinAudioVerified = false
	t.Cleanup(func() { darwinAudioVerified = prevAudio })
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

// TestClassifyVideoCapabilityWithExec_Table exercises
// ClassifyVideoCapabilityWithExec directly (QA regression gap: fix-wave A's
// capability_test.go rewrite covered ClassifyVideoCapability's install-root
// table and CaptureVideoCapability's ADR-048 gates, but no test ever called
// ClassifyVideoCapabilityWithExec itself — the exec_path override branch
// installer.go/manager.go's VideoCapability() actually delegates to for every
// real gateway call — leaving it with zero DIRECT unit coverage). Covers: the
// empty-execPath delegation to ClassifyVideoCapability, a real full-Chrome
// exec_path on linux (capable), the two documented headless-shell basename
// spellings (not capable — no tabCapture surface), and the non-linux
// early-out via the goosForCapability seam (not capable regardless of the
// exec_path basename). The gateway-level "exec_path set + empty install root
// still proceeds past the capability gate" ladder assertion is already
// covered by TestHandleWebRTCOffer_StartFailure_ClearsStickySessionAndAuditsDistinctEvent
// (pkg/gateway/browser_webrtc_fixwave_test.go) — deliberately not duplicated
// here.
func TestClassifyVideoCapabilityWithExec_Table(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Fatalf("cftPlatform: %v", err)
	}

	t.Run("empty execPath delegates to ClassifyVideoCapability", func(t *testing.T) {
		withCapabilitySeams(t, "linux")
		root := t.TempDir()
		seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())

		withExec := ClassifyVideoCapabilityWithExec("", root)
		base := ClassifyVideoCapability(root)
		if withExec != base {
			t.Fatalf(
				"ClassifyVideoCapabilityWithExec(\"\", root) = %+v, want it to equal ClassifyVideoCapability(root) = %+v (delegation)",
				withExec,
				base,
			)
		}
		if !withExec.Capable {
			t.Fatalf("expected Capable=true with a full-Chrome build seeded, got %+v", withExec)
		}

		// And the delegation must carry through to the not-capable case too
		// (empty install root, nothing seeded) — not just the happy path.
		emptyRoot := t.TempDir()
		withExecEmpty := ClassifyVideoCapabilityWithExec("", emptyRoot)
		baseEmpty := ClassifyVideoCapability(emptyRoot)
		if withExecEmpty != baseEmpty {
			t.Fatalf(
				"ClassifyVideoCapabilityWithExec(\"\", emptyRoot) = %+v, want it to equal ClassifyVideoCapability(emptyRoot) = %+v",
				withExecEmpty,
				baseEmpty,
			)
		}
		if withExecEmpty.Capable {
			t.Fatal("expected not-capable with an empty install root and no exec_path override")
		}
	})

	t.Run("linux full-chrome exec_path -> capable", func(t *testing.T) {
		withCapabilitySeams(t, "linux")
		execPath := filepath.Join(t.TempDir(), "chrome-linux64", "chrome")
		if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(execPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake chrome: %v", err)
		}

		got := ClassifyVideoCapabilityWithExec(execPath, t.TempDir())
		if !got.Capable {
			t.Fatalf("expected Capable=true for a linux exec_path not naming headless-shell, got %+v", got)
		}
		if got.Reason != "" {
			t.Fatalf("Reason must be empty when Capable=true, got %q", got.Reason)
		}
	})

	t.Run("chrome-headless-shell basename -> not capable", func(t *testing.T) {
		withCapabilitySeams(t, "linux")
		execPath := filepath.Join(t.TempDir(), "chrome-headless-shell-linux64", "chrome-headless-shell")

		got := ClassifyVideoCapabilityWithExec(execPath, t.TempDir())
		if got.Capable {
			t.Fatalf("expected not-capable for a chrome-headless-shell exec_path (no tabCapture surface), got %+v", got)
		}
		if got.Reason == "" {
			t.Fatal("expected a non-empty operator-facing Reason (O-3)")
		}
	})

	t.Run("chrome_headless_shell underscore variant basename -> not capable", func(t *testing.T) {
		withCapabilitySeams(t, "linux")
		execPath := filepath.Join(t.TempDir(), "bin", "chrome_headless_shell")

		got := ClassifyVideoCapabilityWithExec(execPath, t.TempDir())
		if got.Capable {
			t.Fatalf("expected not-capable for a chrome_headless_shell (underscore) exec_path, got %+v", got)
		}
		if got.Reason == "" {
			t.Fatal("expected a non-empty operator-facing Reason (O-3)")
		}
	})

	t.Run("non-linux via goosForCapability seam -> not capable regardless of exec_path", func(t *testing.T) {
		for _, goos := range []string{"darwin", "windows", "android"} {
			t.Run(goos, func(t *testing.T) {
				withCapabilitySeams(t, goos)
				execPath := filepath.Join(t.TempDir(), "chrome") // a plausible FULL-chrome basename
				got := ClassifyVideoCapabilityWithExec(execPath, t.TempDir())
				if got.Capable {
					t.Fatalf("expected not-capable on GOOS=%s regardless of exec_path basename, got %+v", goos, got)
				}
				if got.Reason == "" {
					t.Fatal("expected a non-empty operator-facing Reason (O-3)")
				}
			})
		}
	})
}

// TestCaptureVideoCapability_RequiresSharedContextEnabled proves the other
// half of ADR-048 condition 3: a fully-capable base classification + a
// seeded extension still must NOT report Capable=true when the coordinator's
// shared-default-context capture mode is disabled — capture is certain to
// fail (chrome.tabCapture cannot reach a tab in a CDP-created context).
func TestCaptureVideoCapability_RequiresCoordinatorAttached(t *testing.T) {
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

	// No AttachSharedChrome yet: this manager drives no Chrome that the
	// encoder page could share, so capture cannot succeed and the classifier
	// must say so rather than advertising Capable=true.
	got := mgr.CaptureVideoCapability()
	if got.Capable {
		t.Fatalf("CaptureVideoCapability = Capable=true with no coordinator attached, want not-capable")
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason")
	}

	mgr.AttachSharedChrome(
		NewBrowserCoordinator(t.TempDir(), BrowserConfig{}),
		browserTestKey("agent-cap-test"),
	)
	got = mgr.CaptureVideoCapability()
	if !got.Capable {
		t.Fatalf(
			"CaptureVideoCapability = Capable=false once a coordinator is attached, want true (reason=%q)",
			got.Reason,
		)
	}
}

// TestCaptureVideoCapability_PoolAttachedCountsAsAttached is the regression
// guard for the ADR-072 FR-037 pool cutover.
//
// AttachSharedChrome pins a coordinator at attach time; AttachPool — which is
// production's ONLY path (pkg/agent/loop.go's browserFactory) — deliberately
// does not, because which Chrome the manager drives is resolved on every
// ensureStarted. A capability check that read m.coordinator directly therefore
// stopped asking "is this manager attached to the workspace's Chrome" and
// started asking "has that Chrome been launched yet", so every pool-attached
// manager classified not_capable until something else happened to start it —
// which is what silently disabled the boot-time capture warm-up for an
// operator running warm_capture_at_boot with warm_tab_at_boot off, and what
// made eleven pkg/gateway WebRTC tests report "not_capable" where they expect
// the real "error" outcome.
//
// The load-bearing assertion is the Coordinator()==nil check: it proves the
// Capable=true verdict came from the ATTACHMENT and not from a Chrome having
// been launched behind the test's back.
func TestCaptureVideoCapability_PoolAttachedCountsAsAttached(t *testing.T) {
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

	if got := mgr.CaptureVideoCapability(); got.Capable {
		t.Fatal("test setup: an unattached manager must classify not-capable")
	}

	pool := NewBrowserPool(t.TempDir(), BrowserConfig{})
	t.Cleanup(pool.Shutdown)
	mgr.AttachPool(pool, browserTestKey("agent-pool-cap-test"))

	if mgr.Coordinator() != nil {
		t.Fatal("AttachPool must NOT pin a coordinator — the whole point is that the pool resolves one per ensureStarted")
	}
	got := mgr.CaptureVideoCapability()
	if !got.Capable {
		t.Fatalf(
			"CaptureVideoCapability = Capable=false for a pool-attached manager, want true (reason=%q)",
			got.Reason,
		)
	}
}

// TestVideoCapability_CachedExecPathFallback proves the fix for the
// download-vs-launch mismatch (BrowserManager.VideoCapability, manager.go):
// exec_resolver.go's resolve() checks $PATH for a system google-chrome/
// chromium BEFORE falling back to the managed Chrome-for-Testing download,
// so on a host with a system Chrome on $PATH the managed install root
// ClassifyVideoCapability inspects is NEVER populated. Without reading
// execPathCaches' already-resolved path, VideoCapability would permanently
// misclassify a perfectly video-capable host as not_capable. Table-driven,
// mirroring TestClassifyVideoCapabilityWithExec_Table's style.
//
// Regression proof: the first scenario below ("resolved system full Chrome
// on $PATH") FAILS against the pre-fix VideoCapability (which reads only
// cfg.ExecPath and ignores the execPathCaches success cache entirely) and
// PASSES once VideoCapability falls back to m.execPath.cachedPath().
func TestVideoCapability_CachedExecPathFallback(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	managedFullChrome := filepath.Join(string(filepath.Separator), "opt", "omnipus-chromium",
		"131.0.6778.108", "chrome-linux64", "chrome")
	managedHeadlessShell := filepath.Join(string(filepath.Separator), "opt", "omnipus-chromium",
		"131.0.6778.108", "chrome-headless-shell-linux64", "chrome-headless-shell")

	type scenario struct {
		name            string
		goos            string
		cfgExecPath     string
		cachedExecPath  string
		wantCapable     bool
		wantReasonMatch string // substring required in Reason when !wantCapable; "" to skip the check
	}

	scenarios := []scenario{
		{
			name:           "resolved system full Chrome on $PATH -> capable",
			goos:           "linux",
			cachedExecPath: "/usr/bin/google-chrome",
			wantCapable:    true,
		},
		{
			name:           "resolved managed full Chrome build -> capable",
			goos:           "linux",
			cachedExecPath: managedFullChrome,
			wantCapable:    true,
		},
		{
			name:            "resolved managed headless-shell build -> not capable",
			goos:            "linux",
			cachedExecPath:  managedHeadlessShell,
			wantCapable:     false,
			wantReasonMatch: "headless-shell",
		},
		{
			name:        "no cached path and empty install root -> not capable (unchanged regression guard)",
			goos:        "linux",
			wantCapable: false,
		},
		{
			name:            "explicit cfg.ExecPath still takes priority over the cached path",
			goos:            "linux",
			cfgExecPath:     managedHeadlessShell,
			cachedExecPath:  "/usr/bin/google-chrome",
			wantCapable:     false,
			wantReasonMatch: "headless-shell",
		},
		{
			name:           "non-linux via goosForCapability seam -> not capable regardless of cached path",
			goos:           "darwin",
			cachedExecPath: "/usr/bin/google-chrome",
			wantCapable:    false,
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			withCapabilitySeams(t, sc.goos)
			mgr := newCapabilityTestManager(t)
			mgr.cfg.ExecPath = sc.cfgExecPath
			mgr.execPath.success = sc.cachedExecPath

			got := mgr.VideoCapability()
			if got.Capable != sc.wantCapable {
				t.Fatalf("Capable = %v, want %v (reason: %q)", got.Capable, sc.wantCapable, got.Reason)
			}
			if !got.Capable {
				if got.Reason == "" {
					t.Fatalf("expected a non-empty operator-facing Reason when not capable (O-3)")
				}
				if sc.wantReasonMatch != "" && !strings.Contains(got.Reason, sc.wantReasonMatch) {
					t.Fatalf("Reason = %q, want it to contain %q", got.Reason, sc.wantReasonMatch)
				}
			} else if got.Reason != "" {
				t.Fatalf("Reason must be empty when Capable=true, got %q", got.Reason)
			}
		})
	}
}
