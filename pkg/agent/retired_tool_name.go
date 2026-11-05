// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// retiredToolNameReason is the audit reason of a call to a retired tool name
// (tool-parity r17 §7.12.3 step 2).
const retiredToolNameReason = "retired_tool_name"

// refuseRetiredToolName answers a call to a tool name retired by a rename. A
// retired name is never in the filter-time policy map, so the call stops at
// the execution-policy check; instead of the generic policy_denied payload it
// gets the renamed-tool text as its refused result, and the audit row carries
// the reason retired_tool_name. Nothing is granted, executed or aliased, and
// it is not a policy denial: the denial ledger and quarantine are untouched.
func (ex *agentLoopRunTurnToolsExecute) refuseRetiredToolName(tc providers.ToolCall, canonical string) agentLoopRunTurnToolsExecuteFlow {
	rt := ex.rx.rr.rq.ri.rf.rt
	rt.al.emitPolicyDenyAudit(rt.ts, ex.toolName, "deny", retiredToolNameReason,
		map[string]any{"reason": retiredToolNameReason, "canonical": canonical})
	if err := ex.admitAndCheckpoint(toolResultAdmission{
		Tool: tc.Name, ToolCallID: tc.ID, Content: tools.RetiredToolMessage(ex.toolName, canonical),
		IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
	}); err != nil {
		return ex.contextWindowExit(err)
	}
	rt.al.emitEvent(
		EventKindToolExecSkipped,
		rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
		ToolExecSkippedPayload{Tool: ex.toolName, Reason: retiredToolNameReason},
	)
	return agentLoopRunTurnToolsExecuteContinue
}
