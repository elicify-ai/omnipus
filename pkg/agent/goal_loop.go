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

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newGoalID mints a stable per-generation goal identifier (ADR-053 R§8.11,
// UAT S3 fix): a ULID prefixed "goal_", mirroring session.NewSessionID's
// "session_"-prefixed convention and the "goal_01J3ZQK8N2H8VXNRP5T7C9M4WU"
// example in contracts/components/schemas/Goal.yaml. Called exactly once per
// goal generation — when a goal activates from empty (fresh `/goal
// <condition>` or a confirmed fresh pending goal) — never per-frame; a
// fabricated per-frame id would be worse than no id at all (the SPA's
// GoalPillTray keys one pill per goal-id, so a stable id is load-bearing).
func newGoalID() string {
	return "goal_" + ulid.Make().String()
}

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
		return true, true, al.clearGoal(sessionID, store, goalClearNoteUser)
	}
	if isGoalConfirmVerb(args) {
		// US-3 S9: while a clarification question is pending, nothing is
		// confirmable yet — `/goal confirm` gets an informative redirect,
		// never "No pending goal to confirm".
		if meta, _ := store.GetMeta(sessionID); meta != nil &&
			loadGoalClarification(meta.GoalClarificationJSON) != nil {
			return true, true, "Answer the pending question first (or `/goal clear` to discard the goal draft)."
		}
		reply, startCondition := al.confirmPendingGoal(sessionID, store)
		if startCondition != "" {
			// A FRESH pending goal just activated (ADR-074 D4a): run round 1
			// in this same turn — the confirm is the activation, exactly like
			// the marker-only path's own same-turn round 1 (US-3 S1).
			routeAgentID := ""
			if agentInst != nil {
				routeAgentID = agentInst.ID
			}
			al.recordGoalRouting(sessionID, opts.Channel, opts.ChatID, opts.SessionKey, routeAgentID)
			opts.UserMessage = startCondition
			return true, false, ""
		}
		return true, true, reply
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

	// Re-statement amendment gate (N-6/D11, FR-113): a `/goal <new intent>`
	// issued while a goal is ALREADY active is diffed as an amendment and
	// confirmed via `/goal confirm` — NEVER silently recompiled. The active
	// goal's Condition + GoalCriteriaJSON are untouched while pending.
	// ADR-074 D4a keeps this path DETERMINISTIC this phase (US-3 S8) — no LLM
	// call on an active-goal restate.
	if meta, _ := store.GetMeta(sessionID); meta != nil && meta.GoalCondition != "" {
		res := compileGoalIntent(args, fc, sessionID)
		if res.Rejection != nil {
			return true, true, formatCompileRejection(res.Rejection)
		}
		return true, true, al.proposeGoalAmendment(sessionID, store, meta, res.Goal)
	}

	// ADR-074 D4a (US-3 S1/S2): a prose or mixed intent takes the two-phase
	// LLM path — admission CHECK first (a capped user never pays for a refused
	// compile, S6; the authoritative Admit still runs at confirm), then the
	// bounded compile producing a PENDING goal (or one clarifying question, or
	// a plain-language rejection). Activation happens ONLY on confirm. A fresh
	// `/goal <intent>` also supersedes any pending compile or clarification
	// from an earlier attempt (US-3 S9, R2-10).
	if goalIntentNeedsLLMCompile(args) {
		if pe := GetPlanEngine(al); pe != nil {
			if admitted, active, capN := pe.Admit("goal"); !admitted {
				return true, true, fmt.Sprintf(
					"Cannot start a new goal: active loops %d/%d (cap reached). "+
						"Stop an existing /goal or /loop, or wait for a running plan to finish.",
					active, capN,
				)
			}
		}
		outcome := al.compileGoalIntentLLM(ctx, agentInst, fc, args, sessionID, "", "", opts.WorkspaceID)
		return true, true, al.applyGoalCompileOutcome(sessionID, store, args, outcome)
	}

	// Marker-only intent (every criterion from explicit markers — US-3 S3):
	// today's path PINNED unchanged. Deterministic compile, immediate
	// activation, same-turn round 1, zero LLM calls. The `/goal` command IS
	// the chat confirmation here (FR-113, narrowed to marker-only by ADR-074
	// D4a); the compiled goal is echoed via the goal_status frame and the
	// persisted GoalCriteriaJSON (the S1 unified record). Admit to the R5 cap
	// first.
	res := compileGoalIntent(args, fc, sessionID)
	if res.Rejection != nil {
		// Fail-closed: no rejected criterion persists (FR-111). Surface the
		// reason in chat so the owner can re-state.
		return true, true, formatCompileRejection(res.Rejection)
	}
	compiled := res.Goal

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
	// UAT S3 fix: a fresh goal (no active GoalCondition, checked above) always
	// mints a NEW goal-id generation — this is what gives the second `/goal`
	// after a clear its own distinct pill/history entry instead of collapsing
	// into the first goal's bucket.
	goalID := newGoalID()
	if err := store.SetMeta(sessionID, session.MetaPatch{
		GoalID:                &goalID,
		GoalCondition:         &condition,
		GoalCriteriaJSON:      &criteriaJSON,
		GoalPendingJSON:       &emptyPending,
		GoalClarificationJSON: &emptyPending,
		GoalRoundsUsed:        &zero,
		GoalMaxRounds:         &maxRounds,
		GoalLatestReason:      &emptyReason,
		GoalStartedAt:         &nowStr,
		GoalLastActivityAt:    &nowStr,
	}); err != nil {
		logger.WarnCF("agent", "goal: failed to persist goal set",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return true, true, "Could not start the goal loop (internal error persisting session state)."
	}

	// The goal_status frame carries the compiled criteria as the echo (the SPA
	// renders the active goal + its ladder). This is the FR-113 "echoed in chat".
	al.emitGoalStatusFrame(sessionID, goalID, condition, 0, maxRounds, "", goalPillActive)

	// ADR-053 Phase-2 §1: capture the chat routing so a later idle-settlement
	// unmet verdict can re-inject a steering turn via the async-notifier (the
	// idle path has no turnResult to attach a followUp to).
	routeAgentID := ""
	if agentInst != nil {
		routeAgentID = agentInst.ID
	}
	al.recordGoalRouting(sessionID, opts.Channel, opts.ChatID, opts.SessionKey, routeAgentID)

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

// applyGoalCompileOutcome lands one D4a compile outcome (initial or resumed)
// on the session: a clarifying question becomes the pending-clarification
// record; a rejection is surfaced plain-language (nothing persists); compiled
// criteria become a PENDING goal awaiting `/goal confirm` (US-3 S1) with the
// itemized plain-language echo as the reply — the FR-113 confirmation surface.
// Every branch clears the clarification record (the question round, if any,
// is spent) and stamps GoalLastActivityAt so the idle-expiry sweep covers the
// pending state (US-3 S10).
func (al *AgentLoop) applyGoalCompileOutcome(
	sessionID string, store *session.UnifiedStore, intent string, outcome llmGoalCompileOutcome,
) string {
	empty := ""
	nowStr := time.Now().UTC().Format(time.RFC3339)

	if outcome.ClarifyingQuestion != "" {
		record := &goalClarificationRecord{
			Intent: intent, Question: outcome.ClarifyingQuestion, AskedAt: nowStr,
		}
		recordJSON, merr := marshalGoalClarification(record)
		if merr != nil {
			logger.WarnCF("agent", "goal: could not marshal clarification record",
				map[string]any{"session_id": sessionID, "error": merr.Error()})
			return "Could not record the clarifying question (internal error). Please restate the goal."
		}
		if err := store.SetMeta(sessionID, session.MetaPatch{
			GoalClarificationJSON: &recordJSON,
			GoalPendingJSON:       &empty, // a question supersedes any earlier pending compile
			GoalLastActivityAt:    &nowStr,
		}); err != nil {
			logger.WarnCF("agent", "goal: could not persist clarification record",
				map[string]any{"session_id": sessionID, "error": err.Error()})
			return "Could not record the clarifying question (internal error). Please restate the goal."
		}
		return "Before I lock this goal in, one question:\n\n" + outcome.ClarifyingQuestion +
			"\n\nAnswer in chat, or `/goal clear` to discard the draft."
	}

	if outcome.Result.Rejection != nil {
		// Fail-closed: nothing persists — and any clarification record is
		// spent (US-3 S7: one round, answered or not).
		if err := store.SetMeta(sessionID, session.MetaPatch{
			GoalClarificationJSON: &empty,
			GoalPendingJSON:       &empty,
		}); err != nil {
			logger.WarnCF("agent", "goal: could not clear pending state after compile rejection",
				map[string]any{"session_id": sessionID, "error": err.Error()})
		}
		return formatCompileRejection(outcome.Result.Rejection)
	}

	compiled := outcome.Result.Goal
	pendingJSON, merr := marshalCompiledGoal(compiled)
	if merr != nil {
		logger.WarnCF("agent", "goal: could not marshal pending compiled goal",
			map[string]any{"session_id": sessionID, "error": merr.Error()})
		return "Could not store the compiled goal (internal error). Please restate it."
	}
	if err := store.SetMeta(sessionID, session.MetaPatch{
		GoalPendingJSON:       &pendingJSON,
		GoalClarificationJSON: &empty,
		GoalLastActivityAt:    &nowStr,
	}); err != nil {
		logger.WarnCF("agent", "goal: could not persist pending compiled goal",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return "Could not store the compiled goal (internal error). Please restate it."
	}

	// The pending state occupies the `queued` pill (ADR-074 D4a: compiled,
	// awaiting user confirmation — not yet admitted). No goal-id yet: the
	// generation is minted at confirm (newGoalID's own contract). The frame
	// carries the compiled criteria breakdown (D5.2/FR-011) AND, per
	// ADR-080 D-STATEMENT/D-DOD, the restated Definition and the DoD
	// breakdown, so the SPA's confirmation card renders exactly what
	// formatGoalEcho's chat echo shows.
	condition := compiled.Prompt
	if condition == "" {
		condition = compiled.Intent
	}
	maxRounds := config.DefaultGoalMaxRounds
	if cfg := al.GetConfig(); cfg != nil {
		maxRounds = cfg.Planning.EffectiveGoalMaxRounds()
	}
	al.emitGoalStatusFrameWithCriteriaAndDoD(
		sessionID, "", condition, 0, maxRounds, "", goalPillQueued, compiled.Definition, compiled.Criteria, compiled.DoD,
	)

	echo := formatGoalEcho(compiled)
	if outcome.UsedFallback {
		echo += goalEchoFallbackNote
	}
	return echo
}

// applyGoalPendingReply is the ADR-074 D4a pre-LLM reply-routing hook (US-3
// S9): called from processMessage right after handleCommand, it intercepts
// BARE (non-slash) messages only when the session carries a pending goal
// state. Taxonomy:
//
//   - Pending-clarification: the next ordinary chat message — whatever it
//     says, confirm-words included — IS the answer, feeding ONE resumed
//     compile (with its own single repair, FR-007).
//   - Pending-confirm: a bare message that is exactly one confirm token
//     (confirmGoalAliases) activates; a fresh activation rewrites the turn
//     into round 1 (handled=false + opts.UserMessage). ANY other bare message
//     passes through as ordinary chat and the pending goal stays pending — a
//     routine chat message never silently mutates goal state.
//
// Slash commands never reach this hook's branches (handleCommand owns them);
// the same fail-closed origin gate as applyGoalCommandPrompt applies.
//
// ADR-078 D3: the terminal `return false, ""` fall-through below is
// DELIBERATELY kept as-is — a non-confirm reply must never itself recompile
// or mutate goal state. What changed is what happens AFTER this hook returns
// false with GoalPendingJSON still set: the turn continues into
// runAgentLoop → runTurn, where buildGoalPendingNote/injectGoalPendingNote
// (goal_pending_note.go) reads that same still-set GoalPendingJSON and
// injects it as a per-turn ephemeral system note, so the model is no longer
// context-blind about the pending goal on that turn. This router stays the
// single authority on deterministic state transitions (confirm/clarify
// only); the injector is a separate, additive context-awareness mechanism.
func (al *AgentLoop) applyGoalPendingReply(
	ctx context.Context,
	msg bus.InboundMessage,
	agentInst *AgentInstance,
	opts *processOptions,
) (handled bool, reply string) {
	if opts == nil || !opts.UserInitiated || opts.IsTaskRun {
		return false, ""
	}
	if opts.TranscriptStore == nil || opts.TranscriptSessionID == "" {
		return false, ""
	}
	if commands.HasCommandPrefix(msg.Content) {
		return false, ""
	}
	store := opts.TranscriptStore
	sessionID := opts.TranscriptSessionID
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return false, ""
	}

	if clar := loadGoalClarification(meta.GoalClarificationJSON); clar != nil {
		var fc FeasibilityContext
		if agentInst != nil {
			fc = agentFeasibilityContext{agentInst: agentInst}
		}
		answer := strings.TrimSpace(msg.Content)
		outcome := al.compileGoalIntentLLM(ctx, agentInst, fc, clar.Intent, sessionID, clar.Question, answer, opts.WorkspaceID)
		return true, al.applyGoalCompileOutcome(sessionID, store, clar.Intent, outcome)
	}

	if strings.TrimSpace(meta.GoalPendingJSON) != "" && IsGoalConfirm(msg.Content) {
		confirmReply, startCondition := al.confirmPendingGoal(sessionID, store)
		if startCondition != "" {
			routeAgentID := ""
			if agentInst != nil {
				routeAgentID = agentInst.ID
			}
			al.recordGoalRouting(sessionID, opts.Channel, opts.ChatID, opts.SessionKey, routeAgentID)
			opts.UserMessage = startCondition
			return false, "" // turn continues into round 1 with the condition
		}
		return true, confirmReply
	}

	return false, ""
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
// goal) on `/goal confirm` or a bare confirm token (FR-113/D11, extended by
// ADR-074 D4a: the fresh-set prose path now parks its compile here as a
// pending goal, so this is reachable from the fresh path too — not just
// amendments). Mints a new goal generation: the proposed Condition +
// GoalCriteriaJSON take effect, GoalPendingJSON clears, and GoalRoundsUsed
// resets to 0 (the amended criteria are a fresh verification target — R§8.1
// "re-statement AMENDS to a new goal generation").
//
// Returns (reply, startCondition): startCondition is non-empty ONLY when a
// FRESH pending goal just activated (no goal was active before) — the caller
// then rewrites the turn to run round 1 with that condition (US-3 S1
// "activation only on confirmation → round 1"); an amendment confirm returns
// ("...", "") and answers synchronously (the goal is already running).
func (al *AgentLoop) confirmPendingGoal(sessionID string, store *session.UnifiedStore) (string, string) {
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil || strings.TrimSpace(meta.GoalPendingJSON) == "" {
		return "No pending goal to confirm. Use `/goal <intent>` to start one.", ""
	}
	pending := loadCompiledGoal(meta.GoalPendingJSON)
	if pending == nil {
		// Malformed pending — clear it rather than leave a stuck state.
		empty := ""
		_ = store.SetMeta(sessionID, session.MetaPatch{GoalPendingJSON: &empty})
		return "The pending goal could not be read; it was cleared. Please restate with `/goal <intent>`.", ""
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
	// UAT S3 fix: a fresh pending goal (no active GoalCondition) mints a NEW
	// goal-id generation, exactly like the fresh-activation branch in
	// applyGoalCommandPrompt. An AMENDMENT (a goal already active) keeps its
	// existing goal-id — it is the SAME goal being refined, not a new one; the
	// pill/history entry for this generation continues, it does not restart.
	goalID := meta.GoalID
	fresh := meta.GoalCondition == ""
	if fresh {
		// Activating a fresh pending goal — the authoritative Admit runs HERE
		// (US-3 S6: the pre-compile check in applyGoalCommandPrompt was only a
		// courtesy refusal before the compile spend).
		if pe := GetPlanEngine(al); pe != nil {
			if admitted, active, capN := pe.Admit("goal"); !admitted {
				return fmt.Sprintf(
					"Cannot activate the goal: active loops %d/%d (cap reached).", active, capN), ""
			}
		}
		goalID = newGoalID()
	}
	if serr := store.SetMeta(sessionID, session.MetaPatch{
		GoalID:                &goalID,
		GoalCondition:         &condition,
		GoalCriteriaJSON:      &criteriaJSON,
		GoalPendingJSON:       &emptyPending,
		GoalClarificationJSON: &emptyPending,
		GoalRoundsUsed:        &zero,
		GoalMaxRounds:         &maxRounds,
		GoalLatestReason:      &emptyReason,
		GoalStartedAt:         &nowStr,
		GoalLastActivityAt:    &nowStr,
	}); serr != nil {
		logger.WarnCF("agent", "goal: could not apply confirmed amendment",
			map[string]any{"session_id": sessionID, "error": serr.Error()})
		return "Could not activate the goal (internal error persisting session state).", ""
	}
	al.emitGoalStatusFrame(sessionID, goalID, condition, 0, maxRounds, "", goalPillActive)
	if fresh {
		// The caller rewrites the turn to run round 1 with the condition —
		// the goal_status frame above is the activation surface in the UI.
		return "", condition
	}
	return fmt.Sprintf("Goal amended: %s\nAcceptance criteria: %d.", condition, len(pending.Criteria)), ""
}

// goalStatusReply formats `/goal status`'s deterministic reply (FR-069):
// condition, elapsed wall-clock, rounds_used/bound, cumulative token spend
// (visible-only, NFR-1 — never used to stop the loop), latest judge reason,
// and active loops: N/cap.
func (al *AgentLoop) goalStatusReply(sessionID string, store *session.UnifiedStore) string {
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return "No active goal on this session. Use `/goal <condition>` to start one."
	}
	if meta.GoalCondition == "" {
		// ADR-074 D4a pending states (US-3 S10): a `/goal` status during a
		// pending compile must report it, never "No active goal".
		if clar := loadGoalClarification(meta.GoalClarificationJSON); clar != nil {
			return fmt.Sprintf(
				"Goal compile waiting for your answer.\nIntent: %s\nQuestion: %s\n"+
					"Answer in chat, restate with `/goal <intent>`, or `/goal clear` to discard.",
				clar.Intent, clar.Question)
		}
		if pending := loadCompiledGoal(meta.GoalPendingJSON); pending != nil {
			cond := pending.Prompt
			if cond == "" {
				cond = pending.Intent
			}
			return fmt.Sprintf(
				"Goal pending your confirmation: %s\nAcceptance criteria: %d.\n"+
					"Reply **%s** (or `/goal confirm`) to activate, `/goal <new intent>` to restate, "+
					"or `/goal clear` to discard.",
				cond, len(pending.Criteria), ConfirmGoalWord)
		}
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

// goalClearNoteMet / goalClearNoteUser are the two `clearGoal` note literals
// clearGoal itself pattern-matches to pick a pill state (below) — named so
// every call site and the match live in exactly one place instead of two
// independently-typed string literals drifting apart.
const (
	goalClearNoteMet  = "condition met"
	goalClearNoteUser = "cleared by user"
)

// clearGoal clears the session's goal fields (FR-070's shared body for
// `/goal clear` + aliases, the bound-reached brake, and the task/plan card
// Clear button's future REST equivalent). Returns the user-facing reply.
//
// Terminal pill state (ADR-053 R§8.10 pill enum, extended by the UAT S3 fix
// with a 9th "cleared" value):
//   - note == goalClearNoteMet ("condition met") → state "done" (terminal success)
//   - note == goalClearNoteUser ("cleared by user", the ONLY user-initiated
//     path — applyGoalCommandPrompt's `/goal clear|stop|off|reset|cancel|none`)
//     → state "cleared": a deliberate, successful user action is NOT a
//     failure and must not paint the pill as one (UAT S3 — the prior
//     "collapse into failed" behavior flipped a red X badge for an
//     intentional stop; see contracts/components/schemas/GoalStatusFrame.yaml
//     for the enum extension this required per Constraint #8).
//   - anything else (round bound reached, budget exhausted, idle-expired) →
//     state "failed": a genuine terminal failure, not a user choice.
func (al *AgentLoop) clearGoal(sessionID string, store *session.UnifiedStore, note string) string {
	meta, err := store.GetMeta(sessionID)
	// FR-114 (N-12): /goal clear cancels the in-flight verifier AND any in-flight
	// compilation (a pending amendment, a pending fresh goal whose
	// GoalCondition isn't set yet, or an ADR-074 D4a pending-clarification
	// record). hadGoal is true when there is an active goal OR a pending
	// compilation/clarification to discard.
	hadGoal := err == nil && meta != nil &&
		(meta.GoalCondition != "" || meta.GoalPendingJSON != "" ||
			meta.GoalCriteriaJSON != "" || meta.GoalClarificationJSON != "")

	// Capture goal-id + condition + rounds BEFORE clearing so the terminal
	// pill frame still carries the id/text the user was watching (UAT S3: the
	// frame that announces a goal's end must identify WHICH goal ended).
	goalID := ""
	condition := ""
	rounds, maxRounds := 0, 0
	if meta != nil {
		goalID = meta.GoalID
		condition = meta.GoalCondition
		rounds = meta.GoalRoundsUsed
		maxRounds = meta.GoalMaxRounds
	}
	pillState := goalPillFailed
	switch note {
	case goalClearNoteMet:
		pillState = goalPillDone
	case goalClearNoteUser:
		pillState = goalPillCleared
	}

	empty := ""
	zero := 0
	serr := store.SetMeta(sessionID, session.MetaPatch{
		GoalID:                &empty,
		GoalCondition:         &empty,
		GoalCriteriaJSON:      &empty,
		GoalPendingJSON:       &empty,
		GoalClarificationJSON: &empty,
		GoalRoundsUsed:        &zero,
		GoalMaxRounds:         &zero,
		GoalLatestReason:      &empty,
		GoalStartedAt:         &empty,
		GoalLastActivityAt:    &empty,
	})
	if serr != nil {
		// SetMeta failed — the on-disk goal fields are still set. Do NOT
		// emit the terminal "cleared" pill (the user would see a cleared
		// status while the durable state still has the live goal), and do
		// NOT release the verifier / clear the trigger state (that would
		// diverge the in-memory surface from the still-set on-disk state).
		// Let the next turn re-drive the clear. (Fix B.7 — silent-failure
		// hunter #11: prior code logged the warning but proceeded as if the
		// clear succeeded.)
		logger.WarnCF("agent", "goal: failed to clear goal state — skipping terminal side effects; next turn will re-drive clear",
			map[string]any{"session_id": sessionID, "error": serr.Error()})
		return "Goal clear deferred (SetMeta failed — will retry on next turn)."
	}
	if !hadGoal {
		return "No active goal to clear."
	}
	if pe := GetPlanEngine(al); pe != nil {
		pe.Release("goal") // paired with the Admit("goal") call at set time
		al.cancelGoalVerifierIfAny(pe, sessionID)
	}
	// ADR-053 Phase-2 §1 (FR-114/N-12): reset the in-memory trigger surface so
	// a later stray GOAL_STATUS: met is inert (no active goal to adjudicate
	// against), the waiting_on_user pause clears, and the bounce streak / idle
	// re-arm marker / routing entry all drop.
	al.clearGoalTriggerState(sessionID)
	al.emitGoalStatusFrame(sessionID, goalID, condition, rounds, maxRounds, note, pillState)
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
	_, armed, err := al.RequestCancelForSession(cancelCtx, verifierSessionID, "", "")
	switch {
	case err != nil:
		logger.WarnCF("agent", "goal: could not cancel in-flight goal verifier session",
			map[string]any{"session_id": sessionID, "verifier_session_id": verifierSessionID, "error": err.Error()})
	case armed:
		// No turn was registered yet for the verifier session at the moment
		// `/goal clear` ran — a pre-registration cancel latch
		// (cancel_prearm.go) now stands in for this cancel and will fire the
		// instant that turn registers (within cancelPreArmTTL). Not a
		// failure, just deferred; Debug (not Warn) because
		// RequestCancelForSession's own OnLatchExpired hook (cancel.go)
		// already gives an operator-visible Warn if the latch itself later
		// expires unconsumed.
		logger.DebugCF("agent", "goal: clear armed a pre-registration cancel latch for the in-flight goal verifier session",
			map[string]any{"session_id": sessionID, "verifier_session_id": verifierSessionID})
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
// EmitGoalStatusChanged. goalID is the stable per-generation identifier
// (UAT S3 fix, ADR-053 R§8.11) — empty for a legacy pre-upgrade goal that
// never had one minted, in which case the wire frame simply omits goal_id
// (it is OPTIONAL per GoalStatusFrame.yaml).
func (al *AgentLoop) emitGoalStatusFrame(sessionID, goalID, condition string, round, maxRounds int, reason, state string) {
	al.emitGoalStatusFrameWithCriteria(sessionID, goalID, condition, round, maxRounds, reason, state, nil)
}

// emitGoalStatusFrameWithCriteria is emitGoalStatusFrame plus the compiled
// criteria breakdown (ADR-074 D5.2 / FR-011): the `queued` pending-confirm
// emission carries the itemized criteria so GoalThreadTailCards' echo card
// shows exactly what will run; every other emission passes nil.
func (al *AgentLoop) emitGoalStatusFrameWithCriteria(sessionID, goalID, condition string, round, maxRounds int, reason, state string, criteria []task.AcceptanceCriterion) {
	al.emitGoalStatusFrameWithCriteriaAndDoD(sessionID, goalID, condition, round, maxRounds, reason, state, "", criteria, nil)
}

// emitGoalStatusFrameWithCriteriaAndDoD is emitGoalStatusFrameWithCriteria
// plus ADR-080's `definition` (D-STATEMENT) and `dod` (D-DOD) breakdown: the
// `queued` pending-confirm emission is the ONLY call site that passes a
// non-empty definition/dod (goal_loop.go's applyGoalCompileOutcome) — every
// other emission passes "" / nil, exactly like criteria above, so the
// confirm card renders the same statement + criteria + DoD the chat echo
// (formatGoalEcho) does.
func (al *AgentLoop) emitGoalStatusFrameWithCriteriaAndDoD(
	sessionID, goalID, condition string, round, maxRounds int, reason, state, definition string,
	criteria, dod []task.AcceptanceCriterion,
) {
	active, capN := al.activeLoopsSnapshot("goal")
	al.EmitGoalStatusChanged(GoalStatusChangedPayload{
		SessionID:    sessionID,
		GoalID:       goalID,
		Condition:    condition,
		Round:        round,
		MaxRounds:    maxRounds,
		LatestReason: reason,
		ActiveLoops:  active,
		Cap:          capN,
		State:        state,
		Definition:   definition,
		Criteria:     criteria,
		DoD:          dod,
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
	// askuserquestion-tool-spec §0.7 (M-R2-5): TurnEndStatusParked is NOT a
	// natural turn stop — a parked clarification/compile turn (AskUserQuestion,
	// or any future ParksTurn tool) never advances the goal round, invokes the
	// Judge, or re-dispatches. The gate lives here, at the function's entry.
	if result.status == TurnEndStatusParked {
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

	// ADR-053 Phase-2 §1 (FR-101): the Judge fires ONLY on (a) an explicit
	// completion claim ([goal:evidence] + GOAL_STATUS: met) or (b)
	// event-driven idle settlement — NEVER after every worker turn (the
	// superseded ADR-052 defect). This turn-stop hook implements the CLAIM
	// path and the waiting_on_user pause; the IDLE path runs from the
	// PlanEngine tick (goalQuietWindowSettle, driven via goalIdleExpirySweep).
	// Parse the S6 typed marker once (parser co-located with the evidence
	// gate in task_completion_signal.go, FR-199).
	marker := parseGoalStatusMarker(result.finalContent)

	// G-5 resume (US-2 AS-3): a genuine user reply (UserInitiated) to a
	// waiting_on_user goal clears the pause and re-arms the idle timer. The
	// user's turn itself is the new activity; it is NOT itself a claim, so
	// fall through to the classification below rather than judging here. The
	// goal loop's own re-injected follow-up (SenderID == goalLoopFollowUpSenderID)
	// does NOT clear the pause — only a real user message resumes.
	if opts.UserInitiated && al.goalIsWaitingOnUser(sessionID) {
		al.goalSetWaitingOnUser(sessionID, false)
		al.bumpGoalActivityOnTurn(store, sessionID)
	}

	switch {
	case marker.Present && marker.Status == goalStatusWaitingOnUser:
		// FR-104 / G-5: typed pause. NO verdict, NO round consumed; idle
		// settlement SUPPRESSED while the pause holds (goalIsWaitingOnUser,
		// checked in maybeSettleGoalIdle). Explicit Non-Behavior: only this
		// typed marker counts — never a prose classifier.
		al.goalSetWaitingOnUser(sessionID, true)
		al.emitGoalStatusFrame(sessionID, meta.GoalID, meta.GoalCondition, meta.GoalRoundsUsed,
			meta.GoalMaxRounds, "waiting on user", goalPillWaitingOnUser)
		return

	case marker.Present && marker.Status == goalStatusMet && marker.HasEvidence:
		// G-1: explicit completion claim WITH evidence → invoke the Judge
		// EXACTLY once (INV-1, via the shared runGoalAdjudication body).
		//
		// corr-MAJOR-3 (claim-path F5 self-race, G-1): the IDLE path already
		// guards a double-Judge via goalAdjudicationInFlight; the CLAIM path
		// did NOT. A concurrent idle-tick + claim-turn would race TWO Judge
		// LLM turns (G-1 "exactly once" violated), clobber GoalRoundsUsed, and
		// race clearGoal/Unregister. If an idle adjudication is already
		// in-flight for this goal, DROP this claim — the in-flight idle
		// adjudication will resolve the goal (it reads the same persisted
		// evidence, G-3). The verifier-registry's CAS Register is the second
		// layer of defense (corr-MAJOR-3b), catching any residual race this
		// check-then-act guard misses.
		if al.goalAdjudicationInFlight(sessionID) {
			logger.InfoCF("agent", "goal claim: adjudication already in-flight; dropping claim (idle path will resolve)",
				map[string]any{"session_id": sessionID})
			return
		}
		// Clear any bounce streak (the worker satisfied the evidence gate).
		// ClaimText = the worker's whole turn output (placed LAST in the
		// judge's input ordering per ClaimText semantics); the adjudication
		// re-arms the idle timer via its own activity bump.
		al.clearGoalBareClaimStreak(sessionID)
		al.runGoalAdjudication(ctx, agentInst, opts.WorkspaceID, sessionID, store, meta,
			result.finalContent,
			func(steer string) {
				// Claim-path steer re-inject: the existing turn re-inject seam
				// (result.followUps, republished by runAgentLoop). Content
				// deliberately does not start with "/goal"; UserInitiated is
				// left false (Gap #8). An unmet verdict's steer re-dispatch
				// IS new activity (G-2).
				result.followUps = append(result.followUps, bus.InboundMessage{
					Channel:    opts.Channel,
					ChatID:     opts.ChatID,
					Sender:     bus.SenderInfo{CanonicalID: goalLoopFollowUpSenderID},
					Content:    steer,
					SessionID:  sessionID,
					SessionKey: opts.SessionKey,
				})
			})
		return

	case marker.Present && marker.Status == goalStatusMet:
		// G-4: bare claim (GOAL_STATUS: met with NO [goal:evidence]). Bounce
		// economics — 1st free (teaching steer), 2nd costs a round. NEVER
		// invokes the Judge (nothing to judge). Claiming stays cheaper than
		// idling (D8/N-13).
		al.handleBareGoalClaim(ctx, agentInst, opts, store, sessionID, meta, result)
		return

	default:
		// No claim marker and not waiting: an ordinary worker turn. Bump the
		// activity clock (re-arm the idle quiet window, FR-102) and clear any
		// already-settled marker (G-2 re-arm). Do NOT judge — the idle path
		// adjudicates accumulated evidence after the quiet window (FR-101(b),
		// G-3). An UNRECOGNIZED marker value also lands here: the
		// deterministic not-a-claim-not-a-pause fallback (FR-104 AS-2 — no
		// marker means not-waiting, and by symmetry not a claim either).
		al.bumpGoalActivityOnTurn(store, sessionID)
		al.emitGoalStatusFrame(sessionID, meta.GoalID, meta.GoalCondition, meta.GoalRoundsUsed,
			meta.GoalMaxRounds, meta.GoalLatestReason, goalPillActive)
	}
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
	if err := store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("goal-%s-judge-%d", sessionID, verdict.Round),
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   string(payload),
		AgentID:   verdict.JudgeAgentID,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		taskGoalTranscriptWriteFailures.Add(1)
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
		// ADR-074 D4a (US-3 S10): the sweep's empty-condition skip is extended
		// to cover the pending states — a compiled-but-unconfirmed goal and a
		// pending-clarification record expire on the SAME TTL policy as an
		// active goal (both stamp GoalLastActivityAt when created).
		if s == nil || (s.GoalCondition == "" && s.GoalPendingJSON == "" && s.GoalClarificationJSON == "") {
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
		label := s.GoalCondition
		if label == "" {
			label = "(pending — never confirmed)"
		}
		handover := fmt.Sprintf(
			"Goal %q idle-expired after %d day(s) with no activity (last activity: %s).",
			label, maxDays, last.Format(time.RFC3339),
		)
		al.writeGoalSystemTranscript(store, sessionID, s.ActiveAgentID, handover)
		// clearGoal Releases the "goal" R5 admission-cap slot (paired with the
		// Admit("goal") call at set time) as part of its shared body.
		al.clearGoal(sessionID, store, fmt.Sprintf("idle-expired after %d day(s)", maxDays))
	}

	// ADR-053 Phase-2 §1 (FR-102/G-2/G-3): the ~60 s quiet-window idle
	// settlement — distinct from the multi-DAY calendar brake above — fires
	// ONE claimless adjudication per goal-id whose quiet window elapsed.
	// Same tick driver (DoD-11: one periodic driver for all goal sweeps).
	al.goalQuietWindowSettle(now)
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
	if err := store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("goal-%s-handover-%d", sessionID, time.Now().UnixNano()),
		Type:      session.EntryTypeSystem,
		Role:      "system",
		Content:   content,
		AgentID:   agentID,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("agent", "goal loop: handover transcript write failed",
			map[string]any{"session_id": sessionID, "error": err.Error()})
	}
}
