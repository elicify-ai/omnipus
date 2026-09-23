// loop_run_turn_tools.go: Execute and record one iteration's tool calls

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// agentLoopRunTurnToolsExecute carries the shared state of executeToolCalls across its stages.
type agentLoopRunTurnToolsExecute struct {
	rx                        *agentLoopRunTurnTools
	setGoalSucceededThisRound bool
	toolName                  string
	toolArgs                  map[string]any
	ledgerToolName            string
	toctouPolicy              string
	toolCallID                string
	asyncCallback             func(_ context.Context, result *tools.ToolResult)
	asyncCallbackGate         *asyncToolCallbackGate
	toolCBSig                 string
	toolResult                *tools.ToolResult
	toolDuration              time.Duration
	contentForLLM             string
	recallDecision            recallInjectionDecision
	admitted                  admittedToolResult
	toolResultMsg             providers.Message
	tcRecord                  session.ToolCall
	ret0                      agentLoopRunTurnToolsFlow
}

// asyncToolCallbackGate keeps an executor that completes inline from publishing
// its terminal result before the loop has recorded the async-start result. A
// genuinely later callback passes straight through on the executor's caller.
type asyncToolCallbackGate struct {
	mu      sync.Mutex
	ready   bool
	pending *tools.ToolResult
	handle  func(*tools.ToolResult)
}

func (g *asyncToolCallbackGate) callback(result *tools.ToolResult) {
	g.mu.Lock()
	if !g.ready {
		g.pending = result
		g.mu.Unlock()
		return
	}
	g.mu.Unlock()
	g.handle(result)
}

func (g *asyncToolCallbackGate) release() {
	g.mu.Lock()
	if g.ready {
		g.mu.Unlock()
		return
	}
	g.ready = true
	pending := g.pending
	g.pending = nil
	g.mu.Unlock()
	if pending != nil {
		g.handle(pending)
	}
}

// agentLoopRunTurnToolsExecuteFlow reports how a block stage of agentLoopRunTurnToolsExecute wants the conductor to proceed.
type agentLoopRunTurnToolsExecuteFlow int

const (
	agentLoopRunTurnToolsExecuteNext agentLoopRunTurnToolsExecuteFlow = iota
	agentLoopRunTurnToolsExecuteReturn
	agentLoopRunTurnToolsExecuteContinue
	agentLoopRunTurnToolsExecuteBreak
)

// executeToolCalls executes and records the normalized tool calls returned for this iteration.
func (rx *agentLoopRunTurnTools) executeToolCalls() agentLoopRunTurnToolsFlow {
	ex := &agentLoopRunTurnToolsExecute{rx: rx}

	ex.setGoalSucceededThisRound = false
agentLoopRunTurnToolsExecuteLoop1:
	for i, tc := range ex.rx.rr.normalizedToolCalls {
		switch ex.validateCall(i, tc) {
		case agentLoopRunTurnToolsExecuteReturn:
			return ex.ret0
		case agentLoopRunTurnToolsExecuteContinue:
			continue agentLoopRunTurnToolsExecuteLoop1
		}

		// ADR-058 fix: ledgerToolName is the PRE-HOOK tool name, captured
		// before hooks.BeforeTool below gets a chance to run. Every
		// recordToolDenial/recordQuarantineReplay call for THIS call must
		// key the ledger by this value, not by whatever toolName holds
		// after hooks.BeforeTool's HookActionContinue/HookActionModify
		// case (a few lines down) may have reassigned it to
		// toolReq.Tool — because the quarantine gate immediately below
		// looks a tool up BEFORE any hook runs, using exactly this
		// pre-hook value, on EVERY call including the next one. A hook
		// that renames a tool would otherwise store its quarantine entry
		// under the RENAMED (post-hook) name while every future lookup
		// for the same incoming call keys on the ORIGINAL (pre-hook)
		// name — a latent key-shape mismatch that means the short-circuit
		// silently never fires for a renaming hook, and every repeat call
		// resumes a full approval round-trip. No in-tree hook renames a
		// tool today, but nothing prevented one from doing so.

		switch ex.applyQuarantineAndHooks(tc) {
		case agentLoopRunTurnToolsExecuteReturn:
			return ex.ret0
		case agentLoopRunTurnToolsExecuteContinue:
			continue agentLoopRunTurnToolsExecuteLoop1
		}

		switch ex.applyApprovalHook(tc) {
		case agentLoopRunTurnToolsExecuteReturn:
			return ex.ret0
		case agentLoopRunTurnToolsExecuteContinue:
			continue agentLoopRunTurnToolsExecuteLoop1
		}

		// FR-079 (M2): TOCTOU re-check. Re-load the policy pointer and re-resolve
		// the effective policy for this specific tool right before execution.
		// This closes the window between filter-time tools[] assembly and execution.

		switch ex.enforceExecutionPolicy(tc) {
		case agentLoopRunTurnToolsExecuteReturn:
			return ex.ret0
		case agentLoopRunTurnToolsExecuteContinue:
			continue agentLoopRunTurnToolsExecuteLoop1
		}

		switch ex.resolveAskPolicy(tc) {
		case agentLoopRunTurnToolsExecuteReturn:
			return ex.ret0
		case agentLoopRunTurnToolsExecuteContinue:
			continue agentLoopRunTurnToolsExecuteLoop1
		}

		switch ex.prepareDispatch(tc) {
		case agentLoopRunTurnToolsExecuteReturn:
			return ex.ret0
		case agentLoopRunTurnToolsExecuteContinue:
			continue agentLoopRunTurnToolsExecuteLoop1
		}

		// UAT fix (fix/uat-defects-2026-08-22, Defect 1): dispatch-side
		// circuit breaker for a tool call that has already failed with
		// this exact same name+arguments toolFailureCircuitBreakThreshold
		// times in a row THIS turn (see tool_failure_circuit_breaker.go).
		// Mirrors the SEC-26 rate-limit denial immediately above — fail
		// closed (do not even call Execute), surface the denial as a
		// normal tool-result error so the model can react, keep the
		// turn running rather than aborting it. Skipping Execute here
		// (rather than only warning post-hoc) is what actually bounds
		// the token burn: a model that ignores the warning notice below
		// still cannot force more than toolFailureCircuitBreakThreshold
		// real dispatch attempts of the identical call in one turn.

		switch ex.guardAndDispatch(tc) {
		case agentLoopRunTurnToolsExecuteReturn:
			return ex.ret0
		case agentLoopRunTurnToolsExecuteContinue:
			continue agentLoopRunTurnToolsExecuteLoop1
		}

		// UAT 2026-09-13 D-23: a loop of SUCCESSFUL, mutually-cancelling
		// calls (create X / delete X / create X …) never touches the
		// streak above. Record every dispatched call's signature and warn
		// once the turn's history repeats a short cycle; the pre-dispatch
		// check above (toolCircuitBreakerTripped) refuses the call that
		// would extend it past the break point.

		ex.deliverToolOutput()

		ex.recordToolResult(tc)

		// RC-5 (ADR-057 UAT root-cause fix): for every OTHER failed tool
		// call — bash, write_file, async delegate, anything not covered by
		// the two Result-populating branches above — persist the same
		// human-readable reason already sent to the LLM (contentForLLM,
		// computed above) so the durable transcript is never left with a
		// null result and no explanation. Gated on tcRecord.Result == nil
		// so a call that already carries a richer Result (media
		// descriptors, or buildSyncDelegateResult's {"text":…,"error":true}
		// shape) is not given a redundant, differently-shaped Error too.
		//
		// No new data exposure: contentForLLM at this point has already
		// passed through the SEC-25 prompt-guard sanitizer (untrusted
		// tools only) and — CONDITIONALLY, only when
		// cfg.Tools.IsFilterSensitiveDataEnabled() is true — cfg.
		// FilterSensitiveData above (see the `if cfg.Tools.
		// IsFilterSensitiveDataEnabled()` gate a few lines up); when that
		// setting is off, contentForLLM here is unfiltered, matching what
		// was actually sent to the LLM as the tool-result message and
		// already logged via ToolExecEndPayload.Result a few lines up
		// (Duration/IsError block) — this call is not introducing any
		// exposure beyond what those two sinks already have. Truncated via
		// truncateRunes (reusing task_completion_signal.go's existing
		// rune-safe truncation convention, not inventing a new one) to
		// bound transcript growth from a single pathological tool error.
		//
		// Not necessarily redundant with the in-memory session history:
		// ts.agent.Sessions.AddFullMessage below (the tool-result message
		// history write) is gated on `!ts.opts.NoHistory`, but
		// appendToolCallTranscript (which persists tcRecord, including
		// this Error field) is not — it only requires a wired
		// transcriptStore/transcriptSessionID (turn.go's
		// appendToolCallTranscript). So on a NoHistory turn (e.g. a
		// delegated sub-turn's ephemeral history — see subturn.go), this
		// durable transcript write is the ONLY copy of the failure reason
		// that survives the turn at all.

		switch ex.finishCall(i) {
		case agentLoopRunTurnToolsExecuteReturn:
			return ex.ret0
		case agentLoopRunTurnToolsExecuteBreak:
			break agentLoopRunTurnToolsExecuteLoop1
		}
	}
	return agentLoopRunTurnToolsNext
}

// validateCall validates one call and handles early structural refusals.
func (ex *agentLoopRunTurnToolsExecute) validateCall(i int, tc providers.ToolCall) agentLoopRunTurnToolsExecuteFlow {
	if ex.rx.rr.rq.ri.rf.rt.ts.hardAbortRequested() {
		ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
		ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurn(ex.rx.rr.rq.ri.rf.rt.ts, "tool_loop", hardInterruptAbortReason)
		ex.ret0 = agentLoopRunTurnToolsReturn
		return agentLoopRunTurnToolsExecuteReturn
	}

	// A done turn context (the agent's own turn timeout, or any other
	// cancellation of turnCtx that is not a hard abort) means no further
	// tool call in this batch may start. ExecuteWithContext does not
	// consult the context itself, so before this check a batch that began
	// before the deadline kept dispatching every queued call after it.
	// Mirrors the hard-abort check above (end the turn now) and the
	// steering/graceful-interrupt skip at the end of this loop (every call
	// that will not run still gets a synthetic result, so each tool_call
	// in the assistant message keeps its paired tool result). The turn
	// then ends through typedTurnExit — the same typed cancel/timeout exit
	// the provider call uses when this context is done — instead of
	// spending a provider round that can only fail on the same context.
	if ctxErr := ex.rx.rr.rq.ri.rf.rt.turnCtx.Err(); ctxErr != nil {
		const ctxDoneSkipMessage = "Skipped: this turn ran out of time or was cancelled before this tool call could start."
		skipReason := "turn context done (" + ctxErr.Error() + ")"
		logger.InfoCF("agent", "Turn checkpoint: turn context done, skipping remaining tools",
			map[string]any{
				"agent_id":  ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				"completed": i,
				"skipped":   len(ex.rx.rr.normalizedToolCalls) - i,
				"reason":    skipReason,
			})
		for j := i; j < len(ex.rx.rr.normalizedToolCalls); j++ {
			skippedTC := ex.rx.rr.normalizedToolCalls[j]
			ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindToolExecSkipped,
				ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
				ToolExecSkippedPayload{
					Tool:   skippedTC.Name,
					Reason: skipReason,
				},
			)
			// ADR-066 D4: a synthetic skipped result is a builtin-failure
			// surface result like any other skip (FR-009).
			skippedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
				Tool: skippedTC.Name, ToolCallID: skippedTC.ID, Content: ctxDoneSkipMessage, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
			}).Message
			ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, skippedMsg)
		}
		res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ctxErr)
		ex.rx.rr.rq.ri.turnStatus = status
		ex.rx.ret0 = res
		ex.rx.ret1 = exitErr
		ex.ret0 = agentLoopRunTurnToolsReturn
		return agentLoopRunTurnToolsExecuteReturn
	}

	// Unsanitize tool name from LLM — dots were replaced with underscores
	// for Anthropic/Azure API compatibility (e.g., "browser_navigate" → "browser.navigate").
	ex.toolName = ex.rx.rr.rq.ri.rf.rt.ts.agent.Tools.UnsanitizeToolName(tc.Name)
	ex.toolArgs = cloneStringAnyMap(tc.Arguments)

	// pkg/agent/verifier_budget.go::VerifierBudget (JUDGE-FR-051/
	// FR-052): once a verifier adjudication's tool-call or byte cap
	// has been reached by every call already admitted this turn,
	// refuse EVERY further tool call here — before the quarantine
	// gate, before any hook, before dispatch — with a tool-result
	// message telling the Judge the cap was reached and to conclude
	// with the evidence already gathered. The turn is NEVER killed
	// (FR-052): this is an ordinary refused tool-result, the same
	// shape as the serialised-tool-argument-bound refusal further
	// below, and the loop continues so the Judge's next assistant
	// message can still emit its verdict.
	// verifierBudgetForTurn returns nil for every non-verifier turn
	// (the overwhelming majority — an ordinary chat turn's turnID
	// was never registered), and CheckCap on a nil *VerifierBudget
	// is a no-op, so this costs one map lookup on the hot path and
	// nothing more.
	if vb := verifierBudgetForTurn(ex.rx.rr.rq.ri.rf.rt.ts.turnID); vb != nil {
		if refusal, capped := vb.CheckCap(); capped {
			logger.WarnCF("agent", "verifier tool call refused: adjudication budget cap reached (JUDGE-FR-051/FR-052)",
				map[string]any{
					"agent_id": ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
					"tool":     ex.toolName,
				})
			// ADR-066 D4: refused results enter through the choke
			// point on the builtin-failure surface (FR-009).
			// SkipVerifierBudgetAccounting is set because this
			// result exists ONLY because the cap was already
			// reached — it must not itself count toward that same
			// cap.
			refusedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
				Tool: tc.Name, ToolCallID: tc.ID, Content: refusal, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
				SkipVerifierBudgetAccounting: true,
			}).Message
			ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, refusedMsg)
			// ADR-066 D6 (T066-13): the window check runs after EVERY
			// admitted result — empty-only mid-turn, Skip never
			// moves; a thrash-guard fire ends the turn typed with no
			// further provider call (FR-032).
			if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
				res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
				ex.rx.rr.rq.ri.turnStatus = status
				ex.rx.ret0 = res
				ex.rx.ret1 = exitErr
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindToolExecSkipped,
				ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
				ToolExecSkippedPayload{
					Tool:   ex.toolName,
					Reason: refusal,
				},
			)
			return agentLoopRunTurnToolsExecuteContinue
		}
	}

	// A call to a tool this request did not offer never runs (ADR-088
	// D3: the narrowed goal request's "exactly two" is exact; ADR-071
	// §1.1: a lazy tool is callable only once ToolSearch promotes it).
	// Checked on the pre-hook name, before the quarantine gate, hooks,
	// the argument bound and any approval prompt — a call that will not
	// run must not cost the user an approval. Not a policy denial: the
	// denial ledger and quarantine are not consulted, and a tool policy
	// denies at filter time is left to the exec-time deny path below
	// (see toolNotOfferedRefusal).
	if refusal, notOffered := ex.rx.rr.rq.ri.rf.rt.al.toolNotOfferedRefusal(
		ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.offeredTools, tc.Name, ex.toolName, ex.rx.rr.rq.filterTimePolicyMap, ex.rx.rr.rq.goalForce, ex.rx.rr.rq.ri.cfg.Tools.Manifest.Compressed,
	); notOffered {
		logger.WarnCF("agent", "Tool call refused: the tool was not offered in this request",
			map[string]any{
				"agent_id":  ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				"tool":      ex.toolName,
				"iteration": ex.rx.rr.rq.ri.rf.rt.iteration,
				"narrowed":  ex.rx.rr.rq.goalForce.layer1,
			})
		refusedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
			Tool: tc.Name, ToolCallID: tc.ID, Content: refusal, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
		}).Message
		ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, refusedMsg)
		// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
		// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
		// ends the turn typed with no further provider call (FR-032).
		if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
			res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
			ex.rx.rr.rq.ri.turnStatus = status
			ex.rx.ret0 = res
			ex.rx.ret1 = exitErr
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindToolExecSkipped,
			ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
			ToolExecSkippedPayload{
				Tool:   ex.toolName,
				Reason: "tool_not_offered",
			},
		)
		return agentLoopRunTurnToolsExecuteContinue
	}

	// UAT B-9 run 4: an AskUserQuestion trailing a SUCCESSFUL set_goal
	// from the SAME model response never runs. The rubric note already
	// says the two narrowed doors are alternatives ("Call exactly ONE
	// of the two, never both in the same response"); the live case had
	// the model register the record and then emit an invented
	// "Placeholder question - not used" ask, whose park froze a session
	// for 18 minutes even though the goal record was registered. The
	// ask is refused with a result telling the model to work now, or to
	// ask a REAL question on its next turn. Refused here, before
	// dispatch, so no park happens, no question card is created, and
	// the FR-010 question-round budget is not spent (that bump fires
	// only on a genuine ParksTurn success below).
	if ex.toolName == tools.AskUserQuestionToolName && ex.setGoalSucceededThisRound {
		const askAfterSetGoalRefusal = "AskUserQuestion was not called: this same response already " +
			"registered the goal record with set_goal. Registering the record and asking are alternatives — " +
			"you chose to register. Start working on the goal now (your full tool set returns on the next " +
			"request), or, if you are genuinely blocked on a real question, ask that real question on your " +
			"next turn — never a placeholder."
		logger.WarnCF("agent", "goal: refusing an AskUserQuestion trailing a successful set_goal in the same response",
			map[string]any{
				"agent_id":  ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				"turn_id":   ex.rx.rr.rq.ri.rf.rt.ts.turnID,
				"iteration": ex.rx.rr.rq.ri.rf.rt.iteration,
			})
		refusedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
			Tool: tc.Name, ToolCallID: tc.ID, Content: askAfterSetGoalRefusal, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
		}).Message
		ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, refusedMsg)
		// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
		// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
		// ends the turn typed with no further provider call (FR-032).
		if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
			res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
			ex.rx.rr.rq.ri.turnStatus = status
			ex.rx.ret0 = res
			ex.rx.ret1 = exitErr
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindToolExecSkipped,
			ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
			ToolExecSkippedPayload{
				Tool:   ex.toolName,
				Reason: "goal_turn_ask_after_set_goal",
			},
		)
		return agentLoopRunTurnToolsExecuteContinue
	}
	return agentLoopRunTurnToolsExecuteNext
}

// applyQuarantineAndHooks applies denial quarantine, control gates, hooks, and the argument bound.
func (ex *agentLoopRunTurnToolsExecute) applyQuarantineAndHooks(tc providers.ToolCall) agentLoopRunTurnToolsExecuteFlow {
	ex.ledgerToolName = ex.toolName

	// ADR-058 FR-058-11: the quarantine gate. A tool that has already
	// produced one PERMANENT denial earlier in this turn is answered
	// from the cached payload here — before hooks.BeforeTool, the
	// TOCTOU re-check, and the approval path, so none of them run for
	// this call: no hook call, no policy re-resolution, no
	// CheckGrantOrRequestApproval, no RequestApproval, no
	// tool_approval_required frame. The turn CONTINUES (D5 rejected
	// removing the tool from tools[]; the advertised tool set stays
	// stable and this gate is what makes offering it again safe).
	if payload, qReason, quarantined := ex.rx.rr.rq.ri.rf.rt.ts.quarantinedDenialFor(ex.ledgerToolName); quarantined {
		ex.rx.rr.rq.ri.rf.rt.al.emitPolicyDenyAudit(ex.rx.rr.rq.ri.rf.rt.ts, ex.toolName, "quarantined", qReason)
		// ADR-058 fix: persist a transcript record for this replay too.
		// Before this, only the FIRST denial that created the
		// quarantine entry ever produced a tool_call transcript
		// entry — replays 2..N left no record at all and vanished on
		// reload. This never blocks (quarantine is a synchronous
		// short-circuit), so there is no preceding `pending`
		// placeholder to settle; settleAskToolCallTranscript already
		// handles that "no placeholder" case by appending directly
		// (the same shape the headless auto-deny site below relies
		// on for the identical reason).
		settleAskToolCallTranscript(ex.rx.rr.rq.ri.rf.rt.ts, session.ToolCallID(tc.ID), ex.toolName, ex.toolArgs, qReason)
		// ADR-066 D4: denied results enter through the choke point on the
		// builtin-failure surface (FR-009); it persists the line itself.
		deniedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
			Tool: tc.Name, ToolCallID: tc.ID, Content: payload, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
		}).Message
		ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, deniedMsg)
		// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
		// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
		// ends the turn typed with no further provider call (FR-032).
		if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
			res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
			ex.rx.rr.rq.ri.turnStatus = status
			ex.rx.ret0 = res
			ex.rx.ret1 = exitErr
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindToolExecSkipped,
			ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
			ToolExecSkippedPayload{
				Tool:   ex.toolName,
				Reason: fmt.Sprintf("permission_denied (quarantined: %s)", qReason),
			},
		)
		if used, exhausted := ex.rx.rr.rq.ri.rf.rt.ts.recordQuarantineReplay(ex.ledgerToolName); exhausted {
			ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
			ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurnForToolDenialBudget(ex.rx.rr.rq.ri.rf.rt.ts, ex.ledgerToolName, qReason, used)
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		return agentLoopRunTurnToolsExecuteContinue
	}

	// ADR-085 BROWSER-FR-016/FR-016a: once this turn's control-gate
	// deferral bound (BROWSER-FR-014, N=3) has been reached, every
	// LATER control-gated browser tool call short-circuits HERE —
	// before hooks.BeforeTool, before dispatch, before any CDP
	// contact, no lease acquisition, no audit action row, no entry
	// into pkg/tools/browser at all. This is a SEPARATE ledger and
	// refusal from the quarantine gate immediately above: it shares
	// this tool-dispatch point and nothing else (see loop.go's
	// shared-file-chain doc). Never mixes with turnDenialBudget.
	if isBrowserControlGatedTool(ex.ledgerToolName) && ex.rx.rr.rq.ri.rf.rt.ts.browserControlGateExhausted() {
		ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindToolExecSkipped,
			ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
			ToolExecSkippedPayload{
				Tool:   ex.toolName,
				Reason: "browser_control_gate_exhausted",
			},
		)
		exhaustedMsg := browserControlGateExhaustedMessage(ex.toolName)
		settleAskToolCallTranscript(ex.rx.rr.rq.ri.rf.rt.ts, session.ToolCallID(tc.ID), ex.toolName, ex.toolArgs, exhaustedMsg)
		admittedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
			Tool: tc.Name, ToolCallID: tc.ID, Content: exhaustedMsg, IsError: false, ParallelN: len(ex.rx.rr.normalizedToolCalls),
		}).Message
		ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, admittedMsg)
		if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
			res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
			ex.rx.rr.rq.ri.turnStatus = status
			ex.rx.ret0 = res
			ex.rx.ret1 = exitErr
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		return agentLoopRunTurnToolsExecuteContinue
	}

	if ex.rx.rr.rq.ri.rf.rt.al.hooks != nil {
		toolReq, decision := ex.rx.rr.rq.ri.rf.rt.al.hooks.BeforeTool(ex.rx.rr.rq.ri.rf.rt.turnCtx, &ToolCallHookRequest{
			Meta:      ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.before"),
			Tool:      ex.toolName,
			Arguments: ex.toolArgs,
			Channel:   ex.rx.rr.rq.ri.rf.rt.ts.channel,
			ChatID:    ex.rx.rr.rq.ri.rf.rt.ts.chatID,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if toolReq != nil {
				ex.toolName = toolReq.Tool
				ex.toolArgs = toolReq.Arguments
			}
		case HookActionDenyTool:
			denyContent := hookDeniedToolContent("Tool execution denied by hook", decision.Reason)
			ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindToolExecSkipped,
				ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
				ToolExecSkippedPayload{
					Tool:   ex.toolName,
					Reason: denyContent,
				},
			)
			// ADR-066 D4: denied results enter through the choke point on the
			// builtin-failure surface (FR-009); it persists the line itself.
			deniedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
				Tool: tc.Name, ToolCallID: tc.ID, Content: denyContent, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
			}).Message
			ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, deniedMsg)
			// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
			// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
			// ends the turn typed with no further provider call (FR-032).
			if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
				res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
				ex.rx.rr.rq.ri.turnStatus = status
				ex.rx.ret0 = res
				ex.rx.ret1 = exitErr
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			// ADR-058 fix: this branch used to `continue` with no
			// ClassifyDenial, no recordToolDenial and no budget check
			// at all — a third-party ProcessHook that denies a tool
			// reproduced the pre-ADR-058 infinite retry exactly,
			// despite tool_denial.go's package doc, turnDenialLedger's
			// doc, and audit.EventTurnAbortedToolDenialBudget's doc all
			// asserting the budget covers "every denial response
			// handed to the model". This does NOT route through
			// ClassifyDenial/denialPayloadJSON — decision.Reason is
			// arbitrary third-party-hook free text with no fixed
			// literal to classify against, and denyContent (plain
			// text, not a JSON envelope) is already an honest
			// attribution ("denied by hook", never a false claim of a
			// human decision) so D1/D2 do not apply here. quarantine
			// is unconditionally true: a hook that explicitly denies a
			// tool is a deliberate decision, like a human "no" or a
			// resolved policy deny, and there is no FR-079-style
			// re-check mechanism for hook decisions that a cached
			// short-circuit would disable.
			const hookDeniedLedgerReason = "hook_denied"
			if used, exhausted := ex.rx.rr.rq.ri.rf.rt.ts.recordToolDenial(ex.ledgerToolName, hookDeniedLedgerReason, true, denyContent); exhausted {
				ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
				ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurnForToolDenialBudget(ex.rx.rr.rq.ri.rf.rt.ts, ex.ledgerToolName, hookDeniedLedgerReason, used)
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			return agentLoopRunTurnToolsExecuteContinue
		case HookActionAbortTurn:
			ex.rx.rr.rq.ri.turnStatus = TurnEndStatusError
			ex.rx.ret0 = turnResult{}
			ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.hookAbortError(ex.rx.rr.rq.ri.rf.rt.ts, "before_tool", decision)
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		case HookActionHardAbort:
			_ = ex.rx.rr.rq.ri.rf.rt.ts.requestHardAbort()
			ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
			ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurn(ex.rx.rr.rq.ri.rf.rt.ts, "before_tool", decision.Reason)
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
	}

	// ADR-066 D4 / FR-016: the tool-argument bound, measured on the
	// serialised arguments AFTER hooks.BeforeTool (a hook may rewrite
	// them) and BEFORE any approval round-trip — a call that will be
	// refused must not cost the user an approval prompt. Over the
	// cap the tool does not run; the ADR-060-family refusal
	// (tools.ToolArgumentRefusalResult) enters through the choke
	// point like any other result and the turn continues — the model
	// sees the size and the cap and retries smaller. Not a policy
	// denial: the ledger/quarantine machinery is deliberately not
	// consulted.
	if argChars, argCap := serialisedToolArgsChars(ex.toolArgs), toolArgumentsBound(ex.rx.rr.rq.ri.cfg); argChars > argCap {
		refusal := tools.ToolArgumentRefusalResult(ex.toolName, argChars, argCap)
		logger.WarnCF("agent", "Tool call refused: serialised arguments exceed the cap (ADR-066 D4)",
			map[string]any{
				"agent_id":   ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				"tool":       ex.toolName,
				"size_chars": argChars,
				"cap_chars":  argCap,
			})
		refusedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
			Tool: tc.Name, ToolCallID: tc.ID, Content: refusal.ContentForLLM(), IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
		}).Message
		ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, refusedMsg)
		// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
		// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
		// ends the turn typed with no further provider call (FR-032).
		if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
			res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
			ex.rx.rr.rq.ri.turnStatus = status
			ex.rx.ret0 = res
			ex.rx.ret1 = exitErr
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindToolExecSkipped,
			ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
			ToolExecSkippedPayload{
				Tool:   ex.toolName,
				Reason: fmt.Sprintf("%s: serialised arguments of %d chars exceed the %d-char cap", tools.ToolArgumentsTooLargeCode, argChars, argCap),
			},
		)
		return agentLoopRunTurnToolsExecuteContinue
	}
	return agentLoopRunTurnToolsExecuteNext
}

// applyApprovalHook applies the optional approval hook.
func (ex *agentLoopRunTurnToolsExecute) applyApprovalHook(tc providers.ToolCall) agentLoopRunTurnToolsExecuteFlow {
	if ex.rx.rr.rq.ri.rf.rt.al.hooks != nil {
		approval := ex.rx.rr.rq.ri.rf.rt.al.hooks.ApproveTool(ex.rx.rr.rq.ri.rf.rt.turnCtx, &ToolApprovalRequest{
			Meta:      ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.approve"),
			Tool:      ex.toolName,
			Arguments: ex.toolArgs,
			Channel:   ex.rx.rr.rq.ri.rf.rt.ts.channel,
			ChatID:    ex.rx.rr.rq.ri.rf.rt.ts.chatID,
			SessionID: ex.rx.rr.rq.ri.rf.rt.ts.transcriptSessionID,
		})
		if !approval.IsApproved() {
			denyContent := hookDeniedToolContent("Tool execution denied by approval hook", approval.Reason)
			// FR-017 audit-coverage fix: an approval hook (e.g. the gateway's
			// wsApprovalHook) can reject a tool for policy reasons BEFORE the
			// loop reaches the TOCTOU re-check at exec time. That branch only
			// `continue`s, so without this call a denied tool on a WS/CLI turn
			// would write NO attributed audit entry — the most common security
			// event (a policy-denied tool) would be invisible in the audit log.
			// emitPolicyDenyAudit stamps User: ts.auditUser(), so WS-originated
			// turns carry the acting principal (e.g. "cli") and channel/non-
			// gateway turns keep User empty. This is the ONLY tool-deny branch
			// that did not already audit; the TOCTOU (deny), ask-auto-deny, and
			// ask-human-deny branches below each emit their own entry, so there
			// is no double-audit.
			denyReason := approval.Reason
			if denyReason == "" {
				denyReason = "tool execution denied by approval hook"
			}
			ex.rx.rr.rq.ri.rf.rt.al.emitPolicyDenyAudit(ex.rx.rr.rq.ri.rf.rt.ts, ex.toolName, "ask", "approval_hook_deny: "+denyReason)
			ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindToolExecSkipped,
				ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
				ToolExecSkippedPayload{
					Tool:   ex.toolName,
					Reason: denyContent,
				},
			)
			// ADR-066 D4: denied results enter through the choke point on the
			// builtin-failure surface (FR-009); it persists the line itself.
			deniedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
				Tool: tc.Name, ToolCallID: tc.ID, Content: denyContent, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
			}).Message
			ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, deniedMsg)
			// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
			// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
			// ends the turn typed with no further provider call (FR-032).
			if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
				res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
				ex.rx.rr.rq.ri.turnStatus = status
				ex.rx.ret0 = res
				ex.rx.ret1 = exitErr
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			// ADR-058 fix: same rationale as the HookActionDenyTool
			// branch above — this hook-deny path used to bypass the
			// ledger entirely (no ClassifyDenial, no
			// recordToolDenial, no budget), so a ToolApprover hook
			// (e.g. the gateway's wsApprovalHook) denying the same
			// tool repeatedly reproduced the pre-ADR-058 infinite
			// retry exactly, despite this file's own doc comments
			// claiming total budget coverage. Kept as plain text
			// (denyContent), not the JSON permission_denied envelope:
			// approval.Reason is arbitrary hook-supplied free text
			// with no fixed literal to classify against, and the
			// message already names the actual cause ("denied by
			// approval hook") rather than claiming a human decision
			// that did not occur.
			const approvalHookDeniedLedgerReason = "approval_hook_denied"
			if used, exhausted := ex.rx.rr.rq.ri.rf.rt.ts.recordToolDenial(ex.ledgerToolName, approvalHookDeniedLedgerReason, true, denyContent); exhausted {
				ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
				ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurnForToolDenialBudget(ex.rx.rr.rq.ri.rf.rt.ts, ex.ledgerToolName, approvalHookDeniedLedgerReason, used)
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			return agentLoopRunTurnToolsExecuteContinue
		}
	}
	return agentLoopRunTurnToolsExecuteNext
}

// enforceExecutionPolicy rechecks execution policy and records a policy denial.
func (ex *agentLoopRunTurnToolsExecute) enforceExecutionPolicy(tc providers.ToolCall) agentLoopRunTurnToolsExecuteFlow {
	ex.toctouPolicy = ex.rx.rr.rq.ri.rf.rt.al.resolveToolPolicyAtExec(ex.rx.rr.rq.ri.rf.rt.ts, ex.toolName, ex.rx.rr.rq.filterTimePolicyMap)
	if ex.toctouPolicy == "deny" {
		// Policy flipped to deny between filter-time and exec-time.
		// ADR-058 site 1: this branch has no approver-supplied
		// reason at all, so it uses the fixed loop pseudo-reason
		// "policy_denied" (spec §4.1 row 9) — the one table row that
		// exists purely so this site can be uniformly rewired
		// through denialPayloadJSON/ClassifyDenial without changing
		// the pre-existing message text ("already true" per ADR D2;
		// the payload as a whole does still gain "reason" and
		// "permanent" fields it previously lacked, see the
		// denialTable row's own comment).
		const policyDeniedReason = "policy_denied"
		cls, _ := ClassifyDenial(policyDeniedReason)
		denyMsg := denialPayloadJSON(ex.toolName, policyDeniedReason, cls)
		ex.rx.rr.rq.ri.rf.rt.al.emitPolicyDenyAudit(ex.rx.rr.rq.ri.rf.rt.ts, ex.toolName, "deny", "mid_turn_policy_change")
		// ADR-066 D4: denied results enter through the choke point on the
		// builtin-failure surface (FR-009); it persists the line itself.
		deniedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
			Tool: tc.Name, ToolCallID: tc.ID, Content: denyMsg, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
		}).Message
		ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, deniedMsg)
		// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
		// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
		// ends the turn typed with no further provider call (FR-032).
		if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
			res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
			ex.rx.rr.rq.ri.turnStatus = status
			ex.rx.ret0 = res
			ex.rx.ret1 = exitErr
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindToolExecSkipped,
			ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
			ToolExecSkippedPayload{
				Tool:   ex.toolName,
				Reason: "permission_denied (mid-turn policy change)",
			},
		)
		// ADR-058 fix: policy_denied must NOT quarantine, even
		// though cls.Permanent is true for message-classification
		// purposes. FR-079's TOCTOU re-check exists BECAUSE policy
		// can change again mid-turn — quarantining here would
		// silently disable that re-check for the rest of the turn,
		// serving every later call to this tool a stale cached
		// denial with no policy re-resolution at all. If an
		// operator fixes the policy back to allow/ask a moment
		// later, a quarantined tool would never notice; passing
		// `false` here (not cls.Permanent) keeps recordToolDenial's
		// aggregate-budget counting intact while excluding this one
		// reason from the quarantine cache, so resolveToolPolicyAtExec
		// keeps running on every subsequent call to this tool. See
		// recordToolDenial's own doc for the quarantine-vs-Permanent
		// distinction this relies on.
		if used, exhausted := ex.rx.rr.rq.ri.rf.rt.ts.recordToolDenial(ex.ledgerToolName, policyDeniedReason, false, denyMsg); exhausted {
			ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
			ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurnForToolDenialBudget(ex.rx.rr.rq.ri.rf.rt.ts, ex.ledgerToolName, policyDeniedReason, used)
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		return agentLoopRunTurnToolsExecuteContinue
	}
	return agentLoopRunTurnToolsExecuteNext
}

// resolveAskPolicy resolves ask-policy approval before dispatch.
func (ex *agentLoopRunTurnToolsExecute) resolveAskPolicy(tc providers.ToolCall) agentLoopRunTurnToolsExecuteFlow {
	if ex.toctouPolicy == "ask" {
		// Headless auto-deny (issue #264, FR-009): a scheduled run has no
		// operator to approve, so any `ask`-policy tool is denied without
		// ever issuing an approval request — the run must never stall.
		if ex.rx.rr.rq.ri.rf.rt.ts.opts.AutoDenyAsk {
			// ADR-058: this literal is a DEDICATED denialTable row
			// (agent.autoDenyHeadlessReason, tool_denial.go) with
			// headless-specific wording, not the generic
			// unknown-reason fallback — an earlier revision of this
			// comment described the fallback path, which produced a
			// stuttering message ("the tool call was refused (reason:
			// auto-denied: ...)") with no headless-specific guidance
			// and failed AC-01's "every driven reason must be known"
			// guard. A headless scheduled run has no operator by
			// construction, for the whole run, so Permanent: true is
			// the correct classification (ADR D1 row 9).
			const denialReason = autoDenyHeadlessReason
			cls, _ := ClassifyDenial(denialReason)
			denyMsg := denialPayloadJSON(ex.toolName, denialReason, cls)
			// Build optional extra Details for the deny.attempted entry so
			// both correlated records carry the schedule identity (O-3 / F-13
			// / issue #342). scheduledJobContextFrom is a no-op read — safe to
			// call even when no job info was injected.
			var denyExtra map[string]any
			if jobInfo, ok := scheduledJobContextFrom(ex.rx.rr.rq.ri.rf.rt.turnCtx); ok && jobInfo.JobID != "" {
				denyExtra = map[string]any{
					"schedule_job_id":   jobInfo.JobID,
					"schedule_job_name": jobInfo.JobName,
				}
			}
			ex.rx.rr.rq.ri.rf.rt.al.emitPolicyDenyAudit(ex.rx.rr.rq.ri.rf.rt.ts, ex.toolName, "ask", denialReason, denyExtra)
			// O-3 / F-13 / issue #342: emit the canonical tool.policy.ask.denied
			// entry via EmitToolPolicyAskDenied (CRIT-6 compliant, INFO severity,
			// reason=AskDenyReasonScheduled). See emitScheduledAutoDenyAudit.
			ex.rx.rr.rq.ri.rf.rt.al.emitScheduledAutoDenyAudit(ex.rx.rr.rq.ri.rf.rt.turnCtx, ex.rx.rr.rq.ri.rf.rt.ts, ex.toolName, tc.ID)
			// Persist the refusal as a real tool_call entry. This path
			// never blocks (there is no approver on a headless run), so
			// there is no pending placeholder to settle — but without
			// this the scheduled run's transcript showed the tool had
			// simply never been called, with the reason living only in
			// the audit log. settleAskToolCallTranscript appends when it
			// finds no placeholder, which is exactly this case.
			settleAskToolCallTranscript(
				ex.rx.rr.rq.ri.rf.rt.ts, session.ToolCallID(tc.ID), ex.toolName, ex.toolArgs, denialReason)
			// ADR-066 D4: denied results enter through the choke point on the
			// builtin-failure surface (FR-009); it persists the line itself.
			deniedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
				Tool: tc.Name, ToolCallID: tc.ID, Content: denyMsg, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
			}).Message
			ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, deniedMsg)
			// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
			// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
			// ends the turn typed with no further provider call (FR-032).
			if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
				res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
				ex.rx.rr.rq.ri.turnStatus = status
				ex.rx.ret0 = res
				ex.rx.ret1 = exitErr
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindToolExecSkipped,
				ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
				ToolExecSkippedPayload{
					Tool:   ex.toolName,
					Reason: fmt.Sprintf("permission_denied (ask auto-denied: %s)", denialReason),
				},
			)
			if used, exhausted := ex.rx.rr.rq.ri.rf.rt.ts.recordToolDenial(ex.ledgerToolName, denialReason, cls.Permanent, denyMsg); exhausted {
				ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
				ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurnForToolDenialBudget(ex.rx.rr.rq.ri.rf.rt.ts, ex.ledgerToolName, denialReason, used)
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			return agentLoopRunTurnToolsExecuteContinue
		}
		// ask-policy: consult the session-scoped "Always Allow" grant
		// store first (ADR-036 §3.4 — the sole grant-consultation point
		// now that the legacy WS-frame gate, wsApprovalHook, has been
		// retired), then fall through to interactive human approval
		// (FR-011) only when no grant is on file.
		//
		// A standing grant resolves without ever contacting a human, so
		// it must NOT write a pending placeholder — that would render an
		// "awaiting approval" card for a call nobody was asked about.
		// Consult the grant store separately here (CheckGrantOrRequestApproval
		// consults the SAME store first, so a granted call still
		// short-circuits identically), and write the placeholder only on
		// the path that genuinely blocks on a human.
		approved := ex.rx.rr.rq.ri.rf.rt.al.ApprovalGrants().IsAllowed(ex.rx.rr.rq.ri.rf.rt.ts.transcriptSessionID, ex.rx.rr.rq.ri.rf.rt.ts.agentID, ex.toolName, ex.toolArgs)
		denialReason := ""
		if !approved {
			// About to block on a human, for up to the approval
			// registry's timeout (600 s by default, configurable —
			// pkg/gateway/gateway.go's defaultToolApprovalTimeout). The
			// wait is server-side and needs no browser attached: a task
			// run's approval waits exactly like a chat turn's (ADR-082;
			// pinned by pkg/gateway/task_run_ask_approval_test.go).
			// Record the call as `pending`
			// FIRST so the thread shows what the turn is waiting on for
			// the whole wait, and so a reload mid-wait still shows it:
			// the tool_approval_required WS frame is live-only and does
			// not survive a refresh. Before this, an unanswered approval
			// rendered nothing at all and the turn looked hung for no
			// visible reason.
			recordAskPendingToolCall(ex.rx.rr.rq.ri.rf.rt.ts, session.ToolCallID(tc.ID), ex.toolName, ex.toolArgs)
			approved, denialReason = ex.rx.rr.rq.ri.rf.rt.al.CheckGrantOrRequestApproval(
				ex.rx.rr.rq.ri.rf.rt.turnCtx, ex.rx.rr.rq.ri.rf.rt.ts.transcriptSessionID, ex.rx.rr.rq.ri.rf.rt.ts.agentID, ex.toolName, tc.ID, ex.rx.rr.rq.ri.rf.rt.ts.turnID, ex.toolArgs,
			)
		}
		if !approved {
			// Settle the placeholder to `denied` with the outcome
			// reason, so "denied by the user" and "expired after five
			// minutes with nobody watching" are distinguishable in the
			// thread and on replay.
			settleAskToolCallTranscript(
				ex.rx.rr.rq.ri.rf.rt.ts, session.ToolCallID(tc.ID), ex.toolName, ex.toolArgs, denialReason)
			// ADR-058 site 3 — the original defect: denialReason here
			// is verbatim from CheckGrantOrRequestApproval, so it is
			// classified for real rather than assumed to be a user
			// "no". ClassifyDenial handles every reason this call is
			// KNOWN to be able to produce — not just the
			// approvals.go-authored six (user, timeout, saturated,
			// cancel, restart, batch_short_circuit), but also
			// internal_error (policy_approver.go's nil-entry branch),
			// no_approver_configured (tool_approver.go's nop
			// fallback), the empty reason, and "session canceled"
			// (verified end-to-end in this session:
			// pkg/agent/cancel.go::AgentLoop.RequestCancel ->
			// hooks.CancelPendingApprovals ->
			// pkg/gateway/approvals.go::cancelAllPendingForSessions's
			// ApprovalOutcome{Reason: "session canceled"} -> here,
			// distinct from the single-word "cancel" reason above).
			// An earlier revision of this comment claimed the table
			// "covers every reason this call can produce" and
			// enumerated only nine of these — that was never a
			// closed set, and any reason NOT in denialTable still
			// fails safe (Permanent: true) through ClassifyDenial's
			// unknown-reason fallback rather than being silently
			// treated as retryable.
			cls, _ := ClassifyDenial(denialReason)
			denyMsg := denialPayloadJSON(ex.toolName, denialReason, cls)
			ex.rx.rr.rq.ri.rf.rt.al.emitPolicyDenyAudit(ex.rx.rr.rq.ri.rf.rt.ts, ex.toolName, "ask", denialReason)
			// ADR-066 D4: denied results enter through the choke point on the
			// builtin-failure surface (FR-009); it persists the line itself.
			deniedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
				Tool: tc.Name, ToolCallID: tc.ID, Content: denyMsg, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
			}).Message
			ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, deniedMsg)
			// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
			// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
			// ends the turn typed with no further provider call (FR-032).
			if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
				res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
				ex.rx.rr.rq.ri.turnStatus = status
				ex.rx.ret0 = res
				ex.rx.ret1 = exitErr
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindToolExecSkipped,
				ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
				ToolExecSkippedPayload{
					Tool:   ex.toolName,
					Reason: fmt.Sprintf("permission_denied (ask denied: %s)", denialReason),
				},
			)
			// ADR-058 §3.5 (R5, Binding Rule 4 — the positive lower
			// bound): cls.Permanent is false ONLY for "saturated" at
			// THIS site, so recordToolDenial never quarantines it
			// here — a later call to the same tool in the same turn
			// is free to reach the approver and execute (AC-06).
			if used, exhausted := ex.rx.rr.rq.ri.rf.rt.ts.recordToolDenial(ex.ledgerToolName, denialReason, cls.Permanent, denyMsg); exhausted {
				ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
				ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurnForToolDenialBudget(ex.rx.rr.rq.ri.rf.rt.ts, ex.ledgerToolName, denialReason, used)
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			return agentLoopRunTurnToolsExecuteContinue
		}
		// Approved: fall through to execute.
	}
	return agentLoopRunTurnToolsExecuteNext
}

// prepareDispatch records dispatch metadata and prepares asynchronous result handling.
func (ex *agentLoopRunTurnToolsExecute) prepareDispatch(tc providers.ToolCall) agentLoopRunTurnToolsExecuteFlow {
	ts := ex.rx.rr.rq.ri.rf.rt.ts
	feedbackReachesUser := ex.rx.rr.rq.ri.rf.rt.al.toolFeedbackReachesUser(
		ex.rx.ctx, steer.BoundarySyncToolText, ts,
	)

	argsJSON, marshalErr := json.Marshal(ex.toolArgs)
	if marshalErr != nil {
		logger.WarnCF("agent", "failed to marshal tool args for preview", map[string]any{"tool": ex.toolName, "error": marshalErr.Error()})
		argsJSON = []byte("{}")
	}
	argsPreview := utils.Truncate(string(argsJSON), 200)
	logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", ex.toolName, argsPreview),
		map[string]any{
			"agent_id":  ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
			"tool":      ex.toolName,
			"iteration": ex.rx.rr.rq.ri.rf.rt.iteration,
		})
	toolExecSID := u9ToolExecSessionIDs(ex.rx.rr.rq.ri.rf.rt.ts)
	ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
		EventKindToolExecStart,
		ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.start"),
		ToolExecStartPayload{
			ToolCallID: session.ToolCallID(tc.ID),
			ChatID:     ex.rx.rr.rq.ri.rf.rt.ts.chatID,
			// ADR-057 FR-011/FR-012 (W4/W5d, U9): see u9ToolExecSessionIDs
			// and ToolExecStartPayload.SessionID's doc comments (events.go,
			// U23) for the full rationale. The session ID is always the
			// tool-producing session's own transcript identity.
			SessionID:         toolExecSID,
			Tool:              ex.toolName,
			Arguments:         cloneEventArguments(ex.toolArgs),
			ParentSpawnCallID: session.ToolCallID(ex.rx.rr.rq.ri.rf.rt.ts.parentSpawnCallID),
			AgentID:           ex.rx.rr.rq.ri.rf.rt.ts.resolveActiveAgentID(), // Bug 1: runtime-current agent
		},
	)

	// Per-channel tool feedback routing (agent-form spec §3.3 / F-01):
	// only messaging channels emit standalone tool-call messages; webchat,
	// internal channels (system/cli/subagent), cron schedules, and empty
	// channels suppress feedback because the UI already renders tool calls
	// inline or because the channel has no human recipient.
	if ex.rx.rr.rq.ri.cfg.Agents.Defaults.IsToolFeedbackEnabled() &&
		feedbackReachesUser && !ts.opts.SuppressToolFeedback &&
		isMessagingChannel(ts.channel) {
		feedbackPreview := utils.Truncate(
			string(argsJSON),
			ex.rx.rr.rq.ri.cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength(),
		)
		feedbackMsg := fmt.Sprintf("[tool] `%s`\n```\n%s\n```", tc.Name, feedbackPreview)
		fbCtx, fbCancel := context.WithTimeout(ex.rx.rr.rq.ri.rf.rt.turnCtx, 3*time.Second)
		if fbErr := ex.rx.rr.rq.ri.rf.rt.al.bus.PublishOutbound(fbCtx, bus.OutboundMessage{
			Channel: ex.rx.rr.rq.ri.rf.rt.ts.channel,
			ChatID:  ex.rx.rr.rq.ri.rf.rt.ts.chatID,
			Content: feedbackMsg,
		}); fbErr != nil {
			logger.WarnCF("agent", "Failed to publish tool feedback",
				map[string]any{"tool": tc.Name, "channel": ex.rx.rr.rq.ri.rf.rt.ts.channel, "error": fbErr.Error()})
		}
		fbCancel()
	}

	ex.toolCallID = tc.ID
	toolIteration := ex.rx.rr.rq.ri.rf.rt.iteration
	asyncToolName := ex.toolName
	asyncToolCallID := tc.ID
	gate := &asyncToolCallbackGate{
		handle: func(result *tools.ToolResult) {
			ex.handleAsyncResult(result, asyncToolName, asyncToolCallID, toolIteration)
		},
	}
	ex.asyncCallbackGate = gate
	ex.asyncCallback = func(_ context.Context, result *tools.ToolResult) {
		gate.callback(result)
	}

	// SEC-26: Per-agent tool call rate limit check. The system agent is exempt.
	if ex.rx.rr.rq.ri.rf.rt.al.rateLimiter != nil && ex.rx.rr.rq.ri.cfg.Sandbox.RateLimits.MaxAgentToolCallsPerMinute > 0 &&
		!security.IsPrivilegedAgent(ex.rx.rr.rq.ri.rf.rt.ts.agent.AgentType) {
		toolWindow := ex.rx.rr.rq.ri.rf.rt.al.rateLimiter.GetOrCreate(
			"agent:"+ex.rx.rr.rq.ri.rf.rt.ts.agent.ID+":tool_call",
			ex.rx.rr.rq.ri.cfg.Sandbox.RateLimits.MaxAgentToolCallsPerMinute,
			time.Minute,
			security.ScopeAgent,
			ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
			"tool_call",
		)
		if toolRLResult := toolWindow.Allow(); !toolRLResult.Allowed {
			ex.rx.rr.rq.ri.rf.rt.al.recordRateLimitDenial(
				ex.rx.rr.rq.ri.rf.rt.ts,
				"agent_tool_calls_per_minute",
				RateLimitPayload{
					Scope:             string(security.ScopeAgent),
					Resource:          "tool_call",
					PolicyRule:        toolRLResult.PolicyRule,
					RetryAfterSeconds: toolRLResult.RetryAfterSeconds,
					AgentID:           ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
					ChatID:            ex.rx.rr.rq.ri.rf.rt.ts.chatID,
					Tool:              ex.toolName,
					SessionID:         string(ex.rx.rr.rq.ri.rf.rt.ts.routingSessionID),
				},
				map[string]any{"retry_after_seconds": toolRLResult.RetryAfterSeconds},
			)
			// Soft denial: the tool call is rejected (fail closed — the tool
			// does not execute) but the denial is surfaced as a tool-result
			// error rather than aborting the turn, so the LLM can react
			// (e.g. inform the user, back off). Contrast with the LLM-call
			// rate limit above, which aborts the turn entirely.
			errMsg := fmt.Sprintf("Rate limited: %s (retry after %.0fs)",
				toolRLResult.PolicyRule, toolRLResult.RetryAfterSeconds)
			// ADR-066 D4: denied results enter through the choke point on the
			// builtin-failure surface (FR-009); it persists the line itself.
			deniedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
				Tool: tc.Name, ToolCallID: tc.ID, Content: errMsg, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
			}).Message
			ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, deniedMsg)
			// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
			// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
			// ends the turn typed with no further provider call (FR-032).
			if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
				res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
				ex.rx.rr.rq.ri.turnStatus = status
				ex.rx.ret0 = res
				ex.rx.ret1 = exitErr
				ex.ret0 = agentLoopRunTurnToolsReturn
				return agentLoopRunTurnToolsExecuteReturn
			}
			ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindToolExecSkipped,
				ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
				ToolExecSkippedPayload{
					Tool:   ex.toolName,
					Reason: errMsg,
				},
			)
			return agentLoopRunTurnToolsExecuteContinue
		}
	}
	return agentLoopRunTurnToolsExecuteNext
}

func (ex *agentLoopRunTurnToolsExecute) handleAsyncResult(
	result *tools.ToolResult,
	toolName string,
	toolCallID string,
	toolIteration int,
) {
	ts := ex.rx.rr.rq.ri.rf.rt.ts
	feedbackReachesUser := ex.rx.rr.rq.ri.rf.rt.al.toolFeedbackReachesUser(
		ex.rx.ctx, steer.BoundaryAsyncToolFeedback, ts,
	)
	allowOrdinaryFeedback := feedbackReachesUser && !ts.opts.SuppressToolFeedback
	allowSuppressedErrorFeedback := feedbackReachesUser && result.IsError &&
		ts.opts.SuppressToolFeedback && ts.opts.SendResponse
	if allowOrdinaryFeedback || allowSuppressedErrorFeedback {
		// Send ForUser content directly to the user (immediate feedback),
		// mirroring the synchronous tool execution path. This stays separate
		// from AsyncNotifier, which owns the reactive continuation turn below.
		userContent := asyncToolResultUserContent(
			toolName,
			ts.channel,
			ts.opts.SuppressToolFeedback,
			result,
		)
		if userContent != "" && result.IsError && ex.rx.rr.rq.ri.rf.rt.ts.opts.SuppressToolFeedback &&
			ex.rx.rr.rq.ri.rf.rt.ts.channel == "webchat" {
			persistAsyncToolErrorNotice(ex.rx.rr.rq.ri.rf.rt.ts, toolCallID, userContent)
			callbackSID := u9ToolExecSessionIDs(ex.rx.rr.rq.ri.rf.rt.ts)
			callbackNoticeID := fmt.Sprintf("%s:async-error:%d", toolCallID, time.Now().UnixNano())
			ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
				EventKindToolExecEnd,
				ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.async.error"),
				ToolExecEndPayload{
					ToolCallID:        session.ToolCallID(callbackNoticeID),
					ChatID:            ex.rx.rr.rq.ri.rf.rt.ts.chatID,
					SessionID:         callbackSID,
					Tool:              toolName,
					ForLLMLen:         len(result.ContentForLLM()),
					ForUserLen:        len(result.ForUser),
					IsError:           true,
					Async:             true,
					Result:            userContent,
					ParentSpawnCallID: session.ToolCallID(ex.rx.rr.rq.ri.rf.rt.ts.parentSpawnCallID),
					AgentID:           ex.rx.rr.rq.ri.rf.rt.ts.resolveActiveAgentID(),
				},
			)
		} else if userContent != "" {
			outboundSessionID := ""
			if allowSuppressedErrorFeedback {
				outboundSessionID = ts.transcriptSessionID
			}
			outCtx, outCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer outCancel()
			if pubErr := ex.rx.rr.rq.ri.rf.rt.al.bus.PublishOutbound(outCtx, bus.OutboundMessage{
				Channel:   ts.channel,
				ChatID:    ts.chatID,
				SessionID: outboundSessionID,
				Content:   userContent,
			}); pubErr != nil {
				logger.WarnCF("agent", "Async tool ForUser content failed to publish",
					map[string]any{
						"tool":    toolName,
						"channel": ex.rx.rr.rq.ri.rf.rt.ts.channel,
						"error":   pubErr.Error(),
					})
			}
		}
	}

	content := result.ContentForLLM()
	if content == "" {
		return
	}

	// A canceled parent turn must not spring back to life through an async
	// completion. The completion notice above remains visible; only the new
	// reactive continuation turn is suppressed.
	if ex.rx.rr.rq.ri.rf.rt.ts.cancelFired.Load() {
		logger.InfoCF("agent", "Suppressing async-notify continuation turn: originating turn was canceled",
			map[string]any{
				"tool":        toolName,
				"channel":     ex.rx.rr.rq.ri.rf.rt.ts.channel,
				"chat_id":     ex.rx.rr.rq.ri.rf.rt.ts.chatID,
				"agent_id":    ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				"content_len": len(content),
			})
		return
	}

	// AsyncNotifier owns sensitive-data filtering, truncation, the queued
	// event, and the inbound publish that starts the continuation turn.
	notifyCtx := withAsyncNotifyEventMeta(
		context.Background(),
		ex.rx.rr.rq.ri.rf.rt.ts.scope.meta(toolIteration, "runTurn", "turn.follow_up.queued"),
	)
	if notifyErr := ex.rx.rr.rq.ri.rf.rt.al.asyncNotifier.Notify(notifyCtx, AsyncNotifyEvent{
		Channel:             ex.rx.rr.rq.ri.rf.rt.ts.channel,
		ChatID:              ex.rx.rr.rq.ri.rf.rt.ts.chatID,
		AgentID:             ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
		TranscriptSessionID: ex.rx.rr.rq.ri.rf.rt.ts.transcriptSessionID,
		SourceKind:          toolName,
		Content:             content,
	}); notifyErr != nil {
		logger.ErrorCF("agent", "Failed to publish async tool result; result permanently lost",
			map[string]any{"tool": toolName, "channel": ex.rx.rr.rq.ri.rf.rt.ts.channel, "error": notifyErr.Error()})
	}
}

// guardAndDispatch checks dispatch guards, executes the tool, and normalizes its result.
func (ex *agentLoopRunTurnToolsExecute) guardAndDispatch(tc providers.ToolCall) agentLoopRunTurnToolsExecuteFlow {
	ex.toolCBSig = toolCallSignature(ex.toolName, ex.toolArgs)
	if cbReason, tripped := ex.rx.rr.rq.ri.rf.rt.ts.toolCircuitBreakerTripped(ex.toolCBSig); tripped {
		errMsg := toolCircuitBreakerDenialMessage(ex.toolName, cbReason)
		deniedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
			Tool: tc.Name, ToolCallID: tc.ID, Content: errMsg, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
		}).Message
		ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, deniedMsg)
		if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
			res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
			ex.rx.rr.rq.ri.turnStatus = status
			ex.rx.ret0 = res
			ex.rx.ret1 = exitErr
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
			EventKindToolExecSkipped,
			ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
			ToolExecSkippedPayload{
				Tool:   ex.toolName,
				Reason: errMsg,
			},
		)
		return agentLoopRunTurnToolsExecuteContinue
	}

	// ADR-071 §4.3.1(a) "Clear": a tool about to be dispatched is, by
	// definition, no longer an abandoned promotion — delete any
	// pending search-follow-up entry for it under this agent's
	// bucket. Runs unconditionally (harmless no-op when there is no
	// pending entry, e.g. a full-tier tool or a by-name load).
	ex.rx.rr.rq.ri.rf.rt.al.clearPendingSearchPromotion(ex.rx.rr.rq.ri.rf.rt.ts.manifestBucket(), ex.toolName)

	toolStart := time.Now()
	// Inject the current tool call's ID into the context so that tools like
	// spawn can read it as their parentSpawnCallID when they in turn call
	// SpawnSubTurn (FR-H-003).
	execCtx := withSpawnToolCallID(ex.rx.rr.rq.ri.rf.rt.turnCtx, tc.ID)
	// Also expose it via the pkg/tools-level accessor (W2): DelegateTool
	// cannot see the agent-package-private spawnToolCallIDKey above, so
	// it reads its OWN call ID this way at task-creation time to record
	// the correlation anchor a spawned child sub-turn's transcript
	// entries will carry back as ParentSpawnCallID.
	execCtx = tools.WithToolCallID(execCtx, tc.ID)
	// Carry the turn's EXISTING AutoDenyAsk onto the tool context.
	// The loop already uses it to auto-deny `ask`-policy calls; a
	// tool that must refuse one ARGUMENT rather than the whole call
	// (browser_handle_dialog{accept:true}) has no other way to know
	// whether anyone is there to approve. Deliberately the same
	// field, not a second discriminator: two independently-computed
	// answers to "is anyone there" would eventually disagree.
	execCtx = tools.WithAutoDenyAsk(execCtx, ex.rx.rr.rq.ri.rf.rt.ts.opts.AutoDenyAsk)
	// Approval can wait while configuration changes. Recheck current authority
	// immediately before dispatch, including connector assignments removed meanwhile.
	if flow := ex.enforceExecutionPolicy(tc); flow != agentLoopRunTurnToolsExecuteNext {
		return flow
	}
	ex.toolResult = ex.rx.rr.rq.ri.rf.rt.ts.agent.Tools.ExecuteWithContext(
		execCtx,
		ex.toolName,
		ex.toolArgs,
		ex.rx.rr.rq.ri.rf.rt.ts.channel,
		ex.rx.rr.rq.ri.rf.rt.ts.chatID,
		ex.asyncCallback,
	)
	ex.toolDuration = time.Since(toolStart)

	if ex.rx.rr.rq.ri.rf.rt.ts.hardAbortRequested() {
		ex.releaseAsyncCallback()
		ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
		ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurn(ex.rx.rr.rq.ri.rf.rt.ts, "after_tool_exec", hardInterruptAbortReason)
		ex.ret0 = agentLoopRunTurnToolsReturn
		return agentLoopRunTurnToolsExecuteReturn
	}

	if ex.rx.rr.rq.ri.rf.rt.al.hooks != nil {
		toolResp, decision := ex.rx.rr.rq.ri.rf.rt.al.hooks.AfterTool(ex.rx.rr.rq.ri.rf.rt.turnCtx, &ToolResultHookResponse{
			Meta:      ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.after"),
			Tool:      ex.toolName,
			Arguments: ex.toolArgs,
			Result:    ex.toolResult,
			Duration:  ex.toolDuration,
			Channel:   ex.rx.rr.rq.ri.rf.rt.ts.channel,
			ChatID:    ex.rx.rr.rq.ri.rf.rt.ts.chatID,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if toolResp != nil {
				if toolResp.Tool != "" {
					ex.toolName = toolResp.Tool
				}
				if toolResp.Result != nil {
					ex.toolResult = toolResp.Result
				}
			}
		case HookActionAbortTurn:
			ex.releaseAsyncCallback()
			ex.rx.rr.rq.ri.turnStatus = TurnEndStatusError
			ex.rx.ret0 = turnResult{}
			ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.hookAbortError(ex.rx.rr.rq.ri.rf.rt.ts, "after_tool", decision)
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		case HookActionHardAbort:
			ex.releaseAsyncCallback()
			_ = ex.rx.rr.rq.ri.rf.rt.ts.requestHardAbort()
			ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
			ex.rx.ret0, ex.rx.ret1 = ex.rx.rr.rq.ri.rf.rt.al.abortTurn(ex.rx.rr.rq.ri.rf.rt.ts, "after_tool", decision.Reason)
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
	}

	if ex.toolResult == nil {
		ex.toolResult = tools.ErrorResult("hook returned nil tool result")
	}

	// ADR-085 BROWSER-FR-012a/FR-013/FR-015: a REAL dispatch (not the
	// FR-016 short-circuit above, which never reaches here) came back
	// deferred by the browser control gate. Record it on this turn's
	// ledger via the STRUCTURAL Deferred field alone — never by
	// parsing ForLLM's prose — and, on exactly the call that reaches
	// BROWSER-FR-014's bound (the third), append FR-015's terminal
	// instruction to this one result's own ForLLM.
	if ex.toolResult.Deferred != nil && ex.toolResult.Deferred.Gate == browserControlDeferralGate {
		if _, justReachedBound := ex.rx.rr.rq.ri.rf.rt.ts.recordBrowserControlDeferral(); justReachedBound {
			ex.toolResult.ForLLM += browserControlGateBoundReachedNote
		}
	}

	// UAT fix (fix/uat-defects-2026-08-22, Defect 1): update this
	// exact call's consecutive-failure streak. A success (or a hook
	// that turned a failure into one) clears the streak outright; a
	// real failure bumps it and, once it crosses the warn threshold,
	// augments the error content the model is about to see. The
	// REFUSAL side lives entirely in the pre-dispatch check above
	// (toolCircuitBreakerTripped): D-81 made it refuse attempt
	// number toolFailureCircuitBreakThreshold of an identical call
	// BEFORE dispatch, so a streak recorded here can never reach the
	// break threshold — a post-dispatch arm that tripped the breaker
	// at streak >= toolFailureCircuitBreakThreshold was unreachable
	// and is deleted (round-3 cut list, 2026-09-14 review; see the
	// reachability note in tool_failure_circuit_breaker.go). Keyed
	// on toolCBSig computed before dispatch/hooks so a hook renaming
	// the tool does not fragment the streak it is meant to track.
	if ex.toolResult.IsError {
		// A failure ends any identical-SUCCESS run; identical failures
		// are the failure streak's job.
		ex.rx.rr.rq.ri.rf.rt.ts.resetToolSuccessRepeat()
		streak := ex.rx.rr.rq.ri.rf.rt.ts.recordToolFailure(ex.toolCBSig)
		switch {
		case streak >= toolFailureCircuitBreakThreshold:
			reason := toolFailureCircuitBreakerReason(ex.toolName, streak)
			ex.rx.rr.rq.ri.rf.rt.ts.tripToolCircuitBreaker(ex.toolCBSig, reason)
			ex.toolResult.ForLLM = ex.toolResult.ContentForLLM() + toolFailureWarnNotice(ex.toolName, streak)
		case streak >= toolFailureWarnThreshold:
			ex.toolResult.ForLLM = ex.toolResult.ContentForLLM() + toolFailureWarnNotice(ex.toolName, streak)
		}
	} else {
		ex.rx.rr.rq.ri.rf.rt.ts.recordToolSuccess(ex.toolCBSig)
		// Feeds the same-response ask refusal above: a set_goal that
		// succeeded in this response makes any trailing AskUserQuestion
		// in the same batch a refusal, not a park.
		if ex.ledgerToolName == tools.SetGoalToolName {
			ex.setGoalSucceededThisRound = true
		}
		// Identical SUCCESSFUL repetition (tool_failure_circuit_breaker.go):
		// warn the model, and at the stop threshold end the turn after
		// this round (the takeToolRepeatStop check after the tool loop).
		switch run := ex.rx.rr.rq.ri.rf.rt.ts.recordToolSuccessRepeat(ex.toolCBSig); {
		case run >= toolRepeatStopThreshold:
			ex.rx.rr.rq.ri.rf.rt.ts.requestToolRepeatStop(toolRepeatStopNotice(ex.toolName, run))
			ex.toolResult.ForLLM = ex.toolResult.ContentForLLM() + toolRepeatWarnNotice(ex.toolName, run)
		case run >= toolRepeatWarnThreshold:
			ex.toolResult.ForLLM = ex.toolResult.ContentForLLM() + toolRepeatWarnNotice(ex.toolName, run)
		}
	}
	return agentLoopRunTurnToolsExecuteNext
}

// deliverToolOutput delivers loop notices, media, and user-facing tool output.
func (ex *agentLoopRunTurnToolsExecute) deliverToolOutput() {
	if loopNotice := ex.rx.rr.rq.ri.rf.rt.ts.recordToolCallForLoopDetection(ex.toolCBSig); loopNotice != "" {
		ex.toolResult.ForLLM = ex.toolResult.ContentForLLM() + loopNotice
	}
	// Always deliver any media the tool produced AND tag the result with
	// artifact references so the LLM can reason about them in the
	// follow-up call. The follow-up call itself is now unconditional —
	// the model decides whether to add a caption, emit empty content,
	// or run more tools.
	if len(ex.toolResult.Media) > 0 {
		parts := make([]bus.MediaPart, 0, len(ex.toolResult.Media))
		for _, ref := range ex.toolResult.Media {
			part := bus.MediaPart{Ref: ref}
			if ex.rx.rr.rq.ri.turnMediaStore != nil {
				if _, meta, err := ex.rx.rr.rq.ri.turnMediaStore.ResolveWithMetaOpts(ref, media.ResolveOpts{}); err == nil {
					part.Filename = meta.Filename
					part.ContentType = meta.ContentType
					part.Type = inferMediaType(meta.Filename, meta.ContentType)
				}
			}
			parts = append(parts, part)
		}
		outboundMedia := bus.OutboundMediaMessage{
			// ADR-065 FR-6: media sends carry their origin too, so
			// send_file is not a silent gap in the audit trail.
			//
			// From ts.agent.ID, NOT tools.ToolAgentID(ctx): ctx here is
			// runTurn's ORIGINAL parameter and never carries the agent
			// id — only the derived turnCtx does. The FIX 1 comment on
			// WorkspaceID two fields below says precisely this, and the
			// first version of this line ignored it and read back "".
			AgentID: ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
			Channel: ex.rx.rr.rq.ri.rf.rt.ts.channel,
			ChatID:  ex.rx.rr.rq.ri.rf.rt.ts.chatID,
			// FIX 1: workspace-scoped media resolution (channels'
			// store.ResolveWithCallerWorkspace) was silently degrading
			// to the private/global room for every channel send
			// because WorkspaceID was never set here. ts.opts.WorkspaceID
			// is the authoritative source — it is what turnCtx itself
			// was populated with via tools.WithWorkspaceID above (see
			// "Inject the workspace ID" a few hundred lines up in this
			// function). Deliberately read directly from ts.opts rather
			// than tools.ToolWorkspaceID(ctx): `ctx` here is runTurn's
			// ORIGINAL parameter, not turnCtx — WithWorkspaceID was
			// only ever applied to the derived turnCtx (and its
			// children, e.g. execCtx), never back-propagated onto the
			// `ctx` variable, so tools.ToolWorkspaceID(ctx) would
			// always read back "". ts.opts.WorkspaceID carries the
			// exact same value turnCtx was stamped with and needs no
			// context-plumbing assumptions to stay correct.
			WorkspaceID: ex.rx.rr.rq.ri.rf.rt.ts.opts.WorkspaceID,
			SessionID:   ex.rx.rr.rq.ri.rf.rt.ts.transcriptSessionID,
			Parts:       parts,
		}
		// ADR-091 boundary 4 (FR-B-001): a steered
		// session's media is persisted to its own transcript (untouched
		// above) but never sent to a channel or published — this boundary
		// was UNGATED before ADR-091 (sent whenever media was present).
		// audienceFor also calls steer.BoundaryObserver.Observe before this
		// decision is acted on (FR-B-014).
		mediaAudience := ex.rx.rr.rq.ri.rf.rt.al.audienceFor(ex.rx.ctx, steer.BoundaryMedia, ex.rx.rr.rq.ri.rf.rt.ts.transcriptSessionID)
		if mediaAudience != steer.AudienceUser {
			logger.DebugCF("agent", "Steered session: media contained (not sent to a channel)",
				map[string]any{"tool": ex.toolName, "session_id": ex.rx.rr.rq.ri.rf.rt.ts.transcriptSessionID})
		} else if ex.rx.turnChannelManager != nil && ex.rx.rr.rq.ri.rf.rt.ts.channel != "" && !constants.IsInternalChannel(ex.rx.rr.rq.ri.rf.rt.ts.channel) {
			if err := ex.rx.turnChannelManager.SendMedia(ex.rx.ctx, outboundMedia); err != nil {
				logger.WarnCF("agent", "Failed to deliver tool media",
					map[string]any{
						"agent_id": ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
						"tool":     ex.toolName,
						"channel":  ex.rx.rr.rq.ri.rf.rt.ts.channel,
						"chat_id":  ex.rx.rr.rq.ri.rf.rt.ts.chatID,
						"error":    err.Error(),
					})
				ex.toolResult = tools.ErrorResult(fmt.Sprintf("failed to deliver attachment: %v", err)).WithError(err)
			}
		} else if ex.rx.rr.rq.ri.rf.rt.al.bus != nil {
			ex.rx.rr.rq.ri.rf.rt.al.bus.PublishOutboundMedia(ex.rx.ctx, outboundMedia)
		}
		ex.toolResult.ArtifactTags = buildArtifactTags(ex.rx.rr.rq.ri.turnMediaStore, ex.toolResult.Media)
	}

	userContent := toolResultUserContent(
		ex.toolName,
		ex.rx.rr.rq.ri.rf.rt.ts.channel,
		ex.rx.rr.rq.ri.rf.rt.ts.opts.SuppressToolFeedback,
		ex.toolResult,
	)
	// ADR-091 boundary 1 (FR-B-001): a steered session's
	// audience is never the user, regardless of SendResponse. audienceFor
	// also calls steer.BoundaryObserver.Observe before this decision is
	// acted on (FR-B-014).
	if userContent != "" &&
		ex.rx.rr.rq.ri.rf.rt.ts.opts.SendResponse &&
		ex.rx.rr.rq.ri.rf.rt.al.toolFeedbackReachesUser(
			ex.rx.ctx, steer.BoundarySyncToolText, ex.rx.rr.rq.ri.rf.rt.ts,
		) {
		if pubErr := ex.rx.rr.rq.ri.rf.rt.al.bus.PublishOutbound(ex.rx.ctx, bus.OutboundMessage{
			Channel: ex.rx.rr.rq.ri.rf.rt.ts.channel,
			ChatID:  ex.rx.rr.rq.ri.rf.rt.ts.chatID,
			Content: userContent,
		}); pubErr != nil {
			logger.WarnCF("agent", "PublishOutbound failed for tool result",
				map[string]any{
					"tool":  ex.toolName,
					"error": pubErr.Error(),
				})
		} else {
			logger.DebugCF("agent", "Sent tool result to user",
				map[string]any{
					"tool":        ex.toolName,
					"content_len": len(userContent),
				})
		}
	}
}

// toolResultUserContent applies the turn-origin feedback policy at both the
// synchronous and asynchronous publication sites. System-woken turns keep
// successful tool output suppressed. Their failures are already attributable
// in webchat's structured tool frame, while external messaging channels need a
// labeled text fallback because they have no structured execution UI.
func toolResultUserContent(toolName, channel string, suppress bool, result *tools.ToolResult) string {
	if result == nil || result.Silent || result.ForUser == "" {
		return ""
	}
	if !suppress {
		return result.ForUser
	}
	channelType, _ := config.ParseInstanceKey(channel)
	if result.IsError && isMessagingChannel(channelType) {
		return attributedToolErrorNotice(toolName, result.ForUser)
	}
	return ""
}

// asyncToolResultUserContent also covers webchat because the ordinary
// ToolExecEnd event describes only the async start acknowledgement; the later
// callback needs its own attributed completion update.
func asyncToolResultUserContent(toolName, channel string, suppress bool, result *tools.ToolResult) string {
	if content := toolResultUserContent(toolName, channel, suppress, result); content != "" {
		return content
	}
	if suppress && channel == "webchat" && result != nil && !result.Silent && result.IsError && result.ForUser != "" {
		return attributedToolErrorNotice(toolName, result.ForUser)
	}
	return ""
}

func attributedToolErrorNotice(toolName, detail string) string {
	return fmt.Sprintf("Tool `%s` failed:\n%s", toolName, detail)
}

func persistAsyncToolErrorNotice(ts *turnState, toolCallID, content string) {
	if ts == nil || ts.transcriptStore == nil || ts.transcriptSessionID == "" || content == "" {
		return
	}
	now := time.Now().UTC()
	entry := session.TranscriptEntry{
		ID:        fmt.Sprintf("async-tool-error-%s-%d", toolCallID, now.UnixNano()),
		Type:      session.EntryTypeSystem,
		Role:      "system",
		AgentID:   ts.resolveActiveAgentID(),
		Content:   content,
		Timestamp: now,
		Status:    "error",
		TurnID:    ts.turnID,
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		transcriptWriteFailures.Add(1)
		logger.WarnCF("agent", "could not record async tool error notice to transcript",
			map[string]any{
				"session_id":   ts.transcriptSessionID,
				"tool_call_id": toolCallID,
				"error":        err.Error(),
			})
	}
}

// recordToolResult sanitizes and records the admitted tool result.
func (ex *agentLoopRunTurnToolsExecute) recordToolResult(tc providers.ToolCall) {
	ex.contentForLLM = ex.toolResult.ContentForLLM()

	// SEC-25: Sanitize tool results from untrusted sources (web fetch,
	// web search, browser output, read_file) before they enter the
	// LLM's context. Trusted tools (exec, spawn, message, task_*,
	// file writes, etc.) are NEVER sanitized because their output is
	// either user-authored or produced by a peer agent inside the
	// same trust boundary.
	//
	// Order of operations: prompt guard FIRST, sensitive-data filter
	// SECOND. Reversing the order would let an injection payload
	// that mentions a secret pattern be partially redacted, leaving
	// the injection prefix intact and feeding it to the LLM.
	if ex.rx.rr.rq.ri.rf.rt.al.promptGuard != nil && isUntrustedToolResult(ex.toolName) {
		original := ex.contentForLLM
		ex.contentForLLM = ex.rx.rr.rq.ri.rf.rt.al.promptGuard.Sanitize(ex.contentForLLM, false)
		// Log every actual mutation to the operator stream AND to the
		// audit log (when enabled). Mutation is the signal the security
		// team cares about; logging no-op passes would drown real
		// events. The operator-stream log is unconditional so that
		// disabling audit logging does NOT hide prompt-guard rewrites.
		if ex.contentForLLM != original {
			details := map[string]any{
				"action":          "prompt_guard_sanitize",
				"strictness":      string(ex.rx.rr.rq.ri.rf.rt.al.promptGuard.Strictness()),
				"original_bytes":  len(original),
				"sanitized_bytes": len(ex.contentForLLM),
				"tool":            ex.toolName,
				"agent_id":        ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
			}
			logger.InfoCF("agent", "prompt guard sanitized tool result", details)
			// CRIT-6: route through audit.EmitEntry — Log failure bumps the
			// audit-skipped counter so /health audit_degraded surfaces gaps.
			audit.EmitEntry(ex.rx.rr.rq.ri.rf.rt.al.auditLogger, &audit.Entry{
				Event:    audit.EventPolicyEval,
				Decision: audit.DecisionAllow,
				AgentID:  ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
				User:     ex.rx.rr.rq.ri.rf.rt.ts.auditUser(), // FR-017
				Tool:     ex.toolName,
				Details:  details,
			})
		}
	}

	// ADR-066 D4 (FR-009, FR-013): the sensitive-data filter now runs
	// INSIDE the choke point, on the full content, before the cap —
	// so a secret straddling the head or tail cut is redacted whole
	// in both the archive and the window (B-16). The choke point
	// also persists the archive line itself (the mark cites that
	// line), so the AddFullMessage this site used to do is gone.
	// Media refs are resolved on a scratch message first so both
	// the archived and the window form carry them.
	var mediaMsg providers.Message
	// Attach inline image data URLs so vision-capable models can SEE the
	// screenshot/image returned by the tool. Without this the LLM only
	// gets the placeholder text and cannot reason about the picture.
	//
	// ADR-051 Rev 4 Gap 4: the inline-attach site must normalize
	// non-universal image MIMEs (SVG / AVIF / HEIC / HEIF / ICO)
	// before building the data URL — providers 400 on image/svg+xml
	// blocks and the pure-Go decoder set cannot normalize the rest,
	// and the tool-result message is persisted into session
	// history at loop.go:8442-8443, so a bad MIME would poison
	// every subsequent turn. attachToolResultMedia owns that
	// guard; the artifact tag at loop.go:8246 is the path-based
	// fallback hook for the rare "rasterize failed" case.
	if len(ex.toolResult.Media) > 0 && ex.rx.rr.rq.ri.turnMediaStore != nil {
		attachToolResultMedia(&mediaMsg, ex.toolResult.Media, ex.rx.rr.rq.ri.turnMediaStore, ex.rx.rr.rq.ri.maxMediaSize)
	}
	// ADR-066 D5.4 (FR-041/FR-042): budget-first recall decision,
	// BEFORE the choke point so the archive, transcript and events
	// carry the truthful outcome — the tool's "now in your context"
	// receipt only when the span will be spliced below, the non-fit
	// message otherwise. A no-op for every other tool.
	ex.recallDecision = ex.rx.rr.rq.ri.rf.rt.al.decideRecallInjection(ex.rx.rr.rq.ri.rf.rt.ts, tc.Name, ex.rx.rr.rq.ri.messages, ex.contentForLLM)
	ex.contentForLLM = ex.recallDecision.content
	ex.admitted = ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
		Tool:       tc.Name,
		ToolCallID: ex.toolCallID,
		Content:    ex.contentForLLM,
		Media:      mediaMsg.Media,
		IsError:    ex.toolResult.IsError,
		ParallelN:  len(ex.rx.rr.normalizedToolCalls),
	})
	// contentForLLM from here on is the FILTERED full content the
	// archive holds — what the event sinks and the transcript error
	// field always carried (the gateway tool_results/ store keeps it
	// for Verbose chat); the window form is toolResultMsg.
	ex.contentForLLM = ex.admitted.Archived.Content
	ex.toolResultMsg = ex.admitted.Message
	if len(ex.toolResult.InspectionImages) > 0 {
		if ex.rx.rr.rq.ri.rf.rt.inspectionImages == nil {
			ex.rx.rr.rq.ri.rf.rt.inspectionImages = make(map[string][]tools.InspectionImage)
		}
		ex.rx.rr.rq.ri.rf.rt.inspectionImages[ex.toolCallID] = ex.toolResult.InspectionImages
	}
	endSID := u9ToolExecSessionIDs(ex.rx.rr.rq.ri.rf.rt.ts)
	ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
		EventKindToolExecEnd,
		ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.end"),
		ToolExecEndPayload{
			ToolCallID: session.ToolCallID(ex.toolCallID),
			ChatID:     ex.rx.rr.rq.ri.rf.rt.ts.chatID,
			// ADR-057 FR-011/FR-012 (W4/W5d, U9): see the matching
			// ToolExecStartPayload construction above — identical
			// contract on the result frame.
			SessionID:         endSID,
			Tool:              ex.toolName,
			Duration:          ex.toolDuration,
			ForLLMLen:         len(ex.contentForLLM),
			ForUserLen:        len(ex.toolResult.ForUser),
			IsError:           ex.toolResult.IsError,
			Async:             ex.toolResult.Async,
			Result:            ex.contentForLLM,
			ParentSpawnCallID: session.ToolCallID(ex.rx.rr.rq.ri.rf.rt.ts.parentSpawnCallID),
			AgentID:           ex.rx.rr.rq.ri.rf.rt.ts.resolveActiveAgentID(), // Bug 1: runtime-current agent
		},
	)
	tcStatus := "success"
	switch {
	case ex.toolResult.ParksTurn:
		// ADR-057 UAT defect C2 fix (2026-08-04): a SYNCHRONOUS
		// delegate/spawn call whose child sub-turn parked awaiting
		// the parent's answer (message_parent(kind="question",
		// wait=true) — see pkg/agent/subturn.go's spawnSubTurn,
		// the `if turnRes.status == TurnEndStatusParked` branch
		// that sets ToolResult.ParksTurn, the single source of
		// truth for this signal). Without this case, a parked
		// child's toolResult here has Interrupted==false and
		// IsError==false (it is neither a failure nor a
		// cancellation), so tcStatus fell through to the
		// "success" initializer — persisting the OUTER delegate
		// tool call's own tc.Status as "success" even though the
		// live subagent_end WS frame (spawnSubTurn's endStatus
		// switch, now SubTurnStatusParked) already correctly said
		// "parked". That divergence meant a SESSION RELOAD
		// (pkg/gateway/replay.go's resolveStatus(tc.Status), used
		// to reconstruct the subagent_end frame from this exact
		// persisted record) would show "success" for a
		// synchronously-dispatched parked child even after the
		// live-render half of this fix, exactly the class of
		// live/reload-parity bug the surrounding tcStatus switch
		// already exists to close for "interrupted" below.
		// Checked FIRST (highest priority), mirroring this same
		// loop's `parked := toolResult.ParksTurn` priority check
		// (below, in the tool-execution loop) — a park must win
		// over the (mutually exclusive, by construction) Interrupted/
		// IsError cases.
		tcStatus = "parked"
	case ex.toolResult.Interrupted:
		// Finding F (A-I4 round 5): a synchronous delegate/spawn call
		// whose child sub-turn was interrupted by a parent-turn
		// cancellation — see pkg/agent/subturn.go's spawnSubTurn
		// cleanup defer, the single source of truth for this
		// classification (ToolResult.Interrupted's doc comment).
		// Persisting "interrupted" here — rather than folding it into
		// the generic "error" case below — is what lets a session
		// reload's subagent_end frame (pkg/gateway/replay.go reads
		// this exact tc.Status back) show the same terminal status
		// the live WS stream already showed, instead of "failed"
		// (SubagentEndFrame.yaml's status enum explicitly supports
		// "interrupted" for this). The OUTER tool_call_result frame
		// for this same call is unaffected — replay.go clamps any
		// non-success tc.Status down to "error" for that stricter,
		// binary wire enum, matching toolResult.IsError (still true
		// here) and today's unchanged live behavior for the outer
		// badge.
		tcStatus = "interrupted"
	case ex.toolResult.IsError:
		tcStatus = "error"
	}
	ex.tcRecord = session.ToolCall{
		ID:               session.ToolCallID(ex.toolCallID),
		Tool:             ex.toolName,
		Status:           tcStatus,
		DurationMS:       ex.toolDuration.Milliseconds(),
		Parameters:       cloneEventArguments(ex.toolArgs),
		ParentToolCallID: session.ToolCallID(ex.rx.rr.rq.ri.rf.rt.ts.parentSpawnCallID),
	}
	// Persist media descriptors so replay can re-emit the `media`
	// frame and reopened sessions show the attachments the user
	// originally saw. We store enough metadata (ref + filename +
	// content_type + type) for replay to reconstruct the wire frame
	// without re-resolving against the MediaStore at replay time.
	if len(ex.toolResult.Media) > 0 {
		descs := make([]map[string]any, 0, len(ex.toolResult.Media))
		for _, ref := range ex.toolResult.Media {
			d := map[string]any{"ref": ref}
			if ex.rx.rr.rq.ri.turnMediaStore != nil {
				if _, meta, err := ex.rx.rr.rq.ri.turnMediaStore.ResolveWithMetaOpts(ref, media.ResolveOpts{}); err == nil {
					if meta.Filename != "" {
						d["filename"] = meta.Filename
					}
					if meta.ContentType != "" {
						d["content_type"] = meta.ContentType
					}
					d["type"] = inferMediaType(meta.Filename, meta.ContentType)
				}
			}
			descs = append(descs, d)
		}
		// Persist the tool's human-readable result text ALONGSIDE the
		// media descriptors. Previously only {media} was stored, so a
		// media-bearing tool's text vanished on reload — e.g.
		// browser_screenshot's "Current page URL: …" header showed live
		// but the reloaded-from-history view had only {media:[…]}.
		// buildMediaFrame still re-emits from Result["media"]; the
		// added "text" key is what the replayed tool card renders.
		result := map[string]any{"media": descs}
		if resultText := strings.TrimSpace(ex.contentForLLM); resultText != "" {
			result["text"] = resultText
		}
		ex.tcRecord.Result = result
	} else if r := buildSyncDelegateResult(ex.toolName, ex.contentForLLM, ex.toolResult.IsError, ex.toolResult.Async); r != nil {
		// W4 (sync path): spawnSubTurn's async result-persistence defer
		// (subturn.go) no-ops for SYNCHRONOUS delegation — it runs before
		// this record exists and only retries when cfg.Async — so this
		// write is the sync delegate tool_call's FINAL persisted state.
		// Populate Result with the same {"text":…}(+"error") shape the
		// async defer produces, so a reloaded sync delegation shows what
		// the delegate produced (matching the live WS stream and the
		// async path) instead of an empty result. No-op for non-delegate
		// tools AND for async delegation (buildSyncDelegateResult returns
		// nil — async is owned by the defer, never persisted here),
		// preserving prior behavior for every other case. See
		// delegate_result.go.
		ex.tcRecord.Result = r
	}
}

// finishCall persists the call outcome and handles post-call turn control.
func (ex *agentLoopRunTurnToolsExecute) finishCall(i int) agentLoopRunTurnToolsExecuteFlow {
	if ex.toolResult.IsError && ex.tcRecord.Result == nil {
		ex.tcRecord.Error = truncateRunes(ex.contentForLLM, maxFailClosedOutputChars)
	}
	// ADR-066 FR-046: the transcript tool_call entry carries the
	// BOUNDED result the model saw (the window form) so D5.5
	// hydration (T066-06) rebuilds a window that is not lossy, plus
	// the projection state for the SPA's content_state. Only when
	// nothing richer is there already (media descriptors, the sync
	// delegate shape, or the failure Error text).
	if ex.tcRecord.Result == nil && ex.tcRecord.Error == "" {
		if text := strings.TrimSpace(ex.toolResultMsg.Content); text != "" {
			ex.tcRecord.Result = map[string]any{"text": text}
		}
	}
	if ex.admitted.Capped {
		// Always the plain "capped": the SPA-facing content_state
		// enum (ToolCall.yaml) is full | capped | emptied and does
		// NOT distinguish the D4 surface. The internal state also
		// records which cap produced the live bytes
		// (memory.ProjectionCappedFailure) — that value must never
		// be written here, it is not on the wire.
		ex.tcRecord.ContentState = string(memory.ProjectionCapped)
	}
	ex.rx.rr.rq.ri.rf.rt.ts.appendToolCallTranscript(ex.tcRecord)
	ex.releaseAsyncCallback()
	ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, ex.toolResultMsg)
	// ADR-066 D5.4 (FR-041): the recalled text joins the in-memory
	// slice HERE — the same mutation point every mid-turn request
	// is built from — so the provider's next call carries it.
	if ex.recallDecision.inject {
		ex.rx.rr.rq.ri.messages = ex.rx.rr.rq.ri.rf.rt.al.spliceRecallSpan(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.recallDecision.span)
	}
	// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
	// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
	// ends the turn typed with no further provider call (FR-032).
	if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
		res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
		ex.rx.rr.rq.ri.turnStatus = status
		ex.rx.ret0 = res
		ex.rx.ret1 = exitErr
		ex.ret0 = agentLoopRunTurnToolsReturn
		return agentLoopRunTurnToolsExecuteReturn
	}

	// C2 (ADR-057 UAT 2026-08-03): a successful message_parent(kind=
	// question, wait=true) call parks the CALLING child's own durable
	// LifecycleRecord in needs_input (pkg/tools/message_parent.go's
	// parkNeedsInput) — but until this check existed, this in-memory
	// loop was completely blind to that transition and kept iterating,
	// eventually overwriting the durable park with a later terminal
	// state before any `delegate respond` could ever reach it (the
	// child "kept running" past its own park, permanently stranding
	// the correlation_id). toolResult.ParksTurn is the signal
	// message_parent.go sets on exactly that success path; checked
	// FIRST (highest priority) because a park must win over an
	// in-flight steering message or graceful interrupt too.
	parked := ex.toolResult.ParksTurn

	// Steering-queue dequeue (the issue #760 fix). The parking
	// early-return at the end of this function does NOT carry
	// pendingMessages out of the turnResult — a parked turn's
	// turnResult has followUps/turnFailed but no pendingMessages
	// field. A naive "always dequeue here" therefore drains
	// steering messages into a buffer that nobody reads when the
	// tool parks, AND processTurn's post-turn drain
	// (session_worker.go:543, `for al.pendingSteeringCountForScope
	// (target.SessionKey) > 0`) sees the queue empty and skips
	// Continue. The resume message is silently dropped — exactly
	// the founder's "answering the AskUserQuestion card kills the
	// running turn; answers only surface on the next prompt"
	// symptom in pkg/agent/loop_run_turn_tools.go:1759-1761 (pre-fix).
	// Gate the dequeue on !parked so a parked tool leaves the
	// steering queue intact for the post-turn drain to drive the
	// resume turn via AgentLoop.Continue
	// (pkg/agent/steering.go:378) — which dequeues the message,
	// calls runAgentLoop with InitialSteeringMessages=[msg], and
	// the resume turn runs as a normal continuation of the parked
	// session's LLM history. The user-initiated §0.2 correlated
	// user-role resume message reaches the chat target's next
	// turn, and the parked turn's waiter resolves instead of
	// dying. ASKUSER-FIX.
	if !parked {
		if steerMsgs := ex.rx.rr.rq.ri.rf.rt.al.dequeueSteeringMessagesForScope(ex.rx.rr.rq.ri.rf.rt.ts.sessionKey); len(steerMsgs) > 0 {
			ex.rx.rr.rq.ri.pendingMessages = append(ex.rx.rr.rq.ri.pendingMessages, steerMsgs...)
		}
	}

	// ADR-088 FR-010: the question door was genuinely taken on a
	// narrowed goal turn — bump the persisted per-generation
	// question-round budget. Scoped tightly: only THIS exact tool
	// (never any other ParksTurn tool, e.g. a nested delegate's
	// parked child), only when the narrowed pair actually offered
	// the ask door THIS request (goalForce.layer1 &&
	// goalForce.askOffered — a stray AskUserQuestion call on some
	// unrelated turn must never consume a goal's budget it has no
	// relation to), and only on the genuine success path
	// (parked==true — a refused/errored ask attempt asked nothing
	// and must not spend the round, spec S-14/E6).
	if parked && ex.rx.rr.rq.goalForce.layer1 && ex.rx.rr.rq.goalForce.askOffered && ex.toolName == tools.AskUserQuestionToolName {
		ex.rx.rr.rq.ri.rf.rt.al.bumpGoalQuestionRoundsUsed(ex.rx.rr.rq.goalForce)
	}

	skipReason := ""
	skipMessage := ""
	if parked {
		skipReason = "session parked (message_parent question wait=true)"
		skipMessage = "Skipped: this session parked awaiting the parent's answer."
	} else if len(ex.rx.rr.rq.ri.pendingMessages) > 0 {
		skipReason = "queued user steering message"
		skipMessage = "Skipped due to queued user message."
	} else if gracefulPending, _ := ex.rx.rr.rq.ri.rf.rt.ts.gracefulInterruptRequested(); gracefulPending {
		skipReason = "graceful interrupt requested"
		skipMessage = "Skipped due to graceful interrupt."
	}

	if skipReason != "" {
		remaining := len(ex.rx.rr.normalizedToolCalls) - i - 1
		if remaining > 0 {
			logger.InfoCF("agent", "Turn checkpoint: skipping remaining tools",
				map[string]any{
					"agent_id":  ex.rx.rr.rq.ri.rf.rt.ts.agent.ID,
					"completed": i + 1,
					"skipped":   remaining,
					"reason":    skipReason,
				})
			for j := i + 1; j < len(ex.rx.rr.normalizedToolCalls); j++ {
				skippedTC := ex.rx.rr.normalizedToolCalls[j]
				ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
					EventKindToolExecSkipped,
					ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   skippedTC.Name,
						Reason: skipReason,
					},
				)
				// ADR-066 D4: the synthetic skipped result is a builtin-failure
				// surface result like any denial (FR-009).
				skippedMsg := ex.rx.rr.rq.ri.rf.rt.al.admitToolResult(ex.rx.rr.rq.ri.rf.rt.ts, toolResultAdmission{
					Tool: skippedTC.Name, ToolCallID: skippedTC.ID, Content: skipMessage, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
				}).Message
				ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, skippedMsg)
			}
		}
		if parked {
			// Stop the turn NOW — modeled on the hardAbortRequested
			// early-return above (this same loop), not on the
			// graceful-interrupt `break` below: `break` only exits
			// THIS tool-execution loop and falls through to another
			// LLM call at the top of the iteration loop (turnLoop),
			// which is exactly the bug (the loop resuming past the
			// park). A genuine `return` here is what actually stops
			// runTurn. Unlike abortTurn, this deliberately does NOT
			// call ts.restoreSession — a park is not a rollback: the
			// history through this tool call's own recorded result
			// must survive on disk exactly as-is so a later `delegate
			// respond` resumes from this point, not from a rewound
			// pre-turn snapshot.
			ex.rx.rr.rq.ri.rf.rt.ts.setPhase(TurnPhaseParked)
			ex.rx.rr.rq.ri.turnStatus = TurnEndStatusParked
			ex.rx.ret0 = turnResult{
				status:     TurnEndStatusParked,
				followUps:  append([]bus.InboundMessage(nil), ex.rx.rr.rq.ri.rf.rt.ts.followUps...),
				turnFailed: ex.rx.rr.rq.ri.rf.rt.ts.turnFailed,
			}
			ex.rx.ret1 = nil
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		// ADR-066 D6 (T066-13): the window check runs after EVERY admitted
		// result — empty-only mid-turn, Skip never moves; a thrash-guard fire
		// ends the turn typed with no further provider call (FR-032).
		if ex.rx.rr.rq.ri.messages, ex.rx.midTurnGuardErr = ex.rx.rr.rq.ri.rf.rt.al.midTurnWindowCheck(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.messages, ex.rx.rr.rq.ri.rf.providerToolDefs); ex.rx.midTurnGuardErr != nil {
			res, status, exitErr := ex.rx.rr.rq.ri.rf.rt.al.typedTurnExit(ex.rx.rr.rq.ri.rf.rt.ts, ex.rx.rr.rq.ri.rf.rt.iteration, ex.rx.rr.rq.ri.rf.rt.llmModel, ex.rx.midTurnGuardErr)
			ex.rx.rr.rq.ri.turnStatus = status
			ex.rx.ret0 = res
			ex.rx.ret1 = exitErr
			ex.ret0 = agentLoopRunTurnToolsReturn
			return agentLoopRunTurnToolsExecuteReturn
		}
		return agentLoopRunTurnToolsExecuteBreak
	}

	// Also poll for any SubTurn results that arrived during tool execution.
	if ex.rx.rr.rq.ri.rf.rt.ts.pendingResults != nil {
		select {
		case result, ok := <-ex.rx.rr.rq.ri.rf.rt.ts.pendingResults:
			if ok && result != nil && result.ForLLM != "" {
				content := ex.rx.rr.rq.ri.cfg.FilterSensitiveData(result.ForLLM)
				msg := providers.Message{Role: "user", Content: fmt.Sprintf("[SubTurn Result] %s", content)}
				ex.rx.rr.rq.ri.messages = append(ex.rx.rr.rq.ri.messages, msg)
				if !ex.rx.rr.rq.ri.rf.rt.ts.opts.NoHistory {
					ex.rx.rr.rq.ri.rf.rt.ts.agent.Sessions.AddFullMessage(ex.rx.rr.rq.ri.rf.rt.ts.sessionKey, msg)
				}
			}
		default:
			// No results available
		}
	}
	return agentLoopRunTurnToolsExecuteNext
}

func (ex *agentLoopRunTurnToolsExecute) releaseAsyncCallback() {
	gate := ex.asyncCallbackGate
	ex.asyncCallbackGate = nil
	if gate != nil {
		gate.release()
	}
}

// finishToolIteration ticks tool state and stops a turn that repeats successful calls without progress.
func (rx *agentLoopRunTurnTools) finishToolIteration() agentLoopRunTurnToolsFlow {
	rx.rr.rq.ri.rf.rt.ts.agent.Tools.TickTTL()
	logger.DebugCF("agent", "TTL tick after tool execution", map[string]any{
		"agent_id": rx.rr.rq.ri.rf.rt.ts.agent.ID, "iteration": rx.rr.rq.ri.rf.rt.iteration,
	})

	// Identical SUCCESSFUL repetition reached toolRepeatStopThreshold this
	// round (tool_failure_circuit_breaker.go). Every tool result of the
	// round is already recorded, so the history stays well-formed; end the
	// turn through the same finalization path the iteration cap uses, with
	// a visible final message instead of another provider round. A queued
	// user message is new input: let it through and restart the count.
	if notice := rx.rr.rq.ri.rf.rt.ts.takeToolRepeatStop(); notice != "" {
		if len(rx.rr.rq.ri.pendingMessages) > 0 {
			rx.rr.rq.ri.rf.rt.ts.resetToolSuccessRepeat()
		} else {
			logger.WarnCF("agent", "Turn stopped: an identical tool call kept succeeding without progress",
				map[string]any{
					"agent_id":  rx.rr.rq.ri.rf.rt.ts.agent.ID,
					"turn_id":   rx.rr.rq.ri.rf.rt.ts.turnID,
					"iteration": rx.rr.rq.ri.rf.rt.iteration,
					"threshold": toolRepeatStopThreshold,
				})
			rx.finalContent = notice
			rx.rr.rq.ri.rf.rt.ts.markTurnFailed()
			return agentLoopRunTurnToolsBreakL1
		}
	}
	return agentLoopRunTurnToolsNext
}
