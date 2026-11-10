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

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/tools"
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

// consentAudit returns the agent loop's live audit logger for the consent
// records (channels security S3), or nil when audit is off or no loop is
// wired (pre-loop test harnesses).
func (a *policyApproverAdapter) consentAudit() *audit.Logger {
	if a.wsHandler == nil || a.wsHandler.agentLoop == nil {
		return nil
	}
	return a.wsHandler.agentLoop.AuditLogger()
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
func (a *policyApproverAdapter) RequestApproval(ctx context.Context, req agent.PolicyApprovalReq) (bool, string, bool) {
	// Keep room pre-admission cards tied to their room. For a member's own
	// work, consumed channel input takes precedence; plain contexts retain
	// the server-derived label supplied by the turn (FR-240 / K7).
	originLabel, roomRef := req.OriginLabel, req.RoomRef
	if origin := tools.ToolTurnOrigin(ctx); !req.RoomPreAdmission && origin != nil && (originLabel == "" || origin.ChannelTriggered()) {
		originLabel = origin.Label()
		if origin.ChannelTriggered() {
			roomRef = nil
		}
	}
	entry, accepted := a.reg.requestApproval(
		req.ToolCallID,
		req.ToolName,
		req.Args,
		req.AgentID,
		req.SessionID,
		req.TurnID,
		approvalExtras{RoomRef: roomRef, OriginLabel: originLabel, RoomPreAdmission: req.RoomPreAdmission},
	)
	if entry == nil {
		// Defensive: should never happen (requestApproval always returns non-nil).
		slog.Error("policyApprover: requestApproval returned nil entry", "tool", req.ToolName)
		return false, "internal_error", false
	}

	// Every admission attempt writes its request record, a refused one too
	// (saturated, missing turn id): its pre-delivered refusal is audited as the
	// terminal decision below, and every decision is paired with its request
	// (channels S3, approval state table, CHECK F2).
	auditAskRequested(ctx, a.consentAudit(), entry)
	if accepted {
		// Broadcast the WS frame to connected clients scoped to the session owner (FR-073).
		a.wsHandler.broadcastToolApprovalRequired(entry)
		// A member's own approval while it works on a group-chat request shows
		// the room "waiting for you in <Agent>'s main chat" (FR-169). A room's
		// pre-admission card is not the member's work and does not count.
		if !entry.RoomPreAdmission {
			// #1449 FR-053: a card that carries this group's reference flips
			// only its own request.
			a.wsHandler.roomCardFor(entry.SessionID, entry.RoomRef, entry.ApprovalID, true)
			defer a.wsHandler.roomCardFor(entry.SessionID, entry.RoomRef, entry.ApprovalID, false)
		}
	}
	// accepted==false → saturated; no WS broadcast, outcome pre-delivered.

	approvalStart := time.Now()

	// Block until the entry resolves or the turn context is canceled.
	select {
	case outcome := <-entry.resultCh:
		// FR-039: record approval latency on every terminal transition.
		globalToolMetrics.ObserveApprovalLatency(outcome.Reason, time.Since(approvalStart).Seconds())
		auditAskDecision(ctx, a.consentAudit(), entry, outcome, time.Since(approvalStart))
		return outcome.Approved, outcome.Reason, outcome.RecordGrant
	case <-ctx.Done():
		// Turn was canceled (hard abort, graceful shutdown, etc.). Cancel the entry.
		outcome := ApprovalOutcome{Reason: denialReasonCancel}
		if resolved, _ := a.reg.resolve(entry.ApprovalID, ApprovalActionCancel, false); !resolved {
			// Another path (a session Stop's CancelPendingApprovals, a
			// click, the timeout) made the entry terminal first. Every
			// terminal transition delivers exactly one outcome on the
			// buffered resultCh, after the registry lock is released, so it
			// may not have arrived yet; this goroutine is its only reader.
			// Receive it so the terminal decision is audited exactly once,
			// here, whichever path resolved the card (channels S3, CHECK F1).
			//
			// GUARD: this receive blocks with no timeout. It relies on the
			// registry invariant that EVERY transition out of pending
			// delivers EXACTLY ONE outcome on resultCh. A new terminal
			// transition that skips the send hangs this turn forever; one
			// that sends twice blocks its sender on the full buffer.
			outcome = <-entry.resultCh
		}
		auditAskDecision(context.WithoutCancel(ctx), a.consentAudit(), entry, outcome, time.Since(approvalStart))
		globalToolMetrics.ObserveApprovalLatency("cancel", time.Since(approvalStart).Seconds())
		return false, "cancel", false
	}
}
