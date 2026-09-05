// Omnipus — the scope declaration `go build` cannot check for you.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// WHY THIS FILE EXISTS AT ALL, given that Scope() is part of the Tool
// interface and a missing method is a compile error.
//
// tools.BaseTool supplies Category() but NOT Scope(). So OMITTING Scope() is
// caught by the compiler — and returning the WRONG VALUE is not. A browser
// tool that returned ScopeGeneral, or the zero value, is denied fail-closed by
// pkg/tools/compositor.go's scope gate BEFORE the policy merge runs. That
// means: the seed is correct, coverage is complete, every policy test is
// green, and the tool refuses every call on every agent with no log line and
// no gap report.
//
// It is the same silent-deny shape a missing policy seed produces, reached by
// a different route, which is why it gets its own assertion rather than being
// left to the policy tests.

package browser

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestBrowserTools_AllSixDeclareScopeCore covers the five tools ADR-075 D2
// adds here plus browser_upload_file, which is implemented and seeded but
// deliberately unregistered (FR-029) — a held tool with the wrong scope would
// ship the defect the day #659 closes and its registration line lands.
//
// (browser_handle_dialog is the sixth tool of the D2 set and belongs to the
// dialog stream; it is asserted alongside its own implementation.)
func TestBrowserTools_AllSixDeclareScopeCore(t *testing.T) {
	for _, tool := range []tools.Tool{
		&SelectOptionTool{},
		&PressKeyTool{},
		&HoverTool{},
		&UploadFileTool{},
		&SnapshotTool{},
	} {
		if got := tool.Scope(); got != tools.ScopeCore {
			t.Errorf("%s.Scope() = %q, want %q. A non-core scope is denied fail-closed by "+
				"compositor.go's scope gate BEFORE the policy merge, so this tool would refuse every "+
				"call on every agent while its seed, its coverage and every policy test stayed green",
				tool.Name(), got, tools.ScopeCore)
		}
	}
}

// TestBrowserTools_AllSixDeclareCategoryBrowser is the positive control for
// the table above. Without it the file would pass on a build where these
// structs were replaced by anything at all that returns ScopeCore.
func TestBrowserTools_AllSixDeclareCategoryBrowser(t *testing.T) {
	want := map[string]bool{
		"browser_select_option": true,
		"browser_press_key":     true,
		"browser_hover":         true,
		"browser_upload_file":   true,
		"browser_snapshot":      true,
	}
	seen := map[string]bool{}
	for _, tool := range []tools.Tool{
		&SelectOptionTool{},
		&PressKeyTool{},
		&HoverTool{},
		&UploadFileTool{},
		&SnapshotTool{},
	} {
		if !want[tool.Name()] {
			t.Errorf("unexpected tool name %q in the D2 table", tool.Name())
			continue
		}
		seen[tool.Name()] = true
		if got := tool.Category(); got != tools.CategoryBrowser {
			t.Errorf("%s.Category() = %q, want %q — it would be filed under the wrong heading in the "+
				"operator's tool picker", tool.Name(), got, tools.CategoryBrowser)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("no tool in the table is named %q", name)
		}
	}
}
