package browser

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ManagerResolver hands a tool the BrowserManager for the turn it is executing
// in. Implemented in pkg/agent over ResolveBrowsingKey + BrowserManagerForKey;
// faked in pkg/tools/browser tests. The interface lives in pkg/tools/browser
// and is implemented in pkg/agent because the import direction forbids the
// reverse — pkg/tools/browser cannot import pkg/agent.
type ManagerResolver interface {
	// ManagerFor resolves this turn's browser, launching it on first use.
	// Returns ErrNoBrowsingContext, ErrNoTabOwner, or a launch error. It NEVER
	// returns "pool full" — there is no pool cap at all (D1.5a, §0.6).
	//
	// It also returns the TabOwner this turn addresses:
	// TabOwnerSession(tools.ToolTranscriptSessionID(ctx)) for every ordinary
	// tool call. When the turn carries no transcript session id this returns
	// ErrNoTabOwner — it does NOT fall back to the workspace-owned set, which
	// would let a transcript-less turn drive the operator's tabs.
	//
	// A caller that must reach the operator's shared tab set supplies a
	// resolver that answers TabOwnerWorkspace(); acquisition is IMPLICIT and
	// has no tool-facing surface (FR-070).
	ManagerFor(ctx context.Context) (*BrowserManager, BrowsingKey, TabOwner, error)
}

// resolveTurn is the one line every browser tool's Execute starts with. It
// returns the manager, the manager-level session key the tools address (the
// (BrowsingKey, TabOwner) pair rendered, FR-002b/FR-080), and a ready-made
// error result when the turn has no browser or no tabs of its own.
//
// The error is RETURNED, never swallowed into a shared browser or the
// operator's tab set (FR-008, FR-080) — that swallowing is the whole defect
// ADR-072 exists to fix.
func resolveTurn(
	ctx context.Context, res ManagerResolver, toolName string,
) (mgr *BrowserManager, key BrowsingKey, owner TabOwner, sid string, failure *tools.ToolResult) {
	if res == nil {
		return nil, BrowsingKey{}, TabOwner{}, "",
			tools.ErrorResult(toolName + ": browser tools are not wired to a browser resolver")
	}
	mgr, key, owner, err := res.ManagerFor(ctx)
	if err != nil {
		return nil, BrowsingKey{}, TabOwner{}, "",
			tools.ErrorResult(toolName + ": " + err.Error())
	}
	if mgr == nil {
		return nil, BrowsingKey{}, TabOwner{}, "",
			tools.ErrorResult(toolName + ": " + ErrNoBrowsingContext.Error())
	}
	return mgr, key, owner, sessionKey(key, owner), nil
}

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
// RegisterTools registers browser automation tools with the given registry.
//
// The manager is RESOLVED PER CALL, not captured at registration (FR-002a).
// Before ADR-072 this function constructed one BrowserManager and closed every
// tool struct over it, which is what made a tool's browser a property of the
// agent that happened to be registered rather than of the turn being executed.
// No tool struct holds a *BrowserManager after this change — the structural
// test TestRegisterTools_NoBoundManagerField asserts it.
func RegisterTools(
	registry *tools.ToolRegistry,
	res ManagerResolver,
	evaluateEnabled bool,
	agentHome string,
	restrict bool,
) error {
	if registry == nil {
		return errors.New("browser: RegisterTools requires a tool registry")
	}
	if res == nil {
		return errors.New("browser: RegisterTools requires a ManagerResolver")
	}

	// RegisterReplacing, not Register: this function re-runs on EVERY hot
	// reload (registerSharedTools via ReloadProviderAndConfig, any Settings
	// save, UpsertAgentFast), and each re-run must install tools carrying the
	// operator's CURRENT security state — EvaluateTool.executeEnabled and the
	// screenshot workspace confinement.
	// #278 hardened Register to keep the incumbent and DISCARD a same-name
	// newcomer, which is right for an untrusted claim but silently voided
	// this first-party re-wire: disabling browser_evaluate in Settings
	// reported success and left the JS-execution gate open. Regression test:
	// TestRegisterTools_RewireMustApplyNewSecurityState.

	registry.RegisterReplacing(&NavigateTool{res: res})
	registry.RegisterReplacing(&ClickTool{res: res})
	registry.RegisterReplacing(&TypeTool{res: res})
	registry.RegisterReplacing(&ScreenshotTool{res: res, agentHome: agentHome, restrict: restrict})
	registry.RegisterReplacing(&GetTextTool{res: res})
	registry.RegisterReplacing(&WaitTool{res: res})
	// browser_evaluate is always registered so the LLM sees it; the evaluateEnabled
	// flag is forwarded to the tool's Execute method, which is the SOLE live gate
	// (deny-by-default unless the operator opts in) — see the doc comment above.
	registry.RegisterReplacing(&EvaluateTool{res: res, executeEnabled: evaluateEnabled})
	// ADR-041 D3 — tab-management tools.
	registry.RegisterReplacing(&ListTabsTool{res: res})
	registry.RegisterReplacing(&SwitchTabTool{res: res})
	registry.RegisterReplacing(&CloseTabTool{res: res})
	// Opens a NEW tab (the agent-facing counterpart to the human "+" button)
	// — distinct from browser_navigate, which reuses the current tab.
	registry.RegisterReplacing(&OpenTabTool{res: res})

	return nil
}
