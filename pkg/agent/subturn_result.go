// subturn_result.go: Collect, format and return a sub-turn's result to the parent.

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// subTurnForceCancel is one sub-turn's time limit, enforced as a hard abort.
type subTurnForceCancel struct {
	timer *time.Timer
	done  chan struct{}
	fired atomic.Bool
}

// armSubTurnForceCancel schedules a force-cancel of childTS after `after`.
//
// Documented intent (pkg/tools/delegate.go DelegateTool.Description and the
// timeout_seconds schema): "A delegation is force-cancelled after
// timeout_seconds ... if it has not finished by then." A force-cancel must
// therefore stop the child the way a hard cancel does. requestHardAbort sets
// turnState.hardAbort under ts.mu BEFORE it fires turnCancel/providerCancel, so
// a tool call that returns because its context ended finds
// hardAbortRequested() already true, and runTurn's tool loop aborts instead of
// dispatching the next queued call. runTurn's deferred
// Finish(hardAbortRequested()) then cascades the hard abort to the child's own
// sub-turns.
//
// A plain context deadline gave none of that: the loop's only pre-dispatch
// stop check is hardAbortRequested(), and ToolRegistry.ExecuteWithContext does
// not check ctx before Execute, so a child whose deadline expired during one
// tool call ran the next queued call — a file write — after its delegator had
// been told it failed (TestSubTurn_TimedOutChild_StartsNoFurtherToolCalls).
//
// If a hard abort was already requested (a user or parent cancel won the
// race), the timer records nothing and the outcome stays that cancellation.
func armSubTurnForceCancel(childTS *turnState, after time.Duration) *subTurnForceCancel {
	fc := &subTurnForceCancel{done: make(chan struct{})}
	fc.timer = time.AfterFunc(after, func() {
		defer close(fc.done)
		if childTS.requestHardAbort() {
			fc.fired.Store(true)
		}
	})
	return fc
}

// disarm stops the timer and reports whether the force-cancel fired. When the
// callback has already started, disarm waits for it to finish so the answer is
// final: callers classify the sub-turn's outcome from this value, and a
// callback still in flight could otherwise flip it after they read it.
func (fc *subTurnForceCancel) disarm() bool {
	if fc.timer.Stop() {
		return false
	}
	<-fc.done
	return fc.fired.Load()
}

// subTurnTimedOutResult builds what spawnSubTurn returns for a sub-turn whose
// force-cancel either stopped it or reached the detach ceiling while an
// already-running operation ignored cancellation. The error wraps
// tools.ErrDelegationTimedOut (the discriminator pkg/tools/delegate.go reports
// from), ErrTurnTimedOut and
// context.DeadlineExceeded, so TranslateTurnError and every existing
// errors.Is(err, context.DeadlineExceeded) caller still classify it as a
// timeout. cause — what the dispatch itself returned, typically nil or
// context.Canceled from the hard abort — is logged, never wrapped: wrapping a
// context.Canceled would make a timeout read as a user cancellation downstream.
//
// It also records the timeout in the child's own transcript and on the event
// bus when the turn's own exit did not already (a hard abort exits through
// abortTurn's Case 1, which deliberately records nothing), so the child's
// session shows why it stopped — and says so truthfully: the delegation's time
// limit, not the model provider.
func subTurnTimedOutResult(
	al *AgentLoop,
	childTS *turnState,
	limit time.Duration,
	cause error,
	detached bool,
) (*tools.ToolResult, error) {
	err := fmt.Errorf("%w: reached its %s time limit and was force-cancelled (%w: %w)",
		tools.ErrDelegationTimedOut, limit, ErrTurnTimedOut, context.DeadlineExceeded)
	if detached {
		err = fmt.Errorf("%w: reached its %s time limit (%w: %w)",
			tools.ErrDelegationDetached, limit, ErrTurnTimedOut, context.DeadlineExceeded)
	}
	message := fmt.Sprintf("This delegated task reached its %s time limit and was force-cancelled. "+
		"It made no further tool calls or changes after that point.", limit)
	resultMessage := fmt.Sprintf("SubTurn timed out: it reached its %s time limit and was force-cancelled. "+
		"It is stopped and will make no further tool calls or changes; work it completed before the "+
		"limit may remain.", limit)
	if detached {
		message = fmt.Sprintf("This delegated task reached its %s time limit, ignored cancellation, and was detached. "+
			"The parent stopped waiting. No new model or tool call will be dispatched, but the already-running "+
			"operation may still be unwinding.", limit)
		resultMessage = fmt.Sprintf("SubTurn timed out: it reached its %s time limit, ignored cancellation, and "+
			"was detached. The parent stopped waiting. No new model or tool call will be dispatched, but the "+
			"already-running operation may still be unwinding; work completed before the limit may remain.", limit)
	}
	slog.Warn("subturn: reached its time limit",
		"child_turn_id", childTS.turnID,
		"agent_id", childTS.agentID,
		"limit", limit,
		"detached", detached,
		"exit_cause", cause,
	)

	if !errors.Is(cause, ErrTurnTimedOut) {
		llm := TranslateTurnError(err)
		llm.Message = message
		// The same error frame and transcript record typedTurnExit writes for
		// a turn that timed out on its own, through the same emitter: before
		// the force-cancel existed, a timed-out child exited through
		// typedTurnExit, and this is that record with truthful wording. The
		// frame's session id is stamped inside emitTurnErrorFrame (loop.go),
		// so this function never reads routingSessionID and adds no consumer
		// to ADR-057 FR-014's closed set.
		meta := childTS.eventMeta("spawnSubTurn", "subturn.force_cancel")
		if detached {
			al.emitDetachedTurnErrorFrame(childTS, meta, "subturn_timeout", "subturn_timeout", llm)
		} else {
			al.emitTurnErrorFrame(childTS, meta, "subturn_timeout", "subturn_timeout", llm)
		}
	}

	return &tools.ToolResult{
		Err:     err,
		ForLLM:  resultMessage,
		IsError: true,
	}, err
}

// updateToolCallStatusRetryDelays is the bounded backoff schedule
// updateToolCallStatusWithRetry uses when retrying for async delegation.
// Total budget: ~935ms across 6 retries (after the first, immediate
// attempt) — comfortably covering the parent's own tool-result
// post-processing (hooks/media/events) between ExecuteWithContext returning
// and ts.appendToolCallTranscript actually persisting the placeholder ack,
// without meaningfully delaying anything user-visible (this all runs inside
// spawnSubTurn's cleanup defer, on the child sub-turn's OWN background
// goroutine — never blocking the parent's turn).
var updateToolCallStatusRetryDelays = []time.Duration{
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
}

// updateToolCallStatusSleep is the sleep primitive updateToolCallStatusWithRetry
// uses for its backoff loop. It is a package var (not a direct time.Sleep call)
// purely as a test seam: production always leaves it as time.Sleep, but a test
// can swap in a counting/no-op stand-in to assert on ATTEMPT COUNT — the real
// property ("did it retry at all?") — instead of racing a wall-clock margin
// against machine load. See subturn_ack_race_test.go's
// TestUpdateToolCallStatusWithRetry_FoundOnFirstAttemptSkipsRetry for why a
// timing-based proxy for this was a false-signal generator in both directions.
var updateToolCallStatusSleep = time.Sleep

// updateToolCallStatusWithRetry calls store.UpdateToolCallStatusAndResult,
// retrying with updateToolCallStatusRetryDelays's bounded backoff when async
// is true and the first attempt finds no matching record yet (found=false,
// err=nil). result is forwarded as-is (nil is valid and leaves the existing
// ToolCall.Result untouched — see UpdateToolCallStatusAndResult's doc
// comment); passing the delegate sub-turn's own result here (W4) is what lets
// a session reload's delegate tool_call entry carry the sub-turn's outcome
// instead of an empty `result`.
//
// FIX 4 (confirmed independently by silent-failure-hunter, architect, and
// code-reviewer): DelegateTool.executeAsync (pkg/tools/delegate.go) launches
// the child sub-turn in a goroutine and returns to the parent turn
// immediately; the PARENT writes this SAME tool call's own placeholder ack
// record only after further processing (hooks, media, events) in its own
// call stack, back in pkg/agent/loop.go's tool-execution loop. When the
// child's dispatch fails fast (e.g. a depth-limit or target-resolution
// rejection), spawnSubTurn's cleanup defer — this function's only caller —
// can reach UpdateToolCallStatus BEFORE the parent's placeholder write
// lands, and UpdateToolCallStatus's "not found" case was previously
// (incorrectly, for this specific caller) treated as a terminal, expected
// no-op. Retrying establishes REAL happens-before ordering without
// requiring pkg/tools (which deliberately has no transcript-store access —
// SubTurnSpawner/AsyncExecutor exist specifically to avoid an agent<->tools
// import cycle) to change at all.
//
// For SYNCHRONOUS delegation (async=false), found=false on the first
// attempt is the documented, PERMANENT, expected outcome — the caller
// (DelegateTool.executeSync, via the tool-execution loop) will append the
// record itself moments later, once spawnSubTurn returns with the real
// result. Retrying in that case would only waste the full backoff budget on
// every synchronous delegation for no benefit, so this makes a single,
// no-retry attempt when async is false.
func updateToolCallStatusWithRetry(
	store *session.UnifiedStore,
	sessionID string,
	toolCallID session.ToolCallID,
	status string,
	durationMS int64,
	async bool,
	result map[string]any,
) (found bool, err error) {
	found, err = store.UpdateToolCallStatusAndResult(sessionID, toolCallID, status, durationMS, result)
	if err != nil || found || !async {
		return found, err
	}
	for _, delay := range updateToolCallStatusRetryDelays {
		updateToolCallStatusSleep(delay)
		found, err = store.UpdateToolCallStatusAndResult(sessionID, toolCallID, status, durationMS, result)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// ====================== Result Delivery ======================

// deliverSubTurnResult delivers a sub-turn result to the parent turn's pendingResults channel.
//
// IMPORTANT: This function is ONLY called for asynchronous sub-turns (Async=true).
// For synchronous sub-turns (Async=false), results are returned directly via the function
// return value to avoid double delivery.
//
// Delivery behavior:
//   - If parent turn is still running: attempts to deliver to pendingResults channel
//   - If channel is full: emits SubTurnOrphanResultEvent (result is lost from channel but tracked)
//   - If parent turn has finished: emits SubTurnOrphanResultEvent (late arrival)
//
// Thread safety:
//   - Reads parent state under lock, then releases lock before channel send
//   - Small race window exists but is acceptable (worst case: result becomes orphan)
//
// Event emissions:
//   - SubTurnResultDeliveredEvent: successful delivery to channel
//   - SubTurnOrphanResultEvent: delivery failed (parent finished or channel full)
func deliverSubTurnResult(al *AgentLoop, parentTS *turnState, childID string, result *tools.ToolResult) {
	// Let GC clean up the pendingResults channel; parent Finish will no longer close it.
	// We use defer/recover to catch any unlikely channel panics if it were ever closed.
	defer func() {
		if r := recover(); r != nil {
			logger.WarnCF("subturn", "recovered panic sending to pendingResults", map[string]any{
				"parent_id": parentTS.turnID,
				"child_id":  childID,
				"recover":   r,
			})
			if result != nil && al != nil {
				al.emitEvent(EventKindSubTurnOrphan,
					parentTS.eventMeta("deliverSubTurnResult", "subturn.orphan"),
					SubTurnOrphanPayload{ParentTurnID: parentTS.turnID, ChildTurnID: childID, Reason: "panic"},
				)
			}
		}
	}()
	parentTS.mu.Lock()
	isFinished := parentTS.isFinished.Load()
	resultChan := parentTS.pendingResults
	parentTS.mu.Unlock()

	// If parent turn has already finished, treat this as an orphan result
	if isFinished || resultChan == nil {
		if result != nil && al != nil {
			al.emitEvent(EventKindSubTurnOrphan,
				parentTS.eventMeta("deliverSubTurnResult", "subturn.orphan"),
				SubTurnOrphanPayload{ParentTurnID: parentTS.turnID, ChildTurnID: childID, Reason: "parent_finished"},
			)
		}
		return
	}

	// Parent Turn is still running → attempt to deliver result
	// We use a select statement with parentTS.Finished() to ensure that if the
	// parent turn finishes while we are waiting to send the result (e.g. channel
	// is full), we don't leak this goroutine by blocking forever.
	select {
	case resultChan <- result:
		// Successfully delivered
		if al != nil {
			al.emitEvent(EventKindSubTurnResultDelivered,
				parentTS.eventMeta("deliverSubTurnResult", "subturn.result_delivered"),
				SubTurnResultDeliveredPayload{ContentLen: len(result.ForLLM)},
			)
		}
	case <-parentTS.Finished():
		// Parent finished while we were waiting to deliver.
		// The result cannot be delivered to the LLM, so it becomes an orphan.
		logger.WarnCF("subturn", "parent finished before result could be delivered", map[string]any{
			"parent_id": parentTS.turnID,
			"child_id":  childID,
		})
		if result != nil && al != nil {
			al.emitEvent(
				EventKindSubTurnOrphan,
				parentTS.eventMeta("deliverSubTurnResult", "subturn.orphan"),
				SubTurnOrphanPayload{
					ParentTurnID: parentTS.turnID,
					ChildTurnID:  childID,
					Reason:       "parent_finished_waiting",
				},
			)
		}
	}
}
