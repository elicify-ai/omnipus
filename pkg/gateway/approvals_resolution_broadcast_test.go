// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the tool-approval resolution broadcast.
//
// Defect (UAT 2026-09-14, E-3/A-5): a "Tool Approval Required" dialog stayed
// stuck in every tab that had not itself resolved the approval. The registry
// resolved entries on many paths (another tab's decision, timeout, Stop, agent
// deletion, shutdown) but only ever broadcast the REQUEST, never the
// resolution, so other tabs kept a dialog whose actions returned 410 and then
// 404. These tests pin: (1) every pending→terminal path notifies the
// registry's resolution listener exactly once, (2) that listener reaches every
// WS connection as a tool_approval_resolved frame on a Stop and on agent
// deletion, (3) deleting an agent denies its pending approvals, and (4) the
// approval frames carry the requesting session's workspace.

package gateway

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type apprResRecord struct {
	ApprovalID string
	State      ApprovalState
}

type apprResRecorder struct {
	mu   sync.Mutex
	recs []apprResRecord
}

func (rr *apprResRecorder) listen(e *approvalEntry, s ApprovalState) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.recs = append(rr.recs, apprResRecord{ApprovalID: e.ApprovalID, State: s})
}

func (rr *apprResRecorder) snapshot() []apprResRecord {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return append([]apprResRecord(nil), rr.recs...)
}

func apprResAwaitOutcome(t *testing.T, e *approvalEntry) ApprovalOutcome {
	t.Helper()
	select {
	case o := <-e.resultCh:
		return o
	case <-time.After(2 * time.Second):
		t.Fatalf("approval %s: no outcome delivered within 2s", e.ApprovalID)
		return ApprovalOutcome{}
	}
}

// TestApprovalRegistry_ResolutionListener_EveryTerminalPath: each way a
// pending approval can end notifies the listener exactly once, with the
// terminal state that path produces, and a late second action on the same id
// does not notify again.
func TestApprovalRegistry_ResolutionListener_EveryTerminalPath(t *testing.T) {
	const agentID = "agent-res"
	const sessionID = "sess-res"

	cases := []struct {
		name      string
		timeout   time.Duration
		act       func(reg *approvalRegistryV2, e *approvalEntry)
		wantState ApprovalState
	}{
		{"approve", 0, func(reg *approvalRegistryV2, e *approvalEntry) { reg.resolve(e.ApprovalID, ApprovalActionApprove, false) }, ApprovalStateApproved},
		{"deny", 0, func(reg *approvalRegistryV2, e *approvalEntry) { reg.resolve(e.ApprovalID, ApprovalActionDeny, false) }, ApprovalStateDeniedUser},
		{"cancel action", 0, func(reg *approvalRegistryV2, e *approvalEntry) { reg.resolve(e.ApprovalID, ApprovalActionCancel, false) }, ApprovalStateDeniedCancel},
		{"batch short-circuit", 0, func(reg *approvalRegistryV2, e *approvalEntry) { reg.cancelBatchShortCircuit(e.ApprovalID) }, ApprovalStateDeniedBatchShortCircuit},
		{"session stop", 0, func(reg *approvalRegistryV2, _ *approvalEntry) {
			reg.cancelAllPendingForSessions([]string{sessionID}, denialReasonSessionCanceled)
		}, ApprovalStateDeniedCancel},
		{"agent deleted", 0, func(reg *approvalRegistryV2, _ *approvalEntry) {
			reg.cancelAllPendingForAgent(agentID, denialReasonCancel)
		}, ApprovalStateDeniedCancel},
		{"gateway restart", 0, func(reg *approvalRegistryV2, _ *approvalEntry) { reg.cancelAllPendingForRestart() }, ApprovalStateDeniedRestart},
		{"timeout", 30 * time.Millisecond, func(*approvalRegistryV2, *approvalEntry) {}, ApprovalStateDeniedTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			timeout := tc.timeout
			if timeout == 0 {
				timeout = 300 * time.Second
			}
			reg := newApprovalRegistryV2(64, timeout)
			reg.terminalRetention = 0
			rec := &apprResRecorder{}
			reg.setResolutionListener(rec.listen)

			e, accepted := reg.requestApproval("tc-"+tc.name, "write_file",
				map[string]any{"path": "x.txt"}, agentID, sessionID, "turn-res")
			require.True(t, accepted)
			assert.Empty(t, rec.snapshot(), "requesting an approval is not a resolution")

			tc.act(reg, e)
			apprResAwaitOutcome(t, e)

			require.Eventually(t, func() bool { return len(rec.snapshot()) >= 1 }, 2*time.Second, 5*time.Millisecond,
				"%s: resolution listener never called", tc.name)
			assert.Equal(t, []apprResRecord{{ApprovalID: e.ApprovalID, State: tc.wantState}}, rec.snapshot())

			// A late action on the already-terminal approval must not notify again.
			reg.resolve(e.ApprovalID, ApprovalActionDeny, false)
			time.Sleep(20 * time.Millisecond)
			assert.Len(t, rec.snapshot(), 1, "%s: a late action re-notified", tc.name)
		})
	}
}

// TestApprovalRegistry_ResolutionListener_SaturatedNeverNotifies: a request
// refused at the saturation cap was never announced to clients, so it must
// never be announced as resolved either.
func TestApprovalRegistry_ResolutionListener_SaturatedNeverNotifies(t *testing.T) {
	reg := newApprovalRegistryV2(1, 300*time.Second)
	reg.terminalRetention = 0
	rec := &apprResRecorder{}
	reg.setResolutionListener(rec.listen)

	first, accepted := reg.requestApproval("tc-1", "write_file", nil, "a", "s", "t")
	require.True(t, accepted)
	_, accepted = reg.requestApproval("tc-2", "write_file", nil, "a", "s", "t")
	require.False(t, accepted, "second request must hit the cap")
	assert.Empty(t, rec.snapshot())

	reg.resolve(first.ApprovalID, ApprovalActionDeny, false)
	apprResAwaitOutcome(t, first)
	assert.Equal(t, []apprResRecord{{ApprovalID: first.ApprovalID, State: ApprovalStateDeniedUser}}, rec.snapshot())
}

// apprResAttachConns registers n fake WS connections on handler and returns them.
func apprResAttachConns(t *testing.T, handler *WSHandler, n int) []*wsConn {
	t.Helper()
	conns := make([]*wsConn, 0, n)
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.sessions == nil {
		handler.sessions = make(map[string]*wsConn)
	}
	for i := 0; i < n; i++ {
		wc := &wsConn{sendCh: make(chan []byte, 16), doneCh: make(chan struct{}), userID: "tab"}
		handler.sessions["tab-"+string(rune('a'+i))] = wc
		conns = append(conns, wc)
	}
	return conns
}

// apprResReadFrame returns the first frame of the given type in wc's buffer.
func apprResReadFrame(t *testing.T, wc *wsConn, frameType string) map[string]any {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case raw := <-wc.sendCh:
			var f map[string]any
			require.NoError(t, json.Unmarshal(raw, &f))
			if f["type"] == frameType {
				return f
			}
		case <-deadline:
			t.Fatalf("no %s frame reached the connection within 2s", frameType)
			return nil
		}
	}
}

// TestWS_ToolApprovalResolved_BroadcastOnSessionStop: a Stop resolves the
// approval server-side; every connected tab must be told.
func TestWS_ToolApprovalResolved_BroadcastOnSessionStop(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	reg := newApprovalRegistryV2(64, 300*time.Second)
	reg.terminalRetention = 0
	handler.approvalRegV2 = reg
	reg.setResolutionListener(handler.broadcastToolApprovalResolved)
	tabs := apprResAttachConns(t, handler, 2)

	e, accepted := reg.requestApproval("tc-stop", "write_file",
		map[string]any{"path": "x.txt"}, "agent-stop", "sess-stop", "turn-stop")
	require.True(t, accepted)

	handler.buildCancelHooks(nil).CancelPendingApprovals("sess-stop", denialReasonSessionCanceled)
	o := apprResAwaitOutcome(t, e)
	assert.False(t, o.Approved)

	for i, wc := range tabs {
		f := apprResReadFrame(t, wc, "tool_approval_resolved")
		assert.Equal(t, e.ApprovalID, f["approval_id"], "tab %d", i)
		assert.Equal(t, string(ApprovalStateDeniedCancel), f["state"], "tab %d", i)
		assert.Equal(t, "sess-stop", f["session_id"], "tab %d", i)
	}
}

// TestWS_ToolApprovalFrames_CarryWorkspaceID: the approval frames carry the
// workspace of the requesting session — inherited from the delegating parent
// when a child session has none — and omit it for a session with no workspace.
func TestWS_ToolApprovalFrames_CarryWorkspaceID(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	store := al.GetSessionStore()
	require.NotNil(t, store)

	const wsID = "01M2TESTWORKSPACE0000000000"
	parent, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	wsVal := wsID
	require.NoError(t, store.SetMeta(parent.ID, session.MetaPatch{WorkspaceID: &wsVal}))
	child, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	parentID := parent.ID
	require.NoError(t, store.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &parentID}))
	loose, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	reg := newApprovalRegistryV2(64, 300*time.Second)
	handler.approvalRegV2 = reg
	inWS, ok := reg.requestApproval("tc-ws", "write_file", nil, "mia", child.ID, "turn-ws")
	require.True(t, ok)
	noWS, ok := reg.requestApproval("tc-nows", "write_file", nil, "mia", loose.ID, "turn-nows")
	require.True(t, ok)
	t.Cleanup(func() {
		reg.resolve(inWS.ApprovalID, ApprovalActionCancel, false)
		reg.resolve(noWS.ApprovalID, ApprovalActionCancel, false)
	})

	tabs := apprResAttachConns(t, handler, 1)
	handler.broadcastToolApprovalRequired(inWS)
	f := apprResReadFrame(t, tabs[0], "tool_approval_required")
	assert.Equal(t, wsID, f["workspace_id"], "a delegated child inherits its parent's workspace")
	handler.broadcastToolApprovalRequired(noWS)
	f = apprResReadFrame(t, tabs[0], "tool_approval_required")
	assert.NotContains(t, f, "workspace_id", "no workspace → field omitted")

	wc := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
	handler.emitSessionState(wc, "")
	st := apprResReadFrame(t, wc, "session_state")
	pending, _ := st["pending_approvals"].([]any)
	byID := map[string]map[string]any{}
	for _, p := range pending {
		m, ok := p.(map[string]any)
		require.True(t, ok, "pending_approvals entry must be an object, got %T", p)
		id, ok := m["approval_id"].(string)
		require.True(t, ok, "approval_id must be a string, got %T", m["approval_id"])
		byID[id] = m
	}
	require.Contains(t, byID, inWS.ApprovalID)
	require.Contains(t, byID, noWS.ApprovalID)
	assert.Equal(t, wsID, byID[inWS.ApprovalID]["workspace_id"])
	assert.NotContains(t, byID[noWS.ApprovalID], "workspace_id")
}
