// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// steer_delegate_cancel.go — the stop an AGENT performs on a worker it
// started (delegate(action="stop_all")), wired from
// session_messaging_wire.go::wireSessionMessagingForAgent.
package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// cancelDelegatedSubtree stops sessionID and everything below it through the
// SAME durable cascade a human's Stop uses (pkg/gateway/websocket_cancel.go::
// cancelSteeredSubtree -> steer.Canceller), and returns the session ids it
// reached.
//
// It replaces the live-turn interrupt (Interrupt / InterruptSessionHard with
// ScopeSubtree) this hook used to be wired to, which was wrong twice over
// once ADR-091 made a worker a session rather than a sub-turn:
//
//   - A QUEUED worker has no live turn at all, so the interrupt reached
//     nothing, returned an empty descendant list, and the tool reported the
//     session had "terminated between the terminal check and the cancel
//     hook — no action needed". It had not: the record was still `queued`,
//     no Stop marker was written, and the worker started as soon as a slot
//     freed — while the at-limit result had told the model this very action
//     would "drop this queued session"
//     (pkg/tools/delegate_run.go::launchAndDispatch).
//   - ScopeSubtree's walk finds descendants through parentTurnID links
//     between live turns (steering.go::collectLiveDescendantTurnStates). No
//     steered turn has one — reconstructSteeredTurn builds every child as a
//     standalone turn — so cancelling a running child left its own
//     grandchildren running (AC-8, ADR-057 D8/R-13's leak, reopened by the
//     sub-turn path's deletion).
//
// The durable parent-child edge has neither problem: it is written at Launch,
// before any turn exists, and outlives every turn.
//
// hard chooses which half of I-6 applies to the LIVE turns the cascade
// reaches. hard=true is the human Stop exactly: stamp, then fire each turn's
// generation-aware hard abort. hard=false keeps ADR-053's cooperative
// contract — stamp durably now (so nothing queued can start and the stop
// survives a restart), then ask each reached turn to stop at its next tool
// boundary; the tool's own cancel_grace backstop escalates afterwards.
//
// Returns the reached ids, which is what the tool reads to tell "I stopped
// something" from "there was nothing to stop". An empty result with an
// unreachable node is an error, never a quiet success.
func (al *AgentLoop) cancelDelegatedSubtree(sessionID string, by steer.Principal, hard bool, hint string) ([]string, error) {
	reached, _, err := al.cancelDelegatedSubtreeSelected(sessionID, by, hard, hint)
	return reached, err
}

// cancelDelegatedSubtreeSoftWithBackstop is the soft stop plus the grace
// backstop bound to what that stop accepted. The returned backstop hard-aborts
// ONLY the executions the cooperative acceptance selected, through the same
// cascade lock and the same carried-selection effect boundaries — it accepts no
// new Stop, so a same-generation Resume admitted during the grace window is
// never a target. A nil backstop means the soft stop accepted nothing to abort.
func (al *AgentLoop) cancelDelegatedSubtreeSoftWithBackstop(sessionID string, by steer.Principal, hint string) ([]string, func() error, error) {
	reached, accepted, err := al.cancelDelegatedSubtreeSelected(sessionID, by, false, hint)
	if err != nil {
		return reached, nil, err
	}
	canceller := al.steerCanceller()
	backstop := func() error {
		report, cerr := canceller.ReapplySelectedStops(context.Background(), sessionID, accepted, al.SteerGenerationCancel)
		if cerr != nil {
			return fmt.Errorf("steer: delegate cancel %q: grace backstop: %w", sessionID, cerr)
		}
		if len(report.Unreachable) > 0 {
			return fmt.Errorf("steer: delegate cancel %q: grace backstop: unreachable %d (%s)",
				sessionID, len(report.Unreachable), unreachableSummary(report.Unreachable))
		}
		return nil
	}
	return reached, backstop, nil
}

// cancelDelegatedSubtreeSelected runs the cascade and also returns the
// acceptance-time selections a cooperative (hard=false) stop carried to its
// effect, in order, so a later backstop can act on exactly those.
func (al *AgentLoop) cancelDelegatedSubtreeSelected(sessionID string, by steer.Principal, hard bool, hint string) ([]string, []session.StopSelection, error) {
	if al == nil {
		return nil, nil, fmt.Errorf("steer: delegate cancel: no AgentLoop wired")
	}
	if al.GetSessionLifecycleStore() == nil {
		return nil, nil, fmt.Errorf("steer: delegate cancel: lifecycle store is not configured")
	}
	canceller := al.steerCanceller()
	ctx := context.Background()

	var report steer.CancelReport
	var err error
	var accepted []session.StopSelection
	if hard {
		report, err = canceller.CancelSubtree(ctx, sessionID, by)
	} else {
		report, err = canceller.StopTurns(ctx, sessionID, by, true,
			func(effectCtx context.Context, id string, generation int) (GenerationCancelResult, error) {
				if selected, carried := stopSelectionFromContext(effectCtx); carried {
					accepted = append(accepted, selected)
				}
				return al.steerSoftStop(effectCtx, id, generation, hint)
			})
	}
	if err != nil {
		return nil, nil, fmt.Errorf("steer: delegate cancel %q: %w", sessionID, err)
	}
	// Both hard and cooperative effects removed their own selected queue entry
	// in the callback. A post-return lookup by report ID could borrow a newer
	// Stop's pair, so no second session-based removal or interrupt runs here.

	if len(report.Reached) == 0 && len(report.Unreachable) > 0 {
		return nil, nil, fmt.Errorf("steer: delegate cancel %q: %s",
			report.Unreachable[0].ID, report.Unreachable[0].Reason)
	}
	// [Finding 3, ADR-091 fix lane 2] A partial cascade must never read as a
	// clean success: Unreachable and SkippedNewerGeneration surface as an
	// error naming the counts and ids, so the tool's "cooperatively
	// cancelled"/"hard-cancelled immediately" wording stays honest (see
	// delegate_run.go::cancelBackgroundShellWarnings for the neighbouring
	// shell-kill equivalent).
	// A selection superseded by a newer explicit action (D2/D5) left nothing
	// running because of this Stop, so it is not reported as running past it.
	stillRunning := withoutSuperseded(report.SkippedNewerGeneration, report.Superseded)
	if len(report.Unreachable) > 0 || len(stillRunning) > 0 {
		return report.Reached, accepted, fmt.Errorf(
			"steer: delegate cancel %q: partial cascade — reached %d; unreachable %d (%s); still running past a newer generation: %d (%s)",
			sessionID, len(report.Reached),
			len(report.Unreachable), unreachableSummary(report.Unreachable),
			len(stillRunning), strings.Join(stillRunning, ", "),
		)
	}
	return report.Reached, accepted, nil
}

// withoutSuperseded returns the ids in skipped that are not in superseded.
func withoutSuperseded(skipped, superseded []string) []string {
	out := make([]string, 0, len(skipped))
	for _, id := range skipped {
		if !slices.Contains(superseded, id) {
			out = append(out, id)
		}
	}
	return out
}

// unreachableSummary renders every unreachable session's id and reason for
// the partial-cascade error above — Finding 3 requires naming the ids, not
// just the count.
func unreachableSummary(items []steer.UnreachableSession) string {
	if len(items) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%s: %s", item.ID, item.Reason))
	}
	return strings.Join(parts, "; ")
}
