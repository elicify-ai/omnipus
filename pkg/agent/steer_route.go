// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 identity — steered-session route pinning. A steered child's agent
// is pinned by its LifecycleRecord (SteerLauncher.Launch writes AgentID at
// launch; reconstructSteeredTurn rebuilds every turn under it). This file
// holds the one lookup resolveMessageRoute uses to make inbound routing obey
// that pin; see the intercept there for the failure it prevents.
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// pinnedSteeredAgentID returns the LifecycleRecord-pinned agent for a steered
// child session, or "" when sessionID is blank, no lifecycle store is wired,
// no record exists, or the record is not steered (SteeredBy == nil — an
// ordinary root keeps today's dropdown/handoff/cascade routing). Store read
// errors also return "": a damaged record must not silently re-route a
// message that today's cascade would have delivered; classification-level
// repair stays with boot_sweep/SteerRecordClassifier.
func (al *AgentLoop) pinnedSteeredAgentID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	store := al.GetSessionLifecycleStore()
	if store == nil {
		return ""
	}
	rec, err := store.Load(sessionID)
	if err != nil || rec == nil || rec.SteeredBy == nil {
		return ""
	}
	return strings.TrimSpace(rec.AgentID)
}

// deliverHumanHelperInput routes a person's message addressed to a steered
// helper session. It reports false when msg's session is not a steered
// helper (the ordinary dispatch handles it). A stopped or finished helper
// is revived through the ADR-093 path; a live one gets the text queued for
// its own execution, so a working helper continues with it (the R1 rule
// applies through the same queue). A refusal is answered visibly.
func (al *AgentLoop) deliverHumanHelperInput(msg bus.InboundMessage) bool {
	agentID := al.pinnedSteeredAgentID(msg.SessionID)
	if agentID == "" {
		return false
	}
	if err := al.enqueueHelperSteeringFromMessage(msg, agentID); err != nil {
		logger.WarnCF("agent", "A message to a helper was not accepted",
			map[string]any{"session_id": msg.SessionID, "error": err.Error()})
		replyCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if pubErr := al.bus.PublishOutbound(replyCtx, bus.OutboundMessage{
			Channel: msg.Channel, ChatID: msg.ChatID, SessionID: msg.SessionID,
			Content: userVisibleTurnError(err),
		}); pubErr != nil {
			logger.ErrorCF("agent", "The refusal of a message to a helper could not be sent",
				map[string]any{"session_id": msg.SessionID, "error": pubErr.Error()})
		}
	}
	return true
}

// enqueueHelperSteeringFromMessage is enqueueSteeringFromMessage for a
// steered helper: its queue scope is the helper's own session id, the scope
// its execution polls.
func (al *AgentLoop) enqueueHelperSteeringFromMessage(msg bus.InboundMessage, agentID string) error {
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		return fmt.Errorf("route the message to helper %q: %w", msg.SessionID, err)
	}
	handled, err := al.reviveInactiveInbound(route, msg)
	if err != nil || handled {
		return err
	}
	_, _, err = al.enqueueSteeringMessage(msg.SessionID, agentID, providers.Message{
		Role: "user", Content: msg.Content, Media: append([]string(nil), msg.Media...),
	}, "")
	return err
}
