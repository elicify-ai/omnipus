// reload_pairing_test.go — Wave 2 gate tests for WhatsApp pairing (#283/#368)
//
// Covers:
//   - TestSubscribePairingInterest_LateSubscriber_ReceivesCachedFrame: late subscriber gets cached QR
//   - TestLastPairingState_Eviction: cache eviction on terminal and non-terminal statuses
//   - TestWireChannelManager_ObserverSurvivesChannelRecreation: observer wired on wireChannelManager call
//
// (TestWSHandler_PairingSubscribe_RequiresAdmin removed — the admin-role guard
// on the whatsapp_pairing_subscribe frame was deleted along with the rest of
// the multi-account machinery; every connection now receives the broadcast
// unconditionally, single-user model.)

package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test A: late subscriber receives cached QR ---

// TestSubscribePairingInterest_LateSubscriber_ReceivesCachedFrame verifies that a
// connection subscribing after the first QR has already been emitted immediately
// receives the cached frame (#368).
func TestSubscribePairingInterest_LateSubscriber_ReceivesCachedFrame(t *testing.T) {
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)

	// Pre-seed: store a marshaled whatsapp_pairing "code" frame into the cache.
	pairF := replayFrameDecoder{
		Type:      "whatsapp_pairing",
		ChannelID: "whatsapp_native",
		Status:    "code",
		QR:        "CACHED-QR",
	}
	frameBytes, err := json.Marshal(pairF)
	require.NoError(t, err)
	h.lastPairingState.Store("whatsapp_native", frameBytes)

	// Call subscribePairingInterest with active=true — should re-emit cached frame.
	h.subscribePairingInterest(wc, "whatsapp_native", true)

	select {
	case data := <-ch:
		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(data, &f))
		assert.Equal(t, "whatsapp_pairing", f.Type)
		assert.Equal(t, "CACHED-QR", f.QR)
		assert.Equal(t, "code", f.Status)
		assert.Equal(t, "whatsapp_native", f.ChannelID)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: expected cached QR frame to be delivered to late subscriber")
	}

	// With active=false (unsubscribe), nothing should be written.
	wc2, ch2 := makeForwarderTestConn(8)
	h.subscribePairingInterest(wc2, "whatsapp_native", false)
	select {
	case data := <-ch2:
		t.Fatalf("unsubscribe must not write any frame: got %s", data)
	default:
		// correct: nothing written
	}

	// When no cached state exists for a channel, nothing should be written.
	wc3, ch3 := makeForwarderTestConn(8)
	h.subscribePairingInterest(wc3, "unknown_channel", true)
	select {
	case data := <-ch3:
		t.Fatalf("subscribe with no cached state must not write any frame: got %s", data)
	default:
		// correct: nothing written
	}
}

// --- Test B: cache eviction on terminal and non-terminal statuses ---

// TestLastPairingState_Eviction verifies that the eventForwarder evicts the cache
// for terminal statuses (linked, error), non-terminal QR rotation (timeout), and
// known waiting status — while "code" populates (not evicts) the cache.
func TestLastPairingState_Eviction(t *testing.T) {
	evictStatuses := []channels.PairingStatus{
		channels.PairingStatusLinked,
		channels.PairingStatusTimeout,
		channels.PairingStatusError,
		channels.PairingStatusWaiting,
	}

	for _, status := range evictStatuses {
		t.Run("evict_on_"+string(status), func(t *testing.T) {
			b := agent.NewEventBus()
			h := makeMinimalHandler()
			wc, _ := makeForwarderTestConn(64)
			done := runForwarder(h, wc, "chat-x", b)

			// Pre-seed the cache.
			h.lastPairingState.Store("whatsapp_native", []byte(`{"type":"whatsapp_pairing"}`))

			// Fire the event through the eventForwarder.
			b.Emit(agent.Event{
				Kind: agent.EventKindWhatsAppPairing,
				Payload: agent.WhatsAppPairingPayload{
					ChannelID: "whatsapp_native",
					Status:    status,
				},
			})

			b.Close()
			<-done

			// Cache must be evicted.
			_, ok := h.lastPairingState.Load("whatsapp_native")
			assert.False(t, ok, "lastPairingState must be evicted on status %q", status)
		})
	}

	// "code" must POPULATE the cache, not evict it.
	t.Run("populate_on_code", func(t *testing.T) {
		b := agent.NewEventBus()
		defer b.Close()

		h := makeMinimalHandler()
		wc, _ := makeForwarderTestConn(64)
		done := runForwarder(h, wc, "chat-x", b)

		b.Emit(agent.Event{
			Kind: agent.EventKindWhatsAppPairing,
			Payload: agent.WhatsAppPairingPayload{
				ChannelID: "whatsapp_native",
				Status:    channels.PairingStatusCode,
				QR:        "FRESH-QR",
			},
		})

		b.Close()
		<-done

		cached, ok := h.lastPairingState.Load("whatsapp_native")
		require.True(t, ok, "lastPairingState must be populated on 'code' status")
		frameBytes, ok := cached.([]byte)
		require.True(t, ok, "cached value must be []byte")
		assert.NotEmpty(t, frameBytes, "cached bytes must not be empty")

		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(frameBytes, &f))
		assert.Equal(t, "FRESH-QR", f.QR)
	})
}
