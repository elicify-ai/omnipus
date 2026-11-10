package agent

import "fmt"

// Ordinary execution metadata lives in the existing worker owner map. A lease
// is worker-lifetime capacity; metadata alone is never an admitted worker.
type ordinaryAdmissionOwner struct {
	lease     *ordinaryWorkerLease
	execution *executionDisposition
}

type ordinaryWorkerLease struct{ held bool }

func (a *AdmissionController) workerCountLocked() int {
	count := 0
	for _, owner := range a.activeScopes {
		if owner.lease != nil {
			count++
		}
	}
	return count
}

func (a *AdmissionController) attachExecution(scope string, d *executionDisposition) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	owner := a.activeScopes[scope]
	if owner == nil {
		owner = &ordinaryAdmissionOwner{}
		a.activeScopes[scope] = owner
	}
	if owner.execution != nil {
		return fmt.Errorf("admission: this worker already owns a pending execution disposition")
	}
	owner.execution = d
	d.ordinaryScope = scope
	return nil
}

func (al *AgentLoop) executionDispositionFor(claim executionClaim) *executionDisposition {
	gate := al.steerAdmission()
	gate.mu.Lock()
	entry, ok := gate.active[claim.SessionID]
	if ok && entry.executionClaim() == claim && entry.disposition != nil {
		d := entry.disposition
		gate.mu.Unlock()
		return d
	}
	gate.mu.Unlock()
	if al.admission == nil {
		return nil
	}
	al.admission.mu.Lock()
	defer al.admission.mu.Unlock()
	for _, owner := range al.admission.activeScopes {
		if owner.execution != nil && owner.execution.claim == claim {
			return owner.execution
		}
	}
	return nil
}

func (al *AgentLoop) attachSteeredDisposition(ts *turnState, claim executionClaim) error {
	d, err := al.attachSteeredExecution(claim)
	if err != nil {
		return err
	}
	ts.opts.executionDisposition = d
	return nil
}

// attachSteeredExecution gives the selected steered reservation its execution
// disposition without binding it to a turn. A producer whose turns are built
// later, one per run-loop pass (a task run), carries the returned owner to
// them itself. The caller holds entryMu.
func (al *AgentLoop) attachSteeredExecution(claim executionClaim) (*executionDisposition, error) {
	gate := al.steerAdmission()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	entry, ok := gate.active[claim.SessionID]
	if !ok || entry.executionClaim() != claim || entry.disposition != nil {
		return nil, fmt.Errorf("admission: selected steered reservation is not available for its producer")
	}
	d := newExecutionDisposition(claim)
	d.steered = true
	entry.disposition = d
	gate.active[claim.SessionID] = entry
	return d, nil
}

// The caller holds entryMu. Full claim AND pointer protect both owner maps;
// retirement of an execution never erases a newer worker lease or execution.
func (al *AgentLoop) retireExecutionDisposition(d *executionDisposition) (steerQueueEntry, bool) {
	al.activeTurnStates.Range(func(_, value any) bool {
		if ts, ok := value.(*turnState); ok && ts.opts.executionDisposition == d && al.tsExecutionClaim(ts, "") == d.claim {
			al.activeTurnStates.CompareAndDelete(ts.sessionKey, ts)
		}
		return true
	})
	if d.steered {
		gate := al.steerAdmission()
		gate.mu.Lock()
		defer gate.mu.Unlock()
		entry, ok := gate.active[d.claim.SessionID]
		if ok && entry.executionClaim() == d.claim && entry.disposition == d {
			return gate.releaseLocked(d.claim.SessionID)
		}
		return steerQueueEntry{}, false
	}
	al.admission.mu.Lock()
	defer al.admission.mu.Unlock()
	owner := al.admission.activeScopes[d.ordinaryScope]
	if owner != nil && owner.execution == d && owner.execution.claim == d.claim {
		owner.execution = nil
		if owner.lease == nil {
			delete(al.admission.activeScopes, d.ordinaryScope)
		}
	}
	return steerQueueEntry{}, false
}
