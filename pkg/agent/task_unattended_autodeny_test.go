// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_unattended_autodeny_test.go — UAT defect D-08 (founder decision
// 2026-09-24, FR-057, ADR-092 D10): a task fired by its own Calendar trigger
// on a workspace Board must auto-deny an ask-policy tool call the exact same
// way a headless Schedules-feature run always did — never open a live
// approval card, even if an operator happens to be online at that moment.
//
// Before this fix, TaskTriggerScheduler.RunScheduled -> TaskExecutor.
// ExecuteTask -> processTaskDirect never set AutoDenyAsk on the dispatched
// turn's processOptions, so an always-ask tool (delete_task) opened a live
// approval card and stalled a headless run indefinitely if nobody happened to
// be watching, or silently reached a human "who happened to be online" if
// someone was — audit showed tool.policy.deny.attempted with context="user"
// inside a task-run session, never the headless auto-deny path.
//
// These tests drive the real dispatch primitives end to end (no test seam
// stands in for the AutoDenyAsk propagation itself):
//   - TestTaskTrigger_RunScheduled_StampsAutoDenyAsk proves the exact D-08
//     entry point (TaskTriggerScheduler.RunScheduled) now stamps the
//     headless marker onto the context it hands its dispatch function.
//   - TestProcessTaskDirect_UnattendedAskTool_AutoDeniedAtOnce_ZeroApproverCalls
//     proves an ask-policy tool call made from an unattended dispatch context
//     (the same context shape RunScheduled/CheckQueuedTasks/
//     advanceBlockedTasks/executeTaskPlanVerified now all stamp) is refused
//     at once: zero approver calls, the autoDenyHeadlessReason reason, and
//     both audit rows present with the corrected (non-"user") context.
//   - TestProcessTaskDirect_UnattendedAllowedTool_StillRuns proves the same
//     unattended context does not block a call Auto/an Allow policy would
//     run in a chat — only what needs a human is refused (FR-057 order).
//   - TestProcessDirect_ChatTurn_AskTool_AsksExactlyOnce is the attended
//     control: an ordinary chat turn (no AutoDenyAsk stamped) still produces
//     exactly one approver call for the same ask-policy tool.
package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingApproveApprover is a PolicyApprover that approves every request and
// counts exactly how many times it was consulted — a stricter instrument
// than autoApproveApprover's boolean "was it ever consulted", needed to
// prove "exactly one approver call" rather than merely "at least one".
type countingApproveApprover struct{ calls atomic.Int32 }

func (a *countingApproveApprover) RequestApproval(context.Context, PolicyApprovalReq) (bool, string, bool) {
	a.calls.Add(1)
	return true, "approved", false
}

// TestTaskTrigger_RunScheduled_StampsAutoDenyAsk drives a real due-job fire
// through TaskTriggerScheduler.RunScheduled (the exact D-08 entry point) and
// asserts the context handed to the dispatch function carries
// tools.ToolAutoDenyAsk == true. Unlike newTriggerSchedulerForTest's stock
// dispatchRecorder (which discards ctx), this test's own capturing dispatch
// function is the ONLY way to observe the stamp task_trigger.go's
// RunScheduled now applies before calling s.dispatch.
func TestTaskTrigger_RunScheduled_StampsAutoDenyAsk(t *testing.T) {
	sched, store, clk, _ := newTriggerSchedulerForTest(t)

	var capturedCtx context.Context
	var callCount int
	sched.dispatch = func(ctx context.Context, taskID string, occurrenceMs *int64) error {
		capturedCtx = ctx
		callCount++
		return nil
	}

	fireAtMs := clk.Now().Add(time.Minute).UnixMilli()
	tsk := makeTask(t, store, "agent-a", &task.Trigger{
		Type:   task.TriggerOnce,
		Config: task.TriggerConfig{AtMs: int64P(fireAtMs)},
	})
	sched.OnTaskUpserted(tsk)

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()

	require.Equal(t, 1, callCount, "expected exactly one dispatch call")
	require.NotNil(t, capturedCtx, "dispatch must have been called with a non-nil context")
	assert.True(t, tools.ToolAutoDenyAsk(capturedCtx),
		"D-08: a Calendar/task-trigger fire must stamp AutoDenyAsk=true onto the "+
			"context it hands its dispatch function — RunScheduled has no operator, ever")
}

// TestProcessTaskDirect_UnattendedAskTool_AutoDeniedAtOnce_ZeroApproverCalls
// is the fail-first regression for D-08 itself: an ask-policy tool call made
// from an unattended dispatch context must be refused before ever reaching
// the approver, with the headless reason and both audit rows.
//
// The context here (tools.WithAutoDenyAsk(ctx, true)) is exactly the shape
// every real unattended dispatcher now produces — TaskTriggerScheduler.
// RunScheduled, TaskExecutor.CheckQueuedTasks, TaskExecutor.
// advanceBlockedTasks, TaskExecutor.executeTaskPlanVerified, and
// PlanEngine.dispatchPlanTurn all funnel into processTaskDirect through it —
// so testing processTaskDirect's own behavior against that context shape
// directly proves what every one of those callers relies on.
func TestProcessTaskDirect_UnattendedAskTool_AutoDeniedAtOnce_ZeroApproverCalls(t *testing.T) {
	al, home, auditPath := schedTestLoopWithAudit(t)

	prov := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithText("could not use that tool, proceeding without it")
	mia := registerAgent(t, al, home, "mia", prov, false)

	stub := &dangerousStubTool{}
	mia.Tools.Register(stub)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"dangerous_tool": "ask"},
	})

	approver := &countingApproveApprover{}
	al.SetToolApprover(approver)

	ctx := tools.WithAutoDenyAsk(context.Background(), true)
	reply, err := al.processTaskDirect(ctx, "mia", "please use dangerous_tool", "task-sess-d08-1", "task:d08-1")
	require.NoError(t, err, "auto-deny is a loop-level action, not an agent error")
	assert.Equal(t, "could not use that tool, proceeding without it", reply)

	assert.False(t, stub.wasCalled.Load(),
		"ask-policy tool must be auto-denied — Execute must never run in an unattended task dispatch")
	assert.Equal(t, int32(0), approver.calls.Load(),
		"D-08: zero approver calls — the run must never open a live approval card")

	require.FileExists(t, auditPath, "audit.jsonl must exist")
	records, err := readScheduledAuditRecords(auditPath)
	require.NoError(t, err)
	require.NotEmpty(t, records)

	var askDenied *map[string]any
	for i, rec := range records {
		if rec["event"] != audit.EventToolPolicyAskDenied {
			continue
		}
		fields, ok := rec["fields"].(map[string]any)
		if ok && fields["reason"] == string(audit.AskDenyReasonScheduled) && fields["tool_name"] == "dangerous_tool" {
			askDenied = &records[i]
			break
		}
	}
	require.NotNil(t, askDenied,
		"audit.jsonl must contain a %q record for the auto-denied tool; all records: %v",
		audit.EventToolPolicyAskDenied, records)

	var denyAttempted *map[string]any
	for i, rec := range records {
		if rec["event"] != audit.EventToolPolicyDenyAttempted {
			continue
		}
		details, ok := rec["details"].(map[string]any)
		if ok && details["context"] == autoDenyHeadlessReason {
			denyAttempted = &records[i]
			break
		}
	}
	require.NotNil(t, denyAttempted,
		"D-08: audit.jsonl must contain a %q record whose details.context is the "+
			"headless auto-deny reason, not the interactive-approval \"user\" context "+
			"the defect showed; all records: %v",
		audit.EventToolPolicyDenyAttempted, records)
	denyDetails, ok := (*denyAttempted)["details"].(map[string]any)
	require.True(t, ok)
	assert.NotEqual(t, "user", denyDetails["context"],
		"D-08 regression: the deny.attempted context must never be \"user\" for an unattended dispatch")
}

// TestProcessTaskDirect_UnattendedAllowedTool_StillRuns proves the FR-057
// ordering the founder's brief calls out explicitly: "a call that Auto would
// run in a chat also runs unattended; only what needs a human is refused."
// An Allow-policy tool call in the SAME unattended context as the test above
// must still execute — AutoDenyAsk only ever gates the ask branch.
func TestProcessTaskDirect_UnattendedAllowedTool_StillRuns(t *testing.T) {
	al, home := schedTestLoop(t)

	prov := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithText("wrote the report")
	mia := registerAgent(t, al, home, "mia", prov, false)

	stub := &dangerousStubTool{}
	mia.Tools.Register(stub)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"dangerous_tool": "allow"},
	})

	ctx := tools.WithAutoDenyAsk(context.Background(), true)
	reply, err := al.processTaskDirect(ctx, "mia", "please use dangerous_tool", "task-sess-d08-2", "task:d08-2")
	require.NoError(t, err)
	assert.Equal(t, "wrote the report", reply)
	assert.True(t, stub.wasCalled.Load(),
		"an Allow-policy tool must still run in an unattended dispatch — AutoDenyAsk "+
			"must never block a call that needs no human")
}

// TestProcessDirect_ChatTurn_AskTool_AsksExactlyOnce is the attended control:
// an ordinary chat turn (ProcessDirect, no AutoDenyAsk ever stamped on this
// path) calling an ask-policy tool must still produce exactly one approver
// call — proving the D-08 fix did not regress the interactive path any of
// the unattended dispatchers must NOT touch.
func TestProcessDirect_ChatTurn_AskTool_AsksExactlyOnce(t *testing.T) {
	cfg, _ := baseLoopDenialTestConfig(t)

	prov := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithText("done")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	defer al.Close()

	stub := &dangerousStubTool{}
	al.RegisterTool(stub)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "expected a default agent from the single-entry Agents.List")
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"dangerous_tool": "ask"},
	})

	approver := &countingApproveApprover{}
	al.SetToolApprover(approver)

	reply, err := al.ProcessDirect(context.Background(), "please use dangerous_tool", "chat-turn-d08-session")
	require.NoError(t, err)
	assert.Equal(t, "done", reply)
	assert.Equal(t, int32(1), approver.calls.Load(),
		"a chat turn a person actually started must still show exactly one approval card")
	assert.True(t, stub.wasCalled.Load(), "the approved tool call must execute")
}
