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
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
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

// revisionFromListReport reads the revision list_channels reported to the
// model — the tool's own report, never a value recomputed by the test. The
// report sits among the recorded tool results (the last one at park time is
// the ToolSearch promotion dump), so every `"revision"` occurrence in every
// recorded result is scanned for the first live value; the unconfigured
// type defaults read "absent", and the schema dumps name only
// "expected_revision", never `"revision"` with a value.
func (w *wu6Fixture) revisionFromListReport(t *testing.T) string {
	t.Helper()
	report := w.provider.allToolResults()
	token := `"revision"`
	for at := strings.Index(report, token); at >= 0; {
		rest := strings.TrimSpace(report[at+len(token):])
		rest = strings.TrimPrefix(rest, ":")
		rest = strings.TrimSpace(rest)
		var value string
		if strings.HasPrefix(rest, `"`) {
			// A quoted JSON string: the value ends at the closing quote.
			if end := strings.Index(rest[1:], `"`); end >= 0 {
				value = rest[1 : 1+end]
			}
		} else {
			value = strings.Trim(strings.SplitN(rest, ",", 2)[0], `",`)
		}
		if value != "" && value != "absent" {
			return value
		}
		next := strings.Index(report[at+len(token):], token)
		if next < 0 {
			break
		}
		at = at + len(token) + next
	}
	t.Fatal("BLOCKED: list_channels' report carries no live revision — the connect flow needs the current revision (stale writes are refused)")
	return ""
}

// TestWU7ConnectChannel_BadToken_NeverWorking is BDD-8.3 + BDD-8.1: a
// rejected credential set is never reported "working" — the result says
// "not working" (the product names no auth-specific error type) and never
// echoes a credential value.
func TestWU7ConnectChannel_BadToken_NeverWorking(t *testing.T) {
	w := newWU6Fixture(t)
	// BDD-8.3's binding is VALID: sales exists with mia on its team, so the
	// flow reaches the platform probe — the rejection must come from the
	// credential, never from the binding.
	seedWU5Membership(t, w.f1277.home)
	// The #1277 LINE fixture 401s any bearer except its two known values, so
	// the value behind the committed references makes the platform probe a
	// rejection.
	badValue := "qa1277-revoked-token-value"

	// The ruled connect flow, driven for real: configure saves the
	// instance's non-secret settings; list_channels reports the revision;
	// request_secret raises one card per credential field; the person
	// submits each through the real K5 route; connect_channel then binds,
	// enables and live-tests with the returned references and the current
	// revision. The dynamic half of the script (references, revision) is
	// injected while the turn is parked on a card, before its resume.
	toolSearch := func(id, name string) providers.LLMResponse {
		return providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: id, Type: "function", Name: "ToolSearch",
			Arguments: map[string]any{"names": []any{name}}}}}
	}
	w.provider.setDefaultScript([]providers.LLMResponse{
		toolSearch("wu7-ts-configure", "configure_channel"),
		{ToolCalls: []providers.ToolCall{{ID: "wu7-configure", Type: "function", Name: "configure_channel",
			Arguments: map[string]any{"id": "line.wu7", "expected_revision": "absent"}}}},
		toolSearch("wu7-ts-list", "list_channels"),
		{ToolCalls: []providers.ToolCall{{ID: "wu7-list", Type: "function", Name: "list_channels"}}},
		toolSearch("wu7-ts-secret-cs", "request_secret"),
		{ToolCalls: []providers.ToolCall{{ID: "wu7-secret-cs", Type: "function", Name: "request_secret",
			Arguments: map[string]any{"purpose": "LINE credentials for line.wu7", "instance": "line.wu7", "field": "channel_secret"}}}},
		// The second card's request is static too — it rides in the same
		// script so no resume races a mid-turn injection.
		{ToolCalls: []providers.ToolCall{{ID: "wu7-secret-at", Type: "function", Name: "request_secret",
			Arguments: map[string]any{"purpose": "LINE access token for line.wu7", "instance": "line.wu7", "field": "access_token"}}}},
	})
	// The turn runs in the Omnipus MAIN (a real session whose meta the
	// secret-request registry resolves — a keyed ProcessDirect session is
	// not), as a plain webchat owner message.
	main, err := w.al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "omnipus")
	require.NoError(t, err)
	turnOwner := wu6Owner
	require.NoError(t, w.al.GetSessionStore().SetMeta(main.ID, session.MetaPatch{Owner: &turnOwner}))
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()
	require.NoError(t, w.bus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel: "webchat", ChatID: "wu7-connect",
		SessionID:     main.ID,
		Content:       "connect the channel line.wu7",
		UserInitiated: true,
		Metadata:      map[string]string{"agent_id": "omnipus", "workspace_id": "sales"},
	}))
	w.awaitApproval("configure_channel", 15*time.Second)
	w.approveNext() // configure_channel

	submitCard := func(card gen.SessionStateSecretRequest) []string {
		values := map[string]string{}
		for _, field := range card.Fields {
			values[field.Key] = badValue
		}
		raw, marshalErr := json.Marshal(gen.SecretSubmitRequest{Values: &values})
		require.NoError(t, marshalErr)
		resp := w.do(http.MethodPost, "/api/v1/secret-requests/"+card.RequestId+"/submit", string(raw))
		require.Equal(t, http.StatusOK, resp.Code, "K5 submit: %s", resp.Body.String())
		var state gen.SecretRequestState
		require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &state))
		require.Equal(t, gen.SecretRequestStateStateSubmitted, state.State)
		require.NotNil(t, state.ReferenceNames, "a submitted card returns its reference names (K5, FR-068)")
		return *state.ReferenceNames
	}

	card1 := w.awaitPendingCard(nil)
	// The revision and the second reference's name are both derivable now:
	// the approved configure materialized the instance (list_channels'
	// report carries its live revision), and the recipient derivation the
	// tool itself performs names the access-token reference (base + first
	// version).
	revision := w.revisionFromListReport(t)
	candidate := &config.Config{Channels: map[string]config.ChannelInstanceConfig{
		"line.wu7": w.al.GetConfig().Channels["line.wu7"],
	}}
	recipients, _, _, err := secretrequest.ResolveRecipient(candidate, secretrequest.Input{Instance: "line.wu7", Field: "access_token"})
	require.NoError(t, err, "instrument: the saved channel must resolve its access_token recipient")
	require.NotEmpty(t, recipients)
	expectedRef2 := secretrequest.ReferenceBase(recipients[0]) + ".v1"
	refs1 := submitCard(card1)
	require.Len(t, refs1, 1)

	card2 := w.awaitPendingCard([]string{card1.RequestId})
	// Inject the connect step before card2's resume-consuming submit: the
	// resume after card1 ran the static request_secret above, so nothing
	// races this injection, and the connect carries the real references and
	// the real revision. The steps are APPENDED — the provider's call
	// counter is cumulative across the session's resumed turns.
	w.provider.appendDefaultScript([]providers.LLMResponse{
		toolSearch("wu7-ts-connect", "connect_channel"),
		{ToolCalls: []providers.ToolCall{{ID: "wu7-connect", Type: "function", Name: "connect_channel",
			Arguments: map[string]any{
				"id":                 "line.wu7",
				"workspace_id":       "sales",
				"agent_id":           "mia",
				"expected_revision":  revision,
				"channel_secret_ref": refs1[0],
				"access_token_ref":   expectedRef2,
			}}}},
		{Content: "done", FinishReason: "stop"},
	})
	refs2 := submitCard(card2)
	require.Len(t, refs2, 1)
	// The connect step is itself ask-gated: its approval is the last gate
	// before the tool binds, enables and live-tests.
	w.awaitApproval("connect_channel", 15*time.Second)
	w.approveNext() // connect_channel
	// The turn ends after the connect's report reaches the model; the report
	// itself is the oracle input.
	reportDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(reportDeadline) {
		result := strings.ToLower(w.provider.lastToolResult())
		if strings.Contains(result, "line.wu7") && strings.Contains(result, "outcome") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The connect step's own report — not the ToolSearch catalog dump, whose
	// embedded schema descriptions legitimately contain the word "working".
	result := strings.ToLower(w.provider.lastToolResult())
	require.NotEmpty(t, strings.TrimSpace(result), "instrument: the connect must have produced a tool result")
	withoutNotWorking := strings.ReplaceAll(result, "not working", "")
	require.False(t, strings.Contains(withoutNotWorking, "working"),
		"a rejected credential must never be reported working (BDD-8.3); result: %s", result)
	require.Contains(t, result, "not working", "the result must honestly report not-working (BDD-8.3); result: %s", result)
	require.NotContains(t, result, badValue, "no credential value may appear in the result (FR-062)")

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
	w.awaitApproval("connect_channel", 15*time.Second)
	w.approveNext() // connect_channel
	w.finishConnectTurn()

	// The connect step's own report — see the bad-token test's note on the
	// ToolSearch catalog dump.
	result := strings.ToLower(w.provider.lastToolResult())
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
