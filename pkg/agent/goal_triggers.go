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
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
// disambiguate. Round/budget-exhaustion brakes (genuine terminal failures,
// not a user choice) still report goalPillFailed, unchanged.
//
// goalPillExpired is a 10th value, ADDED by ADR-086 GOAL-FR-028 (this wave,
// E8): the 7-day idle-expiry calendar brake (D-A's sole terminator for a
// goal that never claims) is now its OWN distinct terminal pill, matching
// contracts/components/schemas/GoalStatusFrame.yaml's `expired` state and
// generated.GoalStateExpired — the fourth member of the goal record's own
// four-way terminal split (met/exhausted/expired/cleared). Before this it
// collapsed into goalPillFailed indistinguishably from a genuine round- or
// budget-exhaustion brake; FR-028 requires the terminal vocabulary to
// "distinguish at least: met, budget or round exhaustion, idle expiry, and
// an explicit operator clear" — four groups, now four distinct pills.
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
	goalPillExpired          = "expired"
	// goalPillBlocked is an 11th value, ADDED by ADR-084 revision 9 JUDGE-FR-093
	// (this wave, E13): a goal_claim(status:blocked) call parks the goal —
	// the agent cannot proceed and it is not a question the operator can
	// answer directly (distinct from waiting_on_user, which asks a
	// question). Matches contracts/components/schemas/GoalStatusFrame.yaml's
	// `blocked` state (landed by wave F1, C-39) and
	// generated.GoalLatestClaimStatusBlocked. Not terminal — the goal stays
	// active, parked, until a genuine operator message resumes it (the same
	// resume path that clears waiting_on_user, JUDGE-FR-093/R-24).
	goalPillBlocked = "blocked"
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

// goalIdleSettleSourceKind / goalClaimDeferredSourceKind are the two
// AsyncNotifyEvent.SourceKind values dispatchGoalAsyncFollowUp's callers
// use (JUDGE-FR-099, this wave): the idle-tick path's push/nudge dispatch
// keeps the pre-existing "goal_idle_settle" value; the claim path's
// deferred (post-delivery, D13) adjudication steer uses a DISTINCT value so
// the two are separable in logs and observers, as FR-099 requires.
const (
	goalIdleSettleSourceKind    = "goal_idle_settle"
	goalClaimDeferredSourceKind = "goal_claim_deferred"
)

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
// (which is outside this file's write-set).
//
// GOAL-FR-052 (E12): a goal is its own entity, addressed by its own id — the
// pre-existing "session id *is* the goal id" assumption these maps' doc
// comments used to assert is retired. bareClaimStreak and waitingOnUser are
// keyed by goal-id below: both are written and read exclusively from
// functions in this file's own write-set (checkGoalLoopAfterTurn's claim
// case, handleBareGoalClaim, maybeSettleGoalIdle — all of which already
// carry a session.UnifiedMeta with GoalID populated), so re-keying them was
// safe to do in this wave. routing, idleSettling and diffBoundaryHash are
// deliberately LEFT session-id-keyed — each has a writer this wave's
// write-set does not reach without creating a real correctness hazard:
// idleSettling's markGoalIdleFired helper is also called from
// settleGoalNormally (wave E13's rewrite, not yet landed as of this
// commit) and re-keying only THIS wave's callers would split one map
// between two key spaces silently; diffBoundaryHash is written from
// pkg/agent/verifier_adjudication.go (wave E9), outside this write-set
// entirely; and goalAdjudicationInFlight's verifierUnitForGoal(sessionID)
// key must stay byte-identical to the key runVerifierAdjudication
// registers under in that SAME E9 file, or the F5 self-race guard silently
// stops working. Re-keying all six in one wave would require touching
// files this wave does not own; this is reported as an incomplete part of
// FR-052 rather than risked as a silent, partial, cross-file key mismatch.
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
	// [goal:evidence]) per goal-id, for the G-4 bounce economics. Keyed by
	// GOAL ID (GOAL-FR-052, E12) — bumpGoalBareClaimStreak/
	// clearGoalBareClaimStreak's callers all pass meta.GoalID.
	bareClaimStreak map[string]int

	// waitingOnUser marks a goal paused via a GOAL_STATUS: waiting_on_user
	// marker (G-5). While true, idle settlement is SUPPRESSED for this
	// goal-id. Keyed by GOAL ID (GOAL-FR-052, E12).
	waitingOnUser map[string]bool

	// routing lets the idle path re-inject a steering turn (populated at
	// /goal set, cleared at /goal clear). STILL keyed by session-id (see
	// this struct's own doc comment for why GOAL-FR-052's re-keying was not
	// extended here).
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
	//
	// DD-7(b)/GOAL-FR-052 (E13, this wave): re-keyed from session-id to
	// GOAL ID, alongside idleSettling below — both writers now live entirely
	// inside this wave's own settleGoalNormally rewrite (FR-097's unified
	// push ladder), so re-keying is safe here in a way it was not for E12
	// (whose write-set did not reach this map's other writer).
	outputWatermarks map[string]time.Time

	// blocked marks a goal paused via a goal_claim(status:blocked) call
	// (JUDGE-FR-093, this wave): the agent cannot proceed and it is not a
	// question the operator can answer directly (distinct from
	// waitingOnUser, which invites a specific answer). While true, idle
	// settlement is SUPPRESSED for this goal-id — mirrors waitingOnUser's
	// own shape exactly (R-24: the durable analogue, Goal.LatestClaim.Status
	// == blocked, is recorded onto the goal record alongside this in-memory
	// gate for restart-survivable evidence — see goalSetBlocked's own doc
	// comment for the one residual gap that leaves). Keyed by GOAL ID.
	blocked map[string]bool

	// claimScanWatermarks is JUDGE-FR-092's own per-goal-id boundary: the
	// transcript timestamp that separates "already resolved by an earlier
	// checkGoalLoopAfterTurn pass" from "produced during the CURRENT turn".
	// Updated unconditionally at the top of checkGoalLoopAfterTurn's claim-
	// resolution step (goal_loop.go), before the scan — deliberately
	// INDEPENDENT of GoalLastActivityAt, which some of that function's exit
	// branches (the waiting_on_user park, the blocked park) do not bump, so
	// a claim already resolved on a prior turn is never re-discovered on a
	// later one just because activity was not bumped in between. Keyed by
	// GOAL ID. In-memory only, cleared on goal clear — the first-ever check
	// for a fresh goal-id has no watermark and scans the whole (necessarily
	// short, since the goal just activated) transcript once.
	claimScanWatermarks map[string]time.Time

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
	bareClaimStreak:     make(map[string]int),
	waitingOnUser:       make(map[string]bool),
	routing:             make(map[string]goalRoute),
	idleSettling:        make(map[string]bool),
	diffBoundaryHash:    make(map[string]string),
	outputWatermarks:    make(map[string]time.Time),
	blocked:             make(map[string]bool),
	claimScanWatermarks: make(map[string]time.Time),
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
	s.blocked = make(map[string]bool)
	s.claimScanWatermarks = make(map[string]time.Time)
	s.sessionStoreResolver = nil
}

// --- per-goal-id state accessors (methods on AgentLoop via the singleton) --

// recordGoalRouting captures the channel/chat/sessionKey a goal's chat lives
// on, so a later idle-settlement unmet verdict can re-inject a steering turn.
// Called from applyGoalCommandPrompt when a goal activates/activates-from-
// pending. Idempotent; cleared by clearGoalTriggerState on /goal clear.
//
// ADR-081 FR-031 (round-2 M-9), re-pointed by GOAL-FR-032/FR-033/FR-034
// (E12): the route's channel/chatID are ALSO persisted onto the goal's OWN
// record (goal.SetRoute, wave S5), not just the in-memory map — a gateway
// restart used to silently disable both the keeper's idle-steer re-inject
// and the channel record echo, since neither had anything durable to read.
// routeFor (below) is the matching read side: in-memory first, persisted
// fallback. The session-key field (GOAL-FR-032) and the agent-id field
// (GOAL-FR-033, folded into the goal's own OwnerKind/OwnerID instead) are
// deliberately NOT persisted anywhere any more — routing.go's own doc
// comment: the session key is read by nothing, and a second persisted copy
// of the agent id is redundant with the owner reference already on the
// record; both survive only in the in-memory map above, for this
// process's own lifetime. SetGoalRouteSessionStore wires FR-031's
// session-store resolver — retained for boot-time wiring even though
// routeFor's own post-E12 read path no longer needs it (see routeFor's doc
// comment); gateway.go (wave E11, outside this wave's write-set) still
// calls it at boot next to SetAskUserRegistry, so the function itself
// stays.
func (al *AgentLoop) SetGoalRouteSessionStore() {
	s := goalTriggers()
	s.mu.Lock()
	if s.sessionStoreResolver == nil {
		s.sessionStoreResolver = al.GetSessionStore
	}
	s.mu.Unlock()
}

func (al *AgentLoop) recordGoalRouting(sessionID, goalID, channel, chatID, sessionKey, agentID string) {
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

	if goalID == "" {
		return
	}
	if _, uerr := resolveGoalRecordStore().Update(goalID, func(cur *goal.Goal) error {
		cur.SetRoute(channel, chatID)
		return nil
	}); uerr != nil {
		logger.WarnCF("agent", "goal trigger: could not persist goal routing onto the goal record",
			map[string]any{"session_id": sessionID, "goal_id": goalID, "error": uerr.Error()})
	}
}

// clearGoalTriggerState clears ALL in-memory trigger state for one goal
// generation — the waiting flag, the bounce streak, the routing entry, and
// the idle-in-flight marker. Called from clearGoal (FR-114: /goal clear
// cancels the in-flight verifier via cancelGoalVerifierIfAny and ALSO resets
// the trigger surface so a later stray GOAL_STATUS: met is inert, N-12).
//
// Takes BOTH sessionID and goalID (GOAL-FR-052, E12/E13): bareClaimStreak,
// waitingOnUser, idleSettling, outputWatermarks, blocked and
// claimScanWatermarks are keyed by goalID; routing and diffBoundaryHash are
// still keyed by sessionID (see goalTriggerState's own doc comment for why
// those two were not re-keyed — diffBoundaryHash's other writer,
// pkg/agent/verifier_adjudication.go, is outside every wave's write-set to
// date). goalID may be empty (a goal cleared before wave E12's
// activation-time record-creation ever ran for it, or a test fixture that
// never populated meta.GoalID) — the goalID-keyed deletes are then no-ops,
// matching this function's pre-existing best-effort semantics.
func (al *AgentLoop) clearGoalTriggerState(sessionID, goalID string) {
	if sessionID == "" && goalID == "" {
		return
	}
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	if goalID != "" {
		delete(s.bareClaimStreak, goalID)
		delete(s.waitingOnUser, goalID)
		delete(s.idleSettling, goalID)
		delete(s.outputWatermarks, goalID)
		delete(s.blocked, goalID)
		delete(s.claimScanWatermarks, goalID)
	}
	delete(s.routing, sessionID)
	delete(s.diffBoundaryHash, sessionID)
}

// goalIsWaitingOnUser reports whether goalID is currently paused via a
// GOAL_STATUS: waiting_on_user marker (G-5). Idle settlement consults this
// to SUPPRESS adjudication while the pause holds. Keyed by GOAL ID
// (GOAL-FR-052, E12) — every caller already carries a session.UnifiedMeta
// with GoalID populated.
func (al *AgentLoop) goalIsWaitingOnUser(goalID string) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitingOnUser[goalID]
}

// goalSetWaitingOnUser sets/clears the waiting_on_user pause flag for
// goalID. Returns the prior value. Keyed by GOAL ID (GOAL-FR-052, E12).
func (al *AgentLoop) goalSetWaitingOnUser(goalID string, v bool) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	prior := s.waitingOnUser[goalID]
	if v {
		s.waitingOnUser[goalID] = true
	} else {
		delete(s.waitingOnUser, goalID)
	}
	return prior
}

// goalMarkIdleSettling marks goalID as "quiet-window adjudication fired,
// re-arm only on new activity" (FR-102). Idempotent within one quiet spell.
// Keyed by GOAL ID (GOAL-FR-052, DD-7(b), E13 — re-keyed alongside
// outputWatermarks when this wave rewrote settleGoalNormally; see
// goalTriggerState's own doc comment for why E12 left both session-id-keyed).
func (al *AgentLoop) goalMarkIdleSettling(goalID string, v bool) {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	if v {
		s.idleSettling[goalID] = true
	} else {
		delete(s.idleSettling, goalID)
	}
}

// goalIsIdleSettling reports whether goalID's quiet-window adjudication has
// fired and is awaiting new activity to re-arm. Keyed by GOAL ID (DD-7(b), E13).
func (al *AgentLoop) goalIsIdleSettling(goalID string) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idleSettling[goalID]
}

// goalIsBlocked reports whether goalID is currently parked via a
// goal_claim(status:blocked) call (JUDGE-FR-093). Idle settlement (and the
// D6c nudge/D14b push ladder, which share this same gate) is SUPPRESSED
// while true — mirrors goalIsWaitingOnUser exactly. Keyed by GOAL ID.
func (al *AgentLoop) goalIsBlocked(goalID string) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blocked[goalID]
}

// goalSetBlocked sets/clears the blocked park flag for goalID. Returns the
// prior value. Keyed by GOAL ID.
//
// R-24's durable analogue — Goal.LatestClaim.Status == blocked, persisted
// via pkg/goal/verdict.go's RecordClaim — is written by the caller
// (goal_loop.go's checkGoalLoopAfterTurn) alongside this in-memory gate, so
// the record itself carries evidence a blocked park happened even across a
// restart. This in-memory flag is still the ACTIVE suppression gate (like
// waitingOnUser, it is forgotten on restart) because pkg/goal's Claim type
// has no "cleared" fourth status to durably record the resume — the same
// restart-survival gap R-24 itself names as accepted for waitingOnUser
// today; adding one would mean a new field on pkg/goal.Goal, outside this
// wave's write-set.
func (al *AgentLoop) goalSetBlocked(goalID string, v bool) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	prior := s.blocked[goalID]
	if v {
		s.blocked[goalID] = true
	} else {
		delete(s.blocked, goalID)
	}
	return prior
}

// goalClaimScanWatermark returns goalID's JUDGE-FR-092 claim-scan watermark
// and whether one has been set yet. See claimScanWatermarks' own doc comment
// on goalTriggerState for what it bounds and why it is independent of
// GoalLastActivityAt.
func (al *AgentLoop) goalClaimScanWatermark(goalID string) (time.Time, bool) {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.claimScanWatermarks[goalID]
	return t, ok
}

// setGoalClaimScanWatermark sets goalID's claim-scan watermark.
func (al *AgentLoop) setGoalClaimScanWatermark(goalID string, t time.Time) {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimScanWatermarks[goalID] = t
}

// bumpGoalBareClaimStreak increments and returns goalID's consecutive
// bare-claim count (G-4). Mirrors TaskExecutor.bumpEvidenceRejectStreak.
// Keyed by GOAL ID (GOAL-FR-052, E12).
func (al *AgentLoop) bumpGoalBareClaimStreak(goalID string) int {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bareClaimStreak[goalID]++
	return s.bareClaimStreak[goalID]
}

// clearGoalBareClaimStreak resets the bounce streak — called when a claim is
// HONORED (evidence present → real adjudication) or the goal clears. Keyed
// by GOAL ID (GOAL-FR-052, E12).
func (al *AgentLoop) clearGoalBareClaimStreak(goalID string) {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bareClaimStreak, goalID)
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
	rec *goal.Goal,
	claimText string,
	deliverSteer func(steer string),
) (met bool) {
	if agentInst == nil || rec == nil || store == nil || sessionID == "" {
		return false
	}
	gstore := resolveGoalRecordStore()
	// JUDGE-FR-095 (D13, this wave): the claimless-adjudication contract is
	// retired — every remaining call site is claim-triggered, so an empty
	// claimText is refused rather than silently adjudicated against
	// nothing. The idle path's own former empty-claimText call
	// (settleGoalNormally) no longer exists; this guard is the tripwire
	// that keeps a claimless call site from silently returning if one is
	// ever reintroduced by a future merge.
	if strings.TrimSpace(claimText) == "" {
		logger.WarnCF("agent", "goal: refusing a claimless adjudication (JUDGE-FR-095 retires the claimless contract)",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID})
		return false
	}
	// Emit the ephemeral judging pill BEFORE dispatch (D14 crosswalk: judging
	// ← ephemeral engine-phase signal, pill-only).
	al.emitGoalStatusFrame(sessionID, rec.GoalID, rec.Prompt, rec.Round, rec.MaxRounds, rec.LatestReason, goalPillJudging)

	// ADR-086: the judged set comes from the record's own typed Criteria/DoD
	// lists (GOAL-FR-003) rather than the retired GoalCriteriaJSON string.
	// compiledGoalCriteriaFor still owns the union + condition-fallback rule,
	// so the shape fed to the Judge is byte-identical to before.
	criteria := compiledGoalCriteriaFor(goalRecordCompiledJSON(rec), rec.Prompt, sessionID)
	attempt := rec.Round + 1

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
		// CLAIM path (including its D13 deferred dispatch), which never sets
		// the marker.
		al.goalMarkIdleSettling(rec.GoalID, false)
		logger.WarnCF("agent", "goal trigger: judge unavailable, round not consumed",
			map[string]any{"session_id": sessionID, "reason": jr.Reason, "claim_text_len": len(claimText)})
		al.emitGoalStatusFrame(sessionID, rec.GoalID, rec.Prompt, rec.Round, rec.MaxRounds, jr.Reason, goalPillJudgeUnavailable)
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
	bumpGoalRecordActivity(rec.GoalID, time.Now().UTC())

	verdict := jr.Verdict
	al.writeGoalVerdictTranscript(store, sessionID, verdict)
	// ADR-081 D8: the verdict summary — the event D8 names that this file
	// previously had no INFO line for at all.
	logger.InfoCF("agent", "goal: verdict computed",
		map[string]any{
			"session_id": sessionID, "goal_id": rec.GoalID,
			"met": verdict != nil && verdict.Met, "attempt": attempt,
		})

	// GOAL-FR-036/FR-040/FR-041 (E14, verdict_projection.go): project the
	// verdict's per-criterion outcomes onto the goal record's OWN Criteria
	// and DoD lists — the goal-scope recording site of the projection's
	// three (task_executor.go::adjudicateClaim and
	// plan_engine.go::applyJudgeRoundOutcome are the other two). Reads the
	// record's two SEPARATE lists straight from pkg/goal.Store — never the
	// flattened Criteria ∪ DoD union `criteria` (above) fed to the Judge —
	// so projectGoalVerdict's de-union (FR-041) splits the verdict by real
	// list membership rather than by any property of the id itself (C-21;
	// the flat union is only ever a JUDGE INPUT shape, never a projection
	// input). Runs on BOTH met and unmet outcomes (FR-040 is not
	// conditioned on the overall verdict) and BEFORE the met branch below
	// calls clearGoal, so clearGoal's own terminal frame (E8/C-06) already
	// re-reads a record carrying the freshly-projected statuses — the
	// interlock C-06 requires without this file reaching into clearGoal
	// itself. A re-read failure is warn-logged and the projection is skipped
	// rather than fatal, matching the fail-safe pattern this file's own
	// routeFor already uses for the same store.
	//
	// ADR-086: the record is re-read BY ID (never re-resolved by owner) —
	// this function already holds the goal it is adjudicating, and the
	// owner-keyed GetActiveByOwner(session, sessionID) lookup this block used
	// to do would find nothing at all for a task-owned goal (GOAL-FR-013).
	// It is re-read rather than reused so a `set_goal` write landing during
	// the judge call is not silently overwritten by a stale in-memory copy.
	var projectedCriteria, projectedDoD []task.AcceptanceCriterion
	var goalDefinition string
	if fresh, gerr := gstore.Get(rec.GoalID); gerr != nil || fresh == nil {
		logger.WarnCF("agent", "goal trigger: could not re-read the goal record for verdict projection",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "error": errString(gerr)})
	} else {
		goalDefinition = fresh.Definition
		uc, ud, pstats := projectGoalVerdict(fresh.Criteria, fresh.DoD, verdict)
		// silent-SF-5 (review): the projection is EMITTED TO THE UI further
		// down, and it used to be emitted whether or not the store accepted
		// it. The user watched the criterion ticks flip to met while the
		// durable record still said pending, and on the next reload they
		// reverted — a persistence fault rendered as a success, which is the
		// one failure shape a user can neither see nor report accurately.
		// The frame now carries the DURABLE truth: on a rejected Update the
		// emitted lists fall back to the record's own re-read (unprojected)
		// state, so the ticks that flip are exactly the ticks that landed.
		// projectVerdictOntoCriteria copies its input (verdict_projection.go),
		// so fresh.Criteria/fresh.DoD are still the pre-projection values here
		// and are safe to fall back to.
		projectedCriteria, projectedDoD = fresh.Criteria, fresh.DoD
		if pstats.Applied > 0 {
			if _, uerr := gstore.Update(fresh.GoalID, func(cur *goal.Goal) error {
				cur.Criteria = uc
				cur.DoD = ud
				return nil
			}); uerr != nil {
				logger.WarnCF("agent",
					"goal trigger: could not persist the verdict projection onto the goal record (GOAL-FR-036) — emitting the durable (unprojected) criterion statuses instead",
					map[string]any{"session_id": sessionID, "goal_id": fresh.GoalID, "error": uerr.Error()})
			} else {
				projectedCriteria, projectedDoD = uc, ud
			}
		} else {
			projectedCriteria, projectedDoD = uc, ud
		}
	}

	// maxRounds and reasonText are computed BEFORE the met branch (they used
	// to sit after it) because the met path now needs reasonText for its own
	// RecordVerdict call and maxRounds for the frame it emits when the
	// terminal transition cannot be completed. Neither depends on the
	// verdict's met-ness.
	maxRounds := rec.MaxRounds
	if maxRounds < 1 {
		maxRounds = config.DefaultGoalMaxRounds
	}
	reasonText := goalVerdictReasonText(verdict)

	if verdict != nil && verdict.Met {
		// Review finding 9: a SUCCESSFUL goal used to terminate having
		// persisted NOTHING. RecordVerdict was reached only on the unmet path
		// below, so a met goal ended with LatestVerdict == nil, Round still at
		// its pre-adjudication value and LatestReason unchanged — directly
		// contradicting Goal.Terminate's own contract ("the record survives
		// with its criteria, their final statuses, THE VERDICT, the reason").
		// For a task-owned goal the consequence was worse than a thin record:
		// the next run's Goal.Reactivate appends a TerminalHistory entry built
		// from those fields, so a successful run left an entry reading
		// {Verdict: nil, Round: 0} — no history at all for the only outcome
		// anyone wants a history of.
		//
		// The round is pinned to `attempt` after RecordVerdict's own Round++,
		// exactly as the unmet branch below does, so both outcomes leave the
		// same counter arithmetic behind.
		if _, perr := gstore.Update(rec.GoalID, func(cur *goal.Goal) error {
			if rerr := cur.RecordVerdict(verdict, reasonText, time.Now().UTC()); rerr != nil {
				return rerr
			}
			cur.Round = attempt
			return nil
		}); perr != nil {
			// Same discipline as the unmet branch's silent-M3 abort: do NOT
			// terminate a goal whose winning verdict the store refused, or the
			// record is frozen `met` with no evidence of why, permanently.
			// The goal stays ACTIVE and a later claim re-adjudicates.
			logger.ErrorCF("agent", "goal trigger: could not persist the winning verdict; NOT terminating the goal",
				map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "attempt": attempt, "error": perr.Error()})
			al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, fmt.Sprintf(
				"Goal %q reached a MET verdict but it could not be persisted (%v). The goal is still active — investigate storage and claim completion again.",
				rec.Prompt, perr))
			al.emitGoalStatusFrameWithCriteriaAndDoD(sessionID, rec.GoalID, rec.Prompt, rec.Round, maxRounds,
				"met verdict could not be persisted (goal still active)", goalPillActive,
				goalDefinition, projectedCriteria, projectedDoD)
			return false
		}
		// Use the constant, not a bare literal. clearGoal switches on this note
		// to decide the terminal pill state, so a literal here means editing
		// goalClearNoteMet would silently stop matching and reclassify a MET
		// goal as `failed` — the exact drift the constant was introduced to
		// prevent. (Its doc comment claimed both call sites were converted;
		// this one was missed.)
		//
		// silent-SF-8 (review): the return value used to be DISCARDED. When
		// the terminal transition failed, clearGoal skipped every terminal
		// side effect — including the terminal pill — and this function
		// returned true anyway, so the goal card sat on `judging` forever with
		// nothing in the UI ever correcting it. Honour the result: on a failed
		// transition re-paint the card as active with the real reason, so the
		// user sees a live goal rather than a frozen spinner.
		if _, ok := al.clearGoalStatus(sessionID, store, goalClearNoteMet); !ok {
			logger.ErrorCF("agent", "goal trigger: met verdict persisted but the terminal transition failed; leaving the goal active",
				map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "attempt": attempt})
			al.emitGoalStatusFrameWithCriteriaAndDoD(sessionID, rec.GoalID, rec.Prompt, attempt, maxRounds,
				"met, but the terminal transition could not be persisted — will retry", goalPillActive,
				goalDefinition, projectedCriteria, projectedDoD)
			return false
		}
		return true
	}

	if attempt >= maxRounds {
		// silent-SF-7 (review): the handover used to be written to the user's
		// OWN transcript BEFORE the transition was attempted. On a store
		// failure the user read "Goal X did not reach a MET verdict" while the
		// record was still ACTIVE and the keeper kept pushing it. Attempt the
		// transition first and write the handover only once it has actually
		// landed; on failure the goal stays visibly active and clearGoal's own
		// deferral re-drives it on the next turn.
		note := fmt.Sprintf("round bound reached (%d/%d)", attempt, maxRounds)
		if _, ok := al.clearGoalStatus(sessionID, store, note); !ok {
			logger.ErrorCF("agent", "goal trigger: round-bound termination failed; no handover written (the goal is still active)",
				map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "attempt": attempt, "max_rounds": maxRounds})
			return false
		}
		handover := fmt.Sprintf(
			"Goal %q did not reach a MET verdict within %d round(s). Latest judge feedback:\n%s",
			rec.Prompt, maxRounds, reasonText,
		)
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, handover)
		return false
	}

	newRound := attempt
	// ADR-086: the round counter and the steering reason live on the goal
	// record (Round / LatestReason), not on the retired session-meta
	// GoalRoundsUsed / GoalLatestReason fields. RecordVerdict is used when a
	// real verdict exists so the adjudication's own verdict is retained on
	// the record too (GOAL-FR-006); its Round++ is then pinned back to
	// `newRound`, which is exactly the value the old SetMeta wrote, so the
	// exhaustion gate's arithmetic is unchanged.
	if _, perr := gstore.Update(rec.GoalID, func(cur *goal.Goal) error {
		if verdict != nil {
			if rerr := cur.RecordVerdict(verdict, reasonText, time.Now().UTC()); rerr != nil {
				return rerr
			}
			cur.Round = newRound
			return nil
		}
		cur.Round = newRound
		cur.LatestReason = reasonText
		cur.LastActivityAt = time.Now().UTC()
		return nil
	}); perr != nil {
		// silent-M3 (Phase-2 review): do NOT silently continue on an
		// un-persisted round counter. The exhaustion gate (attempt >=
		// maxRounds) re-reads the record's Round on the next adjudication;
		// a failed Update leaves the persisted counter unchanged, so the
		// store stays at the OLD value while this in-memory branch treated
		// the round as consumed. If execution proceeded, the gate would
		// never fire and the goal could re-loop on the same attempt number
		// indefinitely (each re-fire burning tokens via the steer
		// re-dispatch). Abort THIS adjudication's round-advance: the
		// verifier turn already ran, but the round is NOT counted, no
		// advance is emitted, and no steer is delivered (so the goal is not
		// re-dispatched on a counter the store can't durably advance). The
		// failure is surfaced loudly (ERROR + system transcript) so an
		// operator sees the storage fault rather than a quietly-stale
		// counter; the unbounded-spend backstop is the app-level token
		// budget brake. This is the "don't consume the round" minimal option
		// from the review direction.
		logger.ErrorCF("agent", "goal trigger: round-advance persist failed; aborting adjudication to keep round gate honest",
			map[string]any{"session_id": sessionID, "attempt": attempt, "max_rounds": maxRounds, "error": perr.Error()})
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, fmt.Sprintf(
			"Goal %q: could not persist the adjudication round counter (%v). The round was not counted and no follow-up was dispatched — investigate storage and retry the goal.",
			rec.Prompt, perr))
		// C-22 (criteria-carrying frame swap, E14): this and the follow-up
		// active-path emission below are the two call sites in this
		// function that must swap from the criteria-less emitGoalStatusFrame
		// to the criteria-carrying form — the frame this function emitted
		// BEFORE dispatch (the judging pill, above) and the Unavailable
		// branch's own frame (E8's region, untouched by this wave) both stay
		// criteria-less by design; only a call site downstream of a REAL
		// verdict has anything new to carry.
		al.emitGoalStatusFrameWithCriteriaAndDoD(sessionID, rec.GoalID, rec.Prompt, rec.Round, maxRounds,
			"round-advance persist failed (goal paused)", goalPillActive, goalDefinition, projectedCriteria, projectedDoD)
		return false
	}
	al.emitGoalStatusFrameWithCriteriaAndDoD(sessionID, rec.GoalID, rec.Prompt, newRound, maxRounds, reasonText, goalPillActive,
		goalDefinition, projectedCriteria, projectedDoD)
	if deliverSteer != nil {
		deliverSteer(goalSteeringPrompt(rec.Prompt, reasonText))
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

	// ADR-086: the goal-bearing set is pkg/goal's own ACTIVE records, across
	// BOTH owner kinds (C-24/C-25's shared selector), replacing the retired
	// session.ListSessions() + `GoalCondition != ""` scan.
	//
	// GOAL-FR-015 is explicit that this selector covers task-owned goals too:
	// "Both the after-turn claim driver and the quiet-window idle keeper MUST
	// apply to both owner kinds … and goalQuietWindowSettle's goal-bearing
	// test MUST select task-owned goals" (C-24 restates it). An earlier
	// ListActiveByOwnerKind(session) filter here cited GOAL-FR-020 as its
	// justification; that was a MISREADING. FR-020 says the RECORDLESS NUDGE
	// LADDER (goalNudgePrompt / dispatchGoalFallbackCompile) stays one code
	// path and is unreachable for a task goal — unreachable because a task
	// goal's criteria are mandatory at creation (GOAL-FR-047/D-C), so
	// `len(rec.Criteria) == 0` is never true for one, NOT because the keeper
	// skips task-owned records. Excluding them here made a running task that
	// goes quiet invisible to every keeper driver: the bounded continue-push
	// (GOAL-FR-017), the six suppressions (GOAL-FR-016) and the one-action-
	// per-quiet-spell re-arm (GOAL-FR-018) all hang off this loop.
	//
	// Records still DEFINING are excluded by ListActive itself (GOAL-FR-009,
	// A-11: a task's goal sits in the definition phase until its run mints a
	// session, and no keeper driver may reach it there).
	active, err := resolveGoalRecordStore().ListActive()
	if err != nil {
		logger.WarnCF("agent", "goal idle settle: list active goal records failed", map[string]any{"error": err.Error()})
		return
	}
	for i := range active {
		rec := &active[i]
		sessionID := rec.ActiveSessionID
		if sessionID == "" {
			logger.WarnCF("agent", "goal idle settle: active goal record has no active_session_id — skipping",
				map[string]any{"goal_id": rec.GoalID, "owner_id": rec.OwnerID})
			continue
		}
		// ADR-086 parity: a CHAT goal's session lives in the shared store, but a
		// TASK goal's session is minted per-agent by
		// task_executor.go::createTaskSessionSync via GetAgentStore. Reading only
		// the shared store here missed EVERY task-owned goal — GetMeta returned
		// ENOENT and the record was skipped before one keeper precondition ran,
		// so a quiet task was never nudged, pushed or re-armed. ResolveSessionStore
		// tries the shared store first (so chat behaviour is byte-identical) and
		// then finds the per-agent owner, which is the owner-kind-agnostic
		// property GOAL-FR-014 requires. Measured marginal cost: ~4.6us per
		// task-owned goal per 30s sweep. Do NOT narrow this back to one store.
		recStore := al.ResolveSessionStore(sessionID)
		if recStore == nil {
			recStore = store
		}
		meta, merr := recStore.GetMeta(sessionID)
		if merr != nil || meta == nil {
			logger.WarnCF("agent", "goal idle settle: could not read the goal's session meta — skipping",
				map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "error": errString(merr)})
			continue
		}
		al.maybeSettleGoalIdle(now, recStore, meta, rec)
	}
}

// maybeSettleGoalIdle evaluates ONE session's idle preconditions and fires a
// claimless adjudication if all hold. Split out so the per-goal logic is
// independently testable.
func (al *AgentLoop) maybeSettleGoalIdle(now time.Time, store *session.UnifiedStore, s *session.UnifiedMeta, rec *goal.Goal) {
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
	if al.goalIsWaitingOnUser(rec.GoalID) {
		return
	}
	// JUDGE-FR-093/FR-016 (E13): a goal_claim(status:blocked) park suppresses
	// idle settlement exactly like the waiting_on_user pause above — the
	// agent said it cannot proceed and it is not a question the operator can
	// answer, so there is nothing for the keeper to push toward.
	if al.goalIsBlocked(rec.GoalID) {
		return
	}
	// FR-102 re-arm: a previous quiet-window adjudication already fired for
	// this goal-id and no new activity has re-armed it yet — do not fire a
	// second verdict against the same quiet spell. Keyed by GOAL ID
	// (DD-7(b), E13).
	if al.goalIsIdleSettling(rec.GoalID) {
		return
	}
	// F5 self-race guard: the verifier's own turn counts as activity. If an
	// adjudication is currently in-flight for this goal, the idle timer must
	// not race a second verdict against it.
	if al.goalAdjudicationInFlight(sessionID) {
		return
	}
	last := effectiveGoalActivity(rec)
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
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID})
		bumpGoalRecordActivity(rec.GoalID, now.UTC())
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
		// silent-SF-7 (review): transition FIRST and honour the result — a
		// handover written ahead of a refused transition tells the user the
		// goal stopped while the record is still active and the keeper keeps
		// sweeping it.
		if _, ok := al.clearGoalStatus(sessionID, store, FailedReasonBudgetExhausted); !ok {
			logger.WarnCF("agent", "goal idle settle: budget-exhausted termination failed; no handover written (the goal is still active)",
				map[string]any{"session_id": sessionID, "goal_id": rec.GoalID})
			return
		}
		handover := fmt.Sprintf(
			"Goal %q stopped: the overall token budget is exhausted (consumed %d tokens).",
			rec.Prompt, tb.Consumed())
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, handover)
		return
	}

	// FR-102: mark fired + bump activity so the next tick (still inside this
	// quiet spell) does NOT fire a second adjudication/nudge/push. The mark
	// clears once a dispatched follow-up turn completes (genuine activity);
	// the activity bump also re-arms the multi-day idle-expiry clock.
	// Persist the marker BEFORE dispatch so a concurrent tick observes it.
	al.markGoalIdleFired(rec.GoalID, now)

	// ADR-086: "recordless" is the goal record's own criteria list being
	// empty (GOAL-FR-003) — the direct replacement for the retired
	// `GoalCriteriaJSON == ""` session-meta check.
	if len(rec.Criteria) == 0 {
		// ADR-081 D3/FR-014/D6c: a RECORDLESS goal (active ∧ empty record) is
		// NEVER judged at idle — there is nothing to adjudicate. The keeper's
		// only action is the nudge ladder.
		al.settleRecordlessGoal(store, s, rec, agentInst)
		return
	}

	// JUDGE-FR-095/FR-097 (D13, this wave): the claimless-adjudication path
	// is RETIRED — a `met` claim is now the sole adjudication trigger
	// (checkGoalLoopAfterTurn, goal_loop.go). A quiet or stalled RECORDED
	// goal is re-posted, never judged, so the FR-014b zero-adjudicable-
	// output triple (whether there is genuinely something for the Judge to
	// look at) no longer has anything left to gate: with adjudication gone
	// from this path, "the triple holds" and "the triple does not hold" led
	// to the SAME action (a bounded continue-push) — this is that
	// unification, not a behavior change to which recorded goals get
	// pushed. settleGoalNormally is unconditional now.
	al.settleGoalNormally(sessionID, rec)
}

// settleGoalNormally is JUDGE-FR-095/FR-097's unified push ladder (D13, this
// wave — retired the claimless `runGoalAdjudication` call this function used
// to make, and absorbed the former settleZeroOutputRecordedGoal, which did
// the identical thing under a name that implied a distinction — "zero
// output" vs. "genuine output but still quiet" — this design no longer
// draws): dispatch the SAME bounded continue-push
// (`al.dispatchGoalAsyncFollowUp(sessionID, goalContinuePushPrompt(...))`)
// settleZeroOutputRecordedGoal already used, up to goalZeroOutputPushMax (2)
// times.
//
// Past the push budget: FR-097 is explicit that this function "MUST NOT
// push forever, and MUST NOT fall through to an adjudication when the
// budget is spent" — the old `if pushes >= max { settleGoalNormally(...) }`
// fall-through (the last remaining claimless call site) is exactly the
// shape FR-095 forbids, so it is deleted outright rather than merely
// reached less often. Past the budget this function is a no-op: the goal is
// left quiet, and per D-A its SOLE remaining terminator is the seven-day
// idle-expiry sweep (goalIdleExpirySweep, owned by E8) — never a round
// bound consumed by this path, since no adjudication ever runs here again.
// A fresh worker claim (goal_claim/GOAL_STATUS: met) still resumes the goal
// normally through checkGoalLoopAfterTurn at any time.
// ADR-086: the session store, the session meta and the agent instance are
// all gone from this function's parameter list — every piece of state it
// reads or writes (the push counter, the goal statement, the goal id) now
// lives on the goal record, and the dispatch it makes is keyed by session id
// alone.
func (al *AgentLoop) settleGoalNormally(sessionID string, rec *goal.Goal) {
	// ADR-086 (GOAL-FR-004): the push counter is the goal record's own
	// ZeroOutputPushes, relocated off the retired session-meta field so it
	// applies identically to both owner kinds.
	if rec.ZeroOutputPushes >= goalZeroOutputPushMax {
		logger.InfoCF("agent", "goal idle settle: push budget spent — leaving the goal quiet (D-A: the idle-expiry sweep is the sole remaining terminator)",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "pushes": rec.ZeroOutputPushes})
		return
	}
	newCount := rec.ZeroOutputPushes + 1
	if _, perr := resolveGoalRecordStore().Update(rec.GoalID, func(cur *goal.Goal) error {
		cur.ZeroOutputPushes = newCount
		return nil
	}); perr != nil {
		// silent-SF-6 (review): this used to WARN and dispatch anyway. The
		// bound above is re-read from the STORE on every tick, so with the
		// store unwritable the counter never moves and the "bounded" ladder
		// becomes an unbounded one — a push every quiet window, forever,
		// each one a real agent turn spending real tokens. Do not dispatch a
		// push whose cost cannot be counted. The idleSettling marker set by
		// markGoalIdleFired before this call is cleared so the next tick
		// retries once the store recovers, rather than wedging the goal (the
		// corr-MAJOR-2 failure mode).
		logger.ErrorCF("agent", "goal trigger: could not persist continue-push count; NOT dispatching (an uncounted push is an unbounded ladder)",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "error": perr.Error()})
		al.goalMarkIdleSettling(rec.GoalID, false)
		return
	}
	logger.InfoCF("agent", "goal idle settle: continue-push dispatched (unified ladder, D13 — no adjudication)",
		map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "push_count": newCount})
	al.dispatchGoalAsyncFollowUp(sessionID, rec.GoalID, goalIdleSettleSourceKind, goalContinuePushPrompt(rec.Prompt))
}

// settleRecordlessGoal is ADR-081 D6c's nudge ladder for a RECORDLESS active
// goal at idle (no parked card, quiet turn machinery): dispatch a nudge
// telling the working agent to register its record via set_goal, up to
// goalZeroOutputPushMax (N=2) times; on the (N+1)th observation (still
// recordless after two nudges) run the D7 engine fallback compile instead.
func (al *AgentLoop) settleRecordlessGoal(
	store *session.UnifiedStore, s *session.UnifiedMeta, rec *goal.Goal, agentInst *AgentInstance,
) {
	sessionID := s.ID
	if rec.ZeroOutputPushes >= goalZeroOutputPushMax {
		al.dispatchGoalFallbackCompile(store, s, rec, agentInst)
		return
	}
	newCount := rec.ZeroOutputPushes + 1
	if _, perr := resolveGoalRecordStore().Update(rec.GoalID, func(cur *goal.Goal) error {
		cur.ZeroOutputPushes = newCount
		return nil
	}); perr != nil {
		// silent-SF-6 (review): identical reasoning to settleGoalNormally's
		// ladder above — the nudge bound is re-read from the store every
		// tick, so an unpersisted count makes this ladder unbounded too, and
		// it would additionally never reach the (N+1)th-observation fallback
		// compile. Do not dispatch; clear the idleSettling marker so the next
		// tick retries instead of wedging.
		logger.ErrorCF("agent", "goal trigger: could not persist nudge count; NOT dispatching (an uncounted nudge is an unbounded ladder)",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "error": perr.Error()})
		al.goalMarkIdleSettling(rec.GoalID, false)
		return
	}
	logger.InfoCF("agent", "goal: keeper nudge dispatched",
		map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "nudge_count": newCount})
	al.dispatchGoalAsyncFollowUp(sessionID, rec.GoalID, goalIdleSettleSourceKind, goalNudgePrompt(rec.Prompt, newCount))
}

// markGoalIdleFired records that THIS idle check is about to take an action
// (adjudicate, push, or nudge) — the shared FR-102 re-arm bookkeeping every
// action branch needs before dispatching: mark idleSettling so a concurrent/
// next tick does not double-fire, and bump GoalLastActivityAt so the
// multi-day idle-expiry clock and the quiet-window math both reflect "we
// just looked at this goal". Extracted so all four action paths
// (settleGoalNormally, settleZeroOutputRecordedGoal, settleRecordlessGoal,
// dispatchGoalFallbackCompile) share one implementation.
// ADR-086 (GOAL-FR-004): the activity clock is the goal record's own
// LastActivityAt, so the session store and session id this used to need are
// gone from the parameter list.
func (al *AgentLoop) markGoalIdleFired(goalID string, now time.Time) {
	al.goalMarkIdleSettling(goalID, true)
	bumpGoalRecordActivity(goalID, now.UTC())
}

// goalClaimInstructionLine is D-A's fix line (operator directive, grill
// round 1): both prompt builders below MUST tell the agent that if it
// believes the work is done, it must say so by calling the claim tool
// (goal_claim), naming the tool — not merely retype the goal. This fixes the
// CAUSE the operator identified (the agent does not know it has to claim),
// rather than adding any terminator/bound to the nudge ladder itself (D-A
// explicitly forbids that: the seven-day idle-expiry sweep, owned by E8,
// stays the sole terminator for a goal that never claims).
const goalClaimInstructionLine = "If you believe this goal's work is already done, say so by calling the goal_claim tool (status: met, with evidence) — do not just restate the goal."

// goalNudgePrompt is D6c's nudge turn content: a system-authored prompt
// telling the agent to register its goal record now, citing the durable
// GoalCondition (never the transcript — E8/S-38: the window may have
// trimmed the original goal message away, but the durable record survives).
// D-A: also carries goalClaimInstructionLine, since a recordless goal can
// still already be finished — nothing about the missing record implies the
// work itself is incomplete.
func goalNudgePrompt(condition string, nudgeCount int) string {
	return fmt.Sprintf(
		"You have an active goal but have not yet registered a working record for it.\n\n"+
			"Goal: %s\n\n"+
			"Call set_goal now (mode: register) with your restated statement, acceptance criteria, "+
			"and Definition of Done — your best understanding is enough; state any assumptions. "+
			"(nudge %d of %d before the engine registers one for you)\n\n%s",
		condition, nudgeCount, goalZeroOutputPushMax, goalClaimInstructionLine,
	)
}

// goalContinuePushPrompt is FR-014b/FR-097's bounded continue-push content
// (D13, this wave): no verdict is being reported (there is nothing to
// report — the Judge never runs on this path any more), just a nudge to
// keep working, sourced from the durable GoalCondition (E8/S-38). D-A: also
// carries goalClaimInstructionLine — a goal that goes quiet is exactly the
// goal most likely to already be finished with the agent simply never
// having said so.
func goalContinuePushPrompt(condition string) string {
	return fmt.Sprintf(
		"Continue working toward the goal: %s\n\n"+
			"No new output has been observed since the last check. Keep going.\n\n%s",
		condition, goalClaimInstructionLine,
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
//
// review-round-1 (keeper wedge): unlike dispatchGoalAsyncFollowUp, this
// function NEVER hands off to a dispatched turn — it writes the record
// directly via SetMeta, engine-side. checkGoalLoopAfterTurn's
// bumpGoalActivityOnTurn (the ONLY place that clears the idleSettling
// marker markGoalIdleFired set before this call was reached) therefore
// never runs for this path, on success OR failure. Without the unconditional
// defer below, the keeper wedges permanently at the goalIsIdleSettling gate
// the very first time nudge exhaustion is reached — every exit here must
// clear the marker itself.
func (al *AgentLoop) dispatchGoalFallbackCompile(
	store *session.UnifiedStore, s *session.UnifiedMeta, rec *goal.Goal, agentInst *AgentInstance,
) {
	sessionID := s.ID
	defer al.goalMarkIdleSettling(rec.GoalID, false)
	logger.WarnCF("agent", "goal fallback compile invoked after nudge exhaustion",
		map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "nudges": rec.ZeroOutputPushes})

	var fc FeasibilityContext
	if agentInst != nil {
		fc = agentFeasibilityContext{agentInst: agentInst}
	}
	fallbackCtx, cancel := context.WithTimeout(context.Background(), goalJudgeRoundTimeout)
	defer cancel()
	outcome := al.compileGoalIntentLLM(fallbackCtx, agentInst, fc, rec.Prompt, sessionID, "", "", false, s.WorkspaceID)

	if outcome.Result.Rejection != nil || outcome.Result.Goal == nil {
		reason := "fallback compile produced no usable record"
		if outcome.Result.Rejection != nil {
			reason = outcome.Result.Rejection.Reason
		}
		logger.ErrorCF("agent", "goal fallback compile produced no usable record",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "reason": reason})
		return
	}

	criteriaJSON, merr := marshalCompiledGoal(outcome.Result.Goal)
	if merr != nil {
		logger.ErrorCF("agent", "goal fallback compile: could not marshal compiled record",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "error": merr.Error()})
		return
	}
	// ADR-086 (GOAL-FR-003): the engine-authored record lands on the goal
	// record's own typed Criteria/DoD lists — the same destination a
	// `set_goal` call's WriteRecord writes to — rather than the retired
	// GoalCriteriaJSON session-meta string. The DoD floor is applied here
	// because SetDoD refuses an empty definition of done (D11/D15), matching
	// the floor layer every other authoring path already applies.
	now := time.Now().UTC()
	reason := "engine-authored fallback record registered after nudge exhaustion"
	dod := outcome.Result.Goal.DoD
	if len(dod) == 0 {
		dod = newFloorDoD()
	}
	if _, perr := resolveGoalRecordStore().Update(rec.GoalID, func(cur *goal.Goal) error {
		cur.Definition = outcome.Result.Goal.Definition
		if serr := cur.SetCriteria(outcome.Result.Goal.Criteria, now); serr != nil {
			return serr
		}
		if serr := cur.SetDoD(dod, now); serr != nil {
			return serr
		}
		// The recorded-goal ladder starts fresh (goalZeroOutputPushMax's
		// shared-counter rule).
		cur.ZeroOutputPushes = 0
		cur.LatestReason = reason
		return nil
	}); perr != nil {
		logger.ErrorCF("agent", "goal fallback compile: could not persist engine-authored record",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "error": perr.Error()})
		return
	}
	logger.InfoCF("agent", "goal: engine-authored fallback record registered",
		map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "used_deterministic_parser": outcome.UsedFallback})
	// review-round-1 finding #11: route the fallback registration through
	// the SAME shared post-write path set_goal and the marker paths (#8)
	// use, instead of a raw frame emission — the bare
	// emitGoalStatusFrameWithCriteriaAndDoD call this replaced skipped the
	// FR-020 channel echo entirely, on the path MOST likely to serve
	// channel goals (a channel-origin recordless goal that never got a
	// set_goal call from its own agent within two nudges).
	al.afterGoalRecordWrite(sessionID, criteriaJSON, reason)

	// ADR-082 D9 (review CR8): this is the path MOST likely to produce a
	// record the operator never sees — no turn ran, no tool ran, and under
	// D9 the card renders only from a set_goal call's own result. Anchor
	// the engine-authored record as a synthetic set_goal(mode:register)
	// call: transcript entry (replays after reload) plus live start/end
	// frames (the card appears now on a bound webchat connection).
	route := goalTriggers().routeFor(sessionID)
	anchorAgentID := route.agentID
	if agentInst != nil {
		anchorAgentID = agentInst.ID
	}
	if _, aerr := al.anchorGoalRecordInTranscript(goalRecordAnchor{
		store: store, sessionID: sessionID, goalID: rec.GoalID, agentID: anchorAgentID, chatID: route.chatID,
		mode:      tools.SetGoalModeRegister,
		narration: goalAnchorNarrationFallback,
		record:    outcome.Result.Goal,
		assumptions: []string{
			"Engine-authored fallback record after nudge exhaustion (ADR-081 D7): the agent never called set_goal, so the goal statement was compiled by the engine.",
		},
	}); aerr != nil {
		return
	}
	// A web-routed goal gets no FR-020 channel echo (the frame is the
	// surface there), and no turn follows this write to close the SPA's
	// bubble: the live tool_call_start above opens an assistant bubble on
	// the bound connection and nothing else would ever finalize it. Deliver
	// the narration as one ordinary outbound message — the webchat channel
	// turns it into token+done frames (pkg/gateway/webchat_channel.go's
	// Send), which lands the text beside the card and closes the bubble,
	// while the transcript entry written above stays the single durable
	// copy (webchat Send never writes the transcript itself). AgentID is
	// deliberately left empty: this is a system-originated send
	// (bus.OutboundMessage.AgentID's contract), not a send_message call.
	if route.channel == goalForcingWebChannel && al.bus != nil {
		if perr := al.bus.PublishOutbound(context.Background(), bus.OutboundMessage{
			Channel:   route.channel,
			ChatID:    route.chatID,
			SessionID: sessionID,
			Content:   goalAnchorNarrationFallback,
		}); perr != nil {
			logger.WarnCF("agent", "goal fallback compile: narration delivery to webchat failed",
				map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "error": perr.Error()})
		}
	}
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

// idleSteerDeliverer returns a deliverSteer func that re-injects an unmet
// verdict's steering via the async-notifier — the SAME re-inject seam
// checkGoalLoopAfterTurn's own claim path uses (deferred dispatch, D13) and
// the tick path used to use. An unmet verdict's steer re-dispatch IS new
// activity (G-2): the Notify-originated turn bumps GoalLastActivityAt via
// checkGoalLoopAfterTurn's activity path, re-arming the quiet window.
//
// ADR-081 D6b (the un-wedge fix): dispatchGoalAsyncFollowUp stamps the
// notify event's SenderCanonicalID as goalLoopFollowUpSenderID — the SAME
// sentinel checkGoalLoopAfterTurn's origin gate accepts. Before this, the
// notifier's default "async:<kind>" stamping meant the re-injected steer
// turn was silently DROPPED at the gate: activity never bumped, idleSettling
// never cleared, and the idle keeper fired exactly once per goal, then
// wedged forever (the defect this ADR names verbatim). JUDGE-FR-099 (D13,
// this wave): this is now ALSO the deferred claim-path adjudication's own
// delivery mechanism, replacing the pre-D13 claim path's result.followUps
// append, which a deferred (post-delivery, off-critical-path) dispatch has
// no turnResult left to attach to by the time it runs. sourceKind is
// FR-099's own requirement: a value DISTINCT from the idle path's
// "goal_idle_settle" so the two are separable in logs and observers —
// goal_loop.go's deferred-dispatch caller passes goalClaimDeferredSourceKind.
func (al *AgentLoop) idleSteerDeliverer(sessionID, goalID, sourceKind string) func(steer string) {
	return func(steer string) {
		al.dispatchGoalAsyncFollowUp(sessionID, goalID, sourceKind, steer)
	}
}

// dispatchGoalAsyncFollowUp re-injects content as a NEW turn on sessionID's
// recorded goal routing, stamped as the goal-loop's own sender
// (goalLoopFollowUpSenderID) so checkGoalLoopAfterTurn's origin gate accepts
// it (D6b). The single shared dispatch primitive behind the idle-steer /
// deferred-claim-steer re-inject (idleSteerDeliverer, above), the D6c
// recordless-goal nudge, and FR-097's unified continue-push ladder — all
// three are "there is no turnResult to attach a followUp to, so re-inject
// via the async-notifier instead" with an identical shape. Best-effort
// throughout: an empty content, an unwired notifier, or missing routing
// (WARNed by routeFor itself when genuinely absent on both sides, FR-031)
// are all silent no-ops here — the NEXT idle check (or, for a deferred
// claim steer, nothing further — the adjudication already ran) gets another
// chance, never a hard failure of the caller.
//
// goalID re-keys the idleSettling un-wedge marker (DD-7(b), E13 — see
// goalTriggerState's own doc comment) — the tick-path callers already carry
// it (s.GoalID); the claim-path deferred dispatch (goal_loop.go) threads it
// through from the resolved claim.
//
// review-round-1 (keeper wedge): every TICK-PATH caller of this function is
// reached only after markGoalIdleFired has already set the idleSettling
// marker (maybeSettleGoalIdle sets it BEFORE dispatching). That marker is
// cleared ONLY by a genuine turn running checkGoalLoopAfterTurn's
// bumpGoalActivityOnTurn (D6b's origin gate). A successful Notify() below
// hands off to exactly such a turn — the marker legitimately stays set,
// awaiting that turn's own activity bump. Every exit that does NOT achieve
// that hand-off (no content/no notifier, missing route, Notify error) must
// clear the marker itself, or the keeper wedges at the goalIsIdleSettling
// gate forever, indistinguishable from a genuinely quiet goal. A deferred
// claim-path steer (goalID's marker was never set by markGoalIdleFired for
// this call) clears a marker that was already absent — a harmless no-op.
func (al *AgentLoop) dispatchGoalAsyncFollowUp(sessionID, goalID, sourceKind, content string) {
	if content == "" || al.asyncNotifier == nil {
		al.goalMarkIdleSettling(goalID, false)
		return
	}
	route := goalTriggers().routeFor(sessionID)
	if route.channel == "" || route.chatID == "" {
		// routeFor itself already WARNed + persisted latest_reason when the
		// route is missing on BOTH sides (FR-031); nothing further to log
		// here beyond what it already did. No turn will ever be dispatched
		// for this evaluation — un-wedge the keeper.
		al.goalMarkIdleSettling(goalID, false)
		return
	}
	notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := al.asyncNotifier.Notify(notifyCtx, AsyncNotifyEvent{
		Channel:             route.channel,
		ChatID:              route.chatID,
		AgentID:             route.agentID,
		TranscriptSessionID: sessionID,
		SourceKind:          sourceKind,
		SenderCanonicalID:   goalLoopFollowUpSenderID,
		Content:             content,
	}); err != nil {
		logger.WarnCF("agent", "goal: follow-up turn dispatch failed",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		// The intended turn never dispatched — nothing will clear the
		// marker downstream. Un-wedge the keeper so the next idle cycle
		// gets another chance instead of wedging forever.
		al.goalMarkIdleSettling(goalID, false)
	}
}

// routeFor returns the captured routing for sessionID: the in-memory entry
// when present, else the persisted fallback — rehydrated into the in-memory
// map on a hit so subsequent reads in this process are O(1). Called as a
// bare goalTriggers().routeFor(sessionID) by both this file's own dispatch
// helpers and wave 2a's set_goal channel-echo path.
//
// GOAL-FR-032/FR-033/FR-034 (E12): the persisted fallback reads the goal's
// OWN record, so no session-store lookup is needed for this any more, and
// sessionStoreResolver's own doc comment's "cannot take an *AgentLoop
// receiver" reasoning no longer applies to THIS read path (it is retained
// for the seam's other, still-live use in recordGoalRouting/
// SetGoalRouteSessionStore — outside this function's own concern).
//
// GOAL-FR-052 (the `routing` row): the record is resolved through
// activeGoalForSession — the ACTIVE record BOUND to this session, either
// owner kind — and NOT through GetActiveByOwner(GoalOwnerKindSession, …).
// The owner-keyed lookup only ever worked for a chat goal, whose owner id
// literally IS its session id; a task-owned goal's owner id is the TASK's id
// (GOAL-FR-002), so that lookup could never resolve one and a task goal
// silently lost its route across a restart — leaving the keeper with no
// channel to reach the agent on, which is exactly the state FR-031's
// routing-lost note exists to make visible rather than to normalise.
// GoalRouteSessionKey/GoalRouteAgentID have no persisted copy at all any
// more (FR-032 deletes the session-key field outright; FR-033 folds the
// agent id into the goal's owner reference instead of a second copy) — a
// route rehydrated from the persisted fallback therefore carries only
// channel/chatID; a cold-boot caller that also needs the agent id falls
// back to the session's own ActiveAgentID, the same fallback
// applyGoalMarkerRestate's own anchor call already uses. A route missing on
// BOTH sides (in-memory AND persisted) WARNs and writes a one-line
// LatestReason note onto the goal record via goal.RecordRoutingLost (S5)
// instead of degrading silently (FR-031/FR-035) — RecordRoutingLost's own
// idempotency (never overwrite a fresher reason, never re-write the
// identical note) replaces the switch this function used to run by hand
// against session meta's GoalLatestReason.
func (s *goalTriggerState) routeFor(sessionID string) goalRoute {
	s.mu.Lock()
	route, ok := s.routing[sessionID]
	s.mu.Unlock()
	if ok && route.channel != "" && route.chatID != "" {
		return route
	}
	gstore := resolveGoalRecordStore()
	g := activeGoalForSession(sessionID)
	if g == nil || g.RouteChannel == "" || g.RouteChatID == "" {
		if route.channel == "" && route.chatID == "" {
			logger.WarnCF("agent", "goal trigger: no routing available (neither in-memory nor persisted) — keeper cannot reach the goal's channel",
				map[string]any{"session_id": sessionID})
			if g != nil {
				if _, werr := gstore.Update(g.GoalID, func(cur *goal.Goal) error {
					cur.RecordRoutingLost()
					return nil
				}); werr != nil {
					logger.WarnCF("agent", "goal trigger: could not persist routing-lost reason",
						map[string]any{"session_id": sessionID, "goal_id": g.GoalID, "error": werr.Error()})
				}
			}
		}
		return route
	}
	persisted := goalRoute{channel: g.RouteChannel, chatID: g.RouteChatID}
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
	rec *goal.Goal,
	result *turnResult,
) (bounced bool) {
	streak := al.bumpGoalBareClaimStreak(rec.GoalID)
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
		al.emitGoalStatusFrame(sessionID, rec.GoalID, rec.Prompt, rec.Round, rec.MaxRounds,
			"bare completion claim bounced (no evidence)", goalPillActive)
		return true
	}

	// 2nd+ bare claim: costs an attempt/round (G-4). Clear the streak and
	// advance the round budget WITHOUT a fresh Judge call — there is nothing
	// to judge (no evidence was provided). Bounded by GoalMaxRounds like any
	// other unmet round; on exhaustion the goal fails honestly.
	al.clearGoalBareClaimStreak(rec.GoalID)
	maxRounds := rec.MaxRounds
	if maxRounds < 1 {
		maxRounds = config.DefaultGoalMaxRounds
	}
	newRound := rec.Round + 1
	reason := "repeated bare completion claim with no [goal:evidence] line — treated as an unmet round"
	if newRound >= maxRounds {
		// silent-SF-7 (review): transition FIRST, handover second, result
		// honoured — see the met/round-bound branches in runGoalAdjudication.
		note := fmt.Sprintf("round bound reached (%d/%d) on a bare claim", newRound, maxRounds)
		if _, ok := al.clearGoalStatus(sessionID, store, note); !ok {
			logger.WarnCF("agent", "goal: bare-claim round-bound termination failed; no handover written (the goal is still active)",
				map[string]any{"session_id": sessionID, "goal_id": rec.GoalID, "round": newRound, "max_rounds": maxRounds})
			return true
		}
		handover := fmt.Sprintf(
			"Goal %q did not reach a MET verdict within %d round(s) (round bound reached on a bare claim).",
			rec.Prompt, maxRounds,
		)
		al.writeGoalSystemTranscript(store, sessionID, agentInst.ID, handover)
		return true
	}
	// ADR-086: the round counter and the steering reason live on the goal
	// record (Round / LatestReason), not the retired session-meta fields.
	if _, perr := resolveGoalRecordStore().Update(rec.GoalID, func(cur *goal.Goal) error {
		cur.Round = newRound
		cur.LatestReason = reason
		cur.LastActivityAt = time.Now().UTC()
		return nil
	}); perr != nil {
		// bare-claim M3 sibling (gate honesty): the SAME persist-and-WARN
		// defect Fix-1's silent-M3 fixed in runGoalAdjudication. Do NOT
		// silently continue on an un-persisted round counter — the exhaustion
		// gate (newRound >= maxRounds above) re-reads the record's Round on
		// the next bare claim; on a write failure the store stays at
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
			rec.Prompt, perr))
		al.emitGoalStatusFrame(sessionID, rec.GoalID, rec.Prompt, rec.Round, maxRounds,
			"round-advance persist failed (goal paused)", goalPillActive)
		return true
	}
	al.emitGoalStatusFrame(sessionID, rec.GoalID, rec.Prompt, newRound, maxRounds, reason, goalPillActive)
	if result != nil {
		result.followUps = append(result.followUps, bus.InboundMessage{
			Channel: opts.Channel, ChatID: opts.ChatID,
			Sender: bus.SenderInfo{CanonicalID: goalLoopFollowUpSenderID},
			Content: fmt.Sprintf(
				"Continue working toward the goal: %s\n\n"+
					"A second bare completion claim cost a round (%d/%d). Provide [goal:evidence] before GOAL_STATUS: met, or keep working.\n",
				rec.Prompt, newRound, maxRounds),
			SessionID: sessionID, SessionKey: opts.SessionKey,
		})
	}
	return true
}

// bumpGoalActivityOnTurn records that a genuine (non-waiting) turn just ran
// for goalID — the activity that re-arms the idle quiet window (FR-102) AND
// clears an "idleSettling, awaiting new activity" marker (G-2). Best-effort
// persistence: a write failure only delays idle settlement, never blocks the
// turn. Returns the timestamp it stamped.
//
// ADR-086 (GOAL-FR-004): the clock it bumps is the goal record's own
// LastActivityAt, so the session store and session id it used to take are
// gone from the parameter list, and the return is a real time.Time rather
// than an RFC3339 string.
func (al *AgentLoop) bumpGoalActivityOnTurn(goalID string) time.Time {
	now := time.Now().UTC()
	bumpGoalRecordActivity(goalID, now)
	al.goalMarkIdleSettling(goalID, false)
	return now
}
