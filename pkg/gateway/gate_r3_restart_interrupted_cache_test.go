package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestPairingReconnect_RestartInterruptedFrameReachesAReconnectingPage is the
// founder's restart rule (2026-10-21): the interrupted result is emitted at
// gateway start, before any page has reconnected, so the reconnect cache must
// keep it — status and text only, no ids — for the chat card and Connectors
// that subscribe afterwards. An ordinary unowned result is still evicted
// (TestPairingReconnect_SettingsStartedPairingKeepsNothing).
func TestPairingReconnect_RestartInterruptedFrameReachesAReconnectingPage(t *testing.T) {
	h := makeMinimalHandler()
	h.hubWhatsAppPairing(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatusError, Message: restartInterruptedText,
		Seq: 1, RestartInterrupted: true,
	}})

	page, frames := makeForwarderTestConn(8)
	h.subscribePairingInterest(page, pairingTestChannelID, true)
	select {
	case raw := <-frames:
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		require.Equal(t, "whatsapp_pairing", frame["type"])
		require.Equal(t, "error", frame["status"])
		require.Equal(t, restartInterruptedText, frame["message"])
		require.NotContains(t, frame, "request_id", "no ownership survives a restart")
		require.NotContains(t, frame, "session_id")
		require.NotContains(t, frame, "qr")
	case <-time.After(2 * time.Second):
		t.Fatal("a page reconnecting after the restart never saw that its pairing was interrupted")
	}
}
