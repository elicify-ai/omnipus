// Omnipus — Browser tab-management tools (ADR-041 D3)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"context"
	"fmt"

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
		"browser_click's result) or whenever you need to see what's currently open."
}

func (t *ListTabsTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListTabsTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	tabs, activeIdx, err := t.mgr.ListTabs(defaultSessionID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_list_tabs: %s", err))
	}
	return jsonResult(map[string]any{"tabs": tabsToWire(tabs), "active_index": activeIdx})
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
		"first to see valid indices."
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
		"valid indices."
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
	return jsonResult(map[string]any{"tabs": tabsToWire(tabs), "active_index": activeIdx})
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
)
