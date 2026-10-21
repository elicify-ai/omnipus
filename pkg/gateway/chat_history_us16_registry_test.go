package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// The current wire may omit WU-8's optional fields; the actual pending request
// may not lose its complete execution identity or arguments. Compare with the
// scripted instruction/live call, never manufacture a registry entry for Retry.
func (h *historyUS16Harness) assertPendingRegistry(t *testing.T, live generated.ToolApprovalRequiredFrame) {
	t.Helper()
	pending := h.reg.pendingApprovals()
	require.Len(t, pending, 1)
	entry := pending[0]
	require.Equal(t, live.ApprovalId, entry.ApprovalID)
	require.Equal(t, h.sid, entry.SessionID)
	require.Equal(t, "mia", entry.AgentID)
	require.Equal(t, "write_file", entry.ToolName)
	require.Equal(t, historyUS16CallID, entry.ToolCallID)
	require.Equal(t, live.TurnId, entry.TurnID)
	require.Equal(t, map[string]any{"path": historyUS16Filename, "content": historyUS16Proof}, entry.Args)
}
