package browser

import (
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// RegisterTools registers browser automation tools with the given registry.
// The BrowserManager is shared across all tools and manages the Chromium lifecycle.
// Returns an error if ssrf is nil (SSRF protection is mandatory per SEC-24).
//
// evaluateEnabled does not control registration — browser_evaluate is ALWAYS
// registered, and always was. What it controls is EXECUTION, via the per-tool
// executeEnabled gate inside EvaluateTool.Execute.
//
// It is sourced from cfg.Sandbox.BrowserEvaluateEnabled, which is now SEEDED
// TRUE (ADR D1.9b ruling 2), so on a standard installation the tool executes
// and WHICH AGENTS may call it is decided by tool policy — Jim holds the only
// agent-level grant. The flag is the operator's installation-wide kill switch,
// not the per-agent control it used to read as.
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
//   - browser_screenshot — capture a full-page JPEG screenshot
//   - browser_get_text  — extract inner text from an element
//   - browser_wait      — wait for an element to appear
//   - browser_evaluate  — execute JS (policy-gated deny-by-default, SEC-04)
//   - browser_list_tabs  — list the open tabs + which is active (ADR-041)
//   - browser_switch_tab — make a tab active; tools + live view follow it (ADR-041)
//   - browser_close_tab  — close a tab; never leaves zero tabs (ADR-041)
//   - browser_open_tab   — open a NEW tab (does not reuse the current one) and optionally navigate it
//
// agentHome is the agent's fixed home directory and restrict maps to
// fspolicy.FSScopeConfined (true) / FSScopeUnrestricted (false) — both are
// threaded through solely for ScreenshotTool's ResolvePath-based write
// (ADR-046 FR-009), mirroring the restrict/agentHome pair every other
// path-taking tool receives from its own constructor.
func RegisterTools(
	registry *tools.ToolRegistry,
	cfg BrowserConfig,
	ssrf *security.SSRFChecker,
	evaluateEnabled bool,
	agentHome string,
	restrict bool,
) (*BrowserManager, error) {
	mgr, err := NewBrowserManager(cfg, ssrf)
	if err != nil {
		return nil, err
	}

	// RegisterReplacing, not Register: this function re-runs on EVERY hot
	// reload (registerSharedTools via ReloadProviderAndConfig, any Settings
	// save, UpsertAgentFast), and each re-run must install tools carrying the
	// operator's CURRENT security state — EvaluateTool.executeEnabled, the
	// screenshot workspace confinement, and the new mgr's SSRFChecker.
	// #278 hardened Register to keep the incumbent and DISCARD a same-name
	// newcomer, which is right for an untrusted claim but silently voided
	// this first-party re-wire: disabling browser_evaluate in Settings
	// reported success and left the JS-execution gate open. Regression test:
	// TestRegisterTools_RewireMustApplyNewSecurityState.

	registry.RegisterReplacing(&NavigateTool{mgr: mgr})
	registry.RegisterReplacing(&ClickTool{mgr: mgr})
	registry.RegisterReplacing(&TypeTool{mgr: mgr})
	registry.RegisterReplacing(&ScreenshotTool{mgr: mgr, agentHome: agentHome, restrict: restrict})
	registry.RegisterReplacing(&GetTextTool{mgr: mgr})
	registry.RegisterReplacing(&WaitTool{mgr: mgr})
	// browser_evaluate is always registered so the LLM sees it; the evaluateEnabled
	// flag is forwarded to the tool's Execute method, which is the SOLE live gate
	// (deny-by-default unless the operator opts in) — see the doc comment above.
	registry.RegisterReplacing(&EvaluateTool{mgr: mgr, executeEnabled: evaluateEnabled})
	// ADR-041 D3 — tab-management tools.
	registry.RegisterReplacing(&ListTabsTool{mgr: mgr})
	registry.RegisterReplacing(&SwitchTabTool{mgr: mgr})
	registry.RegisterReplacing(&CloseTabTool{mgr: mgr})
	// Opens a NEW tab (the agent-facing counterpart to the human "+" button)
	// — distinct from browser_navigate, which reuses the current tab.
	registry.RegisterReplacing(&OpenTabTool{mgr: mgr})

	return mgr, nil
}
