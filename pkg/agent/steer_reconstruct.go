// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Implements ADR-091's I-3 turn reconstruction: every entry path (first run,
// wake, follow-up) rebuilds a steered session's turn from its lifecycle
// record through this ONE function, never by hand.
//
// reconstructSteeredTurn is called from three entry paths:
// SessionLauncher.Dispatch (first run and revival, steer_launcher.go),
// loop_inbound.go::processSteeredSystemWake (the wake path), and the bounded
// post-turn steering drain (steer_turn_drain.go). The wake path also appends
// I-3's consumption marker (step 3: "before executing, the turn appends a
// `consumed <MessageID>` marker") once the woken turn is certain to run; the
// drain writes the equivalent marker through Continue's shared dequeue path.
// Boot recovery (boot_sweep.go::SteerBootRecovery) does not call this
// function — a steered session still mid-flight at boot is marked
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

	// FIX (live-view gap, 2026-09-27): a steered child's own UnifiedMeta is
	// minted with an empty Channel by design (steer_launcher.go's
	// reportingTargetFor doc comment: "a steered session's OWN UnifiedMeta
	// is minted with an empty Channel and is never given a PeerID at all")
	// — that emptiness exists for the UPWARD reporting-target resolution
	// (rec.SteeredBy.ReportingTarget), a separate field entirely. Reusing
	// meta.Channel as the TURN's own opts.Channel broke live delivery to
	// the child's OWN session view: WSHandler.GetStreamer
	// (pkg/gateway/websocket_streamer.go) refuses a streamer unless
	// channel=="webchat", so every steered turn silently fell back to a
	// non-streaming provider call and never published a single live token
	// or done frame for its own session — invisible to a browser attached
	// directly to that child, regardless of audience/shadow-stream gating,
	// which never got a chance to run. Live-verified against a debug-level
	// gateway log correlated by session id: every "Provider supports
	// streaming, checking for streamer" call for a steered child logged
	// channel:"" and was never followed by "Using streaming for response";
	// every root-session call logged channel:"webchat" and always got one
	// (36/36 vs 9/9 across two live delegation runs). A steered child's own
	// live view is a real webchat surface (ADR-091 D1: each child owns its
	// own store-backed session, rendered through the identical ChatScreen/
	// WS path a root session uses) — stamp it explicitly rather than
	// inheriting the reporting-target's empty placeholder. An
	// ordinary_root revival (rec.SteeredBy == nil) keeps meta.Channel
	// untouched: that session's channel is real (webchat, whatsapp, ...)
	// and must never be overwritten.
	channel := meta.Channel
	if rec.SteeredBy != nil {
		channel = "webchat"
	}

	opts := processOptions{
		SessionKey:          rec.SessionID,
		Channel:             channel,
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
		// A queued promotion consumes ALL wake content in arrival order. Take
		// the current list (not the earlier Load snapshot) and clear it in a
		// single durable mutation BEFORE the turn can start. If that write
		// fails, return an error: running while the prompts remain persisted
		// would replay the same content on a later promotion.
		if len(rec.PendingUserMessages) > 0 {
			var pending []string
			if persistErr := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(r *session.LifecycleRecord) error {
				if r == nil {
					return session.ErrLifecycleNotFound
				}
				if len(r.PendingUserMessages) == 0 {
					return fmt.Errorf("steer: reconstruct %q: queued wakes disappeared before promotion", rec.SessionID)
				}
				pending = append([]string(nil), r.PendingUserMessages...)
				r.PendingUserMessages = nil
				return nil
			}); persistErr != nil {
				return nil, fmt.Errorf("steer: reconstruct %q: consume queued wakes: %w", rec.SessionID, persistErr)
			}
			opts.UserMessage = strings.Join(pending, "\n\n")
		} else {
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
