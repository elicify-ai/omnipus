package browser

import (
	"fmt"
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
	// VideoOnlyLevel: video capture works but audio does not. This is the
	// steady-state classification for every video-capable host in phase 1
	// (Option A / ADR-044) — audio capture is deferred to phase 2, so
	// ClassifyVideoCapability never returns VideoAndAudioLevel yet.
	VideoOnlyLevel
	// VideoAndAudioLevel: both video and audio capture are available. Not
	// reachable until phase 2 wires audio support back in.
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
	// Capable is the overall classification (Option A / ADR-044): linux + a
	// full-Chrome build already installed under installRoot for the
	// dedicated live-view encoder browser. Agent browsing is unaffected
	// either way — the agent's own browser is always chrome-headless-shell;
	// only the live-view video path (a SEPARATE, dedicated encoder browser
	// process) is gated on this. Derived from Level != NotCapableLevel.
	Capable bool
	// AudioAvailable reports whether audio capture is additionally
	// available. Always false in phase 1 — audio capture is deferred to
	// phase 2 (ADR-044), so every Capable host currently classifies as
	// VideoOnlyLevel. Derived from Level == VideoAndAudioLevel.
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

// goosForCapability is runtime.GOOS by default; overridable in tests so the
// Platform Matrix's non-Linux rows (DS-5: macOS/Windows -> not capable) are
// exercisable on any CI host without actually cross-running on that OS. Only
// ClassifyVideoCapability's platform branch honors this seam — EnsureChromium
// and selectDownloadBuild deliberately keep using the real runtime.GOOS
// because they determine which real binary layout to fetch/extract for THIS
// process, which must never be faked.
var goosForCapability = runtime.GOOS

// ClassifyVideoCapability implements the Option-A (ADR-044) video-capability
// decision: video-capable now means linux + a full-Chrome build already
// installed under installRoot for the dedicated live-view encoder browser —
// the agent's own browser stays chrome-headless-shell regardless of this
// classification (Option A dedicates a SEPARATE full-Chrome browser process
// to encoding; it never runs agent code, so no virtual-display or audio
// sidecar is required to reach it). installRoot is the same managed-Chromium
// install root EnsureChromium/EnsureChromiumFullBuild use, so classification
// never triggers a download — it only inspects what is already on disk.
// Audio capture is deferred to phase 2 (ADR-044): a video-capable host
// always classifies VideoOnlyLevel via videoOnly, never VideoAndAudioLevel,
// until phase 2 lands.
func ClassifyVideoCapability(installRoot string) VideoCapability {
	if goosForCapability != "linux" {
		return notCapable(fmt.Sprintf(
			"live-view video requires a linux full-Chrome build; this host is %s",
			goosForCapability,
		))
	}
	platform, err := cftPlatform()
	if err != nil {
		return notCapable("unsupported platform for managed chromium: " + err.Error())
	}
	if findInstalledBuild(installRoot, platform, fullChromeBuild()) == "" {
		return notCapable("full-Chrome encoder build not installed yet (download pending or unavailable)")
	}
	return videoOnly("audio deferred to phase 2")
}

// notCapable and videoOnly are VideoCapability's only constructors within this
// package (TD5): they derive Capable/AudioAvailable from Level so the two
// cannot disagree, and keep Reason scoped to the not-capable cause while
// AudioReason carries the video-only audio-absent explanation — de-overloading
// what a single Reason field used to carry. There is deliberately no
// videoAndAudio constructor yet: audio capture is a phase-2 increment (the
// dedicated encoder browser is video-only for now), so phase 1 never reaches
// VideoAndAudioLevel. It will be re-added with the audio increment.
func notCapable(reason string) VideoCapability {
	return VideoCapability{Level: NotCapableLevel, Capable: false, AudioAvailable: false, Reason: reason}
}

func videoOnly(audioReason string) VideoCapability {
	return VideoCapability{Level: VideoOnlyLevel, Capable: true, AudioAvailable: false, AudioReason: audioReason}
}
