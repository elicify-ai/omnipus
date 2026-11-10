package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

type l11263Latch struct {
	ch   chan struct{}
	once sync.Once
}

func newL11263Latch() *l11263Latch { return &l11263Latch{ch: make(chan struct{})} }
func (l *l11263Latch) release()    { l.once.Do(func() { close(l.ch) }) }

// #1221 removed the per-agent Never-Auto switch. Chief's 2026-10-18 Q1 A
// replaces that stale brief option with the supported per-chat off switch.
func TestL11263B_PerChatAutoOffTaskAndPlanDenied(t *testing.T) {
	for _, route := range []string{"board", "plan"} {
		t.Run(route, func(t *testing.T) {
			gate := newL11263Latch()
			worker := l11263Script(l11263Write(), l11263Claim())
			worker.steps[0].wait = gate.ch // model-edge barrier, not a policy stub
			h := newL11263Harness(t, true, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
			t.Cleanup(gate.release)
			id := h.startRoute(t, route)
			created, err := h.api.taskStore.Get(id)
			require.NoError(t, err)
			require.NotEmpty(t, created.SessionID)
			require.True(t, h.api.agentLoop.SessionModes().Set(created.SessionID, false))
			gate.release()
			final := h.awaitTask(t, id)
			h.requireRefusal(t, final.SessionID, worker)
		})
	}
}

func TestL11263B_TaskNestedHelpersNeverPrompt(t *testing.T) {
	gate := newL11263Latch()
	root := l11263Script(l11263Delegate(l11263Middle), l11263Claim())
	root.steps[1].wait = gate.ch
	middle := l11263Script(l11263Delegate(l11263Leaf), l11263Call("report-child", "message_parent", map[string]any{"kind": "progress", "text": "child probe observed"}))
	middle.steps[1].wait = gate.ch
	leaf := l11263Script(l11263Write())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{
		l11263Agent: root, l11263Middle: middle, l11263Leaf: leaf,
	})
	t.Cleanup(gate.release)
	created := h.createTask(t, "")
	h.startBoard(t, created)
	h.awaitObservedWithoutPrompt(t, leaf)
	rootTask, err := h.api.taskStore.Get(created.Id)
	require.NoError(t, err)
	h.requireTaskOrigin(t, rootTask.SessionID)
	middleID := l11263DelegateSession(t, root, l11263Middle)
	leafID := l11263DelegateSession(t, middle, l11263Leaf)
	h.requireParent(t, middleID, rootTask.SessionID)
	h.requireParent(t, leafID, middleID)
	h.requireRefusal(t, leafID, leaf)
	h.awaitHelperTerminal(t, leafID)
	gate.release()
	h.awaitTask(t, created.Id)
	h.awaitHelperTerminal(t, middleID)
	h.requireNoApprovals(t)
}

func TestL11263B_PlanHelperNeverPrompts(t *testing.T) {
	gate := newL11263Latch()
	root := l11263Script(l11263Delegate(l11263Leaf), l11263Claim())
	root.steps[1].wait = gate.ch
	leaf := l11263Script(l11263Write())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: root, l11263Leaf: leaf})
	t.Cleanup(gate.release)
	created := h.startPlan(t)
	h.awaitObservedWithoutPrompt(t, leaf)
	rootTask, err := h.api.taskStore.Get(created.Id)
	require.NoError(t, err)
	h.requireTaskOrigin(t, rootTask.SessionID)
	leafID := l11263DelegateSession(t, root, l11263Leaf)
	h.requireParent(t, leafID, rootTask.SessionID)
	h.requireRefusal(t, leafID, leaf)
	h.awaitHelperTerminal(t, leafID)
	gate.release()
	h.awaitTask(t, created.Id)
	h.requireNoApprovals(t)
}

func (h *l11263Harness) awaitObservedWithoutPrompt(t *testing.T, p *l11263Provider) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		h.forbidPrompt(t)
		select {
		case <-p.observed:
			h.forbidPrompt(t)
			return
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("helper/turn never received its tool result; no approval observed (dispatch or harness failure, not no-prompt RED)")
		}
	}
}

func l11263DelegateSession(t *testing.T, parent *l11263Provider, target string) string {
	t.Helper()
	result, ok := parent.result("delegate-" + target)
	require.True(t, ok, "parent must receive the real delegate launch receipt")
	var receipt gen.DelegateSessionResponse
	require.NoError(t, json.Unmarshal([]byte(result), &receipt), "delegate must actually launch, not refuse: %s", result)
	require.NotEmpty(t, receipt.SessionId)
	return receipt.SessionId
}

func (h *l11263Harness) requireParent(t *testing.T, child, parent string) {
	t.Helper()
	meta, err := h.api.agentLoop.GetSessionStore().GetMeta(child)
	require.NoError(t, err)
	require.Equal(t, parent, meta.ParentSessionID, "real helper's ancestry must reach the task root")
	rec, err := h.api.agentLoop.GetSessionLifecycleStore().Load(child)
	require.NoError(t, err)
	require.NotNil(t, rec.SteeredBy)
	require.Equal(t, parent, rec.SteeredBy.SteeringSessionID)
	require.Equal(t, session.OriginKindDelegate, rec.Origin.Kind)
}

func (h *l11263Harness) awaitHelperTerminal(t *testing.T, id string) {
	t.Helper()
	require.Eventually(t, func() bool {
		rec, err := h.api.agentLoop.GetSessionLifecycleStore().Load(id)
		return err == nil && session.IsTerminalLifecycleState(rec.State)
	}, 15*time.Second, 10*time.Millisecond, "helper must really settle before data-root cleanup")
}

func TestL11263B_InteractiveHelperStillPrompts(t *testing.T) {
	gate := newL11263Latch()
	parent := l11263Script(l11263Delegate(l11263Leaf), l11263Call("read-after-child", "list_tasks", map[string]any{}))
	parent.steps[1].wait = gate.ch
	leaf := l11263Script(l11263Write())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: parent, l11263Leaf: leaf})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, err := h.api.agentLoop.ProcessDirectAs(ctx, l11263Agent, "Delegate the file write", "l11263-interactive-parent")
		done <- err
	}()
	t.Cleanup(func() {
		gate.release()
		for _, e := range h.reg.pendingApprovals() {
			h.reg.resolve(e.ApprovalID, ApprovalActionCancel, false)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("interactive parent cleanup did not join")
		}
	})
	entry := h.awaitInteractiveApproval(t)
	require.Equal(t, l11263Leaf, entry.AgentID, "being a child does not make an interactive chain unattended")
	require.NoFileExists(t, filepath.Join(h.workDir, l11263File))
	frame := apprResReadFrame(t, h.tab, "tool_approval_required")
	require.Equal(t, entry.ApprovalID, frame["approval_id"])
	resolved, gone := h.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone)
	h.awaitHelperTerminal(t, entry.SessionID)
	data, err := os.ReadFile(filepath.Join(h.workDir, l11263File))
	require.NoError(t, err)
	require.Equal(t, l11263Content, string(data))
	gate.release()
	select {
	case err := <-done:
		require.NoError(t, err)
		done <- nil
	case <-time.After(15 * time.Second):
		t.Fatal("interactive parent failed to finish")
	}
	h.reg.mu.Lock()
	count := len(h.reg.entries)
	h.reg.mu.Unlock()
	require.Equal(t, 1, count, "exactly one real helper approval")
}
