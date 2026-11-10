// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FR-024 (session-core): tell live clients which of their queued messages a
// Stop discarded before delivery.
package gateway

import (
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// dropPendingMessageStatus removes one client_message_id from sessionID's
// pending-"working" queue, so the turn-end flush can never report a discarded
// message as working. It reports whether an entry was removed.
func (h *WSHandler) dropPendingMessageStatus(sessionID, clientMessageID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	queue := h.pendingMessageStatuses[sessionID]
	for i, p := range queue {
		if p.clientMessageID != clientMessageID {
			continue
		}
		rest := append(append([]pendingMessageStatus(nil), queue[:i]...), queue[i+1:]...)
		if len(rest) == 0 {
			delete(h.pendingMessageStatuses, sessionID)
		} else {
			h.pendingMessageStatuses[sessionID] = rest
		}
		return true
	}
	return false
}

// announceDiscardedInput publishes message_status state=discarded
// (reason stopped_before_delivery) to every tab bound to the session for a
// discarded message that carried a client_message_id. The archived message is
// unchanged; reload shows the same label through input_disposition.
func (h *WSHandler) announceDiscardedInput(d agent.DiscardedInput) {
	if h == nil || h.agentLoop == nil {
		return
	}
	clientID := h.clientMessageIDOf(d.SessionID, d.MessageID)
	if clientID == "" {
		return
	}
	h.dropPendingMessageStatus(d.SessionID, clientID)
	reason := session.InputReasonStoppedBeforeDelivery
	h.hubPublishFrame(d.SessionID, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       d.SessionID,
		ClientMessageId: clientID,
		State:           session.InputStateDiscarded,
		Reason:          &reason,
	}, nil)
}

// announceClearNotPerformed publishes message_status state=failed for a /clear
// the server refused or could not carry out (S3-02): the person's /clear row is
// the one message that failed. The explanatory reply stays as the human reason;
// the client reads this frame, not the reply text. A /clear that happened is
// signalled by its marker row, never by this frame.
func (h *WSHandler) announceClearNotPerformed(sessionID, transcriptEntryID string) {
	if h == nil || h.agentLoop == nil {
		return
	}
	clientID, found := h.clientMessageIDLookup(sessionID, transcriptEntryID)
	if clientID == "" {
		// Never silent: the SPA has no timer any more, so without a failed frame
		// the person's /clear row looks like a held one for ever. A client that
		// sent no client_message_id has no correlated row to move, but the
		// refusal text is in the chat and this line is the operator's trace.
		reason := "the /clear row could not be found in the transcript"
		if found {
			reason = "the /clear message carried no client_message_id"
		}
		slog.Warn("ws: a refused /clear could not be framed as failed; its row will look held",
			"session_id", sessionID, "entry_id", transcriptEntryID, "reason", reason)
		return
	}
	h.dropPendingMessageStatus(sessionID, clientID)
	h.heldTickMu.Lock()
	defer h.heldTickMu.Unlock()
	h.setAcceptedMessageState(sessionID, clientID, "failed") // terminal: a held tick after this is dropped
	h.hubPublishFrame(sessionID, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       sessionID,
		ClientMessageId: clientID,
		State:           "failed",
	}, nil)
}

// announceClearHeld publishes message_status state=held for a /clear the server
// has accepted but not yet applied because it is stopping the running reply first
// (founder, option A). It is the same frame and hub path as the @-recipient held
// tick, for the /clear's own row (sessionId + that row's client_message_id), sent
// after "received" and before any "failed" or the clear marker. A /clear that
// already failed is never reported held.
func (h *WSHandler) announceClearHeld(sessionID, transcriptEntryID string) {
	if h == nil || h.agentLoop == nil {
		return
	}
	clientID, found := h.clientMessageIDLookup(sessionID, transcriptEntryID)
	if clientID == "" {
		reason := "the /clear row could not be found in the transcript"
		if found {
			reason = "the /clear message carried no client_message_id"
		}
		slog.Warn("ws: a held /clear could not be framed as held; its row keeps the ordinary look",
			"session_id", sessionID, "entry_id", transcriptEntryID, "reason", reason)
		return
	}
	h.heldTickMu.Lock()
	defer h.heldTickMu.Unlock()
	if h.acceptedMessageState(sessionID, clientID) == "failed" {
		return
	}
	h.setAcceptedMessageState(sessionID, clientID, "held")
	h.hubPublishFrame(sessionID, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       sessionID,
		ClientMessageId: clientID,
		State:           "held",
	}, nil)
}

// releaseClearHeld ends the recorded "held" state of a /clear the server has
// handed on (the stop settled and the /clear is queued as the next turn). Without
// it a client that retries the /clear's id after it was applied would be answered
// "held" again, because nothing else moves a /clear row on. Only a still-held row
// moves, and only to "working" in the record: a not-performed report that raced
// ahead stays the terminal "failed", and no frame is published (the clear marker,
// or the failed frame, is how a live tab learns the outcome).
func (h *WSHandler) releaseClearHeld(sessionID, transcriptEntryID string) {
	if h == nil || h.agentLoop == nil {
		return
	}
	clientID, _ := h.clientMessageIDLookup(sessionID, transcriptEntryID)
	if clientID == "" {
		slog.Warn("ws: a released /clear could not be found; a retry of it would still read as held",
			"session_id", sessionID, "entry_id", transcriptEntryID)
		return
	}
	h.heldTickMu.Lock()
	defer h.heldTickMu.Unlock()
	if h.acceptedMessageState(sessionID, clientID) == "held" {
		h.setAcceptedMessageState(sessionID, clientID, "working")
	}
}

// clientMessageIDOf returns the client_message_id the archived entry messageID
// was persisted with, or "" when it had none or cannot be read.
func (h *WSHandler) clientMessageIDOf(sessionID, messageID string) string {
	id, _ := h.clientMessageIDLookup(sessionID, messageID)
	return id
}

// clientMessageIDLookup is clientMessageIDOf plus whether the entry itself was
// found (found with an empty id means the message carried none).
func (h *WSHandler) clientMessageIDLookup(sessionID, messageID string) (clientID string, found bool) {
	store := h.agentLoop.ResolveSessionStore(sessionID)
	if store == nil {
		return "", false
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		slog.Error("ws: discarded input: could not read the transcript to find the client message id",
			"session_id", sessionID, "error", err)
		return "", false
	}
	for _, e := range entries {
		if e.ID == messageID {
			return e.ClientMessageID, true
		}
	}
	return "", false
}
