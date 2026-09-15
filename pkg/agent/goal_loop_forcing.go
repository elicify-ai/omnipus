// goal_loop_forcing.go: Goal forcing during a turn (rubric notes, question-round budget, forced next move)

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- moved from loop.go 2026-09-15 ---

// goalTurnRecordState reads the turn session's current goal state from
// pkg/goal — the single read both evaluateGoalForcing (every
// iteration since the D3 amendment, 2026-09-08 — no longer iteration==1
// only) and the mid-turn rubric-note budget estimate (goalRubricNoteForBudget,
// every iteration) share, so the two can never disagree about what "the
// record is still empty" means. holds is
// ADR-088 D3's base predicate: an active goal whose compiled record is still
// empty — the transient window between instant activation (D1) and the
// working agent's own set_goal authorship. rec is nil whenever holds is
// false.
//
// DD-6 (round-6 production blocker, ADR-086, fixed by wave E13): "the
// compiled record is still empty" used to be read straight off session
// meta's GoalCriteriaJSON — but a set_goal TOOL call's WriteRecord
// (goal_record_wiring.go, wave E4) was re-pointed onto pkg/goal.Store and
// writes NO session meta at all (GOAL-FR-004/FR-005), so a real working
// agent that registered its record via the tool left this session-meta
// field permanently empty: the narrowed first-move door never lifted for
// the rest of the goal. Fixed by reading the SAME pkg/goal-backed accessor
// set_goal itself uses (agentLoopGoalRecordAccess.ReadGoalState,
// goal_record_wiring.go) — recordJSON is "" exactly when the session's
// ACTIVE goal record's own criteria list is still empty, whether that
// record was activated via the /goal command (goal_loop.go) or a task run
// (task_executor.go::activateTaskGoal). The REJECTED alternative — making
// WriteRecord mirror the criteria back onto session meta — would reinstate
// precisely the dual-write ADR-086 exists to delete; not implemented here.
//
// ADR-086 (S6) completes that re-point: the "is this a goal turn at all"
// half used to read session meta's GoalCondition, which no longer exists.
// Both halves now come from ONE lookup of the ACTIVE goal record bound to
// this session (activeGoalForSession, goal_record_wiring.go) — its
// existence answers the first question and its own criteria list answers
// the second, so the two can no longer disagree even in principle, and the
// predicate covers a task-owned goal as well as a chat-owned one.
func goalTurnRecordState(al *AgentLoop, ts *turnState) (holds bool, rec *goal.Goal) {
	if al == nil || ts == nil || ts.opts.TranscriptStore == nil || ts.opts.TranscriptSessionID == "" {
		return false, nil
	}
	g := activeGoalForSession(ts.opts.TranscriptSessionID)
	if g == nil {
		return false, nil
	}
	if len(g.Criteria) > 0 {
		return false, nil
	}
	return true, g
}

// goalForcingNarrowTools returns the ADR-088 D3 Layer 1 narrowed tool pair:
// set_goal (always, when present in policyFiltered) plus AskUserQuestion
// when includeAsk is true and it too is present. Never any other tool —
// C-3's "1 or 2 definitions, never any other tool".
func goalForcingNarrowTools(policyFiltered []tools.Tool, includeAsk bool) []tools.Tool {
	out := make([]tools.Tool, 0, 2)
	for _, t := range policyFiltered {
		switch t.Name() {
		case tools.SetGoalToolName:
			out = append(out, t)
		case tools.AskUserQuestionToolName:
			if includeAsk {
				out = append(out, t)
			}
		}
	}
	return out
}

// goalForcingWebChannel is the SPA session origin (ADR-088 D3 [G-B2]) —
// mirrors pkg/tools' own unexported webChannelName const
// (ask_user_question.go), which pkg/agent cannot reach without a
// cross-package coupling for one literal. AskUserQuestion's own web-only
// refusal (ToolChannel(ctx) != webChannelName) is the authority this
// predicate must agree with, so both sides carry the identical value.
const goalForcingWebChannel = "webchat"

// goalForcingDecision is ADR-088 D3/D4's per-request verdict, evaluated once
// at the top of each LLM request inside runTurn's round loop (spec
// FR-007/009/010/011, test 8) and consumed by that SAME iteration:
// providerToolDefs assembly, the rubric-note injection, and — after the
// tool-execution loop processes the model's response — the FR-010
// question-round budget bump when the ask door was genuinely taken.
//
// D3 AMENDMENT (2026-09-07, ADR-088): provider tool-choice forcing is
// DELETED — a goal turn on z-ai/glm-5v-turbo failed with `status=400 "Tool
// choice must be auto" (Z.AI)`, and Z.AI/GLM is the operator's primary
// provider family. Determinism no longer comes from the request shape
// (narrow-and-force); it comes from the ENGINE noticing a skipped first
// move and correcting it on the very next turn (checkGoalLoopAfterTurn's
// immediate post-turn nudge, goal_loop.go). layer1 here now means ONLY
// "the tool surface was narrowed to {set_goal[, AskUserQuestion]}" — never
// "and the model was forced to call one of them". Narrowing is provider-
// agnostic (it is just the tools array offered), so the old CLI-bridged-
// provider exclusion (isCLIBridgedProvider/ToolChoiceForcingCapable) is
// gone too — narrowing applies identically on every provider now.
type goalForcingDecision struct {
	// layer1 is true when the request's tool surface was narrowed to
	// {set_goal[, AskUserQuestion]}: the base predicate holds and set_goal
	// itself is not policy-denied. No tool-choice is ever forced (D3
	// amendment) — a model offered the narrowed pair remains free to answer
	// in plain text; the immediate post-turn correction (goal_loop.go) is
	// what catches that case, not this request's shape.
	layer1 bool
	// rubric is true whenever D3's base predicate holds at all (active goal
	// AND an empty compiled record, this turn's first LLM request) —
	// independent of channel/provider/policy. D4's rubric note injects
	// under this alone; layer1 implies rubric, never the reverse.
	rubric bool
	// askOffered is true when this request's narrowed pair still includes
	// AskUserQuestion (layer1 && webchat origin && the question budget is
	// unspent && policy allows it) — read after the tool-execution loop to
	// know the ask door was actually reachable this request.
	askOffered bool
	// isWebchat records ts.channel == goalForcingWebChannel once so the
	// rubric-note builder and downstream logging need not re-derive it.
	// AskUserQuestion is permanently web-only [G-B2] — isWebchat gates
	// whether it is INCLUDED in the narrowed pair, never whether narrowing
	// itself applies (narrowing now applies on every origin).
	isWebchat bool
	sessionID string
	goalID    string
	// questionRoundsUsed is the persisted budget counter AS READ this
	// evaluation — bumpGoalQuestionRoundsUsed increments from this value,
	// never a re-read, so a concurrent unrelated write between evaluation
	// and the bump cannot silently double-count (the single-flight goal
	// session assumption every other goal-state writer in this package
	// already makes).
	questionRoundsUsed int
	// narrowed is the actual {set_goal[, AskUserQuestion]} slice offered
	// this request when layer1 is true — nil otherwise. providerToolDefs is
	// built directly from this slice (never re-derived), so what the model
	// is offered and what askOffered/layer1 describe can never disagree.
	narrowed []tools.Tool
}

// goalForcingMaxNarrowAttempts bounds ADR-088 D3's narrowed first-move door
// (the D3 amendment, 2026-09-08): once a turn has offered the narrowed
// {set_goal[, AskUserQuestion]} pair this many CONSECUTIVE times without
// either a successful set_goal write or a genuinely parked AskUserQuestion
// card, evaluateGoalForcing releases the door (full tool surface, WARN
// logged) instead of narrowing again — see goalNarrowEscaped's doc comment
// on turnState (turn.go) for the exact counting rule and the real-world
// defect (11:54:48Z→12:12:26Z, a 17-minute wedged turn) this escape exists
// to prevent. N=3: enough for a model to recover from one transient
// schema-validation slip (the observed defect) or two, without letting a
// persistently broken/uncooperative model consume the whole MaxIterations
// budget stuck in the narrowed pair — the post-turn nudge ladder
// (checkGoalLoopAfterTurn, goal_loop.go D6c) is the backstop once this fires.
const goalForcingMaxNarrowAttempts = 3

// evaluateGoalForcing computes goalForcingDecision for the CURRENT LLM
// request (ADR-088 D3 as amended 2026-09-07, further amended 2026-09-08 —
// see goalForcingMaxNarrowAttempts; spec FR-007/009/010; C-3's negative
// rows, grill M1): the predicate deliberately consults ONLY iteration
// (logging only — see below), persisted session state, and this turn's own
// narrow-attempt/escape counters — never opts.UserInitiated or sender
// identity, since a card-resume turn (human-answered or auto-submitted) and
// a keeper nudge turn are goal turns exactly like a fresh activation turn.
//
// D3 AMENDMENT (2026-09-08): narrowing used to apply to the turn's FIRST LLM
// request ONLY (iteration==1), on the theory that the narrowed pair's own
// two outcomes (register or park) never leave a second request with the
// predicate still true in the same turn. That theory missed a third
// outcome: a narrowed call that FAILS (tool-arg validation error, policy
// denial at execution, or an error result) neither registers nor parks, so
// the predicate is STILL true on iteration 2 — and used to get the FULL
// unnarrowed tool surface back while the goal record stayed empty. Real
// evidence: a /goal set at 11:54:48Z narrowed iteration 1; the model's
// AskUserQuestion call failed schema validation ("unexpected property
// \"recommended\"" inside an option); iteration 2 onward ran unnarrowed —
// ToolSearch, write_file×5, bash, serve_web, browser_navigate — for ~17
// minutes before the agent finally called set_goal at 12:12:26Z, because the
// turn never ended for the post-turn correction to catch it. The door now
// stays narrowed for EVERY request while the predicate holds — iteration is
// no longer a gate, only a log field — bounded by goalForcingMaxNarrowAttempts
// so a persistently-failing model cannot wedge the turn instead.
func (al *AgentLoop) evaluateGoalForcing(
	ts *turnState, iteration int, policyFiltered []tools.Tool,
) goalForcingDecision {
	var d goalForcingDecision
	holds, rec := goalTurnRecordState(al, ts)
	if !holds {
		// Covers both "not a goal turn at all" and "a PRIOR request's
		// set_goal already wrote the record" — goalTurnRecordState reads
		// persisted state fresh on every call, so a successful write between
		// iteration N and N+1 is what naturally releases the door here; no
		// escape-counter bookkeeping is needed for this branch.
		return d
	}
	if ts.goalNarrowIsEscaped() {
		// The bounded escape already fired earlier this turn (see
		// goalForcingMaxNarrowAttempts) — stay released for the rest of the
		// turn even though the base predicate still holds. Do not re-arm:
		// the rubric note (D4) keeps nudging, but the tool surface is not
		// narrowed again.
		d.rubric = true
		d.sessionID = ts.opts.TranscriptSessionID
		d.goalID = rec.GoalID
		d.questionRoundsUsed = rec.QuestionRoundsUsed
		d.isWebchat = ts.channel == goalForcingWebChannel
		return d
	}
	d.rubric = true
	d.sessionID = ts.opts.TranscriptSessionID
	d.goalID = rec.GoalID
	d.questionRoundsUsed = rec.QuestionRoundsUsed
	d.isWebchat = ts.channel == goalForcingWebChannel

	setGoalAllowed, askAllowed := false, false
	for _, t := range policyFiltered {
		switch t.Name() {
		case tools.SetGoalToolName:
			setGoalAllowed = true
		case tools.AskUserQuestionToolName:
			askAllowed = true
		}
	}
	if !setGoalAllowed {
		// D3: "if set_goal itself is policy-denied, do NO narrowing and log
		// WARN" — checked specifically for set_goal, independent of whether
		// AskUserQuestion alone would have made the intersection non-empty.
		// This request is NOT counted as a narrowed attempt (it was never
		// narrowed) and does not advance goalNarrowMisses.
		logger.WarnCF("agent", "goal: narrowing skipped — set_goal is policy-denied for this agent",
			map[string]any{"component": "goal", "session_id": d.sessionID, "goal_id": d.goalID, "agent_id": ts.agent.ID, "iteration": iteration})
		return d
	}

	// [G-B2]: AskUserQuestion is permanently web-only — included in the
	// narrowed pair ONLY on a webchat origin with the question budget
	// unspent. On a channel or keeper (Channel:"system") origin the pair
	// degrades to {set_goal} alone (never the empty set): narrowing itself
	// is provider/channel-agnostic since the D3 amendment deleted tool-
	// choice forcing, so there is no reason to skip it off-web anymore —
	// only the ask door is origin-gated.
	includeAsk := d.isWebchat && askAllowed && d.questionRoundsUsed < 1
	narrowed := goalForcingNarrowTools(policyFiltered, includeAsk)

	// Bounded escape (goalForcingMaxNarrowAttempts): this request is about
	// to become another CONSECUTIVE narrowed offering. Count it BEFORE
	// deciding whether to actually narrow — a request that instead exits
	// above (record already written, or the escape already armed) never
	// reaches this bump, so the counter only ever measures genuine
	// narrowed-but-unresolved attempts. Once the count exceeds the bound,
	// this (and every later) request in the turn gets the FULL surface
	// instead.
	attempt := ts.noteGoalNarrowAttempt()
	if attempt > goalForcingMaxNarrowAttempts {
		ts.armGoalNarrowEscape()
		logger.WarnCF("agent", "goal: bounded escape — releasing the narrowed first-move door after repeated unresolved narrowed requests",
			map[string]any{
				"component": "goal", "session_id": d.sessionID, "goal_id": d.goalID,
				"attempts": attempt - 1, "max_attempts": goalForcingMaxNarrowAttempts, "iteration": iteration,
			})
		return d
	}

	d.narrowed = narrowed
	d.layer1 = true
	// askOffered is recomputed from the ACTUAL narrowed slice rather than
	// trusted from includeAsk alone, so it can never disagree with what
	// providerToolDefs (built from this same slice) actually offers.
	for _, t := range d.narrowed {
		if t.Name() == tools.AskUserQuestionToolName {
			d.askOffered = true
		}
	}

	logger.InfoCF("agent", "goal: first-move door narrowed",
		map[string]any{
			"component": "goal", "session_id": d.sessionID, "goal_id": d.goalID,
			"ask_offered": d.askOffered, "channel": ts.channel, "is_webchat": d.isWebchat,
			"iteration": iteration, "attempt": attempt,
		})
	return d
}

// bumpGoalQuestionRoundsUsed persists FR-010's spent question-round budget
// (per GoalID — a restate never re-mints the GoalID, so it correctly
// inherits an already-spent budget). Best-effort: a persistence failure is
// WARN-logged, never turn-fatal — the card has already parked the turn by
// the time this runs.
func (al *AgentLoop) bumpGoalQuestionRoundsUsed(d goalForcingDecision) {
	if d.sessionID == "" {
		return
	}
	// ADR-086 (GOAL-FR-004): the question-round budget is the goal record's
	// own QuestionRoundsUsed counter, relocated off the retired session-meta
	// GoalQuestionRoundsUsed field — which is also what keeps it attached to
	// the GOAL generation (a restate never re-mints the record) rather than
	// to the session.
	if d.goalID == "" {
		logger.WarnCF("agent", "goal: could not persist the spent question-round budget — no goal id on the forcing decision",
			map[string]any{"component": "goal", "session_id": d.sessionID})
		return
	}
	newCount := d.questionRoundsUsed + 1
	if _, err := resolveGoalRecordStore().Update(d.goalID, func(cur *goal.Goal) error {
		cur.QuestionRoundsUsed = newCount
		return nil
	}); err != nil {
		logger.WarnCF("agent", "goal: could not persist the spent question-round budget",
			map[string]any{"component": "goal", "session_id": d.sessionID, "goal_id": d.goalID, "error": err.Error()})
		return
	}
	logger.InfoCF("agent", "goal: question door taken; budget spent",
		map[string]any{"component": "goal", "session_id": d.sessionID, "goal_id": d.goalID, "rounds_used": newCount})
}

// goalRubricNoteForBudget re-derives buildGoalRubricInjectionNote's input
// from persisted session state (ADR-088 D4, midturn_budget.go's
// ephemeralSystemNoteTokens). Before the 2026-09-08 D3 amendment,
// evaluateGoalForcing's own rubric flag was gated to the turn's first
// request only (iteration==1), while mid-turn budget checks run AFTER that
// first request is already assembled and sent — so this function
// deliberately ignored that gate and re-evaluated the base predicate
// unconditionally, a conservative OVER-estimate on iteration ≥ 2 (never an
// under-estimate). Since the amendment, evaluateGoalForcing's rubric flag is
// no longer iteration-gated either (it tracks goalTurnRecordState across the
// whole turn, exactly like this function) — so the two now normally AGREE
// rather than this one merely over-estimating. This function is kept
// re-deriving independently rather than threading evaluateGoalForcing's
// per-request decision through the mid-turn call chain (matching how every
// OTHER ephemeral note in that enumeration is measured), and remains safe
// either way: it can only ever match or over-estimate, never under-count.
func (al *AgentLoop) goalRubricNoteForBudget(ts *turnState) string {
	holds, _ := goalTurnRecordState(al, ts)
	isWebchat := ts != nil && ts.channel == goalForcingWebChannel
	return buildGoalRubricInjectionNote(holds, isWebchat)
}
