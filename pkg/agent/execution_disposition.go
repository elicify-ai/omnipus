package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// executionDisposition belongs to one admitted outer execution, not runTurn.
// Its owner completes it after all same-admission continuations and output tails.
// Finished/IsAlive describe compute and are deliberately not this barrier.
type executionDisposition struct {
	claim         executionClaim
	ordinaryScope string
	steered       bool
	mu            sync.Mutex
	stop          *executionStopBinding
	done          chan struct{}
	result        error
}

type executionStopBinding struct {
	selected session.StopSelection
	notify   []func(string, error)
}

func newExecutionDisposition(claim executionClaim) *executionDisposition {
	return &executionDisposition{claim: claim, done: make(chan struct{})}
}

func executionStopPending(d *executionDisposition) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stop != nil
}

func (d *executionDisposition) bindStop(selected session.StopSelection, notify func(string, error)) error {
	if d.claim != claimForStopEffect(selected.SessionID, selected.Effect) {
		return fmt.Errorf("Stop: selected execution does not own this disposition")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stop == nil {
		d.stop = &executionStopBinding{selected: selected}
	} else if d.stop.selected != selected {
		return fmt.Errorf("Stop: execution already carries another accepted control")
	}
	if notify != nil {
		d.stop.notify = append(d.stop.notify, notify)
	}
	return nil
}

// finishExecutionDisposition is called only by the outer producer. entryMu
// protects retirement -> landing; no lifecycle/registry/turn lock spans compute.
// Worker-lifetime capacity is not released by ordinary per-execution retirement.
func (al *AgentLoop) finishExecutionDisposition(d *executionDisposition) error {
	if d == nil {
		return nil
	}
	gate := al.steerAdmission()
	gate.entryMu.Lock()
	entryLocked := true
	unlock := func() {
		if entryLocked {
			gate.entryMu.Unlock()
			entryLocked = false
		}
	}
	defer unlock()
	next, promote := al.retireExecutionDisposition(d)
	d.mu.Lock()
	binding := d.stop
	d.mu.Unlock()
	var err error
	if binding != nil {
		err = al.settleSelectedStop(binding.selected, unlock)
	}
	unlock()
	if promote {
		al.promoteSteeredExecution(next)
	}
	if binding != nil && err != nil {
		// A connection callback replaces only the live error-frame transport;
		// it never replaces the direct-parent/channel error delivery.
		err = errors.Join(err, al.reportStopSettlementFailure(binding.selected, err, len(binding.notify) == 0))
		for _, notify := range binding.notify {
			notify(binding.selected.SessionID, err)
		}
	}
	d.mu.Lock()
	d.result = err
	close(d.done)
	d.mu.Unlock()
	return err
}

// retainSelectedStop discovers the actual admission owner even after runTurn
// removed its compute handle. A genuinely disposed/never-admitted target lands
// synchronously only under preparation synchronization and exact owning checks.
func (al *AgentLoop) retainSelectedStop(ctx context.Context, id string, generation int, notify func(string, error)) (*executionDisposition, bool, error) {
	gate := al.steerAdmission()
	gate.entryMu.Lock()
	selected, current, err := al.stopSelectionForCallback(ctx, id, generation)
	if err != nil || !current {
		gate.entryMu.Unlock()
		return nil, current, err
	}
	claim := claimForStopEffect(id, selected.Effect)
	d := al.executionDispositionFor(claim)
	if d != nil {
		err = d.bindStop(selected, notify)
		gate.entryMu.Unlock()
		return d, true, err
	}
	if ts := al.activeTurnForCancel(id, CancelScope{SessionID: id, TurnOnly: true}); ts != nil {
		gate.entryMu.Unlock()
		return nil, true, fmt.Errorf("Stop: selected execution has no owned disposition barrier")
	}
	al.removeQueuedStopEffects(id, []session.StopEffect{selected.Effect})
	// A promotion with no attached producer has not registered under entryMu.
	// Retire only the selected reservation, never a replacement's slot.
	next, promote := gate.releaseExecution(claim)
	entryLocked := true
	unlock := func() {
		if entryLocked {
			gate.entryMu.Unlock()
			entryLocked = false
		}
	}
	defer unlock()
	err = al.settleSelectedStop(selected, unlock)
	unlock()
	if promote {
		al.promoteSteeredExecution(next)
	}
	return nil, true, err
}

func (al *AgentLoop) settleSelectedStop(selected session.StopSelection, afterLanding func()) error {
	ctx := withStopSelection(context.Background(), selected)
	err := al.landSteeredStopReportAtCut(ctx, selected.SessionID, selected.Effect.Target.Generation, steer.OutcomeInterrupted, afterLanding)
	reason := ""
	if err != nil {
		reason = "Stop settlement is pending because required storage or notice publication failed; retry after storage is repaired"
	}
	receiptErr := al.GetSessionLifecycleStore().RecordStopControlResult(selected, err == nil, reason)
	return errors.Join(err, receiptErr)
}
