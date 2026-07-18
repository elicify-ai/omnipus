package browser

import (
	"fmt"
	"runtime"
)

// CapabilityLevel is the exhaustive classification enum for live-view video
// capability. It collapses VideoCapability's Capable/AudioAvailable flag
// pair into three legal states, so the illegal fourth combination —
// {Capable:false, AudioAvailable:true} — is simply not representable via the
// constructors ClassifyVideoCapability uses (notCapable / videoAndAudio
// below), even though the back-compat Capable/AudioAvailable fields on
// VideoCapability remain independently settable by any caller that builds a
// VideoCapability struct literal directly (see VideoCapability's doc).
type CapabilityLevel int

const (
	// NotCapableLevel: live-view video is not available on this host at all.
	NotCapableLevel CapabilityLevel = iota
	// VideoOnlyLevel: video capture works but audio does not. Not reachable
	// via ClassifyVideoCapability today — WebRTC tabCapture (the only
	// supported capture mechanism) delivers a MediaStream with BOTH tracks
	// together whenever it works at all (WV1 spike Q2: real audio samples,
	// no PulseAudio, no sidecar, no audio device). Kept in the enum for a
	// possible future partial-capability path (e.g. a capture mode that
	// legitimately lacks audio); has no constructor in this package while
	// unreachable — see videoAndAudio's doc.
	VideoOnlyLevel
	// VideoAndAudioLevel: both video and audio capture are available. This
	// is the only Capable=true outcome ClassifyVideoCapability returns today
	// — see videoAndAudio.
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

// VideoCapability is the classification of whether this host can run the
// live-view WebRTC video+audio capture stack (tabCapture in the managed full
// Chrome build). Anything other than Capable==true MUST make the live-view
// panel show the generic unavailable state; Reason is the specific,
// operator-only cause and MUST NOT be surfaced to the end user.
//
// Level is the exhaustive, non-overloaded classification; Capable and
// AudioAvailable are derived booleans kept for source compatibility with
// callers that read them directly instead of switching on Level.
// ClassifyVideoCapability always derives Capable/AudioAvailable from Level,
// so the two never disagree when built by this package.
type VideoCapability struct {
	// Level is the exhaustive classification enum. Prefer this over
	// Capable/AudioAvailable in new code — it cannot represent the illegal
	// {not capable, audio available} combination.
	Level CapabilityLevel
	// Capable is the overall classification: linux + a full-Chrome build
	// already installed under installRoot. WebRTC tabCapture (the encoder
	// extension's own page, self-consuming the active tab) only works in
	// the full "chrome" build — chrome-headless-shell lacks the tab-capture
	// surface entirely — and only on linux (the only platform the managed
	// full-Chrome capture path is validated against). Derived from
	// Level != NotCapableLevel.
	Capable bool
	// AudioAvailable reports whether audio capture is additionally
	// available. Equal to Capable today: WebRTC tabCapture delivers a
	// MediaStream with live video AND real audio samples together, with no
	// PulseAudio, sidecar, or audio device required (WV1 spike Q2,
	// proven) — there is no separate "audio deferred" phase for this
	// capture mechanism. Derived from Level == VideoAndAudioLevel.
	AudioAvailable bool
	// Reason is the specific, operator-only cause when Capable is false.
	// Empty whenever Capable is true — it does not double as the
	// audio-absent explanation; see AudioReason for that (currently unused,
	// since AudioAvailable never disagrees with Capable — see
	// VideoOnlyLevel's doc).
	Reason string
	// AudioReason explains why AudioAvailable is false when Capable is true
	// (i.e. Level == VideoOnlyLevel). Empty whenever AudioAvailable is true
	// or Capable is false. Always empty today — see VideoOnlyLevel's doc.
	AudioReason string
}

// goosForCapability is runtime.GOOS by default; overridable in tests so the
// platform-matrix's non-Linux rows (macOS/Windows -> not capable) are
// exercisable on any CI host without actually cross-running on that OS. Only
// ClassifyVideoCapability's platform branch honors this seam — EnsureChromium
// and selectDownloadBuild deliberately keep using the real runtime.GOOS
// because they determine which real binary layout to fetch/extract for THIS
// process, which must never be faked.
var goosForCapability = runtime.GOOS

// ClassifyVideoCapability implements the WebRTC live-view video-capability
// decision (supersedes the ADR-044 WebCodecs-relay Option A2 framing this
// package originally shipped): video-capable means linux + a full-Chrome
// build already installed under installRoot. That build IS the one the
// gateway launches for the agent's browsing (installer.go's
// selectDownloadBuild on linux), and the tabCapture encoder extension runs
// as a page inside that same managed Chrome — there is no separate encoder
// process to provision, so this classification really means "is the
// video-capable Chrome build installed yet." installRoot is the same
// managed-Chromium install root EnsureChromium/EnsureChromiumFullBuild use,
// so classification never triggers a download — it only inspects what is
// already on disk.
//
// Unlike the superseded WebCodecs-relay design (which deferred audio to a
// later phase), WebRTC tabCapture delivers video AND audio together in one
// MediaStream with no additional provisioning (WV1 spike Q2: real audio
// samples, no PulseAudio, no sidecar, no audio device) — so every
// video-capable host classifies VideoAndAudioLevel directly. Non-linux
// hosts, hosts with only chrome-headless-shell installed (no full Chrome —
// selectDownloadBuild never chooses full Chrome off-linux, and the manifest/
// platform fallback can leave a linux host on headless-shell too), and
// hosts with neither build installed all classify NotCapableLevel.
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
		return notCapable("full-Chrome build not installed yet (download pending or unavailable)")
	}
	return videoAndAudio()
}

// notCapable and videoAndAudio are VideoCapability's only constructors
// within this package: they derive Capable/AudioAvailable from Level so the
// two cannot disagree, and keep Reason scoped to the not-capable cause.
// There is deliberately no videoOnly constructor: WebRTC tabCapture (the
// only supported capture mechanism) never produces a video-without-audio
// MediaStream, so ClassifyVideoCapability never needs to construct
// VideoOnlyLevel — see VideoOnlyLevel's doc for when one would be added.
func notCapable(reason string) VideoCapability {
	return VideoCapability{Level: NotCapableLevel, Capable: false, AudioAvailable: false, Reason: reason}
}

func videoAndAudio() VideoCapability {
	return VideoCapability{Level: VideoAndAudioLevel, Capable: true, AudioAvailable: true}
}
