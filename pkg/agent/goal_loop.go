// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_loop.go implements `/goal <condition>` (ADR-049 D6/D7, spec Part B
// US-8): a proof-driven session loop where a round = one worker turn plus
// its judge evaluation (D7/MIN-001). Command parsing/persistence lives in
// applyGoalCommandPrompt (the AgentLoop.handleCommand rewrite-hook
// precedent, mirroring applyMemoryCommandPrompt); the judge-gated round
// advance itself lives in checkGoalLoopAfterTurn, called once from
// runAgentLoop right after every natural turn stop (fast no-op unless the
// turn's session carries an active goal in its UnifiedMeta).
//
// Origin gating (Gap #8/r2, R6): applyGoalCommandPrompt requires
// opts.UserInitiated — a cron/async/task/sub-turn turn's "/goal ..." text is
// NOT matched at all and passes through as ordinary chat content.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// applyGoalCommandPrompt is handleCommand's rewrite hook for `/goal`
// (mirrors applyMemoryCommandPrompt/applyExplicitSkillCommand's shape).
// Unlike the memory commands, `/goal` (bare status) and `/goal
// clear|stop|off|reset|cancel|none` answer SYNCHRONOUSLY (matched=true,
// handled=true, no LLM call) so their replies are deterministic; only
// `/goal <condition>` rewrites opts.UserMessage and continues to the LLM
// (matched=true, handled=false) since starting a goal IS running its first
// round.
func (al *AgentLoop) applyGoalCommandPrompt(
	ctx context.Context,
	msg bus.InboundMessage,
	agentInst *AgentInstance,
	opts *processOptions,
) (matched bool, handled bool, reply string) {
	cmdName, ok := commands.CommandName(msg.Content)
	if !ok || cmdName != "goal" {
		return false, false, ""
	}

	// Gap #8/r2, R6: fail-closed origin gate. A non-user-initiated turn (or,
	// defensively, a task-run/sub-turn opts snapshot) never matches — the
	// text passes through unchanged as ordinary chat content.
	if opts == nil || !opts.UserInitiated || opts.IsTaskRun {
		return false, false, ""
	}

	if opts.TranscriptStore == nil || opts.TranscriptSessionID == "" {
		return true, true, "`/goal` requires an active session — start a chat first."
	}

	store := opts.TranscriptStore
	sessionID := opts.TranscriptSessionID
	args := commands.CommandArgs(msg.Content)

	if args == "" {
		return true, true, al.goalStatusReply(sessionID, store)
	}
	if isGoalClearVerb(args) {
		return true, true, al.clearGoal(sessionID, store, "cleared by user")
	}
	if isGoalConfirmVerb(args) {
		return true, true, al.confirmPendingGoal(sessionID, store)
	}

	// ADR-053 Phase-2 compile (FR-110, G-7): the engine-invoked SMART compiler
	// interprets intent → a criteria ladder (behavior/check/prose), vetted by the
	// compile-time feasibility gate (FR-111/D9 — the ONLY net for unverifiable
	// criteria). This is engine-invoked, NOT a skill (ADR-053 §4.5/BOM). A nil
	// agentInst skips the reachability vetoes (tests); production always supplies
	// one so the gate is exhaustive.
	var fc FeasibilityContext
	if agentInst != nil {
		fc = agentFeasibilityContext{agentInst: agentInst}
	}
	res := compileGoalIntent(args, fc, sessionID)
	if res.Rejection != nil {
		// Fail-closed: no rejected criterion persists (FR-111). Surface the
		// reason in chat so the owner can re-state.
		return true, true, formatCompileRejection(res.Rejection)
	}
	compiled := res.Goal

	// Re-statement amendment gate (N-6/D11, FR-113): a `/goal <new intent>`
	// issued while a goal is ALREADY active is diffed as an amendment and
	// confirmed via `/goal confirm` — NEVER silently recompiled. The active
	// goal's Condition + GoalCriteriaJSON are untouched while pending.
	if meta, _ := store.GetMeta(sessionID); meta != nil && meta.GoalCondition != "" {
		return true, true, al.proposeGoalAmendment(sessionID, store, meta, compiled)
	}

	// No active goal: compile + echo + activate. The `/goal` command IS the
	// chat confirmation (FR-113 — no form/modal); the compiled goal is echoed
	// via the goal_status frame and the persisted GoalCriteriaJSON (the S1
	// unified record). Admit to the R5 cap first.
	if pe := GetPlanEngine(al); pe != nil {
		if admitted, active, capN := pe.Admit("goal"); !admitted {
			return true, true, fmt.Sprintf(
				"Cannot start a new goal: active loops %d/%d (cap reached). "+
					"Stop an existing /goal or /loop, or wait for a running plan to finish.",
				active, capN,
			)
		}
	}

	maxRounds := config.DefaultGoalMaxRounds
	if cfg := al.GetConfig(); cfg != nil {
		maxRounds = cfg.Planning.EffectiveGoalMaxRounds()
	}
	condition := compiled.Prompt
	if condition == "" {
		condition = compiled.Intent
	}
	criteriaJSON, merr := marshalCompiledGoal(compiled)
	if merr != nil {
		logger.WarnCF("agent", "goal: could not marshal compiled criteria",
			map[string]any{"session_id": sessionID, "error": merr.Error()})
	}
	nowStr := time.Now().UTC().Format(time.RFC3339)
	zero := 0
	emptyReason := ""
	emptyPending := ""
	if err := store.SetMeta(sessionID, session.MetaPatch{
		GoalCondition:      &condition,
		GoalCriteriaJSON:   &criteriaJSON,
		GoalPendingJSON:    &emptyPending,
		GoalRoundsUsed:     &zero,
		GoalMaxRounds:      &maxRounds,
		GoalLatestReason:   &emptyReason,
		GoalStartedAt:      &nowStr,
		GoalLastActivityAt: &nowStr,
	}); err != nil {
		logger.WarnCF("agent", "goal: failed to persist goal set",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return true, true, "Could not start the goal loop (internal error persisting session state)."
	}

	// The goal_status frame carries the compiled criteria as the echo (the SPA
	// renders the active goal + its ladder). This is the FR-113 "echoed in chat".
	al.emitGoalStatusFrame(sessionID, condition, 0, maxRounds, "", "active")

	opts.UserMessage = condition
	return true, false, ""
}

// formatCompileRejection renders a feasibility-gate rejection (FR-111/D9) for
// chat: the criterion the runtime cannot verify, fail-closed (no rejected
// criterion persists). The owner re-states to remediate.
func formatCompileRejection(r *FeasibilityRejection) string {
	if r == nil {
		return "The goal could not be compiled (no reason given). Please restate it."
	}
	prefix := "The goal was rejected at compile time"
	if r.CriterionIndex >= 0 {
		prefix = fmt.Sprintf("%s — criterion %d was rejected", prefix, r.CriterionIndex+1)
	}
	return prefix + ":\n" + r.Reason +
		"\n\nNo criterion was saved. Please restate the goal with a verifiable criterion."
}

// proposeGoalAmendment is the N-6/D11 re-statement path: a `/goal <new intent>`
// issued while a goal is ALREADY active is diffed as an amendment (added/
// changed/dropped) and stored as GoalPendingJSON for `/goal confirm` — never
// silently recompiled. The active goal is untouched while pending. Returns the
// amendment echo for chat.
func (al *AgentLoop) proposeGoalAmendment(
	sessionID string, store *session.UnifiedStore, meta *session.UnifiedMeta, proposed *CompiledGoal,
) string {
	current := loadCompiledGoal(meta.GoalCriteriaJSON)
	if current == nil {
		// Pre-Phase-2 goal (only GoalCondition): synthesize a single-prose goal
		// to diff against so the amendment still shows what changes.
		current = &CompiledGoal{
			Intent: meta.GoalCondition, Prompt: meta.GoalCondition,
			Criteria: compiledGoalCriteriaFor("", meta.GoalCondition, sessionID),
		}
	}
	amd := diffGoalAmendment(current, proposed)
	pendingJSON, merr := marshalCompiledGoal(proposed)
	if merr != nil {
		logger.WarnCF("agent", "goal: could not marshal pending amendment",
			map[string]any{"session_id": sessionID, "error": merr.Error()})
		return "Could not prepare the amendment (internal error)."
	}
	if err := store.SetMeta(sessionID, session.MetaPatch{GoalPendingJSON: &pendingJSON}); err != nil {
		logger.WarnCF("agent", "goal: could not persist pending amendment",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return "Could not persist the amendment (internal error)."
	}
	return formatAmendmentEcho(amd)
}

// confirmPendingGoal applies a pending amendment (or activates a pending fresh
// goal) on `/goal confirm` (FR-113/D11). Mints a new goal generation: the
// proposed Condition + GoalCriteriaJSON take effect, GoalPendingJSON clears,
// and GoalRoundsUsed resets to 0 (the amended criteria are a fresh verification
// target — R§8.1 "re-statement AMENDS to a new goal generation"). Returns the
// chat reply.
func (al *AgentLoop) confirmPendingGoal(sessionID string, store *session.UnifiedStore) string {
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil || strings.TrimSpace(meta.GoalPendingJSON) == "" {
		return "No pending goal to confirm. Use `/goal <intent>` to start one."
	}
	pending := loadCompiledGoal(meta.GoalPendingJSON)
	if pending == nil {
		// Malformed pending — clear it rather than leave a stuck state.
		empty := ""
		_ = store.SetMeta(sessionID, session.MetaPatch{GoalPendingJSON: &empty})
		return "The pending goal could not be read; it was cleared. Please restate with `/goal <intent>`."
	}
	condition := pending.Prompt
	if condition == "" {
		condition = pending.Intent
	}
	// GoalCriteriaJSON takes the proposed ladder (already JSON in GoalPendingJSON).
	criteriaJSON := meta.GoalPendingJSON
	emptyPending := ""
	zero := 0
	emptyReason := ""
	nowStr := time.Now().UTC().Format(time.RFC3339)
	// A fresh goal (no active GoalCondition) must also reset MaxRounds + admit;
	// an amendment reuses the existing MaxRounds (only rounds reset).
	maxRounds := meta.GoalMaxRounds
	if maxRounds < 1 {
		if cfg := al.GetConfig(); cfg != nil {
			maxRounds = cfg.Planning.EffectiveGoalMaxRounds()
		} else {
			maxRounds = config.DefaultGoalMaxRounds
		}
	}
	if meta.GoalCondition == "" {
		// Activating a fresh pending goal — admit to the R5 cap.
		if pe := GetPlanEngine(al); pe != nil {
			if admitted, active, capN := pe.Admit("goal"); !admitted {
				return fmt.Sprintf(
					"Cannot activate the goal: active loops %d/%d (cap reached).", active, capN)
			}
		}
	}
	if serr := store.SetMeta(sessionID, session.MetaPatch{
		GoalCondition:      &condition,
		GoalCriteriaJSON:   &criteriaJSON,
		GoalPendingJSON:    &emptyPending,
		GoalRoundsUsed:     &zero,
		GoalMaxRounds:      &maxRounds,
		GoalLatestReason:   &emptyReason,
		GoalStartedAt:      &nowStr,
		GoalLastActivityAt: &nowStr,
	}); serr != nil {
		logger.WarnCF("agent", "goal: could not apply confirmed amendment",
			map[string]any{"session_id": sessionID, "error": serr.Error()})
		return "Could not activate the goal (internal error persisting session state)."
	}
	al.emitGoalStatusFrame(sessionID, condition, 0, maxRounds, "", "active")
	verb := "amended"
	if meta.GoalCondition == "" {
		verb = "activated"
	}
	return fmt.Sprintf("Goal %s: %s\nAcceptance criteria: %d.", verb, condition, len(pending.Criteria))
}

// goalStatusReply formats `/goal status`'s deterministic reply (FR-069):
// condition, elapsed wall-clock, rounds_used/bound, cumulative token spend
// (visible-only, NFR-1 — never used to stop the loop), latest judge reason,
// and active loops: N/cap.
func (al *AgentLoop) goalStatusReply(sessionID string, store *session.UnifiedStore) string {
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil || meta.GoalCondition == "" {
		return "No active goal on this session. Use `/goal <condition>` to start one."
	}
	active, capN := al.activeLoopsSnapshot("goal")
	elapsed := "unknown"
	if t, perr := time.Parse(time.RFC3339, meta.GoalStartedAt); perr == nil {
		elapsed = time.Since(t).Round(time.Second).String()
	}
	reason := meta.GoalLatestReason
	if reason == "" {
		reason = "(no round completed yet)"
	}
	return fmt.Sprintf(
		"Goal: %s\nElapsed: %s\nRounds: %d/%d\nToken spend (session, visible-only): %d tokens ($%.4f)\n"+
			"Latest judge reason: %s\nActive loops: %d/%d",
		meta.GoalCondition, elapsed, meta.GoalRoundsUsed, meta.GoalMaxRounds,
		meta.Stats.TokensTotal, meta.Stats.Cost, reason, active, capN,
	)
}

// clearGoal clears the session's goal fields (FR-070's shared body for
// `/goal clear` + aliases, the bound-reached brake, and the task/plan card
// Clear button's future REST equivalent). Returns the user-facing reply.
func (al *AgentLoop) clearGoal(sessionID string, store *session.UnifiedStore, note string) string {
	meta, err := store.GetMeta(sessionID)
	// FR-114 (N-12): /goal clear cancels the in-flight verifier AND any in-flight
	// compilation (a pending amendment or a pending fresh goal whose
	// GoalCondition isn't set yet). hadGoal is true when there is an active goal
	// OR a pending compilation to discard.
	hadGoal := err == nil && meta != nil && (meta.GoalCondition != "" || meta.GoalPendingJSON != "" || meta.GoalCriteriaJSON != "")

	empty := ""
	zero := 0
	if serr := store.SetMeta(sessionID, session.MetaPatch{
		GoalCondition:      &empty,
		GoalCriteriaJSON:   &empty,
		GoalPendingJSON:    &empty,
		GoalRoundsUsed:     &zero,
		GoalMaxRounds:      &zero,
		GoalLatestReason:   &empty,
		GoalStartedAt:      &empty,
		GoalLastActivityAt: &empty,
	}); serr != nil {
		logger.WarnCF("agent", "goal: failed to clear goal state",
			map[string]any{"session_id": sessionID, "error": serr.Error()})
	}
	if !hadGoal {
		return "No active goal to clear."
	}
	if pe := GetPlanEngine(al); pe != nil {
		pe.Release("goal") // paired with the Admit("goal") call at set time
		al.cancelGoalVerifierIfAny(pe, sessionID)
	}
	al.emitGoalStatusFrame(sessionID, "", 0, 0, "", "cleared")
	return "Goal cleared (" + note + ")."
}

// cancelGoalVerifierIfAny implements ADR-052 FR-037's `/goal clear` cancel
// half (7-reviewer gate item 2): looks up the goal unit's registered
// verifier session (verifierUnitForGoal(sessionID)) — set BEFORE dispatch by
// the SAME runVerifierAdjudication (verifier_adjudication.go) plan-Stop's
// fan-out reads — and, if adjudication is currently in flight for this
// session, cancels it via the SAME RequestCancelForSession chat-cancel
// primitive every other Stop surface uses (A2, precedent: plan_engine.go's
// StopPlan/StopTask) — no new cancel machinery — then unregisters the entry.
// A no-op when no verifier is currently registered for this goal (the common
// case: most /goal clears land between rounds, with nothing in flight).
func (al *AgentLoop) cancelGoalVerifierIfAny(pe *PlanEngine, sessionID string) {
	unit := verifierUnitForGoal(sessionID)
	verifierSessionID, ok := pe.VerifierRegistry().Lookup(unit)
	if !ok || verifierSessionID == "" {
		return
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := al.RequestCancelForSession(cancelCtx, verifierSessionID, "", ""); err != nil {
		logger.WarnCF("agent", "goal: could not cancel in-flight goal verifier session",
			map[string]any{"session_id": sessionID, "verifier_session_id": verifierSessionID, "error": err.Error()})
	}
	pe.VerifierRegistry().Unregister(unit)
}

// isGoalClearVerb reports whether args (the text after "/goal ") is one of
// the recognized clear aliases (FR-070, commands.GoalClearAliases).
func isGoalClearVerb(args string) bool {
	norm := strings.ToLower(strings.TrimSpace(args))
	for _, v := range commands.GoalClearAliases() {
		if norm == v {
			return true
		}
	}
	return false
}

// activeLoopsSnapshot reads R5's global active-loop count/cap via the
// installed PlanEngine's side-effect-free Admit (its doc comment: the count
// is computed fresh from persisted state on every call, never mutated) —
// safe to call purely for its (active, cap) return with no admission
// side-effect. Returns (0, DefaultGlobalActiveLoopCap) when no PlanEngine is
// installed (tests, or before boot wiring completes).
func (al *AgentLoop) activeLoopsSnapshot(kind string) (active, capN int) {
	pe := GetPlanEngine(al)
	if pe == nil {
		return 0, config.DefaultGlobalActiveLoopCap
	}
	_, active, capN = pe.Admit(kind)
	return active, capN
}

// emitGoalStatusFrame publishes a goal_status WS frame (FR-069/US-8) via
// EmitGoalStatusChanged.
func (al *AgentLoop) emitGoalStatusFrame(sessionID, condition string, round, maxRounds int, reason, state string) {
	active, capN := al.activeLoopsSnapshot("goal")
	al.EmitGoalStatusChanged(GoalStatusChangedPayload{
		SessionID:    sessionID,
		Condition:    condition,
		Round:        round,
		MaxRounds:    maxRounds,
		LatestReason: reason,
		ActiveLoops:  active,
		Cap:          capN,
		State:        state,
	})
}

// goalJudgeRoundTimeout bounds one /goal round's judge call (review r1 major
// M2). Mirrors plan_engine.go's planJudgeRoundTimeout exactly (same 10-minute
// bound) but for a DIFFERENT reason: the plan judge decouples into its own
// goroutine so a slow call never blocks the tick cycle, whereas a goal round
// runs SYNCHRONOUSLY inside the interactive turn (its outcome — met / unmet+
// round-advance / rounds-exhausted — must be known before the turn can decide
// whether to re-inject a follow-up round, checkGoalLoopAfterTurn's own doc
// comment below). Without an upper bound of its own, JudgeCriteria's D7
// contract of retrying FOREVER on judge-unavailability (respecting only ctx
// cancellation, judge.go's doc comment) would hang the user's live chat turn
// indefinitely whenever the judge is throttled/erroring and the turn's own
// ctx carries no deadline. A var (not const) so tests can substitute a short
// bound without a real multi-minute wait.
var goalJudgeRoundTimeout = planJudgeRoundTimeout //nolint:gochecknoglobals

// checkGoalLoopAfterTurn is US-8's judge-gated round-advance hook (ADR-049
// D6/D7, FR-067..070). Called once, synchronously, from runAgentLoop right
// after every natural (non-aborted) turn stop — a fast no-op unless the
// turn's session carries an active goal. On an unmet verdict with rounds
// remaining, it appends a follow-up bus.InboundMessage to result.followUps
// (the existing turn re-inject seam, runAgentLoop's own followUps-publish
// loop right after its checkGoalLoopAfterTurn call) carrying the judge's
// reason as steering — that loop then re-enters the turn machinery for the
// next round.
// goalLoopFollowUpSenderID is the sentinel Sender.CanonicalID stamped on the
// goal loop's own re-injected follow-up turn (below). checkGoalLoopAfterTurn
// reads it back via opts.SenderID (processMessage threads msg.Sender.CanonicalID
// onto SenderID for every turn, including a republished follow-up) to
// recognize its own continuation without relying on UserInitiated, which is
// correctly false for a system-originated follow-up.
const goalLoopFollowUpSenderID = "system:goal_loop"

func (al *AgentLoop) checkGoalLoopAfterTurn(
	ctx context.Context,
	agentInst *AgentInstance,
	opts processOptions,
	result *turnResult,
) {
	if result == nil || opts.IsTaskRun || opts.TranscriptStore == nil || opts.TranscriptSessionID == "" {
		return
	}
	// review r2 RV3: origin-gate the hook itself, not just IsTaskRun. /goal and
	// /loop can coexist on one session; a /loop/cron/heartbeat/async turn
	// (ProcessScheduled, loop.go, IsTaskRun=false) has UserInitiated=false and
	// SenderID="" (ProcessScheduled builds its processOptions literal directly,
	// never through the msg-based path that sets these — see UserInitiated's
	// own doc comment) and must NOT advance or touch the goal. Only a genuine
	// user turn (UserInitiated) or the goal loop's own re-injected follow-up
	// (SenderID == goalLoopFollowUpSenderID, stamped below) may proceed —
	// mirrors applyGoalCommandPrompt's own origin gate.
	if !opts.UserInitiated && opts.SenderID != goalLoopFollowUpSenderID {
		return
	}
	if agentInst == nil {
		return
	}
	store := opts.TranscriptStore
	sessionID := opts.TranscriptSessionID

	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil || meta.GoalCondition == "" {
		return // no active goal — fast path
	}

	// ADR-053 Phase-2 / D12 (R§8.3c/FR-174): graceful wind-down at the
	// adjudication boundary. If the ONE app-level OVERALL token pool is
	// exhausted, this scope transitions to failed(budget_exhausted) with a
	// handover summary — the current turn already finished (we are AT the
	// boundary), so this is NOT a mid-turn hard-fail. No new adjudication
	// starts once exhausted.
	if tb := al.TokenBudget(); tb != nil && tb.Exhausted() {
		handover := fmt.Sprintf(
			"Goal %q stopped: the overall token budget is exhausted (consumed %d tokens).",
			meta.GoalCondition, tb.Consumed())
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, handover)
		al.clearGoal(sessionID, store, FailedReasonBudgetExhausted)
		return
	}

	// Phase-2 compiled criteria ladder (FR-110): judge against the compiled
	// ladder when GoalCriteriaJSON is present, else fall back to a single prose
	// criterion synthesized from GoalCondition (back-compat with pre-Phase-2
	// sessions). compiledGoalCriteriaFor is the single read point (DoD-11).
	criteria := compiledGoalCriteriaFor(meta.GoalCriteriaJSON, meta.GoalCondition, sessionID)
	attempt := meta.GoalRoundsUsed + 1

	// review r1 major M2: bound the judge call to its OWN timeout, derived
	// from (so an already-aborted turn still cancels promptly) but never
	// longer than goalJudgeRoundTimeout — see that var's doc comment.
	judgeCtx, cancel := context.WithTimeout(ctx, goalJudgeRoundTimeout)
	defer cancel()

	jr := al.JudgeCriteria(judgeCtx, JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		AssigneeAgentID: agentInst.ID,
		Criteria:        criteria,
		Attempt:         attempt,
		ClaimText:       result.finalContent,
		// ADR-052 FR-032/FR-037: the goal's own chat session — keys the
		// verifier registry (so /goal clear can cancel an in-flight goal
		// verifier) and sources the transcript window the verifier is fed.
		GoalSessionID: sessionID,
		// Product-blocker fix (ADR-052 FR-011/012 x ADR-046 P1): the chat
		// turn's own channel-bound workspace (opts.WorkspaceID; may
		// legitimately be empty for an unbound chat — the Judge's turn then
		// falls back to its own agent home, never a hard failure). See
		// JudgeCriteriaInput.WorkspaceID.
		WorkspaceID: opts.WorkspaceID,
	})

	if jr.Unavailable {
		// D7: judge unavailable — attempt/round NOT consumed, no verdict
		// recorded, idle clock untouched (the goal stays exactly as it was;
		// the next natural turn on this session re-evaluates the same round).
		logger.WarnCF("agent", "goal loop: judge unavailable, round not consumed",
			map[string]any{"session_id": sessionID, "reason": jr.Reason})
		return
	}

	// FR-064/D7 idle-expiry (review r1): a real judge round just ran (not
	// Unavailable) — this IS genuine activity, so bump the calendar-brake
	// clock. Best-effort: a write failure here only delays idle-expiry,
	// never blocks the round itself (mirrors plan_engine.go's touchActivity).
	activityNow := time.Now().UTC().Format(time.RFC3339)
	if perr := store.SetMeta(sessionID, session.MetaPatch{
		GoalLastActivityAt: &activityNow,
	}); perr != nil {
		logger.WarnCF("agent", "goal loop: could not bump idle-expiry activity clock",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
	}

	verdict := jr.Verdict
	al.writeGoalVerdictTranscript(store, sessionID, verdict)

	if verdict.Met {
		al.clearGoal(sessionID, store, "condition met")
		return
	}

	maxRounds := meta.GoalMaxRounds
	if maxRounds < 1 {
		maxRounds = config.DefaultGoalMaxRounds
	}
	reasonText := goalVerdictReasonText(verdict)

	if attempt >= maxRounds {
		handover := fmt.Sprintf(
			"Goal %q did not reach a MET verdict within %d round(s). Latest judge feedback:\n%s",
			meta.GoalCondition, maxRounds, reasonText,
		)
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, handover)
		al.clearGoal(sessionID, store, fmt.Sprintf("round bound reached (%d/%d)", attempt, maxRounds))
		return
	}

	newRound := attempt
	if perr := store.SetMeta(sessionID, session.MetaPatch{
		GoalRoundsUsed:   &newRound,
		GoalLatestReason: &reasonText,
	}); perr != nil {
		logger.WarnCF("agent", "goal loop: failed to persist round advance",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
	}
	al.emitGoalStatusFrame(sessionID, meta.GoalCondition, newRound, maxRounds, reasonText, "round_advanced")

	// Re-inject a follow-up turn carrying the judge reason as steering
	// (turn re-inject seam: followUps re-publish, runAgentLoop). Content
	// deliberately does not start with "/goal" — even if this message
	// somehow reached handleCommand again, it would not parse as a command;
	// UserInitiated is left at its zero value (false) regardless, correctly
	// marking this a system continuation, not a fresh user command (Gap #8).
	result.followUps = append(result.followUps, bus.InboundMessage{
		Channel:    opts.Channel,
		ChatID:     opts.ChatID,
		Sender:     bus.SenderInfo{CanonicalID: goalLoopFollowUpSenderID},
		Content:    goalSteeringPrompt(meta.GoalCondition, reasonText),
		SessionID:  sessionID,
		SessionKey: opts.SessionKey,
	})
}

// goalVerdictReasonText builds a human-readable summary of the judge's unmet
// per-criterion reasons, fed forward as steering (FR-043 pattern, applied to
// /goal rounds).
func goalVerdictReasonText(v *task.JudgeVerdict) string {
	if v == nil || len(v.PerCriterion) == 0 {
		return "(no reason recorded)"
	}
	var sb strings.Builder
	for _, cv := range v.PerCriterion {
		if cv.Met {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(cv.Reason)
	}
	if sb.Len() == 0 {
		return "(no reason recorded)"
	}
	return sb.String()
}

// goalSteeringPrompt builds the next round's user-turn content.
func goalSteeringPrompt(condition, reason string) string {
	return fmt.Sprintf(
		"Continue working toward the goal: %s\n\n"+
			"The judge reviewed your last attempt and found it UNMET:\n%s\n\nKeep going.",
		condition, reason,
	)
}

// writeGoalVerdictTranscript writes verdict as a dedicated judge_verdict
// transcript entry (FR-056) so it can never silently disagree with the
// worker's own claim (ADR §6) — mirrors TaskExecutor.writeJudgeVerdictTranscript
// for the goal (session) scope.
func (al *AgentLoop) writeGoalVerdictTranscript(store *session.UnifiedStore, sessionID string, verdict *task.JudgeVerdict) {
	if verdict == nil {
		return
	}
	payload, merr := json.Marshal(verdict)
	if merr != nil {
		logger.WarnCF("agent", "goal loop: could not marshal judge verdict for transcript",
			map[string]any{"session_id": sessionID, "error": merr.Error()})
		return
	}
	if err := store.AppendTranscript(sessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("goal-%s-judge-%d", sessionID, verdict.Round),
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   string(payload),
		AgentID:   verdict.JudgeAgentID,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		logger.WarnCF("agent", "goal loop: judge verdict transcript write failed",
			map[string]any{"session_id": sessionID, "error": err.Error()})
	}
}

// --- Idle-expiry sweep (FR-064/D7, review r1) -----------------------------

// goalIdleExpirySweep expires any session with an active `/goal` loop idle
// for longer than its effective IdleExpiryDays bound — the `/goal`
// counterpart to plan_engine.go's PlanEngine.idleExpirySweep, driven from the
// SAME periodic tick (PlanEngine.goalAndLoopIdleExpirySweep) rather than a
// second ticker. "idle" mirrors the plan engine's own definition: no genuine
// round activity — GoalLastActivityAt is bumped on goal-set and on every
// judge round that actually ran (checkGoalLoopAfterTurn), but deliberately
// NOT on a judge-unavailability pause (R9/m4), so a permanently-unavailable
// judge still ends the loop via this calendar brake rather than looping
// forever. now is caller-supplied (PlanEngine's own injectable clock) so
// tests can pin exact idle-boundary math without a real sleep.
func (al *AgentLoop) goalIdleExpirySweep(cfg config.PlanningConfig, now time.Time) {
	store := al.GetSessionStore()
	if store == nil {
		return
	}
	sessions, err := store.ListSessions()
	if err != nil {
		logger.WarnCF("agent", "goal idle sweep: list sessions failed", map[string]any{"error": err.Error()})
		return
	}
	maxDays := cfg.EffectiveIdleExpiryDays(nil)
	for _, s := range sessions {
		if s == nil || s.GoalCondition == "" {
			continue
		}
		last := effectiveGoalActivity(s)
		if last.IsZero() {
			continue // nothing to compare against; skip rather than guess
		}
		if now.Sub(last) < time.Duration(maxDays)*24*time.Hour {
			continue
		}
		sessionID := s.ID
		handover := fmt.Sprintf(
			"Goal %q idle-expired after %d day(s) with no activity (last activity: %s).",
			s.GoalCondition, maxDays, last.Format(time.RFC3339),
		)
		al.writeGoalSystemTranscript(store, sessionID, s.ActiveAgentID, handover)
		// clearGoal Releases the "goal" R5 admission-cap slot (paired with the
		// Admit("goal") call at set time) as part of its shared body.
		al.clearGoal(sessionID, store, fmt.Sprintf("idle-expired after %d day(s)", maxDays))
	}
}

// effectiveGoalActivity returns the best available "last real activity"
// timestamp for a goal session: GoalLastActivityAt when present, falling
// back to GoalStartedAt (mirrors plan_engine.go's effectiveLastActivity).
func effectiveGoalActivity(m *session.UnifiedMeta) time.Time {
	for _, s := range []string{m.GoalLastActivityAt, m.GoalStartedAt} {
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// writeGoalSystemTranscript writes a plain system-entry note (used for the
// round-bound-reached handover, SD-B9) to the session transcript.
func (al *AgentLoop) writeGoalSystemTranscript(store *session.UnifiedStore, sessionID, agentID, content string) {
	if err := store.AppendTranscript(sessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("goal-%s-handover-%d", sessionID, time.Now().UnixNano()),
		Type:      session.EntryTypeSystem,
		Role:      "system",
		Content:   content,
		AgentID:   agentID,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		logger.WarnCF("agent", "goal loop: handover transcript write failed",
			map[string]any{"session_id": sessionID, "error": err.Error()})
	}
}
