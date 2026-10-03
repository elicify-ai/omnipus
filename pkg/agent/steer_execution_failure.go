// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// reportSteeredExecutionFailure retains the failed queue admission's immutable
// identity across the delayed promotion callback. A later owner is never
// selected just because it now occupies the same session and generation.
func (al *AgentLoop) reportSteeredExecutionFailure(ctx context.Context, claim executionClaim, reason string) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return fmt.Errorf("steer: admission failure: lifecycle store is not wired")
	}
	rec, err := lifecycle.Load(claim.SessionID)
	if err != nil {
		return err
	}
	res, err := al.commitSteeredCompletion(lifecycle, rec, session.LifecycleFailed, steer.OutcomeFailed, "", reason, claim)
	if err != nil {
		return err
	}
	if res.kind != steeredCommitTerminal {
		return nil
	}
	_, err = al.publishCommittedFinal(ctx, rec, res)
	return err
}
