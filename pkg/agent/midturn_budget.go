// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// midturn_budget.go — ADR-066 D6 (spec FR-028…FR-033, T066-13): the ONE
// window check, run mid-turn after EVERY admitted tool result (in addition
// to the existing pre-turn site), against the same budget B every other
// site reads (agentContextBudget). Mid-turn the window NEVER cuts — Skip
// does not move (FR-030); the only legal operation is the D5 empty-in-place
// pass (empty_in_place.go), whose floor is the whole last assistant step
// (FR-031, always satisfiable by the D4 clamp).
//
// Trigger (FR-029), in estimator tokens:
//
//	over ⇔ total > B  OR  share > absoluteShare
//
// where total is the assembled request (messages past the pinned core +
// tool defs; an injected recall span is already in the slice, so it is
// counted — FR-043), share is the tool-result share of the slice, and
// absoluteShare = absolute_trigger_chars ÷ 2.5 (160,000 tokens by default).
// Target after a fire = 80 % of whichever condition fired; emptying stops
// at the target or when no eligible result remains, and does not re-fire on
// the next check unless a condition is exceeded again (FR-021, B-25).
//
// Thrash guard (FR-032): if a TRIGGER condition (never the target) is still
// exceeded after every eligible result is emptied — only possible when a
// non-tool message is itself oversized, which D4's caps and the user/
// argument bounds make unreachable without an injected fault — the check
// returns ErrContextUnrecoverable and the caller ends the turn typed
// (typedTurnExit: one ERROR line, EventKindError, transcript entry) with NO
// further provider call.
//
// Order for one result (FR-033): ingest bound → filter → cap/clamp +
// encoded-line bound → archive append + state (all inside admitToolResult)
// → THIS CHECK → D5 empty → assemble → call.
//
// Performance: the check itself is O(window) estimator work per result — no
// LLM call and no archive read. The archive is read only when a trigger
// actually fires (the D5 pass needs it for the mark), which the pre-turn
// trim keeps rare.
package agent

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// midTurnChecksTotal counts mid-turn window-check evaluations (B-33: N tool
// results → N checks). Exported for test assertions via
// MidTurnBudgetChecksTotal.
var midTurnChecksTotal atomic.Int64

// MidTurnBudgetChecksTotal returns the number of mid-turn D6 checks that
// evaluated their trigger conditions (exempt/NoHistory turns do not count).
func MidTurnBudgetChecksTotal() int64 { return midTurnChecksTotal.Load() }

// absoluteShareTokens converts the operator's absolute_trigger_chars into
// the estimator-token threshold of FR-029's share condition
// (absoluteShare = absolute_trigger_chars ÷ 2.5, i.e. chars × 2/5 — the same
// heuristic estimateMessageTokens uses, so the two cannot drift). A
// non-positive setting falls back to the seeded default (400,000 chars →
// 160,000 tokens).
func absoluteShareTokens(cs config.ContextSettings) int {
	chars := cs.AbsoluteTriggerChars
	if chars <= 0 {
		chars = config.DefaultAbsoluteTriggerChars
	}
	return chars * 2 / 5
}

// midTurnWindowCheck is the D6 mid-turn site. messages is the in-memory
// slice the NEXT provider request is built from; toolDefs the surface the
// turn sends. It returns the (possibly re-sliced) messages and a non-nil
// error — wrapping ErrContextUnrecoverable — ONLY when the thrash guard
// fired; the caller must then end the turn via typedTurnExit and make no
// further provider call.
//
// The pass, in order:
//  1. Evaluate the two FR-029 trigger conditions against B. Neither fired →
//     return unchanged (the overwhelmingly common case, no archive read).
//  2. FR-043: an injected recall span is subject to D5 — under pressure it
//     is dropped FIRST (the FR-019 drop-span-first rule windowTrim already
//     applies), before any real tool result is emptied. Re-evaluate.
//  3. Empty eligible results oldest-first (empty_in_place.go — never the
//     floor set, never Skip) until 80 % of each fired condition holds or
//     no eligible result remains.
//  4. Guard: a trigger condition still exceeded → ErrContextUnrecoverable.
func (al *AgentLoop) midTurnWindowCheck(
	ts *turnState,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
) ([]providers.Message, error) {
	if ts == nil || ts.agent == nil || ts.opts.NoHistory || ts.agent.budgetChecksExempt() {
		// FR-005: an exempt provider manages its own context; an ephemeral
		// (NoHistory) turn has no archive for the D5 pass to mark against.
		return messages, nil
	}
	budget := agentContextBudget(ts.agent)
	cs := config.DefaultContextSettings()
	if cfg := al.GetConfig(); cfg != nil {
		cs = cfg.Context
	}
	absShare := absoluteShareTokens(cs)

	midTurnChecksTotal.Add(1)

	total := requestTokens(messages, toolDefs)
	share := toolResultShareTokens(messages)
	totalFired := total > budget
	shareFired := share > absShare
	if !totalFired && !shareFired {
		return messages, nil
	}

	// FR-043 / B-50d: the injected span goes first. removeInjectedRecallBlock
	// re-slices, so the caller must adopt the returned slice.
	if ts.injectedRecallSpan != nil {
		messages = removeInjectedRecallBlock(ts, messages, true)
		al.dropRecallSpan(ts.sessionKey, "pressure")
		total = requestTokens(messages, toolDefs)
		share = toolResultShareTokens(messages)
		totalFired = total > budget
		shareFired = share > absShare
		if !totalFired && !shareFired {
			return messages, nil
		}
	}

	archive, archErr := ts.agent.Sessions.ReadArchive(context.Background(), ts.sessionKey)
	if archErr != nil {
		// Without the archive the D5 pass cannot build a mark, and firing
		// the guard here would turn an I/O hiccup into a turn-fatal typed
		// error the spec reserves for an injected fault. Log loudly and let
		// the turn proceed — the provider's own context_too_long remains the
		// backstop, exactly as before D6 existed.
		logger.WarnCF("agent", "mid-turn window check: ReadArchive failed; emptying skipped this result",
			map[string]any{"session_key": ts.sessionKey, "error": archErr.Error()})
		return messages, nil //nolint:nilerr // deliberate: an I/O error must not become a typed turn-fatal exit
	}
	lineOf := midTurnLineResolver(archive, messages)

	// Target = 80 % of each condition that fired (FR-029). The un-fired
	// condition imposes nothing — its own trigger still guards it on the
	// next check.
	fits := func(m []providers.Message) bool {
		if totalFired && requestTokens(m, toolDefs) > budget*4/5 {
			return false
		}
		if shareFired && toolResultShareTokens(m) > absShare*4/5 {
			return false
		}
		return true
	}
	al.emptyInPlace(ts, ts.agent, ts.sessionKey, messages, lineOf, archive, fits, emptyingSiteMidTurn)

	// FR-032: the guard re-checks the TRIGGER conditions, not the target —
	// an unreachable target with every trigger back under its threshold is
	// B-36b: the turn simply continues.
	total = requestTokens(messages, toolDefs)
	share = toolResultShareTokens(messages)
	if total > budget || share > absShare {
		return messages, fmt.Errorf(
			"%w: total=%d budget=%d share=%d absolute_share=%d (agent_id=%s session_key=%s)",
			ErrContextUnrecoverable, total, budget, share, absShare, ts.agent.ID, ts.sessionKey)
	}
	return messages, nil
}
