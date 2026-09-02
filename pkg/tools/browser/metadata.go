// Omnipus — Browser Builtin Metadata Catalog
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package browser — BrowserBuiltinMetadata returns metadata-only (never executed)
// instances of every browser.* tool so they can be registered in the central
// BuiltinRegistry for /api/v1/tools exposure.
//
// Design invariants (binding — Issue #350, ADR-018 D-A1, same as
// tools.GeneralBuiltinMetadata):
//   - Instances returned here are NEVER Execute()d. The per-agent registry
//     (pkg/agent/loop.go → browser.RegisterTools) remains the sole execution
//     source, with a live ManagerResolver. The central catalog only calls
//     Name(), Description(), and Category().
//   - Instances are constructed with a nil ManagerResolver. This is safe because
//     every browser tool's Name()/Description()/Category() is a static string
//     (no mgr dereference). Execute() is never called on these instances.
//   - browser_evaluate is constructed with executeEnabled=false (the metadata
//     instance reports existence only, never runs; the live gate is the
//     per-agent instance's executeEnabled flag, see #438).
//
// This closes the catalog gap: browser.* tools previously registered only into
// the per-agent ToolRegistry and were therefore absent from GET /api/v1/tools.

package browser

import "github.com/elicify-ai/omnipus/pkg/tools"

// BrowserBuiltinMetadata constructs metadata-only instances of every browser
// builtin tool and returns them as a slice of tools.Tool. Each exposes correct
// Name(), Description(), and Category() (CategoryBrowser) for the central
// BuiltinRegistry.
//
// The returned tools MUST NOT be Execute()d. All instances carry a nil
// ManagerResolver — safe for metadata only.
func BrowserBuiltinMetadata() []tools.Tool {
	return []tools.Tool{
		&NavigateTool{},
		&ClickTool{},
		&TypeTool{},
		&ScreenshotTool{},
		&GetTextTool{},
		&WaitTool{},
		&EvaluateTool{executeEnabled: false},
		// ADR-041 D3 — tab-management tools.
		&ListTabsTool{},
		&SwitchTabTool{},
		&CloseTabTool{},
		&OpenTabTool{},
		// ADR-072 D2 — the interaction verbs and the accessibility snapshot.
		//
		// THESE ENTRIES ARE ATOMIC WITH THE POLICY SEED, not merely adjacent
		// to it: TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog
		// compares this catalog against coreagent's allStaticToolNames, and a
		// registered tool whose policy is unseeded does NOT abort boot — it
		// resolves a silent deny on every agent. See allStaticToolNames'
		// comment in pkg/coreagent/core.go.
		&SelectOptionTool{},
		&PressKeyTool{},
		&HoverTool{},
		&SnapshotTool{},
		// browser_upload_file appears HERE while it is deliberately absent
		// from RegisterTools (FR-029, issue #659). That asymmetry is the
		// point: "held" means unregistered, not unseeded. The name must be in
		// this catalog and in allStaticToolNames or the drift test fails; it
		// must NOT be in the registry or an agent can call a tool whose
		// unattended-approval story is not finished.
		&UploadFileTool{},
	}
}
