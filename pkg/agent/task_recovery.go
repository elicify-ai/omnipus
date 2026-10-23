package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// checkTaskRecovery follows prior-run fences before any fresh session is created.
func (al *AgentLoop) checkTaskRecovery(ctx context.Context, t *task.Task) error {
	if len(t.RecoverySessionIDs) > 0 && al.GetSessionLifecycleStore() == nil {
		return fmt.Errorf("reconciliation_required: prior-run recovery identity is unavailable")
	}
	for _, id := range t.RecoverySessionIDs {
		rec, err := al.GetSessionLifecycleStore().Load(id)
		if err != nil {
			return fmt.Errorf("reconciliation_required: prior run state cannot be read: %w", err)
		}
		inst, ok := al.GetRegistry().GetAgent(rec.AgentID)
		if !ok {
			return fmt.Errorf("reconciliation_required: prior run agent is unavailable")
		}
		if _, err := al.recoverSessionToolCalls(ctx, inst, rec, id); err != nil {
			endingErr := al.freezePriorTaskReconciliation(ctx, rec)
			return fmt.Errorf("reconciliation_required: prior run %s requires reconciliation: %w", id, errors.Join(err, endingErr))
		}
	}
	return nil
}

func (al *AgentLoop) enforceInheritedTaskRecovery(ts *turnState) error {
	lc := al.GetSessionLifecycleStore()
	if lc == nil {
		return nil
	}
	id := ts.transcriptSessionID
	if id == "" {
		id = ts.sessionKey
	}
	rec, err := lc.Load(id)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if rec.Origin == nil || rec.Origin.Kind != session.OriginKindTask {
		return nil
	}
	if al.taskExecutor == nil {
		return fmt.Errorf("reconciliation_required: task recovery references cannot be read")
	}
	t, err := al.taskExecutor.store.Get(rec.Origin.TaskID)
	if err != nil {
		return err
	}
	for _, prior := range t.RecoverySessionIDs {
		if prior == id {
			continue
		}
		held, err := loadRecoveryHold(prior)
		if err != nil {
			return err
		}
		if len(held.Occurrences) > 0 {
			return fmt.Errorf("reconciliation_required: a fresh task session cannot bypass its prior run's unresolved operations")
		}
	}
	return nil
}
