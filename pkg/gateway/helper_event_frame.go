package gateway

import (
	"encoding/json"

	"github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func (h *WSHandler) hubHelperEvent(evt agent.Event) {
	frame, ok := evt.Payload.(generated.HelperEventFrame)
	if !ok || frame.SessionId == "" || frame.MessageId == "" {
		return
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal helper_event for hub failed", "session_id", frame.SessionId, "error", err)
		return
	}
	h.hubBroadcastWithSequencedCopy(frame.SessionId, string(generated.WsFrameTypeHelperEvent), data)
}
