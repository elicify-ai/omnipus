// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// hubRefusedInput forwards an agent error that refuses ONE person's message
// and ends no turn (agent.ErrorPayload.RefusedInput, SFH-R5-2): the error
// frame carries the session and that message's client message id, so every
// view of the chat takes the SPA's message-scoped branch (C2) and a turn
// still streaming in another view keeps its answer; the message's row then
// gets its failed tick, once. The frame is published outside the active-turn
// projection: it belongs to no turn. A coded refusal goes through
// WireLLMError like any typed error; an uncoded one is an Omnipus sentence
// (the not-saved notice) and is sent as written.
//
// A message whose saved row carries no client message id cannot be scoped by
// that id, and a session-only error frame would end (and relabel) a turn still
// streaming in a view of the chat (SFH-R5-2). Its refusal is therefore sent as
// a system notice (replay_message, role system) — the existing non-terminal
// carrier the SPA shows as its own row; it neither ends the turn nor replaces
// its answer, but like any live system notice it bakes a view's tool calls
// still in flight into their message
// (replay-and-status-frames.ts::bakePendingNonAssistantToolCalls) — and the
// reason it could not be scoped is logged. No client id is ever invented.
func (h *WSHandler) hubRefusedInput(sessionID string, p agent.ErrorPayload) {
	clientID, found := "", false
	if p.RefusedInputEntryID != "" {
		clientID, found = h.clientMessageIDLookup(sessionID, p.RefusedInputEntryID)
	}
	frame := generated.ErrorFrame{
		Type:      string(generated.WsFrameTypeError),
		SessionId: &sessionID,
		Message:   p.Message,
	}
	if p.Code != "" {
		wire := agent.WireLLMError(p)
		frame.Message = wire.Message
		frame.Payload = &generated.ErrorPayload{LlmError: wire}
	}
	if clientID == "" {
		reason := "the refused message has no saved row"
		if found {
			reason = "the refused message carried no client_message_id"
		}
		logsafeWarn("ws: a refusal of one message could not be scoped to it; sent as a chat notice",
			"session_id", sessionID, "entry_id", p.RefusedInputEntryID, "reason", reason)
		noticeID := uuid.NewString()
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		h.hubPublishFrame(sessionID, string(generated.WsFrameTypeReplayMessage), generated.ReplayMessageFrame{
			Type: string(generated.WsFrameTypeReplayMessage), SessionId: sessionID,
			Id: &noticeID, Role: "system", Content: frame.Message, Timestamp: &stamp,
		}, nil)
		return
	}
	frame.ClientMessageId = &clientID
	h.hubPublishFrame(sessionID, string(generated.WsFrameTypeError), frame, nil)
	h.markRefusedInputFailed(sessionID, clientID)
}

// markRefusedInputFailed moves a refused message's row to "failed": its
// pending "working" tick is dropped (so a later turn's flush cannot relabel
// it), the recorded state answers a retry of that id, and the tick is
// published once — a second refused-input frame for the same message (the
// not-saved notice) does not repeat it.
func (h *WSHandler) markRefusedInputFailed(sessionID, clientID string) {
	h.dropPendingMessageStatus(sessionID, clientID)
	h.heldTickMu.Lock()
	defer h.heldTickMu.Unlock()
	if h.acceptedMessageState(sessionID, clientID) == "failed" {
		return
	}
	h.setAcceptedMessageState(sessionID, clientID, "failed")
	h.hubPublishFrame(sessionID, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       sessionID,
		ClientMessageId: clientID,
		State:           "failed",
	}, nil)
}
