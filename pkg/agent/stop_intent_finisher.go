// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// finishUnfinishedStopIntents is ADR-20260928 D4's finisher for stop intents
// whose fence never took hold (D4 crash semantics: "Complete unfinished
// stop/Stop-all fences"; D6 #1053's durable retry item). For each unfinished
// intent of sessionID:
//   - the session moved on (terminal, already stopped, another generation, or
//     another control's fence) -> the intent is marked superseded;
//   - otherwise the stop is carried out through the one Stop (StopSession,
//     this session only, the intent's own cause and actor) and the original
//     intent is then marked superseded by that new control.
//
// A failure leaves the intent queued for the next pass and is returned.
func (al *AgentLoop) finishUnfinishedStopIntents(ctx context.Context, sessionID string) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || sessionID == "" {
		return nil
	}
	intents, err := lifecycle.UnfinishedStopIntents(sessionID)
	if err != nil || len(intents) == 0 {
		return err
	}
	var errs []error
	for _, intent := range intents {
		rec, loadErr := lifecycle.Load(sessionID)
		if loadErr != nil {
			if errors.Is(loadErr, session.ErrLifecycleNotFound) {
				errs = append(errs, lifecycle.RecordStopControlResult(intent.Selection, false, ""))
				continue
			}
			errs = append(errs, fmt.Errorf("Stop retry for %s: %w", sessionID, loadErr))
			continue
		}
		stillOwed := !rec.Terminal() && rec.State != session.LifecycleStopped &&
			rec.Generation == intent.Selection.Effect.Target.Generation &&
			(rec.Stop == nil || rec.Stop.Generation != rec.Generation)
		if stillOwed {
			res, stopErr := al.StopSession(ctx, StopRequest{
				SessionID: sessionID,
				By:        principalFromStopActor(intent.Actor),
				Channel:   "stop-retry",
				Cause:     intent.Cause,
			})
			if stopErr == nil {
				stopErr = res.RootErr
			}
			if stopErr == nil && len(res.Report.Unreachable) > 0 {
				stopErr = fmt.Errorf("%s", res.Report.Unreachable[0].Reason)
			}
			if stopErr != nil {
				errs = append(errs, fmt.Errorf("Stop retry for %s (control %s) did not take hold; it stays pending: %w",
					sessionID, intent.Selection.Effect.ControlID, stopErr))
				continue
			}
		}
		// The original intent no longer owns the session (moved on, or the
		// retry's new control does): its receipt becomes superseded.
		errs = append(errs, lifecycle.RecordStopControlResult(intent.Selection, false, ""))
	}
	return errors.Join(errs...)
}

// principalFromStopActor is the inverse of session.StopActorFromPrincipal for
// the "kind:id" actor strings an accepted stop intent records.
func principalFromStopActor(actor string) steer.Principal {
	kind, id, found := strings.Cut(actor, ":")
	if !found {
		return steer.Principal{Kind: steer.PrincipalKind(actor)}
	}
	return steer.Principal{Kind: steer.PrincipalKind(kind), ID: id}
}

// FinishUnfinishedStopIntents runs the finisher over every session with a
// lifecycle record. The gateway boot hook calls it after boot recovery
// stopped the interrupted runs (D4: stop interrupted sessions first, then
// reconcile pending controls). Every failure is returned; nothing is
// dispatched.
func (al *AgentLoop) FinishUnfinishedStopIntents(ctx context.Context) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil
	}
	records, err := lifecycle.List(session.LifecycleFilter{})
	if err != nil {
		return fmt.Errorf("Stop retry pass: list sessions: %w", err)
	}
	var errs []error
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		errs = append(errs, al.finishUnfinishedStopIntents(ctx, rec.SessionID))
	}
	return errors.Join(errs...)
}
