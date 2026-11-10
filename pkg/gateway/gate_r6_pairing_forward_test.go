package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestPairingForward_ALaggingEventNeverFollowsANewerReplay is gate r5 R5-A01
// (M8, M13): a connection's forwarder still holds attempt A's code when Retry
// B (same request and session ids, different attempt) has already been
// published and cached. The page subscribes and gets B from the cache; the
// lagging forwarder then reaches A. A must not be sent after B — the page
// never regresses to A's obsolete code.
func TestPairingForward_ALaggingEventNeverFollowsANewerReplay(t *testing.T) {
	h := makeMinimalHandler()
	bus := agent.NewEventBus()
	defer bus.Close()
	bus.SetSyncTap(h.hubSyncTap)
	lagging := bus.Subscribe(64) // the forwarder subscribed before A, but has not run yet

	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatusCode, QR: "QR-A",
		RequestID: "req-a", SessionID: "chat-a", Seq: 1,
	}})
	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatusCode, QR: "QR-B",
		RequestID: "req-a", SessionID: "chat-a", Seq: 2,
	}})

	wc, frames := makeForwarderTestConn(16)
	h.subscribePairingInterest(wc, pairingTestChannelID, true) // replays the cached B
	done := make(chan struct{})
	go h.eventForwarder(wc, "chat-a", lagging, done)

	var qrs []string
	deadline := time.After(2 * time.Second)
collect:
	for {
		select {
		case raw := <-frames:
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame["type"] == "whatsapp_pairing" {
				qr, _ := frame["qr"].(string)
				qrs = append(qrs, qr)
			}
		case <-deadline:
			break collect
		}
	}
	require.NotEmpty(t, qrs)
	require.Equal(t, "QR-B", qrs[0], "the replay shows B")
	require.NotContains(t, qrs, "QR-A", "the lagging A is never sent after the newer B")
	require.Equal(t, "QR-B", qrs[len(qrs)-1])
}
