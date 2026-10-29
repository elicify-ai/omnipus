package agent

import (
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// emitAdmittedToolResult runs only after the per-result checkpoint succeeds.
func (ex *agentLoopRunTurnToolsExecute) emitAdmittedToolResult() {
	endSID := u9ToolExecSessionIDs(ex.rx.rr.rq.ri.rf.rt.ts)
	// chat-links D-seam: the open request rides the payload only on success.
	var openTarget *tools.LibraryOpenTarget
	if !ex.toolResult.IsError {
		openTarget = ex.toolResult.OpenInLibrary
	}
	ex.rx.rr.rq.ri.rf.rt.al.emitEvent(
		EventKindToolExecEnd,
		ex.rx.rr.rq.ri.rf.rt.ts.eventMeta("runTurn", "turn.tool.end"),
		ToolExecEndPayload{
			ToolCallID: session.ToolCallID(ex.toolCallID),
			ChatID:     ex.rx.rr.rq.ri.rf.rt.ts.chatID,
			// ADR-057 FR-011/FR-012 (W4/W5d, U9): see the matching
			// ToolExecStartPayload construction above — identical
			// contract on the result frame.
			SessionID:         endSID,
			Tool:              ex.toolName,
			Duration:          ex.toolDuration,
			ForLLMLen:         len(ex.contentForLLM),
			ForUserLen:        len(ex.toolResult.ForUser),
			IsError:           ex.toolResult.IsError,
			Async:             ex.toolResult.Async,
			Result:            ex.contentForLLM,
			ParentSpawnCallID: session.ToolCallID(ex.rx.rr.rq.ri.rf.rt.ts.parentSpawnCallID),
			AgentID:           ex.rx.rr.rq.ri.rf.rt.ts.resolveActiveAgentID(), // Bug 1: runtime-current agent
			OpenInLibrary:     openTarget,
		},
	)
}
