//go:build !cgo

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
//     Scoped to the authenticated user: admins see all sessions; non-admins see own only.

package gateway

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// broadcastToolApprovalRequired sends a tool_approval_required WS frame to
// connected WebSocket clients scoped to the session's owner (FR-073).
//
// Wire format: generated.ToolApprovalRequiredFrame (contract-first, pkg/api/generated).
// Nil-safety: args MUST be an object (never null). The SPA's ToolApprovalModal calls
// Object.keys(args) directly — null crashes with "null is not an object" (Ava-chat bug).
// When entry.Args is nil, we coerce to map[string]any{} at this site.
//
// Scoping rules:
//   - Admin role: receives the frame (can act on any approval).
//   - Non-admin: receives the frame only if wc.userID matches the session owner.
//     Since session→user ownership is not yet persisted, non-admin clients whose
//     userID was set at WS auth time see approvals for sessions they initiated
//     in the same connection. Admins see all.
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

	h.mu.Lock()
	conns := make([]*wsConn, 0, len(h.sessions))
	for _, wc := range h.sessions {
		conns = append(conns, wc)
	}
	h.mu.Unlock()

	for _, wc := range conns {
		// FR-073: scope approval broadcasts so non-owners do not see args.
		// Admin role always receives. Non-admin receives only when their
		// userID matches the session owner (session ownership via userID field).
		if wc.role != config.UserRoleAdmin && wc.userID != entry.AgentID {
			// entry.AgentID is the best proxy for session ownership until a
			// proper session→userID ownership index is maintained. Non-admin
			// clients for a different agent/user are excluded.
			continue
		}
		select {
		case wc.sendCh <- raw:
		default:
			slog.Warn("ws: tool_approval_required dropped — send buffer full",
				"approval_id", entry.ApprovalID)
			wc.droppedFrames.Add(1)
		}
	}
}

// emitSessionState sends the session_state one-shot frame to a single WS connection
// immediately after authentication (FR-052, FR-073, FR-081).
//
// Wire format: generated.SessionStateFrame (contract-first, pkg/api/generated).
// Nil-safety: pending_approvals MUST be an array (never null). The SPA calls
// pending_approvals.map() — null would crash at render time. Coerced to [] when empty.
//
// Scoping rules (FR-073):
//   - Admin role: sees pending approvals for ALL sessions.
//   - Non-admin: sees only approvals for their own sessions (matched by session.AgentID
//     is unreliable without per-session user tracking; until the session-ownership model
//     is wired by A1, non-admins see their own connection's associated session ID, which
//     may be "" on first connect).
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
		isAdmin := wc.role == config.UserRoleAdmin

		for _, e := range allPending {
			// Admin sees all; non-admin sees only approvals matching their own sessions.
			// Until session ownership is tracked at the WS layer, non-admins see nothing
			// (safe default; they will see their own once session ownership is wired).
			if !isAdmin {
				// FR-073: non-admin scoping. Non-admin clients receive an empty set
				// rather than leaking other users' approval data. A full session→
				// userID ownership index would enable per-user filtering here;
				// until then, non-admin users see nothing (safe default).
				continue
			}
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
