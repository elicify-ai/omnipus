// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// stopEffectsForCallback is the immutable target set a delayed stop effect
// may act on. A current fence's StopEffect is that stop's own selection: a
// newer stop of the replacement uses it and does not inherit an older one.
// After resume has cleared the fence, only accepted effects whose run is not
// the current admission remain — the old stop cannot select the replacement.
func (al *AgentLoop) stopEffectsForCallback(sessionID string, generation int) ([]session.StopEffect, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil, fmt.Errorf("steer: stop effect %q: lifecycle store is not wired", sessionID)
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return nil, err
	}
	if rec != nil && rec.Stop != nil && rec.Stop.Generation == generation && rec.StopEffect != nil &&
		rec.StopEffect.ControlID != "" && stopEffectMatchesGeneration(*rec.StopEffect, generation) {
		return []session.StopEffect{*rec.StopEffect}, nil
	}
	effects, err := lifecycle.AcceptedStopEffects(sessionID)
	if err != nil {
		return nil, err
	}
	var currentRun string
	var currentBoot uint64
	if rec != nil && rec.ExecutionID != nil {
		currentRun = rec.ExecutionID.RunID
		currentBoot = rec.ExecutionID.BootSeq
	}
	var stale []session.StopEffect
	for _, effect := range effects {
		if !stopEffectMatchesGeneration(effect, generation) {
			continue
		}
		if currentRun != "" && effect.Target.RunID == currentRun && effect.Target.BootSeq == currentBoot {
			continue
		}
		stale = append(stale, effect)
	}
	return stale, nil
}

func stopEffectMatchesGeneration(effect session.StopEffect, generation int) bool {
	return effect.ControlID != "" && effect.Target.Selected() && effect.Target.Generation == generation && effect.Target.RunID != "" && effect.Target.BootSeq != 0
}

func claimForStopEffect(sessionID string, effect session.StopEffect) executionClaim {
	return executionClaim{
		SessionID:  sessionID,
		Generation: effect.Target.Generation,
		RunID:      effect.Target.RunID,
		BootSeq:    effect.Target.BootSeq,
	}
}

// removeQueuedStopEffects drops only the queued admissions the selected
// effects name. It does not remove another run of the same session.
func (al *AgentLoop) removeQueuedStopEffects(sessionID string, effects []session.StopEffect) {
	gate := al.steerAdmission()
	for _, effect := range effects {
		gate.removeQueuedExecution(claimForStopEffect(sessionID, effect))
	}
}

// liveTurnMatchesStop reports whether the registered turn is one of the
// selected executions. The turn lock is not held after this returns.
func liveTurnMatchesStop(ts *turnState, effects []session.StopEffect) bool {
	if ts == nil {
		return false
	}
	ts.mu.RLock()
	runID, boot, generation := ts.executionRunID, ts.executionBootSeq, ts.generation
	ts.mu.RUnlock()
	for _, effect := range effects {
		if effect.Target.RunID == runID && effect.Target.BootSeq == boot && effect.Target.Generation == generation {
			return true
		}
	}
	return false
}
