// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// websocket_forward_hub_spans.go: #823 catch-up redesign — sub-agent spans
// (subagent_start / subagent_end) and the orphan watchdog, owned by the
// SESSION hub rather than by each browser connection (BE-DESIGN.md §1.2 and
// §7).
//
// Before this file, every connected tab ran its own eventForwarder with its
// own openSpans map and its own orphan watchdog per span. A session watched
// by two tabs therefore produced every subagent frame twice (once per tab,
// each translated independently) and, for an orphaned span, two independent
// watchdogs each synthesizing their own subagent_end{interrupted}. Here the
// EventBus sync tap translates each span event exactly once, publishes it
// once through the session hub (one sequence number, byte-identical bytes to
// every bound tab), and runs exactly one watchdog per span per session.
//
// Why the old orphanFires/eventsAhead hand-off is gone: that machinery
// existed because the per-connection forwarder read events from a lossy,
// asynchronous subscriber queue, so a watchdog verdict could overtake a real
// SubTurnEnd that was already queued but not yet consumed. The sync tap runs
// on the emitting goroutine INSIDE EventBus.Emit, and pkg/agent only clears
// its "span still open" liveness record (markSubTurnSpanEnded) AFTER Emit
// returns — so by the time IsSubTurnActiveForSpawnCall can report "not
// active", this file has already closed the span under spanMu. The watchdog
// re-checks the span under that same lock before synthesizing, so a real end
// always wins; there is no queue left for a verdict to overtake.
package gateway

import (
	"encoding/json"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// hubSpan is one in-flight sub-agent span tracked by a session hub.
type hubSpan struct {
	spanID          string
	parentCallID    string
	agentID         string
	sessionID       string        // routing session id at spawn time; carried on the synthetic end
	parentTurnEnded bool          // the owning root turn ended; the watchdog is armed
	closeCh         chan struct{} // closed when the span resolves (real or synthetic end)
}

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

// hubTurnStart resets a session's root-turn-ended latch when a NEW root turn
// begins (#605): spans that root turn spawns belong to a live parent and must
// be registered unarmed. A CHILD turn's start never resets it — it arrives
// between its own spawn and any new root turn, and resetting there would
// leave a sibling delegate's later spawn unarmed forever.
func (h *WSHandler) hubTurnStart(evt agent.Event) {
	p, ok := evt.Payload.(agent.TurnStartPayload)
	if !ok || !p.IsRoot || h.hubs == nil {
		return
	}
	// The payload's routing session id (#823 review item 8) — the same key
	// hubTurnEnd latches under. The chat binding is only a fallback for an
	// emitter that predates the field: a background turn's chat id is bound
	// to no browser connection, and its event SessionKey
	// ("agent:<id>:session:<sid>") is not a session id at all.
	sid := h.hubResolveSpanSession(p.SessionID, p.ChatID)
	if sid == "" {
		return
	}
	hub := h.hubs.getOrCreate(sid)
	hub.spanMu.Lock()
	hub.rootTurnEnded = false
	hub.rootTurnEndReason = ""
	hub.spanMu.Unlock()
}

// hubSubTurnSpawn publishes subagent_start once through the session hub and
// registers the span for orphan tracking (FR-H-004).
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
	if p.AgentID != "" {
		aid := p.AgentID
		frame.AgentId = &aid
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal subagent_start for hub failed", "session_id", sid, "error", err)
		return
	}
	hub := h.hubs.getOrCreate(sid)
	entry := &hubSpan{
		spanID:       p.SpanID,
		parentCallID: string(p.ParentSpawnCallID),
		agentID:      p.AgentID,
		sessionID:    sid,
		closeCh:      make(chan struct{}),
	}

	hub.spanMu.Lock()
	h.hubPublishMetaAlsoTo(sid, string(generated.WsFrameTypeSubagentStart),
		hubFrameMeta{kind: hubKindSpanStart, key: p.SpanID}, data, nil)
	if hub.spans == nil {
		hub.spans = make(map[string]*hubSpan)
	}
	if prev := hub.spans[entry.parentCallID]; prev != nil {
		// A second spawn under the same call id replaces the first; stop the
		// first's watchdog so it can never synthesize an end for a span the
		// registry no longer tracks.
		closeHubSpan(prev)
	}
	hub.spans[entry.parentCallID] = entry
	// #605: the root turn already ended, so hubTurnEnd's arming pass has run
	// and will never see this entry — arm it now.
	arm := hub.rootTurnEnded
	reason := hub.rootTurnEndReason
	if arm {
		entry.parentTurnEnded = true
	}
	hub.spanMu.Unlock()
	if arm {
		h.startOrphanWatchdog(hub, entry, reason)
	}
}

// hubSubTurnEnd publishes the real subagent_end once through the session hub
// and resolves the span, which stops its watchdog.
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
	// FIX 4: SubTurnEndPayload.Reason (set only for Status == interrupted)
	// rides the wire as SubagentEndFrame.reason.
	if p.Reason != "" {
		reason := p.Reason
		frame.Reason = &reason
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal subagent_end for hub failed", "session_id", sid, "error", err)
		return
	}
	hub := h.hubs.getOrCreate(sid)
	hub.spanMu.Lock()
	h.hubPublishMetaAlsoTo(sid, string(generated.WsFrameTypeSubagentEnd),
		hubFrameMeta{kind: hubKindSpanEnd, key: p.SpanID}, data, nil)
	if entry := hub.spans[string(p.ParentSpawnCallID)]; entry != nil {
		closeHubSpan(entry)
		delete(hub.spans, string(p.ParentSpawnCallID))
	}
	hub.spanMu.Unlock()
}

// hubTurnEnd arms the orphan watchdog for every still-open span of the
// session whose ROOT turn just ended (W1-2: a sub-turn's own end never arms
// a sibling's watchdog), and latches the ended state for spans whose spawn
// event arrives later (#605).
func (h *WSHandler) hubTurnEnd(evt agent.Event) {
	p, ok := evt.Payload.(agent.TurnEndPayload)
	if !ok || !p.IsRoot || h.hubs == nil {
		return
	}
	sid := h.hubResolveSpanSession(p.SessionID, p.ChatID)
	if sid == "" {
		return
	}
	reason := orphanWatchdogReason(p.Status)
	hub := h.hubs.getOrCreate(sid)
	hub.spanMu.Lock()
	hub.rootTurnEnded = true
	hub.rootTurnEndReason = reason
	var toArm []*hubSpan
	for _, entry := range hub.spans {
		if !entry.parentTurnEnded {
			entry.parentTurnEnded = true
			toArm = append(toArm, entry)
		}
	}
	hub.spanMu.Unlock()
	for _, entry := range toArm {
		h.startOrphanWatchdog(hub, entry, reason)
	}
}

// orphanWatchdogReason maps the parent turn's terminal status to the
// SubagentEndFrame.message reason a synthesized interrupted end carries.
func orphanWatchdogReason(status agent.TurnEndStatus) string {
	switch status {
	case agent.TurnEndStatusAborted:
		return "parent_cancelled"
	case agent.TurnEndStatusError:
		return "parent_timeout"
	case agent.TurnEndStatusCompleted:
		return "parent_done_early"
	default:
		// TurnEndStatusParked and anything unknown: unchanged from the
		// per-connection forwarder, which never distinguished them.
		return "unknown"
	}
}

// closeHubSpan closes entry's closeCh exactly once. Caller holds hub.spanMu.
func closeHubSpan(entry *hubSpan) {
	select {
	case <-entry.closeCh:
	default:
		close(entry.closeCh)
	}
}

// startOrphanWatchdog runs ONE watchdog goroutine for entry (BE-DESIGN.md
// §7: one per span per session, never one per tab). After
// orphanWatchdogTimeout it asks whether the real sub-turn is still running
// (agent.AgentLoop.IsSubTurnActiveForSpawnCall) and reschedules while it is,
// up to orphanWatchdogMaxRechecks times (fail-closed past that ceiling — a
// genuinely wedged sub-turn must never wedge its span "running" forever).
// When the span is confirmed orphaned it synthesizes
// subagent_end{status:"interrupted"} — but only if the span is still open
// under spanMu at that moment (see this file's header for why a real end can
// no longer lose that race).
func (h *WSHandler) startOrphanWatchdog(hub *sessionHub, entry *hubSpan, reason string) {
	// Snapshot both test-shrinkable knobs synchronously, before the goroutine
	// starts: a test restores them the moment its own assertions pass, and
	// re-reading the package vars from this long-lived goroutine would race
	// that restore (caught under -race before, see the pre-#823 forwarder).
	watchdogTimeout := orphanWatchdogTimeout
	maxRechecks := orphanWatchdogMaxRechecks
	go func() {
		rechecks := 0
		for {
			timer := time.NewTimer(watchdogTimeout)
			select {
			case <-entry.closeCh:
				timer.Stop()
				return
			case <-timer.C:
			}
			stillActive := h.agentLoop != nil && h.agentLoop.IsSubTurnActiveForSpawnCall(entry.parentCallID)
			forced := false
			if stillActive {
				rechecks++
				if rechecks <= maxRechecks {
					logFn := logsafeDebug
					if rechecks > 2 {
						logFn = logsafeWarn
					}
					logFn("ws: subagent span still genuinely active past watchdog timeout — rescheduling",
						"event", "span_orphan_recheck_still_alive",
						"span_id", entry.spanID,
						"parent_call_id", entry.parentCallID,
						"reason", reason,
						"rechecks", rechecks,
						"max_rechecks", maxRechecks,
					)
					continue
				}
				forced = true
				logsafeError("ws: subagent span still reports active past the watchdog's reschedule "+
					"ceiling — force-emitting interrupted (fail-closed)",
					"event", "span_orphan_ceiling_exceeded",
					"span_id", entry.spanID,
					"parent_call_id", entry.parentCallID,
					"reason", reason,
					"rechecks", rechecks,
					"max_rechecks", maxRechecks,
				)
			}
			h.synthesizeOrphanEnd(hub, entry, reason, forced)
			return
		}
	}()
}

// synthesizeOrphanEnd publishes the synthetic interrupted subagent_end for
// entry through the session hub — once — unless the span was already
// resolved (its real end, or a replacement spawn under the same call id).
func (h *WSHandler) synthesizeOrphanEnd(hub *sessionHub, entry *hubSpan, reason string, forced bool) {
	hub.spanMu.Lock()
	defer hub.spanMu.Unlock()
	if hub.spans[entry.parentCallID] != entry {
		return
	}
	switch {
	case forced:
		// Already logged at Error level by the watchdog.
	case reason == "unknown":
		logsafeError("ws: subagent span orphaned with unknown reason — synthesizing interrupted end",
			"event", "span_orphan_interrupted",
			"span_id", entry.spanID,
			"parent_call_id", entry.parentCallID,
			"reason", reason,
		)
	default:
		logsafeWarn("ws: subagent span orphaned — synthesizing interrupted end",
			"event", "span_orphan_interrupted",
			"span_id", entry.spanID,
			"parent_call_id", entry.parentCallID,
			"reason", reason,
		)
	}
	reasonCopy := reason
	frame := generated.SubagentEndFrame{
		Type:      string(generated.WsFrameTypeSubagentEnd),
		SessionId: entry.sessionID,
		SpanId:    entry.spanID,
		Status:    "interrupted",
		Message:   &reasonCopy,
	}
	if entry.agentID != "" {
		aid := entry.agentID
		frame.AgentId = &aid
	}
	if entry.parentCallID != "" {
		pc := entry.parentCallID
		frame.ParentCallId = &pc
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal synthetic subagent_end failed", "session_id", entry.sessionID, "error", err)
		return
	}
	h.hubPublishMetaAlsoTo(entry.sessionID, string(generated.WsFrameTypeSubagentEnd),
		hubFrameMeta{kind: hubKindSpanEnd, key: entry.spanID}, data, nil)
	closeHubSpan(entry)
	delete(hub.spans, entry.parentCallID)
}
