// Omnipus — Browser tab-management tools (ADR-041 D3)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"context"
	"fmt"

	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- browser_list_tabs (ADR-041 D3) ---

// ListTabsTool reports the current tab set of the shared browser session.
// Read-only — NOT gated by controlledResult (see tools.go's doc comment on
// that function: read-only tools never conflict with a human driving the
// live view).
type ListTabsTool struct {
	tools.BaseTool
	mgr *BrowserManager
}

func (t *ListTabsTool) Name() string                 { return "browser_list_tabs" }
func (t *ListTabsTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ListTabsTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *ListTabsTool) Description() string {
	return "List every open tab in the shared browser session: each tab's index, title, URL, " +
		"and whether it is the currently-active (screencasted) tab. Call this after a click " +
		"that might have opened a new tab (a target=\"_blank\" link or window.open — see " +
		"browser_click's result) or whenever you need to see what's currently open. IMPORTANT: if the " +
		"browser has not been started yet (no browser_* tool has navigated anywhere), this returns " +
		"browser_started=false with an EMPTY tabs list — that means \"no browser running\", not \"a " +
		"running browser with zero tabs\" (a running browser always has at least one tab)."
}

func (t *ListTabsTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListTabsTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	// B-8 fix: distinguish "the browser session doesn't exist yet" (no tool
	// has ever navigated anywhere) from "a running browser with zero tabs" —
	// the latter cannot occur (ADR-041's own invariant), but
	// BrowserManager.ListTabs previously returned the identical empty-list
	// shape for both, so a cold browser looked indistinguishable from a
	// browser genuinely showing no tabs. browser_switch_tab and
	// browser_close_tab both tell the model to "call browser_list_tabs
	// first", making this the model's likely FIRST call, and it deserves an
	// unambiguous answer.
	browserStarted := t.mgr.sessionExists(defaultSessionID)
	tabs, activeIdx, err := t.mgr.ListTabs(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_list_tabs: %s", err))
	}
	return jsonResult(map[string]any{
		"tabs":            tabsToWire(tabs),
		"active_index":    activeIdx,
		"browser_started": browserStarted,
	})
}

// --- browser_switch_tab (ADR-041 D3) ---

// SwitchTabTool makes another tab active. Gated by controlledResult: it
// changes what the live view screencasts and what subsequent interactive
// tools act on, so — like navigate/click/type/evaluate — it defers to a
// human currently holding the live-view control lock rather than fighting
// for the cursor (ADR-038 D6).
type SwitchTabTool struct {
	tools.BaseTool
	mgr *BrowserManager
}

func (t *SwitchTabTool) Name() string                 { return "browser_switch_tab" }
func (t *SwitchTabTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *SwitchTabTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *SwitchTabTool) Description() string {
	return "Switch the active tab in the shared browser session. Subsequent browser_* tool " +
		"calls and the live screencast follow the newly-active tab. Call browser_list_tabs " +
		"first to see valid indices — indices are 0-based (the first tab is index 0). On success, " +
		"returns {\"success\": true, \"active_index\": ..., \"title\": ..., \"url\": ...} describing the " +
		"tab just activated. If a human is currently controlling the browser via the live view, this call " +
		"defers instead of switching — the result is {\"deferred\": true, \"reason\": ...} instead; wait " +
		"for them to release control and retry."
}

func (t *SwitchTabTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"index": map[string]any{
				"type":        "integer",
				"description": "Index of the tab to activate (see browser_list_tabs)",
			},
		},
		"required": []string{"index"},
	}
}

func (t *SwitchTabTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	index, ok := intArg(args, "index")
	if !ok {
		return tools.ErrorResult("browser_switch_tab: 'index' parameter is required")
	}
	if result := controlledResult(t.mgr, t.Name()); result != nil {
		return result
	}

	tab, err := t.mgr.SwitchTab(defaultSessionID, index)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_switch_tab: %s", err))
	}
	return jsonResult(map[string]any{
		"success":      true,
		"active_index": tab.Index,
		"title":        tab.Title,
		"url":          tab.URL,
	})
}

// --- browser_close_tab (ADR-041 D3) ---

// CloseTabTool closes a tab. Gated by controlledResult for the same reason
// as SwitchTabTool — closing the tab a human is currently watching/driving
// would fight for the cursor.
type CloseTabTool struct {
	tools.BaseTool
	mgr *BrowserManager
}

func (t *CloseTabTool) Name() string                 { return "browser_close_tab" }
func (t *CloseTabTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *CloseTabTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *CloseTabTool) Description() string {
	return "Close a tab in the shared browser session. If it was the active tab, a " +
		"neighboring tab becomes active. The last remaining tab is never left closed — a " +
		"fresh blank tab opens in its place instead. Call browser_list_tabs first to see " +
		"valid indices — indices are 0-based (the first tab is index 0). On success, returns " +
		"{\"tabs\": [...], \"active_index\": ...} describing the resulting tab set, the same shape " +
		"browser_list_tabs returns. If a human is currently controlling the browser via the live view, " +
		"this call defers instead of closing the tab — the result is {\"deferred\": true, \"reason\": ...} " +
		"instead; wait for them to release control and retry."
}

func (t *CloseTabTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"index": map[string]any{
				"type":        "integer",
				"description": "Index of the tab to close (see browser_list_tabs)",
			},
		},
		"required": []string{"index"},
	}
}

func (t *CloseTabTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	index, ok := intArg(args, "index")
	if !ok {
		return tools.ErrorResult("browser_close_tab: 'index' parameter is required")
	}
	if result := controlledResult(t.mgr, t.Name()); result != nil {
		return result
	}

	tabs, activeIdx, err := t.mgr.CloseTab(defaultSessionID, index)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_close_tab: %s", err))
	}
	// ADR-043 D7: return the global tab-budget slot this tab held.
	t.mgr.releaseGlobalTab()
	return jsonResult(map[string]any{"tabs": tabsToWire(tabs), "active_index": activeIdx})
}

// --- browser_open_tab ---

// OpenTabTool opens a brand-new tab in the shared browser session (via the
// existing BrowserManager.OpenTab — the same primitive the human "+" button
// drives over the WS, pkg/gateway/browser_ws.go), optionally navigates it to
// a URL, and makes it the active tab. Before this tool existed, the agent's
// ONLY way to end up with a second tab was clicking a target="_blank" link
// (ADR-041 D2 adoption) — there was no way to deliberately OPEN one, e.g. to
// check a second source without losing the current page. Gated by
// controlledResult for the same reason as SwitchTabTool/CloseTabTool: it
// changes what the live view screencasts and what subsequent interactive
// tools act on, so it defers to a human currently holding the live-view
// control lock rather than fighting for the cursor (ADR-038 D6).
type OpenTabTool struct {
	tools.BaseTool
	mgr *BrowserManager
}

func (t *OpenTabTool) Name() string                 { return "browser_open_tab" }
func (t *OpenTabTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *OpenTabTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *OpenTabTool) Description() string {
	return "Open a NEW tab in the shared browser session and make it active — unlike " +
		"browser_navigate, which reuses the CURRENT tab, this always creates an additional one. " +
		"Optionally navigate the new tab to a URL right away. Use this when you need a second tab " +
		"alongside the current one, e.g. to check another source without losing your place on the " +
		"page you already have open. Subject to a maximum concurrent tab budget SHARED across all agents " +
		"using this browser (browser_list_tabs does NOT report this limit — it only lists what's " +
		"currently open); if the budget is reached, this call fails with an explanatory error naming the " +
		"reason — close a tab with browser_close_tab and retry. A provided url is SSRF-checked the same " +
		"as browser_navigate. If a human is currently controlling the browser via the live view, this " +
		"call defers instead of opening a tab — the result is {\"deferred\": true, \"reason\": ...} " +
		"instead; wait for them to release control and retry."
}

func (t *OpenTabTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type": "string",
				"description": "Optional URL to navigate the new tab to (http:// or https:// only). " +
					"If omitted, the new tab opens blank.",
			},
		},
	}
}

func (t *OpenTabTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	rawURL, _ := args["url"].(string)
	if rawURL != "" {
		if err := t.mgr.ValidateURL(ctx, rawURL); err != nil {
			return tools.ErrorResult(err.Error())
		}
	}
	if result := controlledResult(t.mgr, t.Name()); result != nil {
		return result
	}

	// ADR-043 D7: global tab budget across all agents' contexts. Deny a new tab
	// when the shared Chrome is at the cap; the agent can browser_close_tab first.
	// I-1/W3/C1: reserveGlobalTab atomically RESERVES a slot (live tabs + in-flight
	// reservations) under the coordinator lock; if the subsequent OpenTab itself
	// fails, that reservation MUST be returned (releaseGlobalTab) so the budget
	// doesn't grow permanently conservative on every failed open.
	if ok, reason := t.mgr.reserveGlobalTab(); !ok {
		return tools.ErrorResult(
			fmt.Sprintf(
				"browser_open_tab: global tab budget reached (%s) — close a tab with browser_close_tab first",
				reason,
			),
		)
	}

	tab, err := t.mgr.OpenTab(defaultSessionID)
	if err != nil {
		t.mgr.releaseGlobalTab()
		return tools.ErrorResult(fmt.Sprintf("browser_open_tab: %s", err))
	}

	if rawURL == "" {
		return jsonResult(map[string]any{
			"success":      true,
			"active_index": tab.Index,
			"title":        tab.Title,
			"url":          tab.URL,
		})
	}

	// OpenTab already made the new tab active, so Session(default) now
	// resolves ITS context — mirrors NavigateTool.Execute's own
	// navigate-then-verify-redirect flow (tools.go) so a provided url gets
	// the SAME two-stage SSRF gate (initial + post-redirect), not a weaker
	// one-off check.
	sessionCtx, err := t.mgr.Session(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_open_tab: %s", err))
	}
	tabCtx, timeoutCancel := context.WithTimeout(sessionCtx, t.mgr.PageTimeout())
	defer timeoutCancel()

	hops := watchRedirectHops(tabCtx)

	var title string
	if err := chromedp.Run(tabCtx, chromedp.Navigate(rawURL), chromedp.Title(&title)); err != nil {
		// SECURITY: same gap as NavigateTool -- a failed load must not leave
		// the NEW tab parked on the target (see abandonTabAfterFailedLoad).
		return tools.ErrorResult(abandonTabAfterFailedLoad(
			ctx, t.mgr, sessionCtx, "browser_open_tab", rawURL, hops, err,
		))
	}

	var finalURL string
	if err := chromedp.Run(tabCtx, chromedp.Location(&finalURL)); err != nil {
		logger.WarnCF("browser", "browser_open_tab: failed to detect final URL after redirect", map[string]any{
			"requested_url": rawURL,
			"error":         err.Error(),
		})
		finalURL = rawURL
	}

	// Post-redirect SSRF check (mirrors NavigateTool.Execute): Chrome's
	// networking stack follows redirects internally, so a public URL could
	// redirect to a private IP (e.g. 169.254.169.254). Validate the final
	// URL and steer the new tab to about:blank if it lands somewhere blocked.
	if finalURL != rawURL {
		if err := t.mgr.ValidateURL(ctx, finalURL); err != nil {
			_ = chromedp.Run(tabCtx, chromedp.Navigate("about:blank"))
			return tools.ErrorResult(fmt.Sprintf(
				"browser_open_tab: redirect from %s landed on blocked URL: %s", rawURL, err,
			))
		}
	}

	result := map[string]any{
		"success":      true,
		"active_index": tab.Index,
		"title":        title,
		"url":          finalURL,
	}
	if finalURL != rawURL {
		result["redirected_from"] = rawURL
	}
	return jsonResult(result)
}

// intArg extracts an integer parameter from a JSON-decoded args map. JSON
// numbers decode to float64 in Go's map[string]any — mirrors the
// args["limit"].(float64) pattern used throughout pkg/tools (e.g.
// email.go/task.go/web.go).
func intArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key].(float64)
	if !ok {
		return 0, false
	}
	return int(v), true
}

// tabsToWire converts a []Tab snapshot to the []map[string]any shape used by
// both browser_list_tabs and browser_close_tab's jsonResult (SilentResult —
// this is a tool result surfaced to the LLM, not a pkg/api/generated wire
// frame, so a plain map is appropriate here; the generated.BrowserTabsFrame
// shape is built separately in pkg/gateway/browser_ws.go for the WS
// broadcast).
func tabsToWire(tabs []Tab) []map[string]any {
	out := make([]map[string]any, len(tabs))
	for i, tab := range tabs {
		out[i] = map[string]any{
			"index":  tab.Index,
			"title":  tab.Title,
			"url":    tab.URL,
			"active": tab.Active,
		}
	}
	return out
}

// Compile-time interface checks
var (
	_ tools.Tool = (*ListTabsTool)(nil)
	_ tools.Tool = (*SwitchTabTool)(nil)
	_ tools.Tool = (*CloseTabTool)(nil)
	_ tools.Tool = (*OpenTabTool)(nil)
)
