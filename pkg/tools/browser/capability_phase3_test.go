package browser

// capability_phase3_test.go — coverage for ADR-052 Phase 3's per-OS gate
// relaxation seam (AC-4 vs AC-5). The linux-only gate becomes a
// videoCapableOS gate: linux stays capable unconditionally, darwin becomes
// capable ONLY when the AC-1 audio spike (darwinAudioVerified) proves
// --headless chrome.tabCapture audio. Until that spike flips the seam,
// darwin stays not-capable (M3/M6 — the classifier must not advertise
// Capable when capture cannot succeed, ADR-048 condition 3). Windows and
// every other OS are unaffected (Phase 4).
//
// These tests run on the Linux devpod: they exercise the GATE LOGIC via
// the goosForCapability + darwinAudioVerified seams, not a real darwin
// runtime. The darwin disk layout (.app bundle) is verified separately by
// the GOOS=darwin cross-vet and the future macOS spike run (AC-1).

import (
	"path/filepath"
	"testing"
)

// withDarwinAudioSeam overrides darwinAudioVerified for the duration of the
// test, restoring the previous value on cleanup. Mirrors withCapabilitySeams
// so the Phase-3 AC-4 flip point is exercisable without running on darwin.
// The default (false) is the AC-5 posture: darwin stays not-capable until
// the AC-1 spike proves audio.
func withDarwinAudioSeam(t *testing.T, verified bool) {
	t.Helper()
	prev := darwinAudioVerified
	darwinAudioVerified = verified
	t.Cleanup(func() { darwinAudioVerified = prev })
}

// TestClassifyVideoCapability_DarwinAudioUnverified_NotCapable proves the
// safe default (AC-5 posture): with darwinAudioVerified=false (the default,
// i.e. the spike has NOT proven audio yet), darwin classifies not-capable
// EVEN WHEN a valid, SHA-verified package Chrome is present. This is the
// M3/M6 invariant — the gate does not relax until per-OS audio verification.
func TestClassifyVideoCapability_DarwinAudioUnverified_NotCapable(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	// Explicitly the default; set for clarity so the test is self-documenting
	// and resilient to any future default change.
	withCapabilitySeams(t, "darwin")
	withDarwinAudioSeam(t, false)

	pkgRoot := t.TempDir()
	seedPackageChromeAtRoot(t, pkgRoot, true) // valid package Chrome + matching chrome.sha256
	withPackageChromeRoot(t, pkgRoot)

	got := ClassifyVideoCapability(t.TempDir()) // empty installRoot
	if got.Capable {
		t.Fatalf(
			"SEC-ADR048-cond3 / AC-5: darwin with darwinAudioVerified=false must classify not-capable even with a valid package Chrome, got Capable=true (reason=%q)",
			got.Reason,
		)
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason (O-3) on not-capable")
	}
}

// TestClassifyVideoCapability_DarwinAudioVerified_Capable proves the AC-4
// flip works: once the Phase-3 spike flips darwinAudioVerified=true (seam
// set here, flipped in production by the spike result), darwin + a valid
// package Chrome classifies Capable. This is the one-place gate change
// (videoCapableOS) the spike's AUDIO-WORKS outcome activates.
func TestClassifyVideoCapability_DarwinAudioVerified_Capable(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	withCapabilitySeams(t, "darwin")
	withDarwinAudioSeam(t, true)

	pkgRoot := t.TempDir()
	seedPackageChromeAtRoot(t, pkgRoot, true) // valid package Chrome + matching chrome.sha256
	withPackageChromeRoot(t, pkgRoot)

	got := ClassifyVideoCapability(t.TempDir()) // empty installRoot — package Chrome is the only payload
	if !got.Capable {
		t.Fatalf(
			"AC-4: darwin with darwinAudioVerified=true and a valid package Chrome must classify Capable, got not-capable (reason=%q)",
			got.Reason,
		)
	}
	if got.Reason != "" {
		t.Fatalf("Reason must be empty when Capable=true, got %q", got.Reason)
	}
}

// TestClassifyVideoCapabilityWithExec_DarwinAudioUnverified_NotCapable
// mirrors the AC-5 default for the WithExec variant: with
// darwinAudioVerified=false, a darwin exec_path is not-capable regardless
// of its basename — the per-OS gate precedes the headless-shell basename
// check.
func TestClassifyVideoCapabilityWithExec_DarwinAudioUnverified_NotCapable(t *testing.T) {
	withCapabilitySeams(t, "darwin")
	withDarwinAudioSeam(t, false)

	// A plausible FULL-chrome basename (not headless-shell) — only the
	// GOOS+audio seam can block capability here.
	execPath := filepath.Join(t.TempDir(), "chrome")
	got := ClassifyVideoCapabilityWithExec(execPath, t.TempDir())
	if got.Capable {
		t.Fatalf(
			"AC-5: darwin with darwinAudioVerified=false must classify not-capable regardless of exec_path basename, got Capable=true",
		)
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason (O-3) on not-capable")
	}
}

// TestClassifyVideoCapabilityWithExec_DarwinAudioVerified_Capable mirrors
// the AC-4 flip for the WithExec variant: with darwinAudioVerified=true,
// darwin + a non-headless-shell exec_path classifies Capable (the basename
// heuristic still admits it).
func TestClassifyVideoCapabilityWithExec_DarwinAudioVerified_Capable(t *testing.T) {
	withCapabilitySeams(t, "darwin")
	withDarwinAudioSeam(t, true)

	execPath := filepath.Join(t.TempDir(), "chrome") // non-headless-shell basename
	got := ClassifyVideoCapabilityWithExec(execPath, t.TempDir())
	if !got.Capable {
		t.Fatalf(
			"AC-4: darwin with darwinAudioVerified=true and a non-headless-shell exec_path must classify Capable, got not-capable (reason=%q)",
			got.Reason,
		)
	}
	if got.Reason != "" {
		t.Fatalf("Reason must be empty when Capable=true, got %q", got.Reason)
	}
}

// TestClassifyVideoCapabilityWithExec_DarwinAudioVerified_HeadlessShellStillBlocked
// proves the AC-4 flip does NOT override the headless-shell guard: even
// with darwinAudioVerified=true, a headless-shell exec_path stays
// not-capable (chrome-headless-shell lacks the tabCapture surface
// entirely). The audio spike relaxes the per-OS gate, not the
// capture-surface requirement.
func TestClassifyVideoCapabilityWithExec_DarwinAudioVerified_HeadlessShellStillBlocked(t *testing.T) {
	withCapabilitySeams(t, "darwin")
	withDarwinAudioSeam(t, true)

	execPath := filepath.Join(t.TempDir(), "chrome-headless-shell")
	got := ClassifyVideoCapabilityWithExec(execPath, t.TempDir())
	if got.Capable {
		t.Fatalf(
			"headless-shell exec_path must stay not-capable even with darwinAudioVerified=true (no tabCapture surface), got Capable=true",
		)
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason (O-3) on not-capable")
	}
}

// TestClassifyVideoCapability_LinuxUnaffectedByDarwinAudioFlag is the
// regression guard: the darwinAudioVerified seam MUST NOT change linux
// behavior. Even with darwinAudioVerified=true (the spike succeeded), a
// linux host with a full-Chrome build installed still classifies Capable
// exactly as before — videoCapableOS short-circuits on linux. This proves
// HC: no Linux runtime change.
func TestClassifyVideoCapability_LinuxUnaffectedByDarwinAudioFlag(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Fatalf("cftPlatform: %v", err)
	}

	// Spike flag TRUE must not leak into the linux classification path.
	withDarwinAudioSeam(t, true)
	withCapabilitySeams(t, "linux")

	root := t.TempDir()
	seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())

	got := ClassifyVideoCapability(root)
	if !got.Capable {
		t.Fatalf(
			"regression: linux + full-Chrome installed must stay Capable regardless of darwinAudioVerified, got not-capable (reason=%q)",
			got.Reason,
		)
	}
	if got.Reason != "" {
		t.Fatalf("Reason must be empty when Capable=true, got %q", got.Reason)
	}
}

// TestVideoCapableOS_Table is a focused unit test on the AC-4/AC-5 flip
// helper itself, independent of the disk/state the classifier consults
// downstream. Pins the exact contract: linux always capable; darwin capable
// iff darwinAudioVerified; everything else never capable.
func TestVideoCapableOS_Table(t *testing.T) {
	for _, tc := range []struct {
		goos       string
		audioVerif bool
		want       bool
	}{
		{"linux", false, true},
		{"linux", true, true},
		{"darwin", false, false},
		{"darwin", true, true},
		{"windows", false, false},
		{"windows", true, false},
		{"android", false, false},
		{"", false, false},
	} {
		t.Run(tc.goos+"_audio="+boolStr(tc.audioVerif), func(t *testing.T) {
			withDarwinAudioSeam(t, tc.audioVerif)
			got := videoCapableOS(tc.goos)
			if got != tc.want {
				t.Fatalf("videoCapableOS(%q, audioVerified=%v) = %v, want %v", tc.goos, tc.audioVerif, got, tc.want)
			}
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
