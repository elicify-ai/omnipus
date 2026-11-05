// loop_pre_approval.go: refusing an ask-policy call before any approval card

package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// refuseBeforeApproval asks the tool, when it implements
// tools.PreApprovalChecker, whether this call can run at all before a person
// is asked to approve it (channels-spec-r8 FD-11 and founder Q4: an
// own-connection set_channel_outbound request is refused with no card). It
// reports whether the tool refused; the refusal is then recorded by
// guardAndDispatch as the call's result, and the tool is not executed.
func (ex *agentLoopRunTurnToolsExecute) refuseBeforeApproval() bool {
	rt := ex.rx.rr.rq.ri.rf.rt
	if rt.ts.agent == nil || rt.ts.agent.Tools == nil {
		return false
	}
	tool, ok := rt.ts.agent.Tools.Get(ex.toolName)
	if !ok {
		return false
	}
	refusal := tools.RefuseBeforeApproval(rt.turnCtx, tool, ex.toolArgs)
	if refusal == nil {
		return false
	}
	ex.preApprovalRefusal = refusal
	return true
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
