package browser

import (
	"errors"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ErrBrowserUnavailable is the sentinel for the "browser runtime unavailable"
// class of errors: snap stubs on PATH, managed-install failures (e.g. egress
// blocked), chromedp launch failures, and the "cannot locate chromium" wrapper.
// Wrap in-package failures with this sentinel via errors.Join or %w so callers
// can use errors.Is instead of substring matching.
var ErrBrowserUnavailable = errors.New("browser runtime unavailable")

// browserUnavailableMsg is the single canonical agent-facing message for every
// variant of the browser-unavailable class.
const browserUnavailableMsg = "Browser screenshots aren't available in this deployment — no working Chromium runtime."

// isBrowserUnavailable returns true when err belongs to the browser-unavailable
// class. It first checks the structured sentinel (covers all in-package failures
// that wrap ErrBrowserUnavailable), then falls back to a substring check for the
// externally-produced "chrome failed to start" text that chromedp itself emits and
// that we do not control.
func isBrowserUnavailable(err error) bool {
	if errors.Is(err, ErrBrowserUnavailable) {
		return true
	}
	// chromedp emits "chrome failed to start" on its own — we cannot wrap that
	// with ErrBrowserUnavailable because it originates outside this package.
	return strings.Contains(strings.ToLower(err.Error()), "chrome failed to start")
}

// classifyBrowserError maps a raw browser/chromedp error to a clean,
// agent-facing message that:
//   - never leaks internal details (snap paths, fork messages, permission denied)
//   - gives the model enough signal to choose the right next action
//
// Callers are responsible for logging the raw error for diagnostics before
// calling this function.
func classifyBrowserError(err error) string {
	if err == nil {
		return "The browser couldn't complete that action."
	}

	// Browser-unavailable class: sentinel or chromedp's own "chrome failed to start".
	if isBrowserUnavailable(err) {
		return browserUnavailableMsg
	}

	msg := strings.ToLower(err.Error())

	// DNS / host-resolution class (check before the generic "blocked" class
	// because some DNS error messages also contain "blocked").
	if strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dns resolution failed") {
		return "That URL didn't resolve — check the domain name."
	}

	// SSRF / egress-policy class.
	if strings.Contains(msg, "ssrf") ||
		strings.Contains(msg, "blocked by") {
		return "That URL is blocked by the network egress policy."
	}

	return "The browser couldn't complete that action."
}

// browserErrorResult classifies err and returns the appropriate ToolResult:
//
//   - Browser-unavailable → ErrorResultWithGuidance with an explicit
//     "do NOT install" directive (stops the apt/snap/npm flail loop).
//   - DNS / SSRF → plain ErrorResult (no install guidance needed).
//   - Default → plain ErrorResult.
func browserErrorResult(err error) *tools.ToolResult {
	if err == nil {
		return tools.ErrorResult("The browser couldn't complete that action.")
	}

	// Browser-unavailable class: return Guidance to stop the install loop.
	if isBrowserUnavailable(err) {
		return tools.ErrorResultWithGuidance(
			browserUnavailableMsg,
			"The browser runtime is unavailable here and cannot be installed — apt/snap/npm are blocked by the sandbox. Do NOT attempt to install a browser. Use fetch_url to read the page's text content instead.",
		)
	}

	return tools.ErrorResult(classifyBrowserError(err))
}
