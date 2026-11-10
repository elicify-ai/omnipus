package gateway

import (
	"encoding/json"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/validation"
)

func (wh *wsHandlerReadLoop) handleTurnControlFrame(data []byte, frameType string) wsHandlerReadLoopFlow {
	if frameType == string(generated.WsFrameTypeRedirect) {
		return wh.handleRedirectFrame(data)
	}
	return wh.handleCancelFrame(data)
}

// handleRedirectFrame bypasses ordinary message intake, so the session's live
// turn (a chat's or a helper's) cannot consume the command as steering text
// before it has stopped.
func (wh *wsHandlerReadLoop) handleRedirectFrame(data []byte) wsHandlerReadLoopFlow {
	var frame generated.RedirectFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return wh.rejectRedirectFrame("malformed redirect frame", "")
	}
	if err := validation.EntityID(frame.SessionId); err != nil {
		return wh.rejectRedirectFrame("redirect requires a valid session_id", frame.SessionId)
	}
	instruction := strings.TrimSpace(frame.Instruction)
	if instruction == "" {
		return wh.rejectRedirectFrame(commands.StopRedirectUsage, frame.SessionId)
	}
	// The already-approved RedirectFrame contract explicitly counts UTF-8
	// bytes at runtime; the JSON schema's maxLength counts characters.
	if len(frame.Instruction) > 16384 {
		return wh.rejectRedirectFrame("redirect instruction exceeds 16384 UTF-8 bytes", frame.SessionId)
	}
	if strings.TrimSpace(wh.wc.userID) == "" {
		return wh.rejectRedirectFrame("redirect requires an authenticated sender", frame.SessionId)
	}
	if wh.h.agentLoop == nil {
		return wh.rejectRedirectFrame("redirect is unavailable; no instruction was applied", frame.SessionId)
	}
	err := wh.h.agentLoop.RedirectSessionTurn(wh.ctx, frame.SessionId, instruction, wh.wc.userID, "web")
	if err != nil {
		return wh.rejectRedirectFrame(commands.StopRedirectReply(err), frame.SessionId)
	}
	return wsHandlerReadLoopNext
}

func (wh *wsHandlerReadLoop) rejectRedirectFrame(message, sessionID string) wsHandlerReadLoopFlow {
	wh.wc.inboundDropped.Add(1)
	var id *string
	if sessionID != "" {
		id = &sessionID
	}
	sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type: string(generated.WsFrameTypeError), Message: message, SessionId: id,
	})
	return wsHandlerReadLoopContinue
}
