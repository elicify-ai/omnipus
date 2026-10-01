// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Outbound response publishing for the agent loop. This code moved here from
// loop.go (pure move, no behavior change) so the CHECK round-2 fix commit
// could hold pkg/agent/loop.go under its shrink-only size-budget pin
// (scripts/budgets/files.txt) — pkg/agent/CLAUDE.md: new code belongs in a
// sibling file, not appended to the pinned loop.go.
package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// publishResponseIfNeeded publishes response to the bus unless it is empty or
// the turn already sent a message via the send_message tool this round. Every
// inbound caller with a reply the user has not yet seen goes through this one
// path: loop.go's unroutable-message frame, the session worker's turn end and
// terminal frames, and the revived ordinary-root turn (revive_support.go).
//
// turnIdentity, when the caller passes exactly two strings (turnID,
// messageID — session_worker.go's processTurn does, reading them back from
// its turnIdentityOutbox), stamps bus.OutboundMessage.TurnID/MessageID so a
// webchat Send-fallback TokenFrame carries the originating turn's identity
// instead of going out unstamped (#1081 R3). Variadic and optional so every
// existing caller (including this package's own tests) is unchanged.
func (al *AgentLoop) publishResponseIfNeeded(ctx context.Context, ag *AgentInstance, channel, chatID, response string, turnIdentity ...string) {
	if response == "" {
		return
	}

	alreadySent := false
	if ag == nil {
		ag = al.GetRegistry().GetDefaultAgent()
	}
	if ag != nil {
		if tool, ok := ag.Tools.Get("send_message"); ok {
			if mt, ok := tool.(*tools.MessageTool); ok {
				alreadySent = mt.HasSentInRound()
			}
		}
	}

	if alreadySent {
		logger.DebugCF(
			"agent",
			"Skipped outbound (message tool already sent)",
			map[string]any{"channel": channel},
		)
		return
	}

	outbound := bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: response,
	}
	if len(turnIdentity) == 2 {
		outbound.TurnID = turnIdentity[0]
		outbound.MessageID = turnIdentity[1]
	}
	if err := al.bus.PublishOutbound(ctx, outbound); err != nil {
		logger.ErrorCF("agent", "Failed to publish outbound response",
			map[string]any{"channel": channel, "chat_id": chatID, "error": err.Error()})
		return
	}
	logger.InfoCF("agent", "Published outbound response",
		map[string]any{
			"channel":     channel,
			"chat_id":     chatID,
			"content_len": len(response),
		})
}

// publishTurnResponse is publishResponseIfNeeded plus reading box's identity
// (#1081 R3) — the one call processTurn (session_worker.go) uses, so its own
// several publish sites don't each repeat the snapshot-then-call pair.
func (al *AgentLoop) publishTurnResponse(ctx context.Context, ag *AgentInstance, channel, chatID, response string, box *turnIdentityOutbox) {
	turnID, messageID := box.snapshot()
	al.publishResponseIfNeeded(ctx, ag, channel, chatID, response, turnID, messageID)
}
