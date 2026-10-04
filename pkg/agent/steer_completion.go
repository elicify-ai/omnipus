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
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
)

// completeSteeredTurn applies the goal-less completion disposition after a
// real turn exits. Order is D2 CRIT-001's one outcome/publication commit
// boundary (steer_completion_commit.go): the single lifecycle mutation
// commits the terminal state AND the protected, unpublished final-delivery
// outbox tuple FIRST — so terminal-first can no longer lose the child's only
// result; the exact committed bytes live in the durable outbox — and only the
// winning commit is then published to the parent and acknowledged through the
// delivery-only journal.
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
	activeGoal, goalErr := activeGoalForSession(rec.SessionID)
	if goalErr != nil {
		return false, fmt.Errorf("steer: complete: cannot determine goal state: %w", goalErr)
	}
	if activeGoal != nil && !session.IsTerminalLifecycleState(nextState) && nextState != session.LifecycleStopped {
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

// deliverSteeredCompletion is D2 CRIT-001's boundary wired through the
// finishing window: the COMMIT (terminal state + protected outbox tuple in
// one mutation — for a stop disposition, the stopped LANDING) runs first as
// the window's prepare half; the PUBLISH runs as the transition half, only
// for a winning commit. A stopped disposition publishes NOTHING in the
// prepare half — no note rewrite, no parent inbox append, no wake: a
// lifecycle write fault after the fence must not leak a parent effect. The
// D6 direct-parent stopped-child notice is published by the transition half
// AFTER the landing, composed from the control ledger's landed history
// (stopped_notice.go::deliverLandedStopNotices); a notice append failure
// there leaves the landed history pending and retryable and never un-lands
// the stop. A stop that had already landed (the losing completion T11 pins)
// publishes nothing; a non-terminal lifecycle notice keeps its direct
// delivery.
func (al *AgentLoop) deliverSteeredCompletion(ctx context.Context, rec *session.LifecycleRecord, outcome steer.Outcome, nextState session.LifecycleState, answer, failureReason string) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, errors.New("steer: complete: lifecycle store is not wired")
	}
	var commitRes steeredCommitResult
	finalWoke := false
	prepare := func() error {
		// D2 CRIT-001: the COMMIT is the one publication boundary. A stop
		// publishes nothing here — the landing below is the commit, and the
		// notice publisher runs only in the transition half, from the landed
		// history, after it.
		var err error
		commitRes, err = al.commitSteeredCompletion(lifecycle, rec, nextState, outcome, answer, failureReason)
		return err
	}
	transition := func() (bool, error) {
		switch commitRes.kind {
		case steeredCommitTerminal:
			woke, pubErr := al.publishCommittedFinal(ctx, rec, commitRes)
			finalWoke = woke
			return true, pubErr
		case steeredCommitNotice:
			noticeErr := al.deliverSteeredNotice(ctx, rec, outcome, answer, failureReason)
			return false, noticeErr
		case steeredCommitStopped:
			// A stop THIS mutation landed publishes ONLY the D6 historical
			// direct-parent notice, composed from each landed transition's
			// own tuple — never the legacy interrupted/timeout
			// <child>:<gen>:final beside it (a stopped child is not a final
			// hand-back; done/failed keep the committed outbox). A stop that
			// had already landed (this completion is the loser T11 pins)
			// publishes nothing at all. A notice append failure is returned
			// visibly — the landing is already durable and stays.
			if commitRes.landedStop {
				if _, pubErr := al.deliverLandedStopNotices(ctx, rec); pubErr != nil {
					return false, pubErr
				}
				// A fence-less landing has no accepted control behind it, so
				// no landed-stop history exists for the publisher to discover
				// — without this the parent never learns the child stopped.
				// Its notice is delivered HERE, once, through the same
				// publisher function and message format, composed from the
				// landing's retained note. The boot replay has no history to
				// re-deliver, so this one append and one wake are the whole
				// delivery.
				if commitRes.landed == nil && commitRes.landedNote != nil {
					_, noticeErr := al.deliverLandedStopNotice(ctx, rec, stoppedTransitionFromLandedNote(rec, commitRes.landedNote))
					return false, noticeErr
				}
				return false, nil
			}
			return false, nil
		default:
			// steeredCommitRefused: mirror the pre-boundary refusal — report
			// the reloaded record's terminality to the finishing disposal.
			return commitRes.terminalNow, nil
		}
	}
	// Round-3 finishing-window protocol (issue #1020): items accepted during
	// the terminal-transition window are returned via the runTerminalTransitionWithFinishing
	// onFinishing closure (after prepare, after the durable transition, after
	// the deferred finish), and routed to one of three dispositions: revive
	// the child into a new generation (post-finish STEER on a successful
	// commit), replay the durable inbox entry (post-finish WAKE on a
	// successful commit), or drain as a
	// same-generation continuation (commit refused OR prepare failed — the
	// child stays non-terminal and the items get a consumer via the existing
	// retry loop).
	var finishingItems []steeringQueueItem
	if al.steering == nil {
		if prepareErr := prepare(); prepareErr != nil {
			return finalWoke, prepareErr
		}
		_, transitionErr := transition()
		return finalWoke, transitionErr
	}
	started, terminal, transitionErr := al.steering.runTerminalTransitionWithFinishing(
		rec.SessionID,
		prepare,
		transition,
		func(items []steeringQueueItem) { finishingItems = items },
	)
	if !started {
		return finalWoke, errCompleteSteeringPending
	}
	if len(finishingItems) > 0 {
		return finalWoke, al.processFinishingItems(ctx, rec, transitionErr, finishingItems, terminal)
	}
	return finalWoke, transitionErr
}

// deliverSteeredNotice delivers the one non-terminal upward event this
// boundary still publishes directly: the tool-iteration lifecycle notice.
// It commits nothing — the record keeps its current state — and a fenced
// record never reaches here (commitSteeredCompletion already gave the stop
// the record).
func (al *AgentLoop) deliverSteeredNotice(ctx context.Context, rec *session.LifecycleRecord, outcome steer.Outcome, answer, failureReason string) error {
	message, messageErr := al.completionMessage(rec, outcome, answer, failureReason)
	if messageErr != nil {
		return messageErr
	}
	event := steer.UpwardEvent{
		ChildSessionID: rec.SessionID,
		Generation:     rec.Generation,
		Outcome:        outcome,
		Message:        message,
	}
	_, err := al.deliverSteeredTerminal(ctx, rec, event)
	return err
}

// processFinishingItems disposes of the items accepted during the
// terminal-transition finishing window (issue #1020 round-3 / round-4
// correction).
//
// Successful terminal commit: revive the child into a new generation
// carrying every post-finish STEER. A post-finish WAKE is replayed from
// its real durable inbox entry: revive the terminal recipient without
// repeating its old instruction, then run the existing wake consumer,
// which writes the consumed marker and acknowledges that entry.
//
// Commit refused (Stop landed, terminal-write conflict) OR prepare failed:
// the child is non-terminal; drain waiting items as a same-generation
// continuation right away. No silent drop on a subsequent prepare
// failure either — the bounded retry budget lives in disposeSteeredTurnResult
// (continueDrainMaxRetries + loud abandonment, the same shape the
// session_worker uses), not here.
//
// Returns the error completeSteeredTurn should propagate to
// disposeSteeredTurnResult.
func (al *AgentLoop) processFinishingItems(
	ctx context.Context,
	rec *session.LifecycleRecord,
	transitionErr error,
	finishingItems []steeringQueueItem,
	terminalCommitted bool,
) error {
	if al == nil || len(finishingItems) == 0 {
		return transitionErr
	}
	// Successful terminal commit: revive the child into a new generation
	// carrying every post-finish STEER. Every accepted item of THIS
	// hand-off goes into ONE revival (round-4 correction — the previous
	// bounded-drain sentinel capped revival at one per session, which
	// dropped later-arriving items with no consumer; "all accepted steers
	// of one hand-off go into ONE revival carrying all of them in order").
	if terminalCommitted && transitionErr == nil {
		steers := make([]string, 0, len(finishingItems))
		wakes := make([]steeringQueueItem, 0, len(finishingItems))
		for _, item := range finishingItems {
			if item.wake != nil {
				wakes = append(wakes, item)
				continue
			}
			text := strings.TrimSpace(item.message.Content)
			if text == "" {
				continue
			}
			steers = append(steers, text)
		}
		// Carry every accepted post-finish steer in the FIRST revived
		// generation, exactly in arrival order, so the child's next turn
		// actually sees all of them. ReviveStoppedSession appends the
		// text to the transcript BEFORE minting the new generation
		// (its own appendSteeredInstruction call), so the very first
		// revival already carries the full text; subsequent items ride
		// on the same revival by waiting until it registers before
		// enqueueing again — see the brief's "if a revival is already
		// in flight" item, which the natural queue path satisfies
		// because the steering scope is keyed by sessionID, not
		// generation.
		if len(steers) == 0 {
			return al.schedulePostFinishWakes(rec, wakes)
		}
		by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "system:post-finish-steer"}
		// Round-4 correction: mark the post-finish revival at the
		// NEW generation (rec.Generation+1) so completionMessage for
		// THIS session at generation N+1 prefixes the hand-back text.
		// The OLD generation's terminal commit (gen N) does NOT
		// consume the stamp — it carries the pre-late answer
		// unchanged (round-3 spec item 3's contract). Cleared on
		// completion read; cleared on revive failure so a stale
		// stamp never leaks across generations.
		al.markPostFinishRevival(rec.SessionID, rec.Generation+1)
		// Pass the FIRST steer through ReviveStoppedSession's normal
		// path so its appendSteeredInstruction + generation mint runs
		// (the latter is the only place the new generation is created).
		if _, err := al.ReviveStoppedSession(ctx, rec.SessionID, by, steers[0]); err != nil {
			al.clearPostFinishRevival(rec.SessionID)
			return errors.Join(fmt.Errorf("steer: post-finish revive %q: %w", rec.SessionID, err),
				al.schedulePostFinishWakes(rec, wakes))
		}
		// Every remaining post-finish steer rides on the revived
		// generation. They go onto the same sessionID scope, which the
		// new turn dequeues on its next tool boundary; that is the
		// round-4 "append to the revived generation's scope" path.
		for _, text := range steers[1:] {
			if _, err := al.EnqueueSteeringMessage(rec.SessionID, "", providers.Message{
				Role:    "user",
				Content: text,
			}, ""); err != nil {
				return errors.Join(fmt.Errorf("steer: post-finish enqueue onto revived scope %q: %w", rec.SessionID, err),
					al.schedulePostFinishWakes(rec, wakes))
			}
		}
		return al.schedulePostFinishWakes(rec, wakes)
	}
	// Commit refused (Stop landed, terminal-write conflict, etc.) OR
	// prepare failed: drain waiting items as a same-generation
	// continuation right away. The child is non-terminal; the items
	// already have a consumer in the existing drain machinery
	// (retrySteeringContinuation's continueDrainMaxRetries budget inside
	// drainSteeredTurn, then abandonSteeredQueuedSteering's loud
	// abandonment if the budget is exhausted). The round-4 correction
	// removed the previous bounded-drain sentinel here; the bound lives
	// in disposeSteeredTurnResult, not in this function.
	if al.steering == nil {
		return transitionErr
	}
	al.steering.prependItemsScope(rec.SessionID, finishingItems)
	return errCompleteSteeringPending
}

func (al *AgentLoop) deliverSteeredTerminal(
	ctx context.Context,
	rec *session.LifecycleRecord,
	event steer.UpwardEvent,
) (bool, error) {
	deliverer := al.getUpwardDeliverer()
	if deliverer == nil {
		return false, errSteerUpwardDelivererNotWired
	}
	delivery, err := deliverer.Deliver(ctx, event)
	if err != nil {
		return false, fmt.Errorf("steer: complete: deliver %q: %w", rec.SessionID, err)
	}
	reportUndeliveredWake("steer: complete", event, steerParentSessionID(rec), rec.Generation, delivery)
	return deliveryWokeRecipient(delivery.Outcome), nil
}

// commitSteeredTerminal and completionFinalAlreadyStored are deleted: D2
// CRIT-001's one outcome/publication commit boundary
// (steer_completion_commit.go::commitSteeredCompletion) replaced the
// deliver-first-then-write ordering, and the committed outbox tuple replaced
// the inbox-scan dedupe. Delivery retry keys on the committed outbox
// (UpdateFinalDelivery / ListPendingFinalDeliveries), never on an inbox
// probe.

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
// errCompleteStoppedDuringDelivery are the commit boundary's Mutate-refusal
// sentinels (Finding D's successors in
// steer_completion_commit.go::commitSteeredCompletion) — the
// completion-side counterparts of steer_launcher.go's dispatchRefusalError
// family.
var (
	errCompleteStaleGeneration       = errors.New("steer: complete: generation changed during delivery")
	errCompleteAlreadyTerminal       = errors.New("steer: complete: record became terminal during delivery")
	errCompleteStoppedDuringDelivery = errors.New("steer: complete: a Stop fence owns this record; the stop path lands it")
	errCompleteSteeringPending       = errors.New("steer: complete: steering arrived before terminal transition")
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
	//
	// completionDisposition lands a dead turn on EITHER LifecycleFailed
	// (session.IsTerminalLifecycleState — a genuine terminal write) or
	// LifecycleStopped (timed_out/cancelled outcomes). Since the
	// lifecycle-state consolidation (pkg/session/lifecycle.go),
	// LifecycleStopped is deliberately non-terminal — a stopped session can
	// still be revived — so IsTerminalLifecycleState alone no longer
	// recognizes it. Checking for it explicitly here is what keeps those two
	// outcomes on this path instead of silently falling through to the
	// runErr-nil-return below: the ONLY other non-empty nextState
	// completionDisposition ever returns is LifecycleRunning (the
	// tool-iteration lifecycle notice), which this condition correctly
	// leaves alone — that child is resumable, not dead.
	if _, nextState, _ := completionDisposition(*result, runErr, strings.TrimSpace(result.finalContent)); session.IsTerminalLifecycleState(nextState) || nextState == session.LifecycleStopped {
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
		return steer.OutcomeTimedOut, session.LifecycleStopped, "timed_out: the session exceeded its lifetime limit"
	case errors.Is(runErr, context.Canceled), result.status == TurnEndStatusAborted:
		return steer.OutcomeInterrupted, session.LifecycleStopped, "interrupted: the session was cancelled"
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

// postFinishRevivalPrefix is the parent-visible prefix added to the
// hand-back text when the revived generation was triggered by a late
// steer (issue #1020 round-4 correction, founder ruling Q10). The text
// mirrors the spec literally — see completeSteeredTurn's revive branch
// for the marking site and completionMessage for the consumption site.
const postFinishRevivalPrefix = "Follow-up after a late instruction: "

// markPostFinishRevival stamps (sessionID, newGeneration) as a generation
// whose terminal write was triggered by a post-finish revival.
// completionMessage reads-and-clears the stamp on the matching generation;
// if the revive itself fails before the new turn runs,
// clearPostFinishRevival removes the stale stamp so a later
// (non-post-finish) revival does not inherit it. The generation key
// (not sessionID alone) is the load-bearing piece — only the NEW
// generation's completionMessage consumes the stamp; the OLD
// generation's terminal commit (which is the one the post-finish steer
// arrives DURING) does not.
func (al *AgentLoop) markPostFinishRevival(sessionID string, newGeneration int) {
	if al == nil || sessionID == "" || newGeneration <= 0 {
		return
	}
	al.postFinishRevivalMu.Lock()
	defer al.postFinishRevivalMu.Unlock()
	if al.postFinishRevival == nil {
		al.postFinishRevival = map[string]int{}
	}
	al.postFinishRevival[sessionID] = newGeneration
}

// clearPostFinishRevival removes a sessionID from the post-finish-revival
// stamp without reading it. Used on a failed revive so the next hand-off
// for the SAME session is not mislabelled.
func (al *AgentLoop) clearPostFinishRevival(sessionID string) {
	if al == nil || sessionID == "" {
		return
	}
	al.postFinishRevivalMu.Lock()
	defer al.postFinishRevivalMu.Unlock()
	delete(al.postFinishRevival, sessionID)
}

// consumePostFinishRevival reads-and-clears the post-finish-revival stamp
// for sessionID at generation. Returns true when generation matches the
// stamp — meaning the completion is for the new generation created by the
// revival, so the caller (completionMessage) prefixes the hand-back text.
// A subsequent completionMessage for the SAME session at a DIFFERENT
// generation is unaffected (its generation key doesn't match).
func (al *AgentLoop) consumePostFinishRevival(sessionID string, generation int) bool {
	if al == nil || sessionID == "" || generation <= 0 {
		return false
	}
	al.postFinishRevivalMu.Lock()
	defer al.postFinishRevivalMu.Unlock()
	if al.postFinishRevival == nil {
		return false
	}
	stamped, ok := al.postFinishRevival[sessionID]
	if !ok || stamped != generation {
		return false
	}
	delete(al.postFinishRevival, sessionID)
	return true
}

func (al *AgentLoop) completionMessage(rec *session.LifecycleRecord, outcome steer.Outcome, answer, failureReason string) (generated.SessionMessage, error) {
	now := time.Now().UTC()
	var message generated.SessionMessage
	if outcome == steer.OutcomeFinalAnswer {
		// Round-4: a hand-back from a post-finish revival prefixes the
		// answer text so the parent sees "Follow-up after a late
		// instruction: ..." in the wake-up summary. Keyed by the
		// generation the revival minted (rec.Generation+1 at revival
		// time), so the OLD generation's terminal commit — the one
		// whose pre-late answer is round-3 spec item 3's contract —
		// never consumes the stamp.
		if al.consumePostFinishRevival(rec.SessionID, rec.Generation) {
			answer = postFinishRevivalPrefix + answer
		}
		err := message.FromSessionMessageHandback(generated.SessionMessageHandback{
			MessageId:      rec.SessionID,
			SessionId:      rec.SessionID,
			CreatedAt:      now,
			Depth:          1,
			SenderIdentity: rec.AgentID,
			Mode:           generated.SessionMessageHandbackModeFinal,
			ResultSoFar:    answer,
			Artifacts:      []string{},
			// ADR-20260928 D6b (object cd20cf8b): "On this completed path
			// open_questions is empty; parkedQuestions stays only for a
			// non-completion hand-back (e.g. a stopped-child report),
			// excluding open-relay questions (MIN-006)." A child's relayed
			// question is left untouched in the parent's inbox — its
			// non-completion home — and never rides the final hand-back.
			OpenQuestions: []string{},
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
// WILL run), a `needs_input` child (D6b: a child waiting for an answer
// holds back its parent's done), or a `running` child that is actually
// executing: a live turn registered under its own SessionID, or
// (task-origin only) a task run the executor is still holding.
//
// Completion-frontier semantics (sub-agent control-plane ADR, D6 Q2=B /
// D6b): a `stopped` descendant neither blocks nor is it enqueued — it CUTS
// the traversal, so a working or waiting descendant BEYOND a stopped node is
// invisible to this frontier. Per D8.6 the stopped node's direct parent is
// told about its stopped child by the D6 stopped-child notice — not by a
// blocking frontier row — and decides about the branch; that notice delivery
// is a separate D6 deliverable, not wired by this function. When that
// parent explicitly resumes, the record's state
// (queued/running/needs_input) blocks again — the cut follows the record's
// state, never its identity.
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
			case session.LifecycleNeedsInput:
				// D6b: a descendant waiting for an answer holds back the
				// parent's completion until the question is answered or its
				// 24-hour limit lands a visible failure. Unconditional — a
				// parked session has no live turn by definition.
				return true, nil
			case session.LifecycleStopped:
				// D6 Q2=B / D8.6: a stopped descendant does not block, and it
				// CUTS the traversal — its subtree is invisible to this
				// frontier. D8.6 routes the telling to the D6 stopped-child
				// notice to the direct parent, not to this frontier; that
				// notice delivery is a separate D6 deliverable, not wired
				// here. A resumed record blocks again by state, not identity.
				continue
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
// write carries the existing sentinels, and the Mutate-success path ends
// only the child's TURN: under MAJ-003 the terminal write never ends the
// session-owned goal — only natural met/exhaustion adjudication or an
// explicit clear (/goal clear, authorized clear_goal) does that.
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
// caller; the tail's Deliver + terminal write then run with the goal gone —
// the write ends only the child's turn, never the goal — so the (a) gate
// passes and nothing loops.
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
