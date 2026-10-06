package agent

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// scheduledRevivalPrincipal is the principal a scheduled or heartbeat entry
// revives a finished (done/failed) root with: a new round, never a resume of
// a stopped root — only a person resumes a stopped session.
var scheduledRevivalPrincipal = steer.Principal{Kind: steer.PrincipalKindAgent, ID: "scheduler"}

// A scheduled or heartbeat entry owns its disposition through the same ordinary
// loop's output/goal tail. It starts the next round of a finished root it
// reuses, and is refused visibly on a stopped one. It never takes a steered slot.
func (al *AgentLoop) runScheduledTurnWithAdmission(ctx context.Context, agent *AgentInstance, opts processOptions) (resp string, runErr error) {
	by := scheduledRevivalPrincipal
	preparation, err := al.prepareOrdinarySessionExecution(ctx, opts.TranscriptSessionID, opts, &by)
	if err != nil {
		return "", err
	}
	d := preparation.execution
	opts.executionDisposition = d
	defer func() { runErr = errors.Join(runErr, al.finishExecutionDisposition(d)) }()
	resp, runErr = al.runAgentLoop(ctx, agent, opts)
	d.recordTurnOutcome(runErr)
	return resp, runErr
}
