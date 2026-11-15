package gateway

import "github.com/elicify-ai/omnipus/pkg/agent"

// hubLLMRetry routes the agent loop's own retry events. A context-window
// retry carries its persisted diagnostic (hubContextWindowNotice). The loop's
// timeout retry of a call that sent no response, or of an empty stream (issue
// #1664 B5), carries the retry's scheduled time and goes out as one
// provider_retry frame (hubProviderRetry). Every other loop retry reason —
// streaming_reset, empty_response, the truncation repairs, and a retry
// without a scheduled time — stays unforwarded (provider-messages §13).
func (h *WSHandler) hubLLMRetry(evt agent.Event) {
	payload, ok := evt.Payload.(agent.LLMRetryPayload)
	if !ok {
		return // as before #1664: hubContextWindowNotice ignored it too
	}
	if payload.ContextWindowNotice != nil {
		h.hubContextWindowNotice(evt)
		return
	}
	if (payload.Reason == "timeout" || payload.Reason == "empty_reply") && !payload.RetryAt.IsZero() {
		h.hubProviderRetry(evt)
	}
}

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
