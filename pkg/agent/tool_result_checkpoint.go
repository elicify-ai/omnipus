package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// admitAndCheckpoint is shared by refusals and skips. A failed append or
// checkpoint never installs the staged result in the ongoing request slice.
func (ex *agentLoopRunTurnToolsExecute) admitAndCheckpoint(adm toolResultAdmission) error {
	rt := ex.rx.rr.rq.ri.rf.rt
	admitted := rt.al.admitToolResult(rt.ts, adm)
	if admitted.Err != nil {
		return admitted.Err
	}
	candidate := append(append([]providers.Message(nil), ex.rx.rr.rq.ri.messages...), admitted.Message)
	out, err := rt.al.midTurnWindowCheck(rt.ts, candidate, ex.rx.rr.rq.ri.rf.providerToolDefs)
	if err != nil {
		return err
	}
	ex.rx.rr.rq.ri.messages = out
	return nil
}

// checkpointRecordedResult stages the normal result and any recalled span
// before its success-looking event or transcript record is emitted.
func (ex *agentLoopRunTurnToolsExecute) checkpointRecordedResult() (err error) {
	rt := ex.rx.rr.rq.ri.rf.rt
	ts := rt.ts
	ts.mu.RLock()
	span, at, n := ts.injectedRecallSpan, ts.injectedRecallAt, ts.injectedRecallLen
	ts.mu.RUnlock()
	defer func() {
		if err != nil {
			ts.mu.Lock()
			ts.injectedRecallSpan, ts.injectedRecallAt, ts.injectedRecallLen = span, at, n
			ts.mu.Unlock()
		}
	}()
	candidate := append(append([]providers.Message(nil), ex.rx.rr.rq.ri.messages...), ex.toolResultMsg)
	if ex.recallDecision.inject {
		ts.mu.Lock()
		candidate = rt.al.spliceRecallSpan(ts, candidate, ex.recallDecision.span)
		ts.mu.Unlock()
	}
	out, err := rt.al.midTurnWindowCheck(ts, candidate, ex.rx.rr.rq.ri.rf.providerToolDefs)
	if err != nil {
		return err
	}
	if len(out) == 0 || out[len(out)-1].Role != "tool" || out[len(out)-1].ToolCallID != ex.toolCallID {
		return fmt.Errorf("context checkpoint: newest admitted result is missing")
	}
	if !ts.opts.NoHistory && ex.admitted.ArchiveLine >= 0 {
		store, ok := ts.agent.Sessions.(session.ContextWindowStore)
		if !ok {
			return fmt.Errorf("context checkpoint: session store does not support atomic context checkpoints")
		}
		ctx := ts.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		snap, readErr := store.WindowView(ctx, ts.sessionKey)
		if readErr != nil {
			return readErr
		}
		key := memory.ProjectionKey{ToolCallID: ex.toolCallID, ArchiveLine: ex.admitted.ArchiveLine}
		ex.admitted.Projection = snap.State.Projection.Entries[key]
		ex.admitted.Capped = ex.admitted.Projection != ""
	}
	ex.toolResultMsg = out[len(out)-1]
	ex.rx.rr.rq.ri.messages = out
	return nil
}

func (ex *agentLoopRunTurnToolsExecute) contextWindowExit(err error) agentLoopRunTurnToolsExecuteFlow {
	rt := ex.rx.rr.rq.ri.rf.rt
	res, status, exitErr := rt.al.contextWindowTurnExit(rt.ts, rt.iteration, rt.llmModel, err)
	ex.rx.rr.rq.ri.turnStatus = status
	ex.rx.ret0, ex.rx.ret1 = res, exitErr
	ex.ret0 = agentLoopRunTurnToolsReturn
	return agentLoopRunTurnToolsExecuteReturn
}

// Storage/projection failures are real failed turns, never user cancellations
// or locally inferred context-size errors. The original error chain is returned.
func (al *AgentLoop) contextWindowTurnExit(ts *turnState, iteration int, model string, cause error) (turnResult, TurnEndStatus, error) {
	if _, typed := typedExitCode(cause); typed {
		return al.typedTurnExit(ts, iteration, model, cause)
	}
	al.emitTurnErrorFrame(ts, ts.eventMeta("contextCheckpoint", "turn.error"), "context_window", "context_window", TranslateTurnError(cause))
	return turnResult{status: TurnEndStatusError}, TurnEndStatusError, cause
}
