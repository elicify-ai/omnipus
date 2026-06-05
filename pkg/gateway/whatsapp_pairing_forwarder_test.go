//go:build !cgo

// WhatsApp pairing WS-forwarder tests (#283): EventKindWhatsAppPairing →
// whatsapp_pairing frame, the qr/message pointer mapping, and the per-connection
// interest scoping (Option B) — a connection receives the QR only after it
// subscribes to that channel.

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapicom-ai/omnipus/pkg/agent"
)

// TestWhatsAppPairing_CodeFrameDeliveredToSubscriber verifies a "code" pairing
// event is forwarded as a whatsapp_pairing frame to a connection that subscribed
// to that channel, with qr set and message absent.
func TestWhatsAppPairing_CodeFrameDeliveredToSubscriber(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	wc.setPairingInterest("whatsapp_native", true)
	// chatID is unrelated — pairing is not session-tied; subscription is what gates.
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
// absent when empty and carries the message on a non-"code" status (subscriber).
func TestWhatsAppPairing_ErrorFrameOmitsQR(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	wc.setPairingInterest("whatsapp_native", true)
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

// TestWhatsAppPairing_NotDeliveredWithoutSubscription verifies Option B: a
// connection that did NOT subscribe to the channel receives no pairing frame,
// so the QR secret isn't broadcast to every admin tab. An unsubscribe must also
// stop delivery.
func TestWhatsAppPairing_NotDeliveredWithoutSubscription(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	// Subscribe then unsubscribe — delivery must be off.
	wc.setPairingInterest("whatsapp_native", true)
	wc.setPairingInterest("whatsapp_native", false)
	// Also subscribed to a different channel only — must not match.
	wc.setPairingInterest("signal", true)
	done := runForwarder(h, wc, "chat-x", bus)

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

	select {
	case data := <-ch:
		t.Fatalf("unsubscribed connection received a pairing frame: %s", data)
	default:
	}
}
