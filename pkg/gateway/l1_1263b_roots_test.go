package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Most fixtures intentionally exercise independent task roots. Cover the
// production MAIN-mode dispatch too: a task child of its assignee's main is
// still a task run (R5), never an interactive apply helper (R3).
func TestL11263B_MainModeTaskAndPlanNeverPrompt(t *testing.T) {
	for _, route := range []string{"board", "plan"} {
		t.Run(route, func(t *testing.T) {
			worker := l11263Script(l11263Write(), l11263Claim())
			h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
			h.api.taskExecutor.SetMainSessionResolver(taskMainResolver{api: h.api})
			id := h.startRoute(t, route)
			current, err := h.api.taskStore.Get(id)
			require.NoError(t, err)
			meta, err := h.api.agentLoop.GetSessionStore().GetMeta(current.SessionID)
			require.NoError(t, err)
			mainID, err := session.MainSessionID(testHarnessWorkspaceMembershipID, l11263Agent)
			require.NoError(t, err)
			require.Equal(t, mainID, meta.ParentSessionID, "must drive the real MAIN child branch, not another isolated run")
			final := h.awaitTask(t, id)
			h.requireTaskOrigin(t, final.SessionID)
			h.requireRefusal(t, final.SessionID, worker)
		})
	}
}

// R2: a receiver main is not a helper. A real unattended plan member sends a
// real peer request; the receiver's Ask must remain interactive. This guards
// against carrying the sender's unattended flag across the messaging edge.
func TestL11263B_ReceiverMainIsInteractive(t *testing.T) {
	gate := newL11263Latch()
	sender := l11263Script(l11263Call("peer-request", "send_message", map[string]any{
		"workspace_id": testHarnessWorkspaceMembershipID, "agent_id": l11263Leaf, "content": "Write the probe file",
	}), l11263Claim())
	sender.steps[1].wait = gate.ch
	receiver := l11263Script(l11263Write())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: sender, l11263Leaf: receiver})
	h.ws.home = h.api.homePath
	h.api.agentLoop.SetAddressDeps(gatewayAddressDeps{h: h.ws})
	t.Cleanup(gate.release)
	h.startPump(t)
	member := h.startPlan(t)
	entry := h.awaitInteractiveApproval(t)
	mainID, err := session.MainSessionID(testHarnessWorkspaceMembershipID, l11263Leaf)
	require.NoError(t, err)
	require.Equal(t, l11263Leaf, entry.AgentID, "the receiver, not the unattended sender, asks")
	require.Equal(t, mainID, entry.SessionID)
	meta, err := h.api.agentLoop.GetSessionStore().GetMeta(mainID)
	require.NoError(t, err)
	require.Empty(t, meta.ParentSessionID, "a peer message must break the sender's chain")
	rec, err := h.api.agentLoop.GetSessionLifecycleStore().Load(mainID)
	require.NoError(t, err)
	require.Equal(t, session.OriginKindMain, rec.Origin.Kind)
	require.NoFileExists(t, filepath.Join(h.workDir, l11263File))
	frame := apprResReadFrame(t, h.tab, "tool_approval_required")
	require.Equal(t, entry.ApprovalID, frame["approval_id"])
	resolved, gone := h.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone)
	h.awaitTurnSettled(t, mainID)
	data, err := os.ReadFile(filepath.Join(h.workDir, l11263File))
	require.NoError(t, err)
	require.Equal(t, l11263Content, string(data))
	gate.release()
	h.api.taskExecutor.Drain(10 * time.Second)
	current, err := h.api.taskStore.Get(member.Id)
	require.NoError(t, err)
	h.requireTaskOrigin(t, current.SessionID)
	h.reg.mu.Lock()
	count := len(h.reg.entries)
	h.reg.mu.Unlock()
	require.Equal(t, 1, count, "only the separate interactive receiver may have raised an approval")
	require.Empty(t, h.reg.pendingApprovals())
}
