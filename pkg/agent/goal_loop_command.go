// goal_loop_command.go: Apply a goal command prompt (/goal parsing, instant activation, marker restate, status/clear replies)

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/oklog/ulid/v2"
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

// goalConfirmNoOpArg is the ONLY thing `/goal confirm` still recognizes
// (ADR-088 D1/D9/FR-022): goals activate instantly now, so there is nothing
// left to confirm. This is a NEW, narrow verb match — never the retired
// isGoalConfirmVerb/IsGoalConfirm/confirmGoalAliases machinery — kept solely
// so the literal word "confirm" doesn't fall through to the prose path and
// activate a goal literally named "confirm" (grill B3).
const goalConfirmNoOpArg = "confirm"

// Narration lines for the engine-anchored set_goal transcript calls (ADR-082
// D9, review CR8) — the assistant-role content preceding the card. Short,
// plain, and honest about WHO authored the record: the engine, not the
// agent.
const (
	goalAnchorNarrationMarkerRegister = "Goal registered from the markers in your /goal command."
	goalAnchorNarrationMarkerRestate  = "Goal record updated from the markers in your /goal command."
	goalAnchorNarrationFallback       = "The agent did not register this goal's record itself, so the engine compiled one from the goal statement after repeated nudges. The record below is now the working assumption."
)

// applyGoalCommandPrompt is handleCommand's rewrite hook for `/goal`
// (mirrors applyMemoryCommandPrompt/applyExplicitSkillCommand's shape).
// Rewritten by ADR-088 D1 (instant activation): `/goal` (bare status),
// `/goal clear|stop|off|reset|cancel|none`, and `/goal confirm` (now a
// no-op notice, FR-022) answer SYNCHRONOUSLY (matched=true, handled=true, no
// LLM call); `/goal <intent>` — fresh OR an active-goal prose restate —
// rewrites opts.UserMessage and continues to the LLM in THIS SAME turn
// (matched=true, handled=false): starting or restating a goal IS running the
// next round, with no compile call, no clarify gate, and no confirm gate
// standing in front of it (US-1/US-5).
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

	// Gap #8/r2, R6: fail-closed origin gate. A non-user-initiated turn
	// never matches — the text passes through unchanged as ordinary chat
	// content.
	//
	// GOAL-FR-015 (E12): the explicit opts.IsTaskRun arm this gate used to
	// carry is REMOVED — not because a task run should ever activate a goal
	// through the `/goal` chat command (it never does; a task-owned goal
	// activates in task_executor.go::activateTaskGoal instead, against the
	// session the task itself mints, GOAL-FR-012), but because the
	// requirement was a request to remove it as dead, misleading
	// redundancy: opts.IsTaskRun=true (loop.go's processTaskDirect literal)
	// never carries opts.UserInitiated=true, so !opts.UserInitiated ALREADY
	// excludes every task run on its own — the separate IsTaskRun arm added
	// nothing, and its presence invited exactly the wrong reading (that
	// task-owned goals are excluded from `/goal` HERE, rather than by
	// architecture: a task session never sends a `/goal` command at all).
	if opts == nil || !opts.UserInitiated {
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
		clearAgentID := ""
		if agentInst != nil {
			clearAgentID = agentInst.ID
		}
		return true, true, al.clearGoalByUser(sessionID, store, clearAgentID)
	}
	if strings.EqualFold(strings.TrimSpace(args), goalConfirmNoOpArg) {
		// ADR-088 D1/FR-022: goals activate immediately now — there is no
		// pending state left to confirm. Without this recognizer, "confirm"
		// would fall through to the prose path below and activate a goal
		// literally named "confirm".
		return true, true, "Goals activate immediately now — just state your goal with `/goal <intent>`."
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

	if activeGoal := activeGoalForSession(sessionID); activeGoal != nil {
		// ADR-088 D1/D5 (US-5): an active goal's restate is STEERING, never a
		// pending amendment awaiting confirm. The GoalID NEVER changes on a
		// restate (FR-001) — only a fresh activation on a goalless session
		// mints one (this keeps the FR-010 question budget and the FR-014b
		// push counter attached to the SAME goal generation).
		if goalIntentNeedsLLMCompile(args) {
			// review-round-1 finding #9: patch the durable goal statement to
			// the new intent BEFORE rewriting the turn prompt — mirrors
			// applyGoalMarkerRestate's own patch exactly (same GoalID, no
			// confirm ritual, just steering). Without this, the keeper's own
			// prompts (goalNudgePrompt/goalContinuePushPrompt), `/goal
			// status`, and the D7 fallback compile all read the record's
			// Prompt as this goal's intent — every one of them would keep
			// citing the SUPERSEDED pre-restate intent forever, even though
			// the CURRENT turn (opts.UserMessage, below) sees the new one.
			//
			// ADR-086: the statement lives on the goal record's own Prompt
			// field, not the retired session-meta GoalCondition.
			//
			// UAT E-1: the patch used to touch Prompt ONLY, leaving the
			// Definition/Criteria/DoD compiled for the PREVIOUS intent in
			// place. The record then described two different pieces of work
			// (new prompt, old ladder), the agent worked the new intent
			// without re-registering, and the Judge adjudicated its claim
			// against the stale ladder. goal.Restate supersedes the old
			// ladder into history and returns the record to ADR-088 D1's
			// recordless state, so the working agent registers a record for
			// the NEW intent (the post-turn registration nudge fires if it
			// does not) and, until it does, the Judge judges the new prompt
			// itself — never the old criteria.
			newCondition := strings.TrimSpace(args)
			now := time.Now().UTC()
			var superseded bool
			restated, err := resolveGoalRecordStore().Update(activeGoal.GoalID, func(cur *goal.Goal) error {
				changed, rerr := cur.Restate(newCondition, newFloorDoD(), now)
				superseded = changed
				return rerr
			})
			if err != nil {
				logger.WarnCF("agent", "goal: could not persist prose-restated condition",
					map[string]any{"session_id": sessionID, "goal_id": activeGoal.GoalID, "error": err.Error()})
				return true, true, "Could not update the goal (internal error persisting the goal record)."
			}
			if superseded {
				// A claim against the superseded definition may already be
				// under adjudication; its verdict judged the old ladder and
				// must not land on the restated record (runGoalAdjudication
				// also discards such a verdict if the cancel loses the race).
				if pe := GetPlanEngine(al); pe != nil {
					al.cancelGoalVerifierIfAny(pe, sessionID)
				}
				// No goal-status frame here: a prose restate emits none (every
				// frame reads PlanEngine.Admit for its snapshot, and a restate
				// must not re-Admit — TestGoalActivation_InstantProsePath). The
				// restate turn's own after-turn hook emits the active frame
				// carrying the new prompt.
				logger.InfoCF("agent", "goal: prose restate superseded the compiled record; the agent must register one for the new intent",
					map[string]any{"component": "goal", "session_id": sessionID, "goal_id": restated.GoalID,
						"superseded_revisions": len(restated.SupersededCriteria)})
			}
			// Rewrite the turn's working prompt exactly like a fresh
			// activation does — the working agent updates the record itself
			// via set_goal(mode: update) once it runs (wave 2). No LLM call
			// here, no pending state.
			opts.UserMessage = newCondition
			return true, false, ""
		}
		// Marker-only restate: deterministic, zero LLM calls, the feasibility
		// veto still applies — the update lands on the SAME goal generation.
		res := compileGoalIntent(args, fc, sessionID)
		if res.Rejection != nil {
			return true, true, formatCompileRejection(res.Rejection)
		}
		return true, true, al.applyGoalMarkerRestate(sessionID, store, activeGoal, res.Goal)
	}

	// No active goal: a fresh `/goal <intent>` supersedes any AskUserQuestion
	// card still parked from an earlier attempt (E1/S-32) — cancelled WITHOUT
	// dispatching a resume turn (FR-028; the re-homed cancelOrphanedClarifyCard).
	al.cancelOrphanedClarifyCard(al.getAskUserRegistry(), sessionID)

	if goalIntentNeedsLLMCompile(args) {
		// ADR-088 D1 (US-1): instant activation. Admit ONCE (FR-003), mint the
		// goal id, write the ACTIVE record up front with GoalCriteriaJSON
		// EMPTY — the working agent authors the record itself via `set_goal`
		// as its first move (wave 2's forced two-door mechanism; this is D3's
		// forcing predicate's legal transient state) — and continue the turn
		// into round 1 in THIS SAME turn. No compile call, no clarify, no
		// confirm gate (C-1: zero LLM calls before the first working request).
		if pe := GetPlanEngine(al); pe != nil {
			if admitted, active, capN := pe.Admit("goal"); !admitted {
				return true, true, fmt.Sprintf(
					"Cannot start a new goal: active loops %d/%d (cap reached). "+
						"Stop an existing /goal or /loop, or wait for a running plan to finish.",
					active, capN,
				)
			}
		}
		if err := al.activateInstantGoal(sessionID, opts, agentInst, args); err != nil {
			logger.WarnCF("agent", "goal: failed to persist instant goal activation",
				map[string]any{"session_id": sessionID, "error": err.Error()})
			return true, true, "Could not start the goal loop (internal error persisting session state)."
		}
		return true, false, ""
	}

	// Marker-only intent (every criterion from explicit markers — US-3 S3):
	// today's path PINNED unchanged (FR-002 — byte-identical). Deterministic
	// compile, immediate activation, same-turn round 1, zero LLM calls. The
	// compiled goal is echoed via the goal_status frame and the persisted
	// GoalCriteriaJSON (the S1 unified record). Admit to the R5 cap first.
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
	// UAT S3 fix: a fresh goal (no active goal record, checked above) always
	// mints a NEW goal-id generation — this is what gives the second `/goal`
	// after a clear its own distinct pill/history entry instead of collapsing
	// into the first goal's bucket.
	goalID := newGoalID()
	// GOAL-FR-009/FR-010/FR-011: mint AND activate the durable pkg/goal
	// record — see activateInstantGoal's identical call for the full
	// rationale. Unlike the instant-activation path, the marker compile
	// already produced real criteria/dod, so both carry through here rather
	// than starting empty.
	//
	// ADR-086 (S6): the session-meta mirror this block used to write
	// alongside the record (GoalID/GoalCondition/GoalCriteriaJSON/
	// GoalRoundsUsed/GoalMaxRounds/GoalLatestReason/GoalStartedAt/
	// GoalLastActivityAt/GoalQuestionRoundsUsed/GoalZeroOutputPushes) is
	// GONE — those fields no longer exist on session.UnifiedMeta and the
	// record below is now the single durable copy. goal.New itself seeds
	// Round/AttemptsUsed/QuestionRoundsUsed/ZeroOutputPushes at 0 and
	// Activate stamps StartedAt, so review-round-1 finding #5's "a fresh
	// goal generation gets fresh budgets" is structural now — a brand-new
	// record cannot inherit a previous generation's spent counters.
	dod := compiled.DoD
	if len(dod) == 0 {
		dod = newFloorDoD()
	}
	if err := al.createAndActivateSessionGoalRecord(goalID, sessionID, condition, compiled.Criteria, dod, maxRounds); err != nil {
		logger.WarnCF("agent", "goal: failed to persist goal set",
			map[string]any{"session_id": sessionID, "goal_id": goalID, "error": err.Error()})
		return true, true, "Could not start the goal loop (internal error persisting the goal record)."
	}

	// ADR-053 Phase-2 §1: capture the chat routing BEFORE the write-side
	// effect below — afterGoalRecordWrite's FR-020 channel echo reads the
	// RECORDED route (goalTriggers().routeFor), so it must already be set
	// for a channel-origin marker goal to get its echo on this very write
	// (also lets a later idle-settlement unmet verdict re-inject a steering
	// turn via the async-notifier — the idle path has no turnResult to
	// attach a followUp to).
	routeAgentID := ""
	if agentInst != nil {
		routeAgentID = agentInst.ID
	}
	al.recordGoalRouting(sessionID, goalID, opts.Channel, opts.ChatID, opts.SessionKey, routeAgentID)

	// review-round-1 finding #8: route the marker-path activation frame
	// through the SAME post-write path set_goal uses (afterGoalRecordWrite)
	// instead of the bare emitGoalStatusFrame — the marker compile already
	// produced a full criteria/dod ladder (unlike the D1 instant-activation
	// path's legitimately-empty transient record), so the frame should
	// carry it (FR-113/FR-019), and a channel-routed marker goal gets its
	// FR-020 echo exactly like a set_goal-authored one.
	al.afterGoalRecordWrite(sessionID, criteriaJSON, "")

	// ADR-082 D9 (review CR8): this write never ran the set_goal tool, and
	// under D9 the record card renders ONLY from a set_goal call's own
	// result at the call's position — the frame above feeds the pill and
	// the live overlay, never the card. Anchor the record as a synthetic
	// set_goal call (transcript entry + live start/end frames) so a marker-
	// activated goal gets its card exactly like a set_goal-authored one.
	// Non-fatal: the record is already durable; a failed anchor is logged
	// inside anchorGoalRecordInTranscript.
	_, _ = al.anchorGoalRecordInTranscript(goalRecordAnchor{
		store: store, sessionID: sessionID, goalID: goalID, agentID: routeAgentID, chatID: opts.ChatID,
		mode:      tools.SetGoalModeRegister,
		narration: goalAnchorNarrationMarkerRegister,
		record:    compiled,
		assumptions: []string{
			"Record compiled deterministically by the engine from the explicit markers in the /goal command, not authored by the agent.",
		},
	})

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

// activateInstantGoal is ADR-088 D1's instant-activation write (US-1): mints
// the goal id, writes the ACTIVE record up front with GoalCriteriaJSON
// EMPTY — the working agent authors the record itself via `set_goal` as its
// first move (wave 2's forced two-door mechanism; this is D3's forcing
// predicate's legal transient state) — records the chat routing, and
// rewrites the turn's working prompt to the raw intent so the turn
// continues into round 1 in THIS SAME turn: no compile call, no clarify, no
// confirm gate. The caller has already run the ONE Admit for this activation
// (FR-003) before calling this.
func (al *AgentLoop) activateInstantGoal(
	sessionID string, opts *processOptions, agentInst *AgentInstance, intent string,
) error {
	maxRounds := config.DefaultGoalMaxRounds
	if cfg := al.GetConfig(); cfg != nil {
		maxRounds = cfg.Planning.EffectiveGoalMaxRounds()
	}
	goalID := newGoalID()
	// GOAL-FR-009/FR-010/FR-011: mint AND activate the durable pkg/goal
	// record in the same instant-activation write — a chat goal collapses
	// the defining and active phases into one turn (ADR-088 D1). Criteria is
	// empty here by design (the working agent authors it via set_goal as its
	// own first move, D3's forcing predicate); dod is the built-in floor
	// layer. Without this, set_goal's WriteRecord (goal_record_wiring.go)
	// has no active record to write against on this session's very first
	// set_goal call.
	//
	// ADR-086 (S6): the session-meta mirror that used to precede this call
	// is GONE — the record is the single durable copy, and a failure to
	// write it now fails the activation outright rather than leaving a goal
	// that only session meta believed in. Fresh generation = fresh budgets
	// is structural: goal.New seeds every counter at 0.
	if err := al.createAndActivateSessionGoalRecord(goalID, sessionID, intent, nil, newFloorDoD(), maxRounds); err != nil {
		return fmt.Errorf("goal: instant activation: %w", err)
	}
	al.emitGoalStatusFrame(sessionID, goalID, intent, 0, maxRounds, "", goalPillActive)
	routeAgentID := ""
	if agentInst != nil {
		routeAgentID = agentInst.ID
	}
	al.recordGoalRouting(sessionID, goalID, opts.Channel, opts.ChatID, opts.SessionKey, routeAgentID)
	opts.UserMessage = intent
	return nil
}

// createAndActivateSessionGoalRecord is GOAL-FR-009/FR-010/FR-011's
// chat-side activation write (E12): a chat `/goal` collapses the defining
// and active phases into one turn (ADR-088 D1's instant activation,
// unchanged by this wave) — unlike a task-owned goal, which is created in
// the defining phase well before it ever runs and only activated later
// (task_executor.go::activateTaskGoal), a chat goal has to be minted AND
// activated in the SAME call, so this does both.
//
// ADR-086 (S6): this is no longer best-effort. It used to be, because the
// caller's own session-meta write (GoalCondition and friends) was what every
// other reader in this package actually keyed off, so a missing pkg/goal
// record merely degraded the `set_goal` path. Those session-meta fields are
// deleted now and this record is the ONLY durable copy of the goal, so a
// failure here means there is no goal at all — it is returned to the caller,
// which surfaces it to the user and does NOT continue the turn as if a goal
// had started.
func (al *AgentLoop) createAndActivateSessionGoalRecord(
	goalID, sessionID, intent string, criteria, dod []task.AcceptanceCriterion, maxRounds int,
) error {
	if goalID == "" || sessionID == "" {
		return fmt.Errorf("goal: activation requires both a goal id and a session id (goal_id=%q session_id=%q)", goalID, sessionID)
	}
	now := time.Now().UTC()
	g, gerr := goal.New(generated.GoalOwnerKindSession, sessionID, generated.GoalSourceChatCompiled,
		intent, "", criteria, dod, maxRounds, now)
	if gerr != nil {
		return fmt.Errorf("goal: could not build the durable goal record at activation: %w", gerr)
	}
	g.GoalID = goalID
	gstore := resolveGoalRecordStore()
	if cerr := gstore.Create(g); cerr != nil {
		return fmt.Errorf("goal: could not create the durable goal record at activation: %w", cerr)
	}
	if _, aerr := gstore.Update(goalID, func(cur *goal.Goal) error {
		return cur.Activate(sessionID, time.Now().UTC())
	}); aerr != nil {
		return fmt.Errorf("goal: could not activate the durable goal record: %w", aerr)
	}
	return nil
}

// applyGoalMarkerRestate applies a deterministic marker-only restate to an
// ALREADY-ACTIVE goal (ADR-088 D1/US-5 S19): the record updates in place —
// same GoalID, same rounds/started-at, no confirm ritual. diffGoalAmendment
// is wired into set_goal's own update path in a later wave; this restate
// simply lands the new record.
func (al *AgentLoop) applyGoalMarkerRestate(
	sessionID string, store *session.UnifiedStore, rec *goal.Goal, compiled *CompiledGoal,
) string {
	condition := compiled.Prompt
	if condition == "" {
		condition = compiled.Intent
	}
	criteriaJSON, merr := marshalCompiledGoal(compiled)
	if merr != nil {
		logger.WarnCF("agent", "goal: could not marshal restated criteria",
			map[string]any{"session_id": sessionID, "error": merr.Error()})
		return "Could not update the goal (internal error)."
	}
	// The PRIOR record, captured before the write below, is the "old" side of
	// the diff summary (ADR-086: read from the goal record, not the retired
	// session-meta GoalCriteriaJSON copy).
	priorRecordJSON := goalRecordCompiledJSON(rec)
	now := time.Now().UTC()
	dod := compiled.DoD
	if len(dod) == 0 {
		dod = newFloorDoD()
	}
	if _, err := resolveGoalRecordStore().Update(rec.GoalID, func(cur *goal.Goal) error {
		cur.Prompt = condition
		cur.Definition = compiled.Definition
		if serr := cur.SetCriteria(compiled.Criteria, now); serr != nil {
			return serr
		}
		return cur.SetDoD(dod, now)
	}); err != nil {
		logger.WarnCF("agent", "goal: could not persist restated goal",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "error": err.Error()})
		return "Could not update the goal (internal error persisting the goal record)."
	}
	// review-round-1 finding #8: route the marker-path restate frame through
	// the SAME post-write path set_goal(mode:update) uses (afterGoalRecordWrite)
	// instead of the bare emitGoalStatusFrame — the restated record carries a
	// full criteria/dod ladder just like set_goal's own update, so the frame
	// should carry it too, and a channel-routed marker goal gets its FR-020
	// echo on a restate exactly like it does on a fresh marker activation.
	// priorRecordJSON above is the PRIOR record (this function's rec param
	// was read before the store write above) — same diff-summary shape
	// set_goal's own update path logs.
	al.afterGoalRecordWrite(sessionID, criteriaJSON, goalRecordDiffAdapter(priorRecordJSON, criteriaJSON))
	// ADR-082 D9 (review CR8): anchor the amended record as a synthetic
	// set_goal(mode:update) call at THIS position — the amend card renders
	// where the amendment happened (D9), exactly as a tool-authored update
	// does. The chat routing was recorded at activation (recordGoalRouting);
	// the agent falls back to the session's active agent when the route
	// carries none.
	route := goalTriggers().routeFor(sessionID)
	anchorAgentID := route.agentID
	if anchorAgentID == "" {
		if meta, merr := store.GetMeta(sessionID); merr == nil && meta != nil {
			anchorAgentID = meta.ActiveAgentID
		}
	}
	_, _ = al.anchorGoalRecordInTranscript(goalRecordAnchor{
		store: store, sessionID: sessionID, goalID: rec.GoalID, agentID: anchorAgentID, chatID: route.chatID,
		mode:      tools.SetGoalModeUpdate,
		narration: goalAnchorNarrationMarkerRestate,
		record:    compiled,
		assumptions: []string{
			"Record re-compiled deterministically by the engine from the explicit markers in the /goal restate, not authored by the agent.",
		},
	})
	return fmt.Sprintf("Goal updated: %s\nAcceptance criteria: %d.", condition, len(compiled.Criteria))
}

// goalStatusReply formats `/goal status`'s deterministic reply (FR-069/
// FR-029): condition, elapsed wall-clock, rounds_used/bound, cumulative
// token spend (visible-only, NFR-1 — never used to stop the loop), latest
// judge reason, active loops: N/cap, and — REWRITTEN by ADR-088 (E10/S-39)
// — the record summary (statement + criteria + DoD) once the working agent
// has registered one via `set_goal`. The old pending-draft/clarification
// branches are gone (there is no more pending state to report); an
// instant-activated goal legitimately carries an empty GoalCriteriaJSON
// until its first move registers a record (D3's transient state), so status
// simply omits the record summary until then.
func (al *AgentLoop) goalStatusReply(sessionID string, store *session.UnifiedStore) string {
	rec := activeGoalForSession(sessionID)
	if rec == nil {
		return "No active goal on this session. Use `/goal <condition>` to start one."
	}
	active, capN := al.activeLoopsSnapshot("goal")
	elapsed := "unknown"
	if rec.StartedAt != nil && !rec.StartedAt.IsZero() {
		elapsed = time.Since(*rec.StartedAt).Round(time.Second).String()
	}
	reason := rec.LatestReason
	if reason == "" {
		reason = "(no round completed yet)"
	}
	// Token spend stays a SESSION statistic (session.UnifiedMeta.Stats) —
	// ADR-086 relocated the goal's own fields onto the goal record, not the
	// session's accounting.
	var tokensTotal int
	var cost float64
	if meta, merr := store.GetMeta(sessionID); merr == nil && meta != nil {
		tokensTotal = meta.Stats.TokensTotal
		cost = meta.Stats.Cost
	}
	status := fmt.Sprintf(
		"Goal: %s\nElapsed: %s\nRounds: %d/%d\nToken spend (session, visible-only): %d tokens ($%.4f)\n"+
			"Latest judge reason: %s\nActive loops: %d/%d",
		rec.Prompt, elapsed, rec.Round, rec.MaxRounds,
		tokensTotal, cost, reason, active, capN,
	)
	if compiled := compiledGoalFromRecord(rec); compiled != nil {
		status += "\n\n" + formatGoalEcho(compiled)
	}
	return status
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
