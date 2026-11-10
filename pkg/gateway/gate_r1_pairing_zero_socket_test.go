package gateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestPairingCache_OutcomeWithZeroSocketsIsKept is Train 2 gate r1 SFH
// T2-SF-06: the browser saw a QR, then every socket disconnected; the chat's
// pairing timed out meanwhile. With no per-connection forwarder alive, the
// reconnect cache must still move to the final status, so the reconnecting
// chat sees the outcome instead of the old QR.
func TestPairingCache_OutcomeWithZeroSocketsIsKept(t *testing.T) {
	h := makeMinimalHandler()
	bus := agent.NewEventBus()
	defer bus.Close()
	bus.SetSyncTap(h.hubSyncTap) // the real wiring; no connection is subscribed
	emit := func(status channels.PairingStatus, qr string) {
		bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
			ChannelID: "whatsapp", Status: status, QR: qr, RequestID: "r1", SessionID: "s1"}})
	}
	emit(channels.PairingStatusCode, "QR-OLD")
	emit(channels.PairingStatusTimeout, "")

	v, ok := h.lastPairingState.Load("whatsapp")
	require.True(t, ok, "the final status must be cached with zero sockets connected")
	raw, isBytes := v.([]byte)
	require.True(t, isBytes, "the cache holds marshaled frames")
	var frame map[string]any
	require.NoError(t, json.Unmarshal(raw, &frame))
	require.Equal(t, "timeout", frame["status"], "reconnect must see the outcome, not the old QR: %v", frame)
	require.NotContains(t, frame, "qr")
}

// TestPairingCache_LaggingOlderEventNeverOverwritesNewer: the sync tap
// cached a newer outcome; a per-connection forwarder still holding an older
// code frame must not put the stale QR back.
func TestPairingCache_LaggingOlderEventNeverOverwritesNewer(t *testing.T) {
	h := makeMinimalHandler()
	newer := agent.WhatsAppPairingPayload{ChannelID: "whatsapp", Status: channels.PairingStatusTimeout, RequestID: "r1", SessionID: "s1", Seq: 2}
	older := agent.WhatsAppPairingPayload{ChannelID: "whatsapp", Status: channels.PairingStatusCode, QR: "QR-OLD", RequestID: "r1", SessionID: "s1", Seq: 1}
	h.updatePairingCache(newer, pairingFrameFromPayload(newer))
	h.updatePairingCache(older, pairingFrameFromPayload(older))
	v, ok := h.lastPairingState.Load("whatsapp")
	require.True(t, ok)
	raw, isBytes := v.([]byte)
	require.True(t, isBytes, "the cache holds marshaled frames")
	var frame map[string]any
	require.NoError(t, json.Unmarshal(raw, &frame))
	require.Equal(t, "timeout", frame["status"], "an older event must not overwrite the newer cached outcome")
}
