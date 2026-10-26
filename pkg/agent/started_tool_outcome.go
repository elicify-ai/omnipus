package agent

import (
	"context"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// startedToolOutcome serializes the display receipt with confirmed cancellation.
// It owns only a dispatched call, never the remaining advertised/queued calls.
// Normal model admission and its generation/abandonment fences remain separate.
type startedToolOutcome struct {
	mu        sync.Mutex
	ts        *turnState
	al        *AgentLoop
	call      session.ToolCall
	startedAt time.Time
	settled   bool
	cancelled bool
}

func (ex *agentLoopRunTurnToolsExecute) beginStartedTool(tc providers.ToolCall, at time.Time) bool {
	ts := ex.rx.rr.rq.ri.rf.rt.ts
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.hardAbort || ts.abandoned.Load() {
		return false
	}
	o := &startedToolOutcome{
		ts: ts, al: ex.rx.rr.rq.ri.rf.rt.al, startedAt: at,
		call: session.ToolCall{
			ID: session.ToolCallID(tc.ID), Tool: ex.toolName,
			Parameters:       cloneEventArguments(ex.toolArgs),
			ParentToolCallID: session.ToolCallID(ts.parentSpawnCallID),
		},
	}
	ts.startedTool = o
	ex.started = o
	return true
}

func (ts *turnState) cancelStartedTool() {
	ts.mu.RLock()
	o := ts.startedTool
	ts.mu.RUnlock()
	if o != nil {
		o.cancel()
	}
}

func (o *startedToolOutcome) cancel() {
	o.mu.Lock()
	if o.settled {
		o.mu.Unlock()
		return
	}
	o.settled, o.cancelled = true, true
	call := o.call
	call.Status = "cancelled"
	duration := time.Since(o.startedAt)
	call.DurationMS = duration.Milliseconds()
	// No archive line/model admission is invented. This reuses the addressed
	// append/guarded pending settlement used by ordinary display receipts.
	err := o.ts.appendToolCallTranscript(call)
	o.mu.Unlock()
	if err != nil {
		// Surface failure through the existing failed-turn/save-error channel,
		// not only a log, and never publish a successful durability claim.
		o.al.reportSessionSaveFailure(o.ts, &messageNotSavedError{role: "tool", cause: err})
		return
	}
	o.al.emitEvent(EventKindToolExecEnd, o.ts.eventMeta("runTurn", "turn.tool.cancelled"), ToolExecEndPayload{
		ToolCallID: call.ID, Tool: call.Tool, ChatID: o.ts.chatID,
		SessionID: u9ToolExecSessionIDs(o.ts), Duration: duration,
		IsError: true, Cancelled: true, ParentSpawnCallID: call.ParentToolCallID,
		AgentID: o.ts.resolveActiveAgentID(),
	})
}

func (o *startedToolOutcome) complete(write func() error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cancelled || o.ts.hardAbortRequested() || o.ts.abandoned.Load() {
		return context.Canceled
	}
	if o.settled {
		return nil
	}
	if err := write(); err != nil {
		return err
	}
	o.settled = true
	return nil
}
