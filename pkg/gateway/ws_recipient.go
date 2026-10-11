// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"errors"
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
func (h *WSHandler) refuseRecipient(ctx context.Context, sessionID string, setupKickoff bool, to addressing.Pair) (refusal string, askApproved bool) {
	if strings.TrimSpace(sessionID) == "" {
		return "a request to another agent needs an existing chat (session_id)", false
	}
	if setupKickoff {
		return "a workspace setup message cannot be addressed to another agent", false
	}
	if err := to.Validate(); err != nil {
		return "the addressed agent is not a valid {workspace_id, agent_id} pair", false
	}
	// F7: validate the COMPLETE computed address (the main's session id, which
	// is bounded to 255 bytes) before anything is saved - the components can
	// each be valid and the joined id still too long.
	if _, err := session.MainSessionID(to.WorkspaceID, to.AgentID); err != nil {
		return "the addressed agent's address is too long to be reached", false
	}
	store := h.resolveSessionStore(sessionID)
	if store == nil {
		return "session not found", false
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return "session not found", false
	}
	if to.AgentID == meta.AgentID && (meta.WorkspaceID == "" || to.WorkspaceID == meta.WorkspaceID) {
		return "this chat already belongs to that agent; write to it directly instead of addressing it", false
	}
	approved, err := h.agentLoop.CheckPeerAdmissionDecision(ctx, to, sessionID)
	if err != nil {
		logsafeWarn("ws: recipient refused", "session_id", sessionID, "workspace_id", to.WorkspaceID,
			"agent_id", to.AgentID, "error", err)
		return "that agent cannot be reached from here right now", false
	}
	return "", approved
}

// admitRecipientRequest delivers the already-saved user message to the
// recipient's main as a request whose answer returns to this chat. The source
// owner is the chat's own agent; the sender is the authenticated connection
// principal (empty stays empty — anonymous/shared auth is never turned into a
// human).
func (hcm *wsHandlerHandleChatMessage) admitRecipientRequest() {
	owner := addressing.Pair{}
	var ownerAgent string
	// The source is saved only inside AdmitRequest's BeforeCommit, so until then
	// the session is the one the frame names (a recipient needs an existing chat).
	srcID := hcm.frameSessionID
	var meta *session.UnifiedMeta
	var metaErr error
	if srcStore := hcm.h.resolveSessionStore(srcID); srcStore != nil {
		meta, metaErr = srcStore.GetMeta(srcID)
	} else {
		metaErr = errors.New("source session store not found")
	}
	if metaErr != nil || meta == nil {
		// The source chat's owner cannot be read, so no capture can name it:
		// fail closed rather than record an empty owner that skips the
		// return-time binding check.
		logsafeWarn("ws: could not read the source chat to bind the request", "session_id", srcID, "error", metaErr)
		hcm.h.sendRecipientRefusal(hcm.wc, srcID, hcm.clientMessageID, "the request could not be delivered to that agent")
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
	held, err := hcm.h.agentLoop.AdmitRequestReportingHeld(hcm.ctx, agent.RequestAdmission{
		Receiver:    *hcm.recipient,
		Content:     hcm.content,
		Sender:      addressing.Sender{Principal: principalOf(hcm)},
		SenderLabel: label,
		Source: addressing.Source{
			Kind:      addressing.SourceConversation,
			Owner:     owner,
			SessionID: srcID,
		},
		AskApproved: hcm.recipientAskApproved,
		// U8 r4 F12: every receiver authorization decision (including a late
		// Ask and its approval wait) is finished before this runs; only then is
		// the user's message saved and echoed, then the receiver's side written.
		BeforeCommit: func() error {
			if hcm.prepareMessage() {
				return errRecipientSourceRefused // prepareMessage already told the sender why
			}
			if hcm.transcriptPersisted {
				hcm.sendMessageStatus("received")
			}
			return nil
		},
	})
	if errors.Is(err, errRecipientSourceRefused) {
		return
	}
	if err != nil {
		logsafeWarn("ws: could not deliver the request to the recipient", "session_id", srcID, "error", err)
		if hcm.sessionID == "" {
			hcm.h.sendRecipientRefusal(hcm.wc, srcID, hcm.clientMessageID, "the request could not be delivered to that agent")
		} else {
			// The message was already saved and echoed; the deferred failed tick
			// (hcm.sessionID is set) reconciles the row after this error.
			hcm.h.sendRecipientRefusalError(hcm.wc, srcID, hcm.clientMessageID, "the request could not be delivered to that agent")
		}
		return
	}
	hcm.admitted = true
	if held {
		// The recipient is stopped: the request waits until it runs again. The
		// sender's row says so (after "received"); a held request is not a
		// failure and no later tick reports it as one.
		hcm.sendMessageStatus("held")
	}
}

// sendRecipientRefusalError sends the message-scoped refusal of an @-recipient
// send to the sender's connection (delta-review C2; contract ErrorFrame). It is
// tagged with the sender's chat (sessionID) and the message's client_message_id
// so the SPA files the notice in that chat, reconciles the row, and does NOT
// treat it as the end of the chat's running turn. Tags are set only when the
// chat is known (a refusal for a missing session_id stays global) and
// client_message_id only together with the chat.
func (h *WSHandler) sendRecipientRefusalError(wc *wsConn, sessionID, clientMessageID, message string) {
	frame := generated.ErrorFrame{
		Type:    string(generated.WsFrameTypeError),
		Message: message,
	}
	if sessionID != "" {
		sid := sessionID
		frame.SessionId = &sid
		if clientMessageID != "" {
			cid := clientMessageID
			frame.ClientMessageId = &cid
		}
	}
	sendConnGenFrame(wc, string(generated.WsFrameTypeError), frame)
}

// sendRecipientRefusal is sendRecipientRefusalError followed by the sender
// row's message_status "failed" tick, published through the session hub (the
// sender's tab also gets it when it is not bound), in the same error-then-tick
// order as websocket_chat.go::sendAppendFailureError. The refusal runs before
// the message is saved, so the deferred tick of the intake is not armed and
// the tick is published here. No tick without both ids.
func (h *WSHandler) sendRecipientRefusal(wc *wsConn, sessionID, clientMessageID, message string) {
	h.sendRecipientRefusalError(wc, sessionID, clientMessageID, message)
	if sessionID == "" || clientMessageID == "" {
		return
	}
	h.hubPublishFrame(sessionID, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       sessionID,
		ClientMessageId: clientMessageID,
		State:           "failed",
	}, wc)
}

// errRecipientSourceRefused marks a BeforeCommit abort whose user-facing error
// the source preparation has already sent.
var errRecipientSourceRefused = errors.New("recipient request: source message refused")

func principalOf(hcm *wsHandlerHandleChatMessage) string {
	if hcm.wc == nil {
		return ""
	}
	return hcm.wc.userID
}
