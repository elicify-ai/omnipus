// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — in-process tool-approval registry.
//
// This file implements the approval state machine specified in the
// "Approval State Table" section of docs/internal/specs/tool-registry-redesign-spec.md
// (revision 6).  The registry is purely in-process: gateway restart cancels
// all pending approvals (FR-013).
//
// State machine (8 states, 1 active, 7 terminal):
//
//	pending → approved            (approve action)
//	pending → denied_user         (deny action)
//	pending → denied_cancel       (cancel action)
//	pending → denied_timeout      (timer fires, configurable, default 600 s)
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
	case ApprovalStatePending:
		return false
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
	ApprovalID string
	ToolCallID string
	ToolName   string
	Args       map[string]any
	AgentID    string

	// SessionID is the ACTING session id — the id of the session whose turn
	// is actually asking for approval (ADR-057 FR-080). For a delegated child
	// turn that is the CHILD's own session id, never the chat's.
	//
	// This is a routing-relevant distinction, not a naming preference.
	//
	// Doc correction (verified against the actual producer, 2026-08):
	// the generated ToolApprovalRequiredFrame wire type (pkg/api/generated/
	// asyncapi_types.gen.go) DOES define a session_id/producing_session_id
	// split for exactly this class-(a) case ("producing_session_id ...
	// present iff it differs from session_id ... the child turn's own
	// session id when this frame crosses the wire from a delegated child"),
	// but the actual WS frame producer — broadcastToolApprovalRequired,
	// pkg/gateway/ws_tool_approval.go (NOT this file — read-only from here)
	// — does NOT follow that split. It sets the frame's SessionId directly
	// from this field (SessionId: entry.SessionID) and never sets
	// ProducingSessionId at all. So AS ACTUALLY PRODUCED TODAY, the WS
	// frame's `session_id` carries the SAME acting/child id this Go field
	// does — never the chat's routing key — and `producing_session_id` is
	// always omitted. The two-key design this comment used to describe (wire
	// session_id = chat, this field = child) is the CONTRACT's intent, not
	// the producer's current behavior; ws_tool_approval.go needs a fix (wire
	// SessionId from the chat's routing key, ProducingSessionId from this
	// field when it differs) to actually realize it. Reported, not fixed
	// here: ws_tool_approval.go is outside this file's ownership. Impact is
	// reported as contained (the SPA routes this frame into a global store
	// and nothing reads its `sessionId`), so this is a doc/contract-fidelity
	// gap, not a live user-facing bug — unlike Defects 1-4 above.
	//
	// What IS accurate and load-bearing regardless of the wire-frame
	// mismatch: this field is read in-process (never round-tripped through
	// the wire frame) by cancelAllPendingForSessions and by the approve/deny
	// HTTP round trip. Two consequences follow from THAT and both are
	// load-bearing:
	//
	//  1. Cancellation matches on this field by exact equality (see
	//     cancelAllPendingForSessions), so a chat-level Stop must pass the
	//     chat's DESCENDANT SET, not the chat id alone — a chat id alone
	//     matches nothing once a child's entry carries the child's id.
	//  2. An approve/deny response MUST resolve by ApprovalID and never by
	//     this field (FR-081), because the responding client only ever saw the
	//     frame's routing `session_id`, which deliberately differs from it.
	SessionID string

	TurnID    string
	CreatedAt time.Time
	ExpiresAt time.Time

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
	Reason   string // one of "approved","user","timeout","cancel","restart","saturated","batch_short_circuit","internal_error","session canceled"
}

// Denial-reason literals emitted by this file (ADR-058 spec §2.2, FR-058-04).
// "approved" (see resolve's ApprovalActionApprove case, below) carries
// Approved: true and is deliberately EXCLUDED from these constants and from
// allApprovalDenialReasons — it is not a denial (spec §2.2).
//
// denialReasonTimeout mirrors the literal fireTimeout emits (below) without
// replacing it there: ADR-058's spec states "fireTimeout is byte-for-byte
// unchanged" (FR-058-04) — its fail-closed behavior is explicitly correct
// and untouched by this change — so fireTimeout keeps its own inline
// "timeout" literal rather than referencing this constant. The constant
// exists so allApprovalDenialReasons can still enumerate the value it
// produces without editing that function.
const (
	denialReasonSaturated         = "saturated"
	denialReasonTimeout           = "timeout"
	denialReasonUser              = "user"
	denialReasonCancel            = "cancel"
	denialReasonBatchShortCircuit = "batch_short_circuit"
	denialReasonRestart           = "restart"

	// denialReasonInternalError mirrors the literal returned by
	// policy_approver.go::policyApproverAdapter.RequestApproval's nil-entry
	// branch ("internal_error") — a denial reason produced OUTSIDE this file
	// (ADR-058 spec §2.2) by policyApproverAdapter's defensive nil-entry
	// branch. It is collected into allApprovalDenialReasons here rather than
	// replacing the literal in policy_approver.go, which is outside this
	// unit's ownership (ADR-058 spec §9, work unit W3) and is left unchanged.
	denialReasonInternalError = "internal_error"

	// denialReasonSessionCanceled mirrors the literal
	// pkg/agent/cancel.go::AgentLoop.RequestCancel passes as the reason
	// argument to hooks.CancelPendingApprovals — which, via
	// pkg/gateway/websocket.go's buildCancelHooks closure, reaches this
	// package's cancelAllPendingForSession(s) as its reason parameter and is
	// delivered verbatim in ApprovalOutcome.Reason. Unlike
	// denialReasonSaturated/Timeout/User/Cancel/BatchShortCircuit/Restart,
	// this is NOT a literal this file emits itself: cancelAllPendingForSessions
	// accepts reason as a caller-supplied parameter (see its doc comment) and
	// "session canceled" is simply the one value every known production
	// caller passes today. It is collected here, mirroring
	// denialReasonInternalError's out-of-file pattern, rather than by
	// replacing the literal in cancel.go, which is outside this unit's
	// ownership.
	denialReasonSessionCanceled = "session canceled"
)

// allApprovalDenialReasons is every denial reason this package can hand to
// agent.ClassifyDenial (ADR-058 FR-058-04): the six literals emitted
// directly in this file, plus internal_error
// (policy_approver.go::policyApproverAdapter.RequestApproval's nil-entry
// branch) and session canceled (pkg/agent/cancel.go::AgentLoop.RequestCancel,
// via cancelAllPendingForSessions's caller-supplied reason parameter).
// "approved" is excluded (see above) because it is not a denial.
//
// Coverage caveat, stated honestly rather than implied: this slice is
// exhaustive over the reasons THIS FILE's own call sites are known to
// produce today — the six inline literals plus the two out-of-file values
// above, each traced to its actual caller. It is NOT exhaustive over every
// string cancelAllPendingForSessions could theoretically be called with:
// that function's reason parameter is caller-supplied (see its doc
// comment) and nothing in this package enumerates or restricts what a
// future caller might pass. A pkg/gateway test
// (approval_denial_classification_test.go) asserts agent.ClassifyDenial(r)
// returns known == true for every member of THIS SLICE, so a new reason
// added to the slice with no matching pkg/agent/tool_denial.go table row
// fails a test rather than defaulting silently — but a new caller of
// cancelAllPendingForSessions passing a reason never added here is
// invisible to that guard, exactly as session canceled itself was before
// this comment was corrected.
var allApprovalDenialReasons = []string{
	denialReasonSaturated,
	denialReasonTimeout,
	denialReasonUser,
	denialReasonCancel,
	denialReasonBatchShortCircuit,
	denialReasonRestart,
	denialReasonInternalError,
	denialReasonSessionCanceled,
}

// approvalRegistryV2 is the central in-process approval registry (FR-016, FR-070).
// It enforces the saturation cap (default 64) and the full Approval State Table.
//
// The V2 suffix is historical: it originally distinguished this registry from
// the legacy wsApprovalRegistry / exec_approval_request/response WS protocol
// (wsApprovalHook, pkg/gateway/ws_approval.go), which ADR-036 §3.4 retired —
// this registry is now the SOLE tool-approval mechanism.
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

	// missingActingSessionID counts requestApproval calls that supplied an
	// empty acting session id (ADR-057 FR-080).
	//
	// An entry with an empty SessionID is unreachable by cancellation:
	// cancelAllPendingForSessions matches by exact equality, and no caller
	// ever asks to cancel "". Such an entry therefore survives every Stop and
	// only resolves when the gateway.go::defaultToolApprovalTimeout timeout
	// fires — a hung turn with no signal. Counting it turns that into an
	// observable defect rather than a mystery stall, and the counter is
	// asserted by test rather than a log scrape.
	missingActingSessionID atomic.Int64
}

// defaultTerminalRetention is the grace window during which a terminal-state
// entry is retained in the map after its state transition. After this window
// expires, the entry is deleted to bound memory growth.
const defaultTerminalRetention = 60 * time.Second

// newApprovalRegistryV2 creates a registry with the given saturation cap and timeout.
// maxCap == 0 means unlimited (ShouldSaturate always returns false per FR-016).
// maxCap > 0 is the saturation limit. Negative maxCap must not reach here (caller
// must validate via policy.ValidateSaturationCap before constructing).
// timeout <= 0 selects defaultToolApprovalTimeout — the SAME constant the
// boot path uses (see gateway.go), deliberately shared so the two fallbacks
// cannot drift apart. This one existed as an independent literal `300 *
// time.Second` while boot used its own; raising only one would have left the
// effective default depending on which path constructed the registry.
func newApprovalRegistryV2(maxCap int, timeout time.Duration) *approvalRegistryV2 {
	if timeout <= 0 {
		timeout = defaultToolApprovalTimeout
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
// actingSessionID MUST be the session id of the turn actually asking — for a
// delegated child turn, the CHILD's own session id, not the chat's routing key
// (ADR-057 FR-080; see approvalEntry.SessionID for why the two differ and what
// depends on the difference). The registry cannot derive it, so it records
// what the caller supplies and counts an empty one as a defect rather than
// creating a silently uncancellable entry.
//
// Returns (entry, true) if accepted.
// Returns (saturatedEntry, false) if the saturation cap is reached; the returned
// entry is already in denied_saturated state (FR-016, MAJ-009).
func (r *approvalRegistryV2) requestApproval(
	toolCallID, toolName string,
	args map[string]any,
	agentID, actingSessionID, turnID string,
) (*approvalEntry, bool) {
	if actingSessionID == "" {
		total := r.missingActingSessionID.Add(1)
		slog.Warn("approval: request has no acting session id — the entry cannot be cancelled and will only resolve on timeout",
			"tool", toolName,
			"tool_call_id", toolCallID,
			"agent_id", agentID,
			"turn_id", turnID,
			"missing_acting_session_total", total)
	}

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
			ApprovalID: uuid.New().String(),
			ToolCallID: toolCallID,
			ToolName:   toolName,
			Args:       args,
			AgentID:    agentID,
			SessionID:  actingSessionID,
			TurnID:     turnID,
			CreatedAt:  time.Now(),
			state:      ApprovalStateDeniedSaturated,
			resultCh:   make(chan ApprovalOutcome, 1),
		}
		// Pre-deliver the outcome so the caller can receive without blocking.
		synthetic.resultCh <- ApprovalOutcome{Approved: false, Reason: denialReasonSaturated}
		return synthetic, false
	}

	now := time.Now()
	expiresAt := now.Add(r.timeout)
	e := &approvalEntry{
		ApprovalID: uuid.New().String(),
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Args:       args,
		AgentID:    agentID,
		SessionID:  actingSessionID,
		TurnID:     turnID,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
		state:      ApprovalStatePending,
		resultCh:   make(chan ApprovalOutcome, 1),
	}

	// Arm the timeout timer (FR-016, SC-006: default gateway.go::defaultToolApprovalTimeout).
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
// Resolution is BY APPROVAL ID and by nothing else (ADR-057 FR-081). The
// registry map is keyed by approval id and this lookup is its only entry
// point, so the round trip is independent of the entry's SessionID. That
// independence is a requirement, not an accident: the client responding to a
// delegated child's request only ever saw the frame's routing `session_id`
// (the chat's), while the entry is keyed by the child's acting session id
// (FR-080). Any future variant that matched on session id would break the
// round trip on the first delegated approval.
//
// Returns:
//   - resolveOK=true  → the state transitioned; HTTP 200 expected.
//   - resolveOK=false, gone=true  → entry is already terminal; HTTP 410 expected.
//   - resolveOK=false, gone=false → entry not found; HTTP 404 expected.
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
		outcome = ApprovalOutcome{Approved: false, Reason: denialReasonUser}
	case ApprovalActionCancel:
		newState = ApprovalStateDeniedCancel
		outcome = ApprovalOutcome{Approved: false, Reason: denialReasonCancel}
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

	e.resultCh <- ApprovalOutcome{Approved: false, Reason: denialReasonBatchShortCircuit}
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
		snap.resultCh <- ApprovalOutcome{Approved: false, Reason: denialReasonRestart}
	}
	return canceled
}

// cancelAllPendingForSessions auto-denies every pending approval whose
// SessionID matches ANY id in sessionIDs, using ApprovalStateDeniedCancel as
// the terminal state. Called by the cancel handler after the agent loop's
// interrupt entry point so that any agent-loop goroutine blocked on a resultCh
// select is unblocked immediately (FR-7).
//
// # Why this takes a SET (ADR-057 FR-032)
//
// Matching is by exact equality on SessionID, and under ADR-057 a delegated
// child's pending entry carries the CHILD's own acting session id (FR-080),
// not the chat's. A chat-level Stop that passed only the chat id would
// therefore match nothing inside any child, and the child's goroutine would
// stay blocked until its gateway.go::defaultToolApprovalTimeout approval
// timeout fired. Callers MUST pass the
// cancelled session's DESCENDANT SET — the session itself plus every session
// beneath it — which is why the parameter is a slice.
//
// Empty ids in sessionIDs are ignored: no entry may be cancelled by "", or a
// single malformed caller would auto-deny every entry that also failed to
// record an acting session id.
//
// reason is the human-readable explanation delivered in ApprovalOutcome.Reason;
// callers SHOULD pass "session canceled" (per FR-7 / EC-8).
//
// Returns the count of approvals auto-denied (for audit / observability).
// Approvals that are already terminal when this function runs are unaffected
// (FR-8 contract: tools whose execution has already started are not killed here).
func (r *approvalRegistryV2) cancelAllPendingForSessions(sessionIDs []string, reason string) int {
	targets := make(map[string]struct{}, len(sessionIDs))
	for _, sid := range sessionIDs {
		if sid == "" {
			continue
		}
		targets[sid] = struct{}{}
	}
	if len(targets) == 0 {
		return 0
	}

	r.mu.Lock()
	var toDeliver []*approvalEntry
	cancelledIDs := make([]string, 0)
	for _, e := range r.entries {
		if e.state != ApprovalStatePending {
			continue
		}
		if _, hit := targets[e.SessionID]; !hit {
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
		"session_ids", sessionIDs, "count", len(toDeliver), "reason", reason)
	return len(toDeliver)
}

// cancelAllPendingForSession is the single-id convenience form of
// cancelAllPendingForSessions.
//
// It is correct only for a session with no live descendants. A chat-level Stop
// MUST use the plural form with the descendant set (ADR-057 FR-032) — see that
// method's doc for why a chat id alone matches nothing inside a delegated
// child.
func (r *approvalRegistryV2) cancelAllPendingForSession(sessionID, reason string) int {
	return r.cancelAllPendingForSessions([]string{sessionID}, reason)
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
