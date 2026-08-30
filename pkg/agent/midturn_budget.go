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
// FR-032 amendment (regression fix, C1-residue, 2026-08-27): the guard's
// "un-emptiable residue" judges only the WINDOW (tool defs + the pinned/
// floor messages, every eligible result emptied) — never the ephemeral
// system notes C1 (4d357904) added to `total` (scratchpad, workspace
// instructions/AGENT.md, web-rendering, compressed manifest). Those notes
// are un-emptiable AND non-window: folding them into the guard's own
// predicate meant an oversized-but-legal AGENT.md alone (D9's 262,144-byte
// cap has no budget-aware clamp) made the residue exceed B on EVERY
// tool-calling turn on that workspace, forever — turning "unreachable by
// construction" (§7) into an ordinary configuration state, not the injected
// fault this fatal exit is reserved for. The notes still count toward the
// TRIGGER and the 80% TARGET (C1 is not undone — the check still fires and
// still tries to shed what it can); when they are the ONLY reason `total`
// still exceeds B after full emptying, the check logs one ERROR
// (contextResidueOverflowsTotal) and returns nil — the provider's own
// context_too_long (ADR-051) is the backstop, per §8: "nothing size-related
// is turn-fatal once D4–D6 are in."
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
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// midTurnChecksTotal counts mid-turn window-check evaluations (B-33: N tool
// results → N checks). Exported for test assertions via
// MidTurnBudgetChecksTotal.
var midTurnChecksTotal atomic.Int64

// MidTurnBudgetChecksTotal returns the number of mid-turn D6 checks that
// evaluated their trigger conditions (exempt/NoHistory turns do not count).
func MidTurnBudgetChecksTotal() int64 { return midTurnChecksTotal.Load() }

// contextResidueOverflowsTotal counts mid-turn checks where the emptiable
// WINDOW itself (tool defs + the pinned/floor messages, every eligible tool
// result emptied) fits B, but the un-emptiable ephemeral system notes
// (ephemeralSystemNoteTokens — AGENT.md above all) alone push the request
// over. This is a static configuration condition (an oversized AGENT.md,
// typically), never the injected fault FR-032 reserves the thrash-guard's
// fatal exit for — see the FR-032 amendment note on midTurnWindowCheck's
// final guard. Exported for test assertions and the /metrics handler.
var contextResidueOverflowsTotal atomic.Int64

// ContextResidueOverflowsTotal returns the count above.
func ContextResidueOverflowsTotal() int64 { return contextResidueOverflowsTotal.Load() }

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

// ephemeralSystemNoteTokens estimates the token cost of the ephemeral
// system notes runTurn injects into callMessages before the request that is
// ACTUALLY sent to the provider (C1): the scratchpad note, the workspace
// instructions note (AGENT.md — up to 262,144 bytes, ~104,857 estimator
// tokens, with no budget-aware cap), and the web-rendering note (loop.go's
// callMessages assembly — buildScratchpadNote / injectWorkspaceInstructions
// / injectWebRenderingNote, all called on `repairedHistory`, never on the
// `messages` slice either budget site measures). `messages` never carries
// these notes — each injector returns a FRESH slice built strictly AFTER
// both budget checks run — so requestTokens/sumRequestMessageTokens alone
// under-measure the real request by however large AGENT.md is. A large
// AGENT.md (up to the 256 KB cap) can by itself push the assembled request
// tens of thousands of tokens past what either check saw, producing a
// provider context_too_long on a window the engine believed it was
// protecting — exactly the ADR's §1 incident class.
//
// The fourth ephemeral note — the compressed tool manifest — is charged
// separately (manifestNoteTokens below, mid-turn only): the pre-turn site
// already folds it into sentToolSurfaceTokens' own measurement, so adding
// it here too would double-count it there.
func (al *AgentLoop) ephemeralSystemNoteTokens(ts *turnState) int {
	if ts == nil || ts.agent == nil {
		return 0
	}
	tokens := 0
	add := func(note string) {
		if note != "" {
			tokens += estimateMessageTokens(providers.Message{Role: "system", Content: note})
		}
	}
	add(al.buildScratchpadNote(ts.agent.ID))
	add(buildWorkspaceInstructionsNote(ts.opts.WorkspaceID))
	add(buildWebRenderingNote(ts.channel))
	return tokens
}

// manifestNoteTokens estimates the token cost of the compressed tool
// manifest note (tool_manifest.go) when it will actually be injected. It
// mirrors sentToolSurfaceTokens' own manifest-note measurement — the same
// tool universe (agent.Tools.GetAll()), the same loaded-tools lookup, the
// same builder (tools.BuildCompressedManifest) — so the two estimates
// cannot drift apart. Mid-turn only: see ephemeralSystemNoteTokens' doc
// comment for why the pre-turn site must NOT also call this (it would
// double-count the manifest cost already inside sentToolSurfaceTokens).
func (al *AgentLoop) manifestNoteTokens(ts *turnState, cfg *config.Config) int {
	if ts == nil || ts.agent == nil || ts.agent.Tools == nil || cfg == nil || !cfg.Tools.Manifest.Compressed {
		return 0
	}
	bucket := ts.manifestBucket()
	loaded := al.sessionLoadedTools(bucket)
	note := tools.BuildCompressedManifest(ts.agent.Tools.GetAll(), loaded)
	if note == "" {
		return 0
	}
	return estimateMessageTokens(providers.Message{Role: "system", Content: note})
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
	cfg := al.GetConfig()
	cs := config.DefaultContextSettings()
	if cfg != nil {
		cs = cfg.Context
	}
	absShare := absoluteShareTokens(cs)

	// C1: `total` must measure the request that is ACTUALLY sent — the
	// pinned core + window + injected ephemeral notes (scratchpad, workspace
	// instructions, web-rendering, compressed manifest) — not just the raw
	// window `requestTokens` sees. noteTokens is computed once per check;
	// none of these notes depend on the emptying pass below, so the same
	// value is added at every re-measurement in this function.
	noteTokens := al.ephemeralSystemNoteTokens(ts) + al.manifestNoteTokens(ts, cfg)

	midTurnChecksTotal.Add(1)

	total := requestTokens(messages, toolDefs) + noteTokens
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
		total = requestTokens(messages, toolDefs) + noteTokens
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

	// FR-032 pre-check: can the pass possibly succeed?
	//
	// D5 only ever removes the CONTENT of the ELIGIBLE results. Everything
	// else in the request is un-emptiable: the tool-definition surface (which
	// `total` counts but B does not subtract), the system notes, the user
	// message and the whole floor set. When that residue ALONE still exceeds
	// a fired trigger, the guard below is certain to fire — and running the
	// pass first would empty, and PERSIST as emptied, every eligible result
	// on a turn that is about to abort. typedTurnExit (unlike abortTurn)
	// never calls restoreSession, so those projection entries would survive
	// forever: the marks become permanent and every later turn on the session
	// dies the same way. Refuse before touching anything instead.
	//
	// The residue is measured with the candidates' content replaced by the
	// SAME recall mark emptyOldestFirst would build for them (M2) — never
	// content-removed-entirely, which undercounts by the mark's own cost
	// (~120+ estimator tokens each, buildRecallMark/recall_mark.go) and lets
	// this pre-check pass a band the real post-pass guard then refuses,
	// after the marks are already persisted.
	//
	// FR-032 amendment (regression fix, C1-residue, 2026-08-27): noteTokens
	// is deliberately EXCLUDED from this predicate. C1 (4d357904) made
	// noteTokens un-emptiable AND added it to the residue this pre-check
	// refuses on — so an oversized-but-legal AGENT.md (D9's 262,144-byte
	// cap, no budget-aware clamp) alone made the residue exceed B on every
	// tool-calling turn, forever, on that workspace: "unreachable by
	// construction" (§7) became reachable by ordinary configuration, not
	// the injected fault FR-032 reserves this fatal exit for. The window
	// portion — everything D5 could ever have emptied, fully emptied — is
	// what this predicate judges; the notes still ride on `total`/`fits`
	// below (the trigger and the target still see them, so C1 is not
	// undone: the check still fires and still tries to shed what it can),
	// but a residue that is over budget ONLY because of the un-emptiable
	// notes is not this guard's failure to report — see the final guard's
	// own amendment note for where that case is actually surfaced.
	if !al.midTurnPassCanSucceed(ts, messages, toolDefs, lineOf, archive, budget, absShare, totalFired, shareFired) {
		return messages, fmt.Errorf(
			"%w: the un-emptiable residue alone exceeds the budget "+
				"(total=%d budget=%d share=%d absolute_share=%d agent_id=%s session_key=%s)",
			ErrContextUnrecoverable, total, budget, share, absShare, ts.agent.ID, ts.sessionKey)
	}

	// Target = 80 % of each condition that fired (FR-029). The un-fired
	// condition imposes nothing — its own trigger still guards it on the
	// next check. noteTokens rides along on every `total`-shaped comparison
	// here too (C1) — the ephemeral notes are un-emptiable, so they are a
	// constant addend, never a candidate for emptyInPlace.
	fits := func(m []providers.Message) bool {
		if totalFired && requestTokens(m, toolDefs)+noteTokens > budget*4/5 {
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
	//
	// FR-032 amendment (regression fix, C1-residue): windowTokens is the
	// request MINUS the un-emptiable ephemeral notes — everything D5 could
	// ever have emptied, now fully emptied. That is the only quantity this
	// guard may kill the turn over: a window that still doesn't fit after
	// every eligible result is gone is the genuine "non-tool message itself
	// oversized" case §7 calls unreachable except by an injected fault.
	// `total` (window + noteTokens) is still computed and logged — the
	// notes are real request bytes and a real cause of overflow — but when
	// they are the ONLY reason total exceeds B, that is a static
	// configuration condition (an oversized AGENT.md, typically), not the
	// injected fault this fatal exit is reserved for (ADR-066 §7 "unreachable
	// by construction", §8 "nothing size-related is turn-fatal once D4–D6
	// are in"). Log loudly — an operator needs to know AGENT.md is
	// oversized — and let the provider's own context_too_long (ADR-051
	// translate_error.go) be the backstop, exactly as it was before C1 made
	// noteTokens visible to this check at all.
	windowTokens := requestTokens(messages, toolDefs)
	total = windowTokens + noteTokens
	share = toolResultShareTokens(messages)
	if windowTokens > budget || share > absShare {
		return messages, fmt.Errorf(
			"%w: total=%d budget=%d share=%d absolute_share=%d (agent_id=%s session_key=%s)",
			ErrContextUnrecoverable, total, budget, share, absShare, ts.agent.ID, ts.sessionKey)
	}
	if total > budget {
		contextResidueOverflowsTotal.Add(1)
		logger.ErrorCF("agent",
			"mid-turn window check: un-emptiable system-note overhead alone exceeds the context budget; "+
				"continuing turn, provider's own context error is the backstop",
			map[string]any{
				"total": total, "budget": budget, "window_tokens": windowTokens,
				"note_tokens": noteTokens, "agent_id": ts.agent.ID, "session_key": ts.sessionKey,
			})
	}
	return messages, nil
}

// midTurnPassCanSucceed reports whether emptying every eligible result could
// bring the fired trigger conditions back under their thresholds. See the
// FR-032 pre-check in midTurnWindowCheck for why this runs BEFORE the pass
// rather than as an after-the-fact guard.
//
// M2: the residue models each candidate's post-emptying content as the SAME
// recall mark emptyOldestFirst (empty_in_place.go) would build for it — the
// mark REPLACES the content, it does not remove it, and the mark is real
// request bytes (~120+ estimator tokens each: a JSON payload with the tool
// name, tool_call_id, archive line, size and a recall_conversation hint).
// A pre-check that assumed zero-cost emptying (residue[i].Content = "")
// under-measured the true post-pass total by N × the mark cost: any state
// where the fired trigger's margin was smaller than that would pass this
// pre-check, run the whole D5 pass, persist every (tool_call_id,
// archive_line) → emptied entry, and STILL have the post-pass guard in
// midTurnWindowCheck fire — on a turn typedTurnExit never rolls back, so
// those marks survive forever and poison every later turn on the session.
// Building the mark via the SAME producer (buildRecallMark) the real pass
// calls, with the same inputs, makes this exact rather than an estimate
// that can drift out of sync with what emptyOldestFirst actually writes.
func (al *AgentLoop) midTurnPassCanSucceed(
	ts *turnState,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	lineOf func(int) int,
	archive []memory.ArchivedMessage,
	budget, absShare int,
	totalFired, shareFired bool,
) bool {
	if ts.agent.Sessions == nil {
		return true
	}
	set := ts.agent.Sessions.Projection(ts.sessionKey).Entries
	candidates := eligibleToolResults(messages, lineOf, set)

	residue := make([]providers.Message, len(messages))
	copy(residue, messages)
	for _, i := range candidates {
		m := residue[i]
		line := lineOf(i)
		tool, _ := owningToolCall(residue, i, m.ToolCallID)
		full := markSourceContent(m, line, archive)
		mark, err := buildRecallMark("emptied", tool, m.ToolCallID, line, full, turnNumberForArchiveLine(archive, line))
		if err != nil {
			// buildRecallMark reported the marshal failure; mirror
			// emptyOldestFirst's own fallback (empty_in_place.go) so the
			// residue estimate stays consistent with what the real pass
			// would persist in this rare failure case.
			mark = ""
		}
		residue[i].Content = mark
	}
	// FR-032 amendment (regression fix, C1-residue): noteTokens is
	// deliberately NOT added here — see the call site's doc comment. This
	// predicate judges only what D5 could ever have emptied (residue) plus
	// the un-emptiable WINDOW surface (toolDefs, the pinned/floor messages)
	// requestTokens already folds in; the ephemeral system notes are a
	// separate, non-window addend the final guard in midTurnWindowCheck
	// accounts for on its own terms.
	if totalFired && requestTokens(residue, toolDefs) > budget {
		return false
	}
	if shareFired && toolResultShareTokens(residue) > absShare {
		return false
	}
	return true
}
