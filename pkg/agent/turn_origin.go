// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// requestSenderAgentMetadataKey names the agent that sent a peer request on
// the request's owner wake, so the receiving turn's origin is that peer.
const requestSenderAgentMetadataKey = "request_sender_agent"

// inputOrigin is who one consumed input came from (omnipus-system-agent §6.18).
type inputOrigin struct {
	channelPerson string // display name of a channel person
	connection    string // the connection (channel instance) it arrived on
	peerAgentID   string // the agent that sent a peer request
	// approval retains the ranked room-aware approval provenance alongside
	// the channel provenance used by secret policy. Private, never wire data.
	approval approvalOrigin
}

// inboundInputOrigin derives an input's origin from the inbound message. A
// message from a channel adapter carries its instance (bus.InboundMessage
// .InstanceID, set only by the adapters' single publication point); a peer
// request's owner wake carries the sending agent.
func inboundInputOrigin(msg bus.InboundMessage) inputOrigin {
	var o inputOrigin
	if conn := strings.TrimSpace(msg.InstanceID); conn != "" {
		o.connection = conn
		o.channelPerson = firstNonBlank(msg.Sender.DisplayName, msg.Sender.Username, msg.Sender.CanonicalID, msg.Peer.ID)
		if o.channelPerson == "" {
			o.channelPerson = "a person"
		}
	}
	if msg.Metadata != nil {
		o.peerAgentID = strings.TrimSpace(msg.Metadata[requestSenderAgentMetadataKey])
	}
	return o
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// recordInto adds this input to a turn's origin.
func (o inputOrigin) recordInto(al *AgentLoop, origin *tools.TurnOrigin) {
	if o.channelPerson != "" {
		origin.AddChannelPerson(o.channelPerson, o.connection)
	}
	if o.peerAgentID != "" {
		name := al.agentDisplayName(addressing.Pair{AgentID: o.peerAgentID})
		if name == "" {
			name = o.peerAgentID
		}
		origin.AddPeer(name)
	}
}

// recordConsumedInputOrigins adds the origins of steering inputs the running
// turn on scope is consuming, so a channel person's message read mid-turn
// makes that turn channel-triggered (§6.18: "any input").
func (al *AgentLoop) recordConsumedInputOrigins(scope string, items []steeringQueueItem) {
	v, ok := al.activeTurnStates.Load(scope)
	if !ok {
		return
	}
	ts, ok := v.(*turnState)
	if !ok || ts == nil {
		return
	}
	for _, item := range items {
		item.origin.recordInto(al, ts.origin)
	}
}

// approvalFor preserves a captured room-aware origin, or derives the matching
// channel/peer approval label for an input admitted through the inbound path.
func (o inputOrigin) approvalFor(al *AgentLoop) approvalOrigin {
	if o.approval.Label != "" || o.approval.none || o.approval.mixed || o.approval.RoomRef != nil {
		return o.approval
	}
	origin := tools.NewTurnOrigin()
	o.recordInto(al, origin)
	class := originPeer
	if origin.ChannelTriggered() {
		class = originChannel
	}
	// An input with no label (a message typed in the agent's own chat, a
	// task) is still an input: merging it drops a group reference (D23).
	return approvalOrigin{Label: origin.Label(), Class: class}
}
