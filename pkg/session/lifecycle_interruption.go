package session

import (
	"fmt"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// InterruptionReason uses the contract vocabulary without exposing private recovery state.
type InterruptionReason = generated.SessionLifecycleRecordInterruptionReason

const (
	InterruptionToolLimit              InterruptionReason = "tool_limit"
	InterruptionReconciliationRequired InterruptionReason = "reconciliation_required"
)

func validateLifecycleInterruption(rec *LifecycleRecord) error {
	if rec.State == LifecycleInterrupted {
		if rec.InterruptionReason != InterruptionToolLimit && rec.InterruptionReason != InterruptionReconciliationRequired {
			return fmt.Errorf("session: interrupted requires a valid interruption_reason")
		}
		if rec.FailedReason != "" || rec.NeedsInput != nil {
			return fmt.Errorf("session: interrupted cannot carry failed_reason or needs_input")
		}
	} else if rec.InterruptionReason != "" {
		return fmt.Errorf("session: interruption_reason requires state interrupted")
	}
	return nil
}
