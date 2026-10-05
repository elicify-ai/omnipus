package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// The worker's outer processTurn owns this carrier through its continuation,
// response and typing-stop tails. A direct processMessage owns its own carrier.
type ordinaryExecutionEntry struct{ disposition *executionDisposition }
type ordinaryExecutionEntryKey struct{}

func ordinaryExecutionContext(ctx context.Context) (context.Context, *ordinaryExecutionEntry) {
	entry := &ordinaryExecutionEntry{}
	return context.WithValue(ctx, ordinaryExecutionEntryKey{}, entry), entry
}

func ordinaryExecutionFromContext(ctx context.Context) *ordinaryExecutionEntry {
	entry, _ := ctx.Value(ordinaryExecutionEntryKey{}).(*ordinaryExecutionEntry)
	return entry
}

func ordinaryDispositionFromContext(ctx context.Context) *executionDisposition {
	if entry := ordinaryExecutionFromContext(ctx); entry != nil {
		return entry.disposition
	}
	return nil
}

// A zero preparation is valid for an ordinary path with no lifecycle record.
// Its value type keeps that case distinct from a failed admission.
type ordinaryExecutionPreparation struct{ execution *executionDisposition }

// prepareOrdinaryExecution reuses identity preparation, not steered capacity or
// FIFO admission. Only existing lifecycle-backed human entry paths use it.
func (al *AgentLoop) prepareOrdinaryExecution(ctx context.Context, msg bus.InboundMessage, opts processOptions) (ordinaryExecutionPreparation, error) {
	store := al.GetSessionLifecycleStore()
	if !reviveInboundIsHumanTurn(msg) || msg.SessionID == "" || store == nil {
		return ordinaryExecutionPreparation{}, nil
	}
	gate := al.steerAdmission()
	gate.entryMu.Lock()
	defer gate.entryMu.Unlock()
	rec, err := store.Load(msg.SessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return ordinaryExecutionPreparation{}, nil
	}
	if err != nil {
		return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: read saved session: %w", err)
	}
	if fenceErr := al.inboundStopFenceInFlight(msg.SessionID); fenceErr != nil {
		return ordinaryExecutionPreparation{}, fenceErr
	}
	if al.activeTurnForCancel(msg.SessionID, CancelScope{SessionID: msg.SessionID, TurnOnly: true}) != nil {
		return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: %w: an execution is already registered", steer.ErrStaleGeneration)
	}
	al.admission.mu.Lock()
	owner := al.admission.activeScopes[opts.SessionKey]
	pending := owner != nil && owner.execution != nil
	al.admission.mu.Unlock()
	if pending {
		return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: previous execution disposition is still pending")
	}
	if rec.Terminal() || rec.State == session.LifecycleStopped {
		if reviveErr := al.reviveRecordForHumanTurn(ctx, msg.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: msg.GatewayUserID}); reviveErr != nil {
			return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: explicit revival failed: %w", reviveErr)
		}
		rec, err = store.Load(msg.SessionID)
		if err != nil {
			return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: read revived session: %w", err)
		}
	}
	if err := al.checkNewAdmission(rec); err != nil {
		return ordinaryExecutionPreparation{}, err
	}
	claim := executionClaim{SessionID: rec.SessionID, Generation: rec.Generation, RunID: freshRunID(), BootSeq: al.bootEpochFor()}
	if err := stampAdmissionExecution(store, rec.SessionID, rec.Generation, claim.RunID, claim.BootSeq, rec.ExecutionID); err != nil {
		return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: %w", err)
	}
	d := newExecutionDisposition(claim)
	if err := al.admission.attachExecution(opts.SessionKey, d); err != nil {
		return ordinaryExecutionPreparation{}, err
	}
	return ordinaryExecutionPreparation{execution: d}, nil
}

// newTurnStateForAdmission preserves the ordinary routing/history key. The
// canonical lifecycle identity comes only from its admitted execution carrier.
func (al *AgentLoop) newTurnStateForAdmission(agent *AgentInstance, opts processOptions) (*turnState, error) {
	ts := newTurnState(agent, opts, al.newTurnEventScope(agent.ID, opts.SessionKey))
	d := opts.executionDisposition
	if d == nil {
		return ts, nil
	}
	gate := al.steerAdmission()
	gate.entryMu.Lock()
	defer gate.entryMu.Unlock()
	rec, err := al.GetSessionLifecycleStore().Load(d.claim.SessionID)
	if err != nil {
		return nil, fmt.Errorf("ordinary admission: validate producing identity: %w", err)
	}
	if !d.claim.matches(rec) || al.executionDispositionFor(d.claim) != d {
		return nil, fmt.Errorf("ordinary admission: %w: selected owner changed", steer.ErrStaleGeneration)
	}
	if ok, reason := reserveDispatch(rec, d.claim.Generation); !ok {
		return nil, dispatchRefusalError(reason)
	}
	ts.generation = d.claim.Generation
	if err := ts.setExecutionIdentity(d.claim.RunID, d.claim.BootSeq); err != nil {
		return nil, err
	}
	if !al.registerTurnIfAbsent(ts) {
		return nil, fmt.Errorf("ordinary admission: %w: registration was already claimed", steer.ErrStaleGeneration)
	}
	if _, err := commitSteeredExecutionState(al.GetSessionLifecycleStore(), d.claim, session.LifecycleRunning, ""); err != nil {
		al.activeTurnStates.CompareAndDelete(ts.sessionKey, ts)
		return nil, err
	}
	return ts, nil
}
