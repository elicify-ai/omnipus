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

// --- Pill state constants (D14 crosswalk, FR / Pill-state enum) -----------
//
// The pill emitted on the goal_status WS frame. The durable lifecycle
// authority (S2 8-state enum) is owned elsewhere; these pill values are the
// DISPLAY overlay reconstructed from lifecycle + engine phase. The two
// ephemeral engine-phase pills (judging / judge_unavailable) and the
// waiting_on_user pause are what THIS file drives; re-planning is a durable
// plan_phase overlay (C1) owned by the plan-owner loop.
//
// goalPillCleared is a 9th value ADDED to the original ADR-053 8-value pill
// enum by the UAT S3 fix (contracts/components/schemas/GoalStatusFrame.yaml,
// Constraint #8): a user-initiated `/goal clear` (clearGoal's
// goalClearNoteUser path, goal_loop.go) now reports this instead of
// collapsing into goalPillFailed. This intentionally departs from the
// original R§8.10 crosswalk's "failed/cancelled/timed_out → failed" design
// (docs/internal/specs/unified-goal-plan-subagent-spec.md's crosswalk table)
// — a deliberate, human-UAT-confirmed amendment: painting a red "failed"
// badge for a deliberate, successful user stop is misleading regardless of
// what `latest_reason` says, and the SPA pill does not read latest_reason to
// disambiguate. Round/budget/idle-expiry brakes (genuine terminal failures,
// not a user choice) still report goalPillFailed, unchanged.
const (
	goalPillQueued           = "queued"
	goalPillActive           = "active"
	goalPillWaitingOnUser    = "waiting_on_user"
	goalPillJudgeUnavailable = "judge_unavailable"
	goalPillRePlanning       = "re-planning"
	goalPillJudging          = "judging"
	goalPillDone             = "done"
	goalPillFailed           = "failed"
	goalPillCleared          = "cleared"
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

// goalZeroOutputPushMax is ADR-081 FR-014b/FR-017's "N=2" bound — SHARED by
// TWO ladders through the SAME persisted GoalZeroOutputPushes field
// (session.UnifiedMeta), a deliberate decision (reported per the lane
// brief, not an oversight): a RECORDED goal's bounded zero-output
// continue-push count (FR-014b) and a RECORDLESS goal's nudge count
// (FR-017/D6c). The spec names no second counter for the nudge ladder, and
// the two states are mutually exclusive at any single idle check (a goal's
// GoalCriteriaJSON is either empty XOR populated when maybeSettleGoalIdle
// evaluates it), so one field safely counts "consecutive keeper
// free-actions" across both without ever conflating a push with a nudge.
// Reset to 0 on: a successful set_goal write (wave 2a's responsibility —
// NOT duplicated here), the zero-output triple evaluating false at an idle
// (settleZeroOutputRecordedGoal's caller), and an engine-authored fallback
// registration (dispatchGoalFallbackCompile — the goal transitions from
// recordless to recorded, so the recorded-ladder's counter must start
// fresh).
const goalZeroOutputPushMax = 2

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

	// diffBoundaryHash is ADR-081 D6a/FR-014b's per-goal-id git-commit
	// boundary for the zero-output triple's diff term (resolveGoalScopedDiffEmpty,
	// verifier_adjudication.go): the HEAD hash observed the last time this
	// goal-id's diff term was evaluated, refreshed on every evaluation. This
	// is what makes the term "goal-scoped" (round-2 B-4) — a co-tenant
	// session sharing the same WorkspaceID that committed something BEFORE
	// this goal-id's own boundary was first captured can never mask this
	// goal's emptiness, since the scoped diff only ever looks at commits
	// AFTER this goal's own last look. In-memory only, like the rest of
	// this singleton (a restart simply re-baselines on the next check —
	// safe, because the OTHER two triple terms still gate the free-push
	// decision independently, and GoalZeroOutputPushes — the field that
	// actually bounds the loop — IS persisted).
	diffBoundaryHash map[string]string

	// outputWatermarks is the transcript-output term's own per-goal-id
	// "since" boundary — deliberately NOT session.UnifiedMeta.GoalLastActivityAt,
	// which THIS SAME zero-output evaluation's own dispatched continue-push/
	// nudge turn bumps forward via bumpGoalActivityOnTurn once that turn
	// runs. Using GoalLastActivityAt as the "since" boundary would compare
	// every check against a timestamp chronologically AFTER the very
	// output that produced it, making the transcript-output term read
	// "zero" forever regardless of real activity. This watermark is
	// refreshed to "now" (the wall-clock time AT evaluation) on every call,
	// so the NEXT check correctly sees any transcript growth that happened
	// in between. In-memory only, cleared on goal clear.
	outputWatermarks map[string]time.Time

	// sessionStoreResolver is FR-031's session-store seam for routeFor's
	// persisted-routing rehydration. routeFor is called as a bare
	// goalTriggers().routeFor(sessionID) — no *AgentLoop receiver, by
	// design (wave 2a's set_goal channel-echo path calls it exactly that
	// way) — so it cannot itself resolve al.GetSessionStore(). Populated
	// opportunistically (idempotent, first-writer-wins) by every method in
	// this file that already has an *AgentLoop in scope and runs
	// regularly regardless of any specific goal's activation
	// (recordGoalRouting, goalQuietWindowSettle) — see routeFor's doc
	// comment for the one residual cold-boot gap this leaves and the
	// recommended follow-up.
	sessionStoreResolver func() *session.UnifiedStore
}

// goalTriggersMu guards goalTriggersSingleton (the package-wide seam).
var goalTriggersMu sync.RWMutex //nolint:gochecknoglobals // package-wide seam, mirrors verifierPublisherSeam.

//nolint:gochecknoglobals // package-wide singleton; one AgentLoop per process.
var goalTriggersSingleton = &goalTriggerState{
	bareClaimStreak:  make(map[string]int),
	waitingOnUser:    make(map[string]bool),
	routing:          make(map[string]goalRoute),
	idleSettling:     make(map[string]bool),
	diffBoundaryHash: make(map[string]string),
	outputWatermarks: make(map[string]time.Time),
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
	s.diffBoundaryHash = make(map[string]string)
	s.outputWatermarks = make(map[string]time.Time)
	s.sessionStoreResolver = nil
}

// --- per-goal-id state accessors (methods on AgentLoop via the singleton) --

// recordGoalRouting captures the channel/chat/sessionKey a goal's chat lives
// on, so a later idle-settlement unmet verdict can re-inject a steering turn.
// Called from applyGoalCommandPrompt when a goal activates/activates-from-
// pending. Idempotent; cleared by clearGoalTriggerState on /goal clear.
//
// ADR-081 FR-031 (round-2 M-9): the route is ALSO persisted onto the goal
// record's GoalRoute* meta fields, not just the in-memory map — a gateway
// restart used to silently disable both the keeper's idle-steer re-inject
// and the channel record echo, since neither had anything durable to read.
// routeFor (below) is the matching read side: in-memory first, persisted
// fallback. This also opportunistically wires sessionStoreResolver (see its
// doc comment) so routeFor can reach the store on a bare, receiverless call.
// SetGoalRouteSessionStore wires FR-031's session-store resolver at BOOT,
// closing the cold-start gap the opportunistic wiring below leaves open: a
// channel record echo (set_goal write) or keeper action firing in a fresh
// process BEFORE any /goal command or quiet-window tick would otherwise find
// routeFor without a store and degrade. Called from gateway boot next to
// SetAskUserRegistry (W2b deviation #6 fast-follow, ADR-081 FR-031).
func (al *AgentLoop) SetGoalRouteSessionStore() {
	s := goalTriggers()
	s.mu.Lock()
	if s.sessionStoreResolver == nil {
		s.sessionStoreResolver = al.GetSessionStore
	}
	s.mu.Unlock()
}

func (al *AgentLoop) recordGoalRouting(sessionID, channel, chatID, sessionKey, agentID string) {
	if sessionID == "" {
		return
	}
	s := goalTriggers()
	s.mu.Lock()
	s.routing[sessionID] = goalRoute{
		channel: channel, chatID: chatID, sessionKey: sessionKey, agentID: agentID,
	}
	if s.sessionStoreResolver == nil {
		s.sessionStoreResolver = al.GetSessionStore
	}
	s.mu.Unlock()

	if store := al.GetSessionStore(); store != nil {
		if perr := store.SetMeta(sessionID, session.MetaPatch{
			GoalRouteChannel:    &channel,
			GoalRouteChatID:     &chatID,
			GoalRouteSessionKey: &sessionKey,
			GoalRouteAgentID:    &agentID,
		}); perr != nil {
			logger.WarnCF("agent", "goal trigger: could not persist goal routing",
				map[string]any{"session_id": sessionID, "error": perr.Error()})
		}
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
	delete(s.diffBoundaryHash, sessionID)
	delete(s.outputWatermarks, sessionID)
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
	al.emitGoalStatusFrame(sessionID, meta.GoalID, meta.GoalCondition, meta.GoalRoundsUsed, meta.GoalMaxRounds, meta.GoalLatestReason, goalPillJudging)

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
		al.emitGoalStatusFrame(sessionID, meta.GoalID, meta.GoalCondition, meta.GoalRoundsUsed, meta.GoalMaxRounds, jr.Reason, goalPillJudgeUnavailable)
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
	// ADR-081 D8: the verdict summary — the event D8 names that this file
	// previously had no INFO line for at all.
	logger.InfoCF("agent", "goal: verdict computed",
		map[string]any{
			"session_id": sessionID, "goal_id": meta.GoalID,
			"met": verdict != nil && verdict.Met, "attempt": attempt,
		})

	if verdict != nil && verdict.Met {
		// Use the constant, not a bare literal. clearGoal switches on this note
		// to decide the terminal pill state, so a literal here means editing
		// goalClearNoteMet would silently stop matching and reclassify a MET
		// goal as `failed` — the exact drift the constant was introduced to
		// prevent. (Its doc comment claimed both call sites were converted;
		// this one was missed.)
		al.clearGoal(sessionID, store, goalClearNoteMet)
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
		al.emitGoalStatusFrame(sessionID, meta.GoalID, meta.GoalCondition, meta.GoalRoundsUsed, maxRounds,
			"round-advance persist failed (goal paused)", goalPillActive)
		return false
	}
	al.emitGoalStatusFrame(sessionID, meta.GoalID, meta.GoalCondition, newRound, maxRounds, reasonText, goalPillActive)
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
	// FR-031: opportunistically wire routeFor's session-store seam. This
	// tick runs regardless of any specific goal's activation, so by the
	// time ANY idle-triggered routeFor call happens downstream of THIS
	// function, the resolver is guaranteed set — closing the restart gap
	// for the common case (see sessionStoreResolver's doc comment for the
	// one residual cold-boot path this does not cover).
	gs := goalTriggers()
	gs.mu.Lock()
	if gs.sessionStoreResolver == nil {
		gs.sessionStoreResolver = al.GetSessionStore
	}
	gs.mu.Unlock()

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

	// ADR-081 D6a/FR-016: a parked AskUserQuestion card suppresses BOTH idle
	// settlement and the D6c nudge ladder — the goal is waiting on the
	// operator in the same sense as the waiting_on_user marker pause below.
	// Checked in the SAME early, silent position as goalIsWaitingOnUser
	// (neither logs on every tick — both are routine, long-lived states, not
	// "genuinely became idle" moments). The expiry sweep (goalIdleExpirySweep,
	// this function's caller) is unaffected — it runs its own multi-day
	// calendar check before ever reaching here.
	if al.goalHasParkedCard(sessionID) {
		return
	}
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

	// ADR-081 D6a/FR-013: a live turn for this goal's session — its own root
	// turn OR any delegated descendant — counts as activity. Idle settlement
	// (and the D6c nudge ladder, which shares this same gate) is suppressed
	// while one exists; the window re-arms so the NEXT check waits a full
	// quiet window rather than re-testing every tick.
	if al.goalHasLiveTurn(sessionID) {
		logger.InfoCF("agent", "goal idle settle: suppressed, turn in flight",
			map[string]any{"session_id": sessionID, "goal_id": s.GoalID})
		activityNow := now.UTC().Format(time.RFC3339)
		if perr := store.SetMeta(sessionID, session.MetaPatch{GoalLastActivityAt: &activityNow}); perr != nil {
			logger.WarnCF("agent", "goal idle settle: could not re-arm activity clock (in-flight turn)",
				map[string]any{"session_id": sessionID, "error": perr.Error()})
		}
		return
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
	// quiet spell) does NOT fire a second adjudication/nudge/push. The mark
	// clears once a real judge round runs OR a dispatched follow-up turn
	// completes (genuine activity); the activity bump also re-arms the
	// multi-day idle-expiry clock. Persist the marker BEFORE dispatch so a
	// concurrent tick observes it.
	al.markGoalIdleFired(store, sessionID, now)

	if s.GoalCriteriaJSON == "" {
		// ADR-081 D3/FR-014/D6c: a RECORDLESS goal (active ∧ empty record) is
		// NEVER judged at idle — there is nothing to adjudicate. The keeper's
		// only action is the nudge ladder.
		al.settleRecordlessGoal(store, s, agentInst)
		return
	}

	// ADR-081 D6a/FR-014b: a RECORDED goal — evaluate the zero-adjudicable-
	// output triple BEFORE judging. Only when it is false (genuine
	// adjudicable material exists) does normal claimless adjudication run.
	if al.goalZeroOutputTripleHolds(store, s, now) {
		al.settleZeroOutputRecordedGoal(store, s, agentInst)
		return
	}
	if s.GoalZeroOutputPushes != 0 {
		zero := 0
		if perr := store.SetMeta(sessionID, session.MetaPatch{GoalZeroOutputPushes: &zero}); perr != nil {
			logger.WarnCF("agent", "goal trigger: could not reset zero-output push count",
				map[string]any{"session_id": sessionID, "error": perr.Error()})
		}
	}
	al.settleGoalNormally(store, s, agentInst)
}

// settleGoalNormally is the pre-ADR-081 idle-settlement tail (unchanged
// behavior): a real claimless adjudication via the shared runGoalAdjudication
// body. Reached only when the goal is RECORDED and the FR-014b zero-output
// triple does not hold — i.e. there is genuinely something to judge.
func (al *AgentLoop) settleGoalNormally(store *session.UnifiedStore, s *session.UnifiedMeta, agentInst *AgentInstance) {
	sessionID := s.ID
	logger.InfoCF("agent", "goal idle settle: firing claimless adjudication after quiet window",
		map[string]any{"session_id": sessionID, "goal_id": s.GoalID, "quiet_window_s": int(goalIdleQuietWindow.Seconds())})

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

// settleZeroOutputRecordedGoal is ADR-081 FR-014b's action for a RECORDED
// goal whose zero-adjudicable-output triple holds at idle: dispatch a
// bounded continue-push (never a verdict, never a round) UNLESS the push
// budget (goalZeroOutputPushMax, persisted on GoalZeroOutputPushes) is
// already spent — in which case normal adjudication runs anyway, so a
// genuinely stuck goal still terminates via the standard rounds bound
// rather than pushing forever.
func (al *AgentLoop) settleZeroOutputRecordedGoal(store *session.UnifiedStore, s *session.UnifiedMeta, agentInst *AgentInstance) {
	sessionID := s.ID
	if s.GoalZeroOutputPushes >= goalZeroOutputPushMax {
		al.settleGoalNormally(store, s, agentInst)
		return
	}
	newCount := s.GoalZeroOutputPushes + 1
	if perr := store.SetMeta(sessionID, session.MetaPatch{GoalZeroOutputPushes: &newCount}); perr != nil {
		logger.WarnCF("agent", "goal trigger: could not persist zero-output push count",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
	}
	logger.InfoCF("agent", "goal idle settle: suppressed, zero-output continue-push dispatched",
		map[string]any{"session_id": sessionID, "goal_id": s.GoalID, "push_count": newCount})
	al.dispatchGoalAsyncFollowUp(sessionID, goalContinuePushPrompt(s.GoalCondition))
}

// settleRecordlessGoal is ADR-081 D6c's nudge ladder for a RECORDLESS active
// goal at idle (no parked card, quiet turn machinery): dispatch a nudge
// telling the working agent to register its record via set_goal, up to
// goalZeroOutputPushMax (N=2) times; on the (N+1)th observation (still
// recordless after two nudges) run the D7 engine fallback compile instead.
func (al *AgentLoop) settleRecordlessGoal(store *session.UnifiedStore, s *session.UnifiedMeta, agentInst *AgentInstance) {
	sessionID := s.ID
	if s.GoalZeroOutputPushes >= goalZeroOutputPushMax {
		al.dispatchGoalFallbackCompile(store, s, agentInst)
		return
	}
	newCount := s.GoalZeroOutputPushes + 1
	if perr := store.SetMeta(sessionID, session.MetaPatch{GoalZeroOutputPushes: &newCount}); perr != nil {
		logger.WarnCF("agent", "goal trigger: could not persist nudge count",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
	}
	logger.InfoCF("agent", "goal: keeper nudge dispatched",
		map[string]any{"session_id": sessionID, "goal_id": s.GoalID, "nudge_count": newCount})
	al.dispatchGoalAsyncFollowUp(sessionID, goalNudgePrompt(s.GoalCondition, newCount))
}

// markGoalIdleFired records that THIS idle check is about to take an action
// (adjudicate, push, or nudge) — the shared FR-102 re-arm bookkeeping every
// action branch needs before dispatching: mark idleSettling so a concurrent/
// next tick does not double-fire, and bump GoalLastActivityAt so the
// multi-day idle-expiry clock and the quiet-window math both reflect "we
// just looked at this goal". Extracted so all four action paths
// (settleGoalNormally, settleZeroOutputRecordedGoal, settleRecordlessGoal,
// dispatchGoalFallbackCompile) share one implementation.
func (al *AgentLoop) markGoalIdleFired(store *session.UnifiedStore, sessionID string, now time.Time) {
	al.goalMarkIdleSettling(sessionID, true)
	activityNow := now.UTC().Format(time.RFC3339)
	if perr := store.SetMeta(sessionID, session.MetaPatch{GoalLastActivityAt: &activityNow}); perr != nil {
		logger.WarnCF("agent", "goal idle settle: could not bump activity clock",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
	}
}

// goalNudgePrompt is D6c's nudge turn content: a system-authored prompt
// telling the agent to register its goal record now, citing the durable
// GoalCondition (never the transcript — E8/S-38: the window may have
// trimmed the original goal message away, but the durable record survives).
func goalNudgePrompt(condition string, nudgeCount int) string {
	return fmt.Sprintf(
		"You have an active goal but have not yet registered a working record for it.\n\n"+
			"Goal: %s\n\n"+
			"Call set_goal now (mode: register) with your restated statement, acceptance criteria, "+
			"and Definition of Done — your best understanding is enough; state any assumptions. "+
			"(nudge %d of %d before the engine registers one for you)",
		condition, nudgeCount, goalZeroOutputPushMax,
	)
}

// goalContinuePushPrompt is FR-014b's bounded continue-push content: no
// verdict is being reported (nothing to report — the triple held), just a
// nudge to keep working, sourced from the durable GoalCondition (E8/S-38).
func goalContinuePushPrompt(condition string) string {
	return fmt.Sprintf(
		"Continue working toward the goal: %s\n\n"+
			"No new output has been observed since the last check. Keep going.",
		condition,
	)
}

// dispatchGoalFallbackCompile is ADR-081 D6c/D7's engine-authored fallback:
// after goalZeroOutputPushMax recordless nudges the agent still has not
// called set_goal, so the engine runs compileGoalIntentLLM ITSELF — the
// same call the pre-ADR-081 front path used to make, D7-repointed at the
// Judge system agent's model by wave 2a — with the goal's own durable
// condition as intent, and registers whatever comes back directly via the
// session meta patch (bypassing the set_goal tool entirely: there is no
// worker turn to call it from). compileGoalIntentLLM's own EC-4/D7
// fallback-of-a-fallback (nil agent instance / no provider → the
// deterministic marker parser) still applies underneath this call, so SOME
// record lands either way, closing FR-017's invariant that every active
// goal ends up judgeable.
func (al *AgentLoop) dispatchGoalFallbackCompile(store *session.UnifiedStore, s *session.UnifiedMeta, agentInst *AgentInstance) {
	sessionID := s.ID
	logger.WarnCF("agent", "goal fallback compile invoked after nudge exhaustion",
		map[string]any{"session_id": sessionID, "goal_id": s.GoalID, "nudges": s.GoalZeroOutputPushes})

	var fc FeasibilityContext
	if agentInst != nil {
		fc = agentFeasibilityContext{agentInst: agentInst}
	}
	fallbackCtx, cancel := context.WithTimeout(context.Background(), goalJudgeRoundTimeout)
	defer cancel()
	outcome := al.compileGoalIntentLLM(fallbackCtx, agentInst, fc, s.GoalCondition, sessionID, "", "", false, s.WorkspaceID)

	if outcome.Result.Rejection != nil || outcome.Result.Goal == nil {
		reason := "fallback compile produced no usable record"
		if outcome.Result.Rejection != nil {
			reason = outcome.Result.Rejection.Reason
		}
		logger.ErrorCF("agent", "goal fallback compile produced no usable record",
			map[string]any{"session_id": sessionID, "goal_id": s.GoalID, "reason": reason})
		return
	}

	criteriaJSON, merr := marshalCompiledGoal(outcome.Result.Goal)
	if merr != nil {
		logger.ErrorCF("agent", "goal fallback compile: could not marshal compiled record",
			map[string]any{"session_id": sessionID, "goal_id": s.GoalID, "error": merr.Error()})
		return
	}
	zero := 0
	nowStr := time.Now().UTC().Format(time.RFC3339)
	reason := "engine-authored fallback record registered after nudge exhaustion"
	if perr := store.SetMeta(sessionID, session.MetaPatch{
		GoalCriteriaJSON:     &criteriaJSON,
		GoalZeroOutputPushes: &zero, // the recorded-goal ladder starts fresh (goalZeroOutputPushMax's shared-field rule)
		GoalLastActivityAt:   &nowStr,
		GoalLatestReason:     &reason,
	}); perr != nil {
		logger.ErrorCF("agent", "goal fallback compile: could not persist engine-authored record",
			map[string]any{"session_id": sessionID, "goal_id": s.GoalID, "error": perr.Error()})
		return
	}
	logger.InfoCF("agent", "goal: engine-authored fallback record registered",
		map[string]any{"session_id": sessionID, "goal_id": s.GoalID, "used_deterministic_parser": outcome.UsedFallback})
	al.emitGoalStatusFrameWithCriteriaAndDoD(sessionID, s.GoalID, s.GoalCondition, s.GoalRoundsUsed, s.GoalMaxRounds, "", goalPillActive,
		outcome.Result.Goal.Definition, outcome.Result.Goal.Criteria, outcome.Result.Goal.DoD)
}

// --- D6a repairs: in-flight suppression, parked-card suppression, and the
// FR-014b zero-adjudicable-output triple -------------------------------

// goalHasParkedCard reports whether sessionID currently has a pending
// AskUserQuestion set (FR-016): a parked card suppresses BOTH idle
// settlement and the D6c nudge ladder — the goal is waiting on the operator,
// not idle in the sense either mechanism exists to police. A nil registry
// (unwired) or nothing pending is "no parked card".
func (al *AgentLoop) goalHasParkedCard(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	reg := al.getAskUserRegistry()
	if reg == nil {
		return false
	}
	_, ok := reg.PendingForSession(sessionID)
	return ok
}

// goalHasLiveTurn is D6a's FR-013 in-flight-suppression predicate: reports
// whether a LIVE turn exists for sessionID — its own root turn OR any
// delegated descendant.
//
// MECHANISM (deviation from the ADR's literal "transcriptSessionID" wording,
// reported per the lane brief): resolved via collectDescendantTurnIDs
// (steering.go), which matches turnState.routingSessionID, NOT a bare
// transcriptSessionID comparison. A delegated child's transcriptSessionID is
// its OWN distinct id (ADR-057 D2/FR-011 gave every delegate its own
// store-backed session); matching on transcriptSessionID alone would find
// ONLY the goal's own root turn and miss every live delegate entirely — the
// exact "goal whose agent is waiting on a delegate is working, not idle"
// case D6a requires. routingSessionID, by contrast, is inherited verbatim
// through the whole delegation subtree from the chat root (turn.go's
// routingSessionID doc comment) — precisely "root turn or delegated
// descendant" in one match. This is the SAME mechanism ADR-057's chat-wide
// Stop cascade uses for an identical "reach the whole subtree" need.
func (al *AgentLoop) goalHasLiveTurn(sessionID string) bool {
	ids := al.collectDescendantTurnIDs(sessionID)
	if len(ids) == 0 {
		return false
	}
	return len(al.liveTurnStatesAmong(ids)) > 0
}

// goalZeroOutputTripleHolds evaluates FR-014b's named triple for a RECORDED
// goal at idle: zero evidence records ∧ zero goal-scoped workspace diff ∧
// zero transcript output (counting delegated descendants). All three terms
// are independently best-effort (a read failure degrades toward "zero" —
// never toward a fail-closed push denial) so a storage hiccup cannot itself
// force a push/nudge.
//
// The "since" boundary for the transcript-output term is this goal-id's own
// outputWatermarks entry — refreshed to `now` on EVERY call, never derived
// from GoalLastActivityAt (see outputWatermarks' doc comment for why: this
// SAME evaluation's own dispatched push/nudge turn bumps GoalLastActivityAt
// forward once it runs, which would make every later check compare against
// a boundary chronologically AFTER the very output that produced it). On the
// FIRST-EVER observation for a goal-id (fresh goal, or a post-restart
// re-baseline — this watermark is in-memory only) there is nothing to
// compare against yet: the triple is conservatively treated as holding
// (a harmless extra push/nudge at worst, never a premature fail-closed
// verdict) and tracking starts from here.
func (al *AgentLoop) goalZeroOutputTripleHolds(store *session.UnifiedStore, s *session.UnifiedMeta, now time.Time) bool {
	sessionID := s.ID
	gs := goalTriggers()
	gs.mu.Lock()
	prevWatermark, hadWatermark := gs.outputWatermarks[sessionID]
	gs.outputWatermarks[sessionID] = now
	gs.mu.Unlock()

	evidenceZero := al.goalZeroEvidenceRecords(sessionID)
	diffZero := al.resolveGoalScopedDiffEmpty(sessionID, s.WorkspaceID)

	if !hadWatermark {
		return true
	}

	outputZero := !al.goalHasTranscriptOutputSince(store, sessionID, prevWatermark)
	return evidenceZero && diffZero && outputZero
}

// goalZeroEvidenceRecords is FR-014b's first triple term: whether ANY
// task.EvidenceRecord exists under sessionID's own key in the machine-check
// evidence store.
//
// HONEST CAVEAT (reported per the lane brief, not hidden): under TODAY's
// wiring, NOTHING writes an EvidenceRecord under a goal-session-keyed id —
// judge.go's persistEvidence takes its taskID from JudgeCriteriaInput.TaskID,
// which is always empty for task.VerdictScopeGoal (goal-scope adjudication
// carries GoalSessionID, never TaskID — see judge.go's Validate). So this
// term is currently vacuously true for every goal. It is kept as its own
// real, independently-evaluated conjunct — rather than folded away or
// dropped — because task.EvidenceStore is a data source genuinely DISJOINT
// from transcript content (a separate on-disk store, not
// session.TranscriptEntry), and becomes meaningful the moment any future
// change wires goal-scoped machine-check evidence under this key. A read
// failure degrades toward "zero" (best-effort), matching every other term.
func (al *AgentLoop) goalZeroEvidenceRecords(sessionID string) bool {
	if sessionID == "" {
		return true
	}
	es := al.evidenceStore()
	recs, err := es.List(sessionID)
	if err != nil {
		logger.WarnCF("agent", "goal trigger: could not list evidence records for zero-output check",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return true
	}
	return len(recs) == 0
}

// goalHasTranscriptOutputSince is FR-014b's third triple term (negated):
// reports whether sessionID OR any of its delegated descendant sessions
// recorded any transcript entry timestamped strictly after since. A goal
// whose agent delegated ALL of its work MUST NOT read as empty (US-6 A2) —
// each delegate owns its own store-backed transcript post-ADR-057, so a
// scan of the root session alone would silently miss real work.
//
// Descendants are resolved via the DURABLE ParentSessionID chain over
// store.ListSessions() — NOT al.goalHasLiveTurn's in-memory turnState scan,
// which only sees turns still ACTIVE right now. A delegate that already
// finished and cleared from al.activeTurnStates still needs to count here
// (its transcript output persisted regardless of whether the turn is still
// live).
func (al *AgentLoop) goalHasTranscriptOutputSince(store *session.UnifiedStore, sessionID string, since time.Time) bool {
	if store == nil || sessionID == "" {
		return false
	}
	all, err := store.ListSessions()
	if err != nil {
		logger.WarnCF("agent", "goal trigger: could not list sessions for descendant transcript scan",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return false
	}
	for _, id := range goalDescendantSessionIDs(all, sessionID) {
		if al.sessionHasTranscriptOutputSince(store, id, since) {
			return true
		}
	}
	return false
}

// goalDescendantSessionIDs returns rootID plus every session in all whose
// ParentSessionID chain leads back to rootID, at any depth — the DURABLE
// (on-disk) counterpart to steering.go's collectLiveDescendantTurnStates,
// which only sees turns still registered in al.activeTurnStates. Uses
// exactly the same reader surface (store.ListSessions +
// session.UnifiedMeta.ParentSessionID) every existing session-listing call
// site in this package already has — no new pkg/session surface needed.
func goalDescendantSessionIDs(all []*session.UnifiedMeta, rootID string) []string {
	if rootID == "" {
		return nil
	}
	byParent := make(map[string][]string, len(all))
	for _, m := range all {
		if m == nil || m.ParentSessionID == "" {
			continue
		}
		byParent[m.ParentSessionID] = append(byParent[m.ParentSessionID], m.ID)
	}
	ids := []string{rootID}
	reached := map[string]bool{rootID: true}
	for i := 0; i < len(ids); i++ {
		for _, child := range byParent[ids[i]] {
			if !reached[child] {
				reached[child] = true
				ids = append(ids, child)
			}
		}
	}
	return ids
}

// sessionHasTranscriptOutputSince reports whether sessionID's OWN transcript
// (not its descendants — the caller walks those separately) has any entry
// timestamped strictly after since. Best-effort: a read failure is treated
// as "no output", never a hard failure of the wider sweep.
func (al *AgentLoop) sessionHasTranscriptOutputSince(store *session.UnifiedStore, sessionID string, since time.Time) bool {
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Timestamp.After(since) {
			return true
		}
	}
	return false
}

// idleSteerDeliverer returns a deliverSteer func that re-injects an unmet
// idle verdict's steering via the async-notifier — the SAME re-inject seam
// checkGoalLoopAfterTurn uses via result.followUps, adapted for the tick path
// (which has no turnResult to attach to). An unmet verdict's steer
// re-dispatch IS new activity (G-2): the Notify-originated turn bumps
// GoalLastActivityAt via checkGoalLoopAfterTurn's activity path, re-arming the
// quiet window.
//
// ADR-081 D6b (the un-wedge fix): dispatchGoalAsyncFollowUp stamps the
// notify event's SenderCanonicalID as goalLoopFollowUpSenderID — the SAME
// sentinel checkGoalLoopAfterTurn's origin gate accepts. Before this, the
// notifier's default "async:<kind>" stamping meant the re-injected steer
// turn was silently DROPPED at the gate: activity never bumped, idleSettling
// never cleared, and the idle keeper fired exactly once per goal, then
// wedged forever (the defect this ADR names verbatim).
func (al *AgentLoop) idleSteerDeliverer(sessionID string) func(steer string) {
	return func(steer string) {
		al.dispatchGoalAsyncFollowUp(sessionID, steer)
	}
}

// dispatchGoalAsyncFollowUp re-injects content as a NEW turn on sessionID's
// recorded goal routing, stamped as the goal-loop's own sender
// (goalLoopFollowUpSenderID) so checkGoalLoopAfterTurn's origin gate accepts
// it (D6b). The single shared dispatch primitive behind the idle-steer
// re-inject (idleSteerDeliverer, above), the D6c recordless-goal nudge, and
// the FR-014b recorded-goal zero-output continue-push — all three are "the
// tick path has no turnResult to attach a followUp to, so re-inject via the
// async-notifier instead" with an identical shape. Best-effort throughout: an
// empty content, an unwired notifier, or missing routing (WARNed by routeFor
// itself when genuinely absent on both sides, FR-031) are all silent no-ops
// here — the NEXT idle check gets another chance, never a hard failure of
// the sweep.
func (al *AgentLoop) dispatchGoalAsyncFollowUp(sessionID, content string) {
	if content == "" || al.asyncNotifier == nil {
		return
	}
	route := goalTriggers().routeFor(sessionID)
	if route.channel == "" || route.chatID == "" {
		// routeFor itself already WARNed + persisted latest_reason when the
		// route is missing on BOTH sides (FR-031); nothing further to log
		// here beyond what it already did.
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
		SenderCanonicalID:   goalLoopFollowUpSenderID,
		Content:             content,
	}); err != nil {
		logger.WarnCF("agent", "goal: follow-up turn dispatch failed",
			map[string]any{"session_id": sessionID, "error": err.Error()})
	}
}

// routeFor returns the captured routing for sessionID: the in-memory entry
// when present, else FR-031's persisted fallback — the four GoalRoute* meta
// fields recordGoalRouting wrote — rehydrated into the in-memory map on a
// hit so subsequent reads in this process are O(1). Called as a bare
// goalTriggers().routeFor(sessionID) by both this file's own dispatch
// helpers and wave 2a's set_goal channel-echo path — see
// sessionStoreResolver's doc comment for why this method cannot take an
// *AgentLoop receiver and how it still reaches the session store. A route
// that is missing on BOTH sides (in-memory AND persisted) WARNs and writes
// a one-line GoalLatestReason note instead of degrading silently (FR-031).
func (s *goalTriggerState) routeFor(sessionID string) goalRoute {
	s.mu.Lock()
	route, ok := s.routing[sessionID]
	resolver := s.sessionStoreResolver
	s.mu.Unlock()
	if ok && route.channel != "" && route.chatID != "" {
		return route
	}
	if resolver == nil {
		return route
	}
	store := resolver()
	if store == nil {
		return route
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return route
	}
	persisted := goalRoute{
		channel:    meta.GoalRouteChannel,
		chatID:     meta.GoalRouteChatID,
		sessionKey: meta.GoalRouteSessionKey,
		agentID:    meta.GoalRouteAgentID,
	}
	if persisted.channel == "" || persisted.chatID == "" {
		if route.channel == "" && route.chatID == "" {
			logger.WarnCF("agent", "goal trigger: no routing available (neither in-memory nor persisted) — keeper cannot reach the goal's channel",
				map[string]any{"session_id": sessionID})
			lostReason := "keeper cannot reach the goal's channel — routing lost"
			if perr := store.SetMeta(sessionID, session.MetaPatch{GoalLatestReason: &lostReason}); perr != nil {
				logger.WarnCF("agent", "goal trigger: could not persist routing-lost reason",
					map[string]any{"session_id": sessionID, "error": perr.Error()})
			}
		}
		return route
	}
	s.mu.Lock()
	s.routing[sessionID] = persisted
	s.mu.Unlock()
	return persisted
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
	candidates := make([]string, 0, 1+len(s.AgentIDs))
	candidates = append(candidates, s.ActiveAgentID)
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
		al.emitGoalStatusFrame(sessionID, meta.GoalID, meta.GoalCondition, meta.GoalRoundsUsed, meta.GoalMaxRounds,
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
		al.emitGoalStatusFrame(sessionID, meta.GoalID, meta.GoalCondition, meta.GoalRoundsUsed, maxRounds,
			"round-advance persist failed (goal paused)", goalPillActive)
		return true
	}
	al.emitGoalStatusFrame(sessionID, meta.GoalID, meta.GoalCondition, newRound, maxRounds, reason, goalPillActive)
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
