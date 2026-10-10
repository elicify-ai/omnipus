// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// gatewayAddressDeps is the gateway half of session-core U8's peer/reply
// routing (agent.AddressDeps): who is entitled to a main, and live delivery of
// a guest reply. It adds no policy of its own.
type gatewayAddressDeps struct{ h *WSHandler }

var _ agent.AddressDeps = gatewayAddressDeps{}

// PairEligible applies the same rule that decides whether a pair owns a main
// at all (mainSessionPairEligible): current member (Admin: default workspace
// only) and a chat target. A missing workspace is "not eligible"; an
// unreadable one is an error, so a read failure never admits a request.
func (d gatewayAddressDeps) PairEligible(workspaceID, agentID string) (bool, error) {
	ws, err := readWorkspaceFile(d.h.home, workspaceID)
	if err != nil {
		if errors.Is(err, errWorkspaceNotFound) {
			return false, nil
		}
		return false, err
	}
	return mainSessionPairEligible(d.h.agentLoop.GetConfig(), ws, agentID), nil
}

// PublishGuestReply publishes a persisted guest reply to every tab bound to
// the session as one token + done pair carrying the guest author and the id of
// the request it answers (the same values replay emits from the entry).
func (d gatewayAddressDeps) PublishGuestReply(sessionID string, entry session.TranscriptEntry) {
	if entry.Content == "" {
		return // silence creates no empty bubble
	}
	messageID, agentID, replyTo := entry.ID, entry.AgentID, entry.ReplyToMessageID
	token := generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		SessionId: sessionID,
		Content:   entry.Content,
		MessageId: &messageID,
	}
	if agentID != "" {
		token.AgentId = &agentID
	}
	if replyTo != "" {
		token.ReplyToMessageId = &replyTo
	}
	d.h.hubPublishFrameMeta(sessionID, string(generated.WsFrameTypeToken), hubFrameMeta{
		kind: hubKindToken, messageID: messageID, agentID: agentID, content: entry.Content,
	}, token)
	done := generated.DoneFrame{
		Type:      string(generated.WsFrameTypeDone),
		SessionId: sessionID,
		MessageId: &messageID,
	}
	d.h.hubPublishFrameMeta(sessionID, string(generated.WsFrameTypeDone), hubFrameMeta{kind: hubKindDone}, done)
}
