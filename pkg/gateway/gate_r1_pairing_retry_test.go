package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// subscribeFromChat sends the real whatsapp_pairing_subscribe frame the chat
// card sends after Retry, through the read loop's dispatcher.
func (f *pairingStopFixture) subscribeFromChat(user, requestID, sessionID string) {
	f.t.Helper()
	h := makeMinimalHandler()
	h.agentLoop = f.al
	wc, _ := makeForwarderTestConn(16)
	wc.userID = user
	reader := &wsHandlerReadLoop{h: h, ctx: context.Background(), wc: wc, chatID: "chat"}
	raw, err := json.Marshal(generated.WhatsAppPairingSubscribeFrame{
		Type: string(generated.WsFrameTypeWhatsappPairingSubscribe), ChannelId: pairingTestChannelID,
		Active: true, RequestId: &requestID, SessionId: &sessionID,
	})
	require.NoError(f.t, err)
	reader.dispatchFrame(raw, wsTypeOnly{Type: string(generated.WsFrameTypeWhatsappPairingSubscribe)})
}

func (f *pairingStopFixture) requireStopReached(want bool) {
	f.t.Helper()
	select {
	case <-f.fake.stopCalled:
		require.True(f.t, want, "Stop must not reach a pairing this chat does not own")
	case <-time.After(3 * time.Second):
		require.False(f.t, want, "the chat's Stop never reached the retried pairing")
	}
}

// TestPairingRetry_ChatRetryRestoresOwnershipForStop is Train 2 gate r1
// CR-T2-04 / SFH T2-SF-04 / architect F03: a chat-started pairing times out,
// the chat card's Retry restarts the channel and resubscribes with its
// request/session ids. The server restores the pairing's ownership only for
// that exact request and session, from a connection of that session's
// owner — so the chat's Stop ends the retried pairing.
func TestPairingRetry_ChatRetryRestoresOwnershipForStop(t *testing.T) {
	f := newPairingStopFixture(t)
	f.al.EmitWhatsAppPairing(pairingTestChannelID, channels.PairingStatusTimeout, "", "expired")

	f.subscribeFromChat(wu6Owner, f.requestID, f.mainID)
	f.stopChat(f.mainID)
	f.requireStopReached(true)
	payload := f.awaitPairingEvent("stopped")
	require.Equal(t, f.requestID, payload.RequestID)
	require.Equal(t, f.mainID, payload.SessionID)
}

// TestPairingRetry_OwnershipIsNotRestoredForAnotherUserOrRequest: a
// subscription from another account, or naming another request, restores
// nothing — client-supplied ids are never trusted on their own.
func TestPairingRetry_OwnershipIsNotRestoredForAnotherUserOrRequest(t *testing.T) {
	for name, sub := range map[string][3]string{
		"other user":    {"someone-else", "", ""},
		"other request": {wu6Owner, "req-not-the-owner", ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := newPairingStopFixture(t)
			f.al.EmitWhatsAppPairing(pairingTestChannelID, channels.PairingStatusTimeout, "", "expired")
			requestID := sub[1]
			if requestID == "" {
				requestID = f.requestID
			}
			f.subscribeFromChat(sub[0], requestID, f.mainID)
			f.stopChat(f.mainID)
			f.requireStopReached(false)
		})
	}
}
