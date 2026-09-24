// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// websocket_forward_hub_spans.go: #823 catch-up redesign — the parent
// session's sub-agent lifecycle frames (subagent_start / subagent_end and,
// since ADR-091 D7/I-4, subagent_message / subagent_state), owned by the
// SESSION hub rather than by each browser connection (BE-DESIGN.md §1.2 and
// §7).
//
// Before the hub, every connected tab ran its own eventForwarder, so a
// session watched by two tabs produced every subagent frame twice (once per
// tab, each translated independently). Here the EventBus sync tap
// translates each lifecycle event exactly once and publishes it once
// through the session hub (one sequence number, byte-identical bytes to
// every bound tab, journaled whether or not a tab is attached).
//
// ADR-091 UAT defect 2 retired the orphan watchdog (a steered child is a
// session of its own and is DESIGNED to outlive its parent's turn; its real
// terminal state arrives via its own EventKindSubTurnEnd from
// pkg/agent/steer_frames.go::deliverSubagentEnd). The hub-side watchdog,
// its span registry and the #605 root-turn latch that the #823 redo had
// moved here were retired with it; what stays is the once-per-session
// publication of the real frames.
package gateway

import (
	"encoding/json"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// hubResolveSpanSession returns the session hub a span event belongs to:
// the payload's routing session id when present (ADR-057 FR-011 — a
// delegated child's span frames are numbered in the PARENT's hub), else the
// session currently bound to the event's chat id.
func (h *WSHandler) hubResolveSpanSession(payloadSessionID, chatID string) string {
	if payloadSessionID != "" {
		return payloadSessionID
	}
	return h.hubResolveSessionIDForChat(chatID)
}

// hubSubTurnSpawn publishes subagent_start once through the session hub
// (FR-H-004). ADR-091 I-4: the payload's Label carries the steered child's
// own session id, surfaced as child_session_id for the side panel's open
// control.
func (h *WSHandler) hubSubTurnSpawn(evt agent.Event) {
	p, ok := evt.Payload.(agent.SubTurnSpawnPayload)
	if !ok || h.hubs == nil {
		return
	}
	sid := h.hubResolveSpanSession(p.SessionID, p.ChatID)
	if sid == "" {
		return
	}
	logsafeDebug("ws: subagent_start",
		"span_id", p.SpanID,
		"parent_call_id", p.ParentSpawnCallID,
		"agent_id", p.AgentID,
		"session_id", sid,
	)
	frame := generated.SubagentStartFrame{
		Type:         string(generated.WsFrameTypeSubagentStart),
		SessionId:    sid,
		SpanId:       p.SpanID,
		ParentCallId: string(p.ParentSpawnCallID),
		TaskLabel:    p.TaskLabel,
	}
	if p.Label != "" {
		childSessionID := p.Label
		frame.ChildSessionId = &childSessionID
	}
	if p.AgentID != "" {
		aid := p.AgentID
		frame.AgentId = &aid
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal subagent_start for hub failed", "session_id", sid, "error", err)
		return
	}
	h.hubPublishMetaAlsoTo(sid, string(generated.WsFrameTypeSubagentStart),
		hubFrameMeta{}, data, nil) // not a projection item — see activeTurnProjection.active
}

// hubSubTurnEnd publishes the real subagent_end once through the session hub.
func (h *WSHandler) hubSubTurnEnd(evt agent.Event) {
	p, ok := evt.Payload.(agent.SubTurnEndPayload)
	if !ok || h.hubs == nil {
		return
	}
	sid := h.hubResolveSpanSession(p.SessionID, p.ChatID)
	if sid == "" {
		return
	}
	logsafeDebug("ws: subagent_end",
		"span_id", p.SpanID,
		"parent_call_id", p.ParentSpawnCallID,
		"agent_id", p.AgentID,
		"session_id", sid,
	)
	frame := generated.SubagentEndFrame{
		Type:      string(generated.WsFrameTypeSubagentEnd),
		SessionId: sid,
		SpanId:    p.SpanID,
		Status:    string(p.Status),
	}
	if p.DurationMS != 0 {
		dm := int(p.DurationMS)
		frame.DurationMs = &dm
	}
	if p.AgentID != "" {
		aid := p.AgentID
		frame.AgentId = &aid
	}
	if p.ParentSpawnCallID != "" {
		pc := string(p.ParentSpawnCallID)
		frame.ParentCallId = &pc
	}
	// FIX 4: SubTurnEndPayload.Reason rides the wire as
	// SubagentEndFrame.reason (ADR-091 RX-SUBTURN note: its only current
	// constructor, steer_frames.go::deliverSubagentEnd, never sets it).
	if p.Reason != "" {
		reason := p.Reason
		frame.Reason = &reason
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal subagent_end for hub failed", "session_id", sid, "error", err)
		return
	}
	h.hubPublishMetaAlsoTo(sid, string(generated.WsFrameTypeSubagentEnd),
		hubFrameMeta{}, data, nil)
}

// hubSubagentMessage publishes ADR-091 D7/I-4's side-panel status line
// (subagent_message) once through the PARENT session's hub. The payload's
// SessionID is the parent's own session (agent.SubagentMessagePayload); the
// frame was built and persisted into the parent's transcript by
// pkg/agent/steer_frames.go::deliverSubagentMessage before the event was
// emitted, so its MessageId agrees between live, replay and cold load.
func (h *WSHandler) hubSubagentMessage(evt agent.Event) {
	p, ok := evt.Payload.(agent.SubagentMessagePayload)
	if !ok || h.hubs == nil {
		return
	}
	sid := p.SessionID
	if sid == "" {
		return
	}
	data, err := json.Marshal(p.Frame)
	if err != nil {
		logsafeError("ws: marshal subagent_message for hub failed", "session_id", sid, "error", err)
		return
	}
	h.hubPublishAndDeliver(sid, string(generated.WsFrameTypeSubagentMessage), data)
}

// hubSubagentState publishes ADR-091 D7/I-4's subagent_state once through
// the PARENT session's hub — see hubSubagentMessage for the id and
// persistence contract, which applies identically.
func (h *WSHandler) hubSubagentState(evt agent.Event) {
	p, ok := evt.Payload.(agent.SubagentStatePayload)
	if !ok || h.hubs == nil {
		return
	}
	sid := p.SessionID
	if sid == "" {
		return
	}
	data, err := json.Marshal(p.Frame)
	if err != nil {
		logsafeError("ws: marshal subagent_state for hub failed", "session_id", sid, "error", err)
		return
	}
	h.hubPublishAndDeliver(sid, string(generated.WsFrameTypeSubagentState), data)
}
