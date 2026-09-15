// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// browser_deferral.go — ADR-085's turn-engine seams (BROWSER-FR-013 through
// FR-017, FR-021/FR-022, FR-029/FR-029a). New file, per the joint delivery
// plan's shared-file-chain row for pkg/agent/loop.go and pkg/agent/turn.go:
// wave B123 lands every new symbol these two churn-heavy files need here,
// touching loop.go/turn.go themselves only at the three narrow points their
// own rows name.
//
// Four things live here:
//
//  1. browserControlGatedToolNames — the FR-016a classifier set, built once
//     from browser.ControlGatedToolNames() (action ∪ capture), so the engine
//     can short-circuit a control-gated tool call BEFORE dispatch without
//     importing pkg/tools/browser's unexported rosters and without pkg/tools/
//     browser importing pkg/agent in the reverse direction.
//  2. turnBrowserDeferralLedger + turnState methods — the per-turn deferral
//     count (FR-013/FR-014/FR-017), modelled on tool_denial.go's
//     turnDenialLedger: a fresh ledger per turn, an aggregate counter, no
//     cross-turn state.
//  3. AgentLoop.SetBrowserWheelReleaseHook / the hook's invocation from
//     processMessage (FR-029) — modelled on the existing SetReloadFunc
//     gateway-registered-callback pattern. Split from the ACTION (the
//     gateway's own release-across-every-reachable-tab-set fan-out) because
//     pkg/agent cannot reach pkg/gateway/browser_ws.go's audit/notification
//     machinery, and must not need to.
//  4. WithToolRootChatSessionID stamping — FR-021's tool-context carrier,
//     read by pkg/tools/browser/tools.go::controlledResult for its FR-020
//     two-key coverage check.
package agent

import (
	"context"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// --- FR-016a: the control-gate classifier -----------------------------

// browserControlGatedToolNames is the FR-016a short-circuit set: every
// browser tool name pkg/tools/browser/tools.go::controlledResult can defer
// (action ∪ capture). Built ONCE from the package's own exported view —
// never a fourth hand-written list — so a tool added to browser.go's
// rosters and forgotten here is impossible by construction; a tool removed
// there simply stops appearing.
var browserControlGatedToolNames = buildBrowserControlGatedToolNames()

func buildBrowserControlGatedToolNames() map[string]bool {
	names := browser.ControlGatedToolNames()
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// isBrowserControlGatedTool reports whether toolName is one this turn's
// browser-deferral ledger governs (BROWSER-FR-016a).
func isBrowserControlGatedTool(toolName string) bool {
	return browserControlGatedToolNames[toolName]
}

// browserControlDeferralGate is the tools.ToolDeferral.Gate value
// controlledResult populates (BROWSER-FR-012a) — the ONLY structural
// signal this ledger reads. Never inferred from ForLLM's prose.
const browserControlDeferralGate = "browser_control"

// browserControlGateBound is BROWSER-FR-014's N: three deferred attempts per
// turn before the engine short-circuits every later control-gated call
// itself. See FR-014's justification in the spec: a control deferral is
// 100%-reproducible until a human acts, so a retry cannot succeed — three
// leaves exactly one attempt of slack for a multi-tool sequence (e.g.
// browser_snapshot then browser_click) to surface the state on the tool the
// model actually cares about.
const browserControlGateBound = 3

// browserControlGateBoundReachedNote is appended (BROWSER-FR-015) to the
// THIRD deferral's own ForLLM body — the model is told, on the call that
// exhausts the bound, to state what it is waiting for and stop trying for
// the rest of the turn.
const browserControlGateBoundReachedNote = "\n\nYou have now been deferred by the browser control gate " +
	"three times this turn. Stop attempting any browser action for the rest of this turn: state " +
	"plainly, in your reply, what you are waiting for. The operator resumes your browser driving by " +
	"sending a new message — do not retry."

// browserControlGateExhaustedMessage is the terminal instruction handed to
// the model for every FOURTH-AND-LATER control-gated call this turn
// (BROWSER-FR-016): the engine short-circuits before dispatch — no CDP
// contact, no lease acquisition, no audit action row, no entry into
// pkg/tools/browser at all — and repeats the same instruction rather than
// attempting the call again.
func browserControlGateExhaustedMessage(toolName string) string {
	return toolName + ": deferred — the browser control gate's per-turn attempt bound (3) was " +
		"already reached this turn. Do not retry any browser action; state what you are waiting for " +
		"and continue with other work. The operator resumes your browser driving by sending a new message."
}

// --- FR-013/FR-014/FR-017: the per-turn ledger -------------------------

// turnBrowserDeferralLedger is turnState's ADR-085 per-turn bookkeeping,
// modelled directly on tool_denial.go's turnDenialLedger: a fresh value per
// turn (newTurnState's zero value is exactly "no deferrals yet"), no
// cross-turn or cross-session state. A delegated child turn gets its own
// turnState and therefore its own ledger (FR-017) — there is no shared
// counter to thread through spawnSubTurn.
type turnBrowserDeferralLedger struct {
	used int
}

// recordBrowserControlDeferral records one control-gate deferral this turn
// (BROWSER-FR-013). Returns the new count and whether THIS call is the one
// that just reached BROWSER-FR-014's bound (used == browserControlGateBound
// exactly) — the caller appends FR-015's note to that one call's own
// ForLLM, and only that one.
//
// Takes ts.mu.Lock(), not RLock() — mirrors recordToolDenial's discipline:
// ts.mu is a sync.RWMutex shared by many unrelated turnState fields, and
// this mutates turnBrowserDeferralLedger.
func (ts *turnState) recordBrowserControlDeferral() (used int, justReachedBound bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.browserDeferralLedger.used++
	return ts.browserDeferralLedger.used, ts.browserDeferralLedger.used == browserControlGateBound
}

// browserControlGateExhausted reports whether this turn has already reached
// BROWSER-FR-014's bound, i.e. every LATER control-gated call must
// short-circuit before dispatch (BROWSER-FR-016). A pure read: ts.mu.RLock().
func (ts *turnState) browserControlGateExhausted() bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.browserDeferralLedger.used >= browserControlGateBound
}

// --- FR-021: the root-chat tool-context carrier ------------------------

// browserRootChatSessionID resolves the id pkg/tools/browser/tools.go's
// controlledResult needs for FR-020's second (root-chat panel) coverage
// check: turnState.routingSessionID (ADR-057 FR-011), the id that is
// inherited verbatim through a whole delegation subtree and therefore
// stable for a delegated child driving its own tab set while the operator
// holds the PARENT chat's wheel (FR-023).
//
// The ONE routingSessionID read this file performs is intentionally kept in
// this single small function — see
// routing_session_id_consumer_set_adr057_test.go's u19 classifier, which
// this file's name and this function's name are keyed against.
func browserRootChatSessionID(ts *turnState) string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return string(ts.routingSessionID) // u19:browser-gate
}

// withBrowserRootChatSessionID stamps ctx with FR-021's tool-context carrier
// (tools.WithToolRootChatSessionID) so pkg/tools/browser/tools.go's
// controlledResult can read it without pkg/tools/browser importing pkg/agent
// or the ManagerResolver interface growing a new method (see the spec's
// "why not ManagerResolver.ManagerFor" rejection). A turn with no root chat
// id (a non-chat turn — cron/heartbeat/task) stamps "", which
// controlledResult's own doc comment documents as "skip the second check
// entirely" (FR-020's fail-OPEN case).
func withBrowserRootChatSessionID(ctx context.Context, ts *turnState) context.Context {
	return tools.WithToolRootChatSessionID(ctx, browserRootChatSessionID(ts))
}

// --- FR-029: the gateway-registered release hook -----------------------

// ReleaseActor carries FR-030's attribution fields for the browser-wheel
// release the hook below triggers: who the release is audited as, resolved
// by the caller (pkg/gateway/browser_ws.go's ReleaseBrowserWheelForPrompt)
// from the SAME bus.InboundMessage the loop already has in hand — never
// re-derived here, since pkg/agent has no audit-record shape of its own for
// this and must not invent one.
type ReleaseActor struct {
	// GatewayUserID is the WS-authenticated gateway principal, when known
	// (bus.InboundMessage.GatewayUserID — set ONLY on the webchat WS path).
	// Empty on every channel-originated prompt.
	GatewayUserID string
	// SenderCanonicalID is the platform sender (e.g. "telegram:123"),
	// recorded as the actor when GatewayUserID is empty (FR-030's fallback
	// order). Never stamped as a gateway user.
	SenderCanonicalID string
}

// browserWheelReleaseHookFunc is the shape AgentLoop.SetBrowserWheelReleaseHook
// registers. sessionID is the turn's transcript session id (the chat the
// prompt landed on) — the gateway resolves FR-050's whole reachability set
// (every tab set reachable from this root chat) from there.
type browserWheelReleaseHookFunc func(ctx context.Context, sessionID string, actor ReleaseActor)

// browserWheelReleaseHook is guarded by browserWheelReleaseHookMu: set once
// at gateway wiring time (pkg/gateway/browser_ws.go::newBrowserWSHandler),
// read on every processMessage call. A nil hook is a silent no-op —
// headless/test builds with no gateway never call it, exactly like
// reloadFunc's own nil-guard.
var (
	browserWheelReleaseHookMu sync.RWMutex
	browserWheelReleaseHook   browserWheelReleaseHookFunc
)

// SetBrowserWheelReleaseHook registers fn as the ADR-085 BROWSER-FR-029
// release action, invoked once per operator-originated prompt (see
// processMessage's call site) BEFORE the turn begins. Modelled on the
// existing AgentLoop.SetReloadFunc gateway-registered-callback pattern.
// Passing nil clears the hook.
//
// Package-level rather than a field on *AgentLoop: processMessage does not
// carry an *AgentLoop receiver reference to every call site cheaply, and —
// unlike reloadFunc, which is genuinely per-loop-instance state — there is
// exactly ONE browser-wheel release action per process, registered once at
// gateway wiring time, so a package-level seam (guarded by its own mutex,
// never touching AgentLoop's own internal locks) is the honest shape rather
// than threading a new field through every call.
func (al *AgentLoop) SetBrowserWheelReleaseHook(fn browserWheelReleaseHookFunc) {
	browserWheelReleaseHookMu.Lock()
	defer browserWheelReleaseHookMu.Unlock()
	browserWheelReleaseHook = fn
}

// invokeBrowserWheelReleaseHookIfOperatorPrompt is processMessage's FR-029
// call site: fires the registered release hook exactly once, if and only if
// msg.OperatorPrompt is true (bus.InboundMessage's fail-closed carrier — set
// ONLY at the three operator-originated publish sites: websocket.go,
// sse.go, channels/base.go::HandleMessage) and a hook is registered. Never
// invoked for a question-card resume, a background/async-notifier
// completion, or a goal-loop follow-up re-injection — none of those set
// OperatorPrompt (see bus.InboundMessage's own doc comment).
func invokeBrowserWheelReleaseHookIfOperatorPrompt(ctx context.Context, msg bus.InboundMessage, sessionID string) {
	if !msg.OperatorPrompt {
		return
	}
	browserWheelReleaseHookMu.RLock()
	hook := browserWheelReleaseHook
	browserWheelReleaseHookMu.RUnlock()
	if hook == nil {
		return
	}
	actor := ReleaseActor{
		GatewayUserID:     msg.GatewayUserID,
		SenderCanonicalID: msg.Sender.CanonicalID,
	}
	hook(ctx, sessionID, actor)
}
