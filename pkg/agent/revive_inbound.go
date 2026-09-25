// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-093 D4 — an open conversation keeps its delegation. The ordinary
// inbound-turn admission path (processMessage) admits the turn through a
// revival check: when the message's chat's own lifecycle record is terminal
// or durably stopped at its current generation, the record is revived (new
// generation via resumed_from, terminal history immutable) BEFORE any tool
// runs, so a delegate call in that very turn succeeds.
//
// What never passes through here: system wakes — processSystemMessage calls
// runAgentLoop directly and never reaches processMessage's tail — and every
// other runAgentLoop caller. The wrapper is hooked only into processMessage,
// the human-message path, which is what keeps MIN-004 ("system wakes never
// revive") true structurally: a delegate call in a system wake's turn hits
// D2's launch backstop instead.

package agent

import (
	"context"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// reviveInboundIsHumanTurn is MIN-004's human predicate: only an ordinary
// human message can revive. A system wake arrives with channel "system"
// and/or the steer-wake metadata key
// (loop_inbound.go::processSystemMessage's own dispatch key) and is excluded.
func reviveInboundIsHumanTurn(msg bus.InboundMessage) bool {
	if msg.Channel == "system" {
		return false
	}
	return msg.Metadata["steer_message_id"] == ""
}

// inboundRevivable reports whether sessionID's lifecycle record is terminal
// or durably stopped at its current generation — the two states ADR-093 D4
// lets an ordinary human message revive. No record, an unreadable record or
// a blank id is not revivable: nothing to revive, nothing to guess about.
func (al *AgentLoop) inboundRevivable(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	store := al.GetSessionLifecycleStore()
	if sessionID == "" || store == nil {
		return false
	}
	rec, err := store.Load(sessionID)
	if err != nil {
		return false
	}
	return rec.Terminal() || rec.Stopped()
}

// reviveRecordForHumanTurn revives a terminal or durably-stopped record to a
// new generation — SteerCanceller.Revive's existing shape, unchanged
// (generation+1, resumed_from = the session's own id, running, failed reason
// cleared, the older Stop kept as inert history) — and resets the session's
// UnifiedMeta.Status to active in the same revival (ADR-093 D4 / MIN-002:
// SteerCanceller.Revive has no UnifiedStore, so the AgentLoop wrapper does
// it). No dispatch lives here: whether the revived session runs an ordinary
// inbound turn (processMessage) or is redispatched as steered
// (ReviveStoppedSession) is the caller's routing decision.
func (al *AgentLoop) reviveRecordForHumanTurn(ctx context.Context, sessionID string, by steer.Principal) error {
	if _, err := al.steerCanceller().Revive(ctx, sessionID, by); err != nil {
		return err
	}
	if store := al.ResolveSessionStore(sessionID); store != nil {
		active := session.StatusActive
		if err := store.SetMeta(sessionID, session.MetaPatch{Status: &active}); err != nil {
			logger.WarnCF("agent", "adr093: revival could not reset the unified meta status to active",
				map[string]any{"session_id": sessionID, "error": err.Error()})
		}
	}
	return nil
}

// runInboundTurnWithRevival is processMessage's turn admission (ADR-093 D4):
// a human message whose chat's record is terminal or durably stopped revives
// the record first — before any tool runs — and the turn then runs on the
// new generation. Revival failure never fails the message: it is logged and
// the turn runs unrevived, where a delegate call in it hits D2's launch
// backstop with the D5 sentence rather than a raw store error.
func (al *AgentLoop) runInboundTurnWithRevival(
	ctx context.Context,
	agent *AgentInstance,
	msg bus.InboundMessage,
	opts processOptions,
) (string, *AgentInstance, error) {
	if reviveInboundIsHumanTurn(msg) && al.inboundRevivable(msg.SessionID) {
		if err := al.reviveRecordForHumanTurn(ctx, msg.SessionID,
			steer.Principal{Kind: steer.PrincipalKindHuman, ID: msg.GatewayUserID}); err != nil {
			logger.WarnCF("agent", "adr093: inbound revival failed; the turn runs unrevived",
				map[string]any{"session_id": msg.SessionID, "error": err.Error()})
		}
	}
	resp, err := al.runAgentLoop(ctx, agent, opts)
	return resp, agent, err
}
