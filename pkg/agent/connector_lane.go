package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// Per-chat ordering of bound connector input (founder ruling on U8 r2 F5:
// within one chat messages are handled strictly in arrival order, other chats
// keep flowing).
//
// Admission of a voice note includes its transcription, which can take seconds
// and must not stall the single dispatch loop. So a voice note is admitted on a
// per-chat lane (a goroutine) instead of inline. While a lane exists for a chat,
// every later message of THAT chat joins the lane's queue behind it; a chat
// with no lane is admitted inline, exactly as before. A lane ends when its queue
// is empty, so idle chats cost nothing.

// connectorLane is one chat's serial queue.
type connectorLane struct {
	queue []bus.InboundMessage
}

// connectorLaneKey identifies the chat: the bound instance plus the chat id.
func connectorLaneKey(msg bus.InboundMessage) string {
	return connectorInstanceKey(msg) + "\x00" + msg.ChatID
}

// routeBoundConnectorInput admits msg now (inline) or on its chat's lane.
func (al *AgentLoop) routeBoundConnectorInput(runCtx context.Context, msg bus.InboundMessage) {
	if _, _, bound := al.boundConnectorPair(msg); !bound {
		al.admitDispatch(runCtx, msg) // not bound-connector input: unchanged, inline
		return
	}
	key := connectorLaneKey(msg)

	al.connLanes.Lock()
	if lane := al.connLaneMap[key]; lane != nil {
		// A voice note (or what follows it) is still in flight: queue behind it.
		lane.queue = append(lane.queue, msg)
		al.connLanes.Unlock()
		return
	}
	if !al.boundAudioNeedsTranscription(msg) || !al.beginActiveRequest() {
		al.connLanes.Unlock()
		al.admitDispatch(runCtx, msg) // nothing in flight for this chat: inline
		return
	}
	if al.connLaneMap == nil {
		al.connLaneMap = map[string]*connectorLane{}
	}
	lane := &connectorLane{queue: []bus.InboundMessage{msg}}
	al.connLaneMap[key] = lane
	al.connLanes.Unlock()

	go al.runConnectorLane(runCtx, key, lane)
}

// runConnectorLane drains one chat's queue in order and removes the lane once it
// is empty (checked under the lock, so a message arriving as the lane ends is
// either queued here or admitted inline after the lane is gone - never lost,
// never reordered).
func (al *AgentLoop) runConnectorLane(runCtx context.Context, key string, lane *connectorLane) {
	defer al.endActiveRequest()
	for {
		al.connLanes.Lock()
		if len(lane.queue) == 0 {
			delete(al.connLaneMap, key)
			al.connLanes.Unlock()
			return
		}
		msg := lane.queue[0]
		lane.queue = lane.queue[1:]
		al.connLanes.Unlock()
		al.admitDispatch(runCtx, msg)
	}
}

// admitDispatch is admitAndDispatch behind the test hook.
func (al *AgentLoop) admitDispatch(runCtx context.Context, msg bus.InboundMessage) {
	if al.admitDispatchHook != nil {
		al.admitDispatchHook(runCtx, msg)
		return
	}
	al.admitAndDispatch(runCtx, msg)
}
