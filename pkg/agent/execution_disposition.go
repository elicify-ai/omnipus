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
	// turnRan/turnErr record the outcome of the ordinary turn this
	// disposition admitted (recordTurnOutcome); the owner settles an
	// ordinary root's lifecycle from it (settleOrdinaryRoot).
	turnRan bool
	turnErr error
}

// recordTurnOutcome notes how the admitted ordinary turn ended. The last
// outcome recorded before finishExecutionDisposition wins.
func (d *executionDisposition) recordTurnOutcome(err error) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.turnRan, d.turnErr = true, err
	d.mu.Unlock()
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
	if binding == nil {
		// The durable fence, not the in-memory handoff, is what makes this
		// execution's Stop pending (D2): a fence accepted for exactly this
		// claim whose effect has not yet bound here is still this owner's to
		// land after its tail retired.
		selected, owned, readErr := al.durableStopSelectionFor(d.claim)
		if readErr != nil {
			err = readErr
		} else if owned {
			binding = &executionStopBinding{selected: selected}
		}
	}
	if binding != nil {
		err = al.settleSelectedStop(binding.selected, unlock)
	} else if err == nil && !d.steered {
		err = al.settleOrdinaryRoot(d)
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

// durableStopSelectionFor reads the current fence and reports the accepted
// selection when its effect targets exactly claim. A missing record or a fence
// for any other execution is not this owner's; a read failure is returned.
func (al *AgentLoop) durableStopSelectionFor(claim executionClaim) (session.StopSelection, bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || claim.SessionID == "" || claim.RunID == "" {
		return session.StopSelection{}, false, nil
	}
	rec, err := lifecycle.Load(claim.SessionID)
	if err != nil {
		if errors.Is(err, session.ErrLifecycleNotFound) {
			return session.StopSelection{}, false, nil
		}
		return session.StopSelection{}, false, fmt.Errorf("Stop: read selected execution fence for %q: %w", claim.SessionID, err)
	}
	if rec.StopEffect == nil {
		return session.StopSelection{}, false, nil
	}
	selected := session.StopSelection{SessionID: claim.SessionID, Effect: *rec.StopEffect}
	if claimForStopEffect(claim.SessionID, selected.Effect) != claim || !stopSelectionMatchesRecord(selected, rec) {
		return session.StopSelection{}, false, nil
	}
	return selected, true, nil
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

// settleOrdinaryRoot ends an ordinary root chat's turn on its lifecycle
// record (ADR-20260928 Vocabulary, F0929-1/2: only a turn ends; a final
// answer is stored completed and shown done; a turn that failed is stored
// failed). It writes the lifecycle record only — the chat is never archived
// or hidden — and only while the record still belongs to this execution
// and carries no Stop. A turn cut short by cancellation is left to Stop or
// boot recovery. A later message or scheduled run starts the next round.
func (al *AgentLoop) settleOrdinaryRoot(d *executionDisposition) error {
	d.mu.Lock()
	ran, turnErr := d.turnRan, d.turnErr
	d.mu.Unlock()
	lifecycle := al.GetSessionLifecycleStore()
	if !ran || lifecycle == nil || d.claim.SessionID == "" || errors.Is(turnErr, context.Canceled) {
		return nil
	}
	next, failedReason := session.LifecycleCompleted, ""
	if turnErr != nil {
		next, failedReason = session.LifecycleFailed, turnErr.Error()
	}
	err := lifecycle.Mutate(d.claim.SessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		// The record moved on, is steered, or carries a Stop that its own
		// landing settles: this execution writes nothing.
		if cur.SteeredBy != nil || cur.State != session.LifecycleRunning || !d.claim.matches(cur) ||
			(cur.Stop != nil && cur.Stop.Generation == cur.Generation) {
			return errOrdinaryRootNotSettled
		}
		cur.State, cur.FailedReason = next, failedReason
		return nil
	})
	if errors.Is(err, errOrdinaryRootNotSettled) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("settle root %q after its turn: %w", d.claim.SessionID, err)
	}
	return nil
}

// errOrdinaryRootNotSettled aborts the settlement write without error: the
// record is not this execution's to settle.
var errOrdinaryRootNotSettled = errors.New("ordinary root: record not settled by this execution")
