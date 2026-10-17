// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// valueFreeRefusedToolCall retains call identity but removes the entire
// rejected argument map before assistant/model-window persistence. Refusal is
// carried privately by the turn, never by a forgeable argument marker.
func valueFreeRefusedToolCall(registry *tools.ToolRegistry, call providers.ToolCall) (providers.ToolCall, *tools.ToolResult) {
	tool, exists := registry.Get(registry.UnsanitizeToolName(call.Name))
	if !exists {
		return call, nil
	}
	refusal := tools.ToolArgumentRefusal(tool, call.Arguments)
	if refusal == nil {
		return call, nil
	}
	call.Arguments = map[string]any{}
	if call.Function != nil {
		function := *call.Function
		function.Arguments = "{}"
		call.Function = &function
	}
	return call, refusal
}

// refusePlaintextToolCall uses the normal synthetic-result admission path before
// hooks, approval, preview or ToolExecStart. It does not execute the tool or
// change policy. Other tools are untouched by the opt-in interface.
func (ex *agentLoopRunTurnToolsExecute) refusePlaintextToolCall(tc providers.ToolCall) agentLoopRunTurnToolsExecuteFlow {
	tool, exists := ex.rx.rr.rq.ri.rf.rt.ts.agent.Tools.Get(ex.toolName)
	if !exists {
		return agentLoopRunTurnToolsExecuteNext
	}
	return ex.admitPlaintextRefusal(tc, tools.ToolArgumentRefusal(tool, ex.toolArgs))
}

func (ex *agentLoopRunTurnToolsExecute) admitPlaintextRefusal(tc providers.ToolCall, refusal *tools.ToolResult) agentLoopRunTurnToolsExecuteFlow {
	if refusal == nil {
		return agentLoopRunTurnToolsExecuteNext
	}
	if err := ex.admitAndCheckpoint(toolResultAdmission{
		Tool: tc.Name, ToolCallID: tc.ID, Content: refusal.ForLLM, IsError: true,
		ParallelN: len(ex.rx.rr.normalizedToolCalls),
	}); err != nil {
		return ex.contextWindowExit(err)
	}
	ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
		EventKindToolExecSkipped,
		ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
		ToolExecSkippedPayload{Tool: ex.toolName, Reason: "plaintext_secret_input"},
	)
	return agentLoopRunTurnToolsExecuteContinue
}
