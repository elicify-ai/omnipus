package browser

import (
	"github.com/dapicom-ai/omnipus/pkg/security"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// RegisterTools registers browser automation tools with the given registry.
// The BrowserManager is shared across all tools and manages the Chromium lifecycle.
// Returns an error if ssrf is nil (SSRF protection is mandatory per SEC-24).
//
// evaluateEnabled is accepted for call-site compatibility but no longer controls
// registration — browser.evaluate is ALWAYS registered. Its live safety floor is
// the per-tool executeEnabled gate inside EvaluateTool.Execute (deny-by-default
// unless cfg.Sandbox.BrowserEvaluateEnabled=true), SEC-04/SEC-06. Operators who
// want the tool to actually execute must set BrowserEvaluateEnabled=true.
//
// NOTE (#438): pkg/policy.builtinToolPolicies["browser.evaluate"] = deny is the
// SAME intent expressed declaratively, but that map is consulted only by the
// pkg/policy Evaluator.EvaluateTool path, which has no live tool-dispatch caller
// (test-only). The executeEnabled gate below is therefore the one and only thing
// stopping browser.evaluate at runtime — do not remove it on the assumption the
// policy map covers it.
//
// All tools registered:
//   - browser.navigate  — navigate to a URL (SSRF-checked)
//   - browser.click     — click an element by CSS selector
//   - browser.type      — type text into an input
//   - browser.screenshot — capture a full-page PNG screenshot
//   - browser.get_text  — extract inner text from an element
//   - browser.wait      — wait for an element to appear
//   - browser.evaluate  — execute JS (policy-gated deny-by-default, SEC-04)
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
	// browser.evaluate is always registered; deny is enforced at dispatch by the
	// policy engine (pkg/policy.builtinToolPolicies). The evaluateEnabled flag
	// is forwarded to the tool's Execute method which checks it at invocation
	// time as a second gate (operators must opt in at both layers).
	registry.Register(&EvaluateTool{mgr: mgr, executeEnabled: evaluateEnabled})

	return mgr, nil
}
