package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
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
// The selected generation/run is rechecked under that lock; metadata
// mirroring retains the existing ordinary wrapper's behavior. (The
// revival-failure ledger memory was removed with the D2 launch backstop it
// served — ADR-093 amendment 2026-10-23.) by is the principal that caused
// the revival: only a person's revival clears
// the record's Unattended flag (#891), exactly as on every other revival path.
func (al *AgentLoop) reviveOrdinaryRecordWithExecution(ctx context.Context, selected *session.LifecycleRecord, identity session.ExecutionIdentity, by steer.Principal) error {
	if _, err := al.steerCanceller().reviveWithExecution(ctx, selected.SessionID, by, selected, &identity); err != nil {
		return refuseOrdinaryIfStale(err)
	}
	al.resetUnifiedMetaStatusActive(selected.SessionID)
	return nil
}
