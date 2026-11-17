package gateway

// ws_helper_notices.go: a helper's provider notices (#1537, spec
// helper-model-notices-spec.md "API and Data").
//
// A helper turn's provider_retry / provider_status event carries the
// helper's own chat (ChildSessionID) next to its top chat (SessionID). The
// hub publishes it twice from the one event: to the helper's chat without
// identity fields, and to the top chat with child_session_id and agent_id
// (the named copy). The copies are equal except session_id, the identity
// fields and seq — each hub numbers its own copy. When that helper turn ends
// (finished, failed or stopped), the top chat receives exactly one
// provider_status `ended`, sequenced there, so an incremental catch-up
// replays it. A frame about a chat's own turn is published once, unchanged.

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// providerStatusEnded is the provider_status state of a named copy whose
// helper turn has ended (contract enum value; top-chat copies only).
const providerStatusEnded = "ended"

// helperIdentityMaxLen is the contract's maxLength for child_session_id and
// agent_id.
const helperIdentityMaxLen = 255

// helperNoticeTurn is where one helper turn left its named copies.
type helperNoticeTurn struct {
	top     string // the top chat that received the named copies
	child   string // the helper's own chat
	agentID string // the helper's agent
}

// helperNoticeTurns maps a helper turn id to its named-copy address. The
// zero value is ready to use; every method is safe for concurrent use.
type helperNoticeTurns struct {
	mu    sync.Mutex
	turns map[string]helperNoticeTurn
}

func (t *helperNoticeTurns) note(turnID string, n helperNoticeTurn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.turns == nil {
		t.turns = make(map[string]helperNoticeTurn)
	}
	t.turns[turnID] = n
}

// take removes and returns turnID's entry: a duplicate turn end finds
// nothing, so it can never produce a second `ended`.
func (t *helperNoticeTurns) take(turnID string) (helperNoticeTurn, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, ok := t.turns[turnID]
	if ok {
		delete(t.turns, turnID)
	}
	return n, ok
}

// providerNoticeMarshal renders one copy of a provider notice frame for
// sessionID; child and agentID are nil on every copy except the named one.
type providerNoticeMarshal func(sessionID string, child, agentID *string) ([]byte, error)

// hubPublishProviderNotice publishes one provider_retry / provider_status
// event. sid is the frame's session: the chat's own for an own turn, the
// top chat for a helper turn (child != ""). namedOnly publishes only the
// top chat's named copy (FR-006: a helper's model answered after an
// announced retry without a recovered note). Every copy registers as the
// turn's per-type projection item in its own hub, so a reattaching tab of
// either chat gets the current notice back (FR-008).
func (h *WSHandler) hubPublishProviderNotice(frameType string, evt agent.Event, sid, child string, namedOnly bool, marshal providerNoticeMarshal) {
	meta := hubFrameMeta{kind: hubKindItem, key: frameType + ":" + evt.Meta.TurnID, turnID: evt.Meta.TurnID}
	if child == "" || child == sid {
		if namedOnly {
			return // a chat's own turn keeps its existing rules (FR-003)
		}
		h.hubPublishProviderCopy(frameType, sid, meta, marshal, nil, nil)
		return
	}
	if !namedOnly {
		h.hubPublishProviderCopy(frameType, child, meta, marshal, nil, nil)
	}
	agentID := evt.Meta.AgentID
	if agentID == "" || len(agentID) > helperIdentityMaxLen || len(child) > helperIdentityMaxLen {
		// The contract requires both identity fields within 1..255; a copy
		// without them would be an own frame in the wrong chat.
		logsafeWarn("ws: named helper notice copy not sent: helper identity outside the contract",
			"frame_type", frameType, "turn_id", evt.Meta.TurnID, "session_id", sid,
			"agent_id_len", len(agentID), "child_session_id_len", len(child))
		return
	}
	if h.hubPublishProviderCopy(frameType, sid, meta, marshal, &child, &agentID) {
		h.helperNotices.note(evt.Meta.TurnID, helperNoticeTurn{top: sid, child: child, agentID: agentID})
	}
}

// hubPublishProviderCopy marshals and publishes one copy; it reports whether
// the copy was published.
func (h *WSHandler) hubPublishProviderCopy(frameType, sessionID string, meta hubFrameMeta, marshal providerNoticeMarshal, child, agentID *string) bool {
	data, err := marshal(sessionID, child, agentID)
	if err != nil {
		logsafeError("ws: marshal provider notice for hub failed", "type", frameType, "session_id", sessionID, "error", err)
		return false
	}
	h.hubPublishMetaAlsoTo(sessionID, frameType, meta, data, nil)
	return true
}

// hubHelperNoticeEnded sends the one provider_status `ended` for a helper
// turn that left a named copy in its top chat (FR-012). The frame is
// sequenced in the top chat's hub but is not a projection item: the turn's
// items left the projection with the turn (hubTurnEnd), and a snapshot that
// lacks the row needs no clear.
func (h *WSHandler) hubHelperNoticeEnded(turnID string) {
	n, ok := h.helperNotices.take(turnID)
	if !ok {
		return
	}
	child, agentID := n.child, n.agentID
	frame := generated.ProviderStatusFrame{
		Type:           string(generated.WsFrameTypeProviderStatus),
		SessionId:      n.top,
		TurnId:         turnID,
		State:          providerStatusEnded,
		SentAt:         time.Now().Format(time.RFC3339Nano),
		ChildSessionId: &child,
		AgentId:        &agentID,
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal provider_status ended for hub failed", "session_id", n.top, "error", err)
		return
	}
	h.hubPublishMetaAlsoTo(n.top, string(generated.WsFrameTypeProviderStatus), hubFrameMeta{}, data, nil)
}
