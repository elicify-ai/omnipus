package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
)

// completeSteeredTurn applies the goal-less completion disposition after a
// real turn exits. Delivery is deliberately first; boot recovery can repair a
// delivered-but-not-terminal record, while terminal-first could lose the only
// copy of the child's result.
func (al *AgentLoop) completeSteeredTurn(ctx context.Context, snapshot *session.LifecycleRecord, result turnResult, runErr error) error {
	_, err := al.completeSteeredTurnDurably(ctx, snapshot, result, runErr)
	return err
}

// completeSteeredTurnDurably is completeSteeredTurn's result-bearing form.
// finalWoke is true only after the generation's deterministic final inbox
// entry is durable and its delivery produced a parent wake (or queued the
// entry into an already-live parent turn). Goal completion uses that fact to
// decide whether a wake-suppressed met verdict may be acknowledged.
func (al *AgentLoop) completeSteeredTurnDurably(ctx context.Context, snapshot *session.LifecycleRecord, result turnResult, runErr error) (finalWoke bool, err error) {
	if al == nil || snapshot == nil || snapshot.SteeredBy == nil {
		return false, nil
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, errors.New("steer: complete: lifecycle store is not wired")
	}
	rec, err := lifecycle.Load(snapshot.SessionID)
	if err != nil {
		return false, fmt.Errorf("steer: complete: load %q: %w", snapshot.SessionID, err)
	}
	if rec.Generation != snapshot.Generation || rec.State == session.LifecycleNeedsInput {
		return false, nil
	}
	if rec.Terminal() {
		return false, nil
	}
	al.setSteeredCompletionWrite(rec.SessionID, true)
	defer al.setSteeredCompletionWrite(rec.SessionID, false)

	answer := strings.TrimSpace(result.finalContent)
	outcome, nextState, failureReason := completionDisposition(result, runErr, answer)
	// A goal-bearing session's SUCCESS is decided only by the claim/Judge
	// loop (finishSteeredGoalTurn). Its DEATH is not: a turn that failed, ran
	// out of time or was stopped has no claim for the Judge to adjudicate, so
	// I-5's outcome table applies to it like any other steered session. The
	// blanket refusal that used to sit here left such a child `running` for
	// ever — nothing reached the parent, and hasRunningOrQueuedDescendant
	// kept the parent from completing either.
	// (a) #947 defect 1: the deferral gate keys on an ACTIVE goal record, not
	// the historical GoalRef stamp. A child whose goal decision is already
	// settled (met, exhausted, idle-expired — or a judge-unavailable re-drive
	// that just failed it) completes through the tail below; a child with a
	// LIVE deferred adjudication still defers here. The old stamp-based gate
	// kept deferring after the goal had ended: the verdict was delivered, the
	// goal record closed, and the child stayed `running` for ever — the exact
	// #947 hang.
	if activeGoalForSession(rec.SessionID) != nil && !session.IsTerminalLifecycleState(nextState) {
		return false, nil
	}

	if outcome == "" {
		if answer == "" {
			outcome = steer.OutcomeEmptyAnswer
			nextState = session.LifecycleFailed
			failureReason = "empty_answer: the session produced no final answer"
		} else {
			blocked, blockErr := al.hasRunningOrQueuedDescendant(rec.SessionID)
			if blockErr != nil {
				return false, blockErr
			}
			if blocked {
				// The answer is already durable in the child's transcript. Keep the
				// lifecycle running until the last executing descendant completes.
				return false, nil
			}
			outcome = steer.OutcomeFinalAnswer
			nextState = session.LifecycleCompleted
		}
	}

	if completeBeforeDeliveryTestHook != nil {
		completeBeforeDeliveryTestHook(rec.SessionID)
	}
	return al.runSteeredCompletionOnce(rec, func() (bool, error) {
		return al.deliverSteeredCompletion(ctx, rec, outcome, nextState, answer, failureReason)
	})
}

func (al *AgentLoop) deliverSteeredCompletion(ctx context.Context, rec *session.LifecycleRecord, outcome steer.Outcome, nextState session.LifecycleState, answer, failureReason string) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, errors.New("steer: complete: lifecycle store is not wired")
	}
	finalAlreadyStored, err := al.completionFinalAlreadyStored(rec, outcome)
	if err != nil {
		return false, err
	}
	// Snapshot the Stop marker BEFORE Deliver's I/O window so the Mutate
	// below can tell "a Stop raced my write" from "the Stop that caused
	// my write". See the closure for why the distinction matters.
	stopBeforeDelivery := rec.Stop

	finalWoke := false
	if !finalAlreadyStored {
		message, messageErr := al.completionMessage(rec, outcome, answer, failureReason)
		if messageErr != nil {
			return false, messageErr
		}
		deliverer := al.getUpwardDeliverer()
		if deliverer == nil {
			return false, errSteerUpwardDelivererNotWired
		}
		// Finding A (ADR-091 fix lane 1, CRITICAL — the release blocker):
		// this Deliver call is the ONLY upward path left. The former
		// completeWaitingAncestors shortcut is deleted: it substituted the
		// parent's stale answer instead of re-entering it with the child's
		// result. Deliver's wake is the required re-entry.
		event := steer.UpwardEvent{
			ChildSessionID: rec.SessionID,
			Generation:     rec.Generation,
			Outcome:        outcome,
			Message:        message,
		}
		delivery, deliverErr := deliverer.Deliver(ctx, event)
		if deliverErr != nil {
			return false, fmt.Errorf("steer: complete: deliver %q: %w", rec.SessionID, deliverErr)
		}
		reportUndeliveredWake("steer: complete", event, steerParentSessionID(rec), rec.Generation, delivery)
		finalWoke = deliveryWokeRecipient(delivery.Outcome)
	}

	if completeStateWriteTestHook != nil {
		completeStateWriteTestHook(rec.SessionID)
	}

	// Finding D (ADR-091 fix lane 1, HIGH — three reviewers found this
	// independently): this used to be a raw Load (above) -> Deliver (I/O,
	// just above) -> mutate the PRE-Deliver snapshot in memory -> Persist —
	// the exact stale-write-back shape already fixed on the dispatch path
	// (steer_launcher.go::commitSteeredDispatchState), with a WIDER window
	// here because Deliver does real I/O (an inbox append, transcript
	// writes, a parent wake). A Stop pressed while Deliver was still
	// running used to be ERASED by this write: state went straight to
	// completed/failed, Stop became nil, and the durable record that Stop
	// was ever pressed was gone. pkg/session/lifecycle.go's own rule is
	// explicit: a caller doing read-then-decide-then-write MUST use Mutate.
	// This re-checks generation AND the Stop marker inside the SAME lock
	// the write happens under, refusing rather than writing a stale
	// snapshot over either — mirrors commitSteeredDispatchState exactly.
	mutateErr := lifecycle.Mutate(rec.SessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return fmt.Errorf("steer: complete: record %q vanished during delivery", rec.SessionID)
		}
		if cur.Generation != rec.Generation {
			return errCompleteStaleGeneration
		}
		if cur.Terminal() {
			return errCompleteAlreadyTerminal
		}
		// A current-generation Stop marker means one of two DIFFERENT
		// things, and conflating them strands the session forever:
		//
		//   (a) a Stop landed WHILE Deliver was doing I/O -- a genuine
		//       race. The cascade owns finishing this session; refuse
		//       and let it, exactly as before.
		//
		//   (b) the Stop is the REASON this turn is completing. The
		//       cascade cancelled a live turn, the turn unwound with
		//       context.Canceled, and we are now writing the terminal
		//       state that the Stop asked for. Refusing here left the
		//       record `running` with its marker FOREVER, and
		//       hasRunningOrQueuedDescendant kept the parent waiting --
		//       the exact hang ADR-091 exists to remove, on the most
		//       common Stop path (a session that HAD a live turn).
		//
		// stopBeforeDelivery is the pre-Deliver snapshot, so
		// stopLandedDuringDelivery distinguishes them the same way
		// steer_cancel.go::reportSteeredSessionTerminalUpward does.
		// Keeping the two paths symmetric is the point: they are the
		// only two writers that land a terminal state on a stopped
		// session.
		if stopLandedDuringDelivery(stopBeforeDelivery, cur) {
			return errCompleteStoppedDuringDelivery
		}
		cur.State = nextState
		cur.NeedsInput = nil
		// The Stop has now been carried out, so the marker is spent --
		// clear it (founder decision, 2026-09-24), mirroring
		// reportSteeredSessionTerminalUpward. persistLocked REJECTS a
		// terminal record that still carries a current-generation
		// marker, so leaving it would fail the write outright. An OLDER
		// marker is inert history a revival deliberately keeps.
		if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
			cur.Stop = nil
		}
		if nextState == session.LifecycleFailed {
			cur.FailedReason = failureReason
		}
		return nil
	})
	switch {
	case mutateErr == nil:
		// (e)① #947 defect 1 — the pair ends together (FD1=A): the terminal
		// write just landed, so the child's ACTIVE session-owned goal ends
		// with its session, the outcome recording why. Idempotent (no active
		// goal, no action), and it never speaks for a task-owned goal.
		al.endSessionOwnedGoalOnTerminal(rec.SessionID,
			goalEndingForTerminalState(nextState), goalSessionEndedReasonForState(nextState))
		return finalWoke, nil
	case errors.Is(mutateErr, errCompleteStaleGeneration),
		errors.Is(mutateErr, errCompleteStoppedDuringDelivery),
		errors.Is(mutateErr, errCompleteAlreadyTerminal),
		errors.Is(mutateErr, session.ErrLifecycleTerminalImmutable):
		// The message is already durably delivered (Deliver ran above); a
		// Stop, a Revive or a second completion racing this write is a
		// legitimate outcome, not a caller-actionable failure — mirrors the
		// top-of-function guard's own "already terminal" no-op.
		return finalWoke, nil
	default:
		return finalWoke, fmt.Errorf("steer: complete: persist %q: %w", rec.SessionID, mutateErr)
	}
}

// completionFinalAlreadyStored reports whether this generation's terminal
// result exists in the parent's durable inbox AND has been acknowledged.
// Existence alone proves only that Deliver's append ran; it does not prove the
// parent wake was queued. An unacknowledged final must therefore re-enter
// Deliver, whose deterministic id deduplicates storage while retrying the
// wake. The generation-bound id keeps a revived session's newer final
// independent from an older attempt.
func (al *AgentLoop) completionFinalAlreadyStored(rec *session.LifecycleRecord, outcome steer.Outcome) (bool, error) {
	if !isTerminalOutcome(outcome) {
		return false, nil
	}
	inbox := al.GetMessageInboxStore()
	ownerKey := deliverOwnerKey(rec)
	if inbox == nil || ownerKey == "" {
		return false, nil
	}
	entries, err := inbox.Entries(ownerKey)
	if err != nil {
		return false, fmt.Errorf("steer: complete: inspect parent inbox %q: %w", ownerKey, err)
	}
	wantID := fmt.Sprintf("%s:%d:final", rec.SessionID, rec.Generation)
	for _, entry := range entries {
		if entry.Kind == session.InboxEntryMessage && entry.Message != nil && messageIDOf(*entry.Message) == wantID {
			acked, ackedErr := deliverEntryIsAcked(inbox, ownerKey, rec.SessionID, wantID)
			if ackedErr != nil {
				return false, fmt.Errorf("steer: complete: inspect final acknowledgement %q: %w", wantID, ackedErr)
			}
			return acked, nil
		}
	}
	return false, nil
}

func deliveryWokeRecipient(outcome steer.DeliveryOutcome) bool {
	return outcome == steer.DeliveryWoke || outcome == steer.DeliveryQueuedIntoLiveTurn
}

type steeredCompletionFlightKey struct {
	loop       *AgentLoop
	sessionID  string
	generation int
}

type steeredCompletionFlight struct {
	done      chan struct{}
	finalWoke bool
	err       error
}

// steeredCompletionFlights serializes the side-effecting completion tail per
// AgentLoop/session/generation. Entries exist only while a completion is in
// flight and are removed on every exit, so the process-global coordinator
// never retains an AgentLoop after the call returns.
var steeredCompletionFlights sync.Map //nolint:gochecknoglobals

func (al *AgentLoop) runSteeredCompletionOnce(rec *session.LifecycleRecord, complete func() (bool, error)) (bool, error) {
	key := steeredCompletionFlightKey{loop: al, sessionID: rec.SessionID, generation: rec.Generation}
	candidate := &steeredCompletionFlight{done: make(chan struct{})}
	actual, loaded := steeredCompletionFlights.LoadOrStore(key, candidate)
	if loaded {
		flight, ok := actual.(*steeredCompletionFlight)
		if !ok {
			steeredCompletionFlights.CompareAndDelete(key, actual)
			return false, errors.New("steer: complete: invalid completion-flight state")
		}
		<-flight.done
		return flight.finalWoke, flight.err
	}

	defer func() {
		close(candidate.done)
		steeredCompletionFlights.Delete(key)
	}()
	current, err := al.completionFlightCurrent(rec)
	if err != nil || !current {
		candidate.err = err
		return false, err
	}
	candidate.finalWoke, candidate.err = complete()
	return candidate.finalWoke, candidate.err
}

func (al *AgentLoop) completionFlightCurrent(rec *session.LifecycleRecord) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, errors.New("steer: complete: lifecycle store is not wired")
	}
	current, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		return false, fmt.Errorf("steer: complete: revalidate %q: %w", rec.SessionID, err)
	}
	return current != nil && current.Generation == rec.Generation && !current.Terminal(), nil
}

// errCompleteStaleGeneration, errCompleteAlreadyTerminal and
// errCompleteStoppedDuringDelivery are completeSteeredTurn's Mutate-refusal
// sentinels (Finding D, above) — the completion-side counterparts of
// steer_launcher.go's dispatchRefusalError family.
var (
	errCompleteStaleGeneration       = errors.New("steer: complete: generation changed during delivery")
	errCompleteAlreadyTerminal       = errors.New("steer: complete: record became terminal during delivery")
	errCompleteStoppedDuringDelivery = errors.New("steer: complete: a Stop landed during delivery")
)

// completeStateWriteTestHook is a test-only synchronization seam, fired
// immediately before completeSteeredTurn's terminal-state write — after
// Deliver has already performed its I/O, before the write that could race a
// concurrent Stop or Revive. Mirrors steer_launcher.go's
// dispatchStateWriteTestHook; always nil in production, never set outside a
// _test.go file.
var completeStateWriteTestHook func(sessionID string)

// completeBeforeDeliveryTestHook is a test-only synchronization seam fired
// after every completion precondition has passed but before the durable final
// entry is delivered. It lets concurrency tests line up two completion paths
// at the exact side-effect boundary without using sleeps.
var completeBeforeDeliveryTestHook func(sessionID string)

// reportUndeliveredWake is the ONE place every pkg/agent `Deliver` call site
// ACTS on steer.Delivery.Outcome.
//
// [ADR-091 fix lane RX-OUTCOME, HIGH] That field carries the single fact
// separating "the parent knows" (DeliveryWoke / DeliveryQueuedIntoLiveTurn)
// from "the parent will never know" (DeliveryStoredNotWoken), and until this
// lane every caller in this package threw it away with
// `_, err := deliverer.Deliver(...)`. A stored-not-woken outcome on a
// wake-eligible message means the entry IS durable but nothing will re-enter
// the recipient until boot recovery re-nudges it — an indefinite, completely
// invisible stall. We spent hours chasing exactly this class of silent hang;
// it is now an ERROR line naming child, parent, message id and generation.
//
// It deliberately only LOGS. The inbox entry is already durable by the time
// Deliver returns, so refusing the caller's own follow-on write (the terminal
// lifecycle write, the frames, the tool result) would add a SECOND
// inconsistency on top of the first rather than repair anything — the same
// posture steer_cancel.go::deliverTerminalReport settled on for the identical
// question, and the same reason steer_audience.go::Deliver itself treats a
// failed wake as non-fatal.
//
// Wake-eligibility, not the outcome kind, is what makes a stored-not-woken
// result newsworthy: for `progress`, `checkpoint` and a non-fatal `error`,
// stored-not-woken IS the contract (FR-B-010) and must stay silent, or the
// log fills with non-events. Classification is read from the message itself
// via session.ClassifySessionMessage — the SAME authority Deliver used to
// decide whether to wake at all — so the two can never drift apart. An
// unclassifiable message is reported, never swallowed.
func reportUndeliveredWake(op string, event steer.UpwardEvent, parentSessionID string, generation int, delivery steer.Delivery) {
	if delivery.Outcome != steer.DeliveryStoredNotWoken {
		return
	}
	// Q1=A (#984 follow-up): a producer-suppressed wake is a deliberate
	// stored-not-woken — the session-goal met verdict, acked at hand-back —
	// not a stall. The ERROR below exists for UNintended stored-not-woken
	// outcomes; a suppressed event must never trip it.
	if event.SuppressWake {
		return
	}
	if class, err := session.ClassifySessionMessage(event.Message); err == nil && !class.WakeEligible {
		return
	}
	if parentSessionID == "" {
		parentSessionID = "(unknown)"
	}
	logger.ErrorCF("agent", op+": stored but the recipient was NOT woken — it learns nothing until boot recovery re-nudges it",
		map[string]any{
			"session_id":        event.ChildSessionID,
			"parent_session_id": parentSessionID,
			"message_id":        delivery.MessageID,
			"generation":        generation,
			"outcome":           string(event.Outcome),
			"delivery_outcome":  string(delivery.Outcome),
			"terminal":          isTerminalOutcome(event.Outcome),
		})
}

// steerParentSessionID returns rec's steering session id, or "" when rec
// carries no edge. Nil-safe on purpose: reportUndeliveredWake's diagnostic
// must never be the thing that panics on an already-degraded record.
func steerParentSessionID(rec *session.LifecycleRecord) string {
	if rec == nil || rec.SteeredBy == nil {
		return ""
	}
	return rec.SteeredBy.SteeringSessionID
}

// steerDeliveryEdge best-effort reads (parent session id, generation) for
// childSessionID straight from the lifecycle store, for the call sites that
// hold a task or a goal record rather than the child's LifecycleRecord.
// Returns ("", 0) when the record cannot be read — a diagnostic lookup never
// fails its caller, and reportUndeliveredWake still logs without them.
func steerDeliveryEdge(lifecycle *session.LifecycleStore, childSessionID string) (string, int) {
	if lifecycle == nil || childSessionID == "" {
		return "", 0
	}
	rec, err := lifecycle.Load(childSessionID)
	if err != nil || rec == nil {
		return "", 0
	}
	return steerParentSessionID(rec), rec.Generation
}

// finishSteeredGoalTurn sends a goal-bearing child through the same
// session-owned claim/Judge pipeline used by an interactive goal turn. The
// Judge dispatch remains asynchronous, matching runAgentLoop's ordering.
//
// A turn that DIED never reaches that pipeline: failed, timed_out and
// cancelled are I-5 outcomes, not goal verdicts, and go to
// completeSteeredTurn — see the branch at the top of the body.
//
// A steered child runs through steer_launcher.go's own dispatch goroutine,
// never through runAgentLoop's inline post-turn block (loop.go) — so
// checkGoalLoopAfterTurn's re-injected follow-ups (result.followUps) have no
// turnResult of an in-flight request to ride back out on, exactly the
// "there is no turnResult to attach a followUp to" situation
// dispatchGoalAsyncFollowUp's own doc comment (goal_triggers.go) names for
// the idle-tick and deferred-claim paths. Reuses that SAME re-inject
// primitive (al.asyncNotifier.Notify, the one PublishInbound call site
// scripts/check-operator-prompt-sites.sh's FR-029a census already counts)
// instead of a second, direct bus.MessageBus.PublishInbound call — a bare
// direct publish here would also have resolved the wrong agent for a worker
// whose Channel/ChatID are not a routable live instance (the exact "Worker
// vs Jim" misattribution FIX 5d's AsyncOriginAgentID exists to prevent;
// processSystemMessage). SenderCanonicalID carries the follow-up's own
// goalLoopFollowUpSenderID stamp through unchanged so
// checkGoalLoopAfterTurn's origin gate still accepts the re-injected turn.
func (al *AgentLoop) finishSteeredGoalTurn(ts *turnState, rec *session.LifecycleRecord, result *turnResult, runErr error) {
	if al == nil || ts == nil || rec == nil || result == nil {
		return
	}
	// A dead turn has no claim to adjudicate. Route the three failing
	// outcomes of I-5's table (failed / timed_out / cancelled) through the
	// ONE completion path instead of returning silently: completeSteeredTurn
	// delivers the failure upward, writes the terminal state, and releases
	// any ancestor that was only waiting on this child. Without it the
	// record stayed `running` for ever and the parent waited on a worker
	// that was already gone.
	if _, nextState, _ := completionDisposition(*result, runErr, strings.TrimSpace(result.finalContent)); session.IsTerminalLifecycleState(nextState) {
		if err := al.completeSteeredTurn(context.Background(), rec, *result, runErr); err != nil {
			logger.WarnCF("agent", "steer: report a dead goal-bearing child upward failed",
				map[string]any{"session_id": rec.SessionID, "state": string(nextState), "error": err.Error()})
		}
		return
	}
	if runErr != nil {
		return
	}
	al.checkGoalLoopAfterTurn(context.Background(), ts.agent, ts.opts, result)
	for _, followUp := range result.followUps {
		if al.asyncNotifier == nil {
			logger.WarnCF("agent", "steer: publish goal follow-up failed: async notifier is not wired",
				map[string]any{"session_id": ts.opts.TranscriptSessionID})
			continue
		}
		// A delegate/task-origin steered child carries no external channel
		// binding at all (meta.Channel/PeerID are seeded "" at launch,
		// steer_launcher.go::Launch) — AsyncNotifyEvent.Notify's FR-N7 guard
		// refuses an empty destination outright. Falls back to the SAME
		// "system"/synthetic-chat-id destination
		// TaskExecutor.wakeOwnerAttemptsExhausted (task_executor_judge.go)
		// already uses for a channel-less task origin: it is discarded for
		// routing purposes (processSystemMessage resolves the agent from
		// AsyncOriginAgentID, never from this pair) and only shapes where a
		// SendResponse reply is published, which a channel-less steered
		// child has nowhere real to receive anyway.
		channel, chatID := followUp.Channel, followUp.ChatID
		if channel == "" || chatID == "" {
			channel, chatID = "system", "steer:"+followUp.SessionID
		}
		notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		// Finding F (ADR-091 fix lane 1, MEDIUM): a follow-up published with
		// no steer_message_id/steer_generation metadata falls through
		// processSystemMessage's routing check into its legacy,
		// hand-built-SendResponse tail (AC-11's retired containment) instead
		// of processSteeredSystemWake — bypassing reconstruction (I-3), the
		// Stop reservation, admission (I-3 "live turns only") AND
		// generation-aware cancel (I-6) for every re-injected goal
		// follow-up. Stamping the SAME metadata pair WakeParent/
		// WakeParentAlways stamp (async_notifier.go) routes this exactly
		// like any other wake: rec.Generation is the CURRENT generation of
		// the session this follow-up continues, never a fresh mint.
		err := al.asyncNotifier.Notify(notifyCtx, AsyncNotifyEvent{
			Channel:             channel,
			ChatID:              chatID,
			AgentID:             ts.agentID,
			TranscriptSessionID: followUp.SessionID,
			SourceKind:          "steer_goal_loop",
			SenderCanonicalID:   followUp.Sender.CanonicalID,
			Content:             followUp.Content,
			Metadata: map[string]any{
				"steer_message_id": uuid.NewString(),
				"steer_generation": rec.Generation,
			},
		})
		cancel()
		if err != nil {
			logger.WarnCF("agent", "steer: publish goal follow-up failed",
				map[string]any{"session_id": ts.opts.TranscriptSessionID, "error": err.Error()})
		}
	}
	if result.goalDeferredAdjudication != nil {
		work := result.goalDeferredAdjudication
		go al.dispatchDeferredGoalAdjudication(work)
	}
}

func completionDisposition(result turnResult, runErr error, answer string) (steer.Outcome, session.LifecycleState, string) {
	switch {
	case errors.Is(runErr, context.DeadlineExceeded):
		return steer.OutcomeTimedOut, session.LifecycleTimedOut, "timed_out: the session exceeded its lifetime limit"
	case errors.Is(runErr, context.Canceled), result.status == TurnEndStatusAborted:
		return steer.OutcomeInterrupted, session.LifecycleCancelled, "interrupted: the session was cancelled"
	case runErr != nil:
		return steer.OutcomeFailed, session.LifecycleFailed, "failed: " + runErr.Error()
	case result.finalContent == toolLimitResponse:
		// The turn hit the tool-iteration ceiling with no final response
		// (loop_run_turn.go::finalizeTurn sets finalContent to the
		// toolLimitResponse sentinel and marks the turn failed). This is a
		// non-fatal lifecycle notice — the child stopped early, it did not
		// crash — so the parent gets an `error` (fatal: false) inbox entry;
		// the record stays running so the child remains resumable rather
		// than terminal-failed.
		//
		// ADR-091 fix lane RX-HANG: this outcome used to ALSO be non-wake-
		// eligible (I-5's blanket "fatal: false never wakes" rule), which
		// meant the parent was never told anything happened at all — it
		// could not "nudge the child" because it never learned the child
		// had stopped. hasRunningOrQueuedDescendant then saw this child
		// stuck at `running` forever and refused to let the PARENT complete
		// either, hanging the whole ancestor chain silently up to the
		// user's chat, recoverable only by a gateway restart
		// (boot_sweep.go::recoverSteered). The failureReason text below
		// carries the message_inbox.go::LifecycleNoticeReasonPrefixToolIterations
		// prefix that classifyEnvelope now recognizes as wake-eligible even
		// though Fatal stays false, so the parent IS woken — see that
		// constant's doc comment for the full reasoning. Pairs with
		// hasRunningOrQueuedDescendant's own idle-check below, which stops
		// treating a `running`-but-no-live-turn child (exactly this
		// outcome) as blocking its parent's own completion forever.
		return steer.OutcomeLifecycleNotice, session.LifecycleRunning,
			session.LifecycleNoticeReasonPrefixToolIterations + " the session reached its tool-iteration limit without a final answer"
	case result.turnFailed || result.status == TurnEndStatusError:
		return steer.OutcomeFailed, session.LifecycleFailed, "failed: the turn ended with an error"
	case result.status == TurnEndStatusParked:
		return "", "", ""
	default:
		return "", "", ""
	}
}

func (al *AgentLoop) completionMessage(rec *session.LifecycleRecord, outcome steer.Outcome, answer, failureReason string) (generated.SessionMessage, error) {
	now := time.Now().UTC()
	var message generated.SessionMessage
	if outcome == steer.OutcomeFinalAnswer {
		questions, err := al.parkedQuestions(rec.SessionID)
		if err != nil {
			return message, fmt.Errorf("steer: complete: collect parked questions: %w", err)
		}
		err = message.FromSessionMessageHandback(generated.SessionMessageHandback{
			MessageId:      rec.SessionID,
			SessionId:      rec.SessionID,
			CreatedAt:      now,
			Depth:          1,
			SenderIdentity: rec.AgentID,
			Mode:           generated.SessionMessageHandbackModeFinal,
			ResultSoFar:    answer,
			Artifacts:      []string{},
			OpenQuestions:  questions,
		})
		return message, err
	}
	if failureReason == "" {
		failureReason = string(outcome) + ": the session did not complete"
	}
	// A lifecycle notice (the tool-iteration limit) is the one non-terminal
	// outcome to reach this branch — steer_audience.go::validateOutcomeMessage
	// pairs OutcomeLifecycleNotice with an `error` carrying `fatal: false`.
	// Every other non-final-answer outcome is a genuine failure and is fatal.
	// The notice also takes a fresh, inbox-assigned id: Deliver only stamps
	// the deterministic `<child>:<gen>:final` id for terminal outcomes, so a
	// re-nudged child that hits the limit again must not collide on
	// rec.SessionID and get silently deduplicated.
	messageID := rec.SessionID
	fatal := outcome != steer.OutcomeLifecycleNotice
	if outcome == steer.OutcomeLifecycleNotice {
		messageID = uuid.NewString()
	}
	err := message.FromSessionMessageError(generated.SessionMessageError{
		MessageId:      messageID,
		SessionId:      rec.SessionID,
		CreatedAt:      now,
		Depth:          1,
		SenderIdentity: rec.AgentID,
		Fatal:          fatal,
		Text:           failureReason,
	})
	return message, err
}

// setSteeredCompletionWrite tracks the delivery-before-terminal interval of
// a steered session's completion, even after its turn has deregistered.
func (al *AgentLoop) setSteeredCompletionWrite(sessionID string, active bool) {
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

func (al *AgentLoop) steeredCompletionWriteActive(sessionID string) bool {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.steeredCompletionWrites[sessionID]
}

// hasRunningOrQueuedDescendant reports whether parentID has any descendant
// that is genuinely still working — a `queued` child (admitted or not, it
// WILL run), or a `running` child that is actually executing: a live turn
// registered under its own SessionID, or (task-origin only) a task run the
// executor is still holding.
//
// ADR-091 fix lane RX-HANG: a `running` child is deliberately NOT enough on
// its own. completionDisposition's steer.OutcomeLifecycleNotice case keeps
// a child that exhausted its tool-iteration budget at LifecycleRunning on
// purpose — it is resumable, not dead — but that also means its turn has
// already fully exited (runDispatchedSteeredTurn's defer chain has already
// cleared it from al.activeTurnStates) with nothing else ever going to
// change its record. Counting that child as "blocking" made its PARENT
// wait forever too, even after fixing the wake itself (message_inbox.go's
// classifyEnvelope): the parent would be woken, but its own
// completeSteeredTurn call would still see this child as `running` and
// refuse to complete. getActiveTurnState + IsAlive() is the SAME
// live-turn signal steer_audience.go's Deliver already uses to decide
// in-turn-queue vs. async wake — a `running` record with no live turn is
// idle, not executing, and must not block its parent's completion.
//
// TASK-ORIGIN LIVENESS (ADR-091 fix lane RX-OUTCOME, replacing RX-HANG's
// round-2 unconditional exclusion): al.getActiveTurnState is keyed by
// turnState.sessionKey, which is NOT always the LifecycleRecord's own
// SessionID. steer_launcher.go::dispatchSteeredSessionWithReservation's
// task-origin branch (rec.Origin.Kind == session.OriginKindTask) never calls
// registerTurnIfAbsent at all — it hands off to
// TaskExecutor.dispatchLaunchedTask, which runs the turn under
// task_executor_run.go::taskTurnSessionKey, a DIFFERENT string from the
// child's own SessionID. So for a task-origin child on its ORIGINAL run,
// getActiveTurnState(child.SessionID) returns nil unconditionally, for the
// entire lifetime of the run, whether or not it is genuinely still
// executing — not a narrow race window, a permanent miss.
//
// RX-HANG closed that by making a `running` task-origin child block its
// parent unconditionally. Safe, but too coarse: a task-origin child left
// `running` by its own tool-iteration lifecycle notice (its turn long
// finished, its record deliberately kept resumable) then blocked its parent
// FOR EVER — a narrower version of the same hang. The parent was woken and
// still could not complete past that child.
//
// The fix is to ask the right authority instead of excluding the origin:
// al.taskRunInFlight (task_executor_run.go, which OWNS the task dispatch
// path) reports whether the executor is currently holding that task's run.
// The two checks compose rather than compete — either a live turn under the
// child's own SessionID, or a task run in flight, counts as working:
//
//   - The wake/follow-up re-entry path (loop_inbound.go::
//     processSteeredSystemWake) and the generic steer_launcher.go Path B DO
//     call registerTurnIfAbsent under the record's own SessionID BEFORE
//     writing LifecycleRunning, so getActiveTurnState is a reliable signal
//     there — including for a task-origin child that was later re-woken.
//   - The original task run is covered by the dispatch slot, which is taken
//     before the `running` write and released only after the terminal one,
//     and therefore also spans the gaps between a multi-turn run's turns
//     that no turn-registry lookup could see.
//
// Reconstructing the "agent:...:task:..." key here to look the turn up under
// its real name was considered and rejected twice: the format is owned by
// task_executor_run.go, duplicating it as a correctness-critical signal
// would silently reopen this hole the moment it drifts, and it would STILL
// be the wrong question — a run between turns has no registered turn at all.
func (al *AgentLoop) hasRunningOrQueuedDescendant(parentID string) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		// SteerLauncher.Launch refuses a child without a lifecycle store, so
		// there is no descendant to wait for. A failed List on a wired store
		// still returns an error instead of appearing quiet.
		return false, nil
	}
	seen := map[string]bool{parentID: true}
	queue := []string{parentID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := lifecycle.List(session.LifecycleFilter{SteeringSessionID: current})
		if err != nil {
			return false, fmt.Errorf("steer: complete: list descendants of %q: %w", current, err)
		}
		for i := range children {
			child := children[i]
			if seen[child.SessionID] {
				continue
			}
			seen[child.SessionID] = true
			switch child.State {
			case session.LifecycleQueued:
				return true, nil
			case session.LifecycleRunning:
				if al.steeredCompletionWriteActive(child.SessionID) {
					return true, nil
				}
				if ts := al.getActiveTurnState(child.SessionID); ts != nil && ts.IsAlive() {
					return true, nil
				}
				if al.taskRunInFlight(&child) {
					// A task-origin child whose run the executor still
					// holds — genuinely executing under a key this
					// registry cannot be asked about (see the
					// task-origin liveness note above). Always false
					// for every other origin kind.
					return true, nil
				}
				// A `running` record with no live turn and no run in
				// flight (e.g. a tool-iteration lifecycle notice) is
				// idle, not executing — fall through and keep walking
				// its own descendants instead of blocking on it.
			}
			queue = append(queue, child.SessionID)
		}
	}
	return false, nil
}

// completeSteeredTurnAfterGoal is the ONE completion tail the goal path's
// terminal arms call once the goal-record transition has landed (design-note
// decision (b)) — and the one entry the judge-unavailable re-drive uses to
// fail the child visibly (decision (c)). It reuses completeSteeredTurn
// wholesale: the disposition derives the outcome from runErr (nil → the
// child's answer as a normal final-answer completion; non-nil → a failed
// child whose reason names the judge), Deliver runs first, the terminal
// write carries the existing sentinels, and the Mutate-success path's
// pair-end (e)① circled-one ends the goal when the write lands with the goal
// still active.
//
// Every error here is logged and swallowed: the goal decision is already
// settled at this point, so a delivery or persist failure is a repairable
// gap (Deliver-first means boot recovery repairs a delivered-but-not-terminal
// record), never a reason to unwind the caller's own completed transition.
func (al *AgentLoop) completeSteeredTurnAfterGoal(ctx context.Context, sessionID, answer string, runErr error) bool {
	if al == nil || sessionID == "" {
		return false
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		// Without this store no steered child can have been launched, so no
		// parent-facing completion tail exists for this ordinary goal.
		return false
	}
	snapshot, err := lifecycle.Load(sessionID)
	if err != nil || snapshot == nil {
		logger.WarnCF("agent", "goal: completion tail skipped — the child record is unreadable",
			map[string]any{"session_id": sessionID, "error": errString(err)})
		return false
	}
	finalWoke, err := al.completeSteeredTurnDurably(ctx, snapshot, turnResult{finalContent: answer}, runErr)
	if err != nil {
		logger.WarnCF("agent", "goal: completion tail failed — boot recovery repairs a delivered-but-not-terminal gap",
			map[string]any{"session_id": sessionID, "error": err.Error()})
	}
	return finalWoke
}

// completeSteeredTurnIfDeferredAtGate routes a steered child through the
// completion tail exactly when it is deferred at the (a) gate: an ACTIVE goal
// ended above it (/goal clear, idle-expiry sweep) while its own turn had
// already exited and its record is still non-terminal. The gate only re-runs
// on a turn exit or cancel, so without this call nothing re-enters it — the
// child record stays `running` until the next boot sweep repairs it (architect
// finding F4, #984 follow-up). The goal record itself is already ended by the
// caller; the tail's Deliver + terminal write + pair-end (e)① then run with
// the goal gone, so the (a) gate passes and nothing loops.
//
// A LIVE turn is deliberately left alone: its own exit re-runs the gate (the
// goal is gone by then) and completes it through the same tail. NeedsInput
// (parked) children are left to their own park flow. The cancelled outcome is
// honest for "the operator ended the goal": interrupted handback to the
// parent, state cancelled, never a false "empty answer" failure.
func (al *AgentLoop) completeSteeredTurnIfDeferredAtGate(sessionID string) {
	if al == nil || sessionID == "" {
		return
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil || rec == nil || rec.SteeredBy == nil {
		return
	}
	if rec.Terminal() || rec.State == session.LifecycleNeedsInput {
		return
	}
	if ts := al.getActiveTurnState(sessionID); ts != nil && ts.IsAlive() {
		return
	}
	tailErr := fmt.Errorf("%w: the goal was ended while this session was deferred at the completion gate", context.Canceled)
	al.completeSteeredTurnAfterGoal(context.Background(), sessionID, "", tailErr)
}

func (al *AgentLoop) parkedQuestions(ownerID string) ([]string, error) {
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return nil, errors.New("message inbox store is not wired")
	}
	messages, cursor, more, err := inbox.Drain(ownerID, "", "", 256)
	if err != nil {
		return nil, err
	}
	for more {
		var page []generated.SessionMessage
		page, cursor, more, err = inbox.Drain(ownerID, "", cursor, 256)
		if err != nil {
			return nil, err
		}
		messages = append(messages, page...)
	}
	questions := make([]string, 0)
	for _, message := range messages {
		kind, err := message.Discriminator()
		if err != nil || kind != "question" {
			continue
		}
		question, err := message.AsSessionMessageQuestion()
		if err == nil && strings.TrimSpace(question.Text) != "" {
			questions = append(questions, question.Text)
		}
	}
	return questions, nil
}
