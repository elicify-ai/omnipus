package gateway

// Oracle: r17 SR-1, 10.4 (CK-11). The approval registry, live broadcast and
// reconnect snapshot are real. Fresh artificial values sit in env_overrides
// containers nested under UNKNOWN arguments (object and array); neither
// emitted frame may carry them, while the unknown names and an ordinary
// unknown value stay visible (17.3).
import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func freshApprovalCanary(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 12)
	_, err := rand.Read(buf)
	require.NoError(t, err, "instrument: fresh canary generation")
	return "t8-frame-canary-" + hex.EncodeToString(buf)
}

func TestT8Approval_NestedUnknownEnvOverridesMaskedLiveAndOnReconnect(t *testing.T) {
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	reg := newApprovalRegistryV2(4, 300*time.Second)
	handler.approvalRegV2 = reg
	object, array := freshApprovalCanary(t), freshApprovalCanary(t)
	args := map[string]any{
		"id":       "worker",
		"zz_plain": "must remain visible",
		"zz_probe": map[string]any{"env_overrides": map[string]any{"TOKEN": object}},
		"zz_list":  []any{map[string]any{"env_overrides": map[string]any{"API_KEY": array}}, "plain item"},
	}
	entry, accepted := reg.requestApproval("t8-nested-call", "update_agent", args, "mia", "t8-nested-session", "t8-nested-turn")
	require.True(t, accepted, "instrument: the real registry admitted a pending request")
	t.Cleanup(func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) })
	wc := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{}), userID: "t8-nested-person"}
	handler.mu.Lock()
	if handler.sessions == nil {
		handler.sessions = make(map[string]*wsConn)
	}
	handler.sessions[wc.userID] = wc
	handler.mu.Unlock()

	want := map[string]any{
		"id":       "worker",
		"zz_plain": "must remain visible",
		"zz_probe": map[string]any{"env_overrides": map[string]any{"TOKEN": "[redacted]"}},
		"zz_list":  []any{map[string]any{"env_overrides": map[string]any{"API_KEY": "[redacted]"}}, "plain item"},
	}
	handler.broadcastToolApprovalRequired(entry)
	select {
	case raw := <-wc.sendCh:
		assert.NotContains(t, string(raw), object, "live frame must not carry a nested object env value")
		assert.NotContains(t, string(raw), array, "live frame must not carry a nested array env value")
		var live generated.ToolApprovalRequiredFrame
		require.NoError(t, json.Unmarshal(raw, &live))
		assert.Equal(t, want, live.Args, "unknown names and ordinary values stay visible (17.3)")
	case <-time.After(2 * time.Second):
		t.Fatal("instrument: actual pending request produced no live approval frame")
	}

	raw := handler.sessionStateBytes(wc, "")
	require.NotEmpty(t, raw, "instrument: reconnect snapshot really emitted JSON")
	assert.NotContains(t, string(raw), object)
	assert.NotContains(t, string(raw), array)
	var snapshot generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	require.Len(t, snapshot.PendingApprovals, 1, "10.4 reconnect restores the pending card")
	assert.Equal(t, want, snapshot.PendingApprovals[0].Args)

	assert.Equal(t, map[string]any{"env_overrides": map[string]any{"TOKEN": object}}, entry.Args["zz_probe"],
		"display projection must not change the pending request's own arguments")
	assert.Equal(t, []any{map[string]any{"env_overrides": map[string]any{"API_KEY": array}}, "plain item"}, entry.Args["zz_list"])
}
