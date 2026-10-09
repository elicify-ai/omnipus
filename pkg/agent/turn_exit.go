// turn_exit.go: Typed turn exits — how a turn ends, its end status and result

package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ClaimCancel performs the atomic first-cancel-wins check under cancelMu.
// Returns true if this call successfully set cancelFired from false→true.
func (ts *turnState) ClaimCancel() bool {
	return ts.claimCancel(false)
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
// projected. A failed restore retains the undo records for a later attempt.
func (ts *turnState) revertEmptiedTranscript() error {
	// Serialize restoration with undo-list updates. Failed I/O retains the
	// exact list, and concurrent restores cannot consume the same prefix twice.
	ts.mu.Lock()
	defer ts.mu.Unlock()
	prev := append([]session.ToolCallProjectionUpdate(nil), ts.emptiedTranscriptPrev...)
	if len(prev) == 0 || ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return nil
	}
	// Undo newest change first. A result shortened repeatedly in one turn
	// must finish on its exact original result, error and projection state.
	for i, j := 0, len(prev)-1; i < j; i, j = i+1, j-1 {
		prev[i], prev[j] = prev[j], prev[i]
	}
	if _, err := ts.transcriptStore.UpdateToolCallProjections(ts.transcriptSessionID, prev); err != nil {
		return fmt.Errorf("context rollback: transcript projection restore: %w", err)
	}
	ts.emptiedTranscriptPrev = ts.emptiedTranscriptPrev[len(prev):]
	return nil
}

func (ts *turnState) restoreSession(agent *AgentInstance) error {
	if ts.opts.NoHistory {
		return nil
	}
	ts.mu.RLock()
	start, captureErr := ts.initialWindow, ts.windowError
	ts.mu.RUnlock()
	if start == nil {
		if captureErr != nil {
			return captureErr
		}
		return fmt.Errorf("context rollback: starting window snapshot is missing")
	}
	store, ok := agent.Sessions.(session.ContextWindowStore)
	if !ok {
		return fmt.Errorf("context rollback: session store does not support atomic context checkpoints")
	}
	// The aborted turn's appended content must be physically REMOVED, not merely
	// de-windowed, so the next turn's model window cannot see a failed turn's
	// partial output. That removal is the canonical append-undo
	// (RollbackAppended, ADR-066 FR-020): it truncates the archive to the
	// turn-start length and restores meta.Skip and the turn-start projection set
	// in one write. RollbackWindow can no longer do this — session-core FR-006
	// makes it a window-metadata-only restore that never rewrites retained bytes
	// (contract: pkg/memory/window.go::RollbackWindow).
	//
	// targetSkip is the turn-start Skip: initialArchiveLen records were on disk
	// when the turn began, initialHistoryLength of them were in the live window
	// (turn.go captures both).
	targetSkip := ts.initialArchiveLen - ts.initialHistoryLength
	if targetSkip < 0 {
		targetSkip = 0
	}
	agent.Sessions.RollbackAppended(ts.sessionKey, ts.initialArchiveLen, targetSkip, ts.initialEmptiedSet)
	// Restore the exact turn-start window metadata (cursor, anchor and source
	// limits) captured in the snapshot.
	if err := store.RollbackWindow(context.Background(), ts.sessionKey, start.Clone()); err != nil {
		return err
	}
	// RollbackAppended is fire-and-forget on the SessionStore seam (it logs its
	// own I/O failure). Re-read so a failed rollback surfaces as a real error
	// instead of proceeding as if the turn-start window were restored — the
	// aborted-turn bytes must never silently survive into the next turn.
	archived, err := agent.Sessions.ReadArchive(context.Background(), ts.sessionKey)
	if err != nil {
		return fmt.Errorf("context rollback: verify archive after rollback: %w", err)
	}
	if len(archived) != ts.initialArchiveLen {
		return fmt.Errorf("context rollback: archive holds %d records after rollback, expected %d", len(archived), ts.initialArchiveLen)
	}
	if err := ts.revertEmptiedTranscript(); err != nil {
		return err
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
	//
	// The steered-turn admission slot is NOT released here. Every holder of
	// one releases it explicitly, at the site that claimed it — the delegate
	// front in steer_launcher.go::dispatchSteeredSessionWithReservation's
	// dispatch goroutine, the task front through the callback handed to
	// task_executor.go::dispatchLaunchedTask. Finish has no reliable
	// back-reference to release through (ts.al is unset for every steered
	// turn), so a drain here reads as the release mechanism while freeing
	// nothing.
	ts.closeOnce.Do(func() {
		if ch != nil {
			close(ch)
		}
	})

	// Cancel the turn context
	if ts.cancelFunc != nil {
		ts.cancelFunc()
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
