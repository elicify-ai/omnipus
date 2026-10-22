package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// checkTaskRecovery follows prior-run fences before any fresh session is created.
func (al *AgentLoop) checkTaskRecovery(ctx context.Context, t *task.Task) error {
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
			return fmt.Errorf("reconciliation_required: prior run %s requires reconciliation: %w", id, err)
		}
	}
	return nil
}
