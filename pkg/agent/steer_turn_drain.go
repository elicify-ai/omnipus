// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

var (
	errSteeredDrainStopped  = errors.New("steer: post-turn drain: session stopped")
	errSteeredDrainInactive = errors.New("steer: post-turn drain: generation is no longer active")
)

type steeredContinuationAttempt struct {
	ts      *turnState
	result  turnResult
	runErr  error
	turnRan bool
}

// continueSteeredTurnBeforeRunTestHook is a test-only synchronization seam,
// fired after the continuation is registered and its lifecycle generation is
// rechecked, immediately before runTurn. It is nil in production.
var continueSteeredTurnBeforeRunTestHook func(sessionID string, generation int)

// drainSteeredTurn consumes steering that arrived after runTurn's final poll.
// It keeps the admission slot and lifetime context owned by the original
// dispatch, but reconstructs a fresh turnState for every continuation. The
// caller performs the completion tail once, after this returns, using the
// final attempted turn's state/result/error.
func (al *AgentLoop) drainSteeredTurn(
	ctx context.Context,
	snapshot *session.LifecycleRecord,
	initialTS *turnState,
	initialResult turnResult,
	initialErr error,
) (*turnState, turnResult, error) {
	lastTS, lastResult, lastErr := initialTS, initialResult, initialErr
	// Founder Q2=A: this bounded drain is the original admitted job. Keep
	// its selected immutable claim, even if the session now has a replacement.
	claim := al.tsExecutionClaim(initialTS, "")
	if snapshot == nil {
		return lastTS, lastResult, lastErr
	}

	if _, stateErr := al.steeredDrainRecord(snapshot.SessionID, snapshot.Generation, claim); stateErr != nil {
		switch {
		case errors.Is(stateErr, errSteeredDrainStopped):
			return lastTS, lastResult, context.Canceled
		case errors.Is(stateErr, errSteeredDrainInactive):
			return lastTS, lastResult, lastErr
		}
	}

	for {
		if al.steering == nil || al.steering.scopeEmpty(snapshot.SessionID) {
			return lastTS, lastResult, lastErr
		}
		var attempted steeredContinuationAttempt
		continued, attempts, continueErr := retrySteeringContinuation(ctx, func() (string, error) {
			var err error
			attempted, err = al.continueSteeredTurn(ctx, snapshot.SessionID, snapshot.Generation, claim)
			return attempted.result.finalContent, err
		}, func(err error) bool {
			return errors.Is(err, errContinuePostDequeueFailure) ||
				errors.Is(err, errSteeredDrainStopped) ||
				errors.Is(err, errSteeredDrainInactive)
		})

		if attempted.turnRan {
			lastTS, lastResult, lastErr = attempted.ts, attempted.result, attempted.runErr
		}
		if continueErr != nil {
			switch {
			case errors.Is(continueErr, errSteeredDrainStopped):
				return lastTS, lastResult, context.Canceled
			case errors.Is(continueErr, errSteeredDrainInactive):
				return lastTS, lastResult, lastErr
			}
			// A Stop may have landed while a tool-capable continuation was
			// unwinding. Its restored queue belongs to a future revival and
			// must not be abandoned as an ordinary continuation failure.
			if _, stateErr := al.steeredDrainRecord(snapshot.SessionID, snapshot.Generation, claim); errors.Is(stateErr, errSteeredDrainStopped) {
				return lastTS, lastResult, context.Canceled
			}
			al.abandonSteeredQueuedSteering(lastTS, snapshot.SessionID, continueErr, attempts)
			return lastTS, lastResult, continueErr
		}
		if continued == "" {
			continue
		}
	}
}

// continueSteeredTurn adapts the shared Continue dequeue/restore machinery to
// a reconstructed steered turn so the full turnResult remains available to
// the single completion tail. It refuses a stopped, terminal or superseded
// generation both before reconstruction and immediately before runTurn.
func (al *AgentLoop) continueSteeredTurn(
	ctx context.Context,
	sessionID string,
	generation int,
	claim executionClaim,
) (attempt steeredContinuationAttempt, err error) {
	rec, err := al.steeredDrainRecord(sessionID, generation, claim)
	if err != nil {
		return attempt, err
	}
	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		return attempt, err
	}
	attempt.ts = ts

	_, err = al.continuePendingSteeringWithAgent(ctx, sessionID, ts.agent,
		func(_ *AgentInstance, steeringMsgs []providers.Message, steeringCorrelationIDs []string) (string, error) {
			gate := al.steerAdmission()
			gate.entryMu.Lock()
			ts.opts.executionDisposition = al.executionDispositionFor(claim)
			if rec.SteeredBy != nil || ts.opts.executionDisposition != nil {
				if identityErr := ts.setExecutionIdentity(claim.RunID, claim.BootSeq); identityErr != nil {
					gate.entryMu.Unlock()
					return "", identityErr
				}
			}
			registered := al.registerTurnIfAbsent(ts)
			gate.entryMu.Unlock()
			if !registered {
				return "", fmt.Errorf("steer: post-turn drain: session %q already has an active turn", sessionID)
			}
			if _, stateErr := al.steeredDrainRecord(sessionID, generation, claim); stateErr != nil {
				al.clearActiveTurnStateEntry(sessionID, ts)
				return "", stateErr
			}
			if rec.SteeredBy != nil {
				if _, stateErr := commitSteeredExecutionState(al.GetSessionLifecycleStore(), claim, session.LifecycleRunning, ""); stateErr != nil {
					al.clearActiveTurnStateEntry(sessionID, ts)
					if _, currentErr := al.steeredDrainRecord(sessionID, generation, claim); currentErr != nil {
						return "", currentErr
					}
					return "", stateErr
				}
			}
			if continueSteeredTurnBeforeRunTestHook != nil {
				continueSteeredTurnBeforeRunTestHook(sessionID, generation)
			}
			ts.opts.UserMessage = ""
			ts.userMessage = ""
			ts.opts.UserInitiated = false
			ts.opts.InitialSteeringMessages = steeringMsgs
			ts.opts.InitialSteeringCorrelationIDs = steeringCorrelationIDs
			ts.opts.SkipInitialSteeringPoll = true
			attempt.turnRan = true
			attempt.result, attempt.runErr = al.runTurn(ctx, ts)
			return attempt.result.finalContent, attempt.runErr
		})
	return attempt, err
}

func (al *AgentLoop) steeredDrainRecord(sessionID string, generation int, claim executionClaim) (*session.LifecycleRecord, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil, errors.New("steer: post-turn drain: lifecycle store is not wired")
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return nil, fmt.Errorf("steer: post-turn drain: load %q: %w", sessionID, err)
	}
	if rec.Generation != generation || rec.Terminal() {
		return rec, errSteeredDrainInactive
	}
	if claim.RunID != "" && !claim.matches(rec) {
		return rec, errSteeredDrainInactive
	}
	if rec.Stopped() {
		return rec, errSteeredDrainStopped
	}
	return rec, nil
}

// abandonSteeredQueuedSteering is the steered-session counterpart of
// sessionWorker.abandonQueuedSteering: dequeue-and-report after bounded
// retries, while preserving wake-bearing queue items if this narrow failure
// path panics. Each abandoned item is reported in the child's transcript and
// in a distinct parent-scoped subagent_message error frame; a steered child
// has no direct user reply address. The operator log retains the underlying
// error and retry count.
func (al *AgentLoop) abandonSteeredQueuedSteering(
	ts *turnState,
	sessionID string,
	lastErr error,
	attempts int,
) {
	queueDepthBefore := al.pendingSteeringCountForScope(sessionID)
	var abandonedItems []steeringQueueItem
	var abandonedScope string
	defer func() {
		if recovered := recover(); recovered != nil {
			if len(abandonedItems) > 0 {
				al.steering.prependItemsScope(abandonedScope, abandonedItems)
			}
			logger.ErrorCF("agent", "steer: panic while abandoning queued steering — restored to queue",
				map[string]any{"session_id": sessionID, "panic": recovered, "stack": string(debug.Stack())})
			panic(recovered)
		}
	}()

	if al.steering != nil {
		abandonedScope, abandonedItems = al.steering.drainAllScope(sessionID)
	}
	logger.ErrorCF("agent", "steer: persistent Continue failure — abandoning queued steering",
		map[string]any{
			"session_id":  sessionID,
			"queue_depth": queueDepthBefore,
			"attempts":    attempts,
			"error":       lastErr.Error(),
		})
	const notice = "A queued follow-up message could not be processed and was not delivered. Please send it again."
	var childRec *session.LifecycleRecord
	if len(abandonedItems) > 0 {
		if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil {
			var err error
			childRec, err = lifecycle.Load(sessionID)
			if err != nil {
				logger.ErrorCF("agent", "steer: cannot locate parent for abandoned steering",
					map[string]any{"session_id": sessionID, "error": err.Error()})
			}
		} else {
			logger.ErrorCF("agent", "steer: cannot locate parent for abandoned steering — lifecycle store not wired",
				map[string]any{"session_id": sessionID})
		}
	}
	parentID := steerParentSessionID(childRec)
	for i, item := range abandonedItems {
		if ts != nil {
			ts.appendClassifiedError(EventKindError.String(), "steering_continue", LLMError{
				Code:    CodeUnknown,
				Message: notice,
			})
		}
		if parentID == "" {
			logger.ErrorCF("agent", "steer: abandoned steering has no parent to notify",
				map[string]any{"session_id": sessionID, "item_number": i + 1})
			continue
		}
		itemID := item.correlationID
		if item.wake != nil {
			itemID = item.wake.messageID
		}
		if itemID == "" {
			itemID = fmt.Sprintf("item %d", i+1)
		}
		al.deliverSubagentMessage(parentID, childRec, "error",
			fmt.Sprintf("Queued follow-up %q: %s", itemID, notice), nil)
	}
}
