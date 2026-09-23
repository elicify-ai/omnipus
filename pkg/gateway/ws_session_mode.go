// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/validation"
)

// wsSessionError sends the session-scoped ErrorFrame a malformed chat
// lifecycle frame gets, and counts the drop.
func (wh *wsHandlerReadLoop) wsSessionError(message string) wsHandlerReadLoopFlow {
	wh.wc.inboundDropped.Add(1)
	sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type:    string(generated.WsFrameTypeError),
		Message: message,
	})
	return wsHandlerReadLoopContinue
}

// handleSessionCloseFrame is FR-023's explicit session close request.
func (wh *wsHandlerReadLoop) handleSessionCloseFrame(data []byte) wsHandlerReadLoopFlow {
	var f generated.SessionCloseFrame
	if err := json.Unmarshal(data, &f); err != nil {
		slog.Warn("ws: malformed session_close frame", "error", err)
		return wsHandlerReadLoopContinue
	}
	if f.SessionId == "" {
		return wh.wsSessionError("session_close requires session_id")
	}
	if err := validation.EntityID(f.SessionId); err != nil {
		return wh.wsSessionError("invalid session_id")
	}
	wh.h.agentLoop.CloseSession(f.SessionId, "explicit")
	sid := f.SessionId
	sendConnGenFrame(wh.wc, string(generated.WsFrameTypeSessionCloseAck), generated.SessionCloseAckFrame{
		Type:      string(generated.WsFrameTypeSessionCloseAck),
		SessionId: f.SessionId,
		Id:        &sid,
	})
	return wsHandlerReadLoopNext
}

// applySessionModeChoice sets sessionID's ADR-092 per-chat Auto-approve
// modifier — or clears it when autoApprove is nil — and audits the mode
// change. Shared by handleSessionModeUpdateFrame below (a live chat's
// explicit toggle) and websocket_chat.go's recordSessionAndTranscript (a
// choice carried on the MessageFrame that mints a brand-new session), so the
// two write-and-audit exactly the same way and can never diverge. agentID is
// caller-resolved: the two sites obtain it differently (an AgentForSession
// lookup for an existing session vs. the already-resolved mint-time target
// agent for a new one). Returns the chat's newly resolved Auto-approve value
// (SessionAutoApprove) for the caller to report or reason about.
func (h *WSHandler) applySessionModeChoice(ctx context.Context, agentID, sessionID string, autoApprove *bool, source string) bool {
	al := h.agentLoop
	modes := al.SessionModes()
	newMode := "cleared"
	if autoApprove == nil {
		modes.ClearSession(sessionID)
	} else {
		modes.Set(sessionID, *autoApprove)
		newMode = shellModeName(*autoApprove)
	}
	effective := al.SessionAutoApprove(agentID, sessionID)
	audit.EmitShellModeChange(ctx, al.AuditLogger(), audit.DecisionAllow,
		newMode, "chat", audit.ShellModeActorOperator, agentID, sessionID, source)
	return effective
}

// handleSessionModeUpdateFrame applies ADR-092's per-chat Auto-approve
// modifier (SessionModeUpdateFrame): auto_approve true/false sets it, null
// clears it so the chat follows the agent and global defaults again. This is
// the one scope the contract lets LOOSEN, because a human is present in the
// chat; it is reachable only from an authenticated browser WebSocket, never
// from an agent tool. Always acknowledged with session_mode_updated carrying
// the chat's resolved Auto-approve value, and audited as a mode change.
func (wh *wsHandlerReadLoop) handleSessionModeUpdateFrame(data []byte) wsHandlerReadLoopFlow {
	var f generated.SessionModeUpdateFrame
	if err := json.Unmarshal(data, &f); err != nil {
		slog.Warn("ws: malformed session_mode_update frame", "error", err)
		return wh.wsSessionError("malformed session_mode_update frame")
	}
	if f.SessionId == "" {
		return wh.wsSessionError("session_mode_update requires session_id")
	}
	if err := validation.EntityID(f.SessionId); err != nil {
		return wh.wsSessionError("invalid session_id")
	}
	// The generated struct flattens the nullable, REQUIRED auto_approve to a
	// plain bool, which cannot tell null (clear) from false (off). Read the
	// raw value to keep the three states the contract defines.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return wh.wsSessionError("malformed session_mode_update frame")
	}
	value, present := raw["auto_approve"]
	if !present {
		return wh.wsSessionError("session_mode_update requires auto_approve (true, false or null)")
	}

	al := wh.h.agentLoop

	// Review finding #9 (LOW, 2026-09-23 security fix lane): reject an
	// unknown session BEFORE writing anything into SessionModeStore. Before
	// this fix, any well-formed session id (including one that never
	// existed, or one that already ended) was accepted, so the per-chat
	// store could grow without bound — every entry lives until
	// ClearSession/session teardown calls it, and neither ever runs for a
	// session id that was never real. Checking AgentForSession first also
	// gives the actual owning agent for the audit event below, rather than
	// silently reporting the global default under an empty agent_id.
	inst, err := al.AgentForSession(f.SessionId)
	if err != nil || inst == nil {
		slog.Warn("ws: session_mode_update for an unresolvable session; rejected",
			"session_id", f.SessionId, "error", err)
		return wh.wsSessionError("session_mode_update: unknown session")
	}
	agentID := inst.ID

	var autoApprove *bool
	if string(value) != "null" {
		autoApprove = &f.AutoApprove
	}
	effective := wh.h.applySessionModeChoice(wh.ctx, agentID, f.SessionId, autoApprove, "session_mode_update")

	sendConnGenFrame(wh.wc, string(generated.WsFrameTypeSessionModeUpdated), generated.SessionModeUpdatedFrame{
		Type:                 string(generated.WsFrameTypeSessionModeUpdated),
		SessionId:            f.SessionId,
		AutoApproveEffective: effective,
	})
	return wsHandlerReadLoopNext
}

// shellModeName is the audit vocabulary for an Auto-approve value.
func shellModeName(autoApprove bool) string {
	if autoApprove {
		return "auto"
	}
	return "ask"
}
