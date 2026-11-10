package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// §6.18: a channel person's message the running turn consumes mid-turn (a
// steering input) makes that turn channel-triggered; a webchat message does not.
func TestWU4ConsumedSteeringInputUpdatesTheRunningTurnOrigin(t *testing.T) {
	al := &AgentLoop{}
	ts := &turnState{origin: tools.NewTurnOrigin()}
	al.activeTurnStates.Store("agent:mia:s1", ts)

	web := inboundInputOrigin(bus.InboundMessage{Channel: "webchat", Content: "hi"})
	if _, _, _, err := al.consumeDequeuedSteeringResult("agent:mia:s1", []steeringQueueItem{{origin: web}}); err != nil {
		t.Fatal(err)
	}
	if ts.origin.ChannelTriggered() {
		t.Fatal("a webchat input must not make the turn channel-triggered")
	}

	anna := inboundInputOrigin(bus.InboundMessage{
		Channel: "telegram", InstanceID: "tg-1",
		Sender: bus.SenderInfo{DisplayName: "Anna"},
	})
	if _, _, _, err := al.consumeDequeuedSteeringResult("agent:mia:s1", []steeringQueueItem{{origin: anna}}); err != nil {
		t.Fatal(err)
	}
	if !ts.origin.ChannelTriggered() || ts.origin.Label() != "while answering Anna on tg-1" {
		t.Fatalf("after Anna's message: triggered=%v label=%q", ts.origin.ChannelTriggered(), ts.origin.Label())
	}
}
