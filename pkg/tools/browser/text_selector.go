// Text-selector capability for the browser tools.
//
// Root cause (live UAT, Cal.com booking flow): agents (glm-5.2 and most
// LLMs) instinctively write Playwright-style selectors like
// `button:has-text("Confirm")` or `a:has-text("Book a call")`. The browser
// tools resolve selectors via chromedp.ByQuery — standard CSS
// document.querySelector — which REJECTS those pseudo-selectors outright
// ("DOM Error while querying (-32000)"). Every such click failed, breaking
// any multi-step flow where the only reliable handle on an element is its
// visible text (buttons/links rendered by a framework with no stable class
// or id).
//
// Fix: let browser_click/browser_type/browser_get_text/browser_wait match an
// element by its VISIBLE TEXT, two ways:
//  1. The Playwright-style pseudo already embedded in `selector` —
//     :has-text("...")/:text("...") (substring) or :text-is("...") (exact) —
//     parsed by parseTextPseudo.
//  2. An explicit `text` parameter (see each tool's Execute in tools.go).
//
// Both funnel into resolveTextTarget, which runs ONE bounded chromedp.Evaluate
// call to find the most-specific visible match, tags it with a unique marker
// attribute, and hands the caller back an ordinary CSS attribute selector —
// so every existing chromedp.WaitVisible/Click/SendKeys/Text call downstream
// is completely unchanged; only the selector string fed into it differs.
//
// ADR-038 deadlock discipline preserved throughout: resolveTextTarget and
// removeTextMarker each run exactly one bounded (context.WithTimeout)
// chromedp.Run call against the tab context they're given, and never touch
// BrowserManager.mu.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// textMarkerAttr is the DOM attribute resolveTextTarget stamps on the
// element it resolves to, so the caller's existing chromedp action(s) can
// address it as an ordinary CSS attribute selector
// ([data-omnipus-tsel="<token>"]) via the existing ByQuery path — no new
// chromedp query mode is introduced anywhere in the codebase.
const textMarkerAttr = "data-omnipus-tsel"

// textMarkerCleanupTimeout bounds removeTextMarker's best-effort attribute
// cleanup. Deliberately short: cleanup is cosmetic (a stray marker attribute
// left behind if this times out is invisible and never read by anything
// except a fresh resolveTextTarget call, which always mints a brand-new
// unique token), never load-bearing for tool correctness.
const textMarkerCleanupTimeout = 2 * time.Second

// textSelectorSeq is a process-wide counter mixed into every marker token
// (alongside a nanosecond timestamp) so two resolveTextTarget calls that
// land in the same nanosecond — plausible under concurrent tool dispatch —
// still never collide.
var textSelectorSeq uint64

// nextTextSelectorToken returns a fresh, unique marker token.
func nextTextSelectorToken() string {
	n := atomic.AddUint64(&textSelectorSeq, 1)
	return fmt.Sprintf("omnipus-tsel-%d-%d", time.Now().UnixNano(), n)
}

// ---------------------------------------------------------------------------
// Pseudo-selector parsing
// ---------------------------------------------------------------------------

// textPseudoDoubleQuoteRe and textPseudoSingleQuoteRe both recognize a
// TRAILING Playwright-style text pseudo — :has-text("…")/:text("…")
// (substring) or :text-is("…") (exact) — on an otherwise-ordinary selector
// string. Two separate regexes (one per quote style) because Go's RE2 engine
// does not support backreferences, so a single pattern like
// `:(kind)\((['"])(.*)\2\)$` (matching the SAME quote char that opened) isn't
// expressible directly.
//
// (?s) makes `.` match newlines too, in case an agent's text argument spans
// lines. The prefix capture is non-greedy so `cssPrefix` stops at the
// EARLIEST plausible pseudo start; RE2's leftmost-first semantics (see the
// package regexp docs: it returns "the match that a backtracking search
// would have found first") guarantee the overall match is still correct even
// though `(has-text|text-is|text)` are tried in that order — a candidate
// alternative that doesn't lead to a full match is discarded automatically,
// so e.g. "text-is" is never partially matched as "text" leaving "-is(...)"
// unconsumed.
var (
	textPseudoDoubleQuoteRe = regexp.MustCompile(`(?s)^(.*?):(has-text|text-is|text)\("(.*)"\)\s*$`)
	textPseudoSingleQuoteRe = regexp.MustCompile(`(?s)^(.*?):(has-text|text-is|text)\('(.*)'\)\s*$`)
)

// parseTextPseudo detects and parses a trailing text pseudo-selector on
// selector. Returns isText=false for a plain CSS selector (no trailing text
// pseudo) — callers keep the existing ByQuery fast path unchanged in that
// case, exactly zero behaviour change for every selector written before this
// feature existed.
//
// cssPrefix is everything before the pseudo, trimmed; an empty prefix (e.g.
// bare `:has-text("x")`) becomes "*" (match any element). exact is true only
// for :text-is(...) (exact, normalized-text equality); :has-text(...) and
// :text(...) are both substring matches.
func parseTextPseudo(selector string) (cssPrefix, needle string, exact bool, isText bool) {
	trimmed := strings.TrimRight(selector, " \t\r\n")

	m := textPseudoDoubleQuoteRe.FindStringSubmatch(trimmed)
	if m == nil {
		m = textPseudoSingleQuoteRe.FindStringSubmatch(trimmed)
	}
	if m == nil {
		return "", "", false, false
	}

	prefix := strings.TrimSpace(m[1])
	if prefix == "" {
		prefix = "*"
	}
	pseudoKind := m[2]
	text := m[3]

	return prefix, text, pseudoKind == "text-is", true
}

// ---------------------------------------------------------------------------
// Marker-selector helpers
// ---------------------------------------------------------------------------

// tokenFromMarkerSelector extracts the token from a marker selector produced
// by resolveTextTarget (format: [data-omnipus-tsel="<token>"]) so callers can
// hand it to removeTextMarker for cleanup. Returns "" for anything not in
// that exact shape — removeTextMarker no-ops on an empty token, so this is a
// safe, silent no-op rather than a panic if the format ever drifts.
func tokenFromMarkerSelector(marker string) string {
	prefix := `[` + textMarkerAttr + `="`
	const suffix = `"]`
	if !strings.HasPrefix(marker, prefix) || !strings.HasSuffix(marker, suffix) || len(marker) < len(prefix)+len(suffix) {
		return ""
	}
	return marker[len(prefix) : len(marker)-len(suffix)]
}

// ---------------------------------------------------------------------------
// JS-side resolution
// ---------------------------------------------------------------------------

// textSelectorJSTemplateFmt is evaluated in the page (via chromedp.Evaluate)
// to find the most-specific VISIBLE element within scope whose normalized
// text matches needle, mark it with a unique attribute, and return the CSS
// selector for that marker — or "" when nothing matches. scope/needle/exact/
// token/attr are substituted as %s placeholders holding json.Marshal'd
// (therefore injection-safe) JS literals — see buildTextSelectorScript.
//
// "Most specific" = smallest normalized-text length among matches, ties
// broken by DOM order (document.querySelectorAll order) — so
// `:has-text("Confirm")` resolves to <button>Confirm</button> itself, not a
// wrapping <div> that also contains the word "Confirm" by virtue of
// containing the button.
const textSelectorJSTemplateFmt = `(function(){
  var scope = %s;
  var rawNeedle = %s;
  var exact = %s;
  var token = %s;
  var attr = %s;
  var needle = rawNeedle.replace(/\s+/g, ' ').trim().toLowerCase();
  if (!needle) { return ''; }
  var nodes;
  try {
    nodes = document.querySelectorAll(scope || '*');
  } catch (e) {
    nodes = document.querySelectorAll('*');
  }
  var best = null;
  var bestLen = -1;
  for (var i = 0; i < nodes.length; i++) {
    var el = nodes[i];
    var rects = el.getClientRects();
    if (!rects || rects.length === 0) { continue; }
    var raw = (el.innerText || el.textContent || '');
    var norm = raw.replace(/\s+/g, ' ').trim().toLowerCase();
    if (!norm) { continue; }
    var isMatch = exact ? (norm === needle) : (norm.indexOf(needle) !== -1);
    if (!isMatch) { continue; }
    if (best === null || norm.length < bestLen) {
      best = el;
      bestLen = norm.length;
    }
  }
  if (best === null) { return ''; }
  best.setAttribute(attr, token);
  return '[' + attr + '="' + token + '"]';
})()`

// buildTextSelectorScript renders textSelectorJSTemplateFmt with cssScope,
// needle, exact, and token all encoded via json.Marshal. This is the
// CRITICAL injection-safety step: every value becomes a well-formed,
// self-contained JS literal (json.Marshal escapes quotes, backslashes, and
// control characters — and, since encoding/json HTML-escapes by default, <,
// >, and & too), so page content or model-supplied text — including a
// needle containing `"`, `</script>`, or `);alert(` — is embedded as inert
// STRING DATA and can never terminate the literal early or inject executable
// JS. Never raw string-concatenated.
func buildTextSelectorScript(cssScope, needle string, exact bool, token string) (string, error) {
	scopeJSON, err := json.Marshal(cssScope)
	if err != nil {
		return "", fmt.Errorf("failed to encode scope: %w", err)
	}
	needleJSON, err := json.Marshal(needle)
	if err != nil {
		return "", fmt.Errorf("failed to encode text: %w", err)
	}
	exactJSON, err := json.Marshal(exact)
	if err != nil {
		return "", fmt.Errorf("failed to encode exact flag: %w", err)
	}
	tokenJSON, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("failed to encode marker token: %w", err)
	}
	attrJSON, err := json.Marshal(textMarkerAttr)
	if err != nil {
		return "", fmt.Errorf("failed to encode marker attribute name: %w", err)
	}
	return fmt.Sprintf(textSelectorJSTemplateFmt, scopeJSON, needleJSON, exactJSON, tokenJSON, attrJSON), nil
}

// resolveTextTarget locates the most-specific VISIBLE element within cssScope
// (the whole document when cssScope is "" or "*") whose normalized text
// matches needle — substring match, or exact match when exact=true — marks
// it with a unique marker attribute, and returns an ordinary CSS attribute
// selector ([data-omnipus-tsel="<token>"]) that resolves to exactly that
// element via the EXISTING chromedp.ByQuery path.
//
// Runs exactly ONE bounded (context.WithTimeout(tabCtx, timeout))
// chromedp.Evaluate call. Never acquires BrowserManager.mu (ADR-038) — tabCtx
// is the only thing this function touches.
//
// Returns a clear "no visible element matching text %q" error — never the
// cryptic underlying DOM/CDP error — when nothing matches, so the calling
// tool's error message tells the agent exactly what went wrong instead of a
// bare "-32000" style CDP fault.
func resolveTextTarget(tabCtx context.Context, cssScope, needle string, exact bool, timeout time.Duration) (markerSelector string, err error) {
	if strings.TrimSpace(needle) == "" {
		return "", fmt.Errorf("text selector: empty text to match")
	}

	token := nextTextSelectorToken()
	script, err := buildTextSelectorScript(cssScope, needle, exact, token)
	if err != nil {
		return "", fmt.Errorf("text selector: %w", err)
	}

	ctx, cancel := context.WithTimeout(tabCtx, timeout)
	defer cancel()

	var marker string
	if runErr := chromedp.Run(ctx, chromedp.Evaluate(script, &marker)); runErr != nil {
		return "", fmt.Errorf("text selector: evaluation failed: %w", runErr)
	}
	if marker == "" {
		return "", fmt.Errorf("no visible element matching text %q", needle)
	}
	return marker, nil
}

// removeTextMarker best-effort strips the marker attribute resolveTextTarget
// set on its matched element, identified by token (see
// tokenFromMarkerSelector). Runs a single bounded chromedp.Evaluate call on
// tabCtx — same "no lock, always bounded" discipline as resolveTextTarget.
//
// Failures (timeout, the tab having since navigated away or closed) are
// intentionally silent and non-fatal: this cleans up an internal
// implementation detail that is never read by any tool logic other than a
// fresh resolveTextTarget call, which always mints its own brand-new unique
// token. A no-op when token is empty (defensive — matches
// tokenFromMarkerSelector's "" sentinel for an unrecognized marker shape).
func removeTextMarker(tabCtx context.Context, token string) {
	if token == "" {
		return
	}
	tokenJSON, err := json.Marshal(token)
	if err != nil {
		return
	}
	attrJSON, err := json.Marshal(textMarkerAttr)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(tabCtx, textMarkerCleanupTimeout)
	defer cancel()

	script := fmt.Sprintf(`(function(){
  var t = %s;
  var attr = %s;
  var el = document.querySelector('[' + attr + '="' + t + '"]');
  if (el) { el.removeAttribute(attr); }
  return true;
})()`, tokenJSON, attrJSON)

	var ok bool
	if runErr := chromedp.Run(ctx, chromedp.Evaluate(script, &ok)); runErr != nil {
		logger.WarnCF("browser", "text selector: marker cleanup failed (non-fatal)", map[string]any{
			"error": runErr.Error(),
		})
	}
}

// ---------------------------------------------------------------------------
// Tool-facing resolution
// ---------------------------------------------------------------------------

// resolvePseudoOnlySelector resolves selector when it carries a trailing text
// pseudo (:has-text/:text/:text-is) into a marker selector via
// resolveTextTarget; returns selector UNCHANGED, with a no-op cleanup, for
// plain CSS — the existing ByQuery fast path, zero behaviour change.
//
// Used directly by browser_type, which has no separate "locate by visible
// text" PARAMETER: its existing `text` argument is already the value typed
// into the element (required since before this feature), so reusing that
// name for a second, unrelated meaning (as browser_click/get_text/wait do)
// would silently break every existing browser_type caller and its "text"
// parameter validation test. browser_type therefore supports ONLY the
// pseudo-selector route for text-based targeting — see TypeTool's
// Description for the documented limitation.
func resolvePseudoOnlySelector(tabCtx context.Context, toolName, selector string, timeout time.Duration) (target string, cleanup func(), err error) {
	cssPrefix, needle, exact, isText := parseTextPseudo(selector)
	if !isText {
		return selector, func() {}, nil
	}
	marker, rerr := resolveTextTarget(tabCtx, cssPrefix, needle, exact, timeout)
	if rerr != nil {
		return "", func() {}, fmt.Errorf("%s: %w", toolName, rerr)
	}
	token := tokenFromMarkerSelector(marker)
	return marker, func() { removeTextMarker(tabCtx, token) }, nil
}

// resolveActionSelector computes which selector chromedp should actually act
// on for a browser_click/browser_get_text/browser_wait call, given the
// tool's raw `selector` and `text` arguments — implementing the shared
// resolution order documented on each tool's Description:
//
//  1. text non-empty → resolve by visible text, scoped to `selector` (or the
//     whole document when selector is "") — always a SUBSTRING match.
//  2. else, selector carries a trailing text pseudo → resolve using its
//     parsed prefix/needle/exactness (resolvePseudoOnlySelector).
//  3. else → selector is returned unchanged (existing ByQuery fast path).
//
// Returns the selector for the caller's EXISTING chromedp action(s), and a
// cleanup func the caller MUST defer immediately (always safe to call — a
// no-op when no marker was set, including on the error path).
func resolveActionSelector(tabCtx context.Context, toolName, selector, text string, timeout time.Duration) (target string, cleanup func(), err error) {
	if text != "" {
		marker, rerr := resolveTextTarget(tabCtx, selector, text, false, timeout)
		if rerr != nil {
			return "", func() {}, fmt.Errorf("%s: %w", toolName, rerr)
		}
		token := tokenFromMarkerSelector(marker)
		return marker, func() { removeTextMarker(tabCtx, token) }, nil
	}
	return resolvePseudoOnlySelector(tabCtx, toolName, selector, timeout)
}
