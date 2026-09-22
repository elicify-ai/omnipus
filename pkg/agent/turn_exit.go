// turn_exit.go: Typed turn exits — how a turn ends, its end status and result

package agent

import (
	"context"
	"fmt"
	"math"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ClaimCancel performs the atomic first-cancel-wins check under cancelMu.
// Returns true if this call successfully set cancelFired from false→true.
func (ts *turnState) ClaimCancel() bool {
	ts.cancelMu.Lock()
	defer ts.cancelMu.Unlock()
	if ts.cancelFired.Load() {
		return false
	}
	ts.cancelFired.Store(true)
	return true
}

// MarkAbandoned sets the abandoned flag when a controller stops waiting for a
// turn goroutine that ignored hard abort. Callers include the gateway cancel
// watchdog and the delegated-turn timeout detach path.
func (ts *turnState) MarkAbandoned() {
	ts.abandoned.Store(true)
}

// markTurnFailed records that this turn did NOT end in a real, successful model
// response. It is called from four sites in loop.go: (1) empty-response-after-
// retry, (2) tool-iteration limit, (3) generic empty-content exhaustion when the
// caller's DefaultResponse equals the engine's own error sentinel (never when a
// success message is supplied), and (4) runTurn's turn-end defer, whenever
// turnStatus == TurnEndStatusError — the catch-all that covers every error
// return path, including the LLM-error early return that a provider refusal
// takes. See that defer's own comment for why the trigger is exactly
// TurnEndStatusError and not emptiness, and why its LIFO registration position
// relative to `defer ts.finalizeStreamer(ctx)` is load-bearing.
//
// The flag is read by finalizeStreamer and forwarded to the streamer's
// SetTurnFailed before Finalize so it can set DoneStats.TurnFailed on the done
// WS frame. Idempotent: sites (1)-(3) and (4) can both fire for the same turn.
func (ts *turnState) markTurnFailed() {
	ts.mu.Lock()
	ts.turnFailed = true
	ts.mu.Unlock()
}

func (ts *turnState) clearProviderCancel(_ context.CancelFunc) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.providerCancel = nil
}

func (ts *turnState) requestGracefulInterrupt(hint string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.hardAbort {
		return false
	}
	ts.gracefulInterrupt = true
	ts.gracefulInterruptHint = hint
	return true
}

func (ts *turnState) gracefulInterruptRequested() (bool, string) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.gracefulInterrupt && !ts.gracefulTerminalUsed, ts.gracefulInterruptHint
}

func (ts *turnState) markGracefulTerminalUsed() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.gracefulTerminalUsed = true
}

func (ts *turnState) requestHardAbort() bool {
	ts.mu.Lock()
	if ts.hardAbort {
		ts.mu.Unlock()
		return false
	}
	ts.hardAbort = true
	turnCancel := ts.turnCancel
	providerCancel := ts.providerCancel
	ts.mu.Unlock()

	if providerCancel != nil {
		providerCancel()
	}
	if turnCancel != nil {
		turnCancel()
	}
	return true
}

func (ts *turnState) hardAbortRequested() bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.hardAbort
}

// revertEmptiedTranscript puts back the PREVIOUS content_state / result of
// every transcript tool_call record the D5 pass rewrote during this turn
// (ADR-066 FR-020/FR-022): the window's projection set has just been rolled
// back to turn start, so the transcript must stop claiming those results are
// emptied. Best-effort; idempotent (the list is cleared once applied).
func (ts *turnState) revertEmptiedTranscript() {
	ts.mu.Lock()
	prev := ts.emptiedTranscriptPrev
	ts.emptiedTranscriptPrev = nil
	ts.mu.Unlock()
	if len(prev) == 0 || ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return
	}
	// Oldest row first: a record emptied twice in one turn (impossible today,
	// the pass skips already-emptied entries — defensive) must end on its
	// ORIGINAL state, so later rows are overwritten by earlier ones.
	for i, j := 0, len(prev)-1; i < j; i, j = i+1, j-1 {
		prev[i], prev[j] = prev[j], prev[i]
	}
	if _, err := ts.transcriptStore.UpdateToolCallProjections(ts.transcriptSessionID, prev); err != nil {
		logger.WarnCF("agent", "abort: transcript content_state revert failed",
			map[string]any{"session_id": ts.transcriptSessionID, "count": len(prev), "error": err.Error()})
	}
}

func (ts *turnState) restoreSession(agent *AgentInstance) error {
	ts.mu.RLock()
	targetLen := ts.initialArchiveLen
	initialHistLen := ts.initialHistoryLength
	ts.mu.RUnlock()

	// Compute the Skip value at turn start.
	// targetSkip = initialArchiveLen - initialHistoryLength derives the Skip
	// cursor that was in effect before this turn began. If windowTrim advanced
	// Skip mid-turn and the turn is now aborting, restoring Skip to this value
	// ensures GetHistory returns exactly the pre-turn live window (SC-001, SC-010).
	// Guard: when initialArchiveLen == math.MaxInt (ReadArchive failed at turn
	// start, rollback is a no-op), pass targetSkip=0 — it is irrelevant because
	// targetLen=MaxInt means RollbackAppended exits early before touching Skip.
	targetSkip := 0
	if targetLen != math.MaxInt {
		targetSkip = targetLen - initialHistLen
		if targetSkip < 0 {
			targetSkip = 0
		}
	}

	// Rollback: truncate the archive back to the line count captured at turn
	// start AND restore meta.Skip to its turn-start value so mid-turn evictions
	// are undone. SetHistory is explicitly NOT used here — it would overwrite
	// the entire JSONL file and reset Skip=0, permanently deleting any evicted
	// turns that preceded this turn (CRITICAL 1, path 2).
	//
	// The third member of the turn-start triple (ADR-066 FR-020) is the
	// projection set as of turn start (turnState.initialEmptiedSet): every
	// result this turn emptied is un-emptied in the same write, and entries
	// whose archive line is ≥ targetLen go with the truncated tail.
	agent.Sessions.RollbackAppended(ts.sessionKey, targetLen, targetSkip, ts.initialEmptiedSet)
	ts.revertEmptiedTranscript()

	// M4 mirror: verify the rollback actually took effect. RollbackAppended is
	// fire-and-forget (no error return). Re-read the archive and confirm the
	// length dropped to <= targetLen. Skip verification when targetLen is
	// math.MaxInt (ReadArchive failed at turn start — rollback was a no-op by
	// design, so there is nothing meaningful to verify).
	if targetLen != math.MaxInt {
		if postArchive, readErr := agent.Sessions.ReadArchive(context.Background(), ts.sessionKey); readErr == nil {
			if len(postArchive) > targetLen {
				logger.ErrorCF("agent", "restoreSession: rollback did not shrink archive to target",
					map[string]any{
						"session_key": ts.sessionKey,
						"target":      targetLen,
						"after":       len(postArchive),
					})
				return fmt.Errorf(
					"restoreSession: RollbackAppended did not take effect (archive len %d > target %d)",
					len(postArchive),
					targetLen,
				)
			}
		}
		// If ReadArchive itself fails here, we can't verify — fall through and
		// let Save() persist whatever state the backend is in. The caller
		// (interrupt handler) logs the restoreSession error if we return one.
	}

	return agent.Sessions.Save(ts.sessionKey)
}

func (ts *turnState) interruptHintMessage() providers.Message {
	_, hint := ts.gracefulInterruptRequested()
	content := "Interrupt requested. Stop scheduling tools and provide a short final summary."
	if hint != "" {
		content += "\n\nInterrupt hint: " + hint
	}
	return providers.Message{
		Role:    "user",
		Content: content,
	}
}

// SubTurn-related methods

// Finish marks the turn as finished and closes the pendingResults channel.
//
// When cancelFired is true (i.e. handleCancel claimed this turn), Finish
// invokes the onCancelFinish callback exactly once with the cancel method
// ("hard" when isHardAbort, "graceful" otherwise). The callback is called
// from within the goroutine that finishes the turn, so it must not block or
// call back into the agent loop with any locks held.
func (ts *turnState) Finish(isHardAbort bool) {
	if isHardAbort {
		ts.finishedByHardAbort.Store(true)
	}
	ts.isFinished.Store(true)

	// Ensure finishedChan exists before entering closeOnce.Do.
	// Finished() is the single point of lazy creation; calling it here
	// guarantees that Finish() and all Finished() callers share the
	// exact same channel instance, eliminating the race where closeOnce.Do
	// used to create a second channel that Finished() would never return.
	ch := ts.Finished()

	// Signal completion exactly once.
	//
	// pendingResults is intentionally NOT closed here. Closing it while
	// deliverSubTurnResult may hold a local copy of the channel reference and be
	// mid-select-send creates an unavoidable runtime-level race between
	// closechan() and chansend() that the race detector flags even when the
	// channel field itself is zeroed under a mutex. Instead we rely on the
	// finishedChan close as the sole stop signal; all consumers of pendingResults
	// use non-blocking select+default (loop.go) or select+Finished() (subturn.go)
	// so they never block waiting for a close. The channel is garbage-collected
	// once all references drop after the turn is finished.
	ts.closeOnce.Do(func() {
		if ch != nil {
			close(ch)
		}
	})

	// If this is a graceful finish (not hard abort), signal to children
	if !isHardAbort && ts.parentTurnState == nil {
		// This is a root turn finishing gracefully
		ts.parentEnded.Store(true)
	}

	// Cancel the turn context
	if ts.cancelFunc != nil {
		ts.cancelFunc()
	}

	// ADR-091 I-3/D9: this session's turn has ended — it holds no admission
	// slot any more (a waiting parent holding no slot is round 9's whole
	// point). A no-op for a non-steered turn (drainSteerQueue's release is a
	// no-op when this sessionKey was never admitted through the steered-turn
	// gate). al may be nil for an ad-hoc test turnState that skipped
	// newTurnState.
	if ts.al != nil {
		ts.al.drainSteerQueue(ts.sessionKey)
	}

	// Hard abort cascades to all child turns
	if isHardAbort && ts.al != nil {
		ts.mu.RLock()
		children := append([]string(nil), ts.childTurnIDs...)
		ts.mu.RUnlock()
		for _, childID := range children {
			if val, ok := ts.al.activeTurnStates.Load(childID); ok {
				childTS, ok := val.(*turnState)
				if !ok {
					logger.ErrorCF("agent", "activeTurnStates: invariant violated — unexpected value type, skipping child hard-abort cascade",
						map[string]any{"child_turn_id": childID, "got_type": fmt.Sprintf("%T", val)})
					continue
				}
				childTS.Finish(true)
			}
		}
	}

	// If handleCancel claimed this turn, fire the post-cancel callback exactly
	// once. Swap the callback to nil under the write-lock so that concurrent or
	// repeated Finish calls (e.g. runTurn's own deferred Finish call running
	// after an explicit Finish(true) from a hard abort) cannot invoke it a
	// second time (FR-15).
	if ts.cancelFired.Load() {
		ts.mu.Lock()
		cb := ts.onCancelFinish
		ts.onCancelFinish = nil
		ts.mu.Unlock()
		if cb != nil {
			method := "graceful"
			if isHardAbort {
				method = "hard"
			}
			cb(method)
		}
	}
}

// Finished returns a channel that is closed when the turn finishes.
// finishedChan is always initialized by newTurnState for production turns.
// For ad-hoc test struct literals that skip newTurnState, we lazily create
// the channel here under mu.Lock so that Finish() and all Finished() callers
// always share the same channel instance. The lazy-creation path is the
// single authoritative creator; Finish() calls Finished() before closing,
// ensuring no second channel can be created after the close.
func (ts *turnState) Finished() chan struct{} {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.finishedChan == nil {
		ts.finishedChan = make(chan struct{})
	}
	return ts.finishedChan
}

// IsParentEnded checks if the parent turn has ended
func (ts *turnState) IsParentEnded() bool {
	if ts.parentTurnState == nil {
		return false
	}
	return ts.parentTurnState.isFinished.Load()
}

// IsAlive returns true when the turn has not yet finished. This is the
// complement of isFinished and is used by the cancel watchdog timers in
// handleCancel to decide whether to escalate to the next stage.
func (ts *turnState) IsAlive() bool {
	return !ts.isFinished.Load()
}

// SetOnCancelFinish registers a callback that Finish() will invoke exactly
// once when cancelFired==true and the turn exits. The callback receives the
// cancel method ("graceful" or "hard"). Calling this more than once replaces
// the previous callback; it must be called before handleCancel calls Finish
// (i.e. before the timers fire).
func (ts *turnState) SetOnCancelFinish(fn func(cancelMethod string)) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.onCancelFinish = fn
}
