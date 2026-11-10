package tools

import (
	"context"
	"testing"
)

// §6.18 / FR-240 / K7: the origin label names the channel person and
// connection, else the asking agent; a web turn has none. A turn that
// consumed ANY channel input is channel-triggered, even when a peer request
// came first, and the channel label wins.
func TestTurnOriginLabelsAndChannelTrigger(t *testing.T) {
	web := NewTurnOrigin()
	if web.ChannelTriggered() || web.Label() != "" {
		t.Fatalf("web turn: triggered=%v label=%q, want false and empty", web.ChannelTriggered(), web.Label())
	}

	peer := NewTurnOrigin()
	peer.AddPeer("Jim")
	if peer.ChannelTriggered() || peer.Label() != "asked by Jim" {
		t.Fatalf("peer turn: triggered=%v label=%q", peer.ChannelTriggered(), peer.Label())
	}
	peer.AddChannelPerson("Anna", "tg-1") // consumed mid-turn
	if !peer.ChannelTriggered() || peer.Label() != "while answering Anna on tg-1" {
		t.Fatalf("after a channel input: triggered=%v label=%q", peer.ChannelTriggered(), peer.Label())
	}
	peer.AddChannelPerson("Bob", "tg-2")
	if peer.Label() != "while answering Anna on tg-1" {
		t.Fatalf("the first channel person names the label, got %q", peer.Label())
	}

	ctx := WithTurnOrigin(context.Background(), peer)
	if ToolTurnOrigin(ctx) != peer {
		t.Fatal("ctx round trip lost the origin")
	}
	if ToolTurnOrigin(context.Background()) != nil || ToolTurnOrigin(context.Background()).Label() != "" {
		t.Fatal("no origin attached must read as nil with an empty label")
	}
}
