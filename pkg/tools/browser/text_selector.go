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
// Both funnel into resolveTextTarget, which POLLS a bounded chromedp.Evaluate
// call (7-reviewer finding #1 — see its doc comment) to find the
// most-specific visible match, tags it with a unique marker attribute, and
// hands the caller back an ordinary CSS attribute selector — so every
// existing chromedp.WaitVisible/Click/SendKeys/Text call downstream is
// completely unchanged; only the selector string fed into it differs.
//
// ADR-038 deadlock discipline preserved throughout: every chromedp.Run call
// this file makes (resolveTextTarget's poll loop now makes several, not just
// one — see resolveTextTarget's doc comment) is individually bounded via
// context.WithTimeout against the tab context it's given, and NONE of them
// ever touch BrowserManager.mu — that is the actual ADR-038 invariant (see
// manager.go's "ADR-038 rule" comments: never hold the manager's lock across
// a CDP call), not a literal "exactly one call" constraint.

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

// textResolvePollInterval is the delay between retries in resolveTextTarget's
// poll loop (7-reviewer finding #1: a Cal.com confirmation banner, or a
// setTimeout-appended button, renders ASYNCHRONOUSLY — the original
// implementation ran exactly one chromedp.Evaluate scan before the caller's
// own WaitVisible ever got a chance to wait, so browser_wait{text:...}
// against an element that hadn't rendered yet failed in ~1ms instead of
// honoring its timeout budget). Short enough that a typical async render
// (hundreds of ms) is caught within a tick or two; long enough not to hammer
// the CDP connection with a busy-loop.
const textResolvePollInterval = 150 * time.Millisecond

// maxTextSelectorInputLen caps needle/scope length before they are embedded
// into the generated JS (7-reviewer finding #10, security-lead minor). This
// is independent of — and does not weaken — buildTextSelectorScript's
// injection-safety (every value is still json.Marshal'd, never
// raw-concatenated); it is a defense-in-depth bound against handing this
// path megabytes of text.
const maxTextSelectorInputLen = 2048

// textSelectorSeq is a process-wide counter mixed into every marker token so
// two resolveTextTarget calls — including two poll attempts in the same
// resolveTextTarget call, or two calls that land concurrently under
// concurrent tool dispatch — never collide. The counter alone is
// sufficient: monotonic and process-wide, so a nanosecond timestamp
// alongside it (the original implementation's approach) added nothing but
// noise (7-reviewer finding #13).
var textSelectorSeq uint64

// nextTextSelectorToken returns a fresh, unique marker token.
func nextTextSelectorToken() string {
	n := atomic.AddUint64(&textSelectorSeq, 1)
	return fmt.Sprintf("omnipus-tsel-%d", n)
}

// ---------------------------------------------------------------------------
// Pseudo-selector parsing
// ---------------------------------------------------------------------------

// textPseudoStartRe matches the START of any of the three text-pseudo forms.
// Used only to COUNT how many appear in a selector, so a second one is
// rejected with a clear error instead of being silently mis-parsed by
// textPseudoRe's single-pseudo, greedy-capture pattern (7-reviewer finding
// #8: `div:has-text("a"):text-is("b")` previously parsed as prefix="div",
// kind="has-text", needle=`a"):text-is("b` — garbage — because the greedy
// needle capture happily swallowed the second pseudo's literal syntax on its
// way to satisfying the trailing `"\)\s*$` anchor).
var textPseudoStartRe = regexp.MustCompile(`:(?:has-text|text-is|text)\(`)

// textPseudoRe recognizes a TRAILING Playwright-style text pseudo —
// :has-text("…")/:text("…") (substring) or :text-is("…") (exact) — on an
// otherwise-ordinary selector string, accepting either quote style via one
// alternation (7-reviewer finding #12 — collapses what used to be two
// separate regexes, one per quote style, since Go's RE2 engine doesn't
// support backreferences and so can't match "whichever quote opened").
//
// (?s) makes `.` match newlines too, in case an agent's text argument spans
// lines. The prefix capture is non-greedy so it stops at the EARLIEST
// plausible pseudo start; RE2's leftmost-first semantics (see the package
// regexp docs: it returns "the match that a backtracking search would have
// found first") guarantee the overall match is still correct even though
// `(has-text|text-is|text)` are tried in that order — a candidate
// alternative that doesn't lead to a full match is discarded automatically,
// so e.g. "text-is" is never partially matched as "text" leaving "-is(...)"
// unconsumed. FindStringSubmatchIndex (not FindStringSubmatch) is used to
// read the result, because FindStringSubmatch returns "" both for an
// UNMATCHED group (the alternative branch not taken) and for a matched-but-
// EMPTY capture (e.g. :text-is("")) — those two cases must be told apart to
// know which quote-style branch actually fired.
var textPseudoRe = regexp.MustCompile(`(?s)^(.*?):(has-text|text-is|text)\((?:"(.*)"|'(.*)')\)\s*$`)

// parseTextPseudo detects and parses a trailing text pseudo-selector on
// selector. Returns isText=false for a plain CSS selector (no trailing text
// pseudo) — callers keep the existing ByQuery fast path unchanged in that
// case, exactly zero behavior change for every selector written before this
// feature existed.
//
// cssPrefix is everything before the pseudo, trimmed; an empty prefix (e.g.
// bare `:has-text("x")`) becomes "*" (match any element). exact is true only
// for :text-is(...) (exact, normalized-text equality); :has-text(...) and
// :text(...) are both substring matches.
//
// Returns a non-nil err — cssPrefix/needle/exact/isText all zero-valued —
// when selector carries TWO OR MORE text pseudos chained together (7-reviewer
// finding #8): that shape is rejected outright rather than silently
// mis-parsed.
func parseTextPseudo(selector string) (cssPrefix, needle string, exact, isText bool, err error) {
	trimmed := strings.TrimRight(selector, " \t\r\n")

	if len(textPseudoStartRe.FindAllStringIndex(trimmed, 2)) > 1 {
		return "", "", false, false, fmt.Errorf("chained text pseudos are not supported — use one")
	}

	idx := textPseudoRe.FindStringSubmatchIndex(trimmed)
	if idx == nil {
		return "", "", false, false, nil
	}

	prefix := strings.TrimSpace(trimmed[idx[2]:idx[3]])
	if prefix == "" {
		prefix = "*"
	}
	pseudoKind := trimmed[idx[4]:idx[5]]

	var text string
	if idx[6] != -1 {
		// Double-quoted branch participated: `"(.*)"`.
		text = trimmed[idx[6]:idx[7]]
	} else {
		// Single-quoted branch participated: `'(.*)'`.
		text = trimmed[idx[8]:idx[9]]
	}

	return prefix, text, pseudoKind == "text-is", true, nil
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
	if !strings.HasPrefix(marker, prefix) || !strings.HasSuffix(marker, suffix) ||
		len(marker) < len(prefix)+len(suffix) {
		return ""
	}
	return marker[len(prefix) : len(marker)-len(suffix)]
}

// displayLocator returns the ORIGINAL, user-facing locator for a
// selector/text pair — used in error messages and echoed success payloads so
// the internal data-omnipus-tsel marker selector never surfaces to the agent
// (7-reviewer finding #6). Mirrors the pattern browser_wait's Execute
// originally introduced locally; now shared by every text-resolving tool for
// consistency: prefer `selector` when given (it is the more specific of the
// two when both are present — `text` is then merely scoping context already
// implied by a successful resolution), falling back to `text`, and finally to
// the role+name rendering when neither is set.
// Role+name is rendered as the agent wrote it (role=button name="Submit"),
// so an error about an element found that way names it that way and not by
// some CSS string the agent never typed.
func displayLocator(loc Locator) string {
	if loc.Selector != "" {
		return loc.Selector
	}
	if loc.Text != "" {
		return loc.Text
	}
	return displayRoleName(loc)
}

// scrubMarkerFromError replaces any literal occurrence of the internal
// marker selector (target) inside err's message with the ORIGINAL
// user-facing locator (displayTarget), so a POST-resolution chromedp failure
// — e.g. the resolved element vanished between resolveTextTarget marking it
// and the caller's own WaitVisible/Click/SendKeys/Text running (7-reviewer
// finding #6) — never leaks data-omnipus-tsel to the agent. A no-op
// (including on a nil err) when target and displayTarget are identical,
// i.e. no text resolution happened (plain CSS fast path).
func scrubMarkerFromError(err error, target, displayTarget string) error {
	if err == nil || target == displayTarget {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), target, displayTarget))
}

// ---------------------------------------------------------------------------
// JS-side resolution
// ---------------------------------------------------------------------------

// textResolveJSResult is the JSON shape textSelectorJSTemplateFmt returns,
// decoded directly by chromedp.Evaluate: chromedp's parseRemoteObject
// json.Unmarshal's the evaluated value straight into a struct destination
// (see github.com/chromedp/chromedp's eval.go) — no manual byte-then-
// unmarshal step needed, and the existing InspectPoint code path
// (inspect.go) already relies on the identical mechanism.
type textResolveJSResult struct {
	// Marker is the CSS attribute selector for the matched+tagged element;
	// "" when nothing matched and both InvalidScope and Ambiguous are false.
	Marker string `json:"marker"`
	// InvalidScope is true when `scope` was not valid CSS — querySelectorAll
	// threw (7-reviewer finding #3b): a deterministic, non-retryable
	// condition. The JS must never silently fall back to scanning the whole
	// document on a scope error.
	InvalidScope bool `json:"invalidScope"`
	// Ambiguous is true when more than one mutually-non-containing element
	// tied for the most specific match (7-reviewer finding #7) — also
	// non-retryable: the caller gets a clear "N elements match" error
	// instead of a silent first-match.
	Ambiguous bool `json:"ambiguous"`
	// Count is the number of tied elements when Ambiguous is true.
	Count int `json:"count"`
}

// textSelectorJSTemplateFmt is evaluated in the page (via chromedp.Evaluate)
// to find the most-specific VISIBLE element within scope whose normalized
// text matches needle, mark it with a unique attribute, and return a
// textResolveJSResult. scope/needle/exact/token/attr are substituted as %s
// placeholders holding json.Marshal'd (therefore injection-safe) JS literals
// — see buildTextSelectorScript. This injection-safety design is
// load-bearing and must not be weakened: page content or model-supplied
// text is ALWAYS embedded as inert string data via json.Marshal, never
// raw-concatenated.
//
// Visibility (7-reviewer finding #4): an element must have a non-empty
// getClientRects() (excludes display:none), getComputedStyle().visibility
// !== 'hidden', a non-negligible opacity, AND at least one rect with
// non-zero width/height — a display:none, visibility:hidden, opacity:0, or
// zero-size-clipped element is never a candidate.
//
// Text source (7-reviewer finding #5): matching uses el.innerText ONLY — no
// `|| textContent` fallback. innerText is layout-aware (only rendered,
// visible text); textContent is not, and would otherwise leak the literal
// SOURCE of a non-rendered <script>/<style> descendant into matching. An
// element with empty innerText is treated as having no visible text and
// skipped.
//
// Specificity / innermost selection (7-reviewer findings #2 and #7): among
// all matching candidates, any candidate that CONTAINS another candidate is
// excluded first (this is what makes `<div><button>Confirm</button></div>`
// resolve to the BUTTON even when the div's own normalized text is IDENTICAL
// to the button's, i.e. no extra prose in the div — the old strict-`<`
// "smallest wins" comparison kept the ancestor on a length tie because
// document order visits ancestors before descendants). Among the SURVIVING
// (mutually non-containing) candidates, the smallest normalized-text length
// wins; if more than one of those ties for smallest, the match is genuinely
// ambiguous (e.g. two sibling `<button>Delete</button>` elements) and an
// error is returned instead of silently picking the first one in DOM order.
// An ancestor/descendant pair can never register as "two matches" for
// ambiguity purposes, because the ancestor is always removed by the
// containment exclusion before the tie-break ever runs.
const textSelectorJSTemplateFmt = `(function(){
  var scope = %s;
  var rawNeedle = %s;
  var exact = %s;
  var token = %s;
  var attr = %s;
  var noMatch = {marker: '', invalidScope: false, ambiguous: false, count: 0};
  var needle = rawNeedle.replace(/\s+/g, ' ').trim().toLowerCase();
  if (!needle) { return noMatch; }
  var nodes;
  try {
    nodes = document.querySelectorAll(scope || '*');
  } catch (e) {
    return {marker: '', invalidScope: true, ambiguous: false, count: 0};
  }
  var candidates = [];
  for (var i = 0; i < nodes.length; i++) {
    var el = nodes[i];
    var rects = el.getClientRects();
    if (!rects || rects.length === 0) { continue; }
    var hasSize = false;
    for (var r = 0; r < rects.length; r++) {
      if (rects[r].width > 0 && rects[r].height > 0) { hasSize = true; break; }
    }
    if (!hasSize) { continue; }
    var cs = getComputedStyle(el);
    if (cs.visibility === 'hidden') { continue; }
    var opacity = parseFloat(cs.opacity);
    if (!isNaN(opacity) && opacity <= 0) { continue; }
    var raw = el.innerText || '';
    var norm = raw.replace(/\s+/g, ' ').trim().toLowerCase();
    if (!norm) { continue; }
    var isMatch = exact ? (norm === needle) : (norm.indexOf(needle) !== -1);
    if (!isMatch) { continue; }
    candidates.push({el: el, len: norm.length});
  }
  if (candidates.length === 0) { return noMatch; }

  var innermost = [];
  for (var a = 0; a < candidates.length; a++) {
    var containsOther = false;
    for (var b = 0; b < candidates.length; b++) {
      if (a === b) { continue; }
      if (candidates[a].el.contains(candidates[b].el)) { containsOther = true; break; }
    }
    if (!containsOther) { innermost.push(candidates[a]); }
  }

  var bestLen = -1;
  var winners = [];
  for (var k = 0; k < innermost.length; k++) {
    if (bestLen === -1 || innermost[k].len < bestLen) {
      bestLen = innermost[k].len;
      winners = [innermost[k]];
    } else if (innermost[k].len === bestLen) {
      winners.push(innermost[k]);
    }
  }
  if (winners.length > 1) {
    return {marker: '', invalidScope: false, ambiguous: true, count: winners.length};
  }

  var best = winners[0].el;
  best.setAttribute(attr, token);
  return {marker: '[' + attr + '="' + token + '"]', invalidScope: false, ambiguous: false, count: 1};
})()`

// buildTextSelectorScript renders textSelectorJSTemplateFmt with cssScope,
// needle, exact, and token all encoded via json.Marshal. This is the
// CRITICAL injection-safety step: every value becomes a well-formed,
// self-contained JS literal (json.Marshal escapes quotes, backslashes, and
// control characters — and, since encoding/json HTML-escapes by default, <,
// >, and & too), so page content or model-supplied text — including a
// needle containing `"`, `</script>`, or `);alert(` — is embedded as inert
// STRING DATA and can never terminate the literal early or inject executable
// JS. Never raw string-concatenated. (Security-reviewed; do not weaken.)
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
// POLLS (7-reviewer finding #1): retries the resolution scan every
// textResolvePollInterval until either a definitive result is found — a
// match, an ambiguity, or an invalid scope — or timeout elapses, so an
// element that renders ASYNCHRONOUSLY after this call starts (a
// setTimeout-appended button, a fetch-driven confirmation banner) is still
// caught, not missed by a single scan that ran before it existed. This is
// ESSENTIAL for browser_wait, whose entire purpose is waiting for exactly
// that kind of element — see WaitTool.Execute in tools.go, which passes its
// own timeout budget (getTextWaitTimeout / PageTimeout) straight through as
// this function's timeout, so the poll runs for that whole budget rather
// than failing after one ~1ms scan.
//
// Every individual chromedp.Run call this makes is bounded via
// context.WithTimeout against tabCtx, and this function never acquires
// BrowserManager.mu (ADR-038) — tabCtx is the only thing it touches. An
// "ambiguous" or "invalid scope" result is NOT retried — both are
// deterministic outcomes for a given DOM/scope pair, not a "not rendered
// yet" condition, so retrying would only waste the timeout budget.
//
// Returns a clear "no visible element matching text %q" error — never the
// cryptic underlying DOM/CDP error — when nothing matches by the deadline,
// so the calling tool's error message tells the agent exactly what went
// wrong instead of a bare "-32000" style CDP fault.
//
// A DEFINITIVE "no visible element matching text" answer from any earlier
// attempt always wins over a transient evaluation error from a later one
// (see lastNoMatchErr/lastEvalErr below): near the tail of the poll budget,
// an attempt's own per-call context can legitimately end up bounded by only
// a few milliseconds of the OVERALL deadline, and a CDP round trip that
// narrowly misses that sliver produces a bare "context deadline exceeded" —
// that is an infra hiccup on the LAST poll, not a signal that overrides
// what every prior poll already established cleanly.
func resolveTextTarget(
	tabCtx context.Context,
	cssScope, needle string,
	exact bool,
	timeout time.Duration,
) (markerSelector string, err error) {
	if strings.TrimSpace(needle) == "" {
		return "", fmt.Errorf("text selector: empty text to match")
	}
	if len(needle) > maxTextSelectorInputLen {
		return "", fmt.Errorf("text selector: text argument exceeds the %d-byte limit", maxTextSelectorInputLen)
	}
	if len(cssScope) > maxTextSelectorInputLen {
		return "", fmt.Errorf("text selector: selector scope exceeds the %d-byte limit", maxTextSelectorInputLen)
	}

	deadline := time.Now().Add(timeout)
	var lastNoMatchErr, lastEvalErr error

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			// No budget left for another attempt — stop polling and report
			// whatever the last attempt(s) already established, rather than
			// firing off a CDP round trip against an already-expired
			// context that can only ever fail.
			break
		}

		token := nextTextSelectorToken()
		script, berr := buildTextSelectorScript(cssScope, needle, exact, token)
		if berr != nil {
			return "", fmt.Errorf("text selector: %w", berr)
		}

		evalCtx, cancel := context.WithTimeout(tabCtx, remaining)
		var res textResolveJSResult
		runErr := chromedp.Run(evalCtx, chromedp.Evaluate(script, &res))
		cancel()

		switch {
		case runErr != nil:
			lastEvalErr = fmt.Errorf("text selector: evaluation failed: %w", runErr)
		case res.InvalidScope:
			return "", fmt.Errorf("text selector: invalid selector scope %q", cssScope)
		case res.Ambiguous:
			return "", fmt.Errorf(
				"text selector: %d elements match text %q — narrow it with a selector scope",
				res.Count,
				needle,
			)
		case res.Marker != "":
			return res.Marker, nil
		default:
			lastNoMatchErr = fmt.Errorf("no visible element matching text %q", needle)
		}

		// Cap the inter-poll sleep to whatever's left of the budget so it
		// can never overshoot the deadline — overshooting would otherwise
		// hand the NEXT attempt an already-expired (or near-zero) context,
		// which is exactly the doomed-final-attempt shape lastNoMatchErr's
		// priority above also guards against; capping the sleep avoids
		// wasting that attempt in the first place.
		sleepFor := textResolvePollInterval
		if remaining := time.Until(deadline); remaining < sleepFor {
			sleepFor = remaining
		}
		if sleepFor <= 0 {
			break
		}
		select {
		case <-tabCtx.Done():
			// The outer tab context ended (deadline or cancel) mid-wait. Resolve
			// through the SAME priority as the end-of-loop path below so a
			// definitive no-match an earlier poll already established wins over
			// the bare context error. UAT caught the old bare-return here leaking
			// "context deadline exceeded" when the tab's (shorter) deadline fired
			// during a sleep, even though prior polls had cleanly established the
			// element was absent — the agent must read the same actionable "no
			// visible element matching text …" whether the resolver's own
			// deadline or the tab's shorter one expired first.
			return "", resolvePendingErr(needle, lastNoMatchErr, lastEvalErr, tabCtx.Err())
		case <-time.After(sleepFor):
		}
	}

	return "", resolvePendingErr(needle, lastNoMatchErr, lastEvalErr, nil)
}

// resolvePendingErr picks the error resolveTextTarget reports once it stops
// polling — from EITHER exit path: its own timeout budget elapsing, or the
// outer tab context ending mid-wait. A definitive "no visible element matching
// text" answer an earlier poll already established wins over a bare context or
// evaluation error. This is the 7-reviewer intent (a doomed final CDP round
// trip must not override a clean prior result) extended to the tabCtx.Done()
// path that a UAT run caught leaking a raw "context deadline exceeded". Every
// no-match/fallback branch NAMES the needle (the eval-error branch does not —
// a failed CDP evaluation is a distinct "evaluation itself failed" signal, not
// an "element absent" one, so it surfaces its own message, with only the
// internal marker attr name redacted if the raw CDP text ever echoed it); no
// branch exposes the internal marker attribute, so the agent gets an
// actionable, consistent message regardless of which deadline fired.
func resolvePendingErr(needle string, lastNoMatchErr, lastEvalErr, ctxErr error) error {
	switch {
	case lastNoMatchErr != nil:
		// A definitive no-match an earlier poll established wins even over a
		// LATER eval error. Deliberate, knowingly-accepted tradeoff (originally a
		// 7-reviewer call for the common case: a tail CDP round trip narrowly
		// missing the deadline is an infra hiccup, not a real signal, and must
		// not override what healthy prior polls already proved). The rarer flip
		// side a UAT-review raised — a genuine mid-poll tab crash AFTER an
		// initial clean no-match reported as "not found" rather than "evaluation
		// failed" — is accepted so both exit paths stay consistent; the agent
		// re-driving a broken tab surfaces the real breakage on its next action
		// anyway. Distinguishing a one-off hiccup from a sustained crash would
		// need per-poll failure-run tracking not warranted for this race.
		return lastNoMatchErr
	case lastEvalErr != nil:
		// lastEvalErr wraps a raw chromedp/CDP error whose text is outside our
		// control, and the evaluation SCRIPT embeds the marker attribute name —
		// so a pathological CDP error echoing script source could carry it.
		// Redact the internal attr name (only when actually present, keeping the
		// %w chain intact in the overwhelmingly common case) so the
		// no-marker-leak guarantee holds for this branch too, matching the other
		// three that are marker-free by construction.
		if s := lastEvalErr.Error(); strings.Contains(s, textMarkerAttr) {
			return fmt.Errorf("%s", strings.ReplaceAll(s, textMarkerAttr, "[text-marker]"))
		}
		return lastEvalErr
	case ctxErr != nil:
		return fmt.Errorf("text selector: %w while waiting for %q", ctxErr, needle)
	default:
		return fmt.Errorf("no visible element matching text %q", needle)
	}
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

// wrapTextMatch wraps a resolveTextTarget outcome into the (target, cleanup,
// err) triple both resolveActionSelector and resolvePseudoOnlySelector
// return (7-reviewer finding #11 — factored out of what used to be
// duplicated in both: extract the token from the marker selector, build a
// cleanup closure over it, and prefix a resolution error with the tool
// name).
func wrapTextMatch(
	tabCtx context.Context,
	toolName, marker string,
	rerr error,
) (target string, cleanup func(), err error) {
	if rerr != nil {
		return "", func() {}, fmt.Errorf("%s: %w", toolName, rerr)
	}
	token := tokenFromMarkerSelector(marker)
	return marker, func() { removeTextMarker(tabCtx, token) }, nil
}

// textScopeFromSelector returns the CSS scope resolveActionSelector should
// use when `text` is given alongside `selector` (7-reviewer finding #3a): if
// selector itself carries a trailing text pseudo, its CSS PREFIX is used as
// the scope — the pseudo's own needle is discarded, since the explicit
// `text` argument always takes priority for what to match. A plain CSS
// selector (or "") is returned unchanged. Returns an error for a
// malformed/chained pseudo (parseTextPseudo) so the caller never falls back
// to passing untrusted, potentially-invalid CSS straight through as a scope
// — the JS-side invalidScope guard (see textSelectorJSTemplateFmt) is
// defense in depth for scope strings that are invalid CSS WITHOUT a
// trailing pseudo; this is the fix for the specific case where a pseudo on
// `selector` would otherwise poison the scope with its own needle syntax.
func textScopeFromSelector(selector string) (string, error) {
	cssPrefix, _, _, isText, err := parseTextPseudo(selector)
	if err != nil {
		return "", err
	}
	if isText {
		return cssPrefix, nil
	}
	return selector, nil
}

// resolvePseudoOnlySelector resolves selector when it carries a trailing text
// pseudo (:has-text/:text/:text-is) into a marker selector via
// resolveTextTarget; returns selector UNCHANGED, with a no-op cleanup, for
// plain CSS — the existing ByQuery fast path, zero behavior change.
//
// Used directly by browser_type, which has no separate "locate by visible
// text" PARAMETER: its existing `text` argument is already the value typed
// into the element (required since before this feature), so reusing that
// name for a second, unrelated meaning (as browser_click/get_text/wait do)
// would silently break every existing browser_type caller and its "text"
// parameter validation test. browser_type therefore supports ONLY the
// pseudo-selector route for text-based targeting — see TypeTool's
// Description for the documented limitation (7-reviewer finding #9): a bare
// `<input>` has no visible text of its own, so it can never be the resolved
// element via this route — only a container/label whose OWN visible text
// matches can be, which is a different element than the input the caller
// almost certainly means to type into. Use a CSS/attribute selector
// (input[name=…], input[placeholder*=…], input[type=…]) to target a field.
func resolvePseudoOnlySelector(
	tabCtx context.Context,
	toolName, selector string,
	timeout time.Duration,
) (target string, cleanup func(), err error) {
	cssPrefix, needle, exact, isText, perr := parseTextPseudo(selector)
	if perr != nil {
		return "", func() {}, fmt.Errorf("%s: %w", toolName, perr)
	}
	if !isText {
		return selector, func() {}, nil
	}
	marker, rerr := resolveTextTarget(tabCtx, cssPrefix, needle, exact, timeout)
	return wrapTextMatch(tabCtx, toolName, marker, rerr)
}

// resolveActionSelector computes which selector chromedp should actually act
// on for a browser_click/browser_get_text/browser_wait call, given the
// tool's raw `selector` and `text` arguments — implementing the shared
// resolution order documented on each tool's Description:
//
//  1. text non-empty → resolve by visible text, scoped to `selector` (or the
//     whole document when selector is "") — always a SUBSTRING match. When
//     selector itself carries a trailing text pseudo, only its CSS PREFIX is
//     used as the scope (textScopeFromSelector, 7-reviewer finding #3a) —
//     the pseudo's own needle is discarded in favor of the explicit `text`
//     argument.
//  2. else, selector carries a trailing text pseudo → resolve using its
//     parsed prefix/needle/exactness (resolvePseudoOnlySelector).
//  3. else → selector is returned unchanged (existing ByQuery fast path).
//
// Returns the selector for the caller's EXISTING chromedp action(s), and a
// cleanup func the caller MUST defer immediately (always safe to call — a
// no-op when no marker was set, including on the error path).
func resolveActionSelector(
	tabCtx context.Context,
	toolName, selector, text string,
	timeout time.Duration,
) (target string, cleanup func(), err error) {
	if text != "" {
		scope, serr := textScopeFromSelector(selector)
		if serr != nil {
			return "", func() {}, fmt.Errorf("%s: %w", toolName, serr)
		}
		marker, rerr := resolveTextTarget(tabCtx, scope, text, false, timeout)
		return wrapTextMatch(tabCtx, toolName, marker, rerr)
	}
	return resolvePseudoOnlySelector(tabCtx, toolName, selector, timeout)
}
