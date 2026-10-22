package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func (ts *turnState) fileEffectExecutionContext(ctx context.Context, callID string) context.Context {
	issuer, known := ts.callIssuer(callID)
	if !known || ts.opts.NoHistory {
		return ctx
	}
	id := ts.transcriptSessionID
	if id == "" {
		id = ts.sessionKey
	}
	var execution session.ExecutionIdentity
	if d := ts.opts.executionDisposition; d != nil {
		execution = session.ExecutionIdentity{RunID: d.claim.RunID, BootSeq: d.claim.BootSeq}
	}
	return tools.WithFileEffectContext(ctx, tools.FileEffectContext{
		Home: omnipusHome(), SessionID: id, WorkspaceID: tools.ToolWorkspaceID(ctx),
		ToolCallID: callID, Assistant: issuer, Execution: execution,
	})
}
