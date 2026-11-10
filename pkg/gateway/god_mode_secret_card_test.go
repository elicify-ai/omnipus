// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// god_mode_secret_card_test.go — BDD-3.8 (US-3 AS-7, alternate path): under
// God Mode the secret card still appears. A card is not an approval — the
// D5 permission matrix's request_secret row reads "runs (card appears)" in
// all three modes (Ask, Auto on, God Mode), so God Mode floors the global
// tool-policy layer (O14) without ever swallowing the card.
//
// Spec source: docs/internal/specs/omnipus-system-agent-spec.md BDD-3.8 and
// the D5 matrix row traced to it. Expected values derive from the spec, not
// from the implementation.
package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// TestGodMode_RequestSecretStillShowsTheCard drives one real Omnipus web turn
// under God Mode whose model step requests a provider key. BDD-3.8: the
// secret card appears — as a secret_request frame on the chat and as a
// PENDING card in the session's state — and no approval is asked for it: a
// card is not an approval.
func TestGodMode_RequestSecretStillShowsTheCard(t *testing.T) {
	// God Mode availability is a process-level boot decision; pin it on for
	// this test and restore afterwards so other tests see the default.
	agent.SetGodModeAvailable(true)
	t.Cleanup(func() { agent.SetGodModeAvailable(false) })
	require.True(t, sandbox.GodModeAvailable,
		"this build must support god mode — the nogodmode build cannot express the BDD-3.8 row")

	provider := &secretScriptProvider{toolName: "request_secret", toolArgs: map[string]any{
		"purpose":  secretCardPurpose,
		"endpoint": secretCardEndpoint,
	}}
	cfg := secretCardAgentConfig(string(coreagent.IDOmnipus), "Omnipus", false)
	cfg.Sandbox.GodMode = true
	// The explicit ceiling entry stays exactly what a fresh install ships
	// (ADR-092): God Mode floors the global layer at resolution time and must
	// not need the entry changed to let the tool run.
	cfg.Sandbox.ToolPolicies["request_secret"] = "allow"

	al, _, handler := startSecretCardLoop(t, cfg, provider)
	tabs := apprResAttachConns(t, handler, 1)
	main, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, string(coreagent.IDOmnipus))
	require.NoError(t, err)

	handler.handleChatMessageWithClientID(
		context.Background(), "bdd38-godmode", main.ID,
		"I need an OpenAI key for "+secretCardEndpoint,
		string(coreagent.IDOmnipus), nil, "", testHarnessWorkspaceMembershipID,
		"client-bdd38", nil, makeTestConn(),
	)

	// The card appears (BDD-3.8) — the secret_request frame on the chat.
	frame, ok := readFrameType(t, tabs[0], "secret_request", 8*time.Second)
	require.True(t, ok, "BDD-3.8: under God Mode the secret card must still appear on the Omnipus chat")
	encoded, err := json.Marshal(frame)
	require.NoError(t, err)
	var card gen.SecretRequestFrame
	require.NoError(t, json.Unmarshal(encoded, &card), "the frame must match the K1 SecretRequestFrame shape: %s", encoded)
	require.NotEmpty(t, card.RequestId, "the card carries its request id (K10/FR-081)")

	// A card is not an approval: no tool_approval_required frame may arrive
	// for the same turn.
	select {
	case raw := <-tabs[0].sendCh:
		var f map[string]any
		if json.Unmarshal(raw, &f) == nil && f["type"] == "tool_approval_required" {
			t.Fatalf("a card must never arrive as an approval: %s", raw)
		}
	case <-time.After(300 * time.Millisecond):
	}

	// The card is PENDING in the session's state (the K4 session-state view
	// the SPA renders), not merely announced once on the wire.
	deadline := time.Now().Add(8 * time.Second)
	var pending *gen.SessionStateSecretRequest
	for time.Now().Before(deadline) {
		for _, c := range al.SecretRequests().Snapshot("", main.ID) {
			if c.State == "pending" && c.SessionId == main.ID {
				cp := c
				pending = &cp
			}
		}
		if pending != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NotNil(t, pending, "BDD-3.8: the card must be pending in the session's state after the turn parks")
	require.Equal(t, card.RequestId, pending.RequestId, "the pending card is the card the chat was shown")
	require.NotEmpty(t, pending.Purpose, "the pending card carries its purpose (BDD-6.1)")
}
