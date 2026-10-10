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
	"github.com/elicify-ai/omnipus/pkg/session"
)

// pinnedSteeredAgentID returns the LifecycleRecord-pinned agent for a steered
// child session, or "" when sessionID is blank, no lifecycle store is wired,
// no record exists, or the record is not steered (SteeredBy == nil — an
// ordinary root keeps today's dropdown/handoff/cascade routing). Store read
// errors also return "": a damaged record must not silently re-route a
// message that today's cascade would have delivered; classification-level
// repair stays with boot_sweep/SteerRecordClassifier.
func (al *AgentLoop) pinnedSteeredAgentID(sessionID string) string {
	rec := al.pinnedSteeredRecord(sessionID)
	if rec == nil {
		return ""
	}
	return strings.TrimSpace(rec.AgentID)
}

// pinnedSteeredRecord loads the LifecycleRecord for a steered child session, or
// nil when sessionID is blank, no store is wired, no record exists, or the
// record is not steered (SteeredBy == nil). It is the record-loading core the
// N3 human-message delivery needs (for AgentID AND Is3P), factored out of
// pinnedSteeredAgentID so the two callers agree on exactly which records count.
func (al *AgentLoop) pinnedSteeredRecord(sessionID string) *session.LifecycleRecord {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	store := al.GetSessionLifecycleStore()
	if store == nil {
		return nil
	}
	rec, err := store.Load(sessionID)
	if err != nil || rec == nil || rec.SteeredBy == nil {
		return nil
	}
	return rec
}

// deliverHumanHelperInput routes a person's message addressed to a steered
// helper session. It reports false when msg's session is not a steered
// helper (the ordinary dispatch handles it). A stopped or finished helper
// is revived through the ADR-093 path; a live one gets the text queued for
// its own execution, so a working helper continues with it (the R1 rule
// applies through the same queue). A refusal is answered visibly.
func (al *AgentLoop) deliverHumanHelperInput(msg bus.InboundMessage) bool {
	rec := al.pinnedSteeredRecord(msg.SessionID)
	var err error
	switch {
	case rec != nil:
		agentID := strings.TrimSpace(rec.AgentID)
		if agentID == "" {
			return false
		}
		err = al.enqueueHelperSteeringFromMessage(msg, agentID, rec.Is3P)
	default:
		// FR-032: a person typing into the chat of a LIVE external-CLI task run
		// steers that run (interrupt + native resume, consumed by the task
		// loop). Any other task chat keeps the ordinary dispatch.
		taskRec := al.liveExternalTaskRecord(msg.SessionID)
		if taskRec == nil {
			return false
		}
		_, err = al.DeliverExternalCLIInstruction(context.Background(), msg.SessionID, strings.TrimSpace(taskRec.AgentID),
			providers.Message{Role: "user", Content: msg.Content, Media: append([]string(nil), msg.Media...)}, "")
		if err != nil {
			err = fmt.Errorf("deliver message to external-CLI task run %q: %w", msg.SessionID, err)
		}
	}
	if err != nil {
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
//
// N3 (FR-043 / BDD-05.5): for an external-CLI (3P) helper the message is not
// merely queued — it is DELIVERED by interrupt + native-conversation resume,
// the SAME authorized mechanism delegate(action="steer") uses, because a
// person typing into a running external worker's own chat IS the entry BDD-05.5
// names ("send S in existing worker chat"). The delivery refuses visibly when
// there is no live external conversation (and, per the N4 seam, when the target
// is a task run), so a person's course-correction is never silently dropped.
// The native helper keeps the ordinary queue path unchanged.
func (al *AgentLoop) enqueueHelperSteeringFromMessage(msg bus.InboundMessage, agentID string, is3P bool) error {
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		return fmt.Errorf("route the message to helper %q: %w", msg.SessionID, err)
	}
	handled, err := al.reviveInactiveInbound(route, msg)
	if err != nil || handled {
		return err
	}
	if is3P {
		if _, derr := al.DeliverExternalCLIInstruction(
			context.Background(), msg.SessionID, agentID,
			providers.Message{Role: "user", Content: msg.Content, Media: append([]string(nil), msg.Media...)},
			"",
		); derr != nil {
			return fmt.Errorf("deliver message to external-CLI helper %q: %w", msg.SessionID, derr)
		}
		return nil
	}
	_, _, err = al.enqueueSteeringMessage(msg.SessionID, agentID, providers.Message{
		Role: "user", Content: msg.Content, Media: append([]string(nil), msg.Media...),
	}, "")
	return err
}

// liveExternalTaskRecord returns the lifecycle record of sessionID when it is a
// task-origin session on an external CLI whose run is in flight right now, else
// nil. Only a run the task loop can resume qualifies, so a person's message is
// never queued for a consumer that does not exist.
func (al *AgentLoop) liveExternalTaskRecord(sessionID string) *session.LifecycleRecord {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	rec, err := al.loadLifecycleRecord(sessionID)
	if err != nil || rec == nil || !rec.Is3P || rec.Origin == nil || rec.Origin.Kind != session.OriginKindTask {
		return nil
	}
	if strings.TrimSpace(rec.AgentID) == "" {
		return nil
	}
	sess := al.externalRunSessionIfPresent(sessionID)
	if sess == nil {
		return nil
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if !sess.running {
		return nil
	}
	return rec
}
