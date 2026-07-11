// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"context"
	"fmt"
	"strconv"

	"github.com/chromedp/chromedp"
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
// agent's shared live-view tab (DefaultSessionID) and returns its tag name,
// trimmed innerText, and trimmed outerHTML (ADR-039 D-B3).
//
// Best-effort by design: a point with no element under it, or a CDP round
// trip that fails outright (tab crashed, timed out against m.PageTimeout(),
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
// chromedp.Run against the shared tab — it deliberately does not go through
// the LiveView/live-view registry at all (no screencast, no control-lock
// interaction), so it stays disjoint from live.go/browser_ws.go.
func (m *BrowserManager) InspectPoint(x, y float64) (InspectResult, error) {
	tabCtx, err := m.Session(DefaultSessionID)
	if err != nil {
		return InspectResult{}, fmt.Errorf("browser: inspect: cannot resolve session: %w", err)
	}

	ctx, cancel := context.WithTimeout(tabCtx, m.PageTimeout())
	defer cancel()

	js := fmt.Sprintf(inspectJSTemplate, formatCoord(x), formatCoord(y), inspectMaxTextChars, inspectMaxHTMLChars)

	var res inspectEvalResult
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &res)); err != nil {
		// Best-effort (see doc comment above): report "nothing resolved"
		// rather than propagating the CDP/eval error to the caller.
		return InspectResult{}, nil
	}

	if !res.Ok {
		return InspectResult{}, nil
	}
	return InspectResult{Ok: true, Tag: res.Tag, Text: res.Text, Html: res.Html}, nil
}
