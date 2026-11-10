// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_run_ask_approval_test.go — RE-AIMED by #1263 (founder 2026-10-13,
// L1-AMENDMENTS-20261013.md §1.2 R1/R4/R5; amendment §1.5 names this
// inversion). The former tests pinned the superseded 2026-09-15 decision
// that an ask tool inside a task run ASKS and waits. Task and plan runs —
// whatever started them — never ask: an Ask call that Auto, an operator rule
// or a standing grant does not settle is refused at once with the fixed
// visible reason, and a blocked goal_claim ends the task (Q2 A). Through the
// real approval registry, policy approver adapter, WS handler and a real
// task started over REST:
//
//   - no approval is ever registered and no tool_approval_required frame is
//     broadcast — a pending Ask inside a task chain is the superseded
//     behaviour itself, so its appearance is an immediate named failure;
//   - the worker receives the structured refusal carrying the amendment's
//     fixed reason and model instruction, classified permanent;
//   - the transcript keeps one settled "denied" row with the amendment's
//     "Not run:" text;
//   - the run continues past the refusal — the same run's allow-ceiling
//     control call still executes — and the blocked claim ends the task
//     Failed.
//
// This fixture runs with Auto-approve off, so the positive control is the
// allow-ceiling call; the Auto-settled variant of the same control is
// TestL11263B_AutoApprovedTaskAndPlanRun.
package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// The amendment's fixed R4 texts, spelled out here rather than read from the
// implementation's constants: the oracle comes from
// L1-AMENDMENTS-20261013.md §1.2, so an implementation that drops or rewords
// the row fails these tests instead of silently changing with it.
const (
	taskPlanDenialReason     = "auto-denied: ask-policy tool not allowed in a task or plan run"
	taskPlanDenialModelText  = "Task and plan runs never ask a person for approval. This call needed approval and was refused. Do not retry; finish what you can and report what was blocked."
	taskPlanDenialTranscript = "Not run: needs approval, and task and plan runs never ask."
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

const (
	taskControlFileName = "task-run-control.txt"
	taskControlFileText = "allow-ceiling control content\n"
)

// taskRefusalWorker calls the ask probe, then an allow-ceiling control tool
// in the SAME run, then reports blocked with evidence naming both results (a
// blocked claim ends the task, Q2 A). It records every tool result it was
// handed.
type taskRefusalWorker struct {
	mu          sync.Mutex
	calls       int
	toolResults []string
	controlText string
}

func (w *taskRefusalWorker) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	// Merge of release 200d2aac8 (prompt caching appends a volatile tail, so
	// the scripted worker reads the conversation before it) with Train 3's
	// three-step scenario (refused probe, read_file control, claim).
	conversation, err := gatewayConversationBeforeVolatileTail(msgs)
	if err != nil {
		return nil, err
	}
	if last := conversation[len(conversation)-1]; last.Role == "tool" {
		w.toolResults = append(w.toolResults, last.Content)
		switch len(w.toolResults) {
		case 1:
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
				ID: "call-control", Type: "function", Name: "read_file",
				Arguments: map[string]any{"path": taskControlFileName},
			}}}, nil
		case 2:
			w.controlText = last.Content
			evidence := "the probe was refused and the control file read: " + w.controlText
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
				ID: "call-claim", Type: "function", Name: tools.GoalClaimToolName,
				Arguments: map[string]any{"status": tools.GoalClaimStatusBlocked, "evidence": evidence},
			}}}, nil
		default:
			return &providers.LLMResponse{Content: "Reported."}, nil
		}
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: "call-report", Type: "function", Name: askProbeToolName, Arguments: map[string]any{},
	}}}, nil
}

func (w *taskRefusalWorker) GetDefaultModel() string { return "test-model" }

func (w *taskRefusalWorker) snapshot() (int, []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls, append([]string(nil), w.toolResults...)
}

type askTaskHarness struct {
	api    *restAPI
	reg    *approvalRegistryV2
	ws     *WSHandler
	worker *taskRefusalWorker
	tool   *askProbeTool
	wsID   string
}

func newAskTaskHarness(t *testing.T) *askTaskHarness {
	t.Helper()
	worker := &taskRefusalWorker{}
	builder := config.AgentConfig{
		ID: "builder", Name: "Builder",
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
			// The probe is the Ask call under test; read_file is the positive
			// control — an explicitly allowed call in the SAME run that must
			// keep executing past the refusal. (With no entry anywhere,
			// read_file would resolve fail-closed deny — Hard Constraint #6 —
			// and could not serve as the control.)
			Policies: map[string]config.ToolPolicy{
				askProbeToolName: config.ToolPolicyAsk,
				"read_file":      config.ToolPolicyAllow,
			},
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

// writeTaskControlFile seeds the allow-ceiling control call's target inside
// the task's own workspace work folder.
func writeTaskControlFile(t *testing.T, h *askTaskHarness) {
	t.Helper()
	workDir, err := workspace.EnsureWorkDir(h.api.homePath, h.wsID)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(workDir, taskControlFileName), []byte(taskControlFileText), 0o600))
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

// requireNoPendingApproval is the amendment's fail-fast guard: a pending Ask
// inside a task chain is the superseded behaviour itself, so its appearance
// is an immediate named failure carrying the evidence — never a timeout.
func (h *askTaskHarness) requireNoPendingApproval(t *testing.T) {
	t.Helper()
	if pending := h.reg.pendingApprovals(); len(pending) != 0 {
		e := pending[0]
		t.Fatalf("#1263 R1/R5 violated: a task run raised an approval prompt instead of refusing at once: "+
			"tool=%s agent=%s session=%s approval=%s", e.ToolName, e.AgentID, e.SessionID, e.ApprovalID)
	}
}

// awaitTerminalRefused waits for the task to end, failing fast on any pending
// approval along the way. The deadline is a deadlock guard, never an absence
// oracle: the refusal is asserted from the worker's tool result, not from the
// clock.
func (h *askTaskHarness) awaitTerminalRefused(t *testing.T, id string) gen.Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var cur gen.Task
	for time.Now().Before(deadline) {
		h.requireNoPendingApproval(t)
		cur = getTaskFull(t, h.api, id)
		if cur.Status == gen.TaskStatusFailed || cur.Status == gen.TaskStatusDone {
			h.api.taskExecutor.Drain(10 * time.Second)
			return cur
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not end; status=%s", id, cur.Status)
	return cur
}

// requireNoApprovalEntries asserts the registry never held an entry for this
// harness — not even one that resolved before inspection.
func (h *askTaskHarness) requireNoApprovalEntries(t *testing.T) {
	t.Helper()
	h.reg.mu.Lock()
	defer h.reg.mu.Unlock()
	// Empty, not Go-zero: the registry may keep an initialized (but empty)
	// entry map, which still registers nothing.
	require.Empty(t, h.reg.entries, "a task run must never register an approval entry, resolved or not (R1/R5)")
}

// assertNoApprovalFrame drains a connected tab's frame buffer and fails if a
// tool_approval_required frame is anywhere in it.
func assertNoApprovalFrame(t *testing.T, wc *wsConn, tab int) {
	t.Helper()
	for {
		select {
		case raw := <-wc.sendCh:
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			require.NotEqual(t, "tool_approval_required", frame["type"],
				"tab %d received an approval frame for a task run (R1/R5): %s", tab, raw)
		default:
			return
		}
	}
}

// Given a task whose worker calls an "ask" tool and then an allow-ceiling
// control tool in the same run, with no browser connected
// When the run executes
// Then no approval is ever registered, the probe is refused with the
// amendment's fixed reason and never executes, the control call still runs
// (the run continues past the refusal), and the blocked claim ends the task
// Failed.
func TestTaskRun_AskTool_NeverAsks_RefusedWithVisibleReason_RunContinues(t *testing.T) {
	h := newAskTaskHarness(t)
	writeTaskControlFile(t, h)
	created := h.startBuilderTask(t)

	final := h.awaitTerminalRefused(t, created.Id)
	h.requireNoApprovalEntries(t)
	assert.Equal(t, 0, h.tool.runs(), "the refused ask tool never runs")

	_, results := h.worker.snapshot()
	require.GreaterOrEqual(t, len(results), 2, "the worker was handed the probe refusal and the control result")
	var denial map[string]any
	require.NoError(t, json.Unmarshal([]byte(results[0]), &denial), "the refusal is a structured result: %s", results[0])
	assert.Equal(t, map[string]any{
		"error": "permission_denied", "tool": askProbeToolName,
		"reason": taskPlanDenialReason, "permanent": true, "message": taskPlanDenialModelText,
	}, denial, "R4: the fixed visible refusal, permanent, no retry in the turn")
	assert.Contains(t, results[1], taskControlFileText,
		"the allow-ceiling control call in the SAME run executed (positive control)")
	require.NotNil(t, final.Result)
	assert.Contains(t, *final.Result, "refused", "the task's blocked-claim evidence names the refusal")
	assert.Contains(t, *final.Result, taskControlFileText, "the task's blocked-claim evidence names the control result")
	assert.Equal(t, gen.TaskStatusFailed, final.Status, "the blocked claim ends the task Failed (Q2 A), not pending approval")
}

// Given a task whose worker calls an "ask" tool while a browser tab is
// connected
// When the run executes
// Then the tab never receives a tool_approval_required frame, the registry
// never holds an entry, the transcript keeps one settled "denied" row with
// the amendment's "Not run:" text, and the refusal classifies as the
// amendment's permanent task/plan denial.
func TestTaskRun_AskTool_NeverBroadcasts_FrameAndRowPinTheRefusal(t *testing.T) {
	h := newAskTaskHarness(t)
	tabs := apprResAttachConns(t, h.ws, 1)
	created := h.startBuilderTask(t)

	final := h.awaitTerminalRefused(t, created.Id)

	h.requireNoApprovalEntries(t)
	for i, wc := range tabs {
		assertNoApprovalFrame(t, wc, i)
	}

	sessionID := getTaskSessionID(t, h.api, created.Id)
	rows, err := h.api.agentLoop.GetSessionStore().ReadTranscript(sessionID)
	require.NoError(t, err)
	var probeRows []session.ToolCall
	for _, entry := range rows {
		for _, call := range entry.ToolCalls {
			if string(call.ID) == "call-report" {
				probeRows = append(probeRows, call)
			}
		}
	}
	require.Len(t, probeRows, 1, "one settled visible row for the refused call, not missing and not pending")
	assert.Equal(t, "denied", probeRows[0].Status)
	assert.Equal(t, askProbeToolName, probeRows[0].Tool)
	assert.Equal(t, map[string]any{
		"error": true, "text": taskPlanDenialTranscript, "reason": taskPlanDenialReason, "permanent": true,
	}, probeRows[0].Result, "R4: the transcript row carries the amendment's fixed text")

	cls, known := agent.ClassifyDenial(taskPlanDenialReason)
	require.True(t, known, "R4 requires a dedicated denial-table row, not unknown-reason fallback")
	assert.Equal(t, agent.DenialClass{
		Reason: taskPlanDenialReason, Permanent: true,
		ModelMessage: taskPlanDenialModelText, TranscriptText: taskPlanDenialTranscript,
	}, cls)
	assert.Equal(t, gen.TaskStatusFailed, final.Status)
}
