// loop_pre_approval.go: checks and card facts before an ask-policy approval card

package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// prepareApprovalRequest runs the tool's own pre-card step for one ask-policy
// call and returns the arguments the approval request shows.
//
// A tool implementing tools.ApprovalPreparer (Channels Q2-B:
// set_channel_outbound) runs every validity check, reads the card facts and
// returns its pin in one call; the facts are added to the request arguments
// only and the pin is kept for guardAndDispatch (tools.WithApprovalPin). Any
// other tool keeps the two older hooks: tools.PreApprovalChecker
// (channels-spec-r8 FD-11) and tools.ApprovalContextProvider (e.g.
// remove_secret's consumers, FR-077).
//
// refused reports a refusal before any card; it is then the call's final
// result (ex.preApprovalRefusal), recorded by guardAndDispatch, and the tool
// is not executed. ex.toolArgs is never changed.
func (ex *agentLoopRunTurnToolsExecute) prepareApprovalRequest(tc providers.ToolCall, requestArgs map[string]any) (map[string]any, bool) {
	rt := ex.rx.rr.rq.ri.rf.rt
	if rt.ts.agent == nil || rt.ts.agent.Tools == nil {
		return requestArgs, false
	}
	tool, ok := rt.ts.agent.Tools.Get(ex.toolName)
	if !ok {
		return requestArgs, false
	}
	if preparer, ok := tool.(tools.ApprovalPreparer); ok {
		// The preparer audits its own refusals; give it the call's
		// correlation (tool call and turn) the execution context carries.
		prepCtx := tools.WithToolCallID(rt.turnCtx, tc.ID)
		prepCtx = tools.WithTurnID(prepCtx, rt.ts.turnID)
		facts, pin, refusal := preparer.PrepareForApproval(prepCtx, ex.toolArgs)
		if refusal != nil {
			ex.preApprovalRefusal = refusal
			return requestArgs, true
		}
		ex.approvalPin = pin
		return tools.WithApprovalFacts(requestArgs, facts), false
	}
	if refusal := tools.RefuseBeforeApproval(rt.turnCtx, tool, ex.toolArgs); refusal != nil {
		ex.preApprovalRefusal = refusal
		return requestArgs, true
	}
	return tools.WithApprovalContext(rt.turnCtx, tool, requestArgs), false
}

// executeOrPreRefused executes the call, or returns the tool's pre-approval
// refusal without executing it.
func (ex *agentLoopRunTurnToolsExecute) executeOrPreRefused(execCtx context.Context) *tools.ToolResult {
	if ex.preApprovalRefusal != nil {
		return ex.preApprovalRefusal
	}
	rt := ex.rx.rr.rq.ri.rf.rt
	return rt.ts.agent.Tools.ExecuteWithContext(execCtx, ex.toolName, ex.toolArgs, rt.ts.channel, rt.ts.chatID, ex.asyncCallback)
}
