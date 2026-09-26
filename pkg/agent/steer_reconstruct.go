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
	"github.com/elicify-ai/omnipus/pkg/tools"
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
	// Finding 2 fix (ADR-091 seven-reviewer gate, 2026-09): rec.SteeredBy.
	// ToolExclusions is set by the delegate tool, persisted, exposed on the
	// wire, and asserted by a serialisation test — but was never actually
	// READ here. pkg/tools/registry.go's own CloneExcept doc comment
	// claimed it was "applied from the record at reconstruction"; this file
	// contained no such code, so a delegated child could call switch_agent,
	// which D2 and the long-standing identity rule forbid. turnAgent is
	// agentInst unchanged when there is nothing to exclude (the overwhelming
	// common case), so every existing non-excluding path is byte-identical
	// to before.
	turnAgent := agentInst
	if rec.SteeredBy != nil && len(rec.SteeredBy.ToolExclusions) > 0 {
		turnAgent = agentInstanceWithToolExclusions(agentInst, rec.SteeredBy.ToolExclusions)
	}
	ts := newTurnState(turnAgent, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
	ts.generation = rec.Generation
	if rec.SteeredBy != nil {
		// I-1/US-3/AS-1: "B's turn routing root is R" — the ADR-057 D2
		// identity split's one required parent-to-child assignment,
		// generalised: a steered turn's routingSessionID is the CASCADE
		// ROOT, inherited from the edge, never the child's own id (which
		// newTurnState defaulted it to above).
		ts.routingSessionID = session.RoutingSessionID(rec.SteeredBy.RootSessionID)
		// Delegate-session-id carrier (ADR-053/ADR-057 identity split):
		// stamp the session's OWN LifecycleRecord.SessionID onto the turn's
		// options — registerTurnContext turns it into
		// tools.WithDelegateSessionID, and message_parent loads its record
		// by it. An ordinary-root revival (rec.SteeredBy == nil) never
		// enters this branch — ADR-093 D3's standing-root test — so its
		// field stays "" and the carrier's empty-id no-op guard leaves
		// those turns unstamped (message_parent's structural refusal keeps
		// firing for them).
		ts.opts.SteeredSessionID = rec.SessionID
	}
	return ts, nil
}

// agentInstanceWithToolExclusions returns an independent *AgentInstance
// whose Tools registry has excluded names removed (pkg/tools/registry.go's
// ToolRegistry.CloneExcept — switch_agent today, FR-H-006), leaving base
// itself, and every OTHER session currently running that same shared agent,
// untouched. base.Tools is a pointer SHARED by every session that runs this
// agent (AgentRegistry.GetAgent returns the same *AgentInstance to every
// caller) — mutating base.Tools in place, or turning *base into a fresh
// struct via `cp := *base`, would either leak the exclusion onto every other
// session using this agent, or trip `go vet`'s copylocks check (base
// embeds a sync.RWMutex and an atomic.Pointer). The one existing primitive
// in this package that already solves exactly this — an independent,
// copylocks-safe *AgentInstance that shares every field with base except
// the ones a caller needs to override — is
// AgentInstance.snapshotForExternalDispatch (instance.go); this reuses it
// rather than hand-duplicating its ~30-field snapshot here, which would
// itself become the next stale-comment trap the moment a field is added to
// AgentInstance and only one of the two copies is kept in sync.
func agentInstanceWithToolExclusions(base *AgentInstance, excludedNames []string) *AgentInstance {
	if base == nil || base.Tools == nil {
		return base
	}
	excluded := make([]tools.ExcludedTool, 0, len(excludedNames))
	for _, name := range excludedNames {
		if name != "" {
			excluded = append(excluded, tools.ExcludedTool(name))
		}
	}
	if len(excluded) == 0 {
		return base
	}
	clone := base.snapshotForExternalDispatch()
	clone.Tools = base.Tools.CloneExcept(excluded...)
	return clone
}
