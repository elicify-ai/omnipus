// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_run_ask_approval_test.go pins the founder decision of 2026-09-15 for an
// "ask" tool inside a background task run: the call ASKS — it does not silently
// refuse. Through the real approval registry, policy approver adapter and WS
// handler, and a real task started over REST:
//
//   - the call registers one pending approval for the task's own session, and
//     the task waits server-side: the tool has not run and the worker has not
//     been called again;
//   - nothing depends on a browser: with no connection attached the request
//     still waits, and a browser that connects later is shown it — carrying the
//     task's workspace, which is what the cross-workspace banner keys on;
//   - a connected browser receives the live request frame with that workspace;
//   - approving runs the tool and the run continues with its result; denying
//     gives the worker a clear denial result and the run continues to its end.
//
// The approve/deny decision is applied with approvalRegistryV2.resolve — the
// exact call POST /api/v1/tool-approvals/{id} makes.
package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const askProbeToolName = "probe_write_report"

// askProbeTool is a stand-in for a file-writing tool whose policy is "ask".
type askProbeTool struct {
	tools.BaseTool
	mu       sync.Mutex
	executed int
}

func (p *askProbeTool) Name() string        { return askProbeToolName }
func (p *askProbeTool) Description() string { return "writes report.md (test stand-in)" }
func (p *askProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (p *askProbeTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (p *askProbeTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	p.mu.Lock()
	p.executed++
	p.mu.Unlock()
	return &tools.ToolResult{ForLLM: "report.md written"}
}

func (p *askProbeTool) runs() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.executed
}

// askTaskWorker calls the ask tool, then reports blocked with evidence naming
// what the tool returned (a blocked claim ends the task without a Judge), then
// finishes its turn. It records every tool result it was handed.
type askTaskWorker struct {
	mu          sync.Mutex
	calls       int
	toolResults []string
}

func (w *askTaskWorker) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if n := len(msgs); n > 0 && msgs[n-1].Role == "tool" {
		w.toolResults = append(w.toolResults, msgs[n-1].Content)
		if len(w.toolResults) == 1 {
			evidence := "the report tool ran"
			if strings.Contains(msgs[n-1].Content, "permission_denied") {
				evidence = "the report tool was refused"
			}
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
				ID: "call-claim", Type: "function", Name: tools.GoalClaimToolName,
				Arguments: map[string]any{"status": tools.GoalClaimStatusBlocked, "evidence": evidence},
			}}}, nil
		}
		return &providers.LLMResponse{Content: "Reported."}, nil
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: "call-report", Type: "function", Name: askProbeToolName, Arguments: map[string]any{},
	}}}, nil
}

func (w *askTaskWorker) GetDefaultModel() string { return "test-model" }

func (w *askTaskWorker) snapshot() (int, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls, append([]string(nil), w.toolResults...)
}

type askTaskHarness struct {
	api    *restAPI
	reg    *approvalRegistryV2
	ws     *WSHandler
	worker *askTaskWorker
	tool   *askProbeTool
	wsID   string
}

func newAskTaskHarness(t *testing.T) *askTaskHarness {
	t.Helper()
	worker := &askTaskWorker{}
	builder := config.AgentConfig{
		ID: "builder", Name: "Builder",
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
			Policies: map[string]config.ToolPolicy{askProbeToolName: config.ToolPolicyAsk},
		}},
	}
	api := newTestRestAPIAlignedStoresWithProvider(t, worker, builder)
	probe := &askProbeTool{}
	api.agentLoop.RegisterTool(probe)
	reg := newApprovalRegistryV2(64, 10*time.Minute)
	// Production wires the same registry into both the approver adapter and
	// the WS handler (gateway.go); the handler reads it for the reconnect
	// snapshot.
	wsHandler := &WSHandler{agentLoop: api.agentLoop, approvalRegV2: reg}
	api.agentLoop.SetToolApprover(newPolicyApproverAdapter(reg, wsHandler))
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia", "builder"})
	t.Cleanup(func() {
		for _, e := range reg.pendingApprovals() {
			reg.resolve(e.ApprovalID, ApprovalActionCancel, false)
		}
		api.taskExecutor.Drain(10 * time.Second)
	})
	return &askTaskHarness{api: api, reg: reg, ws: wsHandler, worker: worker, tool: probe, wsID: wsID}
}

// startBuilderTask creates a task for builder over REST and starts it.
func (h *askTaskHarness) startBuilderTask(t *testing.T) gen.Task {
	t.Helper()
	created := postTaskForAgent(t, h.api, h.wsID, "builder")
	advanceTaskToNext(t, h.api, created.Id)
	w := patchTask(t, h.api, created.Id, `{"status":"in_progress"}`)
	require.Equal(t, http200, w.Code, "starting the task must be accepted; body=%s", w.Body.String())
	return created
}

const http200 = 200

// awaitOnePendingApproval waits for exactly one pending approval to exist.
func (h *askTaskHarness) awaitOnePendingApproval(t *testing.T) *approvalEntry {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pending := h.reg.pendingApprovals(); len(pending) == 1 {
			return pending[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no single pending approval appeared; pending=%d", len(h.reg.pendingApprovals()))
	return nil
}

func (h *askTaskHarness) awaitTerminal(t *testing.T, id string) gen.Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var cur gen.Task
	for time.Now().Before(deadline) {
		cur = getTaskFull(t, h.api, id)
		if cur.Status == gen.TaskStatusFailed || cur.Status == gen.TaskStatusDone {
			return cur
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not end; status=%s", id, cur.Status)
	return cur
}

// requireWaiting asserts the run is parked on the approval: still in progress,
// the tool not run, and the worker not called again.
func (h *askTaskHarness) requireWaiting(t *testing.T, id string) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, 0, h.tool.runs(), "an ask tool must not run before it is approved")
	calls, _ := h.worker.snapshot()
	assert.Equal(t, 1, calls, "the worker must not be prompted again while its tool call waits")
	assert.Equal(t, gen.TaskStatusInProgress, getTaskFull(t, h.api, id).Status, "the task waits in progress")
}

// Given a task whose worker calls an "ask" tool, with NO browser connected
// When the call is made
// Then one approval is registered for the task's session and the run waits;
// a browser that connects afterwards is shown the request with the task's
// workspace; approving runs the tool and the run continues with its result.
func TestTaskRun_AskTool_WaitsWithNoBrowserAndContinuesWhenApproved(t *testing.T) {
	h := newAskTaskHarness(t)
	created := h.startBuilderTask(t)

	entry := h.awaitOnePendingApproval(t)
	assert.Equal(t, askProbeToolName, entry.ToolName)
	assert.Equal(t, "builder", entry.AgentID)
	assert.Equal(t, getTaskSessionID(t, h.api, created.Id), entry.SessionID,
		"the approval belongs to the task run's own session")
	h.requireWaiting(t, created.Id)

	// A browser connecting now (reconnect snapshot) is shown the request, with
	// the task's workspace attached.
	late := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
	h.ws.emitSessionState(late, "")
	snapshot := apprResReadFrame(t, late, "session_state")
	pending, _ := snapshot["pending_approvals"].([]any)
	require.Len(t, pending, 1, "the waiting approval is in the snapshot")
	first, ok := pending[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, entry.ApprovalID, first["approval_id"])
	assert.Equal(t, h.wsID, first["workspace_id"], "the request carries the task's workspace")

	resolved, gone := h.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone, "the pending approval must resolve")

	final := h.awaitTerminal(t, created.Id)
	assert.Equal(t, 1, h.tool.runs(), "an approved ask tool runs once")
	_, results := h.worker.snapshot()
	require.NotEmpty(t, results)
	assert.Equal(t, "report.md written", results[0], "the run continues with the tool's real result")
	require.NotNil(t, final.Result)
	assert.Equal(t, "Blocked: the report tool ran", *final.Result)
}

// Given a task whose worker calls an "ask" tool while a browser is connected
// When the request is made and the operator denies it
// Then the browser receives the request frame with the task's workspace, the
// tool does not run, the worker is handed a clear denial, and the run goes on
// to its end.
func TestTaskRun_AskTool_BroadcastsWithTheWorkspaceAndDenialReachesTheWorker(t *testing.T) {
	h := newAskTaskHarness(t)
	tabs := apprResAttachConns(t, h.ws, 1)
	created := h.startBuilderTask(t)

	frame := apprResReadFrame(t, tabs[0], "tool_approval_required")
	assert.Equal(t, askProbeToolName, frame["tool_name"])
	assert.Equal(t, h.wsID, frame["workspace_id"], "the live request carries the task's workspace")
	assert.Equal(t, getTaskSessionID(t, h.api, created.Id), frame["session_id"])

	entry := h.awaitOnePendingApproval(t)
	assert.Equal(t, frame["approval_id"], entry.ApprovalID)
	h.requireWaiting(t, created.Id)

	resolved, gone := h.reg.resolve(entry.ApprovalID, ApprovalActionDeny, false)
	require.True(t, resolved && !gone)

	final := h.awaitTerminal(t, created.Id)
	assert.Equal(t, 0, h.tool.runs(), "a denied ask tool never runs")
	_, results := h.worker.snapshot()
	require.NotEmpty(t, results)
	var denial map[string]any
	require.NoError(t, json.Unmarshal([]byte(results[0]), &denial), "the denial is a structured result: %s", results[0])
	assert.Equal(t, "permission_denied", denial["error"])
	assert.Equal(t, "user", denial["reason"], "the worker is told the operator refused it")
	require.NotNil(t, final.Result)
	assert.Equal(t, "Blocked: the report tool was refused", *final.Result)
}
