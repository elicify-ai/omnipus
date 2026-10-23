package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// freezePriorTaskReconciliation records the uncertainty on the dead producing
// execution, not on a fresh Retry session. It neither creates an execution nor
// rewrites a frozen generation, a human Stop, or a replacement's identity.
func (al *AgentLoop) freezePriorTaskReconciliation(ctx context.Context, selected *session.LifecycleRecord) error {
	held, err := loadRecoveryHold(selected.SessionID)
	if err != nil || len(held.Occurrences) == 0 {
		return err
	}
	if selected.State == session.LifecycleInterrupted && selected.InterruptionReason == session.InterruptionReconciliationRequired {
		return al.ensureRecoveryInterruptionReport(selected)
	}
	if selected.Terminal() {
		return nil
	}
	claim := al.executionClaimFor(selected)
	if claim.RunID == "" || claim.BootSeq == 0 {
		return fmt.Errorf("reconciliation_required: prior producing execution is not established")
	}
	if claim.BootSeq >= al.bootEpochFor() || al.executionDispositionFor(claim) != nil {
		return fmt.Errorf("reconciliation_required: prior execution has not settled")
	}
	lc := al.GetSessionLifecycleStore()
	if selected.SteeredBy != nil {
		if err := al.completeSteeredTurnForExecution(ctx, selected, turnResult{finalContent: recoveryInterruptionNotice(selected.SessionID), interruptionReason: session.InterruptionReconciliationRequired}, nil, claim); err != nil {
			return err
		}
	} else {
		if err := lc.Mutate(selected.SessionID, func(current *session.LifecycleRecord) error {
			if !claim.matches(current) || current.Terminal() {
				return steer.ErrStaleGeneration
			}
			if current.Stopped() {
				return steer.ErrDispatchCancelled
			}
			intents, err := lc.UnfinishedStopIntentsLocked(current.SessionID)
			if err != nil {
				return err
			}
			if acceptedStopSelectsExecution(current, intents) {
				return steer.ErrDispatchCancelled
			}
			current.State = session.LifecycleInterrupted
			current.InterruptionReason = session.InterruptionReconciliationRequired
			current.FailedReason, current.NeedsInput = "", nil
			return nil
		}); err != nil {
			return err
		}
	}
	fresh, err := lc.Load(selected.SessionID)
	if err != nil {
		return err
	}
	if !claim.matches(fresh) || fresh.State != session.LifecycleInterrupted || fresh.InterruptionReason != session.InterruptionReconciliationRequired {
		return fmt.Errorf("reconciliation_required: prior execution ending remains unconfirmed")
	}
	return al.ensureRecoveryInterruptionReport(fresh)
}
