// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"context"
	"fmt"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ADR-039 D-B3 — best-effort DOM inspect. inspectMaxTextChars/
// inspectMaxHTMLChars cap innerText/outerHTML BEFORE they leave the browser
// process (truncated inside the injected JS itself via String.slice, not
// after unmarshaling), so a pathological page can't balloon the CDP
// round-trip payload.
const (
	inspectMaxTextChars = 8000
	inspectMaxHTMLChars = 16000
)

// inspectEvalTimeout bounds InspectPoint's chromedp.Evaluate round trip.
// Deliberately much shorter than BrowserManager.PageTimeout() (the budget
// for a full page load/navigation, commonly 30s+) — document.elementFromPoint
// is a trivial synchronous DOM query, not a page load, so there is no reason
// to let it wait as long as a full page when the CDP command queue for this
// workspace's browser is contended (every chromedp.Run against one Chrome
// funnels through one fixed-capacity command queue drained by one goroutine,
// so a single busy or wedged command backs up every other command on that
// browser, any session, any tool — see runCDPWithTimeout and attach()'s doc
// comment in live.go for the ADR-038 analysis).
//
// The citation used to name handleScreencastEvent, which no longer exists:
// ADR-061 deleted the JPEG screencast pipeline outright and took that function
// with it. The queue it documented is still there and still the reason this
// constant exists, so the reference is retargeted rather than dropped.
//
// UAT finding (ADR-039 BE-2): a tester's pop-out annotate flow got a 502 from
// /api/v1/browser/inspect. The gateway log showed InspectPoint's CDP call DID
// fail cleanly with "context deadline exceeded" (the existing best-effort
// contract below already degrades that to a soft {ok:false}) — but it took
// the full m.PageTimeout() to get there, because a concurrent agent
// browser_screenshot tool call had the shared CDP transport backed up at the
// same moment. A ~30s-blocked HTTP handler is well within range to trip a
// fronting reverse proxy's idle/response timeout (Vite's dev proxy, an
// operator's nginx, etc.), which is what actually produced the 502 the
// tester saw — even though this Go handler was always going to resolve to a
// clean 200. Bounding this specific call tightly keeps the worst case within
// a few seconds regardless of what else is contending for the transport,
// mirroring the same "this should be fast, don't reuse the page-load budget"
// pattern screencastAckTimeout (live.go) already established for frame acks.
const inspectEvalTimeout = 5 * time.Second

// InspectResult is the outcome of resolving the DOM element at a point on the
// agent's shared live-view tab (ADR-039 D-B3).
//
// Best-effort by design: Ok=false with a nil error from InspectPoint is a
// normal, expected outcome (no element at that point, a CDP round trip that
// failed, a cross-origin subtlety) — never treated as a hard failure by
// callers. See InspectPoint's doc comment.
type InspectResult struct {
	Ok   bool
	Tag  string
	Text string
	Html string
}

// inspectEvalResult is the direct chromedp.Evaluate unmarshal target — it
// mirrors the JSON object shape produced by inspectJSTemplate.
type inspectEvalResult struct {
	Ok   bool   `json:"ok"`
	Tag  string `json:"tag"`
	Text string `json:"text"`
	Html string `json:"html"`
}

// inspectJSTemplate resolves the element at (x, y) via
// document.elementFromPoint — CSS-pixel viewport coordinates, the SAME space
// the ADR-038 screencast's input-injection path already uses (LiveFrame is
// captured at page_scale=1), so the SPA can pass the click point it captured
// off the live-view frame directly, with no extra transform.
//
// Returns {"ok":false} when no element is found at the point (e.g. an
// out-of-viewport coordinate, or an empty page) — a normal, non-exceptional
// result, not a script error. A cross-origin iframe's content is opaque to
// the top document, so elementFromPoint simply resolves to the <iframe>
// element itself in that case; that is also a valid ok:true result, not
// special-cased here — "best-effort" per ADR-039 D-B3 means the caller
// accepts whatever the browser itself is willing to disclose.
//
// %s/%s are the x/y coordinates (formatCoord); %d/%d are
// inspectMaxTextChars/inspectMaxHTMLChars.
const inspectJSTemplate = `(function(){
  var e = document.elementFromPoint(%s, %s);
  if (!e) { return {ok:false, tag:"", text:"", html:""}; }
  return {
    ok: true,
    tag: e.tagName ? e.tagName.toLowerCase() : "",
    text: (e.innerText || "").slice(0, %d),
    html: (e.outerHTML || "").slice(0, %d)
  };
})()`

// formatCoord renders a float64 as a minimal, locale-independent JS numeric
// literal for interpolation into inspectJSTemplate. strconv.FormatFloat (not
// fmt's %v/%g) guarantees a plain decimal/exponent form with '.' as the
// separator regardless of process locale, and never emits characters that
// could break out of the numeric literal position it's substituted into.
func formatCoord(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// InspectPoint resolves the DOM element at device (CSS) pixel (x, y) on the
// live-view tab — the WORKSPACE-OWNED set the operator drives
// (BrowserManager.OperatorSessionID) — and returns its tag name,
// trimmed innerText, and trimmed outerHTML (ADR-039 D-B3).
//
// Best-effort by design: a point with no element under it, or a CDP round
// trip that fails outright (tab crashed, timed out against inspectEvalTimeout,
// wedged transport, the page navigated away mid-eval), both come back as
// InspectResult{Ok:false}, err=nil — the caller (the gateway's inspect
// handler) is expected to surface this as a soft ok:false result to the SPA,
// never a 5xx: the annotate-and-discuss feature degrades gracefully to the
// cropped-image-only path (D-B1/B2) whenever inspect can't resolve anything.
// A non-nil error is reserved for the one case that IS worth surfacing as an
// infrastructure failure: the manager could not even produce a tab session
// (mirrors every browser_* tool's own Session() error handling).
//
// Does not hold BrowserManager.mu (or any lock) across the chromedp.Run call
// below — m.Session() takes and releases m.mu internally before returning
// the tab context (see manager.go's Session doc comment), so this call
// cannot contribute to the ADR-038 deadlock class that motivated
// LiveView.runCDP. This mirrors the SAME established pattern
// EvaluateTool/WaitTool (tools.go) already use for a one-off, timeout-bounded
// chromedp.Run against a resolved tab — it deliberately does not go through
// the LiveView/live-view registry at all (no screencast, no control-lock
// interaction), so it stays disjoint from live.go/browser_ws.go.
//
// Panic-safe (ADR-039 UAT BE-2): the whole body runs under a defer/recover
// that degrades ANY panic (a future chromedp/cdproto internal bug, a
// malformed CDP response from a wedged/crashing tab, etc.) to the exact same
// best-effort InspectResult{Ok:false}, err=nil outcome as every other
// failure mode above, logged at ERROR so it's diagnosable server-side. Without
// this, a panic here would unwind past net/http's per-request recover with no
// response ever written, which the calling HTTP connection sees as an abrupt
// reset — exactly the class of failure that can surface to a browser as a 502
// through any fronting reverse proxy. The gateway's HandleBrowserInspect
// (pkg/gateway/browser_inspect.go) is written entirely in terms of this
// function's (result, err) contract and needs no panic-handling of its own as
// long as that contract genuinely always holds — this is where it's enforced.
func (m *BrowserManager) InspectPoint(panelSessionID string, x, y float64) (result InspectResult, err error) {
	// Issue #671: inspect must read the tab the panel is SHOWING. The caller
	// resolves it the same way the live view does (PanelTabSetID); an empty id
	// — a caller with no chat context — falls back to the operator's
	// workspace-owned set, which is what this function used unconditionally
	// before. Reading the operator's tab while the panel shows the chat's
	// returns "no element at that point" for a point the user can plainly see,
	// or worse, an element from a page they are not looking at.
	if panelSessionID == "" {
		panelSessionID = m.OperatorSessionID()
	}
	defer func() {
		if rec := recover(); rec != nil {
			logger.ErrorCF("browser", "inspect: panic recovered, reporting best-effort no-result", map[string]any{
				"panic":      fmt.Sprintf("%v", rec),
				"stack":      string(debug.Stack()),
				"session_id": panelSessionID,
			})
			result, err = InspectResult{}, nil
		}
	}()

	tabCtx, sessionErr := m.Session(panelSessionID)
	if sessionErr != nil {
		return InspectResult{}, fmt.Errorf("browser: inspect: cannot resolve session: %w", sessionErr)
	}

	// inspectEvalTimeout, not m.PageTimeout() — see its doc comment (ADR-039
	// UAT BE-2): a DOM point-lookup is trivial and must fail fast under CDP
	// transport contention, not wait as long as a full page load.
	ctx, cancel := context.WithTimeout(tabCtx, inspectEvalTimeout)
	defer cancel()

	js := fmt.Sprintf(inspectJSTemplate, formatCoord(x), formatCoord(y), inspectMaxTextChars, inspectMaxHTMLChars)

	runCDP := m.evalCDP
	if runCDP == nil {
		runCDP = chromedp.Run
	}

	var res inspectEvalResult
	if runErr := runCDP(ctx, chromedp.Evaluate(js, &res)); runErr != nil {
		// Best-effort (see doc comment above): still report "nothing
		// resolved" to the caller rather than propagating the CDP/eval error
		// — but log it first (7-reviewer MEDIUM finding). Without this, a
		// crashed/wedged tab or a call that timed out against
		// inspectEvalTimeout was silently indistinguishable from the normal
		// "no element under the point" outcome (which logs nothing, by
		// design — see the !res.Ok branch below), making a real
		// infrastructure problem undiagnosable from the logs.
		logger.WarnCF("browser", "inspect: CDP/eval round trip failed, reporting best-effort no-result", map[string]any{
			"error":      runErr.Error(),
			"session_id": panelSessionID,
		})
		return InspectResult{}, nil
	}

	if !res.Ok {
		return InspectResult{}, nil
	}
	return InspectResult{Ok: true, Tag: res.Tag, Text: res.Text, Html: res.Html}, nil
}
