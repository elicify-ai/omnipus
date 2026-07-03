package channels

import "testing"

// mockPairingChannel is a mockChannel that also implements PairingObservable,
// recording the observer the manager installs so the test can fire it (#283).
type mockPairingChannel struct {
	mockChannel
	observer func(status PairingStatus, qr, message string)
}

func (m *mockPairingChannel) SetPairingObserver(fn func(status PairingStatus, qr, message string)) {
	m.observer = fn
}

// TestManagerSetPairingObserver_TagsChannelID verifies the #283 wiring glue:
// SetPairingObserver propagates to every already-initialized PairingObservable
// channel and tags each update with that channel's own MAP KEY — which, since
// the ADR-029 multi-instance change, is the INSTANCE id ("whatsapp",
// "whatsapp.sales"), NOT the registry name "whatsapp_native". The SPA's
// pairing store and whatsapp_pairing_subscribe interest are keyed by the same
// instance id, so this tag IS the wire contract: changing it silently breaks
// QR delivery (live-UAT regression, 2026-07-03). Also guards the
// per-iteration loop-variable capture (a swap would report the last channel's
// name for both).
func TestManagerSetPairingObserver_TagsChannelID(t *testing.T) {
	m := newTestManager()
	wa := &mockPairingChannel{}
	other := &mockPairingChannel{}
	// Realistic post-ADR-029 map keys: a bare legacy instance and a namespaced
	// operator-created instance of the same type.
	m.channels["whatsapp"] = wa
	m.channels["whatsapp.sales"] = other

	type got struct {
		channelID   string
		status      PairingStatus
		qr, message string
	}
	var updates []got
	m.SetPairingObserver(func(channelID string, status PairingStatus, qr, message string) {
		updates = append(updates, got{channelID, status, qr, message})
	})

	if wa.observer == nil || other.observer == nil {
		t.Fatal("SetPairingObserver did not propagate to both PairingObservable channels")
	}

	// Fire in a fixed order so the assertion is deterministic regardless of map
	// iteration order during propagation.
	wa.observer("code", "QR-A", "")
	other.observer("error", "", "boom")

	want := []got{
		{"whatsapp", "code", "QR-A", ""},
		{"whatsapp.sales", "error", "", "boom"},
	}
	if len(updates) != len(want) {
		t.Fatalf("got %d updates, want %d: %+v", len(updates), len(want), updates)
	}
	for i := range want {
		if updates[i] != want[i] {
			t.Errorf("update %d = %+v, want %+v", i, updates[i], want[i])
		}
	}
}
