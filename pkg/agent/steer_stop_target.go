// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
)

type stopSelectionContextKey struct{}

func withStopSelection(ctx context.Context, selected session.StopSelection) context.Context {
	return context.WithValue(ctx, stopSelectionContextKey{}, selected)
}

func stopSelectionFromContext(ctx context.Context) (session.StopSelection, bool) {
	selected, ok := ctx.Value(stopSelectionContextKey{}).(session.StopSelection)
	return selected, ok
}

// stopSelectionForCallback requires the carried acceptance-time pair. Current
// record reads validate ownership only, never select a replacement target.
// Queue, handle and mutation cuts recheck their own immutable identities.
func (al *AgentLoop) stopSelectionForCallback(ctx context.Context, sessionID string, generation int) (session.StopSelection, bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return session.StopSelection{}, false, fmt.Errorf("steer: stop effect %q: lifecycle store is not wired", sessionID)
	}
	selected, carried := stopSelectionFromContext(ctx)
	if !carried || selected.SessionID != sessionID || selected.Effect.Target.Generation != generation || selected.Effect.ControlID == "" {
		return session.StopSelection{}, false, fmt.Errorf("steer: stop effect %q: callback does not carry its accepted execution/control pair", sessionID)
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return session.StopSelection{}, false, err
	}
	if !stopSelectionMatchesRecord(selected, rec) {
		return selected, false, nil
	}
	return selected, true, nil
}

func stopSelectionMatchesRecord(selected session.StopSelection, rec *session.LifecycleRecord) bool {
	if rec == nil || selected.SessionID != rec.SessionID || selected.Effect.ControlID == "" ||
		selected.Effect.Target.Generation != rec.Generation || rec.Stop == nil || rec.Stop.Generation != rec.Generation ||
		rec.StopEffect == nil || *rec.StopEffect != selected.Effect {
		return false
	}
	target := selected.Effect.Target
	if !target.Selected() {
		return target.BootSeq == 0 && rec.ExecutionID == nil
	}
	return claimForStopEffect(selected.SessionID, selected.Effect).matches(rec)
}

func claimForStopEffect(sessionID string, effect session.StopEffect) executionClaim {
	return executionClaim{
		SessionID: sessionID, Generation: effect.Target.Generation,
		RunID: effect.Target.RunID, BootSeq: effect.Target.BootSeq,
	}
}

// removeQueuedStopEffects compares the full selected admission under the gate
// lock. A never-admitted selection cannot remove any queued run. A stale
// effect (its fence already landed or was cleared by a Resume) still removes
// its OWN exact admission: that run can never be admitted again, and the
// full-identity match cannot reach a replacement's admission (D2/T27).
func (al *AgentLoop) removeQueuedStopEffects(sessionID string, effects []session.StopEffect) {
	gate := al.steerAdmission()
	for _, effect := range effects {
		if effect.ControlID != "" && effect.Target.RunID != "" && effect.Target.BootSeq != 0 {
			gate.removeQueuedExecution(claimForStopEffect(sessionID, effect))
		}
	}
}

// liveTurnMatchesStop checks the immutable handle itself, including session.
// No registry or turn lock remains held when the caller invokes that handle.
func liveTurnMatchesStop(ts *turnState, selected session.StopSelection) bool {
	if ts == nil || selected.Effect.ControlID == "" || selected.Effect.Target.RunID == "" || selected.Effect.Target.BootSeq == 0 {
		return false
	}
	ts.mu.RLock()
	sessionID := ts.sessionKey
	if ts.opts.executionDisposition != nil {
		sessionID = ts.opts.executionDisposition.claim.SessionID
	}
	claim := executionClaim{SessionID: sessionID, Generation: ts.generation, RunID: ts.executionRunID, BootSeq: ts.executionBootSeq}
	ts.mu.RUnlock()
	return claim == claimForStopEffect(selected.SessionID, selected.Effect)
}

// steerSoftStop is the cooperative effect of a selected durable Stop. It pins
// only that execution's immutable handle, then invokes its provider outside
// lifecycle/cascade/registry/turn locks. Queued-only stops use the same landing.
func (al *AgentLoop) steerSoftStop(ctx context.Context, sessionID string, generation int, hint string) (GenerationCancelResult, error) {
	selected, current, err := al.stopSelectionForCallback(ctx, sessionID, generation)
	if err != nil {
		return GenerationCancelResult{}, err
	}
	if !current {
		al.removeQueuedStopEffects(sessionID, []session.StopEffect{selected.Effect})
		return GenerationCancelResult{SkippedNewerGeneration: true}, nil
	}
	d, current, retainErr := al.retainSelectedStop(ctx, sessionID, generation, nil)
	if retainErr != nil {
		return GenerationCancelResult{}, retainErr
	}
	if !current {
		al.removeQueuedStopEffects(sessionID, []session.StopEffect{selected.Effect})
		return GenerationCancelResult{SkippedNewerGeneration: true}, nil
	}
	if d == nil {
		return GenerationCancelResult{}, nil
	}
	ts, _ := al.activeTurnForCancel(sessionID, CancelScope{SessionID: sessionID, TurnOnly: true}).(*turnState)
	if ts == nil {
		return GenerationCancelResult{Found: true, Cancelled: true}, nil
	}
	if !liveTurnMatchesStop(ts, selected) {
		return GenerationCancelResult{Found: true, SkippedNewerGeneration: true}, nil
	}
	ts.claimCancel(true)
	ts.cancelling.Store(true)
	ts.mu.RLock()
	cancel := ts.providerCancel
	ts.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	if ts.requestGracefulInterrupt(hint) {
		al.emitEvent(EventKindInterruptReceived,
			ts.eventMeta("Interrupt", "turn.interrupt.received"),
			InterruptReceivedPayload{Kind: InterruptKindGraceful, HintLen: len(hint)})
	}
	return GenerationCancelResult{Found: true, Cancelled: true}, nil
}
