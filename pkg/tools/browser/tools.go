package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// maxGetTextBytes caps browser_get_text output to prevent enormous DOM dumps.
const maxGetTextBytes = 100 * 1024 // 100KB per spec edge case

// defaultSessionID is the session used by all browser tools. Sequential tool
// calls (navigate → click → get_text) operate on the same Chromium tab.
const defaultSessionID = "default"

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

	return jsonResult(map[string]any{"success": true, "selector": selector})
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
	return "Capture a screenshot of the current page as a JPEG image."
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
	// inline as a media frame.
	dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf)
	return &tools.ToolResult{
		ForLLM:       dataURL,
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

	var text string
	err = chromedp.Run(tabCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Text(selector, &text, chromedp.ByQuery),
	)
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

	err = chromedp.Run(tabCtx, chromedp.WaitVisible(selector, chromedp.ByQuery))
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
// NOTE (#438): pkg/policy.builtinToolPolicies["browser_evaluate"] = deny expresses
// the same deny-by-default intent declaratively, but it is read only by the
// pkg/policy Evaluator.EvaluateTool path, which has no live tool-dispatch caller
// (test-only). It is therefore advisory, not a live gate — the executeEnabled
// check below is what actually stops execution at runtime.
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
	// This is the SOLE live gate (the pkg/policy deny entry is test-only; see the
	// type doc above and #438).
	if !t.executeEnabled {
		return tools.ErrorResult(
			"browser_evaluate: disabled — set sandbox.browser_evaluate_enabled=true in config to enable",
		)
	}

	js, _ := args["js"].(string)
	if js == "" {
		return tools.ErrorResult("browser_evaluate: 'js' parameter is required")
	}

	tabCtx, err := t.mgr.Session(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_evaluate: %s", err))
	}

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, t.mgr.PageTimeout())
	defer timeoutCancel()

	var result any
	err = chromedp.Run(tabCtx, chromedp.Evaluate(js, &result))
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_evaluate: %s", err))
	}

	return jsonResult(map[string]any{"result": result})
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
