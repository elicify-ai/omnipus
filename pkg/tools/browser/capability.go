package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// VideoCapability is the FR-007/DS-5 classification of whether this host can
// run the live-view video capture stack. Anything other than Capable==true
// MUST make the live-view panel show the generic unavailable state (FR-007);
// Reason is the specific, operator-only cause and MUST NOT be surfaced to the
// end user (O-3).
type VideoCapability struct {
	// Capable is the overall DS-5 classification: linux + a full-Chrome
	// build already installed + Xvfb available. Agent browsing is unaffected
	// either way — only the live-view video path is gated on this.
	Capable bool
	// AudioAvailable reports whether PulseAudio is additionally available.
	// Independent of Capable: DS-5's "full chrome + Xvfb up + PulseAudio
	// absent" row is STILL video-capable (streams; audio silently absent —
	// US-4/AC-2, US-11/AC-2).
	AudioAvailable bool
	// Reason is the specific, operator-only cause when Capable is false
	// (O-3); also set (non-fatally) when Capable is true but audio is not,
	// to explain the audio gap. Empty only when both Capable and
	// AudioAvailable are true.
	Reason string
}

// XvfbAvailableProbe and PulseAudioAvailableProbe are the FR-021/FR-022
// sidecar-availability seams ClassifyVideoCapability and selectDownloadBuild
// consult. The defaults are light, side-effect-free checks (binary on PATH)
// that correctly report "not available" before any sidecar has been wired
// into the boot sequence. The Xvfb and PulseAudio sidecar packages (built
// independently — see the live-browser-video-streaming implementation plan's
// W1-B/W1-C) MAY replace these at gateway-boot init time with a
// liveness-aware Healthy() hook once orchestration wires them in. Tests
// override these directly — no live Xvfb/PulseAudio process is required to
// exercise the classifier or the installer's build-selection logic.
var (
	XvfbAvailableProbe       = defaultXvfbAvailable
	PulseAudioAvailableProbe = defaultPulseAudioAvailable
)

func defaultXvfbAvailable() bool {
	_, err := exec.LookPath("Xvfb")
	return err == nil
}

func defaultPulseAudioAvailable() bool {
	_, err := exec.LookPath("pulseaudio")
	return err == nil
}

// goosForCapability is runtime.GOOS by default; overridable in tests so the
// Platform Matrix's non-Linux rows (DS-5: macOS/Windows -> not capable) are
// exercisable on any CI host without actually cross-running on that OS. Only
// ClassifyVideoCapability's platform branch honors this seam — EnsureChromium
// and selectDownloadBuild deliberately keep using the real runtime.GOOS
// because they determine which real binary layout to fetch/extract for THIS
// process, which must never be faked.
var goosForCapability = runtime.GOOS

// ClassifyVideoCapability implements the DS-5 decision table (Platform
// Matrix, FR-007): linux + a full-Chrome build already installed under
// installRoot + Xvfb available -> video-capable; PulseAudio availability is
// checked independently and only affects AudioAvailable, never Capable.
// installRoot is the same managed-Chromium install root EnsureChromium uses,
// so classification never triggers a download — it only inspects what is
// already on disk.
func ClassifyVideoCapability(installRoot string) VideoCapability {
	if goosForCapability != "linux" {
		return VideoCapability{
			Reason: fmt.Sprintf(
				"video capture requires linux (Xvfb/PulseAudio are linux-only); this host is %s",
				goosForCapability,
			),
		}
	}
	platform, err := cftPlatform()
	if err != nil {
		return VideoCapability{Reason: "unsupported platform for managed chromium: " + err.Error()}
	}
	if findInstalledBuild(installRoot, platform, fullChromeBuild()) == "" {
		return VideoCapability{
			Reason: "full Chrome build not installed (chrome-headless-shell only, or nothing installed yet)",
		}
	}
	if !XvfbAvailableProbe() {
		return VideoCapability{Reason: "Xvfb not available"}
	}
	audio := PulseAudioAvailableProbe()
	reason := ""
	if !audio {
		reason = "PulseAudio not available — video-capable, audio silently absent"
	}
	return VideoCapability{Capable: true, AudioAvailable: audio, Reason: reason}
}
