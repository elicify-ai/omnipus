// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// WebSocket events for the Central Tool Registry redesign (A3 lane).
//
// Emits two event types:
//
//  1. tool_approval_required (FR-011, FR-082)
//     Sent to the connected WS clients of the account whose agent paused on
//     an ask-policy tool call (UAT 2026-09-13 D-16; approvalVisibleTo).
//     Uses expires_in_ms (not expires_at) per OBS-004.
//
//  2. session_state (FR-052, FR-073, FR-081)
//     One-shot per WS connection on every reconnect. Carries the pending
//     approvals visible to the connection's account (approvalVisibleTo).

package gateway

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// approvalAudienceOwnerFn, when set, replaces the session-store lookup in
// approvalOwner. Tests inject it; production leaves it nil.
type approvalAudienceOwnerFn func(sessionID string) string

// approvalOwner resolves the account an approval belongs to: the Owner
// stamped on the ACTING session's meta at creation (websocket.go stamps
// wc.userID, the WS-authenticated identity; a delegated child inherits its
// parent's Owner, FR-082). Returns "" when the owner cannot be determined —
// a channel-originated or CLI session, a store that cannot be resolved, a
// dev-bypass connection that never had an identity — and "" means "everyone
// may see it": an approval nobody can see is a hung turn, which is worse than
// an over-broad audience on an install that has no accounts to separate.
func (h *WSHandler) approvalOwner(sessionID string) string {
	if h.approvalOwnerFn != nil {
		return h.approvalOwnerFn(sessionID)
	}
	if h.agentLoop == nil || sessionID == "" {
		return ""
	}
	store := h.agentLoop.ResolveSessionStore(sessionID)
	if store == nil {
		return ""
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return ""
	}
	return meta.Owner
}

// approvalVisibleTo is the audience rule (UAT 2026-09-13 D-16): an approval
// raised by one account's agent is shown to THAT account's connections only.
// Before this, every connected client received every approval, so a
// request_mount for a host path raised in one account rendered as a blocking,
// approvable modal in every tab of a different signed-in account — and
// because the dialog's Close was a deny (D-90), an operator setting a
// foreign prompt aside answered someone else's decision.
//
// An owner of "" (unknown) and a connection user of "" (dev-mode bypass, no
// identity) both widen to "visible": neither side has an account to scope
// by, and hiding the approval would leave the agent blocked with nobody
// able to answer.
func approvalVisibleTo(owner, connUser string) bool {
	return owner == "" || connUser == "" || owner == connUser
}

// approvalFrameSendTimeout bounds how long broadcastToolApprovalRequired waits
// for a connection's send buffer to drain before giving up on it. The frame
// used to be dropped instantly when the buffer was full ("best-effort"); a
// dropped approval frame is a modal that never appears while the agent sits
// blocked for the whole approval window, which is the shape UAT 2026-09-13
// D-82 reported ("denied with no modal ever shown; only a reload brought it
// back"). It is treated as a critical frame now, like "done" and "error".
const approvalFrameSendTimeout = 2 * time.Second

// broadcastToolApprovalRequired sends a tool_approval_required WS frame to
// every connected WebSocket client of the account that owns the acting
// session (FR-073; see approvalVisibleTo for the audience rule and its
// fallbacks).
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

	owner := h.approvalOwner(entry.SessionID)
	h.mu.Lock()
	conns := make([]*wsConn, 0, len(h.sessions))
	for _, wc := range h.sessions {
		if approvalVisibleTo(owner, wc.userID) {
			conns = append(conns, wc)
		}
	}
	h.mu.Unlock()
	if len(conns) == 0 {
		slog.Warn("ws: tool_approval_required has no connected audience — the request will wait for a reconnect (session_state) or time out",
			"approval_id", entry.ApprovalID, "owner", owner, "tool", entry.ToolName)
	}
	for _, wc := range conns {
		go sendApprovalFrame(wc, raw, entry.ApprovalID)
	}
}

// sendApprovalFrame delivers one approval frame to one connection, waiting up
// to approvalFrameSendTimeout for buffer space instead of dropping on a full
// buffer (D-82). A connection that closes meanwhile is skipped silently; one
// that stays full past the timeout is logged loudly, because that is the one
// case left where a human is never asked.
func sendApprovalFrame(wc *wsConn, raw []byte, approvalID string) {
	select {
	case wc.sendCh <- raw:
	case <-wc.doneCh:
	case <-time.After(approvalFrameSendTimeout):
		slog.Warn("ws: tool_approval_required dropped — send buffer full past timeout",
			"approval_id", approvalID, "user_id", wc.userID)
		wc.droppedFrames.Add(1)
	}
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

		// D-16: the reconnect snapshot follows the same audience rule as the
		// live frame — a tab reconnecting under account B must not re-hydrate
		// account A's pending approvals as blocking stubs.
		for _, e := range allPending {
			if !approvalVisibleTo(h.approvalOwner(e.SessionID), wc.userID) {
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
