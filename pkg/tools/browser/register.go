package browser

import (
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// RegisterTools registers browser automation tools with the given registry.
// The BrowserManager is shared across all tools and manages the Chromium lifecycle.
// Returns an error if ssrf is nil (SSRF protection is mandatory per SEC-24).
//
// evaluateEnabled is accepted for call-site compatibility but no longer controls
// registration — browser_evaluate is ALWAYS registered. Its live safety floor is
// the per-tool executeEnabled gate inside EvaluateTool.Execute (deny-by-default
// unless cfg.Sandbox.BrowserEvaluateEnabled=true), SEC-04/SEC-06. Operators who
// want the tool to actually execute must set BrowserEvaluateEnabled=true.
//
// The executeEnabled gate below is the one and only thing stopping
// browser_evaluate at runtime (#438; the pkg/policy declarative mirror of this
// intent was removed as dead code, #70 — do not reintroduce it as a substitute
// for this gate).
//
// All tools registered:
//   - browser_navigate  — navigate to a URL (SSRF-checked)
//   - browser_click     — click an element by CSS selector
//   - browser_type      — type text into an input
//   - browser_screenshot — capture a full-page PNG screenshot
//   - browser_get_text  — extract inner text from an element
//   - browser_wait      — wait for an element to appear
//   - browser_evaluate  — execute JS (policy-gated deny-by-default, SEC-04)
func RegisterTools(
	registry *tools.ToolRegistry,
	cfg BrowserConfig,
	ssrf *security.SSRFChecker,
	evaluateEnabled bool,
) (*BrowserManager, error) {
	mgr, err := NewBrowserManager(cfg, ssrf)
	if err != nil {
		return nil, err
	}

	registry.Register(&NavigateTool{mgr: mgr})
	registry.Register(&ClickTool{mgr: mgr})
	registry.Register(&TypeTool{mgr: mgr})
	registry.Register(&ScreenshotTool{mgr: mgr})
	registry.Register(&GetTextTool{mgr: mgr})
	registry.Register(&WaitTool{mgr: mgr})
	// browser_evaluate is always registered so the LLM sees it; the evaluateEnabled
	// flag is forwarded to the tool's Execute method, which is the SOLE live gate
	// (deny-by-default unless the operator opts in) — see the doc comment above.
	registry.Register(&EvaluateTool{mgr: mgr, executeEnabled: evaluateEnabled})

	return mgr, nil
}
