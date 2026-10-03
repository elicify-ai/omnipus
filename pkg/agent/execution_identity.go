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
//     handle (turnState.executionRunID/executionBootSeq); a promotion or
//     retry of the SAME admission re-stamps the same id, a new admission
//     never reuses a previous one.
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
	"github.com/google/uuid"
)

// bootEpochRegistry maps *AgentLoop -> the one *session.BootEpochStore the
// gateway minted at boot. A package-level side table, not an AgentLoop
// struct field, mirroring steerAdmissionRegistry's rationale: AgentLoop
// instances are process-lifetime singletons in production, and loop.go must
// not grow (the side-table precedent admission.go documents). Nil until
// SetBootEpochStore wires it (post-boot); every consumer treats nil as
// boot_seq 0 — the same "not minted" value BootEpochStore.Current reports,
// never a constructed counter.
var (
	bootEpochRegistry   = map[*AgentLoop]*session.BootEpochStore{}
	bootEpochRegistryMu sync.Mutex
)

// SetBootEpochStore wires the gateway-booted boot epoch store onto the loop
// (pkg/gateway/gateway_boot.go::wireSteerDeps, next to the other steer dep
// setters). The store stays owned by the boot path: consumers read Current,
// nobody here ever calls Mint.
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
// Last admission wins: a later admission's stamp overwrites an earlier
// one's. That is safe by construction because the completion side never
// trusts the stamp alone — it corroborates it against the claiming
// execution's live registration (executionClaim, below) — and because a
// merely-queued admission re-stamps its own unchanged id when it is
// promoted. bootSeq is the boot epoch read once for this admission via
// bootEpochFor.
func stampAdmissionExecution(lifecycle *session.LifecycleStore, sessionID string, gen int, runID string, bootSeq uint64) error {
	if lifecycle == nil {
		return fmt.Errorf("steer: admission identity %q: no lifecycle store wired", sessionID)
	}
	if runID == "" {
		return fmt.Errorf("steer: admission identity %q: empty run_id", sessionID)
	}
	err := lifecycle.Mutate(sessionID, func(rec *session.LifecycleRecord) error {
		if rec == nil {
			return session.ErrLifecycleNotFound
		}
		if ok, reason := reserveDispatch(rec, gen); !ok {
			return fmt.Errorf("%s", reason)
		}
		rec.ExecutionID = &session.ExecutionIdentity{RunID: runID, BootSeq: bootSeq}
		return nil
	})
	if err != nil {
		return fmt.Errorf("steer: admission identity %q generation %d: %w", sessionID, gen, err)
	}
	return nil
}

// executionClaim is the producing identity a completion presents, plus the
// live registration it was corroborated against at claim time:
//
//   - RunID/BootSeq name the execution producing this outcome — the turn's
//     immutable handle for a real turn, or the zero claim for a turn-less
//     completer.
//   - LiveRunID is the run_id registered live for the session when the
//     claim was taken ("" when none). A record whose stamped owner equals
//     LiveRunID but not RunID is owned by a NEWER execution — the claimant
//     is stale.
type executionClaim struct {
	RunID     string
	BootSeq   uint64
	LiveRunID string
}

// zeroExecutionClaim is the turn-less completer's claim: no execution
// claims the outcome, so the completion boundary applies only the checks it
// could already apply (generation, fence, terminal) — never the identity
// match.
func zeroExecutionClaim() executionClaim { return executionClaim{} }

// tsExecutionClaim builds the claim for a real turn from its immutable
// execution handle, snapshotting the session's live registration in the
// same read.
func (al *AgentLoop) tsExecutionClaim(ts *turnState, sessionID string) executionClaim {
	if ts == nil {
		return zeroExecutionClaim()
	}
	runID, bootSeq := ts.executionIdentity()
	return executionClaim{RunID: runID, BootSeq: bootSeq, LiveRunID: al.liveSteeredRunID(sessionID)}
}

// executionClaimFor is the turn-less completer's derivation: the identity
// of the session's CURRENTLY registered live steered turn, or the zero
// claim when none is registered. It is how the frozen completion entry
// points (completeSteeredTurn, deliverSteeredCompletion,
// reportSteeredSessionTerminalUpward — all called directly by tests and by
// post-turn synthetic dispositions) reach the same boundary: a claim-less
// completion keeps today's checks, a claimed one gets the full identity
// match.
func (al *AgentLoop) executionClaimFor(sessionID string) executionClaim {
	live := al.liveSteeredRunID(sessionID)
	if live == "" {
		return zeroExecutionClaim()
	}
	// The live turn's boot_seq rides its own handle; read it through the
	// registry the same way liveSteeredRunID did.
	if ts := al.getActiveTurnState(sessionID); ts != nil {
		runID, bootSeq := ts.executionIdentity()
		return executionClaim{RunID: runID, BootSeq: bootSeq, LiveRunID: live}
	}
	return zeroExecutionClaim()
}

// liveSteeredRunID returns the run_id of the turn currently registered live
// for sessionID, or "" when none is. A turn that never set an execution
// handle (a non-steered turnState sharing the registry) reports "" too —
// there is no steered execution to corroborate against.
func (al *AgentLoop) liveSteeredRunID(sessionID string) string {
	ts := al.getActiveTurnState(sessionID)
	if ts == nil {
		return ""
	}
	runID, _ := ts.executionIdentity()
	return runID
}
