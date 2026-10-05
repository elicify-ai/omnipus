// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// checkNewAdmission runs under entryMu, before any durable owner write. All
// steered dispatch, wake and continuation entry paths use that same lock.
// A duplicate cannot stamp a phantom owner and then lose registration.
func (al *AgentLoop) checkNewAdmission(rec *session.LifecycleRecord) error {
	if al.getActiveTurnState(rec.SessionID) != nil {
		return fmt.Errorf("steer: admission: %w: an execution is already registered", steer.ErrStaleGeneration)
	}
	gate := al.steerAdmission()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if rec.ExecutionID == nil {
		// Explicit revival cleared the old owner. Its earlier reservation may
		// still be unwinding; tryAdmitRun queues instead of overwriting it.
		return nil
	}
	if _, active := gate.active[rec.SessionID]; active {
		return fmt.Errorf("steer: admission: %w: an admission is already reserved", steer.ErrStaleGeneration)
	}
	for _, entry := range gate.queue {
		if entry.sessionID == rec.SessionID && entry.generation == rec.Generation &&
			entry.runID == rec.ExecutionID.RunID && entry.bootSeq == rec.ExecutionID.BootSeq {
			return fmt.Errorf("steer: admission: %w: an admission is already queued", steer.ErrStaleGeneration)
		}
	}
	return nil
}

// commitSteeredExecutionState checks the admission's complete immutable tuple
// inside the SAME lifecycle mutation that changes its queue/run state. A
// promotion validates its original durable owner; it never re-stamps it.
func commitSteeredExecutionState(lifecycle *session.LifecycleStore, claim executionClaim, state session.LifecycleState, pendingMessage string) (*session.LifecycleRecord, error) {
	if lifecycle == nil || claim.RunID == "" || claim.BootSeq == 0 {
		return nil, errCompleteNoExecutionIdentity
	}
	var committed *session.LifecycleRecord
	var refusal error
	err := lifecycle.Mutate(claim.SessionID, func(rec *session.LifecycleRecord) error {
		if rec == nil {
			refusal = session.ErrLifecycleNotFound
			return refusal
		}
		if ok, reason := reserveDispatch(rec, claim.Generation); !ok {
			refusal = dispatchRefusalError(reason)
			return refusal
		}
		if !claim.matches(rec) {
			refusal = steer.ErrStaleGeneration
			return refusal
		}
		rec.State = state
		if pendingMessage != "" {
			rec.PendingUserMessages = append(rec.PendingUserMessages, pendingMessage)
		}
		// A running admission is real activity. Queued waiting stays inside
		// the live CreatedAt window; stamping it here would let a later
		// restart credit that wait as downtime.
		if state == session.LifecycleRunning {
			rec.NoteRealActivity(time.Now().UTC())
		}
		committed = rec
		return nil
	})
	if err != nil {
		if refusal != nil {
			return nil, refusal
		}
		return nil, fmt.Errorf("steer: admission state: %w: %w", steer.ErrStoreWrite, err)
	}
	return committed, nil
}
