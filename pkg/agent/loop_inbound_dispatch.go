// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, *AgentInstance, error) {
	if err := al.validateInboundWorkspace(msg); err != nil {
		return "", nil, err
	}
	if inboundMetadata(msg, secretResumeMetadataKey) != "" && !secretResumeContextValidated(ctx) {
		return al.processSecretResume(ctx, msg)
	}
	pm := &agentLoopProcessMessage{al: al, ctx: ctx, msg: msg}

	pm.prepareInbound()

	// Route system messages to processSystemMessage
	if pm.msg.Channel == "system" {
		resp, err := pm.al.processSystemMessage(pm.ctx, pm.msg)
		return resp, nil, err
	}

	// ADR-066 D4 / FR-015: the user-message bound, at the one point where an
	// inbound message becomes a turn — BEFORE routing mints a channel session,
	// before the transcript write below, before turn registration. Over the
	// bound, the reply is returned as this message's ordinary response: the
	// caller (session worker / unroutable path) publishes it on the
	// originating channel like any assistant reply — no error frame, no
	// transcript entry, no turn id. Media refs ride in msg.Media and are not
	// counted. See user_message_bound.go.
	if reply, refused := pm.al.refuseOversizedUserMessage(pm.msg); refused {
		logger.InfoCF("agent", "Refused oversized user message before turn start (ADR-066 D4)",
			map[string]any{
				"channel":     pm.msg.Channel,
				"chat_id":     pm.msg.ChatID,
				"size_chars":  UserMessageChars(pm.msg.Content),
				"bound_chars": pm.al.UserMessageBound(),
			})
		return reply, nil, nil
	}

	route, agent, routeErr := pm.al.resolveMessageRoute(pm.msg)
	if routeErr != nil {
		// FR-121: the single point where an unroutable channel message is
		// actually rejected — logged at error level, audited and shown as the
		// instance's routing degradation exactly once (resolveMessageRoute
		// itself is side-effect-free; it has several callers).
		var drop *routeDropError
		if errors.As(routeErr, &drop) {
			pm.al.recordRouteDrop(pm.msg, drop)
		}
		return "", nil, routeErr
	}

	// Reset message-tool state for this round so we don't skip publishing due to a previous round.
	if tool, ok := agent.Tools.Get("send_message"); ok {
		if resetter, ok := tool.(interface{ ResetSentInRound() }); ok {
			resetter.ResetSentInRound()
		}
	}

	// Resolve session key from route, while preserving explicit agent-scoped keys.
	scopeKey := resolveScopeKey(route, pm.msg.SessionKey)
	pm.sessionKey = scopeKey

	logger.InfoCF("agent", "Routed message",
		map[string]any{
			"agent_id":      agent.ID,
			"scope_key":     scopeKey,
			"session_key":   pm.sessionKey,
			"matched_by":    route.MatchedBy,
			"route_agent":   route.AgentID,
			"route_channel": route.Channel,
		})

	// For non-webchat channel messages arriving without a session ID, create or
	// resume a persistent shared session so they appear in the session history panel.
	// FR-022 (ADR-029): for a bound channel instance, stamp the session with the
	// instance's workspace_id so it appears linked to the right workspace.
	if pm.msg.SessionID == "" && pm.msg.Channel != "webchat" && pm.msg.Channel != "system" && pm.msg.Channel != "" {
		instanceSessionID := inboundInstanceID(pm.msg)
		sessionWorkspaceID := ""
		if instanceSessionID != "" {
			if sessionCfg := pm.al.GetConfig(); sessionCfg != nil {
				if instCfg, ok := sessionCfg.Channels[instanceSessionID]; ok {
					sessionWorkspaceID = instCfg.WorkspaceID
				}
			}
		}
		if sid := pm.al.resolveOrCreateChannelSession(
			pm.msg.Channel, instanceSessionID, pm.msg.ChatID, agent.ID, pm.msg.Sender.DisplayName, sessionWorkspaceID,
		); sid != "" {
			pm.msg.SessionID = sid
		} else {
			return "", nil, fmt.Errorf("could not prepare the channel conversation; check session storage and retry")
		}
	}

	// Resolve transcript store for tool call recording. SessionID is now
	// authoritative — populated directly by the gateway from frame.SessionID.

	if pm.msg.SessionID != "" {
		pm.transcriptSessionID = pm.msg.SessionID
		pm.transcriptStore = pm.al.ResolveSessionStore(pm.msg.SessionID)
		if pm.transcriptStore == nil {
			logger.WarnCF(
				"agent",
				"session_id present but store not found — tool calls will not be recorded",
				map[string]any{"session_id": pm.msg.SessionID},
			)
		}
	}

	// Write user message to transcript for channel sessions.
	// Web-chat user messages are written by the WebSocket handler before the
	// turn starts (websocket.go); this path covers every other channel
	// (Telegram, Slack, Discord, etc.) so that replays show the user's prompt
	// alongside tool calls and assistant responses.
	channelNeedsTranscript := pm.transcriptStore != nil &&
		pm.msg.Channel != "webchat" && pm.msg.Channel != "system" &&
		strings.TrimSpace(pm.msg.Content) != "" &&
		// Bound connector input was already written once by
		// admitBoundConnectorInput (it set TranscriptEntryID).
		pm.msg.TranscriptEntryID == ""
	if channelNeedsTranscript {
		entry := session.TranscriptEntry{
			ID:        fmt.Sprintf("user-%d", time.Now().UnixNano()),
			Role:      "user",
			AgentID:   agent.ID,
			Content:   pm.msg.Content,
			Timestamp: time.Now().UTC(),
		}
		// The id is carried only when the write landed: a failed write leaves
		// no entry to recognise, so the rebuild must not drop anything.
		pm.msg.TranscriptEntryID = ""
		if err := pm.transcriptStore.AppendTranscript(pm.transcriptSessionID, entry); err != nil {
			logger.WarnCF("agent", "could not record channel user message to transcript",
				map[string]any{"session_id": pm.transcriptSessionID, "channel": pm.msg.Channel, "error": err.Error()})
		} else {
			pm.msg.TranscriptEntryID = entry.ID
		}
	}

	pm.resolveWorkspace()

	pm.prepareTurn()

	// FR-025: reset idle ticker on every user turn, using transcript session ID
	// when available (web-chat sessions). This starts the ticker on the first
	// turn and resets it on every subsequent turn.
	if pm.transcriptSessionID != "" {
		pm.al.resetIdleTicker(pm.transcriptSessionID)
		// FR-024: track current session per agent for lazy CAS on switch.
		pm.al.agentCurrentSession.Store(agent.ID, pm.transcriptSessionID)
	}

	// context-dependent commands check their own Runtime fields and report
	// "unavailable" when the required capability is nil.
	if response, handled := pm.al.handleCommand(pm.ctx, pm.msg, agent, &pm.opts); handled {
		return response, agent, nil
	}

	// ADR-088 D1/D9: the ADR-074 D4a pending-goal reply-routing hook
	// (applyGoalPendingReply) is retired — goals activate instantly now, so
	// there is no more pending/clarification state for a bare chat message to
	// resolve against (FR-022: bare "confirm" is ordinary chat, no
	// interception exists). Nothing stands between handleCommand above and
	// runAgentLoop below anymore.
	return pm.al.runInboundTurnWithRevival(pm.ctx, agent, pm.msg, pm.opts)
}
