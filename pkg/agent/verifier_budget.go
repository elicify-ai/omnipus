// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// verifier_budget.go — ADR-084 D9's per-adjudication budget: a tool-call
// cap and a byte cap enforced at dispatch (JUDGE-FR-051/FR-052), plus a
// token/cost ceiling tracked with a one-time WARN at 75% of it
// (JUDGE-FR-081). This file supplies the mechanism only; the wave that
// actually dispatches a verifier's tool-using turn (pkg/agent/judge.go,
// outside this file's write-set) constructs a *VerifierBudget from the
// operator's JudgeConfig (pkg/config), registers it for the turn it is
// about to run via RegisterVerifierBudget, and unregisters it in a defer
// once that turn returns.
//
// WHY A TURN-ID-KEYED REGISTRY, NOT A context.Context VALUE. The ADR-084
// judge spec's own "ctx seams (WithReadConfined, the capture handle) follow
// the shipped tools.WithVerifierSessionScope precedent" suggests a
// context.Context value. That is exactly right for the ONE call site this
// wave may edit in pkg/agent/loop.go (the tool-dispatch refusal point
// inside runTurn's turnLoop, where the turn's own ctx is in scope). It is
// NOT usable for the byte-cap side of the accounting, which lives inside
// admitToolResult (pkg/agent/tool_result_admit.go): that function's
// signature — func (al *AgentLoop) admitToolResult(ts *turnState, adm
// toolResultAdmission) admittedToolResult — carries no context.Context, and
// it is called from eleven sites across pkg/agent/loop.go; changing its
// signature to add one would force editing every call site to keep the
// package compiling, which is outside this wave's write-set ("the
// tool-dispatch refusal point ... and nothing else" — see the ADR-084/085/
// 086 joint delivery plan §3, wave E2). turnState.ctx (turn.go) is not a
// substitute either: it is populated only for a delegated sub-turn
// (pkg/agent/subturn.go's spawnSubTurn), not for an ordinary root turn, and
// a verifier adjudication's tool-using turn is dispatched as an ordinary
// turn via runTurn, not as a delegation. turnState.turnID, by contrast, is
// populated on every turn ("<agentID>-turn-<seq>", seq monotonic per agent
// — see the AgentLoop turn constructor) and is readable from both
// admitToolResult and the loop.go dispatch point with no signature change
// and no turn.go edit. This is a deliberate, reported deviation from the
// spec's literal ctx-seam suggestion, not an oversight.
package agent

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// VerifierBudget is one adjudication's tool-call cap, byte cap and token
// ceiling, all checked/updated under one mutex. A nil *VerifierBudget is a
// valid, inert no-op on every method — the zero-cost path for the ordinary
// (non-verifier) turn that is the overwhelming majority of calls into
// admitToolResult and the loop.go dispatch point.
type VerifierBudget struct {
	mu sync.Mutex

	toolCallCap  int   // JUDGE-FR-051, already Effective*-resolved by the caller
	byteCap      int64 // JUDGE-FR-051, already Effective*-resolved by the caller
	tokenCeiling int   // JUDGE-FR-081, already Effective*-resolved by the caller
	warnAt       int64 // JUDGE-FR-081's 75%-of-ceiling mark

	toolCalls    int
	bytes        int64
	tokens       int64
	warnedTokens bool
}

// NewVerifierBudget builds a budget from already-resolved caps — the
// caller passes config.JudgeConfig's EffectiveToolCallCap(),
// EffectiveByteCapBytes(), EffectiveTokenCeiling() and
// JudgeTokenCeilingWarnThreshold(), never the raw config fields, so this
// type never re-implements the default/clamp logic pkg/config already owns.
// A cap of <= 0 disables that cap's enforcement (CheckCap never refuses on
// it) — defensive only, since the config-side Effective* accessors never
// return <= 0 for a JudgeConfig field's default.
func NewVerifierBudget(toolCallCap int, byteCap int64, tokenCeiling int, tokenWarnAt int64) *VerifierBudget {
	return &VerifierBudget{
		toolCallCap:  toolCallCap,
		byteCap:      byteCap,
		tokenCeiling: tokenCeiling,
		warnAt:       tokenWarnAt,
	}
}

// CheckCap reports whether the NEXT tool call may proceed, given every real
// (non-refused) call already recorded via RecordToolCall. When capped is
// true, refusal is JUDGE-FR-052's tool-result content for that call.
// Checked once per tool-call dispatch, before the tool runs — never after —
// so a call made once the cap is already reached never executes at all
// (JUDGE-FR-051: "Enforcement MUST be in the verifier dispatch").
func (vb *VerifierBudget) CheckCap() (refusal string, capped bool) {
	if vb == nil {
		return "", false
	}
	vb.mu.Lock()
	defer vb.mu.Unlock()
	switch {
	case vb.toolCallCap > 0 && vb.toolCalls >= vb.toolCallCap:
		return fmtCapReached("the tool-call cap"), true
	case vb.byteCap > 0 && vb.bytes >= vb.byteCap:
		return fmtCapReached("the tool-result byte cap"), true
	default:
		return "", false
	}
}

func fmtCapReached(which string) string {
	return "Adjudication budget cap reached: " + which + " was reached. No further tool calls will run in this turn — conclude the review with the evidence already gathered."
}

// RecordToolCall accounts one ADMITTED (non-refused) tool call's result
// size toward the byte cap and the call itself toward the tool-call cap.
// Called once per real tool call from admitToolResult — never for the
// synthetic cap-refusal result CheckCap produced, so a string of refusals
// past the cap cannot keep inflating the counters.
func (vb *VerifierBudget) RecordToolCall(resultBytes int) {
	if vb == nil {
		return
	}
	vb.mu.Lock()
	defer vb.mu.Unlock()
	vb.toolCalls++
	if resultBytes > 0 {
		vb.bytes += int64(resultBytes)
	}
}

// RecordTokens accounts one LLM call's prompt+completion tokens toward
// JUDGE-FR-081's cost ceiling and logs a one-time WARN once cumulative usage
// crosses 75% of it. Exposed for the wave that dispatches the verifier's
// LLM calls (pkg/agent/judge.go, outside this wave's write-set) —
// verifier_budget.go supplies the mechanism; wiring the LLM-usage call site
// is that wave's to do.
func (vb *VerifierBudget) RecordTokens(promptTokens, completionTokens int) {
	if vb == nil {
		return
	}
	vb.mu.Lock()
	defer vb.mu.Unlock()
	if promptTokens > 0 {
		vb.tokens += int64(promptTokens)
	}
	if completionTokens > 0 {
		vb.tokens += int64(completionTokens)
	}
	if vb.warnAt > 0 && !vb.warnedTokens && vb.tokens >= vb.warnAt {
		vb.warnedTokens = true
		logger.WarnCF("agent", "verifier adjudication token usage crossed 75% of its cost ceiling (JUDGE-FR-081)",
			map[string]any{
				"tokens_used":   vb.tokens,
				"warn_at":       vb.warnAt,
				"token_ceiling": vb.tokenCeiling,
			})
	}
}

// TokensExceeded reports whether cumulative token usage has reached
// JUDGE-FR-081's ceiling. Exposed for the same wave RecordTokens is —
// wiring a hard stop on top of the WARN is that wave's decision to make
// with the LLM-call site it owns, not this one's.
func (vb *VerifierBudget) TokensExceeded() bool {
	if vb == nil || vb.tokenCeiling <= 0 {
		return false
	}
	vb.mu.Lock()
	defer vb.mu.Unlock()
	return vb.tokens >= int64(vb.tokenCeiling)
}

// --- per-turn registry, keyed by turnState.turnID (see the package doc
// comment above for why turnID rather than a context.Context value) -------

var (
	verifierBudgetsMu sync.Mutex
	verifierBudgets   = map[string]*VerifierBudget{}
)

// RegisterVerifierBudget attaches vb to turnID for the duration of one
// adjudication turn. The caller MUST call UnregisterVerifierBudget(turnID)
// once that turn returns — in a defer set immediately after this call — or
// the entry leaks for the life of the process. turnID collisions cannot
// happen across concurrently live turns: turnState.turnID is
// "<agentID>-turn-<seq>" with seq monotonically increasing per agent.
func RegisterVerifierBudget(turnID string, vb *VerifierBudget) {
	if turnID == "" || vb == nil {
		return
	}
	verifierBudgetsMu.Lock()
	defer verifierBudgetsMu.Unlock()
	verifierBudgets[turnID] = vb
}

// UnregisterVerifierBudget removes turnID's budget, if any. Safe to call
// even when nothing was registered for turnID.
func UnregisterVerifierBudget(turnID string) {
	if turnID == "" {
		return
	}
	verifierBudgetsMu.Lock()
	defer verifierBudgetsMu.Unlock()
	delete(verifierBudgets, turnID)
}

// verifierBudgetForTurn is the shared read both the loop.go dispatch-refusal
// point and admitToolResult use — both are reached with only a *turnState,
// never a context.Context carrying a value (see the package doc comment).
// Returns nil (never ok=false with a non-nil zero value) for an ordinary
// turn that was never registered, and every VerifierBudget method above is
// a nil-safe no-op, so callers may skip an explicit presence check.
func verifierBudgetForTurn(turnID string) *VerifierBudget {
	if turnID == "" {
		return nil
	}
	verifierBudgetsMu.Lock()
	defer verifierBudgetsMu.Unlock()
	return verifierBudgets[turnID]
}
