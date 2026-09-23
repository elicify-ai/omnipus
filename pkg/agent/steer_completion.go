package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	if al == nil || snapshot == nil || snapshot.SteeredBy == nil {
		return nil
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return errors.New("steer: complete: lifecycle store is not wired")
	}
	rec, err := lifecycle.Load(snapshot.SessionID)
	if err != nil {
		return fmt.Errorf("steer: complete: load %q: %w", snapshot.SessionID, err)
	}
	if rec.Generation != snapshot.Generation || rec.Terminal() || rec.State == session.LifecycleNeedsInput {
		return nil
	}

	answer := strings.TrimSpace(result.finalContent)
	outcome, nextState, failureReason := completionDisposition(result, runErr, answer)
	// A goal-bearing session's SUCCESS is decided only by the claim/Judge
	// loop (finishSteeredGoalTurn). Its DEATH is not: a turn that failed, ran
	// out of time or was stopped has no claim for the Judge to adjudicate, so
	// I-5's outcome table applies to it like any other steered session. The
	// blanket refusal that used to sit here left such a child `running` for
	// ever — nothing reached the parent, and hasRunningOrQueuedDescendant
	// kept the parent from completing either.
	if rec.GoalRef != "" && !session.IsTerminalLifecycleState(nextState) {
		return nil
	}

	if outcome == "" {
		if answer == "" {
			outcome = steer.OutcomeEmptyAnswer
			nextState = session.LifecycleFailed
			failureReason = "empty_answer: the session produced no final answer"
		} else {
			blocked, blockErr := al.hasRunningOrQueuedDescendant(rec.SessionID)
			if blockErr != nil {
				return blockErr
			}
			if blocked {
				// The answer is already durable in the child's transcript. Keep the
				// lifecycle running until the last executing descendant completes.
				return nil
			}
			outcome = steer.OutcomeFinalAnswer
			nextState = session.LifecycleCompleted
		}
	}

	message, err := al.completionMessage(rec, outcome, answer, failureReason)
	if err != nil {
		return err
	}
	deliverer := al.getUpwardDeliverer()
	if deliverer == nil {
		return errSteerUpwardDelivererNotWired
	}
	// Finding A (ADR-091 fix lane 1, CRITICAL — the release blocker): this
	// Deliver call is the ONLY upward path left. The former
	// completeWaitingAncestors shortcut that used to run after the write
	// below is DELETED, not kept as a fallback: it read the parent's OWN
	// stale lastAssistantAnswer instead of ever re-entering it, so a
	// grandchild's real result was silently replaced by whatever the parent
	// had said BEFORE delegating. Landing order I-5 says the parent's
	// handback is written "by the last such child's completion wake
	// RE-ENTERING the parent" — Deliver's own wake (steer_audience.go),
	// carried all the way through by Finding B's exit-path fix
	// (disposeSteeredTurnResult, steer_launcher.go/loop_inbound.go), is that
	// re-entry. There is no second, shortcut path to the parent.
	if _, err := deliverer.Deliver(ctx, steer.UpwardEvent{
		ChildSessionID: rec.SessionID,
		Outcome:        outcome,
		Message:        message,
	}); err != nil {
		return fmt.Errorf("steer: complete: deliver %q: %w", rec.SessionID, err)
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
		if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
			return errCompleteStoppedDuringDelivery
		}
		cur.State = nextState
		cur.NeedsInput = nil
		if nextState == session.LifecycleFailed {
			cur.FailedReason = failureReason
		}
		return nil
	})
	switch {
	case mutateErr == nil:
		return nil
	case errors.Is(mutateErr, errCompleteStaleGeneration),
		errors.Is(mutateErr, errCompleteStoppedDuringDelivery),
		errors.Is(mutateErr, errCompleteAlreadyTerminal),
		errors.Is(mutateErr, session.ErrLifecycleTerminalImmutable):
		// The message is already durably delivered (Deliver ran above); a
		// Stop, a Revive or a second completion racing this write is a
		// legitimate outcome, not a caller-actionable failure — mirrors the
		// top-of-function guard's own "already terminal" no-op.
		return nil
	default:
		return fmt.Errorf("steer: complete: persist %q: %w", rec.SessionID, mutateErr)
	}
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

// hasRunningOrQueuedDescendant reports whether parentID has any descendant
// that is genuinely still working — a `queued` child (admitted or not, it
// WILL run) or a `running` child with a live turn actually in flight.
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
// TASK-ORIGIN EXCLUSION (round-2 correctness fix, coordinator-caught):
// al.getActiveTurnState is keyed by turnState.sessionKey, which is NOT
// always the LifecycleRecord's own SessionID. steer_launcher.go::
// dispatchSteeredSessionWithReservation's task-origin branch
// (rec.Origin.Kind == session.OriginKindTask) never calls
// registerTurnIfAbsent at all — it hands off to
// TaskExecutor.dispatchLaunchedTask, which runs the turn through
// processTaskDirect/processTaskDirectExternalCLI
// (task_executor_run.go) under sessionKey =
// fmt.Sprintf("agent:%s:task:%s", t.AgentID, t.ID) — a DIFFERENT string
// than the child's own SessionID. So for a task-origin child,
// getActiveTurnState(child.SessionID) returns nil UNCONDITIONALLY, for
// the entire lifetime of its first (and typically only) turn, whether or
// not that turn is genuinely still executing — not a narrow race window,
// a permanent miss. Treating that nil as "idle" would let a parent
// complete WHILE its task-origin child is still genuinely running,
// producing a silently wrong/premature answer — strictly worse than the
// hang this lane fixes. Reconstructing that "agent:...:task:..." key here
// to look it up under the right name was considered and rejected: the
// format string is owned by task_executor_run.go (outside this lane's 3
// owned files) and duplicating it as a correctness-critical signal would
// silently reopen this exact hole the moment that format ever changes,
// with no compiler or test in THIS package able to catch the drift.
// Every non-task-origin dispatch path this lane found (the generic
// steer_launcher.go Path B AND the wake/follow-up re-entry path,
// loop_inbound.go's processSteeredSystemWake) calls registerTurnIfAbsent
// BEFORE writing LifecycleRunning, using the record's own SessionID —
// getActiveTurnState is a reliable signal there, so only task-origin
// children fall back to the old, safe, unconditional-block rule. This
// means a task-origin child stuck `running` after its OWN tool-iteration
// notice still blocks its parent's completion (a narrower version of the
// original bug, scoped to that one origin kind) — the parent IS still
// correctly woken (message_inbox.go's classifyEnvelope fix is
// origin-agnostic), it just cannot yet complete past that child. Closing
// that fully needs task_executor_run.go to register under the child's
// own SessionID (or a new durable LifecycleRecord field) — both outside
// this lane's owned files; flagged in this lane's final report rather
// than worked around with a fragile duplicated string.
func (al *AgentLoop) hasRunningOrQueuedDescendant(parentID string) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
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
				taskOrigin := child.Origin != nil && child.Origin.Kind == session.OriginKindTask
				if taskOrigin {
					// No reliable liveness signal for this origin kind
					// (see the task-origin exclusion note above) — fall
					// back to the old, safe, unconditional block.
					return true, nil
				}
				if ts := al.getActiveTurnState(child.SessionID); ts != nil && ts.IsAlive() {
					return true, nil
				}
				// A `running` record with no live turn (e.g. a tool-
				// iteration lifecycle notice) is idle, not executing —
				// fall through and keep walking its own descendants
				// instead of blocking on it.
			}
			queue = append(queue, child.SessionID)
		}
	}
	return false, nil
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
