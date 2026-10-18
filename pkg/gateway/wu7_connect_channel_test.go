// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-7 RED tests (US-8 AS-1–AS-3, BDD-8.1–8.4, FR-021, K10/FR-081):
// connect_channel exists and ships ask; a bad-token one-call connect is never
// reported "working"; a valid-token connect whose target binding is invalid
// saves nothing; and a chat-started WhatsApp pairing correlates its
// whatsapp_pairing frame to the requesting chat (request_id + session_id).
//
// The fake platform is the #1277 fixture's real-LINE-with-redirected-
// transport pattern (api.line.me answered by a loopback protocol fixture);
// the ask gate rides the real approval machinery (same round-trip as the
// WU-6 pack).
package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
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

// issueConnect drives one real connect_channel call through the loop in a
// goroutine — the ask-gated tool parks the turn — and returns the pending
// approval.
func (w *wu6Fixture) issueConnect(args map[string]any) *approvalEntry {
	w.t.Helper()
	w.provider.setDefaultScript([]providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "wu7-connect", Type: "function", Name: "connect_channel",
			Arguments: args}}},
		{Content: "done", FinishReason: "stop"},
	})
	w.removeTurnDone = make(chan error, 1)
	go func() {
		_, err := w.al.ProcessDirectWithChannelAs(context.Background(), "omnipus", "connect the channel", "wu7-turn", "webchat", "wu7-chat")
		w.removeTurnDone <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		pending := w.reg.pendingApprovals()
		if len(pending) == 1 && pending[0].ToolName == "connect_channel" {
			return pending[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	w.t.Fatalf("BLOCKED: no pending connect_channel approval appeared — the tool is not implemented (policy/classification lists only), required by FR-021, BDD-8.1")
	return nil
}

// finishConnectTurn waits for the parked turn to finish after a resolution.
func (w *wu6Fixture) finishConnectTurn() {
	w.t.Helper()
	select {
	case err := <-w.removeTurnDone:
		require.NoError(w.t, err, "the connect turn must complete")
	case <-time.After(20 * time.Second):
		w.t.Fatal("the connect turn never finished after the approval")
	}
}

// TestWU7ConnectChannel_BadToken_NeverWorking is BDD-8.3 + BDD-8.1: a revoked
// token is never reported "working" — the result says "saved but not working:
// token rejected" or "failed: nothing saved".
func TestWU7ConnectChannel_BadToken_NeverWorking(t *testing.T) {
	w := newWU6Fixture(t)
	// The #1277 LINE fixture 401s any bearer except its two known values, so
	// a ref the fixture never seeded makes the platform probe a rejection.
	require.NoError(t, w.store.Set("WU7_BAD_TOKEN", "qa1277-revoked-token-value"))

	entry := w.issueConnect(map[string]any{
		"type":                     "line",
		"instance_id":              "line.wu7",
		"workspace_id":             "sales",
		"agent_id":                 "mia",
		"channel_secret_ref":       "WU7_BAD_TOKEN",
		"channel_access_token_ref": "WU7_BAD_TOKEN",
		"webhook_path":             "/webhook/wu7-line",
	})
	w.approveRemove(entry) // the same resolve-and-wait round-trip: one approval (BDD-8.1)
	w.finishConnectTurn()

	result := w.provider.allToolResults()
	require.NotEmpty(t, strings.TrimSpace(result), "instrument: the connect must have produced a tool result")
	lower := strings.ToLower(result)
	withoutNotWorking := strings.ReplaceAll(lower, "not working", "")
	require.False(t, strings.Contains(withoutNotWorking, "working"),
		"a rejected token must never be reported working (BDD-8.3); result: %s", result)
	require.True(t, strings.Contains(lower, "not working") || strings.Contains(lower, "nothing saved"),
		"the result must say \"saved but not working: token rejected\" or \"failed: nothing saved\" (BDD-8.3); result: %s", result)

	// The connection is not left running as if it worked.
	if ch, exists := w.svc.ChannelManager.GetChannel("line.wu7"); exists {
		require.False(t, ch.IsRunning(), "a rejected-token connection must not run (BDD-8.3)")
	}
}

// TestWU7ConnectChannel_WrongBinding_NothingSaved is BDD-8.7: a valid token
// with a target agent that is not a member of the target workspace fails and
// saves nothing.
func TestWU7ConnectChannel_WrongBinding_NothingSaved(t *testing.T) {
	w := newWU6Fixture(t)
	seedWU5Membership(t, w.f1277.home) // sales claims mia; marketing claims ray
	// A token the fixture platform accepts (bot-info 200 with this bearer).
	require.NoError(t, w.store.Set("WU7_GOOD_TOKEN", issue1277HealthyAccess))

	entry := w.issueConnect(map[string]any{
		"type":                     "line",
		"instance_id":              "line.wu7b",
		"workspace_id":             "marketing",
		"agent_id":                 "mia", // mia is NOT a member of marketing
		"channel_secret_ref":       "WU7_GOOD_TOKEN",
		"channel_access_token_ref": "WU7_GOOD_TOKEN",
		"webhook_path":             "/webhook/wu7-line-b",
	})
	w.approveRemove(entry)
	w.finishConnectTurn()

	result := strings.ToLower(w.provider.allToolResults())
	require.Contains(t, result, "nothing saved", "a wrong binding must fail with \"failed: nothing saved\" (BDD-8.7); result: %s", result)
	require.NotContains(t, strings.ReplaceAll(result, "not working", ""), "working", "a failed connect must never report working (BDD-8.7)")
	if ch, exists := w.svc.ChannelManager.GetChannel("line.wu7b"); exists {
		require.False(t, ch.IsRunning(), "nothing was saved — no connection may run (BDD-8.7)")
	}
}

// TestWU7Pairing_QRFrameCorrelatesRequest is BDD-8.4 + K10/FR-081: a
// chat-started WhatsApp pairing's whatsapp_pairing frame carries request_id
// and session_id, correlating the QR to the requesting chat.
func TestWU7Pairing_QRFrameCorrelatesRequest(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	wc.setPairingInterest("whatsapp_native", true)
	done := runForwarder(h, wc, "wu7-chat", bus)

	// The pairing event a chat-started WhatsApp connect emits (the same
	// machinery EmitWhatsAppPairing drives today; at GREEN the chat-started
	// path stamps the correlation).
	bus.Emit(agent.Event{
		Kind: agent.EventKindWhatsAppPairing,
		Payload: agent.WhatsAppPairingPayload{
			ChannelID: "whatsapp_native",
			Status:    "code",
			QR:        "QR-WU7",
		},
	})

	bus.Close()
	<-done

	var raw map[string]any
	select {
	case data := <-ch:
		require.NoError(t, json.Unmarshal(data, &raw), "frame must be valid JSON")
	default:
		t.Fatal("no whatsapp_pairing frame was forwarded")
	}
	require.Equal(t, "whatsapp_pairing", raw["type"])
	for _, field := range []string{"request_id", "session_id"} {
		v, ok := raw[field]
		require.True(t, ok, "BLOCKED: the whatsapp_pairing frame carries no %s — the K10 correlation (FR-081, BDD-8.4: code and progress render in the requesting chat, correlated by request id) is not produced", field)
		require.NotEmpty(t, v, "the %s must be non-empty for a chat-started pairing (K10, BDD-8.4)", field)
	}
}
