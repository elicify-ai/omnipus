//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestPairingObserver verifies the #283 wiring: WhatsAppNativeChannel implements
// channels.PairingObservable and forwards pairing updates (status/qr/message) to
// the registered observer, which the gateway bridges to the SPA.
func TestPairingObserver(t *testing.T) {
	ch, err := NewWhatsAppNativeChannel(
		config.WhatsAppConfig{Enabled: true},
		bus.NewMessageBus(),
		t.TempDir(),
	)
	if err != nil {
		t.Fatalf("NewWhatsAppNativeChannel: %v", err)
	}

	// Capability check: the native channel must be observable.
	obs, ok := ch.(channels.PairingObservable)
	if !ok {
		t.Fatal("WhatsAppNativeChannel does not implement channels.PairingObservable")
	}

	native, ok := ch.(*WhatsAppNativeChannel)
	if !ok {
		t.Fatal("unexpected concrete type")
	}

	type update struct {
		status      channels.PairingStatus
		qr, message string
	}
	var got []update
	obs.SetPairingObserver(func(status channels.PairingStatus, qr, message string) {
		got = append(got, update{status, qr, message})
	})

	// Drive the same emit path the QR goroutine uses.
	native.emitPairing("code", "QR-PAYLOAD", "")
	native.emitPairing("linked", "", "")
	native.emitPairing("error", "", "boom")

	want := []update{
		{"code", "QR-PAYLOAD", ""},
		{"linked", "", ""},
		{"error", "", "boom"},
	}
	if len(got) != len(want) {
		t.Fatalf("observer received %d updates, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("update %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// No-op when no observer is set (must not panic).
	native.SetPairingObserver(nil)
	native.emitPairing("waiting", "", "")
}
