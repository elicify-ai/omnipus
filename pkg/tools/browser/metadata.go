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
//     source, with a live *BrowserManager. The central catalog only calls
//     Name(), Description(), and Category().
//   - Instances are constructed with a nil *BrowserManager. This is safe because
//     every browser tool's Name()/Description()/Category() is a static string
//     (no mgr dereference). Execute() is never called on these instances.
//   - browser.evaluate is constructed with executeEnabled=false (the metadata
//     instance reports existence only, never runs; the live gate is the
//     per-agent instance's executeEnabled flag, see #438).
//
// This closes the catalog gap: browser.* tools previously registered only into
// the per-agent ToolRegistry and were therefore absent from GET /api/v1/tools.

package browser

import "github.com/dapicom-ai/omnipus/pkg/tools"

// BrowserBuiltinMetadata constructs metadata-only instances of every browser
// builtin tool and returns them as a slice of tools.Tool. Each exposes correct
// Name(), Description(), and Category() (CategoryBrowser) for the central
// BuiltinRegistry.
//
// The returned tools MUST NOT be Execute()d. All instances carry a nil
// *BrowserManager — safe for metadata only.
func BrowserBuiltinMetadata() []tools.Tool {
	return []tools.Tool{
		&NavigateTool{},
		&ClickTool{},
		&TypeTool{},
		&ScreenshotTool{},
		&GetTextTool{},
		&WaitTool{},
		&EvaluateTool{executeEnabled: false},
	}
}
