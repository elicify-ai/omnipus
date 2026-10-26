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
// (RequestCancelForSession → StopSession → the selected pairing attempt's own
// teardown) against a REAL channels.Manager and its pairing-attempt store,
// with a registered test channel that runs a real admitted and started
// attempt (gate r3 pairing-ownership model; re-aimed under chief ruling Q4 =
// A). The real whatsapp_native channel's QR loop is whatsmeow-driven and
// needs the WhatsApp servers, so only that platform client is faked; what the
// loop-level tests prove is that the Stop reaches the started attempt's own
// teardown and emits the correlated "stopped" frame, and that an unrelated
// chat's Stop reaches nothing. The reconnect tests drive
// the real forwarder and the real subscribe re-emit over the real event bus.
//
// RED expectation on the pre-implementation base: the Stop oracle and the
// kept-terminal oracle fail (no pairing sweep, and every terminal state
// evicted per #368); the two #368 controls pass on both bases by design.
package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pairingTestChannelID is the registered instance id of the test channel. It
// is deliberately not "whatsapp_native": the tests must resolve the channel
// through the manager, not through a hard-coded production name.
const pairingTestChannelID = "whatsapp-pairing-test"

// pairingTestChannel models the real WhatsApp channel's QR loop as a
// store-tracked pairing attempt (gate_r3_pairing_fakes_test.go): its started
// attempt's own teardown records the stop on stopCalled.
type pairingTestChannel = attemptChannel

func newPairingTestChannel() *pairingTestChannel {
	return newAttemptChannel(pairingTestChannelID)
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
	registerAttemptChannel(t, al, mgr, fake)
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
	// The chat-started pairing (K10/FR-081): the chat's request is admitted
	// as the owner of a new attempt, which then starts.
	admitAndStartConnect(t, al, mgr, fake, requestID, main.ID)

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

// TestPairingStop_ConnectChannelStampsTheChatCorrelation is the tool-side
// producer journey (BDD-8.4/8.8, K10/FR-081): the real connect_channel call
// for a WhatsApp instance stamps the pairing correlation itself — the
// requesting chat's transcript session and the tool call's own request id —
// so a later Stop of that chat reaches the manager-resolved channel and the
// emitted stopped frame carries the tool-produced identity. The test never
// seeds the correlation: everything asserted here was produced by the tool
// and the Stop path.
func TestPairingStop_ConnectChannelStampsTheChatCorrelation(t *testing.T) {
	w := newWU6Fixture(t)
	// The binding is valid: sales exists with mia on its team (BDD-8.1).
	seedWU5Membership(t, w.f1277.home)
	// The channel the connect's pairing attempt runs on: an attempt-tracking
	// test channel under the WhatsApp instance id the connect call names. The
	// id is a proper namespaced key (type prefix "whatsapp" — the connect tool
	// resolves the type from it). The real whatsapp_native channel's QR loop
	// needs the WhatsApp servers; what this journey proves is the
	// tool→admission→start→Stop wiring, and the fake models the platform.
	const waInstance = "whatsapp.wu7"
	fake := newAttemptChannel(waInstance)
	registerAttemptChannel(t, w.al, w.svc.ChannelManager, fake)
	// The fixture has no reload pipeline: this reload starts the connector the
	// way the manager's reload does — consuming the admitted attempt.
	w.al.WireSysagentDeps(&systools.Deps{
		Home: w.f1277.home, ConfigPath: w.f1277.configPath,
		GetCfg: w.al.GetConfig, MutateConfig: w.al.MutateConfig,
		SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(w.f1277.configPath, c) },
		CredStore:        w.store,
		WaitForReloadFunc: func() error {
			token := w.svc.ChannelManager.PairingAttempts().TakeReady(waInstance, channels.ChannelRevision(w.al.GetConfig(), waInstance))
			return fake.Start(channels.WithPairingStart(context.Background(), token, false))
		},
	})

	// The real approved connect call for the WhatsApp instance — no
	// credentials (WhatsApp pairs by QR), no configure step.
	w.provider.setDefaultScript([]providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "wu7-ts-connect-wa", Type: "function", Name: "ToolSearch",
			Arguments: map[string]any{"names": []any{"connect_channel"}}}}},
		{ToolCalls: []providers.ToolCall{{ID: "wu7-connect-wa", Type: "function", Name: "connect_channel",
			Arguments: map[string]any{
				"id":                waInstance,
				"workspace_id":      "sales",
				"agent_id":          "mia",
				"expected_revision": "absent",
			}}}},
		{Content: "done", FinishReason: "stop"},
	})
	// The conversation lives in the workspace the frame claims: this fixture's
	// turn resolves into "sales" through the message metadata (train-3
	// workspace-immutability guard, 61c4d8153 — a session bound elsewhere is
	// refused), so the main session is created there, not in the harness default.
	main, err := w.al.GetSessionStore().GetOrCreateMainSession("sales", "omnipus")
	require.NoError(t, err)
	turnOwner := wu6Owner
	require.NoError(t, w.al.GetSessionStore().SetMeta(main.ID, session.MetaPatch{Owner: &turnOwner}))
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()
	require.NoError(t, w.bus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel: "webchat", ChatID: "wu7-wa-connect",
		SessionID:     main.ID,
		Content:       "connect the WhatsApp channel whatsapp.wu7",
		UserInitiated: true,
		Metadata:      map[string]string{"agent_id": "omnipus", "workspace_id": "sales"},
	}))
	w.awaitApproval("connect_channel", 15*time.Second)
	w.approveNext() // connect_channel

	// The connect's own report: saved, bound, enabled. The reload above
	// starts the admitted pairing, so the outcome is "saved but not working:
	// pairing not finished".
	reportDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(reportDeadline) {
		result := strings.ToLower(w.provider.lastToolResult())
		if strings.Contains(result, "whatsapp.wu7") && strings.Contains(result, "outcome") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	result := strings.ToLower(w.provider.lastToolResult())
	// liveResult reports "saved but not working: pairing not finished" — the
	// save itself carries the binding and the enable.
	require.Contains(t, result, "whatsapp.wu7", "the connect report names the instance; result: %s", result)
	require.Contains(t, result, "outcome", "the connect report carries its outcome; result: %s", result)
	savedCfg := w.al.GetConfig()
	require.NotNil(t, savedCfg.Channels[waInstance], "the connect saved the instance")
	require.Equal(t, "sales", savedCfg.Channels[waInstance].WorkspaceID, "the connect bound the workspace")
	require.True(t, savedCfg.Channels[waInstance].Enabled, "the connect enabled the instance")

	// The tool-side correlation, exercised the only way a chat exercises it:
	// a Stop of the requesting chat. The loop must find the tool-stamped
	// correlation, reach THIS channel, and emit its ids.
	events := w.al.SubscribeEvents(64)
	_, _, err = w.al.RequestCancelForSession(context.Background(), main.ID, wu6Owner, "webchat")
	require.NoError(t, err, "the chat Stop must be accepted")
	select {
	case <-fake.stopCalled:
	case <-time.After(10 * time.Second):
		t.Fatal("the chat Stop never reached the channel the connect named — the tool-side pairing correlation is missing (K10/FR-081, BDD-8.8)")
	}
	deadline := time.After(10 * time.Second)
	var payload agent.WhatsAppPairingPayload
	for {
		select {
		case evt, ok := <-events.C:
			if !ok {
				t.Fatal("the loop's event subscription closed before the stopped frame arrived")
			}
			if evt.Kind != agent.EventKindWhatsAppPairing {
				continue
			}
			p, assertionOK := evt.Payload.(agent.WhatsAppPairingPayload)
			require.True(t, assertionOK, "pairing event payload type: %T", evt.Payload)
			if p.Status == channels.PairingStatus("stopped") {
				payload = p
			}
		case <-deadline:
			t.Fatal("BLOCKED: no stopped pairing frame arrived after the chat Stop — required by BDD-8.8")
		}
		if payload.RequestID != "" || payload.SessionID != "" {
			break
		}
	}
	require.Equal(t, main.ID, payload.SessionID,
		"the stopped frame carries the chat that ran connect_channel — the identity the TOOL stamped, never the test")
	require.NotEmpty(t, payload.RequestID,
		"the stopped frame carries the connect tool call's request id (K10)")
}

// TestPairingReconnect_KeptTerminalFrameStripsAQRPayload pins the cache's
// defensive QR strip: a terminal payload that somehow carries a QR must have
// it stripped before the frame is kept for reconnects — a kept frame is
// status only, never a code (BDD-8.8, FR-081).
func TestPairingReconnect_KeptTerminalFrameStripsAQRPayload(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, _ := makeForwarderTestConn(64)
	wc.setPairingInterest(pairingTestChannelID, true)
	done := runForwarder(h, wc, "chat-a", bus)

	bus.Emit(agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: agent.WhatsAppPairingPayload{
		ChannelID: pairingTestChannelID, Status: channels.PairingStatus("stopped"),
		QR: "QR-STALE", RequestID: "req-strip", SessionID: "chat-a",
	}})
	deadline := time.After(5 * time.Second)
	for {
		if cached, ok := h.lastPairingState.Load(pairingTestChannelID); ok {
			cachedBytes, isBytes := cached.([]byte)
			require.True(t, isBytes, "fixture: the cached pairing frame is raw bytes")
			frame := pairingFrame(t, cachedBytes)
			require.NotNil(t, frame.RequestId, "fixture: the terminal frame was cached")
			break
		}
		select {
		case <-deadline:
			t.Fatal("the chat-started terminal frame was never kept for reconnects (BDD-8.8)")
		case <-time.After(10 * time.Millisecond):
		}
	}
	bus.Close()
	<-done

	fresh, freshCh := makeForwarderTestConn(64)
	h.subscribePairingInterest(fresh, pairingTestChannelID, true)
	frame := pairingFrame(t, <-freshCh)
	assert.Equal(t, "stopped", frame.Status)
	assert.Nil(t, frame.Qr, "a kept terminal frame never re-emits a QR, even if the payload carried one")
	require.NotNil(t, frame.RequestId)
	assert.Equal(t, "req-strip", *frame.RequestId)
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
