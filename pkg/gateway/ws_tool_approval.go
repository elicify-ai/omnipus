// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// WebSocket events for the Central Tool Registry redesign (A3 lane).
//
// Emits two event types:
//
//  1. tool_approval_required (FR-011, FR-082)
//     Sent to all connected WS clients when an ask-policy tool call is paused.
//     Uses expires_in_ms (not expires_at) per OBS-004.
//
//  2. session_state (FR-052, FR-073, FR-081)
//     One-shot per WS connection on every reconnect.
//     Single-user model: every connection sees every pending approval.

package gateway

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// broadcastToolApprovalRequired sends a tool_approval_required WS frame to
// every connected WebSocket client (FR-073; single-user model, no per-account
// scoping).
//
// Wire format: generated.ToolApprovalRequiredFrame (contract-first, pkg/api/generated).
// Nil-safety: args MUST be an object (never null). The SPA's ToolApprovalModal calls
// Object.keys(args) directly — null crashes with "null is not an object" (Ava-chat bug).
// When entry.Args is nil, we coerce to map[string]any{} at this site.
//
// The frame is best-effort: clients that are disconnected or have a full send buffer
// will miss the frame and must rely on the next session_state reset on reconnect.
func (h *WSHandler) broadcastToolApprovalRequired(entry *approvalEntry) {
	if entry == nil {
		return
	}
	// Nil-safety: coerce nil args to empty map so JSON serializes as {} not null.
	// The SPA's ToolApprovalModal calls Object.keys(args) — null would crash.
	// cloneStringAnyMap (pkg/agent/hooks.go) returns nil for empty input, so a tool
	// invoked without parameters lands here with entry.Args == nil.
	args := entry.Args
	if args == nil {
		args = map[string]any{}
	}

	frame := generated.ToolApprovalRequiredFrame{
		Type:        string(generated.WsFrameTypeToolApprovalRequired),
		ApprovalId:  entry.ApprovalID,
		ToolCallId:  entry.ToolCallID,
		ToolName:    entry.ToolName,
		Args:        args,
		AgentId:     entry.AgentID,
		SessionId:   entry.SessionID,
		TurnId:      entry.TurnID,
		ExpiresInMs: int(entry.expiresInMs()), // OBS-004: relative, not absolute
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal tool_approval_required", "error", err)
		return
	}

	// FR-073 scoping is moot under the single-user model — every connected
	// client is the one account, so every connection receives every approval
	// broadcast unconditionally (role-based scoping removed).
	h.broadcastRaw(raw, "ws: tool_approval_required dropped — send buffer full",
		"approval_id", entry.ApprovalID)
}

// emitSessionState sends the session_state one-shot frame to a single WS connection
// immediately after authentication (FR-052, FR-073, FR-081).
//
// Wire format: generated.SessionStateFrame (contract-first, pkg/api/generated).
// Nil-safety: pending_approvals MUST be an array (never null). The SPA calls
// pending_approvals.map() — null would crash at render time. Coerced to [] when empty.
//
// FR-073 scoping is moot under the single-user model: every connection sees
// every pending approval, for all sessions.
//
// Note: When approvalRegV2 is nil (pre-registry harness), the payload has an empty
// pending_approvals array — the SPA receives a valid frame and clears any stale UI.
func (h *WSHandler) emitSessionState(wc *wsConn) {
	if wc == nil {
		return
	}

	// Always initialize to non-nil slice so JSON encodes as [] not null.
	pendingApprovals := make([]generated.SessionStatePendingApproval, 0)

	if h.approvalRegV2 != nil {
		allPending := h.approvalRegV2.pendingApprovals()

		// FR-073 scoping is moot under the single-user model — every connected
		// client is the one account, so every connection sees every pending
		// approval (role-based scoping removed).
		for _, e := range allPending {
			pendingApprovals = append(pendingApprovals, generated.SessionStatePendingApproval{
				ApprovalId:  e.ApprovalID,
				SessionId:   e.SessionID,
				ToolName:    e.ToolName,
				AgentId:     e.AgentID,
				ExpiresInMs: int(e.expiresInMs()),
			})
		}
	}

	frame := generated.SessionStateFrame{
		Type:             string(generated.WsFrameTypeSessionState),
		UserId:           wc.userID,
		PendingApprovals: pendingApprovals,
		EmittedAt:        time.Now().UTC().Format(time.RFC3339),
	}

	// askuserquestion-tool-spec v3 US-6 S1/FR-9: snapshot every PENDING
	// AskUserQuestion card so a reconnecting SPA re-hydrates its card +
	// composer lock (the boot rearm sweep in gateway.go re-populates the
	// registry from session meta after a restart). Optional field — absent
	// when no registry is wired or nothing is pending.
	if h.askUserReg != nil {
		if pendingSets := h.askUserReg.PendingAll(); len(pendingSets) > 0 {
			delay := h.askUserReg.EffectiveDefaultSafeDelay()
			for _, set := range pendingSets {
				frame.PendingAsks = append(frame.PendingAsks, toAskUserCard(set, delay))
			}
		}
	}

	raw, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal session_state", "error", err)
		return
	}

	select {
	case wc.sendCh <- raw:
		slog.Debug("ws: session_state emitted", "user_id", wc.userID, "pending", len(pendingApprovals))
	case <-wc.doneCh:
		// Connection closed before we could send — ignore.
	default:
		slog.Warn("ws: session_state dropped — send buffer full", "user_id", wc.userID)
		wc.droppedFrames.Add(1)
	}
}
