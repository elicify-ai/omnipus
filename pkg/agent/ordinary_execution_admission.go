package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// ErrPreviousExecutionPending refuses an ordinary admission while the previous
// execution of the same chat session has not settled its disposition. The user
// text for it lives in TranslateTurnError.
var ErrPreviousExecutionPending = errors.New("ordinary admission: previous execution disposition is still pending")

// ordinaryAdmissionRefusalError marks a refusal produced by the ORDINARY (human,
// scheduled, hand-back) admission, as opposed to a steered dispatch or a
// cancel. It unwraps to its cause, so a refusal caused by
// steer.ErrStaleGeneration still satisfies errors.Is(err, steer.ErrStaleGeneration)
// for the callers that rely on it; TranslateTurnError maps only this type (and
// ErrPreviousExecutionPending) to the "still finishing" sentence.
type ordinaryAdmissionRefusalError struct{ cause error }

func (e *ordinaryAdmissionRefusalError) Error() string { return e.cause.Error() }
func (e *ordinaryAdmissionRefusalError) Unwrap() error { return e.cause }

func refuseOrdinaryAdmission(cause error) error { return &ordinaryAdmissionRefusalError{cause: cause} }

// refuseOrdinaryIfStale marks err as an ordinary-admission refusal when it is a
// stale-generation refusal, and returns every other error unchanged.
func refuseOrdinaryIfStale(err error) error {
	if errors.Is(err, steer.ErrStaleGeneration) {
		return refuseOrdinaryAdmission(err)
	}
	return err
}

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

// A zero preparation is valid only for a path without a concrete lifecycle
// store/session. A missing record for a real ordinary session is minted first.
type ordinaryExecutionPreparation struct{ execution *executionDisposition }

// prepareOrdinaryExecution reuses identity preparation, not steered capacity or
// FIFO admission. Only an actual human message may revive a saved session.
func (al *AgentLoop) prepareOrdinaryExecution(ctx context.Context, msg bus.InboundMessage, opts processOptions) (ordinaryExecutionPreparation, error) {
	if !reviveInboundIsHumanTurn(msg) {
		return ordinaryExecutionPreparation{}, nil
	}
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: msg.GatewayUserID}
	return al.prepareOrdinarySessionExecution(ctx, msg.SessionID, opts, &by)
}

// prepareOrdinarySessionExecution is shared by human and scheduled entries.
// A nil revival principal never revives: the ordinary dispatch guard still
// refuses stopped/terminal records. A non-human principal never resumes a
// stopped record. This does not reserve a worker or FIFO slot.
func (al *AgentLoop) prepareOrdinarySessionExecution(ctx context.Context, sessionID string, opts processOptions, revival *steer.Principal) (ordinaryExecutionPreparation, error) {
	store := al.GetSessionLifecycleStore()
	if sessionID == "" || store == nil {
		return ordinaryExecutionPreparation{}, nil
	}
	// The previous execution of this conversation may still be finishing
	// its tail (its answer is already out). Wait, bounded, for its owner to
	// settle it before admitting the next one, instead of refusing a
	// message that arrived a moment early. Never waits on this caller's
	// own execution, and never under the admission locks.
	al.awaitPreviousOrdinaryExecution(ctx, sessionID)
	gate := al.steerAdmission()
	gate.entryMu.Lock()
	defer gate.entryMu.Unlock()
	if al.activeTurnForCancel(sessionID, CancelScope{SessionID: sessionID, TurnOnly: true}) != nil {
		return ordinaryExecutionPreparation{}, refuseOrdinaryAdmission(fmt.Errorf("ordinary admission: %w: an execution is already registered", steer.ErrStaleGeneration))
	}
	al.admission.mu.Lock()
	owner := al.admission.activeScopes[sessionID]
	pending := owner != nil && owner.execution != nil
	al.admission.mu.Unlock()
	if pending {
		return ordinaryExecutionPreparation{}, ErrPreviousExecutionPending
	}
	bootSeq := al.bootEpochFor()
	if bootSeq == 0 {
		return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: no minted boot epoch")
	}
	rec, err := store.Load(sessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		rec, err = al.ensureOrdinaryRootRecord(store, sessionID, opts.TranscriptStore)
	}
	if err != nil {
		return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: read saved session: %w", err)
	}
	if fenceErr := al.inboundStopFenceInFlight(sessionID); fenceErr != nil {
		return ordinaryExecutionPreparation{}, fenceErr
	}
	// A person revives a finished or stopped session; a scheduled entry
	// revives a finished one only, so a Stop holds until a person resumes.
	if revival != nil && (rec.Terminal() || (rec.State == session.LifecycleStopped && revival.Kind == steer.PrincipalKindHuman)) {
		if reviveErr := al.reviveRecordForHumanTurn(ctx, sessionID, *revival); reviveErr != nil {
			return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: explicit revival failed: %w", reviveErr)
		}
		rec, err = store.Load(sessionID)
		if err != nil {
			return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: read revived session: %w", err)
		}
	}
	if err := al.checkNewAdmission(rec); err != nil {
		return ordinaryExecutionPreparation{}, refuseOrdinaryIfStale(err)
	}
	claim := executionClaim{SessionID: rec.SessionID, Generation: rec.Generation, RunID: freshRunID(), BootSeq: bootSeq}
	if err := stampAdmissionExecution(store, rec.SessionID, rec.Generation, claim.RunID, claim.BootSeq, rec.ExecutionID); err != nil {
		return ordinaryExecutionPreparation{}, fmt.Errorf("ordinary admission: %w", refuseOrdinaryIfStale(err))
	}
	d := newExecutionDisposition(claim)
	if err := al.admission.attachExecution(sessionID, d); err != nil {
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
		return nil, refuseOrdinaryAdmission(fmt.Errorf("ordinary admission: %w: selected owner changed", steer.ErrStaleGeneration))
	}
	if ok, reason := reserveDispatch(rec, d.claim.Generation); !ok {
		return nil, refuseOrdinaryIfStale(dispatchRefusalError(reason))
	}
	ts.generation = d.claim.Generation
	if err := ts.setExecutionIdentity(d.claim.RunID, d.claim.BootSeq); err != nil {
		return nil, err
	}
	if !al.registerTurnIfAbsent(ts) {
		return nil, refuseOrdinaryAdmission(fmt.Errorf("ordinary admission: %w: registration was already claimed", steer.ErrStaleGeneration))
	}
	if _, err := commitSteeredExecutionState(al.GetSessionLifecycleStore(), d.claim, session.LifecycleRunning, ""); err != nil {
		al.activeTurnStates.CompareAndDelete(ts.sessionKey, ts)
		return nil, refuseOrdinaryIfStale(err)
	}
	return ts, nil
}

// previousExecutionSettleBudget bounds how long a new ordinary admission
// waits for the previous execution of the same conversation to settle.
const previousExecutionSettleBudget = 5 * time.Second

// awaitPreviousOrdinaryExecution waits until the execution currently
// attached to scope (if any, and not the caller's own) has settled, the
// budget runs out, or ctx ends. Admission then decides as before.
func (al *AgentLoop) awaitPreviousOrdinaryExecution(ctx context.Context, scope string) {
	if al.admission == nil || scope == "" {
		return
	}
	al.admission.mu.Lock()
	var previous *executionDisposition
	if owner := al.admission.activeScopes[scope]; owner != nil {
		previous = owner.execution
	}
	al.admission.mu.Unlock()
	if previous == nil || previous == ordinaryDispositionFromContext(ctx) {
		return
	}
	timer := time.NewTimer(previousExecutionSettleBudget)
	defer timer.Stop()
	select {
	case <-previous.done:
	case <-timer.C:
		logger.WarnCF("agent", "ordinary admission: previous execution did not settle within the wait budget; admission proceeds and may be refused",
			map[string]any{"session_id": scope, "blocking_run_id": previous.claim.RunID, "budget": previousExecutionSettleBudget.String()})
	case <-ctx.Done():
	}
}
