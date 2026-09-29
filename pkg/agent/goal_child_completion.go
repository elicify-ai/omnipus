// goal_child_completion.go closes GitHub #947 defect 1 ("the hang"): a
// goal-bearing delegated (steered) child whose turn had ended stayed at
// lifecycle state `running` for ever — the parent never re-entered, the goal
// record stayed active for the keeper to iterate forever.
//
// The design (the approved design note, decisions (a)-(e); founder FD1=A, FD2=A):
// the goal path keeps DECIDING; the ADR-091 completion path owns FINISHING.
// One completion tail — Deliver, then terminalise — serves every exit
// (goal met, rounds exhausted, judge unavailable, session cancelled, boot
// recovery), and every terminal session write ends the session-owned goal
// with it ("the pair ends together"). The judge-unavailable arm of the
// deferred adjudication is re-driven a bounded number of times inside the
// already-off-critical-path goroutine, then the child is failed visibly.
//
// This file deliberately does NOT add a wall-clock reaper (FD2=A): completion
// is event-driven, and the boot sweep repairs crash residuals.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
)

// goalJudgeRedriveAttempts bounds the re-drive of a judge-unavailable
// deferred adjudication (decision (c)): three attempts, the first plus two
// re-drives, after which the child is failed visibly through the completion
// tail. The goroutine is already off the turn's critical path; this bound is
// what keeps it from being unbounded in TIME.
const goalJudgeRedriveAttempts = 3

const goalCompletionReevaluationPrompt = "All delegated work has finished; review the handbacks and claim completion again if appropriate."

type goalCompletionPhase uint8

const (
	goalCompletionNone goalCompletionPhase = iota
	goalCompletionWaitingDescendants
	goalCompletionAdjudicating
	goalCompletionReevaluationDispatched
)

func (phase goalCompletionPhase) String() string {
	switch phase {
	case goalCompletionNone:
		return "none"
	case goalCompletionWaitingDescendants:
		return "waiting_descendants"
	case goalCompletionAdjudicating:
		return "adjudicating"
	case goalCompletionReevaluationDispatched:
		return "reevaluation_dispatched"
	default:
		return fmt.Sprintf("unknown(%d)", phase)
	}
}

// goalTransitionCompletionPhase is the ONE primitive every completion-phase
// write goes through (type-design-analyzer finding 1: install-and-promote must
// be one critical section; finding 2: no scattered lock/check/write sites).
// It performs a compare-and-set under s.mu: the phase is set to `to` only when
// the current phase equals one of `from`. Returns true when the transition
// landed. The case `to == goalCompletionNone` deletes the entry on success.
//
// Why one primitive: a separate "set" + "promote" left two races open — a
// second claim could downgrade an Adjudicating phase to Waiting (type 1), and
// the bookkeeping for "current value seen under the lock" was open-coded at
// five sites. Routing every writer through transition() collapses both into
// one critical section.
func (al *AgentLoop) goalTransitionCompletionPhase(goalID string, from []goalCompletionPhase, to goalCompletionPhase) bool {
	if goalID == "" {
		return false
	}
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !completionPhaseMatchesAny(s.completionPhase[goalID], from) {
		return false
	}
	if to == goalCompletionNone {
		delete(s.completionPhase, goalID)
		return true
	}
	s.completionPhase[goalID] = to
	return true
}

// completionPhaseMatchesAny reports whether current is one of allowed. An
// empty allowed list never matches. goalCompletionNone matches a missing map
// entry (the canonical "no phase" representation).
func completionPhaseMatchesAny(current goalCompletionPhase, allowed []goalCompletionPhase) bool {
	for _, p := range allowed {
		if current == p {
			return true
		}
	}
	return false
}

// goalInstallWaitingCompletion atomically installs the waiting_descendants
// phase from None, WaitingDescendants, or ReevaluationDispatched. Returns
// true on success. The second-claim test (type 1) needs the explicit
// refusal ONLY when the phase is Adjudicating: a Judge call is already in
// flight under that phase, and a second claim must NEVER downgrade it. The
// ReevaluationDispatched case is intentionally allowed — a fresh met claim
// arriving AFTER a reevaluation turn has been dispatched (e.g. the
// concurrent-descendants terminal-race path, where the second post-handback
// claim lands after the first reeval was already scheduled) needs to
// overwrite the dispatched marker so the new claim reaches the Judge under
// the install-and-promote contract. Idempotent on Waiting. The transition
// goes through the same primitive every other write does
// (goalTransitionCompletionPhase).
func (al *AgentLoop) goalInstallWaitingCompletion(goalID string) bool {
	return al.goalTransitionCompletionPhase(goalID,
		[]goalCompletionPhase{
			goalCompletionNone,
			goalCompletionWaitingDescendants,
			goalCompletionReevaluationDispatched,
		},
		goalCompletionWaitingDescendants)
}

// goalPromoteCompletionToAdjudicating installs the adjudicating phase from
// waiting_descendants only. Returns true on success.
func (al *AgentLoop) goalPromoteCompletionToAdjudicating(goalID string) bool {
	return al.goalTransitionCompletionPhase(goalID,
		[]goalCompletionPhase{goalCompletionWaitingDescendants},
		goalCompletionAdjudicating)
}

// goalClearCompletionPhase clears any known phase through the single
// transition primitive, including an absent entry (None).
func (al *AgentLoop) goalClearCompletionPhase(goalID string) {
	al.goalTransitionCompletionPhase(goalID,
		[]goalCompletionPhase{
			goalCompletionNone,
			goalCompletionWaitingDescendants,
			goalCompletionAdjudicating,
			goalCompletionReevaluationDispatched,
		},
		goalCompletionNone)
}

func (al *AgentLoop) goalCompletionWaiting(goalID string) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completionPhase[goalID] == goalCompletionWaitingDescendants
}

// goalCompletionFenceActive reports whether sessionID's completion fence
// refuses new launches (security-lead finding 2 / silent-failure-hunter
// finding 2). Returns the fence state and an error: an unreadable goal store
// is fail-closed — the caller MUST refuse the launch, not let it through.
// nil goal record on a readable store (the
// activeGoalForSessionWithError "no active goal" sentinel) means the session
// has no active goal and the fence is inactive.
func (al *AgentLoop) goalCompletionFenceActive(sessionID string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	rec, err := activeGoalForSessionWithError(sessionID)
	if err != nil {
		if errors.Is(err, errNoActiveGoalForSession) {
			return false, nil
		}
		return false, fmt.Errorf("goal completion fence: read active goal: %w", err)
	}
	if rec == nil {
		return false, nil
	}
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	phase := s.completionPhase[rec.GoalID]
	return phase == goalCompletionWaitingDescendants || phase == goalCompletionAdjudicating, nil
}

func (al *AgentLoop) goalSetSteeredCompletionWrite(sessionID string, active bool) {
	if sessionID == "" {
		return
	}
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	if active {
		s.steeredCompletionWrites[sessionID] = true
		return
	}
	delete(s.steeredCompletionWrites, sessionID)
}

func (al *AgentLoop) goalSteeredCompletionWriteActive(sessionID string) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.steeredCompletionWrites[sessionID]
}

// resumeDeferredGoalAfterDescendantTerminal is the post-terminal half of Q2
// B. It resolves the descendant's steering parent and schedules at most one
// deterministic turn once that parent's whole subtree is quiet. The stale
// met claim is never adjudicated; the new turn must make a fresh claim.
func (al *AgentLoop) resumeDeferredGoalAfterDescendantTerminal(descendantSessionID string) {
	if al == nil || descendantSessionID == "" {
		return
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	descendant, err := lifecycle.Load(descendantSessionID)
	if err != nil || descendant == nil || !descendant.Terminal() || descendant.SteeredBy == nil {
		return
	}
	parentID := descendant.SteeredBy.SteeringSessionID
	parent, parentErr := lifecycle.Load(parentID)
	if parentErr != nil || parent == nil || parent.Terminal() || parent.Stopped() {
		return
	}
	rec := activeGoalForSession(parentID)
	if rec == nil {
		return
	}
	// The phase is process-local. After a restart, reconstruct only the safe
	// pending case from durable evidence: a met claim newer than the latest
	// verdict (or with no verdict yet). Re-evaluation still requires a fresh
	// claim, so an uncertain timestamp can cause extra work but never a false
	// met transition.
	al.goalRestoreWaitingCompletion(rec)
	al.resumeDeferredGoalForSession(parentID, rec.GoalID)
}

// ResumeDeferredGoalAfterDescendantTerminal is the boot-recovery wiring seam
// for the same post-terminal check used by the live completion paths.
func (al *AgentLoop) ResumeDeferredGoalAfterDescendantTerminal(descendantSessionID string) {
	al.resumeDeferredGoalAfterDescendantTerminal(descendantSessionID)
}

func (al *AgentLoop) resumeDeferredGoalForSession(sessionID, goalID string) {
	if al == nil || sessionID == "" || goalID == "" {
		return
	}
	if !al.goalCompletionWaiting(goalID) {
		return
	}

	blocked, err := al.hasRunningOrQueuedDescendant(sessionID)
	if err != nil {
		logger.WarnCF("agent", "goal: deferred completion remains pending because descendant state could not be read",
			map[string]any{"session_id": sessionID, "goal_id": goalID, "error": err.Error()})
		return
	}
	if blocked {
		return
	}

	if !al.goalTransitionCompletionPhase(goalID,
		[]goalCompletionPhase{goalCompletionWaitingDescendants}, goalCompletionReevaluationDispatched) {
		return
	}

	if al.dispatchGoalCompletionReevaluation(sessionID, goalID) {
		return
	}
	// Notification did not hand off a turn. Restore waiting so a later
	// terminal hook or the active-goal keeper can retry.
	al.goalTransitionCompletionPhase(goalID,
		[]goalCompletionPhase{goalCompletionReevaluationDispatched}, goalCompletionWaitingDescendants)
}

// goalClearCompletionPhaseFromRestate invalidates the completion phase on
// every successful goal restate / supersession (security-lead finding 1,
// first half). Without this, the old completion fence stays in control of
// the new goal revision: the revised goal cannot launch needed descendants
// until the pre-restate descendants terminate, and a verdict discarded
// because the goal was restated while the Judge was running leaves
// Adjudicating wedged forever.
//
// The phase is process-local and re-established by goalRestoreWaitingCompletion
// from durable evidence (a fresh met claim with no subsequent verdict), so
// clearing it here is safe: a subtree that genuinely is still quiet gets
// rebuilt by the next keeper tick, and a subtree that was busy in the
// pre-restate sense has its own turn to settle.
func (al *AgentLoop) goalClearCompletionPhaseFromRestate(goalID string) {
	if goalID == "" {
		return
	}
	al.goalTransitionCompletionPhase(goalID,
		[]goalCompletionPhase{
			goalCompletionNone,
			goalCompletionWaitingDescendants,
			goalCompletionAdjudicating,
			goalCompletionReevaluationDispatched,
		},
		goalCompletionNone)
}

func (al *AgentLoop) goalRestoreWaitingCompletion(rec *goal.Goal) {
	if rec == nil || rec.LatestClaim == nil || rec.LatestClaim.Status != generated.GoalLatestClaimStatusMet {
		return
	}
	if rec.LatestVerdict != nil {
		judgedAt, err := time.Parse(time.RFC3339Nano, rec.LatestVerdict.JudgedAt)
		if err == nil && !rec.LatestClaim.ClaimedAt.After(judgedAt) {
			return
		}
	}
	al.goalTransitionCompletionPhase(rec.GoalID,
		[]goalCompletionPhase{goalCompletionNone}, goalCompletionWaitingDescendants)
}

// deferMetWhileDescendantsActive is the final Q2 B quietness fence. It runs
// after the Judge returns but before any verdict is persisted or published.
// A live descendant or an unreadable subtree discards the stale met result
// without consuming a round and returns the goal to pending re-evaluation.
func (ag *agentLoopRunGoalAdjudication) deferMetWhileDescendantsActive() bool {
	if ag == nil || ag.verdict == nil || !ag.verdict.Met {
		return false
	}
	blocked, err := ag.al.hasRunningOrQueuedDescendant(ag.sessionID)
	if err == nil && !blocked {
		return false
	}
	ag.al.goalTransitionCompletionPhase(ag.rec.GoalID,
		[]goalCompletionPhase{goalCompletionAdjudicating}, goalCompletionWaitingDescendants)
	if err != nil {
		logger.WarnCF("agent", "goal: met verdict discarded because descendant state could not be read",
			map[string]any{"session_id": ag.sessionID, "goal_id": ag.rec.GoalID, "error": err.Error()})
		return true
	}
	logger.InfoCF("agent", "goal: met verdict discarded because delegated work is still active",
		map[string]any{"session_id": ag.sessionID, "goal_id": ag.rec.GoalID})
	ag.al.resumeDeferredGoalForSession(ag.sessionID, ag.rec.GoalID)
	return true
}

func (al *AgentLoop) dispatchGoalCompletionReevaluation(sessionID, goalID string) bool {
	if al.asyncNotifier == nil {
		return false
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil || rec == nil || rec.Terminal() || rec.Stopped() {
		return false
	}
	if rec.SteeredBy == nil {
		return al.dispatchGoalAsyncFollowUp(sessionID, goalID, goalCompletionSourceKind, goalCompletionReevaluationPrompt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = al.asyncNotifier.Notify(ctx, AsyncNotifyEvent{
		Channel:             steerReportingSelfChannel,
		ChatID:              sessionID,
		AgentID:             rec.AgentID,
		TranscriptSessionID: sessionID,
		SourceKind:          goalCompletionSourceKind,
		SenderCanonicalID:   goalLoopFollowUpSenderID,
		Content:             goalCompletionReevaluationPrompt,
		Metadata: map[string]any{
			"steer_message_id": uuid.NewString(),
			"steer_generation": rec.Generation,
		},
	})
	if err != nil {
		logger.WarnCF("agent", "goal: completion re-evaluation turn dispatch failed",
			map[string]any{"session_id": sessionID, "goal_id": goalID, "error": err.Error()})
		return false
	}
	return true
}

// goalJudgeRedriveBackoff is the short backoff between re-drive attempts
// (design note (c): "short backoff (e.g. 5s/15s/30s)"). It is deliberately
// NOT the verifier's own judgeRetryBackoff table (60s/120s/300s): that one
// spaces the retries INSIDE one judge round; this one spaces whole rounds.
// Waits run through judgeSleepFn — the same seam the verifier's backoff uses
// — so a test can collapse both the in-round retries and these waits exactly
// as it collapses the retries alone.
var goalJudgeRedriveBackoff = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second} //nolint:gochecknoglobals

// goalSessionEndedNotePrefix marks endActiveGoal's note-switch with the
// FD1=A pair-end: a session-owned goal ended because its SESSION ended, not
// because the user cleared it or the rounds ran out. endActiveGoal maps this
// prefix to the cleared pill/state; the note (stored as the goal's
// TerminalReason) names the session-level cause.
const goalSessionEndedNotePrefix = "session ended: "

// redriveGoalAdjudication is decision (c)'s bounded re-drive: it wraps the
// single shared adjudication body (runGoalAdjudication) so a judge-unavailable
// round no longer strands the pair. Each attempt is bounded by
// goalJudgeRoundTimeout exactly as the pre-fix dispatch was; between attempts
// the wrapper waits through judgeSleepFn, treating a failed wait as "retry
// now" (a cancelled wait only means the process is going away — the boot
// sweep covers the residual).
//
// On exhaustion the child is failed through the SAME completion tail the met
// arm uses (completeSteeredTurnAfterGoal): terminal `failed` with a reason
// naming the judge, a wake-eligible error to the parent, and — via the
// terminal write's own pair-end — the goal record ended. The claim needs no
// re-capture: it is persisted on the goal record, and every attempt re-reads
// the record fresh, so a goal cleared or restated mid-re-drive is simply not
// adjudicated further.
//
// A registry concurrency back-off (#984) is NOT re-driven: runGoalAdjudication
// resolves JudgeCriteriaResult.ConcurrencyBackoff to (false, false) — a
// final, silent stop for THIS wrapper, with no retry (the in-flight
// adjudication resolves the goal) and no child failure. Only a genuine
// judge outage (unavailable=true, reason != the concurrency sentinel) is
// retried and, on exhaustion, fails the child visibly.
//
// type-design finding 3: a missing session store at re-drive time previously
// returned silently, stranding completionPhase at Adjudicating. Now it
// clears the phase and fails the child visibly through the completion tail —
// the same fail-closed treatment the met/exhausted arms use. No silent
// returns from this function.
func (al *AgentLoop) redriveGoalAdjudication(work *goalDeferredAdjudicationWork) {
	if al == nil || work == nil || work.sessionID == "" {
		return
	}
	store := al.GetSessionStore()
	if store == nil {
		// Clear the phase so the goal is not wedged at Adjudicating forever,
		// then fail the child visibly through the completion tail — the same
		// exit the bounded-re-drive exhaustion takes. A goal-bearing steered
		// child with no session store is already broken end-to-end; the user
		// gets one visible error, never a silent "running" session.
		logger.ErrorCF("agent", "goal: deferred adjudication re-drive failed — no session store; clearing phase and failing the child visibly",
			map[string]any{"session_id": work.sessionID})
		rec := activeGoalForSession(work.sessionID)
		if rec != nil {
			al.goalClearCompletionPhase(rec.GoalID)
		}
		const reason = "goal adjudication could not run: no session store is wired to resolve the child's lifecycle record"
		al.completeSteeredTurnAfterGoal(context.Background(), work.sessionID, "", errors.New(reason))
		return
	}
	for attempt := 1; attempt <= goalJudgeRedriveAttempts; attempt++ {
		if attempt > 1 {
			al.waitGoalRedriveBackoff(attempt - 1)
		}
		// The goal may have been cleared, restated or its session cancelled
		// since the last attempt — re-read fresh, exactly as the dispatch
		// does before its own single call today.
		rec := activeGoalForSession(work.sessionID)
		if rec == nil {
			logger.InfoCF("agent", "goal: deferred adjudication re-drive stopped — no active goal on this session any more",
				map[string]any{"session_id": work.sessionID, "attempt": attempt})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), goalJudgeRoundTimeout)
		met, unavailable := al.runGoalAdjudication(ctx, work.agentInst, work.workspaceID, work.sessionID, store, rec, work.claimText,
			al.idleSteerDeliverer(work.sessionID, rec.GoalID, goalClaimDeferredSourceKind))
		cancel()
		if met || !unavailable {
			return
		}
		logger.WarnCF("agent", "goal: judge unavailable on a re-drive attempt — retrying while attempts remain",
			map[string]any{"session_id": work.sessionID, "goal_id": rec.GoalID,
				"attempt": attempt, "max_attempts": goalJudgeRedriveAttempts})
	}
	reason := fmt.Sprintf("judge unavailable after %d attempts: the completion claim could not be adjudicated", goalJudgeRedriveAttempts)
	logger.ErrorCF("agent", "goal: "+reason+" — failing the goal-bearing child visibly",
		map[string]any{"session_id": work.sessionID})
	al.completeSteeredTurnAfterGoal(context.Background(), work.sessionID, "", errors.New(reason))
}

// waitGoalRedriveBackoff spaces two re-drive attempts through judgeSleepFn.
// A failed wait never aborts the re-drive: judgeSleepFn's error means only
// that the wait did not run its course (the substituted test seam, or a
// process shutdown) — the attempts are each timeout-bounded on their own, so
// retrying immediately is the honest interpretation.
func (al *AgentLoop) waitGoalRedriveBackoff(previousAttempt int) {
	idx := previousAttempt - 1
	if idx > len(goalJudgeRedriveBackoff)-1 {
		idx = len(goalJudgeRedriveBackoff) - 1
	}
	delay := goalJudgeRedriveBackoff[idx]
	waitCtx, cancel := context.WithTimeout(context.Background(), delay)
	defer cancel()
	if err := judgeSleepFn(waitCtx, delay); err != nil {
		logger.InfoCF("agent", "goal: re-drive backoff did not run its course — retrying now",
			map[string]any{"delay": delay.String()})
	}
}

// goalEndingForTerminalState maps a terminal lifecycle state onto the goal
// outcome's ending vocabulary (GoalOutcome.ending): a human Stop
// (cancelled) reads as stopped_by_user; every other session death is `other`.
func goalEndingForTerminalState(state session.LifecycleState) generated.GoalOutcomeEnding {
	if state == session.LifecycleCancelled {
		return generated.GoalOutcomeEndingStoppedByUser
	}
	return generated.GoalOutcomeEndingOther
}

// goalSessionEndedReasonForState is the session-level "why" recorded as the
// goal's TerminalReason when the pair ends together (FD1=A: the outcome
// records why).
func goalSessionEndedReasonForState(state session.LifecycleState) string {
	switch state {
	case session.LifecycleCancelled:
		return "the session was cancelled"
	case session.LifecycleTimedOut:
		return "the session timed out"
	case session.LifecycleFailed:
		return "the session failed"
	default:
		return "the session ended"
	}
}

// endSessionOwnedGoalOnTerminal is decision (e)'s pair-end helper: when a
// steered child's record lands terminal, its ACTIVE session-owned goal ends
// with the session (founder decision FD1=A — "a session-owned goal ends with
// its session, and the outcome records why"). Idempotent by construction: no
// active goal, no action; and the state the note-switch below maps to is
// `cleared`, one of the goal contract's terminal states.
//
// Task-owned goals are out of scope ON PURPOSE: their single terminal writer
// is tools.TerminateTaskGoalRecord (goal_outcome.go's header), and this
// helper would otherwise become a second writer racing it. The session-level
// cause travels as the note (prefix + reason) — endActiveGoal stores the note
// as the record's TerminalReason — and the outcome line lands in the session
// transcript like any other goal ending.
func (al *AgentLoop) endSessionOwnedGoalOnTerminal(sessionID string, ending generated.GoalOutcomeEnding, reason string) {
	if al == nil || sessionID == "" {
		return
	}
	rec := activeGoalForSession(sessionID)
	if rec == nil {
		// No active goal — the common case (most steered children have no
		// goal, and an already-ended goal must not be re-ended). Idempotent.
		return
	}
	if rec.OwnerKind != generated.GoalOwnerKindSession {
		// A task-owned goal has its own single terminal writer; a session
		// record ending never speaks for it.
		return
	}
	in := goalOutcomeInput{
		ending:     ending,
		roundsUsed: rec.Round,
		maxRounds:  rec.MaxRounds,
		content: fmt.Sprintf("Goal %q ended with its session before reaching a MET verdict: %s.",
			rec.Prompt, reason),
	}
	if _, ok := al.clearGoalWithOutcome(sessionID, al.GetSessionStore(), goalSessionEndedNotePrefix+reason, in); !ok {
		logger.WarnCF("agent", "goal: pair-end deferred — the goal-record transition was refused; a later terminal path re-drives it",
			map[string]any{"session_id": sessionID, "goal_id": rec.GoalID})
	}
}

// EndSessionOwnedGoalOnTerminal is the exported boot-wiring seam for the
// (e)three pair-end: the gateway binds this method into SteerBootRecovery's
// EndSessionGoal hook, where no ending parameter is knowable (the sweep
// terminalises mid-flight sessions as interrupted) and the package boundary
// requires an exported symbol. The ending is `other`; the reason names the
// interruption.
func (al *AgentLoop) EndSessionOwnedGoalOnTerminal(sessionID, reason string) {
	al.endSessionOwnedGoalOnTerminal(sessionID, generated.GoalOutcomeEndingOther, reason)
}

// goalParkUpwardText picks the parent-facing text for a park delivery
// (decision (d)): the goal_claim tool's evidence/reason argument when the
// park came through the tool, otherwise the turn's own final content with the
// GOAL_STATUS control line stripped — the marker is control prose, not the
// worker's question to its parent.
func goalParkUpwardText(evidence, finalContent string) string {
	if strings.TrimSpace(evidence) != "" {
		return strings.TrimSpace(evidence)
	}
	lines := strings.Split(finalContent, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), goalStatusLabel+":") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// deliverGoalParkUpward is decision (d): a goal-bearing steered child that
// parks — GOAL_STATUS: waiting_on_user, or a goal_claim tool `blocked` —
// tells its parent through the ONE upward path (Deliver), so a park is a
// question the parent can answer, not silence. Both outcomes are already
// wake-eligible (steer_audience.go::validateOutcomeMessage); the keeper's
// suppression while a park holds is untouched (it is correct — nothing should
// push while a question is out).
//
// An interactive goal chat (no steering parent) is skipped silently: the user
// is already the audience of that transcript, and Deliver has no one else to
// tell.
func (al *AgentLoop) deliverGoalParkUpward(sessionID string, blocked bool, text string) {
	if al == nil || sessionID == "" {
		return
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil || rec == nil {
		return
	}
	if rec.SteeredBy == nil {
		return
	}
	deliverer := al.getUpwardDeliverer()
	if deliverer == nil {
		logger.WarnCF("agent", "goal: park delivery skipped — no upward deliverer is wired",
			map[string]any{"session_id": sessionID})
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = "the worker needs input before it can continue"
	}
	now := time.Now().UTC()
	parent := rec.SteeredBy.SteeringSessionID
	authority := generated.SessionMessageQuestionAuthority("owner_required")
	var (
		message generated.SessionMessage
		outcome steer.Outcome
		berr    error
	)
	if blocked {
		outcome = steer.OutcomeBlocker
		berr = message.FromSessionMessageBlocker(generated.SessionMessageBlocker{
			MessageId:       uuid.NewString(),
			SessionId:       rec.SessionID,
			ParentSessionId: &parent,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			Text:            text,
			Severity:        generated.SessionMessageBlockerSeverityMedium,
		})
	} else {
		outcome = steer.OutcomeParkedQuestion
		berr = message.FromSessionMessageQuestion(generated.SessionMessageQuestion{
			MessageId:       uuid.NewString(),
			SessionId:       rec.SessionID,
			ParentSessionId: &parent,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			Text:            text,
			Wait:            true,
			CorrelationId:   uuid.NewString(),
			Authority:       &authority,
		})
	}
	if berr != nil {
		logger.WarnCF("agent", "goal: park delivery failed — the message could not be encoded",
			map[string]any{"session_id": sessionID, "error": berr.Error()})
		return
	}
	event := steer.UpwardEvent{ChildSessionID: rec.SessionID, Outcome: outcome, Message: message}
	delivery, derr := deliverer.Deliver(context.Background(), event)
	if derr != nil {
		logger.WarnCF("agent", "goal: park delivery failed — the parent was not told",
			map[string]any{"session_id": sessionID, "error": derr.Error()})
		return
	}
	reportUndeliveredWake("goal: park delivery", event, steerParentSessionID(rec), rec.Generation, delivery)
}

// ackMetVerdictEntry acknowledges the session-goal met verdict's inbox entry
// after the hand-back produces the parent wake (founder Q1=A, #984 follow-up).
// The met verdict is delivered with its wake suppressed
// (deliverGoalVerdictUpward's SuppressWake); the completion handback that
// follows it is the one parent wake for the whole "met". An entry delivered
// without a wake is consumed by nothing — leaving it unacked would make boot recovery
// (boot_sweep.go::unacknowledged) re-deliver AND re-wake it on every later
// restart, which is exactly the re-entry Q1=A removes.
//
// Best-effort by design: the tail (completeSteeredTurnAfterGoal) runs FIRST
// so a crash before the hand-back wake leaves both entries unacked for boot
// recovery to re-deliver. Once the hand-back has woken, acknowledging the
// wake-suppressed verdict is crash-safe because the deterministic hand-back
// is itself durable and remains unacknowledged until the parent's normal
// consumption marker retires it; a crash before consumption therefore makes
// boot recovery re-deliver that hand-back as the one durable wake carrier.
// This ack runs AFTER the tail returns, so it never races the tail's Deliver.
// No parent edge → nothing to ack (a task-owned goal has no steered edge and
// its verdict wake is load-bearing).
func (al *AgentLoop) ackMetVerdictEntry(sessionID, goalID string, round int) {
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		logger.WarnCF("agent", "goal: met verdict ack skipped — could not load the child's lifecycle record",
			map[string]any{"session_id": sessionID, "goal_id": goalID, "error": err.Error()})
		return
	}
	if rec == nil || rec.SteeredBy == nil {
		return
	}
	ownerKey := deliverOwnerKey(rec)
	if ownerKey == "" {
		return
	}
	id := goalVerdictUpwardMessageID(goalID, round)
	if aerr := inbox.Ack(ownerKey, []string{id}); aerr != nil {
		logger.WarnCF("agent", "goal: met verdict ack failed — boot recovery may re-wake this verdict once",
			map[string]any{"session_id": sessionID, "goal_id": goalID, "message_id": id, "error": aerr.Error()})
	}
}

// wakeMetVerdictEntry re-delivers the already-durable, unacknowledged met
// verdict without wake suppression when no deterministic final hand-back
// woke the parent. Deliver's duplicate handling deliberately re-runs the wake
// for an unacknowledged wake-eligible entry, so this creates no second inbox
// row. Publishing or queueing is not consumption: every outcome leaves the
// verdict unacknowledged until processSteeredSystemWake or
// consumeDequeuedSteering writes the parent's normal consumed marker. That
// preserves boot recovery's ability to re-deliver after a crash between wake
// publication and actual consumption. A missing or stopped parent produces
// DeliveryStoredNotWoken and returns an error under the same durable posture.
func (al *AgentLoop) wakeMetVerdictEntry(sessionID, goalID string, round int) error {
	inbox := al.GetMessageInboxStore()
	lifecycle := al.GetSessionLifecycleStore()
	deliverer := al.getUpwardDeliverer()
	if inbox == nil || lifecycle == nil || deliverer == nil {
		return errors.New("goal: met verdict fallback wake dependencies are not wired")
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return fmt.Errorf("load child lifecycle: %w", err)
	}
	if rec == nil || rec.SteeredBy == nil {
		return errors.New("goal: met verdict fallback wake has no steering edge")
	}
	wantID := goalVerdictUpwardMessageID(goalID, round)
	message, found, err := findUnackedInboxMessage(inbox, deliverOwnerKey(rec), sessionID, wantID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("goal: met verdict %q is not present and unacknowledged", wantID)
	}
	event := steer.UpwardEvent{ChildSessionID: sessionID, Outcome: steer.OutcomeGoalVerdict, Message: message}
	delivery, err := deliverer.Deliver(context.Background(), event)
	if err != nil {
		return fmt.Errorf("wake stored met verdict: %w", err)
	}
	reportUndeliveredWake("goal: met verdict fallback wake", event, steerParentSessionID(rec), rec.Generation, delivery)
	if !deliveryWokeRecipient(delivery.Outcome) {
		return fmt.Errorf("wake stored met verdict: delivery outcome %q did not wake the parent", delivery.Outcome)
	}
	return nil
}

func findUnackedInboxMessage(inbox *session.MessageInboxStore, ownerKey, childSessionID, messageID string) (generated.SessionMessage, bool, error) {
	cursor := ""
	for {
		messages, next, more, err := inbox.Drain(ownerKey, childSessionID, cursor, session.DefaultInboxUnackedMax)
		if err != nil {
			return generated.SessionMessage{}, false, fmt.Errorf("drain parent inbox: %w", err)
		}
		for _, message := range messages {
			if messageIDOf(message) == messageID {
				return message, true, nil
			}
		}
		if !more {
			return generated.SessionMessage{}, false, nil
		}
		cursor = next
	}
}
