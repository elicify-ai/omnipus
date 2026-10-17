// Omnipus — card-scoped AskUserQuestion error replies (#1343, Decision A)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// sendAskUserActionError replies only to the submitting connection. The
// identifiers describe the submitted action, never whichever queued card
// has since become active. This is not an acceptance or turn-ending signal:
// authoritative card updates determine whether the action was consumed.
// Unusable envelopes receive an ordinary error without card correlation.
func (h *WSHandler) sendAskUserActionError(wc *wsConn, submitted generated.AskUserAnswerFrame, message string) {
	frame := generated.ErrorFrame{
		Type:    string(generated.WsFrameTypeError),
		Message: message,
	}
	if strings.TrimSpace(submitted.CardId) != "" && strings.TrimSpace(submitted.SessionId) != "" {
		sessionID, cardID := submitted.SessionId, submitted.CardId
		frame.SessionId = &sessionID
		frame.AskUserCardId = &cardID
	}
	// Do not publish through the session hub or populate first-send fields:
	// a sibling tab did not submit this action, even if it shows this card.
	sendConnGenFrame(wc, string(generated.WsFrameTypeError), frame)
}
