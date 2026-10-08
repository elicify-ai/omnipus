package agent

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// scheduledRevivalPrincipal revives a finished root into its next round, or a
// restart-stopped standing root on the same generation. An operator Stop still
// requires a person to resume; recovery itself never dispatches a turn.
var scheduledRevivalPrincipal = steer.Principal{Kind: steer.PrincipalKindAgent, ID: "scheduler"}

// A scheduled or heartbeat entry owns its disposition through the same ordinary
// loop's output/goal tail. It starts the next round of a finished root or
// resumes a restart-stopped standing one. Other stops are refused visibly.
// It never takes a steered slot.
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
