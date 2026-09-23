// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// steer_delegate_cancel.go — the stop an AGENT performs on a worker it
// started (delegate(action="cancel")), wired from
// session_messaging_wire.go::wireSessionMessagingForAgent.
package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
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
	if al == nil {
		return nil, fmt.Errorf("steer: delegate cancel: no AgentLoop wired")
	}
	if al.GetSessionLifecycleStore() == nil {
		return nil, fmt.Errorf("steer: delegate cancel: lifecycle store is not configured")
	}
	canceller := al.steerCanceller()
	ctx := context.Background()

	var report steer.CancelReport
	var err error
	if hard {
		report, err = canceller.CancelSubtree(ctx, sessionID, by)
	} else {
		report, err = canceller.StopSubtree(ctx, sessionID, by)
	}
	if err != nil {
		return nil, fmt.Errorf("steer: delegate cancel %q: %w", sessionID, err)
	}

	gate := al.steerAdmission()
	for _, id := range report.Reached {
		// A stamped session must not stay in the start queue — see
		// admission.go::removeQueuedSession for why this matters even though
		// reserveDispatch would refuse the promotion.
		gate.removeQueuedSession(id)
		if hard {
			continue
		}
		// ScopeSelfOnly, deliberately: the subtree was already enumerated
		// through the durable edge above, so each reached session needs only
		// its OWN live turn asked to stop. ScopeSubtree here would re-walk
		// the parentTurnID links that no longer exist and add nothing.
		if _, interruptErr := al.Interrupt(id, ScopeSelfOnly, hint); interruptErr != nil {
			logger.WarnCF("agent", "steer: delegate cancel: cooperative interrupt failed (the Stop marker is durable)",
				map[string]any{"session_id": id, "root_session_id": sessionID, "error": interruptErr.Error()})
		}
	}

	if len(report.Reached) == 0 && len(report.Unreachable) > 0 {
		return nil, fmt.Errorf("steer: delegate cancel %q: %s",
			report.Unreachable[0].ID, report.Unreachable[0].Reason)
	}
	return report.Reached, nil
}
