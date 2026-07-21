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
// linux-only gate itself stays strict: per ADR-052 M3/M6, "only after
// per-OS audio verification" — macOS / Windows Phase 3/4 work; the
// classifier must not advertise Capable when capture cannot succeed.
//
// This is the BASE classification only — it does not know about the
// ADR-048 preconditions (capture extension seeded, shared-default-context
// capture enabled). See CaptureVideoCapability (manager.go/this file) for
// the full ADR-048 condition-3 check every gateway call site must use.
// Non-linux hosts, hosts with only chrome-headless-shell installed (no full
// Chrome — selectDownloadBuild never chooses full Chrome off-linux, and the
// manifest/platform fallback can leave a linux host on headless-shell too),
// and hosts with neither build installed all classify not-capable.
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
// panel stays on the JPEG fallback tier (ADR-047 D3), exactly like any other
// capture-path failure.
//
// ADR-052 Phase 1: PreferPackaged true still does NOT relax the linux-only
// gate here — only the Resolution-order priority in exec_resolver.go changes
// when the toggle is set. The capability classifier keeps its
// "linux + non-headless-shell" contract unchanged in Phase 1 (M3/M6: gate
// relaxes only after per-OS audio verification, which lands in Phase 3/4).
func ClassifyVideoCapabilityWithExec(execPath, installRoot string) VideoCapability {
	if execPath == "" {
		return ClassifyVideoCapability(installRoot)
	}
	if goosForCapability != "linux" {
		return notCapable(fmt.Sprintf(
			"live-view video requires a linux full-Chrome build; this host is %s",
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
// immediately without it, "no ExtensionDir configured") and shared-default-
// context capture must be enabled (coordinator.go's
// CaptureSharedContextEnabled — without it the agent's own tab never lives
// in the one context the extension's tabCapture can actually reach, so a
// capture attempt is certain to fail even on an otherwise video-capable
// Chrome build — see ADR-048's "visibility is not capturability" finding).
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
	coord := m.Coordinator()
	if coord == nil || !coord.CaptureSharedContextEnabled() {
		return notCapable(
			"shared default-context capture is disabled (tools.browser.capture_shared_context=false) — " +
				"required for the capture extension to reach the agent's tab, see ADR-048",
		)
	}
	return base
}
