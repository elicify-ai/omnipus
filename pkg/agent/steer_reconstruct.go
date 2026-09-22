// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Owner: WP-A. ADR-091 landing order I-3 — turn reconstruction: every entry
// path (first run, wake, follow-up, boot) rebuilds a steered session's turn
// from its lifecycle record through this ONE function, never by hand.
//
// Scope note: this phase wires reconstruction into SessionLauncher.Dispatch
// (the first-run and revival entry points, steer_launcher.go). Wiring it
// into loop_inbound.go::processSystemMessage (the wake path) and the boot
// sweep (WP-D's boot_sweep.go) is deferred — see the phase-2 report — both
// because processSystemMessage's existing behaviour is exercised by tests
// outside WP-G's still-incomplete 46-of-49-unclassified glob, and because
// the boot sweep itself is WP-D-owned. The consumed-marker half of I-3's
// consumption rule (step 3: "before executing, the turn appends a
// `consumed <MessageID>` marker") is likewise not implemented here — it
// belongs to the same wake-path integration.
package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// reconstructSteeredTurn builds a *turnState for rec's session, entirely
// from the record — I-3: "never builds processOptions for a steered
// session by hand." wake is nil for a first run; when non-nil, a wake
// generation older than rec.Generation is refused (ErrStaleGeneration —
// I-3 step 2, "a stale wake queued before a revival").
//
// Refuses (I-3 step 1/4) any class but ClassSteered or ClassOrdinaryRoot —
// see steer.Class.Runnable().
func (al *AgentLoop) reconstructSteeredTurn(rec *session.LifecycleRecord, wake *steer.WakeInput) (*turnState, error) {
	if rec == nil {
		return nil, errors.New("steer: reconstruct: nil record")
	}

	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	class, err := classifier.Classify(context.Background(), rec.SessionID)
	if err != nil {
		return nil, fmt.Errorf("steer: reconstruct %q: classify: %w", rec.SessionID, err)
	}
	if !class.Runnable() {
		return nil, fmt.Errorf("steer: reconstruct %q: class %q is not runnable", rec.SessionID, class)
	}
	if wake != nil && wake.Generation < rec.Generation {
		return nil, steer.ErrStaleGeneration
	}

	agentInst, ok := al.GetRegistry().GetAgent(rec.AgentID)
	if !ok {
		return nil, fmt.Errorf("steer: reconstruct %q: %w: agent %q", rec.SessionID, steer.ErrAgentUnknown, rec.AgentID)
	}
	store := al.GetSessionStore()
	if store == nil {
		return nil, fmt.Errorf("steer: reconstruct %q: session store is not wired", rec.SessionID)
	}
	meta, err := store.GetMeta(rec.SessionID)
	if err != nil {
		return nil, fmt.Errorf("steer: reconstruct %q: load child address: %w", rec.SessionID, err)
	}

	opts := processOptions{
		SessionKey:          rec.SessionID,
		Channel:             meta.Channel,
		ChatID:              meta.PeerID,
		TranscriptSessionID: rec.SessionID,
		TranscriptStore:     store,
		// I-5: a steered session has no user audience — reconstruction
		// never sets SendResponse true for one. An ordinary_root record
		// (rec.SteeredBy == nil) reaching this path is a revival/re-entry
		// of a session that already talks normally; SendResponse stays
		// false here too — the resume/notification path (not this
		// function) decides whether to surface anything to a human.
		SendResponse: false,
		WorkspaceID:  rec.WorkspaceID,
	}
	ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
	ts.generation = rec.Generation
	if rec.SteeredBy != nil {
		// I-1/US-3/AS-1: "B's turn routing root is R" — the ADR-057 D2
		// identity split's one required parent-to-child assignment,
		// generalised: a steered turn's routingSessionID is the CASCADE
		// ROOT, inherited from the edge, never the child's own id (which
		// newTurnState defaulted it to above).
		ts.routingSessionID = session.RoutingSessionID(rec.SteeredBy.RootSessionID)
	}
	return ts, nil
}
