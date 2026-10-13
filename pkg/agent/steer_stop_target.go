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
			claim := claimForStopEffect(sessionID, effect)
			gate.removeQueuedExecution(claim)
			// NEW-6/NEW-7: that admission can never run a body now, so the
			// revival hold bound to exactly this execution ends with it. A
			// replacement's hold (same generation, another run id) is untouched.
			al.retireExternalReservations(sessionID, claim)
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
