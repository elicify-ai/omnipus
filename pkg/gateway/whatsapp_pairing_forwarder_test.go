//go:build !cgo

// WhatsApp pairing WS-forwarder tests (#283): EventKindWhatsAppPairing →
// whatsapp_pairing frame, with the qr/message pointer mapping and the global
// (non-chatID) forwarding behavior.

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapicom-ai/omnipus/pkg/agent"
)

// TestWhatsAppPairing_CodeFrameForwardsGlobally verifies a "code" pairing event
// is forwarded as a whatsapp_pairing frame even when the connection's chatID is
// unrelated (pairing is not session-tied), with qr set and message absent.
func TestWhatsAppPairing_CodeFrameForwardsGlobally(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	// Deliberately unrelated chatID — the pairing frame must still forward.
	done := runForwarder(h, wc, "unrelated-chat", bus)

	bus.Emit(agent.Event{
		Kind: agent.EventKindWhatsAppPairing,
		Payload: agent.WhatsAppPairingPayload{
			ChannelID: "whatsapp_native",
			Status:    "code",
			QR:        "QR-PAYLOAD",
		},
	})

	bus.Close()
	<-done

	f := drainFrame(t, ch)
	assert.Equal(t, "whatsapp_pairing", f.Type)
	assert.Equal(t, "whatsapp_native", f.ChannelID)
	assert.Equal(t, "code", f.Status)
	assert.Equal(t, "QR-PAYLOAD", f.QR)
	assert.Empty(t, f.Message)
}

// TestWhatsAppPairing_ErrorFrameOmitsQR verifies the pointer mapping leaves qr
// absent when empty and carries the message on a non-"code" status.
func TestWhatsAppPairing_ErrorFrameOmitsQR(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-x", bus)

	bus.Emit(agent.Event{
		Kind: agent.EventKindWhatsAppPairing,
		Payload: agent.WhatsAppPairingPayload{
			ChannelID: "whatsapp_native",
			Status:    "error",
			Message:   "boom",
		},
	})

	bus.Close()
	<-done

	f := drainFrame(t, ch)
	assert.Equal(t, "whatsapp_pairing", f.Type)
	assert.Equal(t, "error", f.Status)
	assert.Empty(t, f.QR)
	assert.Equal(t, "boom", f.Message)
}
