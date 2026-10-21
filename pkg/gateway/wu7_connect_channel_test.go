// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-7 RED tests (US-8 AS-1–AS-3, BDD-8.1–8.4, FR-021, K10/FR-081):
// connect_channel exists and ships ask; a bad-credential one-call connect is
// never reported "working"; a valid-credential connect whose target binding
// is invalid saves nothing; and a chat-started WhatsApp pairing correlates
// its whatsapp_pairing frame to the requesting chat (request_id + session_id)
// while a Settings-started pairing stays uncorrelated.
//
// Flow per the chief's steering: connect_channel loads on demand
// (ToolSearch promotion first, like remove_secret), and non-secret settings
// go through configure_channel BEFORE the connect call. BDD-8.3's wording is
// the product's honest "not working" (no auth-specific error type exists).
package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestWU7ConnectChannel_ToolRegistered is FR-021's surface deliverable.
func TestWU7ConnectChannel_ToolRegistered(t *testing.T) {
	registry, _ := buildCentralBuiltinRegistry(nil)
	for _, entry := range registry.Describe() {
		if entry.Name == "connect_channel" {
			return
		}
	}
	t.Fatal("BLOCKED: connect_channel is not in the central builtin catalog — required by FR-021, BDD-8.1 (US-8 AS-1: one call configures, binds, enables and tests)")
}

// awaitApproval waits for exactly one pending approval for a tool.
func (w *wu6Fixture) awaitApproval(toolName string, within time.Duration) *approvalEntry {
	w.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		pending := w.reg.pendingApprovals()
		if len(pending) == 1 && pending[0].ToolName == toolName {
			return pending[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	w.t.Fatalf("BLOCKED: no pending %s approval appeared — the tool is not implemented or not offered on demand, required by FR-021/FB-021 (BDD-8.1)", toolName)
	return nil
}

// approveNext resolves the current pending approval and returns the NEXT
// pending ask approval (the connect step follows the configure step).
func (w *wu6Fixture) approveNext() {
	w.t.Helper()
	pending := w.reg.pendingApprovals()
	require.Len(w.t, pending, 1, "exactly one ask approval must be pending")
	resolved, _ := w.reg.resolve(pending[0].ApprovalID, ApprovalActionApprove, false)
	require.True(w.t, resolved, "the pending approval must resolve")
}

// awaitPendingCard waits for a NEW pending secret card raised inside the
// connect turn and returns it. seen lists request ids already submitted.
func (w *wu6Fixture) awaitPendingCard(seen []string) gen.SessionStateSecretRequest {
	w.t.Helper()
	isSeen := func(id string) bool {
		for _, s := range seen {
			if s == id {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		// The owner must match the card's ancestry owner (the turn session's
		// stamped owner): Snapshot filters on it, and an empty owner matches
		// only ownerless records.
		for _, card := range w.al.SecretRequests().Snapshot(wu6Owner, "") {
			if card.State == "pending" && !isSeen(card.RequestId) {
				require.NotEmpty(w.t, card.Fields, "the pending card carries its requested fields (K5 submits exactly those)")
				return card
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	w.t.Fatal("BLOCKED: no pending credential card appeared — request_secret must raise it for the connect flow (FR-068, the ruled connect shape)")
	return gen.SessionStateSecretRequest{}
}

// TestWU7ConnectChannel_BadToken_NeverWorking is BDD-8.3 + BDD-8.1: a
// rejected credential set is never reported "working" — the result says
// "not working" (the product names no auth-specific error type) and never
// echoes a credential value.
func TestWU7ConnectChannel_BadToken_NeverWorking(t *testing.T) {
	// Gate r1 pr-test F1: the old fixture returned before platform startup.
	// Use the real reload and a counted, clean 401 so only a genuinely
	// rejected card-bound credential can satisfy the original oracle.
	f := newWU7LiveFixture(t)
	attempt := wu7ConnectJourney(t, f, "mia", issue1277UnauthorizedAccess)
	wu7RequireRejectedCredential(t, f, attempt)
}

// TestWU7ConnectChannel_WrongBinding_NothingSaved is BDD-8.7: valid
// credentials with a target agent that is not a member of the target
// workspace fail and save nothing.
func TestWU7ConnectChannel_WrongBinding_NothingSaved(t *testing.T) {
	// CHECK r1 F1 / pr-test r1 F2: valid card-bound references, exact
	// current revision and a passing member control. The non-member case
	// changes ONLY the target; the refusal must come from the binding rule.
	wu7RequireMembershipRule(t)
}

// TestWU7Pairing_QRFrameCorrelatesRequest is BDD-8.4 + K10/FR-081: a
// chat-started WhatsApp pairing carries RequestID/SessionID on its payload
// (connect_channel admits its attempt via AgentLoop.AdmitConnectPairing) and the
// forwarder copies both into the whatsapp_pairing frame; a Settings-started
// pairing carries neither and the frame stays uncorrelated.
func TestWU7Pairing_QRFrameCorrelatesRequest(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()

	// Chat-started: the payload carries the correlation.
	wc, ch := makeForwarderTestConn(64)
	wc.setPairingInterest("whatsapp_native", true)
	_ = runForwarder(h, wc, "wu7-chat", bus)
	bus.Emit(agent_NewEventForWU7("wu7-req-1", "wu7-chat", "QR-WU7"))

	var started map[string]any
	require.Eventually(t, func() bool {
		select {
		case data := <-ch:
			require.NoError(t, json.Unmarshal(data, &started), "frame must be valid JSON")
			return true
		default:
			return false
		}
	}, 5*time.Second, 20*time.Millisecond, "no whatsapp_pairing frame was forwarded for the chat-started pairing")

	require.Equal(t, "whatsapp_pairing", started["type"])
	require.Equal(t, "wu7-req-1", started["request_id"],
		"the chat-started pairing frame carries the request id (K10, BDD-8.4); got %v", started["request_id"])
	require.Equal(t, "wu7-chat", started["session_id"],
		"the chat-started pairing frame carries the requesting session (K10)")

	// Settings-started control: no correlation on the payload — the frame
	// stays uncorrelated (fields absent), per the ruled shape.
	wc2, ch2 := makeForwarderTestConn(64)
	wc2.setPairingInterest("whatsapp_native", true)
	_ = runForwarder(h, wc2, "settings-chat", bus)
	bus.Emit(agent_NewEventForWU7("", "", "QR-WU7-SETTINGS"))

	var settings map[string]any
	require.Eventually(t, func() bool {
		select {
		case data := <-ch2:
			require.NoError(t, json.Unmarshal(data, &settings), "control frame must be valid JSON")
			return true
		default:
			return false
		}
	}, 5*time.Second, 20*time.Millisecond, "no control frame was forwarded")

	_, hasReq := settings["request_id"]
	require.False(t, hasReq, "a Settings-started pairing stays uncorrelated (ruled shape): request_id must be absent")
	_, hasSess := settings["session_id"]
	require.False(t, hasSess, "a Settings-started pairing stays uncorrelated: session_id must be absent")
}

// agent_NewEventForWU7 builds the pairing event the two start paths emit:
// a chat-started pairing carries RequestID/SessionID (connect_channel stamps
// them by admitting its attempt via AgentLoop.AdmitConnectPairing); a Settings-started one carries
// neither.
func agent_NewEventForWU7(requestID, sessionID, qr string) agent.Event {
	payload := agent.WhatsAppPairingPayload{ChannelID: "whatsapp_native", Status: channels.PairingStatusCode, QR: qr}
	if requestID != "" {
		payload.RequestID = requestID
		payload.SessionID = sessionID
	}
	return agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: payload}
}
