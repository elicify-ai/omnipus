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

// ListTabsTool reports the current tab set of the browser this workspace's agents share.
// Read-only — NOT gated by controlledResult (see tools.go's doc comment on
// that function: read-only tools never conflict with a human driving the
// live view).
type ListTabsTool struct {
	tools.BaseTool
	res ManagerResolver
}

func (t *ListTabsTool) Name() string                 { return "browser_list_tabs" }
func (t *ListTabsTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ListTabsTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *ListTabsTool) Description() string {
	return "List every open tab in the browser this workspace's agents share: each tab's index, title, URL, " +
		"and whether it is the currently-active (screencasted) tab. Call this after a click " +
		"that might have opened a new tab (a target=\"_blank\" link or window.open — see " +
		"browser_click's result) or whenever you need to see what's currently open. " +
		"The \"state\" field says which of three situations you are in, and they are NOT the same: " +
		"\"no_context\" means no browser has been started here yet (no browser_* tool has navigated " +
		"anywhere) — that is \"no browser running\", not \"a running browser with zero tabs\"; " +
		"\"open\" means a running browser with the listed tabs; \"empty\" means a running browser " +
		"that momentarily has no tabs. (browser_started=false is the same thing as state=\"no_context\".) " +
		"\"tabs\" are THIS chat's own tabs and \"operator_tabs\" are the tabs the operator opened on " +
		"this workspace — see \"tab_ownership\" in the result."
}

func (t *ListTabsTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListTabsTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	// TWO questions, and this tool used to answer neither honestly.
	//
	// (1) WHAT IS THERE? `ListTabs` returned the identical empty-list, no-error
	//     shape for "no browsing context here at all" and for "a live context
	//     showing no tabs", so a cold browser was indistinguishable from a
	//     running one with nothing open — and the model was told "no tabs" in a
	//     case where the truthful answer was "I cannot see a browser here".
	//     FR-013 replaces that with the closed three-value TabState.
	//
	// (2) WHOSE ARE THEY? A browser holds one tab set per chat session that has
	//     browsed, plus the operator's own workspace-owned set. Reporting them
	//     merged, or reporting one and calling it "the tabs", is the ownership
	//     confusion ADR-072 §1.1 exists to fix. FR-080 requires the payload to
	//     say which is which, so the answer names ownership rather than leaving
	//     the model to infer it.
	//
	// browser_switch_tab and browser_close_tab both tell the model to "call
	// browser_list_tabs first", making this the model's likely FIRST call, so
	// it is the one place both answers have to be unambiguous.
	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, t.Name())
	if failure != nil {
		return failure
	}
	state, tabs, activeIdx, err := mgr.ListTabsState(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_list_tabs: %s", err))
	}
	out := map[string]any{
		"state":        string(state),
		"tabs":         tabsToWire(tabs),
		"active_index": activeIdx,
		// Retained beside `state` rather than replaced by it: it is an
		// already-shipped key with consumers, and it stays exactly true —
		// `browser_started` is false precisely when `state` is "no_context".
		// `state` is the field to read; this one is the compatible shorthand.
		"browser_started": state != TabStateNoContext,
	}
	if owner.IsWorkspace() {
		// This turn IS addressing the operator's workspace-owned set, so
		// `tabs` already is that set. Reporting it twice under two names would
		// invent a second tab set that does not exist.
		out["tabs_owner"] = "workspace_operator"
		out["tab_ownership"] = "'tabs' are the tabs the operator opened on this workspace; " +
			"every agent on this workspace can see and drive them."
	} else {
		out["tabs_owner"] = "this_chat_session"
		wsState, wsTabs, wsActiveIdx, wsErr := mgr.ListTabsState(sessionKey(key, TabOwnerWorkspace()))
		if wsErr != nil {
			return tools.ErrorResult(fmt.Sprintf("browser_list_tabs: %s", wsErr))
		}
		out["operator_tabs"] = tabsToWire(wsTabs)
		out["operator_tabs_state"] = string(wsState)
		out["operator_tabs_active_index"] = wsActiveIdx
		out["tab_ownership"] = "'tabs' are THIS chat's own tabs — whichever agent the chat is " +
			"currently on sees the same set, and no other chat sees them. 'operator_tabs' are the " +
			"tabs the operator opened on this workspace, which every agent on it can see."
	}
	return jsonResult(out)
}

// --- browser_switch_tab (ADR-041 D3) ---

// SwitchTabTool makes another tab active. Gated by controlledResult: it
// changes what the live view screencasts and what subsequent interactive
// tools act on, so — like navigate/click/type/evaluate — it defers to a
// human currently holding the live-view control lock rather than fighting
// for the cursor (ADR-038 D6).
type SwitchTabTool struct {
	tools.BaseTool
	res ManagerResolver
}

func (t *SwitchTabTool) Name() string                 { return "browser_switch_tab" }
func (t *SwitchTabTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *SwitchTabTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *SwitchTabTool) Description() string {
	return "Switch the active tab in the browser this workspace's agents share. Subsequent browser_* tool " +
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
	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, t.Name())
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

	tab, err := mgr.SwitchTab(sid, index)
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
	res ManagerResolver
}

func (t *CloseTabTool) Name() string                 { return "browser_close_tab" }
func (t *CloseTabTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *CloseTabTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *CloseTabTool) Description() string {
	return "Close a tab in the browser this workspace's agents share. If it was the active tab, a " +
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
	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, t.Name())
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

	tabs, activeIdx, err := mgr.CloseTab(sid, index)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_close_tab: %s", err))
	}
	return jsonResult(map[string]any{"tabs": tabsToWire(tabs), "active_index": activeIdx})
}

// --- browser_open_tab ---

// OpenTabTool opens a brand-new tab in the browser this workspace's agents share (via the
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
	res ManagerResolver
}

func (t *OpenTabTool) Name() string                 { return "browser_open_tab" }
func (t *OpenTabTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *OpenTabTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *OpenTabTool) Description() string {
	return "Open a NEW tab in the browser this workspace's agents share and make it active — unlike " +
		"browser_navigate, which reuses the CURRENT tab, this always creates an additional one. " +
		"Optionally navigate the new tab to a URL right away. Use this when you need a second tab " +
		"alongside the current one, e.g. to check another source without losing your place on the " +
		"page you already have open. If the machine is short of memory this call " +
		"fails with an explanatory error saying so — close a tab with browser_close_tab and retry. " +
		"A provided url is SSRF-checked the same " +
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
	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, t.Name())
	if failure != nil {
		return failure
	}
	if rawURL != "" {
		if err := mgr.ValidateURL(ctx, rawURL); err != nil {
			return tools.ErrorResult(err.Error())
		}
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

	// ADR-072 D1.5a/FR-059: there is no tab budget to reserve any more. Every
	// counter is deleted; the only limit is live memory, and it is enforced one
	// level down inside OpenTab (FR-060) so a refusal names memory rather than
	// a cap an operator would go looking for and not find.
	tab, err := mgr.OpenTab(sid)
	if err != nil {
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

	// OpenTab already made the new tab active, so Session(sid) now
	// resolves ITS context — mirrors NavigateTool.Execute's own
	// navigate-then-verify-redirect flow (tools.go) so a provided url gets
	// the SAME two-stage SSRF gate (initial + post-redirect), not a weaker
	// one-off check.
	sessionCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_open_tab: %s", err))
	}
	tabCtx, timeoutCancel := context.WithTimeout(sessionCtx, mgr.PageTimeout())
	defer timeoutCancel()

	hops := watchRedirectHops(tabCtx)

	var title string
	if err := chromedp.Run(tabCtx, chromedp.Navigate(rawURL), chromedp.Title(&title)); err != nil {
		// SECURITY: same gap as NavigateTool -- a failed load must not leave
		// the NEW tab parked on the target (see abandonTabAfterFailedLoad).
		return tools.ErrorResult(abandonTabAfterFailedLoad(
			ctx, mgr, sessionCtx, "browser_open_tab", rawURL, hops, err,
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
		if err := mgr.ValidateURL(ctx, finalURL); err != nil {
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
