package agent

import (
	"context"
	"errors"
)

// A scheduled or heartbeat entry owns its disposition through the same ordinary
// loop's output/goal tail. It does not borrow human revival or a steered slot.
func (al *AgentLoop) runScheduledTurnWithAdmission(ctx context.Context, agent *AgentInstance, opts processOptions) (resp string, runErr error) {
	preparation, err := al.prepareOrdinarySessionExecution(ctx, opts.TranscriptSessionID, opts, nil)
	if err != nil {
		return "", err
	}
	d := preparation.execution
	opts.executionDisposition = d
	defer func() { runErr = errors.Join(runErr, al.finishExecutionDisposition(d)) }()
	return al.runAgentLoop(ctx, agent, opts)
}
