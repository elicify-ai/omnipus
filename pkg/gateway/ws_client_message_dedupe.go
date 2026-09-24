// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_client_message_dedupe.go: idempotent message intake by
// client_message_id (#823 review item 6).
//
// A client re-sends a message when it never saw the server's acknowledgement
// (the connection dropped between send and "received"). If the first copy did
// reach the server, handling the retry as new would persist a second user
// message and start a second turn. The gateway remembers, per session, the
// client message ids it has accepted, and answers a retry by re-sending that
// message's echo and current status to the sender only, unsequenced — no new
// entry, no new turn, nothing re-published to the session. No wire shape
// changes: the retry is answered with the existing user_message and
// message_status frames.
package gateway

import (
	"encoding/json"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// acceptedClientMessagesPerSession bounds the remembered ids per session.
// A retry only ever concerns the last few messages a client sent.
const acceptedClientMessagesPerSession = 256

// acceptedClientMessage is one accepted message: its unsequenced
// user_message echo and the last status the session was told about.
type acceptedClientMessage struct {
	echo  []byte
	state string // received | working
}

// acceptedClientMessages is guarded by WSHandler.mu.
type acceptedClientMessages struct {
	byID  map[string]*acceptedClientMessage
	order []string
}

func (h *WSHandler) acceptedLocked(sessionID string) *acceptedClientMessages {
	if h.acceptedClientMsgs == nil {
		h.acceptedClientMsgs = make(map[string]*acceptedClientMessages)
	}
	a := h.acceptedClientMsgs[sessionID]
	if a == nil {
		a = &acceptedClientMessages{byID: make(map[string]*acceptedClientMessage)}
		h.acceptedClientMsgs[sessionID] = a
	}
	return a
}

// rememberAcceptedMessage records a persisted message under its client id.
func (h *WSHandler) rememberAcceptedMessage(sessionID, clientMessageID string, echo []byte) {
	if sessionID == "" || clientMessageID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.acceptedLocked(sessionID)
	if _, ok := a.byID[clientMessageID]; !ok {
		a.order = append(a.order, clientMessageID)
	}
	a.byID[clientMessageID] = &acceptedClientMessage{echo: echo, state: "received"}
	for len(a.order) > acceptedClientMessagesPerSession {
		delete(a.byID, a.order[0])
		a.order = a.order[1:]
	}
}

// forgetAcceptedMessage drops a message that ended up not admitted (its
// publish failed and the client was told "failed"), so a retry is handled as
// a fresh send.
func (h *WSHandler) forgetAcceptedMessage(sessionID, clientMessageID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.acceptedClientMsgs[sessionID]; a != nil {
		delete(a.byID, clientMessageID)
	}
}

// markAcceptedMessageWorkingLocked records that the message's turn started.
// Caller holds h.mu.
func (h *WSHandler) markAcceptedMessageWorkingLocked(sessionID, clientMessageID string) {
	if a := h.acceptedClientMsgs[sessionID]; a != nil {
		if m := a.byID[clientMessageID]; m != nil {
			m.state = "working"
		}
	}
}

func (h *WSHandler) markAcceptedMessageWorking(sessionID, clientMessageID string) {
	h.mu.Lock()
	h.markAcceptedMessageWorkingLocked(sessionID, clientMessageID)
	h.mu.Unlock()
}

// answerRetriedMessage handles an inbound message whose client id this
// session already accepted: it re-sends the original echo and the current
// status to the sender (unsequenced) and reports true. False means the id is
// new and the message must be processed normally.
func (hcm *wsHandlerHandleChatMessage) answerRetriedMessage() bool {
	if hcm.clientMessageID == "" || hcm.sessionID == "" {
		return false
	}
	h := hcm.h
	h.mu.Lock()
	var echo []byte
	var state string
	if a := h.acceptedClientMsgs[hcm.sessionID]; a != nil {
		if m := a.byID[hcm.clientMessageID]; m != nil {
			echo, state = m.echo, m.state
		}
	}
	h.mu.Unlock()
	if state == "" {
		return false
	}
	slog.Info("ws: retried client_message_id answered without a second turn",
		"session_id", hcm.sessionID, "client_message_id", hcm.clientMessageID, "state", state)
	if echo != nil {
		sendRawFrameBytes(hcm.wc, string(generated.WsFrameTypeUserMessage), echo)
	}
	status, err := json.Marshal(generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       hcm.sessionID,
		ClientMessageId: hcm.clientMessageID,
		State:           state,
	})
	if err == nil {
		sendRawFrameBytes(hcm.wc, string(generated.WsFrameTypeMessageStatus), status)
	}
	return true
}
