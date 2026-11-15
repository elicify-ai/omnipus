package gateway

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// hubProviderStatus forwards the agent's provider-call state (issues #1478
// and #1280) as one provider_status frame built from the payload's named
// fields only. Like provider_retry it registers as ONE per-turn projection
// item: a newer state for the turn replaces the previous frame, so a tab
// attached mid-silence sees the live notice, and the item leaves with the
// turn. Optional facts are omitted when unknown, never sent as zero times.
func (h *WSHandler) hubProviderStatus(evt agent.Event) {
	p, ok := evt.Payload.(agent.ProviderStatusPayload)
	if !ok {
		// Issue #1664 B11: a dropped notice is never silent.
		logsafeWarn("ws: provider_status dropped: unexpected payload type",
			"payload_type", fmt.Sprintf("%T", evt.Payload), "turn_id", evt.Meta.TurnID, "session_key", evt.Meta.SessionKey)
		return
	}
	sid := p.SessionID
	if sid == "" {
		sid = h.hubResolveSessionIDForChat(evt.Meta.SessionKey)
	}
	if sid == "" {
		// Issue #1664 B12: named fields only — never provider text.
		logsafeWarn("ws: provider_status dropped: no session for chat",
			"state", p.State, "turn_id", evt.Meta.TurnID, "session_key", evt.Meta.SessionKey)
		return
	}
	frame := generated.ProviderStatusFrame{
		Type:      string(generated.WsFrameTypeProviderStatus),
		SessionId: sid,
		TurnId:    evt.Meta.TurnID,
		State:     p.State,
		SentAt:    p.SentAt.Format(time.RFC3339Nano),
	}
	if p.State == agent.ProviderStatusWaiting {
		since := p.SilentSince.Format(time.RFC3339Nano)
		frame.SilentSince = &since
		if !p.StallAt.IsZero() {
			stall := p.StallAt.Format(time.RFC3339Nano)
			frame.StallAt = &stall
		}
		// Issue #1664 B3: "attempt N of M", when the loop counts the call.
		if p.Attempt > 0 && p.MaxAttempts > 0 {
			attempt, maxAttempts := p.Attempt, p.MaxAttempts
			frame.Attempt = &attempt
			frame.MaxAttempts = &maxAttempts
		}
	}
	if p.State == agent.ProviderStatusRecovered && p.Retries > 0 {
		retries := p.Retries
		frame.Retries = &retries
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal provider_status for hub failed", "session_id", sid, "error", err)
		return
	}
	h.hubPublishMetaAlsoTo(sid, string(generated.WsFrameTypeProviderStatus), hubFrameMeta{
		kind:   hubKindItem,
		key:    "provider_status:" + evt.Meta.TurnID,
		turnID: evt.Meta.TurnID,
	}, data, nil)
}
