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
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/providers"
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

// issueConnect drives the real on-demand flow: ToolSearch promotes
// configure_channel, whose call carries the non-secret settings; ToolSearch
// promotes connect_channel, whose ask-gated call carries the binding and the
// stored-credential references. Each ask parks the turn; the caller resolves
// the approvals in order.
func (w *wu6Fixture) issueConnect(instanceID, workspace, agentID, secretRef, tokenRef string) *approvalEntry {
	w.t.Helper()
	w.provider.setDefaultScript([]providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "wu7-ts-configure", Type: "function", Name: "ToolSearch",
			Arguments: map[string]any{"names": []any{"configure_channel"}}}}},
		{ToolCalls: []providers.ToolCall{{ID: "wu7-configure", Type: "function", Name: "configure_channel",
			Arguments: map[string]any{"id": instanceID, "expected_revision": "absent"}}}},
		{ToolCalls: []providers.ToolCall{{ID: "wu7-ts-connect", Type: "function", Name: "ToolSearch",
			Arguments: map[string]any{"names": []any{"connect_channel"}}}}},
		{ToolCalls: []providers.ToolCall{{ID: "wu7-connect", Type: "function", Name: "connect_channel",
			Arguments: map[string]any{
				"id":                 instanceID,
				"workspace_id":       workspace,
				"agent_id":           agentID,
				"expected_revision":  "absent",
				"channel_secret_ref": secretRef,
				"access_token_ref":   tokenRef,
			}}}},
		{Content: "done", FinishReason: "stop"},
	})
	w.removeTurnDone = make(chan error, 1)
	go func() {
		_, err := w.al.ProcessDirectWithChannelAs(context.Background(), "omnipus", "connect the channel "+instanceID, "wu7-turn", "webchat", "wu7-chat")
		w.removeTurnDone <- err
	}()
	return w.awaitApproval("configure_channel", 15*time.Second)
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

// finishConnectTurn waits for the parked turn to finish after the last
// approval.
func (w *wu6Fixture) finishConnectTurn() {
	w.t.Helper()
	select {
	case err := <-w.removeTurnDone:
		require.NoError(w.t, err, "the connect turn must complete")
	case <-time.After(20 * time.Second):
		w.t.Fatal("the connect turn never finished after the approvals")
	}
}

// TestWU7ConnectChannel_BadToken_NeverWorking is BDD-8.3 + BDD-8.1: a
// rejected credential set is never reported "working" — the result says
// "not working" (the product names no auth-specific error type) and never
// echoes a credential value.
func TestWU7ConnectChannel_BadToken_NeverWorking(t *testing.T) {
	w := newWU6Fixture(t)
	// The #1277 LINE fixture 401s any bearer except its two known values, so
	// a ref the fixture never seeded makes the platform probe a rejection.
	require.NoError(t, w.store.Set("WU7_BAD_TOKEN", "qa1277-revoked-token-value"))

	w.issueConnect("line.wu7", "sales", "mia", "WU7_BAD_TOKEN", "WU7_BAD_TOKEN")
	w.approveNext() // configure_channel
	w.approveNext() // connect_channel
	w.finishConnectTurn()

	result := strings.ToLower(w.provider.allToolResults())
	require.NotEmpty(t, strings.TrimSpace(result), "instrument: the connect must have produced a tool result")
	withoutNotWorking := strings.ReplaceAll(result, "not working", "")
	require.False(t, strings.Contains(withoutNotWorking, "working"),
		"a rejected credential must never be reported working (BDD-8.3); result: %s", result)
	require.Contains(t, result, "not working", "the result must honestly report not-working (BDD-8.3); result: %s", result)
	require.NotContains(t, result, "qa1277-revoked-token-value", "no credential value may appear in the result (FR-062)")

	// The connection is not left running as if it worked.
	if ch, exists := w.svc.ChannelManager.GetChannel("line.wu7"); exists {
		require.False(t, ch.IsRunning(), "a rejected-credential connection must not run (BDD-8.3)")
	}
}

// TestWU7ConnectChannel_WrongBinding_NothingSaved is BDD-8.7: valid
// credentials with a target agent that is not a member of the target
// workspace fail and save nothing.
func TestWU7ConnectChannel_WrongBinding_NothingSaved(t *testing.T) {
	w := newWU6Fixture(t)
	seedWU5Membership(t, w.f1277.home) // sales claims mia; marketing claims ray
	// A token the fixture platform accepts (bot-info 200 with this bearer).
	require.NoError(t, w.store.Set("WU7_GOOD_TOKEN", issue1277HealthyAccess))

	w.issueConnect("line.wu7b", "marketing", "mia", "WU7_GOOD_TOKEN", "WU7_GOOD_TOKEN")
	w.approveNext() // configure_channel
	w.approveNext() // connect_channel
	w.finishConnectTurn()

	result := strings.ToLower(w.provider.allToolResults())
	require.Contains(t, result, "nothing saved", "a wrong binding must fail with \"failed: nothing saved\" (BDD-8.7); result: %s", result)
	require.NotContains(t, strings.ReplaceAll(result, "not working", ""), "working", "a failed connect must never report working (BDD-8.7)")
	if ch, exists := w.svc.ChannelManager.GetChannel("line.wu7b"); exists {
		require.False(t, ch.IsRunning(), "nothing was saved — no connection may run (BDD-8.7)")
	}
}

// TestWU7Pairing_QRFrameCorrelatesRequest is BDD-8.4 + K10/FR-081: a
// chat-started WhatsApp pairing carries RequestID/SessionID on its payload
// (connect_channel stamps them via AgentLoop.NotePairingRequest) and the
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
// them via AgentLoop.NotePairingRequest); a Settings-started one carries
// neither.
func agent_NewEventForWU7(requestID, sessionID, qr string) agent.Event {
	payload := agent.WhatsAppPairingPayload{ChannelID: "whatsapp_native", Status: channels.PairingStatusCode, QR: qr}
	if requestID != "" {
		payload.RequestID = requestID
		payload.SessionID = sessionID
	}
	return agent.Event{Kind: agent.EventKindWhatsAppPairing, Payload: payload}
}
