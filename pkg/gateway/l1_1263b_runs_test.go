package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The 2026-09-15 attended-task decision in task_run_ask_approval_test.go is
// superseded by founder 2026-10-13, L1-AMENDMENTS §1.2 R1/R4/R5. These tests
// deliberately do NOT pre-stamp WithAutoDenyAsk or manufacture task sessions.
func TestL11263B_TaskStartNeverPrompts(t *testing.T) {
	worker := l11263Script(l11263Write(), l11263Claim())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
	created := h.createTask(t, "")
	h.startBoard(t, created)
	final := h.awaitTask(t, created.Id)
	h.requireTaskOrigin(t, final.SessionID)
	h.requireRefusal(t, final.SessionID, worker)
	require.Equal(t, "failed", string(final.Status), "a blocked claim ends the task Failed, not pending approval")
	require.Equal(t, "Blocked: write_file approval probe finished", final.Result)
}

func TestL11263B_CalendarRunNowNeverPrompts(t *testing.T) {
	worker := l11263Script(l11263Write(), l11263Claim())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
	created := h.createTask(t, "")
	h.startCalendar(t, created.Id)
	final := h.awaitTask(t, created.Id)
	h.requireTaskOrigin(t, final.SessionID)
	h.requireRefusal(t, final.SessionID, worker)
}

func toolsContextL11263(ws string) context.Context {
	return tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "mia"), ws)
}

func TestL11263B_ChatRunTaskNeverPrompts(t *testing.T) {
	worker := l11263Script(l11263Write(), l11263Claim())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
	created := h.createTask(t, "")
	caller := l11263Script(l11263Call("start-task", "run_task", map[string]any{"task_id": created.Id}))
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Provider = caller
	_, err := h.api.agentLoop.ProcessDirectAs(context.Background(), "mia", "Run the task", "l11263-chat-run-task")
	require.NoError(t, err)
	result, gotResult := caller.result("start-task")
	require.True(t, gotResult, "interactive caller must really execute run_task")
	var receipt map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &receipt), "%s", result)
	require.NotEmpty(t, receipt["session_id"], "run_task must dispatch, not merely return a policy refusal")
	final := h.awaitTask(t, created.Id)
	h.requireTaskOrigin(t, final.SessionID)
	h.requireRefusal(t, final.SessionID, worker)
}

func TestL11263B_PlanRunNeverPrompts(t *testing.T) {
	worker := l11263Script(l11263Write(), l11263Claim())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
	member := h.startPlan(t)
	final := h.awaitTask(t, member.Id)
	h.requireTaskOrigin(t, final.SessionID)
	h.requireRefusal(t, final.SessionID, worker)
}

// R4: Auto is evaluated before unattended refusal. Real write_file must write
// the exact fixture bytes; a fake provider saying "done" cannot satisfy this.
func TestL11263B_AutoApprovedTaskAndPlanRun(t *testing.T) {
	for _, route := range []string{"board", "plan"} {
		t.Run(route, func(t *testing.T) {
			worker := l11263Script(l11263Write(), l11263Claim())
			h := newL11263Harness(t, true, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
			created := h.startRoute(t, route)
			final := h.awaitTask(t, created)
			h.requireTaskOrigin(t, final.SessionID)
			data, err := os.ReadFile(filepath.Join(h.workDir, l11263File))
			require.NoError(t, err, "Auto-eligible Ask call must actually execute")
			require.Equal(t, l11263Content, string(data))
			result, ok := worker.result("approval-write")
			require.True(t, ok)
			require.Equal(t, "File written: "+l11263File, result)
		})
	}
}

func TestL11263B_DenyPolicyStaysDeny(t *testing.T) {
	for _, route := range []string{"board", "plan"} {
		t.Run(route, func(t *testing.T) {
			worker := l11263Script(l11263Write(), l11263Claim())
			h := newL11263Harness(t, true, config.ToolPolicyDeny, map[string]*l11263Provider{l11263Agent: worker})
			id := h.startRoute(t, route)
			h.awaitTask(t, id)
			require.NoFileExists(t, filepath.Join(h.workDir, l11263File), "Auto never overrides an explicit deny")
			result, ok := worker.result("approval-write")
			require.True(t, ok)
			var denial map[string]any
			require.NoError(t, json.Unmarshal([]byte(result), &denial), "%s", result)
			require.Equal(t, "permission_denied", denial["error"])
			require.Equal(t, "policy_denied", denial["reason"])
			require.Equal(t, true, denial["permanent"])
		})
	}
}

func (h *l11263Harness) startRoute(t *testing.T, route string) string {
	t.Helper()
	if route == "plan" {
		return h.startPlan(t).Id
	}
	created := h.createTask(t, "")
	h.startBoard(t, created)
	return created.Id
}

func (h *l11263Harness) requireRefusal(t *testing.T, actingSession string, worker *l11263Provider) {
	t.Helper()
	result, ok := worker.result("approval-write")
	require.True(t, ok, "worker must receive an immediate tool result, never remain parked on Ask")
	var denial map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &denial), "refusal must be a structured tool result: %s", result)
	assert.Equal(t, map[string]any{
		"error": "permission_denied", "tool": "write_file", "reason": l11263Reason,
		"permanent": true, "message": l11263Model,
	}, denial, "R4 model-facing refusal is exact and permanently denied")
	cls, known := agent.ClassifyDenial(l11263Reason)
	assert.True(t, known, "R4 requires a dedicated denial-table row, not unknown-reason fallback")
	assert.Equal(t, agent.DenialClass{Reason: l11263Reason, Permanent: true, ModelMessage: l11263Model, TranscriptText: l11263Transcript}, cls)
	require.NoFileExists(t, filepath.Join(h.workDir, l11263File), "refused call must never execute")
	entries, err := h.api.agentLoop.GetSessionStore().ReadTranscript(actingSession)
	require.NoError(t, err)
	var calls []session.ToolCall
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if string(call.ID) == "approval-write" {
				calls = append(calls, call)
			}
		}
	}
	require.Len(t, calls, 1, "one settled visible row, not missing or still pending")
	assert.Equal(t, "denied", calls[0].Status)
	assert.Equal(t, "write_file", calls[0].Tool)
	assert.Equal(t, map[string]any{"error": true, "text": l11263Transcript, "reason": l11263Reason, "permanent": true}, calls[0].Result)
}

// Uses a real plan run so the pre-change no-prompt dispatch is not enough:
// its cron-flavoured refusal/audit must become the task/plan-specific R4 one.
func TestL11263B_RefusalIsVisibleAndAudited(t *testing.T) {
	worker := l11263Script(l11263Write(), l11263Claim())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
	created := h.startPlan(t)
	final := h.awaitTask(t, created.Id)
	h.requireRefusal(t, final.SessionID, worker)
	f, err := os.Open(filepath.Join(h.api.homePath, "system", "audit.jsonl"))
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	attempted, askDenied := 0, 0
	for scan.Scan() {
		var rec map[string]any
		require.NoError(t, json.Unmarshal(scan.Bytes(), &rec))
		switch rec["event"] {
		case "tool.policy.deny.attempted":
			details, ok := rec["details"].(map[string]any)
			require.True(t, ok)
			if rec["tool"] == "write_file" {
				attempted++
				assert.Equal(t, l11263Reason, details["context"])
			}
		case "tool.policy.ask.denied":
			fields, ok := rec["fields"].(map[string]any)
			require.True(t, ok)
			if fields["tool_name"] == "write_file" {
				askDenied++
				// R4 deliberately prescribes no serialized spelling for the
				// new audit reason constant. Its distinction is what is binding.
				reason, ok := fields["reason"].(string)
				require.True(t, ok)
				assert.True(t, audit.IsValidAskDenyReason(audit.AskDenyReason(reason)), "R4 requires a real new audit reason constant")
				assert.NotContains(t, []string{"scheduled", "user", "timeout", "cancel", "restart", "saturated", "batch_short_circuit"}, reason, "task/plan refusal must not reuse another denial class")
				assert.Empty(t, fields["approval_id"])
				assert.Empty(t, fields["approver_user_id"])
			}
		}
	}
	require.NoError(t, scan.Err())
	assert.Equal(t, 1, attempted, "one real attempted-policy-denial audit")
	assert.Equal(t, 1, askDenied, "one real task/plan Ask-denied audit")
}

func TestL11263B_InteractiveChatStillPrompts(t *testing.T) {
	worker := l11263Script(l11263Write())
	h := newL11263Harness(t, false, config.ToolPolicyAsk, map[string]*l11263Provider{l11263Agent: worker})
	done := make(chan error, 1)
	go func() {
		_, err := h.api.agentLoop.ProcessDirectAs(context.Background(), l11263Agent, "Write the file", "l11263-interactive")
		done <- err
	}()
	t.Cleanup(func() {
		for _, e := range h.reg.pendingApprovals() {
			h.reg.resolve(e.ApprovalID, ApprovalActionCancel, false)
		}
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("interactive turn cleanup did not finish")
		}
	})
	entry := h.awaitInteractiveApproval(t)
	require.Equal(t, l11263Agent, entry.AgentID)
	require.Equal(t, "write_file", entry.ToolName)
	require.Equal(t, map[string]any{"path": l11263File, "content": l11263Content}, entry.Args)
	require.NoFileExists(t, filepath.Join(h.workDir, l11263File), "interactive Ask must not execute before approval")
	select {
	case err := <-done:
		t.Fatalf("interactive turn ended before explicit approval: %v", err)
	default:
	}
	frame := apprResReadFrame(t, h.tab, "tool_approval_required")
	require.Equal(t, entry.ApprovalID, frame["approval_id"], "positive control proves real WS observer sees approvals")
	resolved, gone := h.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone)
	select {
	case err := <-done:
		require.NoError(t, err)
		// Leave the completion signal available to the cleanup join as well.
		done <- nil
	case <-time.After(15 * time.Second):
		t.Fatal("interactive turn failed to continue after real approval")
	}
	data, err := os.ReadFile(filepath.Join(h.workDir, l11263File))
	require.NoError(t, err)
	require.Equal(t, l11263Content, string(data))
	require.Empty(t, h.reg.pendingApprovals())
}

func (h *l11263Harness) awaitInteractiveApproval(t *testing.T) *approvalEntry {
	t.Helper()
	var entry *approvalEntry
	require.Eventually(t, func() bool {
		pending := h.reg.pendingApprovals()
		if len(pending) == 1 {
			entry = pending[0]
			return true
		}
		return false
	}, 15*time.Second, 10*time.Millisecond, "interactive control must raise exactly one real approval")
	return entry
}
