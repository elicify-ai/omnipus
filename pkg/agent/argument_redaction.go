// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// admittedToolCall is valueFreeRefusedToolCall plus the r17 SR-3 redaction
// step, run once per call in recordToolCalls. It returns the execution call
// (real arguments, kept only for execution), the record copy every record,
// log, event and frame carries, and a refusal. A redaction failure refuses
// the call before anything is written (fail closed): the arguments are
// dropped from the call exactly like a value-free refusal.
func admittedToolCall(registry *tools.ToolRegistry, call providers.ToolCall) (providers.ToolCall, map[string]any, *tools.ToolResult) {
	call, refusal := valueFreeRefusedToolCall(registry, call)
	if refusal != nil {
		return call, call.Arguments, refusal
	}
	tool, exists := registry.Get(registry.UnsanitizeToolName(call.Name))
	if !exists {
		return call, call.Arguments, nil
	}
	recorded, err := tools.RecordArguments(tool, call.Arguments)
	if err != nil {
		return withoutArguments(call), map[string]any{}, tools.RedactionRefusal(tool, err)
	}
	return call, recorded, nil
}

// withoutArguments keeps call identity and removes every argument value.
func withoutArguments(call providers.ToolCall) providers.ToolCall {
	call.Arguments = map[string]any{}
	if call.Function != nil {
		function := *call.Function
		function.Arguments = "{}"
		call.Function = &function
	}
	return call
}

// recordArgsAfterHook re-derives the record copy when a BeforeTool hook
// replaced the execution arguments, with the same tool step and the same
// fail-closed refusal. Tools without the step record the arguments as given.
func (ex *agentLoopRunTurnToolsExecute) recordArgsAfterHook() *tools.ToolResult {
	ex.recordArgs = ex.toolArgs
	tool, exists := ex.rx.rr.rq.ri.rf.rt.ts.agent.Tools.Get(ex.toolName)
	if !exists {
		return nil
	}
	recorded, err := tools.RecordArguments(tool, ex.toolArgs)
	if err != nil {
		ex.recordArgs = map[string]any{}
		return tools.RedactionRefusal(tool, err)
	}
	ex.recordArgs = recorded
	return nil
}
