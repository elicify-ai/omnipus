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
	// provenance is the input message's provenance (I-2), joined into the
	// turn's. Zero = unresolved: an input recorded without one makes the
	// turn ineligible for connected programs.
	provenance bus.Provenance
}

// inboundInputOrigin derives an input's origin from the inbound message. A
// message from a channel adapter carries its instance (bus.InboundMessage
// .InstanceID, set only by the adapters' single publication point); a peer
// request's owner wake carries the sending agent.
func inboundInputOrigin(msg bus.InboundMessage) inputOrigin {
	o := inputOrigin{provenance: msg.Provenance}
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
	origin.AddProvenance(o.provenance)
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

// recordTurnInputs starts a turn's origin from its inputs (§6.18 / FR-240,
// I-2: a turn's provenance is the join of the inputs it consumes): the
// triggering input, then the queued inputs a continuation turn was started to
// consume. A continuation that only consumes queued inputs has no triggering
// input of its own (no message, a zero InputOrigin); recording that zero
// value would join an input that never existed, as unresolved, and so make
// a person's queued web message unresolved whenever it arrived just as the
// previous turn was retiring. Every consumed input is still recorded, so one
// without provenance keeps the turn unresolved (fail closed).
func (al *AgentLoop) recordTurnInputs(opts *processOptions, origin *tools.TurnOrigin) {
	own := opts.InputOrigin
	continuationOnly := opts.UserMessage == "" && len(opts.ConsumedInputOrigins) > 0 &&
		own.provenance.IsUnresolved() && own.channelPerson == "" && own.peerAgentID == ""
	if !continuationOnly {
		own.recordInto(al, origin)
	}
	for _, consumed := range opts.ConsumedInputOrigins {
		consumed.recordInto(al, origin)
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
	// An input with no label (a message typed in the agent's own chat, a
	// task) is still an input: merging it drops a group reference (D23). It
	// ranks as web, so a later peer or channel label still applies (§6.18).
	label := origin.Label()
	class := originWeb
	switch {
	case origin.ChannelTriggered():
		class = originChannel
	case label != "":
		class = originPeer
	}
	return approvalOrigin{Label: label, Class: class}
}
