package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deterministic EXTERNAL PROCESS ADAPTER edge. The real registry validates,
// finds and invokes it; the real loop archives/settles it. This is not a fake
// keeper predicate, manufactured transcript row or invocation-fact setter.
// Its operation counter is an independent instrument, never a progress input.
type grProcessOperationEdge struct {
	tools.BaseTool
	mode    string
	invoked atomic.Int64
}

func (*grProcessOperationEdge) Name() string { return "r6_process_operation_edge" }
func (*grProcessOperationEdge) Description() string {
	return "deterministic external operation edge for R6 invocation truth"
}
func (*grProcessOperationEdge) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*grProcessOperationEdge) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"payload": map[string]any{"type": "string"}}, "required": []string{"payload"}}
}
func (p *grProcessOperationEdge) Execute(context.Context, map[string]any) *tools.ToolResult {
	if p.mode == "deferred" {
		return &tools.ToolResult{ForLLM: "external operation gate is held", Deferred: &tools.ToolDeferral{Gate: "browser_control", Reason: "fixture external operation was not invoked"}}
	}
	p.invoked.Add(1)
	if p.mode == "panic" {
		panic("fixture external operation panicked AFTER invocation")
	}
	if p.mode == "error" {
		return tools.ErrorResult("fixture external operation failed AFTER invocation")
	}
	return tools.NewToolResult("fixture external operation executed")
}

type grProgressAfterTool struct {
	target, rename   string
	replace          bool
	seen             atomic.Int64
	originalWasError atomic.Bool
}

func (*grProgressAfterTool) BeforeTool(_ context.Context, call *ToolCallHookRequest) (*ToolCallHookRequest, HookDecision, error) {
	return call, HookDecision{Action: HookActionContinue}, nil
}
func (h *grProgressAfterTool) AfterTool(_ context.Context, result *ToolResultHookResponse) (*ToolResultHookResponse, HookDecision, error) {
	if result.Tool != h.target {
		return result, HookDecision{Action: HookActionContinue}, nil
	}
	h.seen.Add(1)
	h.originalWasError.Store(result.Result != nil && result.Result.IsError)
	if h.rename != "" {
		result.Tool = h.rename
	}
	if h.replace {
		result.Result = tools.NewToolResult("hook synthesized a successful-looking result")
	}
	return result, HookDecision{Action: HookActionModify}, nil
}

func grAttemptTool(t *testing.T, h *grHarness, name string, args map[string]any, wantArchivedName, wantStatus string) {
	t.Helper()
	msg := h.reminder(t)
	grSetStreak(t, h, 1)
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	p := &grActionProvider{tool: name, args: args, callID: "r6-invocation-truth", final: "This real work round has returned."}
	inst.Provider = p
	_, err := h.al.processSystemMessage(h.ctx, msg)
	inst.Provider = h.root
	require.NoError(t, err)
	require.Equal(t, 2, p.calls, "real tool-result handling must precede the final ordinary response")
	entries, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	var actual []session.ToolCall
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if string(call.ID) == p.callID {
				actual = append(actual, call)
			}
		}
	}
	require.Len(t, actual, 1)
	assert.Equal(t, wantArchivedName, actual[0].Tool, "positively observe the actual hook/registry result, not just hook invocation")
	assert.Equal(t, wantStatus, actual[0].Status)
}

func TestGoalReminderToolInvocationTruth(t *testing.T) {
	for _, tc := range []struct {
		name, mode, tool, rename, status string
		args                             map[string]any
		replace, progress                bool
		operations                       int64
	}{
		{name: "RegistryLookupFailureDoesNotReset", tool: "r6_no_registered_operation", args: map[string]any{}, status: "error"},
		{name: "RegistryValidationFailureDoesNotReset", mode: "success", args: map[string]any{}, status: "error"},
		{name: "DeferredExternalOperationWasNotInvoked", mode: "deferred", args: map[string]any{"payload": "work"}, status: "success"},
		{name: "DeferredCannotBecomeProgressThroughAfterToolSuccess", mode: "deferred", args: map[string]any{"payload": "work"}, replace: true, status: "success"},
		{name: "ExecutedErrorStillCountsAsExecutedWork", mode: "error", args: map[string]any{"payload": "work"}, status: "error", progress: true, operations: 1},
		{name: "ExecutedPanicStillCountsAsExecutedWork", mode: "panic", args: map[string]any{"payload": "work"}, status: "error", progress: true, operations: 1},
		{name: "ExecutedErrorKeepsTruthAfterExcludedRenameAndSuccess", mode: "error", args: map[string]any{"payload": "work"}, rename: "set_todos", replace: true, status: "success", progress: true, operations: 1},
		{name: "SetTodosCannotBecomeWorkThroughAfterToolRename", tool: "set_todos", args: map[string]any{"outcome": "Only bookkeeping", "todos": []any{map[string]any{"text": "Do actual work later", "status": "pending"}}}, rename: "read_file", replace: true, status: "success"},
		{name: "GoalClaimCannotBecomeWorkThroughAfterToolRename", tool: "goal_claim", args: map[string]any{"status": "met", "evidence": "Only an unsupported claim, no executed work."}, rename: "read_file", replace: true, status: "success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := grNewRecordedBrake(t)
			probe := &grProcessOperationEdge{mode: tc.mode}
			inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
			require.True(t, ok)
			inst.Tools.RegisterReplacing(probe)
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{probe.Name(): config.ToolPolicyAllow, "set_todos": config.ToolPolicyAllow, "goal_claim": config.ToolPolicyAllow}})
			name := tc.tool
			if name == "" {
				name = probe.Name()
			}
			archivedName := name
			if tc.rename != "" {
				archivedName = tc.rename
			}
			var hook *grProgressAfterTool
			if tc.replace || tc.rename != "" {
				hook = &grProgressAfterTool{target: name, rename: tc.rename, replace: tc.replace}
				require.NoError(t, h.al.MountHook(NamedHook("r6-progress-after-tool", hook)))
			}
			grAttemptTool(t, h, name, tc.args, archivedName, tc.status)
			assert.Equal(t, tc.operations, probe.invoked.Load(), fmt.Sprintf("actual external operation entry count for %s", tc.name))
			if hook != nil {
				require.Equal(t, int64(1), hook.seen.Load(), "the real AfterTool hook must actually run")
			}
			h.age(t)
			next := h.reminder(t)
			want := 2 // one prior assessed failure + this actual completed unproductive outcome
			if tc.progress {
				want = 0
			}
			assert.Equal(t, want, goalRecordForSession(t, h.id).ZeroOutputPushes, "invocation truth and exclusions use actual pre-hook tool identity/result, never returned status/name alone")
			grTextReminder(t, h, next)
		})
	}
}
