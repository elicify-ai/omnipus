// cancel_stop.go: exact-turn, non-terminal Stop within the shared cancel state machine.

package agent

import (
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// activeTurnForCancel keeps legacy routing-root cancellation separate from a
// Stop targeting a session's own turn. ScopeSelfOnly with a raw root ID is not
// sufficient: resolveInterruptAnchors also matches descendants' routing IDs.
func (al *AgentLoop) activeTurnForCancel(sessionID string, scope CancelScope) TurnCancelHook {
	if !scope.TurnOnly {
		return al.GetActiveTurnHookForSession(sessionID)
	}
	if ts := al.getActiveTurnState(sessionID); ts != nil && ts.IsAlive() {
		return ts
	}
	var found *turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if ok && ts.IsAlive() && ts.transcriptSessionID == sessionID {
			found = ts
			return false
		}
		return true
	})
	if found == nil {
		return nil
	}
	return found
}

func (rc *agentLoopRequestCancel) validateStopGeneration() (CancelOutcome, error, bool) {
	if !rc.scope.TurnOnly || rc.scope.Generation == 0 {
		return CancelOutcome{}, nil, false
	}
	store := rc.al.GetSessionLifecycleStore()
	if store == nil {
		return CancelOutcome{}, fmt.Errorf("Stop: lifecycle store is not configured"), true
	}
	rec, err := store.Load(rc.sessionID)
	if err != nil {
		return CancelOutcome{}, err, true
	}
	if rec.Generation > rc.scope.Generation {
		return CancelOutcome{SkippedNewerGeneration: true}, nil, true
	}
	if rec.Generation != rc.scope.Generation || rec.Stop == nil || rec.Stop.Generation != rc.scope.Generation {
		return CancelOutcome{}, fmt.Errorf("Stop: session %q no longer carries generation %d's Stop", rc.sessionID, rc.scope.Generation), true
	}
	return CancelOutcome{}, nil, false
}

func (ts *turnState) claimCancel(stopOnly bool) bool {
	ts.cancelMu.Lock()
	defer ts.cancelMu.Unlock()
	if ts.cancelFired.Load() {
		return false
	}
	// Publish Stop semantics BEFORE any cancellation can wake the disposal
	// tail. Administrative cancellation continues to end session-owned goals.
	if stopOnly {
		ts.stopRequested.Store(true)
	}
	ts.cancelFired.Store(true)
	return true
}

// removeSelectedStopAdmission drops only the queued admission named by the
// stop effect this TurnOnly Stop accepted. A missing record is an ordinary
// chat with nothing in the steer queue. Any other lookup failure is returned.
func (rc *agentLoopRequestCancel) removeSelectedStopAdmission() error {
	if rc.al.GetSessionLifecycleStore() == nil || rc.sessionID == "" {
		return nil
	}
	effects, err := rc.al.stopEffectsForCallback(rc.sessionID, rc.scope.Generation)
	if err != nil {
		if errors.Is(err, session.ErrLifecycleNotFound) {
			return nil
		}
		return err
	}
	rc.al.removeQueuedStopEffects(rc.sessionID, effects)
	return nil
}

func (rc *agentLoopRequestCancel) claimActiveTurn() bool {
	if rc.activeTurn == nil {
		return false
	}
	if !rc.scope.TurnOnly {
		return rc.activeTurn.ClaimCancel()
	}
	ts, ok := rc.activeTurn.(*turnState)
	if !ok {
		return false
	}
	ts.mu.RLock()
	generation := ts.generation
	ts.mu.RUnlock()
	// Ordinary root turns have no reconstructed generation (zero). Their
	// durable generation is checked by validateStopGeneration instead.
	if rc.scope.Generation > 0 && generation > rc.scope.Generation {
		rc.skippedNewerGeneration = true
		return false
	}
	return ts.claimCancel(true)
}

func (rc *agentLoopRequestCancel) cancelTurnIDs() []string {
	if rc.scope.TurnOnly {
		return []string{rc.activeTurn.TurnID()}
	}
	return rc.al.collectDescendantTurnIDs(rc.sessionID)
}

func (rc *agentLoopRequestCancel) liveCancelTargets() []*turnState {
	if !rc.scope.TurnOnly {
		return rc.al.liveTurnStatesAmong(rc.al.collectDescendantTurnIDs(rc.sessionID))
	}
	// Capture identity, not a session-key re-scan. Neither a descendant nor a
	// replacement turn may inherit this Stop's hard/detach timers.
	if ts, ok := rc.activeTurn.(*turnState); ok && ts.IsAlive() {
		return []*turnState{ts}
	}
	return nil
}

func (rc *agentLoopRequestCancel) interruptCancelTargets(hard bool) ([]string, error) {
	if !rc.scope.TurnOnly {
		if hard {
			return rc.al.InterruptSessionHard(rc.sessionID, ScopeSubtree, rc.hint)
		}
		return rc.al.Interrupt(rc.sessionID, ScopeSubtree, rc.hint)
	}
	var interrupted []string
	for _, ts := range rc.liveCancelTargets() {
		ts.cancelling.Store(true)
		interrupted = append(interrupted, ts.turnID)
		if hard {
			ts.requestHardAbort()
			rc.al.emitEvent(EventKindInterruptReceived,
				ts.eventMeta("InterruptSessionHard", "turn.interrupt.received"),
				InterruptReceivedPayload{Kind: InterruptKindHard})
			continue
		}
		ts.mu.RLock()
		cancel := ts.providerCancel
		ts.mu.RUnlock()
		if cancel != nil {
			cancel()
		}
		if ts.requestGracefulInterrupt(rc.hint) {
			rc.al.emitEvent(EventKindInterruptReceived,
				ts.eventMeta("Interrupt", "turn.interrupt.received"),
				InterruptReceivedPayload{Kind: InterruptKindGraceful, HintLen: len(rc.hint)})
		}
	}
	return interrupted, nil
}
