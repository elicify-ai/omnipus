package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// CapabilityLevel is the exhaustive DS-5 classification enum (TD5). It
// collapses VideoCapability's Capable/AudioAvailable flag pair into three
// legal states, so the illegal fourth combination — {Capable:false,
// AudioAvailable:true} — is simply not representable via the constructors
// ClassifyVideoCapability uses (notCapable / videoOnly / videoAndAudio
// below), even though the back-compat Capable/AudioAvailable fields on
// VideoCapability remain independently settable by any caller that builds a
// VideoCapability struct literal directly (see VideoCapability's doc).
type CapabilityLevel int

const (
	// NotCapableLevel: live-view video is not available on this host at all.
	NotCapableLevel CapabilityLevel = iota
	// VideoOnlyLevel: video capture works but PulseAudio is not available —
	// DS-5's "full chrome + Xvfb up + PulseAudio absent" row (US-4/AC-2,
	// US-11/AC-2): still video-capable, audio silently absent.
	VideoOnlyLevel
	// VideoAndAudioLevel: both video and audio capture are available.
	VideoAndAudioLevel
)

// String returns a short, stable, machine-and-log-friendly name for l.
func (l CapabilityLevel) String() string {
	switch l {
	case VideoAndAudioLevel:
		return "video_and_audio"
	case VideoOnlyLevel:
		return "video_only"
	default:
		return "not_capable"
	}
}

// VideoCapability is the FR-007/DS-5 classification of whether this host can
// run the live-view video capture stack. Anything other than Capable==true
// MUST make the live-view panel show the generic unavailable state (FR-007);
// Reason is the specific, operator-only cause and MUST NOT be surfaced to the
// end user (O-3).
//
// Level is the exhaustive, non-overloaded classification (TD5); Capable and
// AudioAvailable are derived booleans kept for source compatibility with
// existing callers (e.g. pkg/gateway/browser_stream.go) that already read
// them directly. ClassifyVideoCapability always derives Capable/AudioAvailable
// from Level, so the two never disagree when built by this package.
type VideoCapability struct {
	// Level is the DS-5 classification enum. Prefer this over
	// Capable/AudioAvailable in new code — it cannot represent the illegal
	// {not capable, audio available} combination.
	Level CapabilityLevel
	// Capable is the overall DS-5 classification: linux + a full-Chrome
	// build already installed + a healthy virtual-display sidecar wired (see
	// DisplaySidecarHealthyProbe). Agent browsing is unaffected either way —
	// only the live-view video path is gated on this. Derived from Level !=
	// NotCapableLevel.
	Capable bool
	// AudioAvailable reports whether PulseAudio is additionally available.
	// Independent of Capable: DS-5's "full chrome + Xvfb up + PulseAudio
	// absent" row is STILL video-capable (streams; audio silently absent —
	// US-4/AC-2, US-11/AC-2). Derived from Level == VideoAndAudioLevel.
	AudioAvailable bool
	// Reason is the specific, operator-only cause when Capable is false
	// (O-3). Empty whenever Capable is true — it no longer doubles as the
	// audio-absent explanation (TD5); see AudioReason for that.
	Reason string
	// AudioReason explains why AudioAvailable is false when Capable is true
	// (i.e. Level == VideoOnlyLevel). Empty whenever AudioAvailable is true
	// or Capable is false.
	AudioReason string
}

// DisplaySidecarHealthyProbe is the AR-C1/GC-1/GC-2 Option-B dormant gate:
// ClassifyVideoCapability and selectDownloadBuild (installer.go) both consult
// THIS signal — never an Xvfb-binary-on-PATH check — to decide whether
// headful, video-capable Chrome is actually reachable on this host right now.
//
// It defaults to defaultDisplaySidecarHealthy, which always reports false.
// This increment ships the Xvfb/PulseAudio sidecar packages (display.go,
// audiosink*.go) but wires NEITHER into any boot sequence —
// BrowserCoordinator.SetDisplaySidecar has zero callers today — so
// classification and the installer's build-selection MUST stay dormant
// (NotCapable / chrome-headless-shell) regardless of what happens to be
// installed on the host's PATH. Gating on an Xvfb-binary-on-PATH probe
// instead of the coordinator's real launch-time signal was exactly the
// AR-C1/GC-1/GC-2 defect: a host with the xvfb/pulseaudio OS packages
// present would download +120MB of full Chrome and get classified
// "video-capable" while coordinator.videoLaunchMode kept launching
// chrome-headless-shell (gated on a wired-and-Healthy() DisplaySidecar,
// which nothing sets) — headless-grade video, no audio, all silent.
//
// THE SEAM FOR THE NEXT INCREMENT: once gateway boot actually starts an Xvfb
// sidecar and wires it via BrowserCoordinator.SetDisplaySidecar, that same
// change should point DisplaySidecarHealthyProbe at the live sidecar's own
// Healthy() (e.g. boot stashes the sidecar reference somewhere this package
// can read, or a coordinator-owned accessor) so classification and the
// installer agree with the coordinator's actual videoLaunchMode decision
// instead of drifting from it again. Until that day this MUST stay a
// hardcoded false — never swap it back for a PATH probe or any other proxy
// signal; there is no live sidecar behind it yet. Tests override it directly.
var DisplaySidecarHealthyProbe = defaultDisplaySidecarHealthy

func defaultDisplaySidecarHealthy() bool {
	return false
}

// PulseAudioAvailableProbe is the FR-022 audio-sidecar-availability seam
// ClassifyVideoCapability consults for the AudioAvailable sub-classification
// ONLY (never Capable/selectDownloadBuild — see DisplaySidecarHealthyProbe
// for that gate). It is reached only once DisplaySidecarHealthyProbe already
// reports true, which does not happen this increment, so this probe's
// PATH-based default is harmless dead weight until the sidecar wiring lands.
// The default is a light, side-effect-free check (binary on PATH); the
// PulseAudio sidecar package (audiosink*.go, built independently — see the
// live-browser-video-streaming implementation plan's W1-C) MAY replace it at
// gateway-boot init time with a liveness-aware Healthy() hook once
// orchestration wires it in. Tests override this directly.
var PulseAudioAvailableProbe = defaultPulseAudioAvailable

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
// installRoot + a healthy virtual-display sidecar wired (see
// DisplaySidecarHealthyProbe — NOT an Xvfb-binary-on-PATH check, AR-C1/GC-1/
// GC-2) -> video-capable; PulseAudio availability is checked independently
// and only affects AudioAvailable, never Capable. installRoot is the same
// managed-Chromium install root EnsureChromium uses, so classification never
// triggers a download — it only inspects what is already on disk.
func ClassifyVideoCapability(installRoot string) VideoCapability {
	if goosForCapability != "linux" {
		return notCapable(fmt.Sprintf(
			"video capture requires linux (Xvfb/PulseAudio are linux-only); this host is %s",
			goosForCapability,
		))
	}
	platform, err := cftPlatform()
	if err != nil {
		return notCapable("unsupported platform for managed chromium: " + err.Error())
	}
	if findInstalledBuild(installRoot, platform, fullChromeBuild()) == "" {
		return notCapable("full Chrome build not installed (chrome-headless-shell only, or nothing installed yet)")
	}
	if !DisplaySidecarHealthyProbe() {
		return notCapable(
			"virtual-display sidecar not wired or not healthy — video capture requires a running Xvfb sidecar",
		)
	}
	if !PulseAudioAvailableProbe() {
		return videoOnly("PulseAudio not available — video-capable, audio silently absent")
	}
	return videoAndAudio()
}

// notCapable, videoOnly, and videoAndAudio are VideoCapability's only
// constructors within this package (TD5): they derive Capable/AudioAvailable
// from Level so the three cannot disagree, and keep Reason scoped to the
// not-capable cause while AudioReason carries the video-only audio-absent
// explanation — de-overloading what a single Reason field used to carry.
func notCapable(reason string) VideoCapability {
	return VideoCapability{Level: NotCapableLevel, Capable: false, AudioAvailable: false, Reason: reason}
}

func videoOnly(audioReason string) VideoCapability {
	return VideoCapability{Level: VideoOnlyLevel, Capable: true, AudioAvailable: false, AudioReason: audioReason}
}

func videoAndAudio() VideoCapability {
	return VideoCapability{Level: VideoAndAudioLevel, Capable: true, AudioAvailable: true}
}
