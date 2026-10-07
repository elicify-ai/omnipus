package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func copyRevivalExecution(identity *session.ExecutionIdentity) *session.ExecutionIdentity {
	if identity == nil {
		return nil
	}
	copied := *identity
	return &copied
}

// reviveOrdinaryRecordWithExecution closes the ordinary revival/stamp crash
// window using the existing revival mutation, not a second continuation path.
// The selected generation/run is rechecked under that lock; metadata mirroring
// and revival failures retain the existing ordinary wrapper's behavior.
func (al *AgentLoop) reviveOrdinaryRecordWithExecution(ctx context.Context, selected *session.LifecycleRecord, identity session.ExecutionIdentity) error {
	if _, err := al.steerCanceller().reviveWithExecution(ctx, selected.SessionID, selected, &identity); err != nil {
		al.markRevivalFailure(selected.SessionID, err)
		return err
	}
	al.clearRevivalFailure(selected.SessionID)
	al.resetUnifiedMetaStatusActive(selected.SessionID)
	return nil
}
