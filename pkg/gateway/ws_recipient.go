// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// refuseRecipient runs the WHOLE admission rule for a MessageFrame.recipient
// before anything is saved, and returns the user-facing refusal ("" = admit).
// A refused recipient admits nothing — not the request and not the user's
// message — so the chat never shows an @-request that was never delivered.
//
// Rules (session-core C-ADDRESS, FR-045; contract MessageFrame.recipient):
//   - valid only together with session_id (the owning chat);
//   - never on a workspace-setup kickoff frame;
//   - a well-formed pair that resolves to an eligible main NOW;
//   - not the owning chat's own pair;
//   - the receiver's existing send_message policy admits it (Ask uses the
//     existing approval path).
func (h *WSHandler) refuseRecipient(ctx context.Context, sessionID string, setupKickoff bool, to addressing.Pair) string {
	if strings.TrimSpace(sessionID) == "" {
		return "a request to another agent needs an existing chat (session_id)"
	}
	if setupKickoff {
		return "a workspace setup message cannot be addressed to another agent"
	}
	if err := to.Validate(); err != nil {
		return "the addressed agent is not a valid {workspace_id, agent_id} pair"
	}
	// F7: validate the COMPLETE computed address (the main's session id, which
	// is bounded to 255 bytes) before anything is saved - the components can
	// each be valid and the joined id still too long.
	if _, err := session.MainSessionID(to.WorkspaceID, to.AgentID); err != nil {
		return "the addressed agent's address is too long to be reached"
	}
	store := h.resolveSessionStore(sessionID)
	if store == nil {
		return "session not found"
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return "session not found"
	}
	if to.AgentID == meta.AgentID && (meta.WorkspaceID == "" || to.WorkspaceID == meta.WorkspaceID) {
		return "this chat already belongs to that agent; write to it directly instead of addressing it"
	}
	if err := h.agentLoop.CheckPeerAdmission(ctx, to, sessionID); err != nil {
		logsafeWarn("ws: recipient refused", "session_id", sessionID, "workspace_id", to.WorkspaceID,
			"agent_id", to.AgentID, "error", err)
		return "that agent cannot be reached from here right now"
	}
	return ""
}

// admitRecipientRequest delivers the already-saved user message to the
// recipient's main as a request whose answer returns to this chat. The source
// owner is the chat's own agent; the sender is the authenticated connection
// principal (empty stays empty — anonymous/shared auth is never turned into a
// human).
func (hcm *wsHandlerHandleChatMessage) admitRecipientRequest() {
	owner := addressing.Pair{}
	ownerAgent := hcm.targetAgentID
	meta, metaErr := hcm.store.GetMeta(hcm.sessionID)
	if metaErr != nil || meta == nil {
		// The source chat's owner cannot be read, so no capture can name it:
		// fail closed rather than record an empty owner that skips the
		// return-time binding check.
		logsafeWarn("ws: could not read the source chat to bind the request", "session_id", hcm.sessionID, "error", metaErr)
		sid := hcm.sessionID
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			Message:   "the request could not be delivered to that agent",
			SessionId: &sid,
		})
		return
	}
	{
		ownerAgent = meta.AgentID
		if meta.WorkspaceID != "" && meta.AgentID != "" {
			owner = addressing.Pair{WorkspaceID: meta.WorkspaceID, AgentID: meta.AgentID}
		}
	}
	label := "the user"
	if hcm.wc != nil && hcm.wc.userID != "" {
		label = "user " + hcm.wc.userID
	}
	if ownerAgent != "" {
		label = fmt.Sprintf("%s in %s's chat", label, ownerAgent)
	}
	_, _, err := hcm.h.agentLoop.AdmitRequest(hcm.ctx, agent.RequestAdmission{
		Receiver:    *hcm.recipient,
		Content:     hcm.content,
		Sender:      addressing.Sender{Principal: principalOf(hcm)},
		SenderLabel: label,
		Source: addressing.Source{
			Kind:      addressing.SourceConversation,
			Owner:     owner,
			SessionID: hcm.sessionID,
		},
	})
	if err != nil {
		logsafeWarn("ws: could not deliver the request to the recipient", "session_id", hcm.sessionID, "error", err)
		sid := hcm.sessionID
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			Message:   "the request could not be delivered to that agent",
			SessionId: &sid,
		})
		return
	}
	hcm.admitted = true
}

func principalOf(hcm *wsHandlerHandleChatMessage) string {
	if hcm.wc == nil {
		return ""
	}
	return hcm.wc.userID
}
