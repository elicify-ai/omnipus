package agent

import "github.com/elicify-ai/omnipus/pkg/session"

// retainAcceptedStop is the acceptance-to-owner handoff. Owner lookup and
// binding stay under that owner's mutex, so retirement cannot miss a binding
// between a copied pointer and its write. It never waits for a producer, calls
// a provider, removes a slot, or publishes lifecycle state.
func (al *AgentLoop) retainAcceptedStop(selected session.StopSelection) error {
	claim := claimForStopEffect(selected.SessionID, selected.Effect)
	gate := al.steerAdmission()
	gate.mu.Lock()
	if entry, ok := gate.active[claim.SessionID]; ok && entry.executionClaim() == claim && entry.disposition != nil {
		err := entry.disposition.bindStop(selected, nil)
		gate.mu.Unlock()
		return err
	}
	gate.mu.Unlock()
	if al.admission == nil {
		return nil
	}
	al.admission.mu.Lock()
	defer al.admission.mu.Unlock()
	for _, owner := range al.admission.activeScopes {
		if owner.execution != nil && owner.execution.claim == claim {
			return owner.execution.bindStop(selected, nil)
		}
	}
	return nil
}
