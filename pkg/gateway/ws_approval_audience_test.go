package gateway

// Regression coverage for UAT 2026-09-13 D-16 (approval modals shown to
// other accounts) and the D-82 delivery hardening (approval frames are no
// longer dropped on a momentarily full send buffer).

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func audienceConn(userID string, buffer int) *wsConn {
	return &wsConn{
		sendCh: make(chan []byte, buffer),
		doneCh: make(chan struct{}),
		userID: userID,
	}
}

func recvFrame(t *testing.T, wc *wsConn, wait time.Duration) ([]byte, bool) {
	t.Helper()
	select {
	case raw := <-wc.sendCh:
		return raw, true
	case <-time.After(wait):
		return nil, false
	}
}

// TestBroadcastToolApprovalRequired_ScopedToOwningAccount is the D-16
// oracle: the account whose session raised the approval receives the frame,
// a different signed-in account does not, and an identity-less (dev-bypass)
// connection still does.
func TestBroadcastToolApprovalRequired_ScopedToOwningAccount(t *testing.T) {
	founder := audienceConn("founder", 4)
	admin := audienceConn("admin", 4)
	anon := audienceConn("", 4)
	h := &WSHandler{
		sessions: map[string]*wsConn{"c1": founder, "c2": admin, "c3": anon},
		approvalOwnerFn: func(sessionID string) string {
			if sessionID == "sess-founder" {
				return "founder"
			}
			return "unexpected"
		},
	}
	entry := &approvalEntry{
		ApprovalID: "appr-1", ToolCallID: "call-1", ToolName: "request_mount",
		Args: map[string]any{"host_path": "/Users/x/vault"}, AgentID: "agent-1",
		SessionID: "sess-founder", TurnID: "turn-1", ExpiresAt: time.Now().Add(time.Minute),
	}
	h.broadcastToolApprovalRequired(entry)

	raw, ok := recvFrame(t, founder, time.Second)
	require.True(t, ok, "the owning account must receive the approval frame")
	var frame generated.ToolApprovalRequiredFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	assert.Equal(t, "appr-1", frame.ApprovalId)

	_, ok = recvFrame(t, admin, 300*time.Millisecond)
	assert.False(t, ok, "a different signed-in account must NOT receive another account's approval")

	_, ok = recvFrame(t, anon, time.Second)
	assert.True(t, ok, "an identity-less (dev-bypass) connection still receives it: there is no account to scope by")
}

// TestBroadcastToolApprovalRequired_UnknownOwnerReachesEveryone pins the
// fallback: when the owner cannot be resolved the frame goes to every
// connection rather than to nobody (an invisible approval is a hung turn).
func TestBroadcastToolApprovalRequired_UnknownOwnerReachesEveryone(t *testing.T) {
	a := audienceConn("a", 2)
	b := audienceConn("b", 2)
	h := &WSHandler{
		sessions:        map[string]*wsConn{"c1": a, "c2": b},
		approvalOwnerFn: func(string) string { return "" },
	}
	h.broadcastToolApprovalRequired(&approvalEntry{
		ApprovalID: "appr-2", ToolCallID: "call-2", ToolName: "bash",
		SessionID: "sess-channel", TurnID: "turn-2", ExpiresAt: time.Now().Add(time.Minute),
	})
	for _, wc := range []*wsConn{a, b} {
		_, ok := recvFrame(t, wc, time.Second)
		assert.True(t, ok)
	}
}

// TestBroadcastToolApprovalRequired_WaitsForBufferInsteadOfDropping pins
// the D-82 hardening: a send buffer that is momentarily full no longer
// loses the approval frame.
func TestBroadcastToolApprovalRequired_WaitsForBufferInsteadOfDropping(t *testing.T) {
	full := audienceConn("founder", 1)
	full.sendCh <- []byte(`{"type":"filler"}`) // buffer is now full
	h := &WSHandler{
		sessions:        map[string]*wsConn{"c1": full},
		approvalOwnerFn: func(string) string { return "founder" },
	}
	h.broadcastToolApprovalRequired(&approvalEntry{
		ApprovalID: "appr-3", ToolCallID: "call-3", ToolName: "bash",
		SessionID: "s", TurnID: "t", ExpiresAt: time.Now().Add(time.Minute),
	})
	// Drain the filler after a short delay — the approval frame must still
	// arrive because the sender waited rather than dropping.
	time.Sleep(200 * time.Millisecond)
	<-full.sendCh
	raw, ok := recvFrame(t, full, time.Second)
	require.True(t, ok, "the approval frame must be delivered once the buffer drains")
	assert.Contains(t, string(raw), `"appr-3"`)
	assert.Equal(t, int32(0), full.droppedFrames.Load())
}

// TestEmitSessionState_PendingApprovalsScopedToAccount pins the reconnect
// half of D-16: a tab reconnecting under another account must not re-hydrate
// a foreign approval as a blocking stub.
func TestEmitSessionState_PendingApprovalsScopedToAccount(t *testing.T) {
	reg := newApprovalRegistryV2(64, time.Minute)
	_, ok := reg.requestApproval("call-1", "knowledge_edit", map[string]any{}, "agent-1", "sess-founder", "turn-1")
	require.True(t, ok)
	h := &WSHandler{
		approvalRegV2: reg,
		approvalOwnerFn: func(sessionID string) string {
			if sessionID == "sess-founder" {
				return "founder"
			}
			return ""
		},
	}

	admin := audienceConn("admin", 2)
	h.emitSessionState(admin)
	raw, got := recvFrame(t, admin, time.Second)
	require.True(t, got)
	var adminState generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(raw, &adminState))
	assert.Empty(t, adminState.PendingApprovals, "another account's pending approval must not appear in this account's snapshot")

	founder := audienceConn("founder", 2)
	h.emitSessionState(founder)
	raw, got = recvFrame(t, founder, time.Second)
	require.True(t, got)
	var founderState generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(raw, &founderState))
	require.Len(t, founderState.PendingApprovals, 1)
	assert.Equal(t, "knowledge_edit", founderState.PendingApprovals[0].ToolName)
}
