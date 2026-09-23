// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Implements ADR-091's I-3 turn reconstruction: every entry path (first run,
// wake, follow-up) rebuilds a steered session's turn from its lifecycle
// record through this ONE function, never by hand.
//
// reconstructSteeredTurn is called from two entry points:
// SessionLauncher.Dispatch (first run and revival, steer_launcher.go) and
// loop_inbound.go::processSteeredSystemWake (the wake path). The latter also
// appends I-3's consumption marker (step 3: "before executing, the turn
// appends a `consumed <MessageID>` marker") once the woken turn is certain
// to run. Boot recovery (boot_sweep.go::SteerBootRecovery) does not call
// this function — a steered session still mid-flight at boot is marked
// OutcomeInterrupted and delivered to its parent rather than resumed.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
		// The first turn is the explicit instruction that launched this
		// session. Mark it as user-originated for the session-owned goal loop;
		// wakes/re-entries carry their own origin and are not initial claims.
		UserInitiated: wake == nil,
	}
	if wake == nil {
		entries, readErr := store.ReadTranscript(rec.SessionID)
		if readErr != nil {
			return nil, fmt.Errorf("steer: reconstruct %q: read launch instruction: %w", rec.SessionID, readErr)
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Role == "user" && strings.TrimSpace(entries[i].Content) != "" {
				opts.UserMessage = entries[i].Content
				break
			}
		}
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
