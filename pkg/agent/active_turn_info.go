// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ActiveForegroundTurnInfo reports whether a ROOT (foreground, depth==0 /
// parentTurnID=="") turn is currently in flight for transcriptSessionID —
// the id a webchat connection binds to via a `message`/`attach_session`
// frame (pkg/gateway/websocket.go), i.e. exactly the id whose in-flight-ness
// a reconnecting SPA needs to know (ADR-082 D4, FR-008).
//
// This is a NEW accessor, deliberately NOT reusing routingSessionID's
// CLOSED CONSUMER SET (see that field's doc comment in turn.go) — D4 is a
// display/reconnect-UX concern (session_state.active_turn), not a
// routing/interrupt-scope one, so it is kept out of that allowlist rather
// than widening it. It is also deliberately independent of (and does not
// replace) the retired ADR-045 orphan-watchdog helper
// getActiveRootTurnStateForSession, which was keyed on routingSessionID for
// a completely different purpose (cancel-scope reaping) and is being
// deleted in the same wave (ADR-082 D1) — do not resurrect that function or
// re-key this one onto routingSessionID to "consolidate" them.
//
// Returns ok=false when no root turn is currently active for the session —
// including when the only resolvable match is a non-root descendant (a
// delegated sub-turn never counts as "the session's foreground turn" here,
// matching the same root-exclusive semantics the retired watchdog helper
// used) or a root turnState that is still in activeTurnStates but has
// already finished (see the IsAlive() check below).
//
// [ADR-082 review F5/S2] Two fixes over the original version:
//
//  1. IsAlive() gate: a turnState is removed from al.activeTurnStates by
//     clearActiveTurn, which — because Go runs defers LIFO and clearActiveTurn
//     is registered AFTER finalizeStreamer in runTurn — fires BEFORE
//     finalizeStreamer, but is not the ONLY way a finished turnState could
//     transiently still be observed here (a caller racing the removal itself
//     could, in principle, observe a just-finished-but-not-yet-cleared
//     entry). Without this check, a finished-but-not-yet-cleared root would be
//     reported as "in flight", so the SPA re-arms its Stop control for a turn
//     that has already ended.
//  2. Deterministic selection: sync.Map.Range's iteration order is randomized
//     per call, so the original "first match wins, stop iterating" pick was
//     non-deterministic for the (rare, but possible) case of two live root
//     turns sharing a transcriptSessionID — the same session_state.active_turn
//     query could report a DIFFERENT turn on different calls with no state
//     change in between. This now scans every candidate and deterministically
//     prefers the earliest-started turn, breaking any remaining tie by turnID
//     so the result never depends on map iteration order.
func (al *AgentLoop) ActiveForegroundTurnInfo(transcriptSessionID string) (turnID, agentID string, startedAt time.Time, ok bool) {
	if transcriptSessionID == "" {
		return "", "", time.Time{}, false
	}
	var found *turnState
	var foundStarted time.Time
	var foundTurnID string
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, tok := value.(*turnState)
		if !tok {
			logger.ErrorCF("agent", "activeTurnStates: invariant violated — unexpected value type, skipping entry",
				map[string]any{"got_type": fmt.Sprintf("%T", value)})
			return true
		}
		if ts.transcriptSessionID != transcriptSessionID {
			return true
		}
		if ts.depth != 0 && ts.parentTurnID != "" {
			return true // not a root turn
		}
		if !ts.IsAlive() {
			return true // already finished — do not report it as in flight
		}
		ts.mu.RLock()
		tsStarted := ts.startedAt
		tsTurnID := ts.turnID
		ts.mu.RUnlock()
		if found == nil || tsStarted.Before(foundStarted) ||
			(tsStarted.Equal(foundStarted) && tsTurnID < foundTurnID) {
			found = ts
			foundStarted = tsStarted
			foundTurnID = tsTurnID
		}
		return true
	})
	if found == nil {
		return "", "", time.Time{}, false
	}
	found.mu.RLock()
	turnID = found.turnID
	agentID = found.agentID
	startedAt = found.startedAt
	found.mu.RUnlock()
	return turnID, agentID, startedAt, true
}
