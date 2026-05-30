//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — in-process tool-approval registry.
//
// This file implements the approval state machine specified in the
// "Approval State Table" section of docs/specs/tool-registry-redesign-spec.md
// (revision 6).  The registry is purely in-process: gateway restart cancels
// all pending approvals (FR-013).
//
// State machine (8 states, 1 active, 7 terminal):
//
//	pending → approved            (approve action, admin if RequiresAdminAsk)
//	pending → denied_user         (deny action)
//	pending → denied_cancel       (cancel action)
//	pending → denied_timeout      (timer fires, configurable, default 300 s)
//	pending → denied_restart      (gateway shutdown)
//	pending → denied_saturated    (skip-pending path when cap exceeded)
//	pending → denied_batch_short_circuit (sibling in same batch denied/canceled)
//
// Any action on a terminal state returns HTTP 410 Gone (FR-018).

package gateway

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// ApprovalState is the lifecycle state of a single tool-approval request.
type ApprovalState string

const (
	// ApprovalStatePending is the sole active state.  The agent loop is paused.
	ApprovalStatePending ApprovalState = "pending"

	// ApprovalStateApproved and the other terminal states return HTTP 410 Gone on any action.
	ApprovalStateApproved                ApprovalState = "approved"
	ApprovalStateDeniedUser              ApprovalState = "denied_user"
	ApprovalStateDeniedTimeout           ApprovalState = "denied_timeout"
	ApprovalStateDeniedCancel            ApprovalState = "denied_cancel"
	ApprovalStateDeniedRestart           ApprovalState = "denied_restart"
	ApprovalStateDeniedSaturated         ApprovalState = "denied_saturated"
	ApprovalStateDeniedBatchShortCircuit ApprovalState = "denied_batch_short_circuit"
)

// isTerminal returns true for every terminal state.
func (s ApprovalState) isTerminal() bool {
	switch s {
	case ApprovalStateApproved,
		ApprovalStateDeniedUser,
		ApprovalStateDeniedTimeout,
		ApprovalStateDeniedCancel,
		ApprovalStateDeniedRestart,
		ApprovalStateDeniedSaturated,
		ApprovalStateDeniedBatchShortCircuit:
		return true
	}
	return false
}

// ApprovalAction is the action sent by the caller to the approve/deny/cancel endpoint.
type ApprovalAction string

const (
	ApprovalActionApprove ApprovalAction = "approve"
	ApprovalActionDeny    ApprovalAction = "deny"
	ApprovalActionCancel  ApprovalAction = "cancel"
)

// approvalEntry holds the mutable state for one pending approval request.
// The loop blocks on resultCh until the entry resolves.
type approvalEntry struct {
	// Immutable fields set at creation.
	ApprovalID    string
	ToolCallID    string
	ToolName      string
	Args          map[string]any
	AgentID       string
	SessionID     string
	TurnID        string
	RequiresAdmin bool // true when tool.RequiresAdminAsk() == true
	CreatedAt     time.Time
	ExpiresAt     time.Time

	// Mutable — protected by the registry's mu.
	state ApprovalState

	// resultCh carries the resolved outcome to the blocked agent loop goroutine.
	// Buffered (cap 1) so the registry resolver never blocks.
	resultCh chan ApprovalOutcome

	// timer fires after the configured timeout and transitions pending→denied_timeout.
	timer *time.Timer
}

// ApprovalOutcome is the result delivered to the blocked agent loop goroutine.
type ApprovalOutcome struct {
	Approved bool
	Reason   string // one of "approved","user","timeout","cancel","restart","saturated","batch_short_circuit"
}

// approvalRegistryV2 is the central in-process approval registry (FR-016, FR-070).
// It enforces the saturation cap (default 64) and the full Approval State Table.
//
// The V2 suffix distinguishes it from the legacy wsApprovalRegistry (kept for the
// existing exec_approval_request/response WS protocol) which remains untouched.
type approvalRegistryV2 struct {
	mu      sync.Mutex
	entries map[string]*approvalEntry // approval_id → entry

	// maxPending is the saturation cap (FR-016).  0 = unlimited (discouraged).
	// Set once at construction; protected by maxPending atomic for fast reads.
	maxPendingAtomic atomic.Int64
	maxPending       int // canonical source
	timeout          time.Duration

	// terminalRetention is how long a terminal-state entry is kept in the map
	// before it is deleted. The retention window preserves FR-018 HTTP 410
	// Gone semantics for late actions (so a second POST to /api/v1/tool-approvals/{id}
	// after resolve still returns 410, not 404). After the window elapses, the
	// entry is removed to bound memory growth.
	//
	// Defaults to 60 s; tests set this directly to 0 to assert deletion
	// synchronously without waiting on a timer.
	terminalRetention time.Duration

	// pendingGauge is a snapshot updated under mu; used for the Prometheus gauge.
	pendingCount atomic.Int64
}

// defaultTerminalRetention is the grace window during which a terminal-state
// entry is retained in the map after its state transition. After this window
// expires, the entry is deleted to bound memory growth.
const defaultTerminalRetention = 60 * time.Second

// newApprovalRegistryV2 creates a registry with the given saturation cap and timeout.
// maxCap == 0 means unlimited (ShouldSaturate always returns false per FR-016).
// maxCap > 0 is the saturation limit. Negative maxCap must not reach here (caller
// must validate via policy.ValidateSaturationCap before constructing).
// timeout <= 0 selects the spec default (300 s).
func newApprovalRegistryV2(maxCap int, timeout time.Duration) *approvalRegistryV2 {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	r := &approvalRegistryV2{
		entries:           make(map[string]*approvalEntry),
		maxPending:        maxCap,
		timeout:           timeout,
		terminalRetention: defaultTerminalRetention,
	}
	r.maxPendingAtomic.Store(int64(maxCap))
	return r
}

// scheduleTerminalDelete arms a timer that removes the entry from r.entries
// after r.terminalRetention has elapsed. The deletion runs under r.mu so it
// is race-free with respect to resolve / get / pendingApprovals. If
// terminalRetention <= 0 the entry is deleted synchronously by the caller
// (used by tests and by cancelAllPendingForRestart on shutdown).
//
// MUST be called with r.mu held (it reads r.terminalRetention).
func (r *approvalRegistryV2) scheduleTerminalDelete(approvalID string) {
	retention := r.terminalRetention
	if retention <= 0 {
		// Caller (or shutdown path) requested immediate deletion; do it inline
		// while we still hold the lock.
		delete(r.entries, approvalID)
		return
	}
	time.AfterFunc(retention, func() {
		r.mu.Lock()
		delete(r.entries, approvalID)
		r.mu.Unlock()
	})
}

// requestApproval creates a new pending approval entry and returns it.
//
// Returns (entry, true) if accepted.
// Returns (saturatedEntry, false) if the saturation cap is reached; the returned
// entry is already in denied_saturated state (FR-016, MAJ-009).
func (r *approvalRegistryV2) requestApproval(
	toolCallID, toolName string,
	args map[string]any,
	agentID, sessionID, turnID string,
	requiresAdmin bool,
) (*approvalEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Saturation check (FR-016, MAJ-009).
	pendingCount := 0
	for _, e := range r.entries {
		if e.state == ApprovalStatePending {
			pendingCount++
		}
	}
	maxCap := r.maxPending
	if maxCap > 0 && pendingCount >= maxCap {
		// Return a synthetic saturated entry — caller must NOT emit WS event.
		synthetic := &approvalEntry{
			ApprovalID:    uuid.New().String(),
			ToolCallID:    toolCallID,
			ToolName:      toolName,
			Args:          args,
			AgentID:       agentID,
			SessionID:     sessionID,
			TurnID:        turnID,
			RequiresAdmin: requiresAdmin,
			CreatedAt:     time.Now(),
			state:         ApprovalStateDeniedSaturated,
			resultCh:      make(chan ApprovalOutcome, 1),
		}
		// Pre-deliver the outcome so the caller can receive without blocking.
		synthetic.resultCh <- ApprovalOutcome{Approved: false, Reason: "saturated"}
		return synthetic, false
	}

	now := time.Now()
	expiresAt := now.Add(r.timeout)
	e := &approvalEntry{
		ApprovalID:    uuid.New().String(),
		ToolCallID:    toolCallID,
		ToolName:      toolName,
		Args:          args,
		AgentID:       agentID,
		SessionID:     sessionID,
		TurnID:        turnID,
		RequiresAdmin: requiresAdmin,
		CreatedAt:     now,
		ExpiresAt:     expiresAt,
		state:         ApprovalStatePending,
		resultCh:      make(chan ApprovalOutcome, 1),
	}

	// Arm the timeout timer (FR-016, SC-006: default 300 s).
	e.timer = time.AfterFunc(r.timeout, func() {
		r.fireTimeout(e.ApprovalID)
	})

	r.entries[e.ApprovalID] = e
	r.pendingCount.Add(1)
	return e, true
}

// fireTimeout is called by the entry's timer goroutine.
// It transitions pending→denied_timeout, delivers the outcome, and schedules
// the entry's removal from the map (FR-018 retention window then delete).
func (r *approvalRegistryV2) fireTimeout(approvalID string) {
	r.mu.Lock()
	e, ok := r.entries[approvalID]
	if !ok || e.state != ApprovalStatePending {
		r.mu.Unlock()
		return
	}
	e.state = ApprovalStateDeniedTimeout
	r.pendingCount.Add(-1)
	r.scheduleTerminalDelete(approvalID)
	r.mu.Unlock()

	slog.Info("approval: timeout fired", "approval_id", approvalID, "tool", e.ToolName)
	e.resultCh <- ApprovalOutcome{Approved: false, Reason: "timeout"}
}

// resolve applies an explicit action (approve/deny/cancel) to a pending approval.
//
// Returns:
//   - resolveOK=true  → the state transitioned; HTTP 200 expected.
//   - resolveOK=false, gone=true  → entry is already terminal; HTTP 410 expected.
//   - resolveOK=false, gone=false → entry not found; HTTP 404 expected.
//
// For approve actions on tools with RequiresAdmin=true, the caller is responsible
// for enforcing the admin-role check before calling resolve (FR-015).
func (r *approvalRegistryV2) resolve(
	approvalID string,
	action ApprovalAction,
) (resolveOK bool, gone bool) {
	r.mu.Lock()
	e, ok := r.entries[approvalID]
	if !ok {
		r.mu.Unlock()
		return false, false
	}
	if e.state.isTerminal() {
		r.mu.Unlock()
		return false, true // HTTP 410
	}

	var newState ApprovalState
	var outcome ApprovalOutcome
	switch action {
	case ApprovalActionApprove:
		newState = ApprovalStateApproved
		outcome = ApprovalOutcome{Approved: true, Reason: "approved"}
	case ApprovalActionDeny:
		newState = ApprovalStateDeniedUser
		outcome = ApprovalOutcome{Approved: false, Reason: "user"}
	case ApprovalActionCancel:
		newState = ApprovalStateDeniedCancel
		outcome = ApprovalOutcome{Approved: false, Reason: "cancel"}
	default:
		r.mu.Unlock()
		return false, false
	}

	e.state = newState
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	r.pendingCount.Add(-1)
	r.scheduleTerminalDelete(approvalID)
	r.mu.Unlock()

	e.resultCh <- outcome
	return true, false
}

// cancelBatchShortCircuit transitions a pending entry to denied_batch_short_circuit.
// Used when a prior call in the same batch was denied/canceled (FR-065).
// Returns false if the entry is already terminal or not found.
func (r *approvalRegistryV2) cancelBatchShortCircuit(approvalID string) bool {
	r.mu.Lock()
	e, ok := r.entries[approvalID]
	if !ok || e.state.isTerminal() {
		r.mu.Unlock()
		return false
	}
	e.state = ApprovalStateDeniedBatchShortCircuit
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	r.pendingCount.Add(-1)
	r.scheduleTerminalDelete(approvalID)
	r.mu.Unlock()

	e.resultCh <- ApprovalOutcome{Approved: false, Reason: "batch_short_circuit"}
	return true
}

// get looks up an entry by approval ID.  Thread-safe; returns nil if not found.
func (r *approvalRegistryV2) get(approvalID string) *approvalEntry {
	r.mu.Lock()
	e := r.entries[approvalID]
	r.mu.Unlock()
	return e
}

// pendingApprovals returns a snapshot of all pending entries.
// Used for the session_state WS event.
func (r *approvalRegistryV2) pendingApprovals() []*approvalEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	out := make([]*approvalEntry, 0)
	for _, e := range r.entries {
		if e.state == ApprovalStatePending && now.Before(e.ExpiresAt) {
			out = append(out, e)
		}
	}
	return out
}

// cancelAllPendingForRestart transitions every pending approval to denied_restart.
// Called during graceful shutdown (FR-013, FR-048).
// Returns the IDs and tool names of canceled approvals for audit logging.
//
// On shutdown the gateway is going down anyway, so each canceled entry is
// scheduled for deletion via the same retention path as resolve/fireTimeout.
// This keeps the contract uniform across all terminal-state transitions and
// prevents the entries from leaking if the process happens to live longer
// than expected (e.g. a slow shutdown sequence).
func (r *approvalRegistryV2) cancelAllPendingForRestart() []approvalEntry {
	r.mu.Lock()
	var canceled []approvalEntry
	cancelledIDs := make([]string, 0)
	for _, e := range r.entries {
		if e.state == ApprovalStatePending {
			e.state = ApprovalStateDeniedRestart
			if e.timer != nil {
				e.timer.Stop()
				e.timer = nil
			}
			canceled = append(canceled, *e)
			cancelledIDs = append(cancelledIDs, e.ApprovalID)
			r.pendingCount.Add(-1)
		}
	}
	for _, id := range cancelledIDs {
		r.scheduleTerminalDelete(id)
	}
	r.mu.Unlock()

	for _, snap := range canceled {
		// capture loop variable
		snap.resultCh <- ApprovalOutcome{Approved: false, Reason: "restart"}
	}
	return canceled
}

// cancelAllPendingForSession auto-denies every pending approval whose SessionID
// matches the given sessionID, using ApprovalStateDeniedCancel as the terminal
// state. Called by the cancel handler after InterruptSession so that any agent
// loop goroutine blocked on a resultCh select is unblocked immediately (FR-7).
//
// reason is the human-readable explanation delivered in ApprovalOutcome.Reason;
// callers SHOULD pass "session canceled" (per FR-7 / EC-8).
//
// Returns the count of approvals auto-denied (for audit / observability).
// Approvals that are already terminal when this function runs are unaffected
// (FR-8 contract: tools whose execution has already started are not killed here).
func (r *approvalRegistryV2) cancelAllPendingForSession(sessionID, reason string) int {
	r.mu.Lock()
	var toDeliver []*approvalEntry
	cancelledIDs := make([]string, 0)
	for _, e := range r.entries {
		if e.state != ApprovalStatePending || e.SessionID != sessionID {
			continue
		}
		e.state = ApprovalStateDeniedCancel
		if e.timer != nil {
			e.timer.Stop()
			e.timer = nil
		}
		toDeliver = append(toDeliver, e)
		cancelledIDs = append(cancelledIDs, e.ApprovalID)
		r.pendingCount.Add(-1)
	}
	for _, id := range cancelledIDs {
		r.scheduleTerminalDelete(id)
	}
	r.mu.Unlock()

	for _, e := range toDeliver {
		e.resultCh <- ApprovalOutcome{Approved: false, Reason: reason}
	}
	slog.Info("approval: auto-denied pending approvals on session cancel",
		"session_id", sessionID, "count", len(toDeliver), "reason", reason)
	return len(toDeliver)
}

// pendingCount returns a current count of pending approvals for the Prometheus gauge.
func (r *approvalRegistryV2) pendingGaugeValue() int64 {
	return r.pendingCount.Load()
}

// expiresInMs returns milliseconds until this entry expires (clamped to 0).
func (e *approvalEntry) expiresInMs() int64 {
	remaining := time.Until(e.ExpiresAt)
	if remaining < 0 {
		return 0
	}
	return remaining.Milliseconds()
}
