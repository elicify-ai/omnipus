package browser

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/chromeintegrity"
)

// VideoCapability is the classification of whether this host can run the
// live-view WebRTC video+audio capture stack (tabCapture in the managed full
// Chrome build). Anything other than Capable==true MUST make the live-view
// panel show the generic unavailable state; Reason is the specific,
// operator-only cause and MUST NOT be surfaced to the end user.
//
// Collapsed to this two-field shape (fix-wave simplification review): the
// previous CapabilityLevel enum + VideoOnlyLevel/AudioAvailable/AudioReason
// apparatus existed to represent a "video works, audio doesn't" state that
// WebRTC tabCapture can never actually produce (a MediaStream always carries
// both tracks together whenever it works at all — WV1 spike Q2) and that no
// production code ever read (only Capable/Reason are consulted anywhere in
// this codebase). Every caller needs exactly two things: can capture work,
// and if not, why — so that's all this type carries now.
type VideoCapability struct {
	// Capable is the overall classification: linux + a full-Chrome build
	// already installed under installRoot (or a trusted exec_path override),
	// the capture extension actually seeded, and shared-default-context
	// capture enabled (ADR-048 condition 3 — see CaptureVideoCapability).
	// WebRTC tabCapture (the encoder extension's own page, self-consuming
	// the active tab) only works in the full "chrome" build —
	// chrome-headless-shell lacks the tab-capture surface entirely — and
	// only on linux (the only platform the managed full-Chrome capture path
	// is validated against).
	Capable bool
	// Reason is the specific, operator-only cause when Capable is false.
	// Always non-empty when Capable is false; always empty when Capable is
	// true.
	Reason string
}

// goosForCapability is runtime.GOOS by default; overridable in tests so the
// platform-matrix's non-Linux rows (macOS/Windows -> not capable) are
// exercisable on any CI host without actually cross-running on that OS. Only
// ClassifyVideoCapability's platform branch honors this seam — EnsureChromium
// and selectDownloadBuild deliberately keep using the real runtime.GOOS
// because they determine which real binary layout to fetch/extract for THIS
// process, which must never be faked.
var goosForCapability = runtime.GOOS

// darwinAudioVerified is the Phase-3 AC-1 spike result. It is false until a
// real darwin --headless chrome.tabCapture run proves audio (ADR-047 §13.4 /
// ADR-052 Phase 3). While false, darwin stays not-capable for video per
// M3/M6: the per-OS gate relaxes only after per-OS audio verification.
// Flipped to true only by the macOS spike; tests set it via this seam. This
// is the single AC-4-vs-AC-5 flip point — when the spike records
// AUDIO-WORKS, flipping this var (consumed by videoCapableOS) lets darwin
// advertise Capable; until then, darwin stays not-capable with a clear
// operator Reason. HC #6: this explicit, documented default (false) is the
// reason darwin is not-capable by default — there is no silent fallback.
// VERIFIED 2026-08-12 on darwin/amd64, macOS, managed full Chrome 151.0.7922.77
// (chrome-mac-x64). The AC-1 spike (spikes/wv1-webrtc/q2-capture) was re-run
// unmodified against the macOS binary — only the hardcoded Linux Chrome path and
// scratch dir were parameterised (SPIKE_CHROME / SPIKE_SCRATCH) — and both gates
// passed with numbers matching the original in-pod Linux run:
//
//	T1 audio baseline (does headless render audio at all, with no audio device):
//	  noflags                selfRms 0.1775   ctxTime +3.01s   state=running
//	  --disable-audio-output selfRms 0.1762   ctxTime +3.00s   state=running
//	  --mute-audio           selfRms 0.1762   ctxTime +3.00s   state=running
//	  (linux measured 0.1770 against 0.1768 theoretical — darwin is identical)
//
//	T3c tabCapture via the SANCTIONED post-137 recipe (--remote-debugging-pipe +
//	Extensions.loadUnpacked + --allowlisted-extension-id), i.e. exactly what
//	coordinator.go's launchChrome/LoadExtension does in production:
//	  videoTracks=1  audioTracks=1  frames=80  fps=26.7
//	  rmsMean=0.30258  rmsMax=0.32193  nonzero=32/32
//
// 32/32 non-zero RMS probes is real tab audio, not silence dressed up as a
// track. The operator independently confirmed it audibly during the run.
//
// Why this was false for so long is worth stating plainly: nobody had ever run
// the spike on darwin. It was never a known macOS limitation — the gate is
// fail-closed by policy ("the per-OS gate relaxes only after per-OS audio
// verification"), and the verification simply had not happened. The practical
// consequence was that EVERY live-browser session on macOS silently used the
// JPEG screencast fallback, which is why the panel felt sluggish on macOS in a
// way that was identical locally and remotely: the cost was per-frame CPU, not
// network.
var darwinAudioVerified = true

// videoCapableOS reports whether a GOOS may advertise video+audio capture
// capability. linux is video-capable unconditionally (the only platform the
// managed full-Chrome capture path is validated against). darwin is
// video-capable only when the Phase-3 AC-1 audio spike has proven
// --headless chrome.tabCapture audio (darwinAudioVerified); until then the
// M3/M6 gate keeps darwin not-capable so the classifier never advertises
// Capable when capture cannot succeed (ADR-048 condition 3). Windows and
// every other OS stay not-capable until their own Phase-4 spike. This is
// the single helper both ClassifyVideoCapability and
// ClassifyVideoCapabilityWithExec consult — the one place to change when a
// per-OS spike completes.
func videoCapableOS(goos string) bool {
	return goos == "linux" || (goos == "darwin" && darwinAudioVerified)
}

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
// ADR-052 Phase 1: the package-managed Chrome (sibling chromium/ next to
// the binary, computed at runtime via packageChromeRoot) is ALSO consulted
// before classifying not-capable. The package Chrome IS video-capable when
// its sha256 verifies (installer.go's findInstalledBuild, which this
// function calls, now verifies chrome.sha256 when present — M2). The
// per-OS gate itself defaults strict: per ADR-052 M3/M6, "only after
// per-OS audio verification" — macOS (Phase 3) and Windows (Phase 4). The
// videoCapableOS helper is the AC-4 flip point for darwin: it stays
// not-capable until the Phase-3 AC-1 audio spike proves darwin
// --headless chrome.tabCapture audio and darwinAudioVerified is flipped.
// The classifier must not advertise Capable when capture cannot succeed.
//
// This is the BASE classification only — it does not know about the
// ADR-048 preconditions (capture extension seeded, shared-default-context
// capture enabled). See CaptureVideoCapability (manager.go/this file) for
// the full ADR-048 condition-3 check every gateway call site must use.
// Non-video-capable hosts (videoCapableOS is false — every non-linux GOOS
// today, plus darwin until darwinAudioVerified is flipped by the Phase-3
// spike), hosts with only chrome-headless-shell installed (no full Chrome —
// selectDownloadBuild chooses headless-shell on windows/other and the
// manifest/platform fallback can leave a linux host on headless-shell too),
// and hosts with neither build installed all classify not-capable.
func ClassifyVideoCapability(installRoot string) VideoCapability {
	if !videoCapableOS(goosForCapability) {
		return notCapable(fmt.Sprintf(
			"live-view video requires a linux full-Chrome build, or darwin once the ADR-052 Phase 3 audio spike (AC-1) proves audio; this host is %s",
			goosForCapability,
		))
	}
	platform, err := cftPlatform()
	if err != nil {
		return notCapable("unsupported platform for managed chromium: " + err.Error())
	}
	if findInstalledBuild(installRoot, platform, fullChromeBuild()) == "" {
		// ADR-052 Phase 1: fall through to the package-managed Chrome before
		// classifying not-capable. The package root is computed at runtime
		// from os.Executable(); when a valid full Chrome lives there
		// (findPackageChrome returns its binary path AND the SHA verifies),
		// the host is video-capable even when the per-profile installRoot
		// is empty.
		pkgRoot, pkgStatus := packageChromeRootProbe()
		if pkgStatus == ProbeUsable && pkgRoot != "" {
			if pkgBin, pkgSHA := findPackageChrome(pkgRoot); pkgBin != "" {
				verifyErr := cachedVerifyChromeSHA256(pkgBin, pkgSHA)
				if verifyErr == nil {
					return videoAndAudio()
				}
				logger.WarnCF("browser", "package chrome SHA-256 verification failed",
					map[string]any{
						"binary":           pkgBin,
						"manifest":         pkgSHA,
						"error":            verifyErr.Error(),
						"manifest_missing": errors.Is(verifyErr, chromeintegrity.ErrSHA256ManifestMissing),
					})
				return notCapable(fmt.Sprintf(
					"package chrome present but chrome.sha256 verification failed: %v — reinstall the omnipus package to recover the integrity-verified payload",
					verifyErr,
				))
			}
		}
		return notCapable("full-Chrome build not installed yet (download pending or unavailable)")
	}
	return videoAndAudio()
}

// ClassifyVideoCapabilityWithExec is ClassifyVideoCapability plus a resolved
// exec path override: when execPath is non-empty, that binary IS the browser
// that actually launches, so the managed install root's contents are
// irrelevant to whether capture can work. execPath may be either (a) the
// operator's explicit exec_path override (W3 e2e finding — tools.browser.exec_path
// pinned in config) or (b) a path exec_resolver.go's resolve() already
// resolved on this manager's behalf without any explicit operator override —
// most commonly a system google-chrome/chromium found on $PATH, but also a
// prior managed Chrome-for-Testing download (BrowserManager.VideoCapability,
// manager.go, passes whichever of the two is available). Both cases are
// trusted identically: full video+audio capability is assumed unless the
// path visibly names a headless-shell build, which lacks the
// tabCapture/extension surface entirely. This mirrors the same
// stat-check-only trust exec_resolver's resolve() itself applies (no
// probing) — this function does no additional validation of its own.
// Misclassifying a genuinely non-capable custom binary as capable is not
// fatal to the product: the capture session simply fails to start and the
// panel reports browser_webrtc_state{available:false}, exactly like any
// other capture-path failure — there is no fallback tier to degrade to
// (ADR-061).
//
// ADR-052 Phase 3: the per-OS gate now consults videoCapableOS — darwin
// passes only when the AC-1 audio spike (darwinAudioVerified) proves audio;
// otherwise the "linux + non-headless-shell" contract is unchanged. Windows
// and every other non-linux GOOS stay not-capable until their own Phase-4
// spike (M3/M6).
func ClassifyVideoCapabilityWithExec(execPath, installRoot string) VideoCapability {
	if execPath == "" {
		return ClassifyVideoCapability(installRoot)
	}
	if !videoCapableOS(goosForCapability) {
		return notCapable(fmt.Sprintf(
			"live-view video requires a linux full-Chrome build, or darwin once the ADR-052 Phase 3 audio spike (AC-1) proves audio; this host is %s",
			goosForCapability,
		))
	}
	base := filepath.Base(execPath)
	if strings.Contains(base, "headless-shell") || strings.Contains(base, "headless_shell") {
		return notCapable(
			"configured exec_path is a headless-shell build (no tabCapture surface); live-view video requires a full Chrome build",
		)
	}
	return videoAndAudio()
}

// notCapable and videoAndAudio are VideoCapability's only constructors
// within this package.
func notCapable(reason string) VideoCapability {
	return VideoCapability{Capable: false, Reason: reason}
}

func videoAndAudio() VideoCapability {
	return VideoCapability{Capable: true}
}

// CaptureVideoCapability narrows VideoCapability (manager.go's
// BrowserManager.VideoCapability, the base linux/full-Chrome-build check)
// with the ADR-048 condition-3 checks the base classifier cannot see on its
// own: the gateway-owned capture extension must actually be seeded
// (ExtensionDir set — capture_session.go's defaultEncoderStarter fails
// immediately without it, "no ExtensionDir configured") and this manager must
// be attached to a coordinator at all, because the encoder page and the tab
// it captures have to live in the same Chrome.
//
// The former third check — tools.browser.capture_shared_context — is gone
// with ADR-072 FR-031. Every session now bootstraps into Chrome's DEFAULT
// browser context unconditionally, because that is the only context
// chrome.tabCapture can reach ("visibility is not capturability", ADR-048);
// isolation moved down to one Chrome PROCESS and one profile directory per
// workspace, which capture is indifferent to. There is no longer a
// configuration under which the extension cannot see the tab.
// Both gaps degrade the classification to not-capable with an explicit
// operator Reason — this is the enforcement point for ADR-048 condition 3:
// "the capability classifier must not advertise Capable=true when capture
// cannot succeed." Every gateway call site (pkg/gateway/browser_webrtc.go)
// MUST use this method, never the bare VideoCapability(), when deciding
// whether to offer/attempt a WebRTC capture session.
func (m *BrowserManager) CaptureVideoCapability() VideoCapability {
	base := m.VideoCapability()
	if !base.Capable {
		return base
	}
	m.mu.Lock()
	extDir := m.cfg.ExtensionDir
	m.mu.Unlock()
	if extDir == "" {
		return notCapable(
			"capture extension not seeded (ExtensionDir unset — check gateway boot logs for a captureext seed failure)",
		)
	}
	if m.Coordinator() == nil {
		return notCapable(
			"no shared-Chrome coordinator attached — WebRTC capture drives the workspace's own Chrome, " +
				"which this manager is not connected to",
		)
	}
	return base
}
