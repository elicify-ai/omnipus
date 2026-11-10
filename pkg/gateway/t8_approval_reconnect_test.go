package gateway

// Oracle: r17 SR-1 and 10.4. The approval registry, live broadcast and the
// reconnect-frame producer are real. Raw artificial values enter the actual
// pending request; no test-owned masker supplies either emitted frame.
import (
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8Approval_ReconnectMasksPendingEnvironmentWithoutChangingExecutionArgs(t *testing.T) {
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	reg := newApprovalRegistryV2(4, 300*time.Second)
	handler.approvalRegV2 = reg
	const secret = "t8-artificial-pending-token-13579"
	args := map[string]any{"id": "worker", "revision": "fixture-revision", "env_overrides": map[string]any{"TOKEN": secret, "SHORT": "abc"}}
	entry, accepted := reg.requestApproval("t8-change-call", "update_agent", args, "mia", "t8-acting-session", "t8-acting-turn")
	require.True(t, accepted, "instrument: the real registry admitted a pending request")
	t.Cleanup(func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) })
	wc := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{}), userID: "t8-reconnect-person"}
	handler.mu.Lock()
	if handler.sessions == nil {
		handler.sessions = make(map[string]*wsConn)
	}
	handler.sessions[wc.userID] = wc
	handler.mu.Unlock()
	handler.broadcastToolApprovalRequired(entry)
	var live generated.ToolApprovalRequiredFrame
	select {
	case raw := <-wc.sendCh:
		require.NoError(t, json.Unmarshal(raw, &live))
	case <-time.After(2 * time.Second):
		t.Fatal("instrument: actual pending request produced no live approval frame")
	}
	wantArgs := map[string]any{"id": "worker", "revision": "fixture-revision", "env_overrides": map[string]any{"TOKEN": "[redacted]", "SHORT": "[redacted]"}}
	assert.Equal(t, wantArgs, live.Args, "SR-1 masks both long and short values in the actual live frame")
	for reconnect := 0; reconnect < 2; reconnect++ {
		raw := handler.sessionStateBytes(wc, "")
		require.NotEmpty(t, raw, "instrument: reconnect snapshot really emitted JSON")
		var snapshot generated.SessionStateFrame
		require.NoError(t, json.Unmarshal(raw, &snapshot))
		require.Len(t, snapshot.PendingApprovals, 1, "10.4 reconnect must restore the pending card, not hide it")
		got := snapshot.PendingApprovals[0]
		assert.Equal(t, entry.ApprovalID, got.ApprovalId)
		assert.Equal(t, entry.ToolCallID, got.ToolCallId)
		assert.Equal(t, entry.TurnID, got.TurnId)
		assert.Equal(t, wantArgs, got.Args, "10.4 uses [redacted] on every reconnect, not only the initial broadcast")
		assert.NotContains(t, string(raw), secret)
	}
	assert.Equal(t, map[string]any{"id": "worker", "revision": "fixture-revision", "env_overrides": map[string]any{"TOKEN": secret, "SHORT": "abc"}}, entry.Args, "display projection must not replace the original arguments needed after approval")
	require.Len(t, reg.pendingApprovals(), 1, "snapshotting never consumes or duplicates a pending approval")
}
