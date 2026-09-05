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
	// It also returns the turn's OWN TabOwner — its "home" tab set:
	// TabOwnerSession(tools.ToolTranscriptSessionID(ctx)) for every ordinary
	// tool call. When the turn carries no transcript session id this returns
	// ErrNoTabOwner — it does NOT fall back to the workspace-owned set, which
	// would let a transcript-less turn drive the operator's tabs.
	//
	// This is the turn's HOME set, not necessarily the set the call acts on.
	// An agent reaches the operator's workspace-owned tabs by ACTING on one it
	// was shown by browser_list_tabs (FR-070: implicit acquisition, no tool,
	// no policy entry, no wire field) — resolveTurn resolves that, not this
	// interface, because it is a property of the call rather than of the turn.
	ManagerFor(ctx context.Context) (*BrowserManager, BrowsingKey, TabOwner, error)
}

// resolveTurn is the one line every browser tool's Execute starts with. It
// returns the manager, the turn's OWN tab set (`home`), the tab set this call
// actually addresses (`owner`) and that pair rendered as the manager-level
// session key (FR-002b/FR-080), plus a ready-made error result when the turn
// has no browser or no tabs of its own.
//
// **`home` and `owner` are two different questions and both have to be
// answered here.** `home` is a property of the TURN — the chat session's own
// tab set, which is all the ManagerResolver knows about. `owner` is a property
// of the CALL: a session that has taken over the operator's workspace-owned
// tabs (by acting on one, FR-070) addresses THOSE until it switches back.
// Resolving `owner` on this one path is what makes the take-over hold for
// every tool rather than for whichever tool remembered to ask.
//
// The gap this closes was total: before it, `owner` was always
// TabOwnerSession(...), so (a) no production path could reach the operator's
// tabs at all, while browser_list_tabs listed them as drivable, and (b) the
// human-control lock — which the live panel takes on
// sessionKey(key, TabOwnerWorkspace()) — was asked about a string that can
// never equal it, so it answered "nobody is driving" every single time.
//
// The error is RETURNED, never swallowed into a shared browser or the
// operator's tab set (FR-008, FR-080) — that swallowing is the whole defect
// ADR-075 exists to fix.
func resolveTurn(
	ctx context.Context, res ManagerResolver, ba *browserAudit, toolName string,
) (mgr *BrowserManager, key BrowsingKey, home, owner TabOwner, sid string, failure *tools.ToolResult) {
	scope, failure := resolveTurnScope(ctx, res, ba, toolName)
	return scope.mgr, scope.key, scope.home, scope.owner, scope.sid, failure
}

// turnScope is the whole answer to "what is this call addressing?", in one
// value: the browser, which browser it is, the turn's own tab set, the set this
// call acts on, and that pair rendered as the manager-level session key.
//
// It exists so that resolveTurn and resolveTurnTabSet can each hand back the
// subset their callers use without either of them discarding fields
// positionally — a row of blank identifiers is exactly as unreadable as the
// linter says, and this resolution has five results precisely because
// ownership is no longer a single question.
type turnScope struct {
	mgr   *BrowserManager
	key   BrowsingKey
	home  TabOwner
	owner TabOwner
	sid   string
}

// resolveTurnScope is the resolution itself; resolveTurn and resolveTurnTabSet
// are views on it.
func resolveTurnScope(
	ctx context.Context, res ManagerResolver, ba *browserAudit, toolName string,
) (turnScope, *tools.ToolResult) {
	if res == nil {
		return turnScope{},
			tools.ErrorResult(toolName + ": browser tools are not wired to a browser resolver")
	}
	mgr, key, home, err := res.ManagerFor(ctx)
	if err != nil {
		return turnScope{}, tools.ErrorResult(toolName + ": " + err.Error())
	}
	if mgr == nil {
		return turnScope{}, tools.ErrorResult(toolName + ": " + ErrNoBrowsingContext.Error())
	}
	owner := mgr.focusedTabSet(home)
	// FR-027's instance-creation event. Emitted here, on the ONE path every
	// browser tool starts with, so that a browser first touched by a read-only
	// call is still recorded as having come into existence — the alternative
	// records nothing at all for a workspace whose first browser tool was
	// browser_screenshot. It latches once per manager (markInstanceAudited),
	// so this is a single atomic CAS on the hot path after the first call.
	//
	// It does NOT substitute for the per-call event: D2.11 rejects first-use
	// auditing as an account of what an agent DID, and the two events answer
	// different questions.
	if ba != nil {
		ba.noteBrowserInstance(ctx, mgr, key)
	}
	return turnScope{mgr: mgr, key: key, home: home, owner: owner, sid: sessionKey(key, owner)}, nil
}

// resolveTurnTabSet is resolveTurn for the read-only tools that need only the
// manager and the tab set to read from — browser_screenshot, browser_get_text
// and browser_wait, none of which is gated by controlledResult or the write
// lease (§14.2 rule 3), so none of which has any use for the key or the owner.
//
// It exists so those three call sites do not have to spell three blanks in a
// row, which is both unreadable and the shape a linter flags. The resolution
// itself is identical, including the take-over: a read-only tool reads the set
// the turn is currently acting on, which is the operator's after the turn has
// switched to one of their tabs.
func resolveTurnTabSet(
	ctx context.Context, res ManagerResolver, ba *browserAudit, toolName string,
) (mgr *BrowserManager, sid string, failure *tools.ToolResult) {
	scope, failure := resolveTurnScope(ctx, res, ba, toolName)
	return scope.mgr, scope.sid, failure
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
// Before ADR-075 this function constructed one BrowserManager and closed every
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
	// ADR-075 D2 — the interaction verbs and the accessibility snapshot.
	//
	// EVERY ONE OF THESE LINES LANDS IN THE SAME COMMIT AS ITS POLICY SEED
	// (pkg/coreagent/core.go's allStaticToolNames + per-agent maps and
	// pkg/config/defaults.go's global ceiling). A registered tool with no
	// seeded policy does NOT abort boot: RepairIncompleteToolPolicyCoverage
	// backfills every gap to an explicit deny before validation runs, and
	// compositor.go fails closed to deny anyway. The result is a tool that is
	// registered, listed in the catalog, and refuses every call on every
	// agent — with at most one WARN and, in the ordering where the catalog
	// edit lands first, none at all.
	registry.RegisterReplacing(&SelectOptionTool{res: res})
	registry.RegisterReplacing(&PressKeyTool{res: res})
	registry.RegisterReplacing(&HoverTool{res: res})
	// Read-only (FR-038): browser_snapshot takes neither the human-control
	// gate nor the write lease, so it answers while a human is driving the tab.
	registry.RegisterReplacing(&SnapshotTool{res: res})
	// Recovery verb (FR-035): browser_handle_dialog takes neither the
	// human-control gate nor the write lease either, but for a different
	// reason than the snapshot's. The snapshot is exempt because it only
	// reads; this one is exempt because the fault it repairs is what holds
	// both gates. The turn that raised the dialog is still blocked on CDP and
	// still owns the lease, and a human staring at a wedged tab has no button
	// — so gating this behind either one is a deadlock, not a safety
	// property.
	registry.RegisterReplacing(&HandleDialogTool{res: res})
	// browser_upload_file is DELIBERATELY NOT REGISTERED HERE (FR-029).
	//
	// It is fully implemented (tools_interact.go), fully seeded (allStaticToolNames,
	// the global ceiling at "ask", and every browser-capable agent's override
	// map), and present in BrowserBuiltinMetadata — and it stays out of the
	// registry until issue #659 is closed. #659 is "AutoDenyAsk is not
	// inherited by delegated sub-turns": the tool is seeded `ask`, so on an
	// unattended delegated run it would raise an approval nobody can answer.
	// The code half of #659 has landed (pkg/agent/subturn.go inherits the
	// flag); the issue is still open.
	//
	// It must NOT copy browser_evaluate's shape — registered, then refused
	// inside Execute. An operator reading the tool catalog would see a tool
	// that is present and then discover by calling it that it is not. Absent
	// is honest. TestUploadFile_NotRegistered pins the absence.

	return nil
}
