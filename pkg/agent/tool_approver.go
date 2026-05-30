// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ToolApprover is the interface the agent loop calls when an ask-policy tool is
// encountered, implementing the human-in-the-loop approval gate (FR-011, FR-082).
//
// The concrete implementation lives in pkg/gateway (approvalRegistryV2 + WSHandler)
// and is injected at boot via AgentLoop.SetToolApprover to avoid an import cycle.
//
// V2.B fail-closed contract: when no PolicyApprover has been wired (gateway
// boot incomplete, mis-configured deployment, or a bare CLI process), the
// loop returns a `nopPolicyApprover` which denies every ask request with
// reason "no_approver_configured" and emits an `approver.fallback` audit row
// once per process. This closes silent-failure-hunter BE CRIT-1: previously
// the nop returned (true, "") and silently auto-approved every ask call —
// including admin-flagged tools — with zero log and zero audit. The
// auto-approve variant survives only under `//go:build test` (see
// `tool_approver_testonly.go`).

package agent

import (
	"context"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// PolicyApprovalReq carries the fields needed to create and broadcast an approval.
type PolicyApprovalReq struct {
	ToolCallID    string
	ToolName      string
	Args          map[string]any
	AgentID       string
	SessionID     string
	TurnID        string
	RequiresAdmin bool
}

// PolicyApprover is implemented by the gateway to wire the central approval
// registry and WebSocket broadcast into the agent loop (FR-011, FR-082).
//
// This is distinct from the hooks.ToolApprover interface (which is for hook-based
// interactive approval). PolicyApprover governs the policy-level ask gate from
// FilterToolsByPolicy, using the in-process approvalRegistryV2.
//
// RequestApproval MUST:
//  1. Create a pending approval entry in the registry.
//  2. Emit a tool_approval_required WS frame (scoped to session owner, FR-073).
//  3. Block until the user approves/denies, the timeout fires, the queue is
//     saturated, or the gateway shuts down.
//  4. Return (true, "") on approve; (false, reason) otherwise.
//
// denialReason matches the Reason field from ApprovalOutcome:
// "user", "timeout", "saturated", "restart", "cancel", "batch_short_circuit".
type PolicyApprover interface {
	RequestApproval(ctx context.Context, req PolicyApprovalReq) (approved bool, denialReason string)
}

// nopApproverDenialReason is the reason string returned by the default-build
// fallback approver. Surfaces in the agent loop's denial audit row and in the
// synthetic tool-result message that the LLM sees, so an operator who sees an
// agent reporting "permission_denied (no_approver_configured)" knows exactly
// what's wrong (gateway approval-wiring incomplete).
const nopApproverDenialReason = "no_approver_configured"

// nopApproverFallbackOnce gates the V2.B `approver.fallback` audit emit so
// a misconfigured deployment that calls ask-policy tools repeatedly does not
// flood the audit log. The first emit is the diagnostic signal; subsequent
// denials are repeated by definition and would only add noise. Process-scoped:
// resetting it would require a process restart, which is also when an
// operator would have a chance to fix the wiring.
var nopApproverFallbackOnce sync.Once

// nopPolicyApprover is returned by loadToolApprover when no PolicyApprover has
// been set. Default-build behavior (V2.B): deny every approval request with
// reason "no_approver_configured" and emit one `approver.fallback` audit row
// per process. Fail-closed: an ask-policy tool reaching the loop without an
// approver wired must NOT execute.
//
// The previous implementation returned (true, "") with the rationale
// "safe fallback for unit tests and CLI mode" — exactly the rationalization
// that lets a fail-open into production. Per the user decision recorded in
// the V2.B ticket: there is one production build today (Electron is not yet
// started), so the default build IS production, and the test-only
// auto-approve variant lives under `//go:build test` in
// `tool_approver_testonly.go`.
type nopPolicyApprover struct {
	// auditLogger is the loop's audit logger, captured at loadToolApprover
	// time so the once-per-process diagnostic emit reaches the same JSONL
	// file as the surrounding policy-deny audit. May be nil (audit
	// disabled by operator) — emit via audit.EmitEntry handles that.
	auditLogger *audit.Logger
}

// RequestApproval is the V2.B fail-closed default-build implementation:
// always deny with reason "no_approver_configured", and emit one
// `approver.fallback` audit row per process so the gap is loud.
func (n nopPolicyApprover) RequestApproval(_ context.Context, req PolicyApprovalReq) (bool, string) {
	nopApproverFallbackOnce.Do(func() {
		audit.EmitEntry(n.auditLogger, &audit.Entry{
			Event:     audit.EventApproverFallback,
			Decision:  audit.DecisionDeny,
			AgentID:   req.AgentID,
			SessionID: req.SessionID,
			Tool:      req.ToolName,
			Details: map[string]any{
				"reason":         nopApproverDenialReason,
				"build":          "default",
				"turn_id":        req.TurnID,
				"tool_call_id":   req.ToolCallID,
				"requires_admin": req.RequiresAdmin,
				"note":           "SetToolApprover was not called; ask-policy tools are denied. Wire the gateway PolicyApprover to enable human-in-the-loop approval (FR-011, FR-082).",
			},
		})
	})
	return false, nopApproverDenialReason
}
