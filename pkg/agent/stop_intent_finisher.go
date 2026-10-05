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

// finishUnfinishedStopIntents is ADR-20260928 D4's finisher for accepted stop
// intents that never landed (D4 crash semantics: "Complete unfinished
// stop/Stop-all fences"; D6 QA1/QA2; #1053's durable retry item). Each intent
// of sessionID is carried out through the one Stop under its ORIGINAL
// identity — same control_id, seq, cause and actor; no new acceptance
// (StopRequest.Continue):
//   - the fence is (re)applied and the selected execution's settlement lands
//     it, which records the original control applied with its notice;
//   - a session that moved on (terminal, already stopped, another generation,
//     execution or control) gets nothing written to its record and the
//     intent is marked superseded.
//
// A failure leaves the original intent queued for the next pass and is
// returned naming the session and control.
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
	for i := range intents {
		intent := intents[i]
		controlID := intent.Selection.Effect.ControlID
		res, stopErr := al.StopSession(ctx, StopRequest{
			SessionID: sessionID,
			By:        principalFromStopActor(intent.Actor),
			Channel:   "stop-retry",
			Continue:  &intent,
		})
		if errors.Is(stopErr, session.ErrStopIntentSuperseded) {
			stopErr = nil
		} else if stopErr == nil {
			stopErr = res.RootErr
		}
		if stopErr == nil && len(res.Report.Unreachable) > 0 {
			stopErr = fmt.Errorf("%s", res.Report.Unreachable[0].Reason)
		}
		if stopErr != nil {
			errs = append(errs, fmt.Errorf("Stop retry for %s (control %s) did not take hold; it stays pending: %w",
				sessionID, controlID, stopErr))
			continue
		}
		if len(res.Report.Reached) > 0 {
			// The original control's own landing records it applied.
			continue
		}
		// The intent no longer owns the session: its receipt is superseded.
		if recordErr := lifecycle.RecordStopControlResult(intent.Selection, false, ""); recordErr != nil {
			errs = append(errs, fmt.Errorf("Stop retry for %s (control %s): %w", sessionID, controlID, recordErr))
		}
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
