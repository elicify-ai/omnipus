// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// execution_identity.go is D2 round-4 R4-MAJ-001's execution-identity seam
// (ADR-20260928 sub-agent control plane, asset cd20cf8b):
//
//   - Every steered admission (initial dispatch, working wake, RESUME
//     revival) mints a fresh run_id and persists the internal
//     (session_id, generation, boot_seq, run_id) tuple under the lifecycle
//     lock BEFORE the admission is enqueued on the steer gate or registered
//     as a live turn — stampAdmissionExecution.
//   - The admission's run_id is copied UNCHANGED into its queue entry
//     (steerQueueEntry.runID) and into the turn's immutable execution
//     handle (turnState.executionRunID/executionBootSeq); a promotion of
//     the SAME admission validates its existing id, a new admission never
//     reuses a previous one.
//   - The terminal completion claims its producing identity
//     (executionClaim) and commitSteeredCompletion checks the full tuple —
//     not only the generation — inside the store lock, committing
//     commit_id == producing run_id (the store rejects anything else on a
//     stamped record).
//
// The explicit RESUME/redirect replacement that must adopt an ACCEPTED D4
// ledger control_id as its run_id is a later unit on the W2a typed store
// API: until that API exists, admissions mint ordinary fresh run_ids and
// no control_id is invented here.
package agent

import (
	"fmt"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
)

// bootEpochRegistry maps *AgentLoop -> the one *session.BootEpochStore the
// gateway minted at boot. A package-level side table, not an AgentLoop
// struct field, mirroring steerAdmissionRegistry's rationale: AgentLoop
// instances are process-lifetime singletons in production, and loop.go must
// not grow (the side-table precedent admission.go documents). Nil until
// SetBootEpochStore wires it (post-boot). Nil remains "not minted"; the
// admission boundary refuses that value rather than constructing a counter.
var (
	bootEpochRegistry   = map[*AgentLoop]*session.BootEpochStore{}
	bootEpochRegistryMu sync.Mutex
)

// SetBootEpochStore wires the gateway-minted epoch before schedulers start
// (pkg/gateway/boot_epoch_warn.go::mintBootEpoch). Consumers only read Current;
// nobody here calls Mint.
func (al *AgentLoop) SetBootEpochStore(store *session.BootEpochStore) {
	bootEpochRegistryMu.Lock()
	bootEpochRegistry[al] = store
	bootEpochRegistryMu.Unlock()
}

// bootEpochFor returns al's current boot epoch — 0 when no store is wired
// (a bare test loop) or none was minted. Never mints, never constructs a
// counter of its own.
func (al *AgentLoop) bootEpochFor() uint64 {
	bootEpochRegistryMu.Lock()
	store := bootEpochRegistry[al]
	bootEpochRegistryMu.Unlock()
	if store == nil {
		return 0
	}
	return store.Current()
}

// freshRunID mints one admission's run identity. A random UUID is enough:
// the run_id is unique-per-admission bookkeeping, never user-visible wire
// data (the upward replay id stays the deterministic
// <session>:<generation>:final).
func freshRunID() string { return uuid.NewString() }

// stampAdmissionExecution persists runID as sessionID's execution identity
// under the lifecycle lock, BEFORE the caller enqueues the admission on the
// steer gate or registers a live turn for it. The Mutate re-runs I-6's
// reserveDispatch guard against the live tail — a dispatch refused here
// never stamps — and takes the identity stamp atomically with that guard.
//
// Callers serialize admission preparation through the steered gate's entry
// lock and reject duplicate active/queued admissions before stamping. A queue
// promotion validates its existing stamp instead of replacing it. bootSeq is
// the genuine gateway-minted epoch, read once for this new admission.
func stampAdmissionExecution(lifecycle *session.LifecycleStore, sessionID string, gen int, runID string, bootSeq uint64, previous *session.ExecutionIdentity) error {
	if lifecycle == nil {
		return fmt.Errorf("steer: admission identity %q: no lifecycle store wired", sessionID)
	}
	if runID == "" {
		return fmt.Errorf("steer: admission identity %q: empty run_id", sessionID)
	}
	if bootSeq == 0 {
		return fmt.Errorf("steer: admission identity %q: no minted boot epoch", sessionID)
	}
	err := lifecycle.Mutate(sessionID, func(rec *session.LifecycleRecord) error {
		if rec == nil {
			return session.ErrLifecycleNotFound
		}
		if ok, reason := reserveDispatch(rec, gen); !ok {
			return dispatchRefusalError(reason)
		}
		if (rec.ExecutionID == nil) != (previous == nil) ||
			(previous != nil && *rec.ExecutionID != *previous) {
			return steer.ErrStaleGeneration
		}
		rec.ExecutionID = &session.ExecutionIdentity{RunID: runID, BootSeq: bootSeq}
		return nil
	})
	if err != nil {
		return fmt.Errorf("steer: admission identity %q generation %d: %w", sessionID, gen, err)
	}
	return nil
}

// executionClaim is the immutable producing or selected admission identity.
// Registry liveness is not part of ownership: a queued replacement owns its
// record just as an active one does.
type executionClaim struct {
	SessionID  string
	Generation int
	RunID      string
	BootSeq    uint64
}

func (claim executionClaim) matches(rec *session.LifecycleRecord) bool {
	return rec != nil && rec.ExecutionID != nil && claim.RunID != "" && claim.BootSeq != 0 &&
		claim.SessionID == rec.SessionID && claim.Generation == rec.Generation &&
		claim.RunID == rec.ExecutionID.RunID && claim.BootSeq == rec.ExecutionID.BootSeq
}

// tsExecutionClaim reads the producing turn itself, never a replacement
// looked up by session ID after the producing turn has left the registry.
func (al *AgentLoop) tsExecutionClaim(ts *turnState, _ string) executionClaim {
	if ts == nil {
		return executionClaim{}
	}
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	sessionID := ts.sessionKey
	if ts.opts.executionDisposition != nil {
		sessionID = ts.opts.executionDisposition.claim.SessionID
	}
	return executionClaim{
		SessionID: sessionID, Generation: ts.generation,
		RunID: ts.executionRunID, BootSeq: ts.executionBootSeq,
	}
}

// executionClaimFor carries the identity of an already-selected record.
// A synthetic disposition must retain this snapshot; it cannot claim a
// replacement's live handle. Missing admission identity remains missing and
// the final commit refuses it visibly.
func (al *AgentLoop) executionClaimFor(rec *session.LifecycleRecord) executionClaim {
	if rec == nil || rec.ExecutionID == nil {
		return executionClaim{}
	}
	return executionClaim{
		SessionID: rec.SessionID, Generation: rec.Generation,
		RunID: rec.ExecutionID.RunID, BootSeq: rec.ExecutionID.BootSeq,
	}
}
