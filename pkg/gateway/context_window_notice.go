package gateway

import "github.com/elicify-ai/omnipus/pkg/agent"

// hubContextWindowNotice delivers the persisted diagnostic once per retry
// event, independently of how many browser tabs are attached or Verbose is set.
func (h *WSHandler) hubContextWindowNotice(evt agent.Event) {
	payload, ok := evt.Payload.(agent.LLMRetryPayload)
	if !ok || payload.ContextWindowNotice == nil {
		return
	}
	frame := *payload.ContextWindowNotice
	h.hubPublishFrame(frame.SessionId, frame.Type, frame, nil)
}
