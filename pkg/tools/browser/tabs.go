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

// ListTabsTool reports the current tab set of this workspace's browser.
// Read-only — NOT gated by controlledResult (see tools.go's doc comment on
// that function: read-only tools never conflict with a human driving the
// live view).
type ListTabsTool struct {
	tools.BaseTool
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res ManagerResolver
}

func (t *ListTabsTool) Name() string                 { return "browser_list_tabs" }
func (t *ListTabsTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ListTabsTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *ListTabsTool) Description() string {
	return "List every open tab in this workspace's browser: each tab's index, title, URL, " +
		"and whether it is the currently-active (screencasted) tab. Call this after a click " +
		"that might have opened a new tab (a target=\"_blank\" link or window.open — see " +
		"browser_click's result) or whenever you need to see what's currently open. " +
		"The \"state\" field says which of three situations you are in, and they are NOT the same: " +
		"\"no_context\" means no browser has been started here yet (no browser_* tool has navigated " +
		"anywhere) — that is \"no browser running\", not \"a running browser with zero tabs\"; " +
		"\"open\" means a running browser with the listed tabs; \"empty\" means a running browser " +
		"that momentarily has no tabs. (browser_started=false is the same thing as state=\"no_context\".) " +
		"\"tabs\" are THIS chat's own tabs and \"operator_tabs\" are the tabs the operator opened on " +
		"this workspace — see \"tab_ownership\" in the result. Indices run continuously across both " +
		"lists, so any index shown can be passed straight to browser_switch_tab or browser_close_tab; " +
		"switching to one of the operator's tabs is how you take over browsing they started. " +
		"\"focused_tab_set\" says which of the two your next browser_* call will act on. " +
		"Each workspace has its own browser, with its own logins; you cannot see or use another workspace's."
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
	//     confusion ADR-075 §1.1 exists to fix. FR-080 requires the payload to
	//     say which is which, so the answer names ownership rather than leaving
	//     the model to infer it.
	//
	// browser_switch_tab and browser_close_tab both tell the model to "call
	// browser_list_tabs first", making this the model's likely FIRST call, so
	// it is the one place both answers have to be unambiguous.
	mgr, key, home, owner, _, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// FR-051: this call is now IN FLIGHT against the workspace's browser, and
	// stays so until Execute returns. The pool reads this before evicting or
	// idle-closing, so that killing a Chrome never turns a running call into
	// an inexplicable error inside somebody's turn. Every browser tool
	// increments it — read-only ones too, because a screenshot that returns
	// "connection lost" mid-turn is no less confusing for having been
	// read-only. The defer is what makes a panicking or cancelled call
	// release; a leaked count is a browser that can never be reclaimed.
	defer mgr.EnterCall()()
	// The LISTING is always of the turn's OWN set plus the operator's, never of
	// whichever set the turn happens to be driving right now. A turn that has
	// taken the operator's tabs over must still be able to see — and switch
	// back to — its own; reporting only the focused set would make its own
	// tabs unreachable by index and hide them entirely.
	sid := sessionKey(key, home)
	state, tabs, activeIdx, err := mgr.ListTabsState(sid)
	if err != nil {
		// FR-013: "every other browser tool" is not scoped to tools.go — a
		// wedged tab times these out too.
		if routed, ok := dialogAwareTimeout(mgr, sid, "browser_list_tabs", err); ok {
			return tools.ErrorResult(routed.Error())
		}
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
	// `focused_tab_set` answers the question every OTHER browser tool's result
	// now depends on: which of the two sets does my next browser_click,
	// browser_type or browser_navigate act on? It is a description of where
	// this chat's cursor is, not a claim that anything was acquired — the
	// operator's tabs stay workspace-owned whoever is driving them (FR-070).
	out["focused_tab_set"] = tabSetLabel(owner)
	if home.IsWorkspace() {
		// This turn IS the operator's own, so `tabs` already is that set.
		// Reporting it twice under two names would invent a second tab set
		// that does not exist.
		out["tabs_owner"] = "workspace_operator"
		out["tab_ownership"] = "'tabs' are the tabs the operator opened on this workspace; " +
			"every agent on this workspace can see and drive them."
	} else {
		out["tabs_owner"] = "this_chat_session"
		wsState, wsTabs, wsActiveIdx, wsErr := mgr.ListTabsState(sessionKey(key, TabOwnerWorkspace()))
		if wsErr != nil {
			return tools.ErrorResult(fmt.Sprintf("browser_list_tabs: %s", wsErr))
		}
		// ONE index space, and this is the line that makes the listing honest.
		// The operator's tabs used to be numbered 0..m-1 in their own private
		// space, overlapping this chat's own 0..n-1 — so an agent that read
		// this payload, picked an operator tab and passed its index to
		// browser_switch_tab landed on a DIFFERENT tab of its own, and could
		// report success. The operator's tabs are numbered after this chat's,
		// so every index shown here is an index the tools accept.
		base := len(tabs)
		out["operator_tabs"] = tabsToWireFrom(wsTabs, base)
		out["operator_tabs_state"] = string(wsState)
		if len(wsTabs) == 0 {
			// No tab, so no index. Offsetting a meaningless 0 into `base`
			// would name a tab in THIS chat's set.
			out["operator_tabs_active_index"] = -1
		} else {
			out["operator_tabs_active_index"] = wsActiveIdx + base
		}
		out["tab_ownership"] = "'tabs' are THIS chat's own tabs — whichever agent the chat is " +
			"currently on sees the same set, and no other chat sees them. 'operator_tabs' are the " +
			"tabs the operator opened on this workspace, which every agent on it can see AND drive. " +
			"Indices run continuously across both lists, so you pass the index shown here straight " +
			"to browser_switch_tab or browser_close_tab. Switching to one of the operator's tabs " +
			"takes over their browsing: your following browser_* calls act on that tab until you " +
			"switch back to one of your own. If the operator is driving the browser right now, " +
			"those calls defer instead — wait and retry."
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
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res ManagerResolver
}

func (t *SwitchTabTool) Name() string                 { return "browser_switch_tab" }
func (t *SwitchTabTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *SwitchTabTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *SwitchTabTool) Description() string {
	return "Switch the active tab in this workspace's browser. Subsequent browser_* tool " +
		"calls and the live screencast follow the newly-active tab. Call browser_list_tabs " +
		"first to see valid indices — indices are 0-based (the first tab is index 0) and they run " +
		"continuously across BOTH lists browser_list_tabs returns: your own chat's \"tabs\" first, " +
		"then the operator's \"operator_tabs\". Switching to one of the operator's tabs is how you " +
		"take over browsing the person started — your following browser_* calls act on that tab " +
		"until you switch back to one of your own. On success, " +
		"returns {\"success\": true, \"active_index\": ..., \"tab_set\": ..., \"title\": ..., \"url\": ...}; " +
		"\"tab_set\" is \"this_chat_session\" or \"workspace_operator\" and says which set you just " +
		"landed on — check it if you meant to take over. " +
		"If a human is currently controlling the browser via the live view, this call " +
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
	mgr, key, home, _, _, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// FR-051: this call is now IN FLIGHT against the workspace's browser, and
	// stays so until Execute returns. The pool reads this before evicting or
	// idle-closing, so that killing a Chrome never turns a running call into
	// an inexplicable error inside somebody's turn. Every browser tool
	// increments it — read-only ones too, because a screenshot that returns
	// "connection lost" mid-turn is no less confusing for having been
	// read-only. The defer is what makes a panicking or cancelled call
	// release; a leaked count is a browser that can never be reclaimed.
	defer mgr.EnterCall()()
	// §14.2 rule 1 step 1 — OWNERSHIP FIRST, and for this tool ownership is
	// what the index says it is. `index` names a tab in the one space
	// browser_list_tabs renders, which spans this chat's own tabs AND the
	// operator's workspace-owned ones; resolving it here is how an agent
	// acquires the operator's tab, by naming it (FR-070, implicit
	// acquisition). The resolved owner is then what BOTH gates below run
	// against — asking either of them about the turn's own set while acting on
	// the operator's is the silent takeover §0.7 exists to prevent.
	owner, local, sid, err := resolveTabIndex(mgr, key, home, index)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_switch_tab: %s", err))
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
	t.recordBrowserAction(ctx, key, owner, t.Name(), hostOfTabAt(mgr, sid, local))

	tab, err := mgr.SwitchTab(sid, local)
	if err != nil {
		if routed, ok := dialogAwareTimeout(mgr, sid, "browser_switch_tab", err); ok {
			return tools.ErrorResult(routed.Error())
		}
		return tools.ErrorResult(fmt.Sprintf("browser_switch_tab: %s", err))
	}
	// The switch SUCCEEDED, so this turn is now driving that set — which is
	// what browser_switch_tab's description has always promised ("subsequent
	// browser_* tool calls follow the newly-active tab") and what makes
	// "take over the operator's browsing" hold past this one call. Recorded
	// after the action, never before: acquisition is by acting, so a call that
	// failed acquired nothing.
	mgr.focusTabSet(home, owner)
	return jsonResult(map[string]any{
		"success":      true,
		"active_index": tab.Index + mergedIndexBase(mgr, key, home, owner),
		"tab_set":      tabSetLabel(owner),
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
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res ManagerResolver
}

func (t *CloseTabTool) Name() string                 { return "browser_close_tab" }
func (t *CloseTabTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *CloseTabTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *CloseTabTool) Description() string {
	return "Close a tab in this workspace's browser. If it was the active tab, a " +
		"neighboring tab becomes active. The last remaining tab is never left closed — a " +
		"fresh blank tab opens in its place instead. Call browser_list_tabs first to see " +
		"valid indices — indices are 0-based (the first tab is index 0) and run continuously across " +
		"both lists browser_list_tabs returns, so an index from \"operator_tabs\" closes one of the " +
		"operator's tabs. Be sure that is what you mean. On success, returns " +
		"{\"tabs\": [...], \"active_index\": ..., \"tab_set\": ...} describing the resulting tab set and " +
		"which set it was. If a human is currently controlling the browser via the live view, " +
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
	mgr, key, home, _, _, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// FR-051: this call is now IN FLIGHT against the workspace's browser, and
	// stays so until Execute returns. The pool reads this before evicting or
	// idle-closing, so that killing a Chrome never turns a running call into
	// an inexplicable error inside somebody's turn. Every browser tool
	// increments it — read-only ones too, because a screenshot that returns
	// "connection lost" mid-turn is no less confusing for having been
	// read-only. The defer is what makes a panicking or cancelled call
	// release; a leaked count is a browser that can never be reclaimed.
	defer mgr.EnterCall()()
	// §14.2 rule 1 step 1 — ownership first, resolved from the index, exactly
	// as browser_switch_tab does. Closing the operator's tab IS acting on it
	// (§12 A25 as resolved by D1.9c), so it acquires implicitly and passes
	// through the same two gates against the same resolved owner.
	owner, local, sid, err := resolveTabIndex(mgr, key, home, index)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_close_tab: %s", err))
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
	t.recordBrowserAction(ctx, key, owner, t.Name(), hostOfTabAt(mgr, sid, local))

	tabs, activeIdx, err := mgr.CloseTab(sid, local)
	if err != nil {
		if routed, ok := dialogAwareTimeout(mgr, sid, "browser_close_tab", err); ok {
			return tools.ErrorResult(routed.Error())
		}
		return tools.ErrorResult(fmt.Sprintf("browser_close_tab: %s", err))
	}
	base := mergedIndexBase(mgr, key, home, owner)
	return jsonResult(map[string]any{
		"tabs":         tabsToWireFrom(tabs, base),
		"active_index": activeIdx + base,
		"tab_set":      tabSetLabel(owner),
	})
}

// --- browser_open_tab ---

// OpenTabTool opens a brand-new tab in this workspace's browser (via the
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
	// browserAudit is FR-027's audit sink, populated by the tool registry
	// through the auditLoggerAware contract (pkg/tools/registry.go) — no
	// RegisterTools parameter, no caller change. See audit.go.
	browserAudit
	res ManagerResolver
}

func (t *OpenTabTool) Name() string                 { return "browser_open_tab" }
func (t *OpenTabTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *OpenTabTool) Category() tools.ToolCategory { return tools.CategoryBrowser }
func (t *OpenTabTool) Description() string {
	return "Open a NEW tab in this workspace's browser and make it active — unlike " +
		"browser_navigate, which reuses the CURRENT tab, this always creates an additional one. " +
		"Optionally navigate the new tab to a URL right away. Use this when you need a second tab " +
		"alongside the current one, e.g. to check another source without losing your place on the " +
		"page you already have open. If the machine is short of memory this call " +
		"fails with an explanatory error saying so — close a tab with browser_close_tab and retry. " +
		"A provided url is SSRF-checked the same " +
		"as browser_navigate. If a human is currently controlling the browser via the live view, this " +
		"call defers instead of opening a tab — the result is {\"deferred\": true, \"reason\": ...} " +
		"instead; wait for them to release control and retry. " +
		"Each workspace has its own browser, with its own logins; you cannot see or use another workspace's."
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
	mgr, key, home, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// FR-051: this call is now IN FLIGHT against the workspace's browser, and
	// stays so until Execute returns. The pool reads this before evicting or
	// idle-closing, so that killing a Chrome never turns a running call into
	// an inexplicable error inside somebody's turn. Every browser tool
	// increments it — read-only ones too, because a screenshot that returns
	// "connection lost" mid-turn is no less confusing for having been
	// read-only. The defer is what makes a panicking or cancelled call
	// release; a leaked count is a browser that can never be reclaimed.
	defer mgr.EnterCall()()
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

	// FR-027, per action. Recorded AFTER both gates (a deferred call never
	// acted, so it is not an action) and BEFORE the CDP work (the trail must
	// keep the attempt even when the action panics, times out or is
	// cancelled). See recordBrowserAction's doc comment for the ordering
	// contract and targetHostForTool for how "target host" is derived.
	t.recordBrowserAction(ctx, key, owner, t.Name(), targetHostForTool(rawURL))

	// ADR-075 D1.5a/FR-059: there is no tab budget to reserve any more. Every
	// counter is deleted; the only limit is live memory, and it is enforced one
	// level down inside OpenTab (FR-060) so a refusal names memory rather than
	// a cap an operator would go looking for and not find.
	tab, err := mgr.OpenTab(sid)
	if err != nil {
		if routed, ok := dialogAwareTimeout(mgr, sid, "browser_open_tab", err); ok {
			return tools.ErrorResult(routed.Error())
		}
		return tools.ErrorResult(fmt.Sprintf("browser_open_tab: %s", err))
	}

	// The new tab's index is reported in the ONE space browser_list_tabs
	// renders, so the number handed back is the number a later
	// browser_switch_tab/browser_close_tab accepts. base is 0 unless this turn
	// has taken the operator's tabs over, so the ordinary payload is
	// unchanged.
	base := mergedIndexBase(mgr, key, home, owner)
	if rawURL == "" {
		return jsonResult(map[string]any{
			"success":      true,
			"active_index": tab.Index + base,
			"tab_set":      tabSetLabel(owner),
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
		"active_index": tab.Index + base,
		"tab_set":      tabSetLabel(owner),
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
func tabsToWire(tabs []Tab) []map[string]any { return tabsToWireFrom(tabs, 0) }

// tabsToWireFrom is tabsToWire with the rendered indices shifted by base — the
// offset a second tab set occupies in the ONE index space (see
// mergedIndexBase). base is 0 for every turn that has not taken the operator's
// tabs over, so the ordinary payload is byte-identical to what it always was.
func tabsToWireFrom(tabs []Tab, base int) []map[string]any {
	out := make([]map[string]any, len(tabs))
	for i, tab := range tabs {
		out[i] = map[string]any{
			"index":  tab.Index + base,
			"title":  tab.Title,
			"url":    tab.URL,
			"active": tab.Active,
		}
	}
	return out
}

// --- the ONE index space (ADR-075 D1.9b ruling 1 / FR-070) ------------------
//
// A turn can see two tab sets: its own chat session's, and the operator's
// workspace-owned one. Both used to be numbered from 0 in their own private
// space, and browser_list_tabs rendered them that way — so "index 2" named two
// different tabs, and every index-taking tool resolved it against the session's
// set only. An agent that picked an operator tab out of the listing landed on a
// tab of its own, or on nothing, and could report success either way.
//
// So the two sets share ONE index space, in the order the listing renders them:
// the turn's own tabs first (0..n-1), then the operator's (n..n+m-1). An index
// the listing showed is an index a tool accepts, which is the whole property.
//
// The space is only as stable as a single-set space ever was — closing a tab
// renumbers everything after it, which is why every one of these tools'
// descriptions has always said to call browser_list_tabs first.

// mergedIndexBase returns how many indices precede `owner`'s own tab set in the
// index space rendered for a turn whose home set is `home`.
//
// Zero for the ordinary case (a turn addressing its own tabs) and zero for a
// turn that IS the operator, so the arithmetic disappears entirely unless a
// take-over is actually in progress.
func mergedIndexBase(mgr *BrowserManager, key BrowsingKey, home, owner TabOwner) int {
	if owner == home || home.IsWorkspace() || !owner.IsWorkspace() {
		return 0
	}
	_, tabs, _, err := mgr.ListTabsState(sessionKey(key, home))
	if err != nil {
		return 0
	}
	return len(tabs)
}

// resolveTabIndex maps an index from that one space onto the tab set that owns
// it, returning the owner, the index WITHIN that owner's set, and the
// manager-level session key naming it.
//
// This is where an agent acquires the operator's tab: by naming it. There is no
// verb, no parameter and no ownership change (FR-070) — the index the listing
// showed resolves to the set that holds it, and the call proceeds against that
// set through both gates (§14.2 rule 1: ownership, then controlledResult, then
// the lease).
func resolveTabIndex(
	mgr *BrowserManager, key BrowsingKey, home TabOwner, index int,
) (owner TabOwner, local int, sid string, err error) {
	homeSid := sessionKey(key, home)
	_, homeTabs, _, err := mgr.ListTabsState(homeSid)
	if err != nil {
		return TabOwner{}, 0, "", err
	}
	if index < 0 {
		return TabOwner{}, 0, "", fmt.Errorf("tab index %d is out of range", index)
	}
	if index < len(homeTabs) {
		return home, index, homeSid, nil
	}
	// A turn that IS the operator sees exactly one set, so there is nothing
	// after it — reporting a second one would invent a tab set.
	if home.IsWorkspace() {
		return TabOwner{}, 0, "", fmt.Errorf(
			"tab index %d is out of range: %d tab(s) are open", index, len(homeTabs))
	}
	wsOwner := TabOwnerWorkspace()
	wsSid := sessionKey(key, wsOwner)
	_, wsTabs, _, err := mgr.ListTabsState(wsSid)
	if err != nil {
		return TabOwner{}, 0, "", err
	}
	if index-len(homeTabs) < len(wsTabs) {
		return wsOwner, index - len(homeTabs), wsSid, nil
	}
	return TabOwner{}, 0, "", fmt.Errorf(
		"tab index %d is out of range: this chat has %d tab(s) and the operator has %d "+
			"(call browser_list_tabs for the current indices)",
		index, len(homeTabs), len(wsTabs))
}

// tabSetLabel is the model-facing name of a tab set. It is descriptive of WHICH
// SET a call acted on, and is deliberately not a report that anything was
// acquired or transferred — there is no such state to report (FR-070). It
// exists because a call that quietly lands on a different tab than the agent
// meant and answers a bare {"success": true} is the silent-wrong-action class
// this project treats as its worst failure.
func tabSetLabel(owner TabOwner) string {
	if owner.IsWorkspace() {
		return "workspace_operator"
	}
	return "this_chat_session"
}

// Compile-time interface checks
var (
	_ tools.Tool = (*ListTabsTool)(nil)
	_ tools.Tool = (*SwitchTabTool)(nil)
	_ tools.Tool = (*CloseTabTool)(nil)
	_ tools.Tool = (*OpenTabTool)(nil)
)
