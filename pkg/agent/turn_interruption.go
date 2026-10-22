package agent

import (
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func (ts *turnState) markInterrupted(reason session.InterruptionReason) {
	ts.mu.Lock()
	ts.interruptionReason = reason
	ts.mu.Unlock()
	if d := ts.opts.executionDisposition; d != nil {
		d.mu.Lock()
		d.interruptionReason = reason
		d.mu.Unlock()
	}
}

func (ts *turnState) interruptedReason() session.InterruptionReason {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.interruptionReason
}

func interruptionReasonForOutcome(outcome steer.Outcome) session.InterruptionReason {
	switch outcome {
	case steer.OutcomeToolLimit:
		return session.InterruptionToolLimit
	case steer.OutcomeReconciliationRequired:
		return session.InterruptionReconciliationRequired
	default:
		return ""
	}
}

func endedInterruption(outcome steer.Outcome) bool {
	return interruptionReasonForOutcome(outcome) != ""
}
