// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// whatsapp_pairing_stop_reconnect_test.go — RED pack, BDD-8.8 / FR-081: the
// Stop of a chat that started a WhatsApp pairing ends it with a K10 frame
// carrying status "stopped" and the same request/session ids, and a
// reconnecting chat re-subscribes to current progress — for a chat-started
// pairing the kept LAST TERMINAL frame, status only, never a QR.
//
// Spec sources (expected values derive from these, never from the
// implementation):
//   - BDD-8.8 (US-8 AS-3): "Stop ends pairing with 'stopped'; a reconnect
//     re-subscribes to the same request and sees current progress."
//   - FR-081: QR pairing renders in the requesting chat, correlated by
//     request and session id, with timeout, Stop and reconnect handling.
//   - K10: WhatsAppPairingFrame carries optional request_id and session_id;
//     "stopped" is a contract pairing status (0e03d98ec).
//   - #368: a Settings-started pairing's terminal state is evicted, never
//     kept (chief ruling 2026-10-18 keeps that for uncorrelated pairings).
//
// Unit boundary: the loop-level tests drive the REAL Stop path
// (RequestCancelForSession → StopSession → the pairing sweep → the channel
// manager) against a REAL channels.Manager with a registered test channel
// whose StopPairing reports an in-flight pairing. The real whatsapp_native
// channel's QR loop is whatsmeow-driven and needs the WhatsApp servers, so
// the channel side of "no further codes" is the PairingStoppable contract
// the fake models; what the loop-level tests prove is that the Stop reaches
// the manager-resolved channel and emits the correlated "stopped" frame, and
// that an unrelated chat's Stop reaches nothing. The reconnect tests drive
// the real forwarder and the real subscribe re-emit over the real event bus.
//
// RED expectation on the pre-implementation base: the Stop oracle and the
// kept-terminal oracle fail (no pairing sweep, and every terminal state
// evicted per #368); the two #368 controls pass on both bases by design.
package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pairingTestChannelID is the registered instance id of the test channel. It
// is deliberately not "whatsapp_native": the tests must resolve the channel
// through the manager, not through a hard-coded production name.
const pairingTestChannelID = "whatsapp-pairing-test"

// pairingTestChannel is a minimal Channel whose pairing is stoppable: it
// models the in-flight state of the real WhatsApp channel's QR loop.
// StopPairing is declared as a plain method — the production type assertion
// (channels.PairingStoppable) is what discovers it, so the test never needs
// the interface type by name.
type pairingTestChannel struct {
	running    chan struct{}
	stopCalled chan string
}

func newPairingTestChannel() *pairingTestChannel {
	return &pairingTestChannel{running: make(chan struct{}), stopCalled: make(chan string, 4)}
}

func (c *pairingTestChannel) Name() string                    { return pairingTestChannelID }
func (c *pairingTestChannel) Start(ctx context.Context) error { close(c.running); return nil }
func (c *pairingTestChannel) Stop(ctx context.Context) error  { return nil }
func (c *pairingTestChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	return nil
}
func (c *pairingTestChannel) IsRunning() bool                            { return true }
func (c *pairingTestChannel) IsAllowed(senderID string) bool             { return true }
func (c *pairingTestChannel) IsAllowedSender(sender bus.SenderInfo) bool { return true }
func (c *pairingTestChannel) ReasoningChannelID() string                 { return "" }

// StopPairing models the in-flight pairing: the real channel returns true
// only while its QR loop runs and the device has not linked.
func (c *pairingTestChannel) StopPairing(ctx context.Context) bool {
	c.stopCalled <- pairingTestChannelID
	return true
}

// pairingStopFixture is the loop-level fixture: a real loop (its own event
// bus), a real channel manager carrying the test channel, a main session
// whose chat started the pairing, and a second chat for the wrong-chat Stop.
type pairingStopFixture struct {
	t         *testing.T
	al        *agent.AgentLoop
	fake      *pairingTestChannel
	mainID    string
	otherID   string
	requestID string
	events    agent.EventSubscription
}

func newPairingStopFixture(t *testing.T) *pairingStopFixture {
	t.Helper()
	cfg := secretCardAgentConfig("omnipus", "Omnipus", false)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &secretScriptProvider{toolName: "bash", toolArgs: map[string]any{"command": "echo unused"}})
	mgr, err := channels.NewManager(cfg, nil, msgBus, nil)
	require.NoError(t, err)
	fake := newPairingTestChannel()
	mgr.RegisterChannel(pairingTestChannelID, fake)
	al.SetChannelManager(mgr)

	store := al.GetSessionStore()
	main, err := store.GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "omnipus")
	require.NoError(t, err)
	other, err := store.NewSession(session.SessionTypeChat, "webchat", "omnipus")
	require.NoError(t, err)
	owner := wu6Owner
	require.NoError(t, store.SetMeta(main.ID, session.MetaPatch{Owner: &owner}))
	require.NoError(t, store.SetMeta(other.ID, session.MetaPatch{Owner: &owner}))

	requestID := "req-pairing-stop"
	// The chat-started correlation (K10/FR-081): the loop records which chat
	// request started the pairing.
	al.NotePairingRequest(pairingTestChannelID, requestID, main.ID)

	return &pairingStopFixture{
		t: t, al: al, fake: fake, mainID: main.ID, otherID: other.ID,
		requestID: requestID, events: al.SubscribeEvents(64),
	}
}

// stopChat drives the one real Stop for a chat.
func (f *pairingStopFixture) stopChat(sessionID string) {
	f.t.Helper()
	_, _, err := f.al.RequestCancelForSession(context.Background(), sessionID, wu6Owner, "webchat")
	require.NoError(f.t, err, "the chat Stop must be accepted")
}

// awaitPairingEvent reads the loop's event subscription until a pairing event
// with the wanted status arrives (or the deadline passes), returning it.
func (f *pairingStopFixture) awaitPairingEvent(status string) agent.WhatsAppPairingPayload {
	f.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case evt, ok := <-f.events.C:
			if !ok {
				f.t.Fatal("the loop's event subscription closed before the pairing event arrived")
			}
			if evt.Kind != agent.EventKindWhatsAppPairing {
				continue
			}
			payload, assertionOK := evt.Payload.(agent.WhatsAppPairingPayload)
			require.True(f.t, assertionOK, "pairing event payload type: %T", evt.Payload)
			if payload.Status == channels.PairingStatus(status) {
				return payload
			}
		case <-deadline:
			f.t.Fatalf("BLOCKED: no %q pairing event arrived on the loop's event bus within the wait — required by BDD-8.8/FR-081", status)
		}
	}
}

// requireQuietPairing fails when any further pairing event arrives within the
// quiet window (the no-further-codes oracle).
func (f *pairingStopFixture) requireQuietPairing() {
	f.t.Helper()
	deadline := time.After(400 * time.Millisecond)
	for {
		select {
		case evt, ok := <-f.events.C:
			if !ok {
				return
			}
			if evt.Kind == agent.EventKindWhatsAppPairing {
				f.t.Fatalf("no further pairing frames may follow the terminal one, got status %q", evt.Payload)
			}
		case <-deadline:
			return
		}
	}
}

// TestPairingStop_ChatStopEndsInFlightPairingWithStoppedFrame is BDD-8.8's
// Stop half: the Stop of the chat that started the pairing calls
// StopPairing on the manager-resolved channel and emits one correlated
// "stopped" frame — the same request and session ids, no further codes.
func TestPairingStop_ChatStopEndsInFlightPairingWithStoppedFrame(t *testing.T) {
	f := newPairingStopFixture(t)

	f.stopChat(f.mainID)

	select {
	case <-f.fake.stopCalled:
	case <-time.After(10 * time.Second):
		t.Fatal("the chat Stop never reached the channel's StopPairing — BDD-8.8 requires the in-flight pairing to be ended")
	}

	payload := f.awaitPairingEvent("stopped")
	require.Equal(t, f.requestID, payload.RequestID, "the stopped frame carries the starting chat's request id (K10)")
	require.Equal(t, f.mainID, payload.SessionID, "the stopped frame carries the starting chat's session id (K10)")

	f.requireQuietPairing()
}

// TestPairingStop_StopInAnotherChatChangesNothing: a Stop in a different
// chat reaches neither the channel nor the correlation; the owning chat's
// own Stop still ends the pairing afterwards.
func TestPairingStop_StopInAnotherChatChangesNothing(t *testing.T) {
	f := newPairingStopFixture(t)

	f.stopChat(f.otherID)

	select {
	case <-f.fake.stopCalled:
		t.Fatal("a Stop in another chat must not reach the pairing channel")
	case <-time.After(400 * time.Millisecond):
	}
	f.requireQuietPairing()

	// The correlation survived the unrelated Stop: the owning chat's Stop
	// still ends the pairing with its own ids.
	f.stopChat(f.mainID)
	payload := f.awaitPairingEvent("stopped")
	require.Equal(t, f.requestID, payload.RequestID)
	require.Equal(t, f.mainID, payload.SessionID)
}

// pairingFrame decodes a raw whatsapp_pairing frame's wire shape.
func pairingFrame(t *testing.T, raw []byte) generatedPairingFrame {
	t.Helper()
	var f generatedPairingFrame
	require.NoError(t, json.Unmarshal(raw, &f))
	return f
}

// generatedPairingFrame is the wire view the tests assert on (the generated
// WhatsAppPairingFrame's JSON names).
type generatedPairingFrame struct {
	Type      string  `json:"type"`
	ChannelId string  `json:"channel_id"`
	Status    string  `json:"status"`
	Qr        *string `json:"qr,omitempty"`
	RequestId *string `json:"request_id,omitempty"`
	SessionId *string `json:"session_id,omitempty"`
}

// TestPairingReconnect_SubscribeReceivesKeptTerminalFrameStatusOnly is
// BDD-8.8's reconnect half for a chat-started pairing: after the QR and the
// terminal frame, a re-subscribing connection receives the kept LAST TERMINAL
// frame — status only, the QR stripped, ids intact.
func TestPairingReconnect_SubscribeReceivesKeptTerminalFrameStatusOnly(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	wc.setPairingInterest(pairingTestChannelID, true)
	done := runForwarder(h, wc, "chat-a", bus)

	requestID := "req-reconnect"
	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatusCode, QR: "QR-RECONNECT",
		RequestID: requestID, SessionID: "chat-a",
	}})
	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatus("stopped"),
		Message: "pairing stopped from the chat", RequestID: requestID, SessionID: "chat-a",
	}})

	// The forwarder must have settled the cache before the reconnect.
	deadline := time.After(5 * time.Second)
	for {
		if _, ok := h.lastPairingState.Load(pairingTestChannelID); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the chat-started terminal frame was never cached for reconnects (BDD-8.8)")
		case <-time.After(10 * time.Millisecond):
		}
	}
	bus.Close()
	<-done

	// The reconnecting chat: a fresh connection subscribing to the same
	// channel's pairing UI.
	fresh, freshCh := makeForwarderTestConn(64)
	h.subscribePairingInterest(fresh, pairingTestChannelID, true)

	frame := pairingFrame(t, <-freshCh)
	assert.Equal(t, "whatsapp_pairing", frame.Type)
	assert.Equal(t, "stopped", frame.Status, "the re-subscribing chat sees current progress: the final status")
	assert.Nil(t, frame.Qr, "a kept terminal frame never carries a QR")
	require.NotNil(t, frame.RequestId)
	assert.Equal(t, requestID, *frame.RequestId, "the kept frame stays correlated to the starting chat (K10)")
	require.NotNil(t, frame.SessionId)
	assert.Equal(t, "chat-a", *frame.SessionId)
	_ = ch
}

// TestPairingReconnect_SettingsStartedPairingKeepsNothing is the #368
// control: a pairing without a starting chat keeps nothing — a terminal
// state is evicted, so a late subscriber is never shown outdated progress.
// Passes on the pre-implementation base by design.
func TestPairingReconnect_SettingsStartedPairingKeepsNothing(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, _ := makeForwarderTestConn(64)
	wc.setPairingInterest(pairingTestChannelID, true)
	done := runForwarder(h, wc, "chat-a", bus)

	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatusCode, QR: "QR-SETTINGS",
	}})
	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatusError, Message: "timed out",
	}})
	bus.Close()
	<-done

	if _, ok := h.lastPairingState.Load(pairingTestChannelID); ok {
		t.Fatal("a Settings-started pairing's terminal state must be evicted (#368), never kept")
	}
	fresh, freshCh := makeForwarderTestConn(64)
	h.subscribePairingInterest(fresh, pairingTestChannelID, true)
	select {
	case raw := <-freshCh:
		t.Fatalf("a late subscriber must receive nothing for a Settings-started pairing, got %s", raw)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestPairingReconnect_NewWaitingEvictsKeptFrame: a new pairing's "waiting"
// emit evicts a kept terminal frame, so a re-subscribing chat never sees the
// previous pairing's outcome as if it were current. Passes on the
// pre-implementation base by design.
func TestPairingReconnect_NewWaitingEvictsKeptFrame(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, _ := makeForwarderTestConn(64)
	wc.setPairingInterest(pairingTestChannelID, true)
	done := runForwarder(h, wc, "chat-a", bus)

	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatusCode, QR: "QR-OLD",
		RequestID: "req-old", SessionID: "chat-a",
	}})
	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatus("stopped"),
		RequestID: "req-old", SessionID: "chat-a",
	}})
	deadline := time.After(5 * time.Second)
	for {
		if _, ok := h.lastPairingState.Load(pairingTestChannelID); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the chat-started terminal frame was never kept for reconnects (BDD-8.8) — the waiting-evicts oracle needs a kept frame to evict")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// A NEW pairing starts: the channel emits "waiting".
	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatusWaiting, Message: "connecting",
	}})
	bus.Close()
	<-done

	if _, ok := h.lastPairingState.Load(pairingTestChannelID); ok {
		t.Fatal("a new pairing's waiting emit must evict the kept terminal frame")
	}
	fresh, freshCh := makeForwarderTestConn(64)
	h.subscribePairingInterest(fresh, pairingTestChannelID, true)
	select {
	case raw := <-freshCh:
		t.Fatalf("a re-subscribing chat must not be shown the previous pairing's outcome, got %s", raw)
	case <-time.After(200 * time.Millisecond):
	}
}

// Compile-time pin: the fixture channel satisfies the generated frame type's
// contract name in this pack's assertions (K10).
var _ = gen.WsFrameTypeWhatsappPairing
