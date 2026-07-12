package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// maxGetTextBytes caps browser_get_text output to prevent enormous DOM dumps.
const maxGetTextBytes = 100 * 1024 // 100KB per spec edge case

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

// DefaultSessionID is the session used by all browser tools. Sequential tool
// calls (navigate → click → get_text) operate on the same Chromium tab.
//
// Exported (ADR-038 finding #1) so the gateway's live-view WS handler
// (pkg/gateway/browser_ws.go) can bind the live view to this SAME tab
// instead of a session keyed by the client-supplied (chat) session id. Before
// this fix, browser_attach{session_id: <chat session uuid>} caused
// BrowserManager.Session to lazily create a brand-new, blank tab distinct
// from the one the agent's tools drive — the live view showed a different
// tab than the agent controlled, and "take control" locked a session the
// tools never checked. The client's session_id is still accepted on the wire
// for context/logging (see BrowserAttachFrame), but the gateway must always
// resolve/attach/control DefaultSessionID, never the raw client value.
const DefaultSessionID = "default"

// defaultSessionID is a package-private alias retained so every existing
// call site inside this package (which predates the export) keeps working
// unchanged. New code — inside or outside this package — should prefer
// DefaultSessionID directly.
const defaultSessionID = DefaultSessionID

// --- browser_navigate (US-5) ---

type NavigateTool struct {
	tools.BaseTool
	mgr *BrowserManager
}

func (t *NavigateTool) Name() string                 { return "browser_navigate" }
func (t *NavigateTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *NavigateTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *NavigateTool) Description() string {
	return "Navigate to a URL and return page metadata. Subject to SSRF protection."
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

func (t *NavigateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return tools.ErrorResult("browser_navigate: 'url' parameter is required")
	}
	if result := controlledResult(t.mgr, t.Name()); result != nil {
		return result
	}

	if err := t.mgr.ValidateURL(ctx, rawURL); err != nil {
		return tools.ErrorResult(err.Error())
	}

	tabCtx, err := t.mgr.Session(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_navigate: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, t.mgr.PageTimeout())
	defer timeoutCancel()

	var title string
	err = chromedp.Run(tabCtx,
		chromedp.Navigate(rawURL),
		chromedp.Title(&title),
	)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_navigate: page load failed: %s", err))
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
		if err := t.mgr.ValidateURL(ctx, finalURL); err != nil {
			// Navigate away from the blocked page to prevent data exfiltration
			_ = chromedp.Run(tabCtx, chromedp.Navigate("about:blank"))
			return tools.ErrorResult(fmt.Sprintf(
				"browser_navigate: redirect from %s landed on blocked URL: %s", rawURL, err))
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
	mgr *BrowserManager
}

func (t *ClickTool) Name() string                 { return "browser_click" }
func (t *ClickTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ClickTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *ClickTool) Description() string          { return "Click an element matching a CSS selector." }
func (t *ClickTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{"type": "string", "description": "CSS selector of the element to click"},
		},
		"required": []string{"selector"},
	}
}

func (t *ClickTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	selector, _ := args["selector"].(string)
	if selector == "" {
		return tools.ErrorResult("browser_click: 'selector' parameter is required")
	}
	if result := controlledResult(t.mgr, t.Name()); result != nil {
		return result
	}

	tabCtx, err := t.mgr.Session(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_click: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, t.mgr.PageTimeout())
	defer timeoutCancel()

	err = chromedp.Run(tabCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
	)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_click: element not found or not clickable: %s", err))
	}

	result := map[string]any{"success": true, "selector": selector}

	// ADR-041 D2: a click on a target="_blank" link or an element that calls
	// window.open may have spawned a new browser tab. Reconcile
	// deterministically right here — the guaranteed detection point per the
	// ADR, complementing the best-effort passive Target.targetCreated
	// listener (manager.go's handleTargetEvent) — and report it so the
	// agent knows a redirect happened and it is now on the new tab, instead
	// of continuing to act on the (now-background) opener page. This is
	// what fixes the headline ADR-041 failure: a Cal.com-style booking
	// button that opens its flow in a new tab.
	if outcome, rerr := t.mgr.ReconcileTabs(defaultSessionID); rerr != nil {
		logger.WarnCF("browser", "browser_click: tab reconcile failed", map[string]any{"error": rerr.Error()})
	} else {
		applyReconcileOutcome(result, outcome)
	}

	return jsonResult(result)
}

// applyReconcileOutcome maps a BrowserManager.ReconcileOutcome onto
// browser_click's result map (ADR-041 fix F2 — the ADR headline bug
// re-created: a click that spawns a new tab beyond MaxTabs, or whose CDP
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
		notes = append(notes, fmt.Sprintf("opened and switched to new tab %d: %s", outcome.NewActive.Index, outcome.NewActive.URL))
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
		case tabAdoptReasonMaxTabs:
			notes = append(notes, lead+" the maximum concurrent tabs limit was reached, so it could not "+
				"be adopted. Close a tab with browser_close_tab and retry, or tell the user a tab could "+
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
	mgr *BrowserManager
}

func (t *TypeTool) Name() string                 { return "browser_type" }
func (t *TypeTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *TypeTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *TypeTool) Description() string {
	return "Type text into an input element matching a CSS selector."
}

func (t *TypeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{"type": "string", "description": "CSS selector of the input element"},
			"text":     map[string]any{"type": "string", "description": "Text to type into the element"},
		},
		"required": []string{"selector", "text"},
	}
}

func (t *TypeTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	selector, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	if selector == "" {
		return tools.ErrorResult("browser_type: 'selector' parameter is required")
	}
	if result := controlledResult(t.mgr, t.Name()); result != nil {
		return result
	}

	tabCtx, err := t.mgr.Session(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_type: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, t.mgr.PageTimeout())
	defer timeoutCancel()

	err = chromedp.Run(tabCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, text, chromedp.ByQuery),
	)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_type: %s", err))
	}

	return jsonResult(map[string]any{"success": true})
}

// --- browser_screenshot (US-5) ---

type ScreenshotTool struct {
	tools.BaseTool

	mgr *BrowserManager
}

func (t *ScreenshotTool) Name() string                 { return "browser_screenshot" }
func (t *ScreenshotTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ScreenshotTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *ScreenshotTool) Description() string {
	return "Capture a screenshot of the CURRENT page as a JPEG image, and report its current URL and title. Use this to see what page is open — including a page the user navigated to themselves via the live browser panel (the tab is shared). Do not guess the URL from the visual content; read it from this tool's output."
}

func (t *ScreenshotTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ScreenshotTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	tabCtx, err := t.mgr.Session(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_screenshot: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, t.mgr.PageTimeout())
	defer timeoutCancel()

	// Wait for the page to finish rendering before capturing.
	// chromedp.Navigate waits for DOMContentLoaded, but CSS/images may still
	// be loading. Poll until document.readyState is "complete", then wait an
	// additional 500ms for client-side JS frameworks to finish painting.
	var buf []byte
	var pageURL, pageTitle string
	err = chromedp.Run(tabCtx,
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

	tmpDir := os.TempDir()
	filename := fmt.Sprintf("omnipus-screenshot-%d.jpg", time.Now().UnixMilli())
	path := filepath.Join(tmpDir, filename)
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_screenshot: failed to save: %s", err))
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
	mgr *BrowserManager
}

func (t *GetTextTool) Name() string                 { return "browser_get_text" }
func (t *GetTextTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *GetTextTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *GetTextTool) Description() string {
	return "Get the inner text of an element matching a CSS selector."
}

func (t *GetTextTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{"type": "string", "description": "CSS selector of the element"},
		},
		"required": []string{"selector"},
	}
}

func (t *GetTextTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	selector, _ := args["selector"].(string)
	if selector == "" {
		return tools.ErrorResult("browser_get_text: 'selector' parameter is required")
	}

	tabCtx, err := t.mgr.Session(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_get_text: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, t.mgr.PageTimeout())
	defer timeoutCancel()

	// Wait for the node to exist with a SHORT, dedicated timeout — not the
	// full PageTimeout — and use WaitReady (DOM presence) rather than
	// WaitVisible (rendered/visible). A node like <title> is present but
	// never "visible"; WaitVisible would block for the full PageTimeout on
	// every such selector before failing. See getTextWaitTimeout's doc
	// comment for the observed 30s hang this fixes.
	waitCtx, waitCancel := context.WithTimeout(tabCtx, getTextWaitTimeout)
	err = chromedp.Run(waitCtx, chromedp.WaitReady(selector, chromedp.ByQuery))
	waitCancel()
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_get_text: element not found: %s", err))
	}

	var text string
	err = chromedp.Run(tabCtx, chromedp.Text(selector, &text, chromedp.ByQuery))
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_get_text: element not found: %s", err))
	}

	if len(text) > maxGetTextBytes {
		text = text[:maxGetTextBytes] + "\n[truncated at 100KB]"
	}

	return jsonResult(map[string]any{"text": text})
}

// --- browser_wait (US-5) ---

type WaitTool struct {
	tools.BaseTool
	mgr *BrowserManager
}

func (t *WaitTool) Name() string                 { return "browser_wait" }
func (t *WaitTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *WaitTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *WaitTool) Description() string {
	return "Wait for an element matching a CSS selector to appear in the DOM."
}

func (t *WaitTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{"type": "string", "description": "CSS selector to wait for"},
		},
		"required": []string{"selector"},
	}
}

func (t *WaitTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	selector, _ := args["selector"].(string)
	if selector == "" {
		return tools.ErrorResult("browser_wait: 'selector' parameter is required")
	}

	tabCtx, err := t.mgr.Session(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_wait: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, t.mgr.PageTimeout())
	defer timeoutCancel()

	// Same fail-fast rationale as browser_get_text (see getTextWaitTimeout's
	// doc comment): a selector that never appears would otherwise block for
	// the full PageTimeout (commonly 30s) before failing. Bound the wait with
	// the same short, dedicated timeout so a missing selector fails fast.
	waitCtx, waitCancel := context.WithTimeout(tabCtx, getTextWaitTimeout)
	err = chromedp.Run(waitCtx, chromedp.WaitVisible(selector, chromedp.ByQuery))
	waitCancel()
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_wait: timeout waiting for %q: %s", selector, err))
	}

	return jsonResult(map[string]any{"found": true})
}

// --- browser_evaluate (US-5) ---
// Denied by default in deny-by-default policy mode (SEC-04/SEC-06).

// EvaluateTool executes arbitrary JavaScript in the browser page context.
// It is always registered in the tool catalog so the LLM always sees the tool.
//
// The LIVE enforcement gate is executeEnabled (set at construction from
// cfg.Sandbox.BrowserEvaluateEnabled): Execute returns a deny error unless the
// operator explicitly opted in. This single gate enforces SEC-04 / SEC-06.
//
// (#438, #70): a pkg/policy declarative mirror of this deny-by-default intent
// used to exist but was dead code (no live tool-dispatch caller) and was
// removed — the executeEnabled check below is what actually stops execution
// at runtime, and always was.
type EvaluateTool struct {
	tools.BaseTool
	mgr            *BrowserManager
	executeEnabled bool
}

func (t *EvaluateTool) Name() string                 { return "browser_evaluate" }
func (t *EvaluateTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *EvaluateTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *EvaluateTool) Description() string {
	return "Execute JavaScript in the page context. Denied by default — must be explicitly allowed by policy."
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
	if result := controlledResult(t.mgr, t.Name()); result != nil {
		return result
	}

	tabCtx, err := t.mgr.Session(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_evaluate: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, t.mgr.PageTimeout())
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
func controlledResult(mgr *BrowserManager, toolName string) *tools.ToolResult {
	if !mgr.Live().IsControlled(defaultSessionID) {
		return nil
	}
	return tools.NewToolResult(fmt.Sprintf(
		"%s: deferred — a human is currently controlling this browser via the live view. "+
			"Wait for them to release control before driving the browser further.",
		toolName,
	))
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
