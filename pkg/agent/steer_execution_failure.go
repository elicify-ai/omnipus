// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
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
	res, err := al.commitSteeredCompletion(lifecycle, rec, session.LifecycleFailed, steer.OutcomeFailed, "", reason, claim, nil)
	if err != nil {
		// No failed outcome or final outbox committed. Validate the original
		// producer before reporting that persistence error; a replacement or
		// Stop must not inherit it as a current-run failure.
		current, readErr := lifecycle.Load(claim.SessionID)
		if readErr != nil {
			return errors.Join(err, readErr)
		}
		if !claim.matches(current) || current.Terminal() || current.Stopped() {
			return err
		}
		// This is an ordinary nonfatal parent error, never a protected final,
		// stopped notice, control receipt, or claim that recovery completed.
		noticeErr := al.deliverSteeredNotice(ctx, rec, steer.OutcomeLifecycleNotice, "",
			"A queued turn could not start, and its failure outcome could not be saved. The outcome remains uncommitted; repair storage before retrying.")
		return errors.Join(err, noticeErr)
	}
	if res.kind != steeredCommitTerminal {
		return nil
	}
	_, err = al.publishCommittedFinal(ctx, rec, res)
	return err
}
