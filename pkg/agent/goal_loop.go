// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_loop.go implements `/goal <condition>` (ADR-049 D6/D7, spec Part B
// US-8): a proof-driven session loop where a round = one worker turn plus
// its judge evaluation (D7/MIN-001). Command parsing/persistence lives in
// applyGoalCommandPrompt (the loop.go:9582 handleCommand rewrite-hook
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

	// Set/replace a goal (FR-067/068 — one active goal per session,
	// replace-on-set).
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
	condition := args
	nowStr := time.Now().UTC().Format(time.RFC3339)
	zero := 0
	emptyReason := ""
	if err := store.SetMeta(sessionID, session.MetaPatch{
		GoalCondition:    &condition,
		GoalRoundsUsed:   &zero,
		GoalMaxRounds:    &maxRounds,
		GoalLatestReason: &emptyReason,
		GoalStartedAt:    &nowStr,
	}); err != nil {
		logger.WarnCF("agent", "goal: failed to persist goal set",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return true, true, "Could not start the goal loop (internal error persisting session state)."
	}

	al.emitGoalStatusFrame(sessionID, condition, 0, maxRounds, "", "active")

	opts.UserMessage = condition
	return true, false, ""
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
	hadGoal := err == nil && meta != nil && meta.GoalCondition != ""

	empty := ""
	zero := 0
	if serr := store.SetMeta(sessionID, session.MetaPatch{
		GoalCondition:    &empty,
		GoalRoundsUsed:   &zero,
		GoalMaxRounds:    &zero,
		GoalLatestReason: &empty,
		GoalStartedAt:    &empty,
	}); serr != nil {
		logger.WarnCF("agent", "goal: failed to clear goal state",
			map[string]any{"session_id": sessionID, "error": serr.Error()})
	}
	if !hadGoal {
		return "No active goal to clear."
	}
	al.emitGoalStatusFrame(sessionID, "", 0, 0, "", "cleared")
	return "Goal cleared (" + note + ")."
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

// checkGoalLoopAfterTurn is US-8's judge-gated round-advance hook (ADR-049
// D6/D7, FR-067..070). Called once, synchronously, from runAgentLoop right
// after every natural (non-aborted) turn stop — a fast no-op unless the
// turn's session carries an active goal. On an unmet verdict with rounds
// remaining, it appends a follow-up bus.InboundMessage to result.followUps
// (the existing turn re-inject seam, loop.go:5832) carrying the judge's
// reason as steering — runAgentLoop's own followUps-publish loop then
// re-enters the turn machinery for the next round.
func (al *AgentLoop) checkGoalLoopAfterTurn(
	ctx context.Context,
	agentInst *AgentInstance,
	opts processOptions,
	result *turnResult,
) {
	if result == nil || opts.IsTaskRun || opts.TranscriptStore == nil || opts.TranscriptSessionID == "" {
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

	criterion := task.AcceptanceCriterion{
		ID:     "goal-condition",
		Kind:   task.KindProse,
		Text:   meta.GoalCondition,
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: sessionID},
		Status: task.CritPending,
	}
	attempt := meta.GoalRoundsUsed + 1

	jr := al.JudgeCriteria(ctx, JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		AssigneeAgentID: agentInst.ID,
		Criteria:        []task.AcceptanceCriterion{criterion},
		Attempt:         attempt,
		ClaimText:       result.finalContent,
	})

	if jr.Unavailable {
		// D7: judge unavailable — attempt/round NOT consumed, no verdict
		// recorded, idle clock untouched (the goal stays exactly as it was;
		// the next natural turn on this session re-evaluates the same round).
		logger.WarnCF("agent", "goal loop: judge unavailable, round not consumed",
			map[string]any{"session_id": sessionID, "reason": jr.Reason})
		return
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
		Sender:     bus.SenderInfo{CanonicalID: "system:goal_loop"},
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
