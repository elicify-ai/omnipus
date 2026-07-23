// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_triggers.go implements ADR-053 Phase-2 §1/§2 claim-or-idle triggering,
// waiting_on_user pause, bounce economics, and the 8-state pill transitions
// (FR-101..FR-105/FR-107/FR-109, acceptance G-1..G-5; folded findings
// F5/N-3/N-11/N-13). It SUPERSEDES ADR-052's after-every-turn adjudication:
// the Judge now fires ONLY on (a) an explicit completion claim or (b)
// event-driven idle settlement — never after every worker turn.
//
// This file owns the per-goal-id in-memory trigger state and the shared
// adjudication body that both the claim path (checkGoalLoopAfterTurn, in
// goal_loop.go) and the idle-settlement path (goalQuietWindowSettlement, in
// this file) call. The durable goal record (GoalCondition / GoalRoundsUsed /
// GoalLastActivityAt) lives on the session meta and is read/written via the
// existing session.UnifiedStore — the SAME spine goal_loop.go/goal_compile.go
// already use (DoD-11 anti-drift: no second goal store).
//
// goal-id ⇄ session-id: the S1 substrate carries ONE GoalCondition per session
// (pkg/session/daypartition.go), so a goal-id is 1:1 with the session-id of
// the chat carrying it. FR-107's "a session with N goals runs N independent
// settlements" is therefore realized as N sessions each carrying one goal,
// each consuming its own global-cap slot (FR-108) — the per-goal-id keying
// here (bounce streak / waiting flag / routing / idle-in-flight marker) is the
// mechanism that keeps two concurrent goals independent.
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- Pill state constants (D14 crosswalk, FR / Pill-state enum 8) ---------
//
// The 8-state pill emitted on the goal_status WS frame. The durable lifecycle
// authority (S2 8-state enum) is owned elsewhere; these pill values are the
// DISPLAY overlay reconstructed from lifecycle + engine phase. The two
// ephemeral engine-phase pills (judging / judge_unavailable) and the
// waiting_on_user pause are what THIS file drives; re-planning is a durable
// plan_phase overlay (C1) owned by the plan-owner loop.
const (
	goalPillQueued           = "queued"
	goalPillActive           = "active"
	goalPillWaitingOnUser    = "waiting_on_user"
	goalPillJudgeUnavailable = "judge_unavailable"
	goalPillRePlanning       = "re-planning"
	goalPillJudging          = "judging"
	goalPillDone             = "done"
	goalPillFailed           = "failed"
)

// goalIdleQuietWindow is the ~60 s idle quiet window (FR-102). A goal-bearing
// session with no genuine activity for this long — and not waiting-on-user,
// not already adjudicating — fires ONE idle adjudication. The PlanEngine tick
// (defaultPlanEngineTickInterval = 30 s) drives the sweep, so this fires on
// the first tick after the window elapses. A var so tests substitute a short
// window without a real 60 s wait.
var goalIdleQuietWindow = 60 * time.Second //nolint:gochecknoglobals

// goalBareClaimCostThreshold is N in G-4's "the Nth consecutive bare claim
// costs a round" (mirrors task_executor.go's evidenceGateMaxConsecutiveRejections
// = 2). The 1st bare claim (streak < threshold) is free — a teaching steer
// only; from the threshold onward it consumes an attempt/round (D8/N-13:
// claiming stays cheaper than idling — a bare claim is at worst one round,
// never the idle path's full quiet-window cost).
const goalBareClaimCostThreshold = 2

// goalRoute captures the channel/chat/sessionKey a goal's chat lives on, so an
// idle-settlement unmet verdict can re-inject a steering turn via the
// async-notifier (the SAME re-inject seam checkGoalLoopAfterTurn uses via
// result.followUps — there is no result to attach to from the tick path).
type goalRoute struct {
	channel    string
	chatID     string
	sessionKey string
	agentID    string
}

// goalTriggerState is the per-process, per-goal-id in-memory trigger state.
// It is held as a single package-level instance (goalTriggers()) mirroring
// verifier_adjudication.go's package-wide seam pattern (verifierPublisherSeam)
// — there is exactly ONE AgentLoop per process, so a package-level singleton
// is equivalent to a field on AgentLoop without requiring a loop.go edit
// (which is outside this file's write-set). All maps are keyed by session-id
// (the goal-id, see the file doc comment).
//
// The state is deliberately in-memory (not persisted): like the verifier
// registry's liveness flag, it is meaningful only within the live process.
// Durable persistence of the needs_input/waiting lifecycle is the S2 durable
// session record's concern (owned elsewhere); this file's waiting flag drives
// the in-process idle-suppression behavior (FR-104) and is re-established on
// the next waiting_on_user marker after any restart.
type goalTriggerState struct {
	mu sync.Mutex

	// bareClaimStreak counts consecutive bare `GOAL_STATUS: met` claims (no
	// [goal:evidence]) per goal-id, for the G-4 bounce economics.
	bareClaimStreak map[string]int

	// waitingOnUser marks a goal paused via a GOAL_STATUS: waiting_on_user
	// marker (G-5). While true, idle settlement is SUPPRESSED for this goal-id.
	waitingOnUser map[string]bool

	// routing lets the idle path re-inject a steering turn (populated at
	// /goal set, cleared at /goal clear).
	routing map[string]goalRoute

	// idleSettling marks a goal-id whose quiet-window adjudication has fired
	// and is awaiting new activity to re-arm (FR-102: "re-arms ONLY on new
	// activity"). Without this, the next tick (still inside the same quiet
	// spell) would fire a second adjudication. It is cleared by any genuine
	// activity (a turn bumping GoalLastActivityAt, or the steer re-dispatch
	// itself — which IS new activity, G-2).
	idleSettling map[string]bool
}

// goalTriggersMu guards goalTriggersSingleton (the package-wide seam).
var goalTriggersMu sync.RWMutex //nolint:gochecknoglobals // package-wide seam, mirrors verifierPublisherSeam.

//nolint:gochecknoglobals // package-wide singleton; one AgentLoop per process.
var goalTriggersSingleton = &goalTriggerState{
	bareClaimStreak: make(map[string]int),
	waitingOnUser:   make(map[string]bool),
	routing:         make(map[string]goalRoute),
	idleSettling:    make(map[string]bool),
}

// goalTriggers returns the package-wide goalTriggerState singleton. The idle
// quiet-window's clock is NOT read here — it flows from the PlanEngine tick
// (pe.clock) through goalIdleExpirySweep(now) → goalQuietWindowSettle(now), so
// tests pin the window by swapping goalIdleQuietWindow + passing a fake now,
// not by overriding a trigger-local clock.
func goalTriggers() *goalTriggerState {
	goalTriggersMu.RLock()
	defer goalTriggersMu.RUnlock()
	return goalTriggersSingleton
}

// resetGoalTriggerStateForTest clears ALL per-goal-id trigger state — intended
// for test isolation only (each test starts with a clean singleton).
func resetGoalTriggerStateForTest() {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bareClaimStreak = make(map[string]int)
	s.waitingOnUser = make(map[string]bool)
	s.routing = make(map[string]goalRoute)
	s.idleSettling = make(map[string]bool)
}

// --- per-goal-id state accessors (methods on AgentLoop via the singleton) --

// recordGoalRouting captures the channel/chat/sessionKey a goal's chat lives
// on, so a later idle-settlement unmet verdict can re-inject a steering turn.
// Called from applyGoalCommandPrompt when a goal activates/activates-from-
// pending. Idempotent; cleared by clearGoalTriggerState on /goal clear.
func (al *AgentLoop) recordGoalRouting(sessionID, channel, chatID, sessionKey, agentID string) {
	if sessionID == "" {
		return
	}
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routing[sessionID] = goalRoute{
		channel: channel, chatID: chatID, sessionKey: sessionKey, agentID: agentID,
	}
}

// clearGoalTriggerState clears ALL in-memory trigger state for sessionID — the
// waiting flag, the bounce streak, the routing entry, and the idle-in-flight
// marker. Called from clearGoal (FR-114: /goal clear cancels the in-flight
// verifier via cancelGoalVerifierIfAny and ALSO resets the trigger surface so a
// later stray GOAL_STATUS: met is inert, N-12).
func (al *AgentLoop) clearGoalTriggerState(sessionID string) {
	if sessionID == "" {
		return
	}
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bareClaimStreak, sessionID)
	delete(s.waitingOnUser, sessionID)
	delete(s.routing, sessionID)
	delete(s.idleSettling, sessionID)
}

// goalIsWaitingOnUser reports whether sessionID's goal is currently paused via
// a GOAL_STATUS: waiting_on_user marker (G-5). Idle settlement consults this
// to SUPPRESS adjudication while the pause holds.
func (al *AgentLoop) goalIsWaitingOnUser(sessionID string) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitingOnUser[sessionID]
}

// goalSetWaitingOnUser sets/clears the waiting_on_user pause flag for
// sessionID. Returns the prior value.
func (al *AgentLoop) goalSetWaitingOnUser(sessionID string, v bool) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	prior := s.waitingOnUser[sessionID]
	if v {
		s.waitingOnUser[sessionID] = true
	} else {
		delete(s.waitingOnUser, sessionID)
	}
	return prior
}

// goalMarkIdleSettling marks sessionID as "quiet-window adjudication fired,
// re-arm only on new activity" (FR-102). Idempotent within one quiet spell.
func (al *AgentLoop) goalMarkIdleSettling(sessionID string, v bool) {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	if v {
		s.idleSettling[sessionID] = true
	} else {
		delete(s.idleSettling, sessionID)
	}
}

// goalIsIdleSettling reports whether sessionID's quiet-window adjudication has
// fired and is awaiting new activity to re-arm.
func (al *AgentLoop) goalIsIdleSettling(sessionID string) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idleSettling[sessionID]
}

// bumpGoalBareClaimStreak increments and returns sessionID's consecutive
// bare-claim count (G-4). Mirrors TaskExecutor.bumpEvidenceRejectStreak.
func (al *AgentLoop) bumpGoalBareClaimStreak(sessionID string) int {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bareClaimStreak[sessionID]++
	return s.bareClaimStreak[sessionID]
}

// clearGoalBareClaimStreak resets the bounce streak — called when a claim is
// HONORED (evidence present → real adjudication) or the goal clears.
func (al *AgentLoop) clearGoalBareClaimStreak(sessionID string) {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bareClaimStreak, sessionID)
}

// goalAdjudicationInFlight reports whether a verifier adjudication is
// currently registered (in-flight) for sessionID's goal — the F5 self-race
// guard: a running adjudication's verifier turn counts as activity, so the
// idle timer must never fire a SECOND verdict against it. Uses the SAME
// verifier registry runVerifierAdjudication registers under
// (verifierUnitForGoal), so the two sides can never disagree about what's
// live (the F1 key-consistency contract).
func (al *AgentLoop) goalAdjudicationInFlight(sessionID string) bool {
	pe := GetPlanEngine(al)
	if pe == nil {
		return false
	}
	_, ok := pe.VerifierRegistry().Lookup(verifierUnitForGoal(sessionID))
	return ok
}

// runGoalAdjudication is the SINGLE shared adjudication body (INV-1: one
// adjudication invokes the Judge EXACTLY once and consumes EXACTLY one round)
// called by both the claim path (checkGoalLoopAfterTurn) and the idle path
// (goalQuietWindowSettlement). It contains the met→done / unmet→one-round+steer
// / rounds-exhausted→failed / unavailable→no-round logic extracted from the
// pre-Phase-2 checkGoalLoopAfterTurn.
//
//   - claimText is the worker's completion claim for a CLAIM adjudication
//     (placed last in the judge's input ordering via ClaimText); EMPTY for a
//     claimless IDLE adjudication (G-3: the Judge bypasses rung-0 and reads
//     persisted evidence — this file only TRIGGERS it, the Judge itself reads
//     the real diffs).
//   - steer is the steering fed forward on an unmet-but-rounds-remaining
//     outcome; the caller decides how it's delivered (claim path: a follow-up
//     bus.InboundMessage re-injected by runAgentLoop; idle path: a
//     Notify-delivered turn). An empty steer means no re-dispatch (met or
//     exhausted).
//
// Returns true if the verdict was Met (goal cleared). The caller uses the
// return only for its own re-arm bookkeeping; all persistence + pill emission
// happens here.
func (al *AgentLoop) runGoalAdjudication(
	ctx context.Context,
	agentInst *AgentInstance,
	workspaceID, sessionID string,
	store *session.UnifiedStore,
	meta *session.UnifiedMeta,
	claimText string,
	deliverSteer func(steer string),
) (met bool) {
	if agentInst == nil || meta == nil || store == nil || sessionID == "" {
		return false
	}
	// Emit the ephemeral judging pill BEFORE dispatch (D14 crosswalk: judging
	// ← ephemeral engine-phase signal, pill-only).
	al.emitGoalStatusFrame(sessionID, meta.GoalCondition, meta.GoalRoundsUsed, meta.GoalMaxRounds, meta.GoalLatestReason, goalPillJudging)

	criteria := compiledGoalCriteriaFor(meta.GoalCriteriaJSON, meta.GoalCondition, sessionID)
	attempt := meta.GoalRoundsUsed + 1

	judgeCtx, cancel := context.WithTimeout(ctx, goalJudgeRoundTimeout)
	defer cancel()

	jr := al.JudgeCriteria(judgeCtx, JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		AssigneeAgentID: agentInst.ID,
		Criteria:        criteria,
		Attempt:         attempt,
		ClaimText:       claimText, // empty for the claimless idle path (G-3)
		GoalSessionID:   sessionID,
		WorkspaceID:     workspaceID,
	})

	if jr.Unavailable {
		// D7 / D14: judge rate-limited/down → judge_unavailable pill, NO round
		// consumed, no verdict recorded.
		//
		// corr-MAJOR-2 (idleSettling wedge — REAL BUG): the IDLE path sets the
		// idleSettling marker (goalMarkIdleSettling true) BEFORE dispatching
		// this adjudication. If we return here WITHOUT clearing it, every
		// subsequent PlanEngine tick early-returns on goalIsIdleSettling and
		// the goal is wedged in judge_unavailable forever (until a user msg /
		// /goal clear) — the old "re-arms next tick" comment below was wrong,
		// because the marker (not the registry entry) is the gate that
		// suppresses the re-fire. Clear it so the next quiet-window re-fires
		// ONCE the Judge recovers (re-arm, not wedge). This is a no-op on the
		// CLAIM path, which never sets the marker.
		al.goalMarkIdleSettling(sessionID, false)
		logger.WarnCF("agent", "goal trigger: judge unavailable, round not consumed",
			map[string]any{"session_id": sessionID, "reason": jr.Reason, "claim_text_len": len(claimText)})
		al.emitGoalStatusFrame(sessionID, meta.GoalCondition, meta.GoalRoundsUsed, meta.GoalMaxRounds, jr.Reason, goalPillJudgeUnavailable)
		return false
	}

	// A real judge round ran → this IS genuine activity (F5): bump the
	// idle-expiry calendar brake so the multi-DAY sweep doesn't double-fire.
	// We deliberately do NOT clear the idleSettling marker here: the marker is
	// the "re-arm only on NEW activity" gate (FR-102), and an adjudication's
	// OWN verifier turn must not re-arm itself — otherwise an idle goal with no
	// external input would refire every quiet window. The marker clears only
	// when a genuine EXTERNAL turn runs (bumpGoalActivityOnTurn, called from
	// checkGoalLoopAfterTurn's worker/user-turn branches) — and an unmet
	// verdict's steer re-dispatch IS such a turn (G-2), which is the activity
	// that legitimately re-arms the next settlement.
	activityNow := time.Now().UTC().Format(time.RFC3339)
	if perr := store.SetMeta(sessionID, session.MetaPatch{GoalLastActivityAt: &activityNow}); perr != nil {
		logger.WarnCF("agent", "goal trigger: could not bump activity clock",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
	}

	verdict := jr.Verdict
	al.writeGoalVerdictTranscript(store, sessionID, verdict)

	if verdict != nil && verdict.Met {
		al.clearGoal(sessionID, store, "condition met")
		return true
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
		return false
	}

	newRound := attempt
	if perr := store.SetMeta(sessionID, session.MetaPatch{
		GoalRoundsUsed:   &newRound,
		GoalLatestReason: &reasonText,
	}); perr != nil {
		// silent-M3 (Phase-2 review): do NOT silently continue on an
		// un-persisted round counter. The exhaustion gate (attempt >=
		// maxRounds) re-reads GoalRoundsUsed from the store on the next
		// adjudication; writeMetaLocked leaves the persisted counter (and its
		// cache) unchanged on a write failure, so the store stays at the OLD
		// value while this in-memory branch treated the round as consumed. If
		// execution proceeded, the gate would never fire and the goal could
		// re-loop on the same attempt number indefinitely (each re-fire
		// burning tokens via the steer re-dispatch). Abort THIS adjudication's
		// round-advance: the verifier turn already ran, but the round is NOT
		// counted, no advance is emitted, and no steer is delivered (so the
		// goal is not re-dispatched on a counter the store can't durably
		// advance). The failure is surfaced loudly (ERROR + system transcript)
		// so an operator sees the storage fault rather than a quietly-stale
		// counter; the unbounded-spend backstop is the app-level token budget
		// brake. This is the "don't consume the round" minimal option from the
		// review direction.
		logger.ErrorCF("agent", "goal trigger: round-advance persist failed; aborting adjudication to keep round gate honest",
			map[string]any{"session_id": sessionID, "attempt": attempt, "max_rounds": maxRounds, "error": perr.Error()})
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, fmt.Sprintf(
			"Goal %q: could not persist the adjudication round counter (%v). The round was not counted and no follow-up was dispatched — investigate storage and retry the goal.",
			meta.GoalCondition, perr))
		al.emitGoalStatusFrame(sessionID, meta.GoalCondition, meta.GoalRoundsUsed, maxRounds,
			"round-advance persist failed (goal paused)", goalPillActive)
		return false
	}
	al.emitGoalStatusFrame(sessionID, meta.GoalCondition, newRound, maxRounds, reasonText, goalPillActive)
	if deliverSteer != nil {
		deliverSteer(goalSteeringPrompt(meta.GoalCondition, reasonText))
	}
	return false
}

// --- Idle quiet-window settlement (FR-102/G-2/G-3) ------------------------
//
// goalQuietWindowSettle is the event-driven idle-settlement pass, driven from
// goalIdleExpirySweep on the SAME PlanEngine tick as the multi-DAY idle-expiry
// calendar brake (one periodic driver for all goal-shaped sweeps, DoD-11). For
// each session carrying an active goal it fires EXACTLY ONE adjudication when
// ALL of the idle preconditions hold, consumes one round, and re-arms ONLY on
// new activity — the ADR-053 supersession of ADR-052's after-every-turn burn.

// goalQuietWindowSettle iterates every active-goal session and fires one
// claimless adjudication per goal-id whose quiet window has elapsed. now is
// caller-supplied (the PlanEngine clock) so tests pin the window without a
// sleep. A no-op when no session store is available.
func (al *AgentLoop) goalQuietWindowSettle(now time.Time) {
	store := al.GetSessionStore()
	if store == nil {
		return
	}
	sessions, err := store.ListSessions()
	if err != nil {
		logger.WarnCF("agent", "goal idle settle: list sessions failed", map[string]any{"error": err.Error()})
		return
	}
	for _, s := range sessions {
		if s == nil || s.GoalCondition == "" {
			continue // no active goal — not goal-bearing
		}
		al.maybeSettleGoalIdle(now, store, s)
	}
}

// maybeSettleGoalIdle evaluates ONE session's idle preconditions and fires a
// claimless adjudication if all hold. Split out so the per-goal logic is
// independently testable.
func (al *AgentLoop) maybeSettleGoalIdle(now time.Time, store *session.UnifiedStore, s *session.UnifiedMeta) {
	sessionID := s.ID

	// G-5: suppress idle settlement while a waiting_on_user pause holds.
	if al.goalIsWaitingOnUser(sessionID) {
		return
	}
	// FR-102 re-arm: a previous quiet-window adjudication already fired for
	// this goal-id and no new activity has re-armed it yet — do not fire a
	// second verdict against the same quiet spell.
	if al.goalIsIdleSettling(sessionID) {
		return
	}
	// F5 self-race guard: the verifier's own turn counts as activity. If an
	// adjudication is currently in-flight for this goal, the idle timer must
	// not race a second verdict against it.
	if al.goalAdjudicationInFlight(sessionID) {
		return
	}
	last := effectiveGoalActivity(s)
	if last.IsZero() {
		return // nothing to compare against; skip rather than guess
	}
	if now.Sub(last) < goalIdleQuietWindow {
		return // quiet window not yet elapsed
	}

	agentInst := resolveGoalAgent(al, s)
	if agentInst == nil {
		logger.WarnCF("agent", "goal idle settle: could not resolve agent",
			map[string]any{"session_id": sessionID, "agent_id": s.ActiveAgentID})
		return
	}

	// corr-MAJOR-1 (idle-path budget brake): the CLAIM path already brakes on
	// TokenBudget().Exhausted() (goal_loop.go's checkGoalLoopAfterTurn), but
	// the IDLE adjudication path did NOT — it would burn a Judge turn PAST the
	// cap. Gate the idle adjudication the same way: when exhausted, do NOT fire
	// (the goal brakes honestly instead of silently over-spending). Surface
	// the budget-exhausted pill/handover, mirroring the claim path's brake
	// exactly. No double-debit: the Judge never runs (we return before
	// runGoalAdjudication), so zero rounds are consumed here.
	if tb := al.TokenBudget(); tb != nil && tb.Exhausted() {
		handover := fmt.Sprintf(
			"Goal %q stopped: the overall token budget is exhausted (consumed %d tokens).",
			s.GoalCondition, tb.Consumed())
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, handover)
		al.clearGoal(sessionID, store, FailedReasonBudgetExhausted)
		return
	}

	// FR-102: mark fired + bump activity so the next tick (still inside this
	// quiet spell) does NOT fire a second adjudication. The mark clears inside
	// runGoalAdjudication once a real judge round runs (genuine activity); the
	// activity bump also re-arms the multi-day idle-expiry clock. Persist the
	// marker BEFORE dispatch so a concurrent tick observes it (single-instance
	// overlap guard makes this a no-op in practice, but the ordering is
	// load-bearing if the guard ever widens).
	al.goalMarkIdleSettling(sessionID, true)
	activityNow := now.UTC().Format(time.RFC3339)
	if perr := store.SetMeta(sessionID, session.MetaPatch{GoalLastActivityAt: &activityNow}); perr != nil {
		logger.WarnCF("agent", "goal idle settle: could not bump activity clock",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
	}

	logger.InfoCF("agent", "goal idle settle: firing claimless adjudication after quiet window",
		map[string]any{"session_id": sessionID, "quiet_window_s": int(goalIdleQuietWindow.Seconds())})

	// G-3: claimText is EMPTY — the Judge bypasses rung-0 and reads persisted
	// evidence (artifacts, write-set-scoped diffs, latest checkpoint). This
	// file only TRIGGERS it.
	settleCtx, cancel := context.WithTimeout(context.Background(), goalJudgeRoundTimeout)
	defer cancel()
	al.runGoalAdjudication(
		settleCtx, agentInst, s.WorkspaceID, sessionID, store, s, "",
		al.idleSteerDeliverer(sessionID),
	)
}

// idleSteerDeliverer returns a deliverSteer func that re-injects an unmet
// idle verdict's steering via the async-notifier — the SAME re-inject seam
// checkGoalLoopAfterTurn uses via result.followUps, adapted for the tick path
// (which has no turnResult to attach to). An unmet verdict's steer
// re-dispatch IS new activity (G-2): the Notify-originated turn bumps
// GoalLastActivityAt via checkGoalLoopAfterTurn's activity path, re-arming the
// quiet window. Best-effort: a notify failure is logged (the round was already
// consumed + persisted; the user can still drive the next turn manually).
func (al *AgentLoop) idleSteerDeliverer(sessionID string) func(steer string) {
	return func(steer string) {
		if steer == "" || al.asyncNotifier == nil {
			return
		}
		route := goalTriggers().routeFor(sessionID)
		if route.channel == "" || route.chatID == "" {
			logger.WarnCF("agent", "goal idle settle: no routing to re-inject steer; left for next turn",
				map[string]any{"session_id": sessionID})
			return
		}
		notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := al.asyncNotifier.Notify(notifyCtx, AsyncNotifyEvent{
			Channel:             route.channel,
			ChatID:              route.chatID,
			AgentID:             route.agentID,
			TranscriptSessionID: sessionID,
			SourceKind:          "goal_idle_settle",
			Content:             steer,
		}); err != nil {
			logger.WarnCF("agent", "goal idle settle: steer re-inject failed",
				map[string]any{"session_id": sessionID, "error": err.Error()})
		}
	}
}

// routeFor returns the captured routing for sessionID (helper on the singleton
// kept separate so the idle deliverer can read it under the lock).
func (s *goalTriggerState) routeFor(sessionID string) goalRoute {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.routing[sessionID]
}

// resolveGoalAgent resolves the AgentInstance that runs the goal-bearing
// session's machine checks (the Judge's AssigneeAgentID). Falls back through
// ActiveAgentID → first AgentIDs entry → the default agent, mirroring how the
// chat turn itself resolves the active agent. Returns nil if none resolves.
func resolveGoalAgent(al *AgentLoop, s *session.UnifiedMeta) *AgentInstance {
	registry := al.GetRegistry()
	if registry == nil {
		return nil
	}
	candidates := []string{s.ActiveAgentID}
	candidates = append(candidates, s.AgentIDs...)
	for _, id := range candidates {
		if id == "" {
			continue
		}
		if inst, ok := registry.GetAgent(id); ok && inst != nil {
			return inst
		}
	}
	return registry.GetDefaultAgent()
}

// --- Bounce economics (G-4 / D8 / N-13) ------------------------------------
//
// handleBareGoalClaim is the G-4 path: a `GOAL_STATUS: met` marker was found
// WITHOUT a preceding [goal:evidence] line. The FIRST such bare claim is
// bounced before the Judge with a teaching steer (free — no round spent); from
// the 2nd consecutive bare claim onward it consumes an attempt/round (mirrors
// task_executor.go's rejectBareEvidenceClaim / evidenceGateMaxConsecutiveRejections).
// A bare claim is therefore ALWAYS cheaper than or equal to the idle path —
// never more expensive (D8/N-13).
//
// deliverSteer re-dispatches the worker (claim path: a follow-up
// bus.InboundMessage). Returns true if the claim was bounced (no Judge
// invocation); false if it cost a round (the caller has already advanced the
// round budget via the real-adjudication path — but the G-4 "2nd costs" branch
// here advances the round WITHOUT a fresh Judge call, since there is no
// evidence to judge).
func (al *AgentLoop) handleBareGoalClaim(
	ctx context.Context,
	agentInst *AgentInstance,
	opts processOptions,
	store *session.UnifiedStore,
	sessionID string,
	meta *session.UnifiedMeta,
	result *turnResult,
) (bounced bool) {
	streak := al.bumpGoalBareClaimStreak(sessionID)
	logger.WarnCF("agent", "goal trigger: bare completion claim (no [goal:evidence])",
		map[string]any{"session_id": sessionID, "consecutive": streak})

	if streak < goalBareClaimCostThreshold {
		// 1st bare claim: free teaching steer, no round spent.
		if result != nil {
			result.followUps = append(result.followUps, bus.InboundMessage{
				Channel: opts.Channel, ChatID: opts.ChatID,
				Sender:    bus.SenderInfo{CanonicalID: goalLoopFollowUpSenderID},
				Content:   goalStatusBareClaimSteer,
				SessionID: sessionID, SessionKey: opts.SessionKey,
			})
		}
		al.emitGoalStatusFrame(sessionID, meta.GoalCondition, meta.GoalRoundsUsed, meta.GoalMaxRounds,
			"bare completion claim bounced (no evidence)", goalPillActive)
		return true
	}

	// 2nd+ bare claim: costs an attempt/round (G-4). Clear the streak and
	// advance the round budget WITHOUT a fresh Judge call — there is nothing
	// to judge (no evidence was provided). Bounded by GoalMaxRounds like any
	// other unmet round; on exhaustion the goal fails honestly.
	al.clearGoalBareClaimStreak(sessionID)
	maxRounds := meta.GoalMaxRounds
	if maxRounds < 1 {
		maxRounds = config.DefaultGoalMaxRounds
	}
	newRound := meta.GoalRoundsUsed + 1
	reason := "repeated bare completion claim with no [goal:evidence] line — treated as an unmet round"
	if newRound >= maxRounds {
		handover := fmt.Sprintf(
			"Goal %q did not reach a MET verdict within %d round(s) (round bound reached on a bare claim).",
			meta.GoalCondition, maxRounds,
		)
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, handover)
		al.clearGoal(sessionID, store, fmt.Sprintf("round bound reached (%d/%d) on a bare claim", newRound, maxRounds))
		return true
	}
	if perr := store.SetMeta(sessionID, session.MetaPatch{
		GoalRoundsUsed:   &newRound,
		GoalLatestReason: &reason,
	}); perr != nil {
		// bare-claim M3 sibling (gate honesty): the SAME persist-and-WARN
		// defect Fix-1's silent-M3 fixed in runGoalAdjudication. Do NOT
		// silently continue on an un-persisted round counter — the exhaustion
		// gate (newRound >= maxRounds above) re-reads GoalRoundsUsed from the
		// store on the next bare claim; on a write failure the store stays at
		// the OLD value while this branch proceeded as if newRound were
		// consumed, so the gate would never fire and the goal could re-loop on
		// the same attempt number indefinitely (each re-fire re-prompting the
		// worker). Abort: the round is NOT counted (emit the OLD counter, not
		// newRound), and no follow-up is dispatched (so the worker isn't
		// re-prompted on a counter the store can't durably advance). The
		// failure is surfaced loudly (ERROR + system transcript).
		logger.ErrorCF("agent", "goal trigger: bare-claim round-advance persist failed; aborting to keep round gate honest",
			map[string]any{"session_id": sessionID, "attempt": newRound, "max_rounds": maxRounds, "error": perr.Error()})
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, fmt.Sprintf(
			"Goal %q: could not persist the bare-claim round counter (%v). The round was not counted and no follow-up was dispatched — investigate storage and retry.",
			meta.GoalCondition, perr))
		al.emitGoalStatusFrame(sessionID, meta.GoalCondition, meta.GoalRoundsUsed, maxRounds,
			"round-advance persist failed (goal paused)", goalPillActive)
		return true
	}
	al.emitGoalStatusFrame(sessionID, meta.GoalCondition, newRound, maxRounds, reason, goalPillActive)
	if result != nil {
		result.followUps = append(result.followUps, bus.InboundMessage{
			Channel: opts.Channel, ChatID: opts.ChatID,
			Sender: bus.SenderInfo{CanonicalID: goalLoopFollowUpSenderID},
			Content: fmt.Sprintf(
				"Continue working toward the goal: %s\n\n"+
					"A second bare completion claim cost a round (%d/%d). Provide [goal:evidence] before GOAL_STATUS: met, or keep working.\n",
				meta.GoalCondition, newRound, maxRounds),
			SessionID: sessionID, SessionKey: opts.SessionKey,
		})
	}
	return true
}

// bumpGoalActivityOnTurn records that a genuine (non-waiting) turn just ran on
// sessionID — the activity that re-arms the idle quiet window (FR-102) AND
// clears an "idleSettling, awaiting new activity" marker (G-2). Best-effort
// persistence: a write failure only delays idle settlement, never blocks the
// turn. Returns the new RFC3339 timestamp.
func (al *AgentLoop) bumpGoalActivityOnTurn(store *session.UnifiedStore, sessionID string) string {
	now := time.Now().UTC().Format(time.RFC3339)
	if store != nil && sessionID != "" {
		if perr := store.SetMeta(sessionID, session.MetaPatch{GoalLastActivityAt: &now}); perr != nil {
			logger.WarnCF("agent", "goal trigger: could not bump activity clock on turn",
				map[string]any{"session_id": sessionID, "error": perr.Error()})
		}
	}
	al.goalMarkIdleSettling(sessionID, false)
	return now
}
