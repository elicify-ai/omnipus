package gateway

import (
	"errors"
	"os"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// acknowledgeNewSession binds the sender before its first echo and supplies
// the initial cursor. Ordinary first sends call it only after a durable append;
// workspace kickoffs and ADR-066 oversized-input refusals keep their old order.
func (hcm *wsHandlerHandleChatMessage) acknowledgeNewSession() {
	hcm.h.mu.Lock()
	hcm.h.sessionIDs[hcm.chatID] = hcm.sessionID
	startHead := hcm.h.bindConnToSessionHubLocked(hcm.wc, hcm.sessionID)
	hcm.h.mu.Unlock()

	started := generated.SessionStartedFrame{
		Type:      string(generated.WsFrameTypeSessionStarted),
		SessionId: hcm.sessionID,
	}
	if hcm.firstMessage && hcm.clientMessageID != "" {
		cid := hcm.clientMessageID
		started.ClientMessageId = &cid
	}
	if hcm.h.hubs != nil && startHead > 0 {
		seq := int64(startHead)
		bootID := hcm.h.hubs.bootID
		started.Seq = &seq
		started.BootId = &bootID
	}
	if hcm.targetAgentID != "" {
		aid := hcm.targetAgentID
		started.AgentId = &aid
	}
	sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeSessionStarted), started)
}

// firstMessageAppendFailure distinguishes failures proven to precede the
// transcript write from uncertain writes. AppendTranscript wraps AppendJSONL:
// mkdir/open failures happen before any record bytes, while write, sync, and
// close failures can follow bytes reaching disk. Unknown errors stay uncertain.
func firstMessageAppendFailure(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && (pathErr.Op == "mkdir" || pathErr.Op == "open") {
		return "not_saved"
	}
	return "delivery_unknown"
}

// sendFirstMessageError emits no filesystem details and only tags an
// ID-bearing first send. A confirmed save names its actual session; an unsaved
// or uncertain append never makes that session look acknowledged to the SPA.
func (hcm *wsHandlerHandleChatMessage) sendFirstMessageError(kind string) {
	message := "Could not save message"
	switch kind {
	case "delivery_unknown":
		message = "Delivery not confirmed"
	case "answer_not_started":
		message = "Message saved, but no answer started"
	}
	frame := generated.ErrorFrame{
		Type:    string(generated.WsFrameTypeError),
		Message: message,
	}
	if hcm.clientMessageID != "" {
		cid := hcm.clientMessageID
		frame.ClientMessageId = &cid
		frame.FirstMessageError = &kind
	}
	if kind == "answer_not_started" {
		sid := hcm.sessionID
		frame.SessionId = &sid
	}
	sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), frame)
}
