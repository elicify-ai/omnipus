// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
)

// Oracle: session-core FR-034 / BDD-10.2 — one native approval frame names the
// ACTING helper or run: its agent_id and session_id are the acting agent and
// the acting session (the run's or helper's own), so the modal can say "approval
// for <helper/run>" and Open that session. The frame is not rewritten to the
// parent chat's key. Real: approvalRegistryV2.requestApproval +
// WSHandler.broadcastToolApprovalRequired.
func TestWS_ToolApprovalRequired_NamesTheActingRunSession(t *testing.T) {
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, _ := newTestWSHandlerForModelName(t, msgBus)

	reg := newApprovalRegistryV2(64, 300*time.Second)
	entry, accepted := reg.requestApproval("tc-run", "bash", map[string]any{"command": "echo hi"},
		"helper-agent", "sess-helper-run-7", "turn-run-7")
	require.True(t, accepted)
	t.Cleanup(func() { go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) }() })
	go func() { <-entry.resultCh }()
	handler.approvalRegV2 = reg

	wc := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{}), userID: "acting-run"}
	handler.mu.Lock()
	if handler.sessions == nil {
		handler.sessions = make(map[string]*wsConn)
	}
	handler.sessions["acting-run"] = wc
	handler.mu.Unlock()

	handler.broadcastToolApprovalRequired(entry)
	select {
	case raw := <-wc.sendCh:
		var frame generated.ToolApprovalRequiredFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		require.Equal(t, "helper-agent", frame.AgentId, "the acting agent")
		require.Equal(t, "sess-helper-run-7", frame.SessionId, "the acting run's own session, the modal's Open target")
		require.Equal(t, entry.ApprovalID, frame.ApprovalId, "one approval id, no duplicate card")
	case <-time.After(2 * time.Second):
		t.Fatal("no tool_approval_required frame")
	}
}
