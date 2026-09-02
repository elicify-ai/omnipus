package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// maxGetTextChars caps browser_get_text output to prevent enormous DOM
// dumps. Aligned to the ADR-066 D4 builtin-success cap (FR-014, B-15) so
// the result never reaches the tool-result choke point already over the
// cap it will be held to. No per-tool opt-out.
const maxGetTextChars = config.DefaultBuiltinSuccessCap // 64,000 chars

// getTextTruncationSuffix is appended when capGetText cuts the text.
const getTextTruncationSuffix = "\n[truncated at 64,000 chars]"

// capGetText holds a browser_get_text result to maxGetTextChars.
func capGetText(text string) string {
	if len(text) > maxGetTextChars {
		return text[:maxGetTextChars] + getTextTruncationSuffix
	}
	return text
}

// getTextWaitTimeout bounds the initial element-presence wait for
// browser_get_text and browser_wait with a SHORT, dedicated timeout instead
// of the full page-load budget (BrowserManager.PageTimeout(), commonly 30s).
// Without this, a selector that exists but is never visible (e.g. "title" —
// <title> lives in <head> and is never rendered/visible), or one that
// matches nothing on the page at all, blocked for the ENTIRE PageTimeout
// before failing — observed live as a ~30s hang on
// browser_get_text{selector:"title"}. 8s is generous for a DOM
// presence/visibility check to settle while still keeping a stuck agent turn
// responsive; PageTimeout remains the outer ceiling for the rest of each
// tool's work (e.g. the subsequent chromedp.Text call).
const getTextWaitTimeout = 8 * time.Second

// --- browser_navigate (US-5) ---

type NavigateTool struct {
	tools.BaseTool
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res ManagerResolver
}

func (t *NavigateTool) Name() string                 { return "browser_navigate" }
func (t *NavigateTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *NavigateTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *NavigateTool) Description() string {
	return "Open a URL in the browser tab (visit a webpage/website) and return page metadata — final URL " +
		"after redirects and page title. It does NOT report an HTTP status code — a 404 or 500 error page " +
		"loads and returns success just like any other page, so check the returned title/text (e.g. via " +
		"browser_get_text) to tell an error page from a real one. Use this to load a page before " +
		"browser_get_text, browser_screenshot, browser_click, or browser_evaluate act on it. Subject to SSRF " +
		"protection: requests to private/internal network addresses are blocked unless explicitly " +
		"allow-listed. If a human is currently controlling the browser via the live view, this call defers " +
		"instead of navigating — the result is {\"deferred\": true, \"reason\": ...} instead of page " +
		"metadata; wait for them to release control and retry."
}

func (t *NavigateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string", "description": "The URL to navigate to (http:// or https:// only)"},
		},
		"required": []string{"url"},
	}
}

// tabAbandonTimeout bounds each recovery CDP call in
// abandonTabAfterFailedLoad. Short: the tab is already in a bad state and the
// caller is on an error path, so a recovery that itself hangs must not extend
// the tool call -- but long enough for a Location read and an about:blank
// navigation on a busy browser.
//
// EACH call gets its OWN budget of this size, never a shared one -- see
// abandonTabAfterFailedLoad's "independent budgets" note for the SSRF window
// a shared budget left open.
const tabAbandonTimeout = 5 * time.Second

func (t *NavigateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return tools.ErrorResult("browser_navigate: 'url' parameter is required")
	}
	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// Composition order is FIXED (spec §14.2 rule 1): ownership resolves the
	// scope, controlledResult decides whether a human outranks this call, and
	// only then is the write lease taken on the resolved (key, owner) pair.
	if result := controlledResult(mgr, key, owner, t.Name()); result != nil {
		return result
	}
	deferred, release := leaseWrite(ctx, mgr, key, owner, tools.ToolAgentID(ctx), t.Name())
	if deferred != nil {
		return deferred
	}
	defer release()

	// FR-027, per action. Recorded AFTER both gates (a deferred call never
	// acted, so it is not an action) and BEFORE the CDP work (the trail must
	// keep the attempt even when the action panics, times out or is
	// cancelled). See recordBrowserAction's doc comment for the ordering
	// contract and targetHostForTool for how "target host" is derived.
	t.recordBrowserAction(ctx, key, owner, t.Name(), targetHostForTool(rawURL))

	if err := mgr.ValidateURL(ctx, rawURL); err != nil {
		return tools.ErrorResult(err.Error())
	}

	sessionCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_navigate: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(sessionCtx, mgr.PageTimeout())
	defer timeoutCancel()

	hops := watchRedirectHops(tabCtx)

	var title string
	err = chromedp.Run(
		tabCtx,
		chromedp.Navigate(rawURL),
		chromedp.Title(&title),
	)
	if err != nil {
		// SECURITY (2026-08-13): a failed load must not leave the tab
		// PARKED on the target -- see abandonTabAfterFailedLoad.
		return tools.ErrorResult(abandonTabAfterFailedLoad(
			ctx, mgr, sessionCtx, "browser_navigate", rawURL, hops, err,
		))
	}

	var finalURL string
	if err := chromedp.Run(tabCtx, chromedp.Location(&finalURL)); err != nil {
		logger.WarnCF("browser", "Failed to detect final URL after redirect", map[string]any{
			"requested_url": rawURL,
			"error":         err.Error(),
		})
		finalURL = rawURL
	}

	// Post-redirect SSRF check: Chrome's networking stack follows redirects
	// internally, so a public URL could redirect to a private IP (e.g.
	// 169.254.169.254). Validate the final URL and kill the page if blocked.
	if finalURL != rawURL {
		if err := mgr.ValidateURL(ctx, finalURL); err != nil {
			// Navigate away from the blocked page to prevent data exfiltration
			_ = chromedp.Run(tabCtx, chromedp.Navigate("about:blank"))
			return tools.ErrorResult(fmt.Sprintf(
				"browser_navigate: redirect from %s landed on blocked URL: %s", rawURL, err,
			))
		}
	}

	result := map[string]any{
		"url":   finalURL,
		"title": title,
	}
	if finalURL != rawURL {
		result["redirected_from"] = rawURL
	}
	return jsonResult(result)
}

// --- browser_click (US-5) ---

type ClickTool struct {
	tools.BaseTool
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res ManagerResolver
}

func (t *ClickTool) Name() string                 { return "browser_click" }
func (t *ClickTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ClickTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *ClickTool) Description() string {
	return "Click an element. Provide `selector` as a standard CSS selector (e.g. \"button.confirm\"), " +
		"OR a trailing Playwright-style text pseudo — button:has-text(\"Confirm\") (case-insensitive " +
		"substring) or a:text-is(\"Book now\") (exact match) — to match by visible text instead of CSS. " +
		"Alternatively (or additionally), pass `text` to target an element by its visible label directly " +
		"(case-insensitive substring match); when both selector and text are given, text is matched only " +
		"among elements inside selector. Provide selector OR text (or both). Text matching only considers " +
		"VISIBLE elements (rendered, non-zero size); if two non-containing candidates tie on the same text, " +
		"this errors as an ambiguous match rather than silently picking the first one. A click on a " +
		"target=\"_blank\" link or one that calls window.open may open a NEW tab and switch to it — " +
		"subsequent browser_* calls then act on that new tab, not the page you clicked from; check this " +
		"result's opened_new_tab/new_tab_index/note fields, or call browser_list_tabs, to confirm what's " +
		"active. If a human is currently controlling the browser via the live view, this call defers " +
		"instead of clicking — the result is {\"deferred\": true, \"reason\": ...} instead of a click " +
		"outcome; wait for them to release control and retry."
}

func (t *ClickTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector of the element to click, optionally ending in :has-text(\"...\") / :text-is(\"...\") to match by visible text",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Match an element by its visible text (case-insensitive substring) instead of — or scoped within — selector",
			},
		},
	}
}

func (t *ClickTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	selector, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	if selector == "" && text == "" {
		return tools.ErrorResult("browser_click: 'selector' parameter is required")
	}
	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// Composition order is FIXED (spec §14.2 rule 1): ownership resolves the
	// scope, controlledResult decides whether a human outranks this call, and
	// only then is the write lease taken on the resolved (key, owner) pair.
	if result := controlledResult(mgr, key, owner, t.Name()); result != nil {
		return result
	}
	deferred, release := leaseWrite(ctx, mgr, key, owner, tools.ToolAgentID(ctx), t.Name())
	if deferred != nil {
		return deferred
	}
	defer release()

	// FR-027, per action. Recorded AFTER both gates (a deferred call never
	// acted, so it is not an action) and BEFORE the CDP work (the trail must
	// keep the attempt even when the action panics, times out or is
	// cancelled). See recordBrowserAction's doc comment for the ordering
	// contract and targetHostForTool for how "target host" is derived.
	t.recordBrowserAction(ctx, key, owner, t.Name(), hostOfActiveTab(mgr, sid))

	tabCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_click: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, mgr.PageTimeout())
	defer timeoutCancel()

	// The ORIGINAL user-facing locator — never the internal marker selector
	// resolveActionSelector may produce — for error messages and the echoed
	// success payload (7-reviewer finding #6).
	displayTarget := displayLocator(selector, text)

	target, cleanup, rerr := resolveActionSelector(tabCtx, "browser_click", selector, text, mgr.PageTimeout())
	defer cleanup()
	if rerr != nil {
		return tools.ErrorResult(rerr.Error())
	}

	err = chromedp.Run(
		tabCtx,
		chromedp.WaitVisible(target, chromedp.ByQuery),
		chromedp.Click(target, chromedp.ByQuery),
	)
	if err != nil {
		// Explicitly NAME displayTarget in the outer message — never rely
		// solely on scrubMarkerFromError finding (and replacing) an
		// occurrence of the marker inside err's own text, since some
		// chromedp failure modes (a bare context-deadline timeout, in
		// particular) don't embed the selector in their error text at all,
		// which would otherwise leave the agent with no indication of what
		// was being acted on (7-reviewer finding #6). scrubMarkerFromError
		// remains defense in depth for the failure modes that DO embed the
		// marker (e.g. chromedp's "could not find node with given
		// selector" DOM errors).
		return tools.ErrorResult(fmt.Sprintf("browser_click: element %q not found or not clickable: %s",
			displayTarget, scrubMarkerFromError(err, target, displayTarget)))
	}

	// Echo back what the caller passed (existing contract); fall back to the
	// `text` argument only for the text-only case (selector == "") — NEVER
	// the resolved marker selector (7-reviewer finding #6: this used to echo
	// the internal data-omnipus-tsel marker straight into the agent-facing
	// success payload).
	echoSelector := selector
	if echoSelector == "" {
		echoSelector = text
	}
	result := map[string]any{"success": true, "selector": echoSelector}

	// ADR-041 D2: a click on a target="_blank" link or an element that calls
	// window.open may have spawned a new browser tab. Reconcile
	// deterministically right here — the guaranteed detection point per the
	// ADR, complementing the best-effort passive Target.targetCreated
	// listener (manager.go's handleTargetEvent) — and report it so the
	// agent knows a redirect happened and it is now on the new tab, instead
	// of continuing to act on the (now-background) opener page. This is
	// what fixes the headline ADR-041 failure: a Cal.com-style booking
	// button that opens its flow in a new tab.
	if outcome, rerr := mgr.ReconcileTabs(sid); rerr != nil {
		logger.WarnCF("browser", "browser_click: tab reconcile failed", map[string]any{"error": rerr.Error()})
	} else {
		applyReconcileOutcome(result, outcome)
	}

	return jsonResult(result)
}

// applyReconcileOutcome maps a BrowserManager.ReconcileOutcome onto
// browser_click's result map (ADR-041 fix F2 — the ADR headline bug
// re-created: a click that spawns a new tab the memory gate refuses, or whose CDP
// attach fails, used to surface as a plain success with no signal). Factored
// out of ClickTool.Execute so the mapping logic is unit-testable without a
// live Chromium/CDP connection (see tabs_test.go's
// TestApplyReconcileOutcome_* cases).
//
// The two "opened_new_tab" and "tab_opened_but_not_adopted" reportings below
// are deliberately INDEPENDENT ifs, not a switch/if-else-if (ADR-041
// second-fix-wave regression: a single click can spawn TWO new targets in
// one go — one adopts, the other is capped or fails to attach — and
// ReconcileOutcome already aggregates both signals independently; an
// if/else-if here silently dropped whichever signal lost the mutual
// exclusion, most commonly the stranded tab's Unadopted/Reason, since Adopted
// was checked first). Both keys, and both notes, can appear in the same
// result map.
func applyReconcileOutcome(result map[string]any, outcome ReconcileOutcome) {
	var notes []string

	if outcome.Adopted && outcome.NewActive != nil {
		result["opened_new_tab"] = true
		result["new_tab_index"] = outcome.NewActive.Index
		result["new_tab_url"] = outcome.NewActive.URL
		notes = append(
			notes,
			fmt.Sprintf("opened and switched to new tab %d: %s", outcome.NewActive.Index, outcome.NewActive.URL),
		)
	}

	if outcome.Unadopted {
		result["tab_opened_but_not_adopted"] = true
		result["reason"] = string(outcome.Reason)
		if outcome.UnadoptedCount > 1 {
			result["unadopted_count"] = outcome.UnadoptedCount
		}
		// "another" phrasing when an adoption note was already appended above
		// — the click both switched to a new tab AND stranded a second one.
		lead := "the click opened a new tab, but"
		if len(notes) > 0 {
			lead = "the click also opened another new tab, but"
		}
		switch outcome.Reason {
		case tabAdoptReasonMemoryPressure:
			// FR-063. The text names MEMORY and a remedy that exists, and
			// names NO limit and NO config key — there is no cap to raise any
			// more (ADR-072 D1.5a), so telling the model to raise one sends it
			// looking for a setting this build does not have. Without this arm
			// the refusal falls to the default "it could not be adopted" text
			// and the model retries the same open in a loop.
			notes = append(notes, lead+" this machine is low on memory, so it could not be adopted. "+
				"Close a tab with browser_close_tab and retry, or tell the user a tab could "+
				"not be opened.")
		case tabAdoptReasonAttachFailed:
			notes = append(notes, lead+" attaching to it failed — it may not be usable. Call "+
				"browser_list_tabs to check what's open.")
		default:
			notes = append(notes, lead+" it could not be adopted.")
		}
	}

	if len(notes) > 0 {
		result["note"] = strings.Join(notes, " ")
	}
}

// --- browser_type (US-5) ---

type TypeTool struct {
	tools.BaseTool
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res ManagerResolver
}

func (t *TypeTool) Name() string                 { return "browser_type" }
func (t *TypeTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *TypeTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *TypeTool) Description() string {
	return "Type text into an input element matching a CSS selector. selector may also end in a " +
		"Playwright-style text pseudo — e.g. div:has-text(\"...\") or :text-is(\"...\") — to match by " +
		"visible text instead of CSS; note the `text` parameter here is the VALUE typed into the element, " +
		"not a way to locate it (unlike browser_click/browser_get_text/browser_wait, which accept a " +
		"separate `text` param to locate an element by its visible label). LIMITATION: a bare <input> or " +
		"<textarea> has no visible text of its own, so the text-pseudo route can never resolve TO one " +
		"directly — it can only match an element whose own rendered text contains the needle (e.g. a " +
		"label or button), which is not the input you want to type into. To target a form field, use a " +
		"CSS/attribute selector instead: input[name=...], input[placeholder*=...], input[type=...], or a " +
		"stable id/class. By default (clear=false) the typed text is APPENDED to whatever the field " +
		"already contains — this tool does NOT clear the field first, so re-typing into a field that " +
		"already holds a value doubles it up (e.g. typing \"alice@example.com\" into a field already " +
		"holding \"bob@example.com\" yields \"bob@example.comalice@example.com\"). Pass clear=true to " +
		"clear the field's existing value before typing — use this when correcting a mistake or " +
		"overwriting stale input. Keep the default clear=false when a human and this agent may share the " +
		"same browser session and you want to continue typing where they left off rather than erase it. " +
		"This tool does NOT press Enter or submit the form — click the submit button separately. The " +
		"result does not echo the field's resulting value; use browser_get_text to verify it when it " +
		"matters. If a human is currently controlling the browser via the live view, this call defers " +
		"instead of typing — the result is {\"deferred\": true, \"reason\": ...} instead of a type " +
		"outcome; wait for them to release control and retry."
}

func (t *TypeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector of the input element, optionally ending in :has-text(\"...\") / :text-is(\"...\") to match by visible text",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Text to type into the element (this is the value typed, not a locator)",
			},
			"clear": map[string]any{
				"type": "boolean",
				"description": "If true, clear the field's existing value before typing (replace mode). If " +
					"false (the default), the text is APPENDED to whatever the field already contains — this " +
					"preserves existing behavior and anything a human or another turn already typed into " +
					"the browser this workspace's agents share. Default: false.",
			},
		},
		"required": []string{"selector", "text"},
	}
}

func (t *TypeTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	selector, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	clearField, _ := args["clear"].(bool)
	if selector == "" {
		return tools.ErrorResult("browser_type: 'selector' parameter is required")
	}
	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// Composition order is FIXED (spec §14.2 rule 1): ownership resolves the
	// scope, controlledResult decides whether a human outranks this call, and
	// only then is the write lease taken on the resolved (key, owner) pair.
	if result := controlledResult(mgr, key, owner, t.Name()); result != nil {
		return result
	}
	deferred, release := leaseWrite(ctx, mgr, key, owner, tools.ToolAgentID(ctx), t.Name())
	if deferred != nil {
		return deferred
	}
	defer release()

	// FR-027, per action. Recorded AFTER both gates (a deferred call never
	// acted, so it is not an action) and BEFORE the CDP work (the trail must
	// keep the attempt even when the action panics, times out or is
	// cancelled). See recordBrowserAction's doc comment for the ordering
	// contract and targetHostForTool for how "target host" is derived.
	t.recordBrowserAction(ctx, key, owner, t.Name(), hostOfActiveTab(mgr, sid))

	tabCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_type: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, mgr.PageTimeout())
	defer timeoutCancel()

	// browser_type has no separate "locate by visible text" PARAMETER (its
	// `text` arg is already the value to type) — only the pseudo-selector
	// route applies here. See resolvePseudoOnlySelector's doc comment.
	target, cleanup, rerr := resolvePseudoOnlySelector(tabCtx, "browser_type", selector, mgr.PageTimeout())
	defer cleanup()
	if rerr != nil {
		return tools.ErrorResult(rerr.Error())
	}

	// clearField (the "clear" arg) lets the caller choose between the
	// historical append-only behavior (default, preserves callers written
	// before this parameter existed — and lets a human and this agent
	// share a browser session without clobbering each other's typing) and
	// clearing the field's existing value first (opt-in). SetValue writes
	// the DOM `value` property directly to "" — it fires no input/change
	// event on its own, but the SendKeys call right after dispatches REAL
	// key events starting from that empty value, so frameworks that listen
	// for native input events (including React's synthetic-event system)
	// observe the same incremental typing they would from a human clearing
	// the field and retyping.
	actions := []chromedp.Action{chromedp.WaitVisible(target, chromedp.ByQuery)}
	if clearField {
		actions = append(actions, chromedp.SetValue(target, "", chromedp.ByQuery))
	}
	actions = append(actions, chromedp.SendKeys(target, text, chromedp.ByQuery))

	err = chromedp.Run(tabCtx, actions...)
	if err != nil {
		// browser_type's only locator is `selector` (its `text` arg is the
		// VALUE typed, never a locator). Explicitly NAME it in the outer
		// message rather than relying solely on scrubMarkerFromError finding
		// an occurrence inside err's own text — some chromedp failure modes
		// (a bare context-deadline timeout) don't embed the selector at all
		// (7-reviewer finding #6).
		return tools.ErrorResult(
			fmt.Sprintf("browser_type: element %q: %s", selector, scrubMarkerFromError(err, target, selector)),
		)
	}

	return jsonResult(map[string]any{"success": true, "cleared": clearField})
}

// --- browser_screenshot (US-5) ---

type ScreenshotTool struct {
	tools.BaseTool
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit

	res ManagerResolver
	// agentHome is the agent's fixed home directory (ADR-046's
	// fspolicy.EffectiveFSPolicy agentHome argument). When the turn carries a
	// TurnWorkspaceDir (the agent is a member of that turn's Workspace),
	// EffectiveFSPolicy prefers that re-rooted directory instead — so a
	// screenshot taken during a Workspace turn lands in that turn's work/
	// dir, not the agent's own home.
	agentHome string
	// restrict maps to fspolicy.FSScopeConfined (true) / FSScopeUnrestricted
	// (false), mirroring every other path-taking tool's restrictToWorkspace
	// setting.
	restrict bool
}

func (t *ScreenshotTool) Name() string                 { return "browser_screenshot" }
func (t *ScreenshotTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ScreenshotTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *ScreenshotTool) Description() string {
	return "Capture a screenshot of the CURRENT page as a JPEG image, and report its current URL and title. Use this to see what page is open — including a page the user navigated to themselves via the live browser panel (the tab is shared). Do not guess the URL from the visual content; read it from this tool's output. Captures the ENTIRE scrollable page (full page height), not just the visible viewport. The JPEG is also written to a file in your current working directory in addition to being returned inline."
}

func (t *ScreenshotTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ScreenshotTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	mgr, _, _, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}

	tabCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_screenshot: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, mgr.PageTimeout())
	defer timeoutCancel()

	// Wait for the page to finish rendering before capturing.
	// chromedp.Navigate waits for DOMContentLoaded, but CSS/images may still
	// be loading. Poll until document.readyState is "complete", then wait an
	// additional 500ms for client-side JS frameworks to finish painting.
	var buf []byte
	var pageURL, pageTitle string
	err = chromedp.Run(
		tabCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Best-effort readyState poll. Any error during the poll is
			// non-fatal — we fall through and let FullScreenshot try anyway.
			for i := 0; i < 30; i++ {
				var state string
				evalErr := chromedp.Evaluate(`document.readyState`, &state).Do(ctx)
				if evalErr != nil {
					break
				}
				if state == "complete" {
					// Extra settle time for JS frameworks (React hydration, etc.)
					_ = chromedp.Sleep(500 * time.Millisecond).Do(ctx)
					break
				}
				_ = chromedp.Sleep(100 * time.Millisecond).Do(ctx)
			}
			return nil //nolint:nilerr // poll errors are non-fatal by design
		}),
		chromedp.FullScreenshot(&buf, 90),
		// Capture the current URL + title so the model KNOWS where it is —
		// critical when the USER drove the shared tab somewhere via the live
		// panel (the agent otherwise has no way to read the location and was
		// observed guessing it from the visual content, e.g. reporting
		// "example.com" for the identical-looking example.org). Best-effort:
		// wrapped so a Location/Title failure never fails the screenshot.
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = chromedp.Location(&pageURL).Do(ctx)
			_ = chromedp.Title(&pageTitle).Do(ctx)
			return nil
		}),
	)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_screenshot: %s", err))
	}

	// Route the screenshot write through ResolvePath (ADR-046 mandatory
	// chokepoint, FR-003/FR-009/FR-034) instead of os.TempDir(), so it lands
	// in the turn's effective working directory (the per-turn Workspace
	// re-root when present, else the agent's own home's work/ dir) rather
	// than a process-wide shared temp directory no per-agent/per-turn
	// confinement ever covered.
	filename := fmt.Sprintf("omnipus-screenshot-%d.jpg", time.Now().UnixMilli())
	policy, err := tools.ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_screenshot: failed to resolve filesystem policy: %s", err))
	}
	handle, err := tools.ResolvePath(ctx, policy, "browser_screenshot", "", tools.FSOpWrite, filename)
	if err != nil {
		return tools.PermissionDeniedResult("browser_screenshot", err, err.Error())
	}
	defer handle.Close()
	if err = handle.WriteFile(buf); err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_screenshot: failed to save: %s", err))
	}
	// RealPath is the ONE documented exception to "never hand back a bare
	// string" (PathHandle.RealPath's doc comment) — used here solely because
	// ArtifactTags' [file:...] marker is an OS-boundary reference consumed
	// later by send_file/the media pipeline, not a PathHandle-based read.
	path, err := handle.RealPath()
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_screenshot: failed to resolve saved path: %s", err))
	}

	// FullScreenshot with quality>0 produces JPEG. Return as data URL so
	// normalizeToolResult can extract it, store via MediaStore, and deliver
	// inline as a media frame. The current-page header is PREPENDED as plain
	// text: normalizeToolResult strips the data: URL into the Media array but
	// KEEPS the surrounding text, so the model receives both the image and an
	// authoritative "you are on <url>" line (it must not infer the URL from
	// the pixels — see Description).
	dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf)
	header := "Current page URL: " + strings.TrimSpace(pageURL)
	if title := strings.TrimSpace(pageTitle); title != "" {
		header += "\nPage title: " + title
	}
	return &tools.ToolResult{
		ForLLM:       header + "\n" + dataURL,
		ArtifactTags: []string{fmt.Sprintf("[file:%s]", path)},
	}
}

// --- browser_get_text (US-5) ---

type GetTextTool struct {
	tools.BaseTool
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res ManagerResolver
}

func (t *GetTextTool) Name() string                 { return "browser_get_text" }
func (t *GetTextTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *GetTextTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *GetTextTool) Description() string {
	return "Read, extract, or scrape the visible inner text content of an element (or the whole page) — " +
		"this is how you read what's actually displayed on a page, not just its structure. Get the inner " +
		"text of an element. Provide `selector` as a standard CSS selector, OR a trailing Playwright-style " +
		"text pseudo — :has-text(\"...\") (substring) / :text-is(\"...\") (exact) — to " +
		"match by visible text. Alternatively (or additionally), pass `text` to target an element by its " +
		"visible label directly (case-insensitive substring match); when both are given, text is matched " +
		"only among elements inside selector. Provide selector OR text (or both). To read the entire " +
		"page's text, use a selector like \"body\" or \"html\". Output is capped at 64,000 characters " +
		"(truncated with a marker beyond that)."
}

func (t *GetTextTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector of the element, optionally ending in :has-text(\"...\") / :text-is(\"...\") to match by visible text",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Match an element by its visible text (case-insensitive substring) instead of — or scoped within — selector",
			},
		},
	}
}

func (t *GetTextTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	selector, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	if selector == "" && text == "" {
		return tools.ErrorResult("browser_get_text: 'selector' parameter is required")
	}

	mgr, _, _, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}

	tabCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_get_text: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, mgr.PageTimeout())
	defer timeoutCancel()

	// The ORIGINAL user-facing locator — never the internal marker selector
	// resolveActionSelector may produce — for error messages (7-reviewer
	// finding #6).
	displayTarget := displayLocator(selector, text)

	target, cleanup, rerr := resolveActionSelector(tabCtx, "browser_get_text", selector, text, getTextWaitTimeout)
	defer cleanup()
	if rerr != nil {
		return tools.ErrorResult(rerr.Error())
	}

	// Wait for the node to exist with a SHORT, dedicated timeout — not the
	// full PageTimeout — and use WaitReady (DOM presence) rather than
	// WaitVisible (rendered/visible). A node like <title> is present but
	// never "visible"; WaitVisible would block for the full PageTimeout on
	// every such selector before failing. See getTextWaitTimeout's doc
	// comment for the observed 30s hang this fixes.
	waitCtx, waitCancel := context.WithTimeout(tabCtx, getTextWaitTimeout)
	err = chromedp.Run(waitCtx, chromedp.WaitReady(target, chromedp.ByQuery))
	waitCancel()
	if err != nil {
		// Explicitly NAME displayTarget — see ClickTool.Execute's identical
		// rationale (7-reviewer finding #6): some chromedp failure modes
		// (bare context-deadline timeouts) don't embed the selector in their
		// own error text, so scrubMarkerFromError alone can't guarantee the
		// locator appears in the message.
		return tools.ErrorResult(fmt.Sprintf("browser_get_text: element %q not found: %s",
			displayTarget, scrubMarkerFromError(err, target, displayTarget)))
	}

	var resultText string
	err = chromedp.Run(tabCtx, chromedp.Text(target, &resultText, chromedp.ByQuery))
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_get_text: element %q not found: %s",
			displayTarget, scrubMarkerFromError(err, target, displayTarget)))
	}

	resultText = capGetText(resultText)

	return jsonResult(map[string]any{"text": resultText})
}

// --- browser_wait (US-5) ---

type WaitTool struct {
	tools.BaseTool
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res ManagerResolver
}

func (t *WaitTool) Name() string                 { return "browser_wait" }
func (t *WaitTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *WaitTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *WaitTool) Description() string {
	return "Wait for an element to become VISIBLE on the page (rendered, non-zero size, and not " +
		"display:none / visibility:hidden / opacity:0) — this is NOT a DOM-presence check. An element " +
		"that exists in the DOM but is never rendered visible (e.g. <title>, <meta>, <script>, or a " +
		"display:none field) can never satisfy this wait; use browser_get_text (which waits for DOM " +
		"presence, not visibility) for those instead. Provide `selector` as a standard CSS selector, OR a " +
		"trailing Playwright-style text pseudo — :has-text(\"...\") (substring) / :text-is(\"...\") " +
		"(exact) — to match by visible text. Alternatively (or additionally), pass `text` to wait for an " +
		"element with the given visible text directly (case-insensitive substring match); when both are " +
		"given, text is matched only among elements inside selector. Provide selector OR text (or both). " +
		"Waits up to 8 seconds by default; pass `timeout_ms` (100-60000) to use a different budget."
}

func (t *WaitTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector to wait for, optionally ending in :has-text(\"...\") / :text-is(\"...\") to match by visible text",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Wait for an element with this visible text (case-insensitive substring) instead of — or scoped within — selector",
			},
			"timeout_ms": map[string]any{
				"type":        "integer",
				"description": "How long to wait for the element to become visible, in milliseconds (100-60000). Default: 8000 (8 seconds).",
			},
		},
	}
}

func (t *WaitTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	selector, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	if selector == "" && text == "" {
		return tools.ErrorResult("browser_wait: 'selector' parameter is required")
	}

	// `timeout_ms` lets the caller extend the wait beyond the
	// previously-hardcoded 8s budget, which used to make "wait longer than
	// 8s" impossible — the whole reason this tool exists for slow-rendering
	// content. Defaults to getTextWaitTimeout (8s) when omitted, matching
	// prior behavior exactly for existing callers.
	waitTimeout := getTextWaitTimeout
	if raw, ok := args["timeout_ms"]; ok {
		ms, ok := raw.(float64)
		if !ok {
			return tools.ErrorResult("browser_wait: 'timeout_ms' must be a number")
		}
		if ms < 100 || ms > 60000 {
			return tools.ErrorResult("browser_wait: 'timeout_ms' must be between 100 and 60000")
		}
		waitTimeout = time.Duration(ms) * time.Millisecond
	}

	mgr, _, _, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}

	tabCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_wait: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, mgr.PageTimeout())
	defer timeoutCancel()

	// Used for the timeout error message below: prefer echoing the CSS
	// selector the caller gave; fall back to the text query when only `text`
	// was supplied, so the error always names what the agent was looking for
	// rather than an opaque internal marker selector (7-reviewer finding #6
	// — now the shared displayLocator helper, mirrored consistently across
	// click/type/get_text/wait, since this is where that pattern started).
	displayTarget := displayLocator(selector, text)

	target, cleanup, rerr := resolveActionSelector(tabCtx, "browser_wait", selector, text, waitTimeout)
	defer cleanup()
	if rerr != nil {
		return tools.ErrorResult(rerr.Error())
	}

	// Same fail-fast rationale as browser_get_text (see getTextWaitTimeout's
	// doc comment): a selector that never appears would otherwise block for
	// the full PageTimeout (commonly 30s) before failing. Bound the wait with
	// the same short, dedicated timeout (or the caller's timeout_ms override)
	// so a missing selector fails fast.
	//
	// NOTE: resolveActionSelector above already POLLS for up to waitTimeout
	// for a text-resolved target to appear — this
	// WaitVisible call is a second, short wait for the now-marked element to
	// additionally become visible, which is normally instantaneous since
	// resolveTextTarget only ever marks a visible element in the first place.
	waitCtx, waitCancel := context.WithTimeout(tabCtx, waitTimeout)
	err = chromedp.Run(waitCtx, chromedp.WaitVisible(target, chromedp.ByQuery))
	waitCancel()
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_wait: timeout waiting for %q: %s",
			displayTarget, scrubMarkerFromError(err, target, displayTarget)))
	}

	return jsonResult(map[string]any{"found": true})
}

// --- browser_evaluate (US-5) ---
// Which agents may call it is decided by TOOL POLICY (Jim holds the only
// agent-level grant; Mia and Ava resolve deny). Whether it runs AT ALL on this
// installation is decided by sandbox.browser_evaluate_enabled, which is now
// SEEDED TRUE (ADR D1.9b ruling 2).

// EvaluateTool executes arbitrary JavaScript in the browser page context.
// It is always registered in the tool catalog so the LLM always sees the tool —
// registration has never been conditional on any flag.
//
// The LIVE enforcement gate is executeEnabled (set at construction from
// cfg.Sandbox.BrowserEvaluateEnabled, which resolves nil -> false). It is the
// operator's runtime kill switch, distinct from the per-agent tool policy: a
// policy denial removes the tool from an agent's manifest entirely, while this
// gate refuses at execution with a message naming the setting.
//
// (#438, #70): a pkg/policy declarative mirror of this deny-by-default intent
// used to exist but was dead code (no live tool-dispatch caller) and was
// removed — the executeEnabled check below is what actually stops execution
// at runtime, and always was.
type EvaluateTool struct {
	tools.BaseTool
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res            ManagerResolver
	executeEnabled bool
}

func (t *EvaluateTool) Name() string                 { return "browser_evaluate" }
func (t *EvaluateTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *EvaluateTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *EvaluateTool) Description() string {
	return "Execute JavaScript in the active tab's page context (run scripts, read/manipulate the DOM). " +
		"Enabled by default on a standard installation. An operator can still turn it off for the whole " +
		"installation by setting sandbox.browser_evaluate_enabled=false; if they have, this tool returns a " +
		"result whose error names that setting explicitly, which is how you tell an installation-wide " +
		"disable apart from your own tool policy denying you (a policy denial removes the tool from your " +
		"list entirely, so you would not be reading this). " +
		"The result's `result` field holds your expression's JSON-serialized value; a genuine JavaScript " +
		"null and a non-serializable value (a DOM node, function, or circular reference) BOTH come back as " +
		"result: null — the only way to tell them apart is a `note` field present ONLY on the " +
		"non-serializable case explaining why. If a human is currently controlling the browser via the " +
		"live view, this call defers instead of executing — the result is {\"deferred\": true, \"reason\": " +
		"...} instead of an evaluation outcome; wait for them to release control and retry."
}

func (t *EvaluateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"js": map[string]any{"type": "string", "description": "JavaScript expression to evaluate"},
		},
		"required": []string{"js"},
	}
}

func (t *EvaluateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	// Execution gate: operator must opt in via cfg.Sandbox.BrowserEvaluateEnabled.
	// This is the SOLE live gate (see the type doc above and #438).
	if !t.executeEnabled {
		return tools.ErrorResult(
			"browser_evaluate: disabled — set sandbox.browser_evaluate_enabled=true in config to enable",
		)
	}

	js, _ := args["js"].(string)
	if js == "" {
		return tools.ErrorResult("browser_evaluate: 'js' parameter is required")
	}
	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// Composition order is FIXED (spec §14.2 rule 1): ownership resolves the
	// scope, controlledResult decides whether a human outranks this call, and
	// only then is the write lease taken on the resolved (key, owner) pair.
	if result := controlledResult(mgr, key, owner, t.Name()); result != nil {
		return result
	}
	deferred, release := leaseWrite(ctx, mgr, key, owner, tools.ToolAgentID(ctx), t.Name())
	if deferred != nil {
		return deferred
	}
	defer release()

	// FR-027, per action. Recorded AFTER both gates (a deferred call never
	// acted, so it is not an action) and BEFORE the CDP work (the trail must
	// keep the attempt even when the action panics, times out or is
	// cancelled). See recordBrowserAction's doc comment for the ordering
	// contract and targetHostForTool for how "target host" is derived.
	t.recordBrowserAction(ctx, key, owner, t.Name(), hostOfActiveTab(mgr, sid))

	tabCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_evaluate: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, mgr.PageTimeout())
	defer timeoutCancel()

	var raw []byte
	err = chromedp.Run(tabCtx, chromedp.Evaluate(js, &raw))
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_evaluate: %s", err))
	}

	return classifyEvalResult(raw)
}

// classifyEvalResult interprets the raw JSON bytes returned by the CDP Evaluate
// call (via chromedp.Evaluate with a *[]byte destination):
//
//   - nil raw: CDP returned no serializable value — the JS expression evaluated
//     to a non-serializable type (DOM node, function, circular object, etc.).
//     Returns a non-error result with result=null and an explanatory note so the
//     agent can distinguish this from an intentional JS null.
//   - raw == "null": genuine JavaScript null. Returns {"result": null} with no note.
//   - everything else: a valid JSON scalar, array, or object. Unmarshal and return
//     {"result": <value>} so the caller gets a typed Go value.
//
// This is factored out so unit tests can exercise the classification logic without
// a live Chromium binary.
func classifyEvalResult(raw []byte) *tools.ToolResult {
	if raw == nil {
		// CDP sent no serializable value — the expression produced a non-JSON type.
		return jsonResult(map[string]any{
			"result": nil,
			"note":   "value was not JSON-serializable (e.g. DOM node, function, or circular reference)",
		})
	}

	// Unmarshal the raw JSON so we pass a typed value through to the result map.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Malformed JSON from CDP — treat as non-serializable.
		return jsonResult(map[string]any{
			"result": nil,
			"note":   "value was not JSON-serializable (e.g. DOM node, function, or circular reference)",
		})
	}

	return jsonResult(map[string]any{"result": v})
}

// controlledResult implements ADR-038 D6's cooperative turn-coordination: when
// a human viewer currently holds interactive control of the browser's live
// view (see pkg/tools/browser/live.go), the agent's own interactive tools
// (navigate/click/type/evaluate — the ones that would "fight for the cursor")
// defer instead of executing, returning a non-error, visible ToolResult so
// the LLM can see why nothing happened and tell the user to wait. Read-only
// tools (browser_screenshot, browser_get_text, browser_wait) are NOT gated —
// they don't inject input, so they can't conflict with a human driving the
// same page.
//
// Returns nil (no deferral) when the session is uncontrolled or has no live
// view at all — the overwhelmingly common case, so this stays a cheap map
// lookup on the hot path.
//
// LIMITATION (documented per ADR-038 D6): this is cooperative, not
// preemptive. A tool call already in flight when a human takes control
// finishes normally — there is no mid-tool preemption in v1.
//
// The deferral is a NON-ERROR result (IsError stays false — the
// deferral is not a tool failure, it's cooperative turn-coordination), but it
// must be structurally distinguishable from a normal success payload rather
// than prose-only. Every one of these seven callers (navigate/click/type/
// evaluate/switch_tab/close_tab/open_tab) previously returned this as a bare
// sentence with no signal beyond text a model might not parse — a
// success-shaped no-op. The body is now JSON: {"deferred": true, "reason":
// "..."}, so a caller can check for the "deferred" key the same way it would
// check any other tool's result shape.
func controlledResult(mgr *BrowserManager, key BrowsingKey, owner TabOwner, toolName string) *tools.ToolResult {
	// FR-002c. This asked the live registry about a hardcoded shared session id
	// until ADR-072 D1
	// re-keyed the live-view registry. Left on the constant it would match
	// nothing and return false FOREVER — an intact, populated human-control
	// lock that is never consulted, with no error, no log line and every lease
	// test still green. It MUST ask about the (BrowsingKey, TabOwner) pair the
	// call has already resolved, which is the same string the live panel takes
	// control of (BrowserManager.OperatorSessionID for the operator's own tabs).
	if !mgr.Live().IsControlled(sessionKey(key, owner)) {
		return nil
	}
	reason := "a human is currently controlling this browser via the live view — " +
		"wait for them to release control before driving the browser further"
	body, err := json.Marshal(map[string]any{
		"deferred": true,
		"reason":   reason,
	})
	if err != nil {
		// Should be unreachable for a static map of strings/bools, but never
		// silently drop the deferral signal if it somehow happens.
		body = []byte(fmt.Sprintf(`{"deferred":true,"reason":%q}`, reason))
	}
	return tools.NewToolResult(fmt.Sprintf("%s: %s", toolName, string(body)))
}

// jsonResult marshals v to JSON and returns a SilentResult.
func jsonResult(v any) *tools.ToolResult {
	data, err := json.Marshal(v)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser: failed to marshal result: %s", err))
	}
	return tools.SilentResult(string(data))
}

// Compile-time interface checks
var (
	_ tools.Tool = (*NavigateTool)(nil)
	_ tools.Tool = (*ClickTool)(nil)
	_ tools.Tool = (*TypeTool)(nil)
	_ tools.Tool = (*ScreenshotTool)(nil)
	_ tools.Tool = (*GetTextTool)(nil)
	_ tools.Tool = (*WaitTool)(nil)
	_ tools.Tool = (*EvaluateTool)(nil)
)

// Ensure logger import is used
var _ = logger.WarnCF

// abandonTabAfterFailedLoad handles the security-critical error path shared by
// browser_navigate and browser_open_tab: a navigation that fails to COMPLETE
// must not leave the tab sitting on the target URL.
//
// The gap it closes (found 2026-08-13 by running the browser suite on macOS,
// where TestExecute_Navigate_PostRedirectSSRF reported "page load failed:
// context deadline exceeded" instead of a blocked-redirect error): both tools
// returned the load error immediately and navigated away ONLY on the
// post-redirect SSRF branch, which is reached only when the load SUCCEEDS. So
// a public URL that redirects to an internal address which merely responds
// SLOWLY produced: page-load timeout -> early error return -> tab still
// pointed at the internal page, with Chrome free to finish loading it after
// our deadline. The next browser_get_text/browser_screenshot on that tab then
// read the internal content -- an SSRF bypass triggered by timing alone, with
// the tool having reported an error. macOS surfaced it because link-local
// (169.254.0.0/16) connections hang there rather than failing fast, but the
// window is platform-independent: any slow-responding internal host does it.
//
// Recovery runs on a FRESH bounded context derived from sessionCtx, never the
// caller's tabCtx -- the usual cause of getting here is tabCtx's own deadline,
// and a dead context cannot navigate anything away.
//
// INDEPENDENT BUDGETS (review finding F8, 2026-08-13): the two recovery steps
// get one tabAbandonTimeout budget EACH, never a shared one. They shared a
// single recoveryCtx when this helper was first written, which meant the
// DIAGNOSTIC location read could consume the entire budget and leave nothing
// for the SECURITY-CRITICAL about:blank navigation -- so the tab stayed parked
// on the internal target in exactly the scenario this helper was written for
// (a wedged renderer that answers nothing inside the deadline). Neither
// original regression test caught it because the stalling fixture commits a
// partial document, so Location answers immediately there. The abandon
// navigation must run regardless of what the read before it did.
//
// The read still goes FIRST (it is what names a blocked redirect target, and
// about:blank would overwrite the answer), which costs the abandon up to
// tabAbandonTimeout of delay on a wedged tab -- bounded, and no tool call of
// this session's can read the tab in the meantime because this one is still
// in flight.
//
// Best-effort by design: the returned string is always an error message for
// the caller, and the tab is steered to about:blank whether or not the
// stranded-location read succeeds. When that read DOES succeed and the
// location is a URL the SSRF checker rejects, the message says so precisely
// instead of leaving the operator with a generic timeout. If the abandon
// navigation ITSELF fails the message says so too: at that point the tab may
// genuinely still hold the target's content, and the caller is the only one
// left who can decide not to read it.
func abandonTabAfterFailedLoad(
	ctx context.Context,
	mgr *BrowserManager,
	sessionCtx context.Context,
	toolName string,
	rawURL string,
	hops *redirectHopRecorder,
	loadErr error,
) string {
	// Budget 1 of 2 -- diagnostic only. Scoped and released here so that a
	// read which burns its whole budget cannot touch budget 2.
	readCtx, cancelRead := context.WithTimeout(sessionCtx, tabAbandonTimeout)
	var stranded string
	readErr := mgr.runAbandonCDP(readCtx, chromedp.Location(&stranded))
	cancelRead()
	if readErr != nil {
		logger.WarnCF("browser", "could not read the stranded location after a failed load", map[string]any{
			"tool":          toolName,
			"requested_url": rawURL,
			"error":         readErr.Error(),
		})
	}

	// Budget 2 of 2 -- the security-critical step, on its own fresh deadline.
	// Unconditional: even when the location is unreadable (or reads as the
	// pre-validated rawURL), a half-loaded page must not remain addressable
	// by the next tool call.
	navCtx, cancelNav := context.WithTimeout(sessionCtx, tabAbandonTimeout)
	defer cancelNav()
	abandonErr := mgr.runAbandonCDP(navCtx, chromedp.Navigate("about:blank"))
	if abandonErr != nil {
		// ERROR, not WARN: this is the one outcome in which the SSRF window
		// this whole helper closes is still open.
		logger.ErrorCF("browser", "could not steer the tab to about:blank after a failed load", map[string]any{
			"tool":          toolName,
			"requested_url": rawURL,
			"stranded_url":  stranded,
			"error":         abandonErr.Error(),
		})
	}

	// Appended to every message below when the abandon navigation did not
	// land, so the failure is visible to the agent reading the result and not
	// only in the operator's log.
	var stillParked string
	if abandonErr != nil {
		stillParked = fmt.Sprintf(
			" (WARNING: the tab could NOT be steered away from the page and may still hold its content: %s)",
			abandonErr,
		)
	}

	// Prefer the OBSERVED redirect chain over the stranded location. When a
	// redirect target merely HANGS, Chrome never commits the document, so
	// Location still reports the previous page and cannot name the target at
	// all -- exactly the macOS link-local case that exposed this path. The
	// hop recorder saw the URL when the request was issued.
	for _, hop := range hops.urls() {
		if hop == rawURL {
			continue
		}
		if verr := mgr.ValidateURL(ctx, hop); verr != nil {
			return fmt.Sprintf(
				"%s: redirect from %s landed on blocked URL: %s (the page also failed to load: %s)%s",
				toolName, rawURL, verr, loadErr, stillParked,
			)
		}
	}
	if stranded != "" && stranded != rawURL && stranded != "about:blank" {
		if verr := mgr.ValidateURL(ctx, stranded); verr != nil {
			return fmt.Sprintf(
				"%s: redirect from %s landed on blocked URL: %s (the page also failed to load: %s)%s",
				toolName, rawURL, verr, loadErr, stillParked,
			)
		}
	}
	return fmt.Sprintf("%s: page load failed: %s%s", toolName, loadErr, stillParked)
}

// runAbandonCDP executes one of abandonTabAfterFailedLoad's recovery round
// trips against ctx, which the caller has ALREADY bounded with that step's own
// budget (see abandonTabAfterFailedLoad's "independent budgets" note -- the
// bounding deliberately lives at the call site, one context per step, so no
// two steps can share a deadline).
//
// Routed through m.abandonCDPFn when set, purely so tests can drive the
// wedged-renderer case deterministically; see that field's doc comment.
func (m *BrowserManager) runAbandonCDP(ctx context.Context, actions ...chromedp.Action) error {
	if m == nil {
		return chromedp.Run(ctx, actions...)
	}
	m.mu.Lock()
	fn := m.abandonCDPFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, actions...)
	}
	return chromedp.Run(ctx, actions...)
}

// redirectHopRecorder collects the URLs a navigation actually requested,
// including every redirect hop, as reported by the Network domain.
//
// Why it exists (2026-08-13): the post-redirect SSRF check reads
// chromedp.Location() AFTER the load, which only names the target when Chrome
// COMMITS a document there. A target that hangs (macOS treats link-local
// 169.254.0.0/16 that way, and any slow internal host does it everywhere)
// never commits, so the operator got "page load failed: context deadline
// exceeded" with no hint that an SSRF-relevant redirect was involved. The
// request-time record survives that.
//
// Safe to use even when the Network domain was never enabled: the listener
// simply never fires and urls() returns nil, leaving the pre-existing
// Location-based check as the only signal (the behaviour before this type).
type redirectHopRecorder struct {
	mu   sync.Mutex
	seen []string
}

// watchRedirectHops registers a hop recorder for the lifetime of tabCtx.
// chromedp.ListenTarget's registration is scoped to the context it is given,
// so a per-navigation context (as both callers use) unregisters on its own.
func watchRedirectHops(tabCtx context.Context) *redirectHopRecorder {
	r := &redirectHopRecorder{}
	chromedp.ListenTarget(tabCtx, func(ev any) {
		e, ok := ev.(*network.EventRequestWillBeSent)
		if !ok || e.Type != network.ResourceTypeDocument {
			return
		}
		r.mu.Lock()
		r.seen = append(r.seen, e.Request.URL)
		r.mu.Unlock()
	})
	return r
}

func (r *redirectHopRecorder) urls() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}
