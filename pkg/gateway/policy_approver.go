//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// policyApproverAdapter bridges the agent loop's PolicyApprover interface to the
// gateway's approvalRegistryV2 + WSHandler (FR-011, FR-082, FR-073).
//
// The agent loop lives in pkg/agent and cannot import pkg/gateway (circular dep).
// This adapter lives in pkg/gateway and implements the interface defined in
// pkg/agent, satisfying it via Go structural typing.

package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/agent"
)

// policyApproverAdapter implements agent.PolicyApprover using the in-process
// approvalRegistryV2 and the WSHandler for broadcasting WS events (FR-073).
//
// Lifecycle: created once at gateway boot after both approvalReg and wsHandler
// are initialized; passed to agentLoop.SetToolApprover before any turns start.
type policyApproverAdapter struct {
	reg       *approvalRegistryV2
	wsHandler *WSHandler
}

// newPolicyApproverAdapter constructs the adapter.  reg and wsHandler must be non-nil.
func newPolicyApproverAdapter(reg *approvalRegistryV2, ws *WSHandler) *policyApproverAdapter {
	return &policyApproverAdapter{reg: reg, wsHandler: ws}
}

// RequestApproval implements agent.PolicyApprover (FR-011).
//
// It creates a pending approval entry, broadcasts the tool_approval_required WS
// frame (FR-082) scoped to the session owner (FR-073), then blocks on the result
// channel until the user approves/denies, the timeout fires, or ctx is canceled.
//
// Saturation path (FR-016, MAJ-009): if requestApproval returns accepted==false
// the entry is already in denied_saturated state and resultCh has a pre-delivered
// outcome, so we skip the WS broadcast and unblock immediately.
func (a *policyApproverAdapter) RequestApproval(ctx context.Context, req agent.PolicyApprovalReq) (bool, string) {
	entry, accepted := a.reg.requestApproval(
		req.ToolCallID,
		req.ToolName,
		req.Args,
		req.AgentID,
		req.SessionID,
		req.TurnID,
		req.RequiresAdmin,
	)
	if entry == nil {
		// Defensive: should never happen (requestApproval always returns non-nil).
		slog.Error("policyApprover: requestApproval returned nil entry", "tool", req.ToolName)
		return false, "internal_error"
	}

	if accepted {
		// Broadcast the WS frame to connected clients scoped to the session owner (FR-073).
		a.wsHandler.broadcastToolApprovalRequired(entry)
	}
	// accepted==false → saturated; no WS broadcast, outcome pre-delivered.

	approvalStart := time.Now()

	// Block until the entry resolves or the turn context is canceled.
	select {
	case outcome := <-entry.resultCh:
		// FR-039: record approval latency on every terminal transition.
		globalToolMetrics.ObserveApprovalLatency(outcome.Reason, time.Since(approvalStart).Seconds())
		return outcome.Approved, outcome.Reason
	case <-ctx.Done():
		// Turn was canceled (hard abort, graceful shutdown, etc.). Cancel the entry.
		a.reg.resolve(entry.ApprovalID, ApprovalActionCancel)
		globalToolMetrics.ObserveApprovalLatency("cancel", time.Since(approvalStart).Seconds())
		return false, "cancel"
	}
}
