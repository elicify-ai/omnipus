// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 D7/I-4 — the four sub-agent lifecycle frames (subagent_start,
// subagent_state, subagent_message, subagent_end), persisted as events in
// the PARENT's own transcript at the moment they happen, keyed by the
// edge's Origin.CallID, so the existing since-cursor replay
// (pkg/gateway/websocket_replay.go::loadReplay) returns them after a
// reload with no new store. This file owns the two frames that had NO
// emitter at all before ADR-091 (subagent_message, subagent_state — "the
// ADR-053 contract, wired at last"), persisted and emitted together from
// steer_audience.go's Deliver, mirroring goal_outcome.go's proven
// "persist first, then emit with the same id" pattern (GoalOutcomePayload's
// own doc comment).
//
// The launcher emits and persists start/end directly. The legacy event
// subscriber remains only until the retired in-chat executor is physically
// deleted; it ignores launcher-authored events to avoid duplicate entries.
package agent

import (
	"context"
	"fmt"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// persistSubagentEntry writes one ADR-091 D7/I-4 sub-agent lifecycle frame
// into parentSessionID's own transcript (the PARENT's, never the child's).
// patch sets exactly one of TranscriptEntry's four Subagent* fields.
func (al *AgentLoop) persistSubagentEntry(parentSessionID, id, subtype string, patch func(*session.TranscriptEntry)) error {
	if parentSessionID == "" || id == "" {
		return fmt.Errorf("steer: persistSubagentEntry: parentSessionID and id are required")
	}
	store := al.GetSessionStore()
	if store == nil {
		return fmt.Errorf("steer: persistSubagentEntry: no session store configured")
	}
	entry := session.TranscriptEntry{
		ID:            id,
		Type:          session.EntryTypeSystem,
		SystemSubtype: subtype,
		Timestamp:     time.Now().UTC(),
	}
	patch(&entry)
	return store.AppendTranscriptStrict(parentSessionID, entry)
}

// subagentSpanID mirrors pkg/gateway/replay.go::buildSubagentStart's own
// span-id convention ("span_" + the originating tool-call id) so a live
// push, a replay and a cold load all key the same span the same way.
func subagentSpanID(originCallID string) string {
	return "span_" + originCallID
}

func (al *AgentLoop) deliverSubagentStart(parentSessionID string, childRec *session.LifecycleRecord, title string) {
	if al == nil || parentSessionID == "" || childRec == nil || childRec.Origin == nil || childRec.Origin.CallID == "" {
		return
	}
	callID := childRec.Origin.CallID
	id := callID + ":start"
	childID := childRec.SessionID
	frame := generated.SubagentStartFrame{
		Type:           string(generated.WsFrameTypeSubagentStart),
		SessionId:      parentSessionID,
		ChildSessionId: &childID,
		SpanId:         subagentSpanID(callID),
		ParentCallId:   callID,
		TaskLabel:      title,
	}
	if childRec.AgentID != "" {
		agentID := childRec.AgentID
		frame.AgentId = &agentID
	}
	if err := al.persistSubagentEntry(parentSessionID, id, session.SystemSubtypeSubagentStart, func(e *session.TranscriptEntry) {
		e.SubagentStart = &frame
	}); err != nil {
		logger.WarnCF("agent", "steer: persist subagent_start failed",
			map[string]any{"parent_session_id": parentSessionID, "child_session_id": childRec.SessionID, "error": err.Error()})
		return
	}
	al.emitEvent(EventKindSubTurnSpawn,
		EventMeta{Source: "steer", TracePath: "steer.launch", SessionKey: parentSessionID},
		SubTurnSpawnPayload{
			AgentID:           childRec.AgentID,
			Label:             childRec.SessionID,
			SpanID:            subagentSpanID(callID),
			ParentSpawnCallID: session.ToolCallID(callID),
			TaskLabel:         title,
			SessionID:         parentSessionID,
		})
}

func (al *AgentLoop) deliverSubagentEnd(parentSessionID string, childRec *session.LifecycleRecord, outcome steer.Outcome) {
	if al == nil || parentSessionID == "" || childRec == nil || childRec.Origin == nil || childRec.Origin.CallID == "" {
		return
	}
	status := SubTurnStatusError
	switch outcome {
	case steer.OutcomeFinalAnswer:
		status = SubTurnStatusSuccess
	case steer.OutcomeInterrupted:
		status = SubTurnStatusInterrupted
	case steer.OutcomeTimedOut:
		status = SubTurnStatusTimeout
	case steer.OutcomeParkedQuestion:
		status = SubTurnStatusParked
	}
	callID := childRec.Origin.CallID
	id := callID + ":end"
	frame := generated.SubagentEndFrame{
		Type:      string(generated.WsFrameTypeSubagentEnd),
		SessionId: parentSessionID,
		SpanId:    subagentSpanID(callID),
		Status:    string(status),
	}
	parentCallID := callID
	frame.ParentCallId = &parentCallID
	if childRec.AgentID != "" {
		agentID := childRec.AgentID
		frame.AgentId = &agentID
	}
	if err := al.persistSubagentEntry(parentSessionID, id, session.SystemSubtypeSubagentEnd, func(e *session.TranscriptEntry) {
		e.SubagentEnd = &frame
	}); err != nil {
		logger.WarnCF("agent", "steer: persist subagent_end failed",
			map[string]any{"parent_session_id": parentSessionID, "child_session_id": childRec.SessionID, "error": err.Error()})
		return
	}
	al.emitEvent(EventKindSubTurnEnd,
		EventMeta{Source: "steer", TracePath: "steer.complete", SessionKey: parentSessionID},
		SubTurnEndPayload{
			AgentID:           childRec.AgentID,
			Status:            status,
			SpanID:            subagentSpanID(callID),
			ParentSpawnCallID: session.ToolCallID(callID),
			SessionID:         parentSessionID,
		})
}

// deliverSubagentMessage persists then emits ONE subagent_message frame
// (ADR-091 D7/I-4) reporting childSessionID's upward event to its steering
// session's side panel. Called from steer_audience.go's Deliver for every
// successful delivery whose Outcome maps onto a kind (below) — never for
// OutcomeWaitingChildren, which produces no upward event at all (I-5).
// Best-effort: a failure here never fails the caller's Deliver — the
// durable inbox entry (message_inbox.go) is the load-bearing delivery;
// this is the side-panel status line, R2's "sourced only from that
// child's own subagent_message/subagent_state frames".
func (al *AgentLoop) deliverSubagentMessage(parentSessionID string, childRec *session.LifecycleRecord, kind, text string, pct *int) {
	if al == nil || parentSessionID == "" || childRec == nil || childRec.Origin == nil || childRec.Origin.CallID == "" {
		return
	}
	originCallID := childRec.Origin.CallID
	id := fmt.Sprintf("%s:%s:msg:%d", originCallID, kind, time.Now().UnixNano())
	childID := childRec.SessionID
	frame := generated.SubagentMessageFrame{
		Type:            string(generated.WsFrameTypeSubagentMessage),
		MessageId:       id,
		// UAT defect 1: SessionId is the PARENT's own session (the frame's
		// routing key — SubagentMessageFrame.yaml: "Session in which the
		// parent's span is running"), exactly like deliverSubagentStart
		// above. The child's own id rides ChildSessionId, a separate field —
		// it never belongs in SessionId. Getting this backwards is what
		// left every subagent_message filed under the CHILD's own bucket
		// on the SPA (which keys purely off session_id), so the parent's
		// side panel never saw it and the pill's running count stayed 0.
		SessionId:       parentSessionID,
		ChildSessionId:  &childID,
		SpanId:          subagentSpanID(originCallID),
		Kind:            kind,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		SenderIdentity:  childRec.AgentID,
		UntrustedOrigin: true,
	}
	if text != "" {
		frame.Text = &text
	}
	if pct != nil {
		frame.Pct = pct
	}
	if err := al.persistSubagentEntry(parentSessionID, id, session.SystemSubtypeSubagentMessage, func(e *session.TranscriptEntry) {
		e.SubagentMessage = &frame
	}); err != nil {
		logger.WarnCF("agent", "steer: persist subagent_message failed",
			map[string]any{"parent_session_id": parentSessionID, "kind": kind, "error": err.Error()})
		return
	}
	al.emitEvent(
		EventKindSubagentMessage,
		EventMeta{Source: "steer", TracePath: "subagent.message", SessionKey: parentSessionID},
		SubagentMessagePayload{SessionID: parentSessionID, MessageID: id, Frame: frame},
	)
}

// deliverSubagentState persists then emits ONE subagent_state frame
// (ADR-091 D7/I-4) reporting childSessionID's lifecycle transition to its
// steering session's side panel. Called from steer_audience.go's Deliver
// on a TERMINAL outcome (state follows the wake-eligibility decision, not
// every non-terminal report) — subagent_end (the existing
// EventKindSubTurnEnd mechanism, not this file's) follows this state
// transition, per I-4's ordering: "subagent_end follows the terminal
// subagent_state". Mid-flight transitions this lane cannot observe
// (queued -> running, a Stop) are NOT emitted here — see this lane's
// final report for that residual gap (no real launcher/canceller exists
// in this worktree to hook).
func (al *AgentLoop) deliverSubagentState(parentSessionID string, childRec *session.LifecycleRecord, state string) {
	if al == nil || parentSessionID == "" || childRec == nil || childRec.Origin == nil || childRec.Origin.CallID == "" {
		return
	}
	originCallID := childRec.Origin.CallID
	id := fmt.Sprintf("%s:%d:state:%s", originCallID, childRec.Generation, state)
	childID := childRec.SessionID
	frame := generated.SubagentStateFrame{
		Type: string(generated.WsFrameTypeSubagentState),
		// UAT defect 1: same fix as deliverSubagentMessage above — SessionId
		// is the PARENT's own session (SubagentStateFrame.yaml: "Session in
		// which the parent's span is running"), never the child's, with the
		// child's id carried separately in ChildSessionId, exactly as
		// deliverSubagentStart already does.
		SessionId:      parentSessionID,
		ChildSessionId: &childID,
		SpanId:         subagentSpanID(originCallID),
		State:          state,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if err := al.persistSubagentEntry(parentSessionID, id, session.SystemSubtypeSubagentState, func(e *session.TranscriptEntry) {
		e.SubagentState = &frame
	}); err != nil {
		logger.WarnCF("agent", "steer: persist subagent_state failed",
			map[string]any{"parent_session_id": parentSessionID, "state": state, "error": err.Error()})
		return
	}
	al.emitEvent(
		EventKindSubagentState,
		EventMeta{Source: "steer", TracePath: "subagent.state", SessionKey: parentSessionID},
		SubagentStatePayload{SessionID: parentSessionID, MessageID: id, Frame: frame},
	)
}

// StartSubagentSpawnPersister subscribes to the EXISTING
// EventKindSubTurnSpawn/EventKindSubTurnEnd broadcast (emitted by this same
// file's persistSubagentEntry callers) and persists each ONCE as a
// subagent_start/subagent_end transcript entry (ADR-091 D7/I-4) —
// deliberately a connection-INDEPENDENT subscriber, never inside
// pkg/gateway/websocket_forward.go's per-connection onSubTurnSpawn/
// onSubTurnEnd (which would persist once per connected viewer). Live
// delivery is unaffected: those existing per-connection forwarders keep
// emitting the live frame exactly as they do today; this subscriber only
// adds the durable half. Started once at boot by
// gateway_boot.go::wireSteerDeps.
func (al *AgentLoop) StartSubagentSpawnPersister(ctx context.Context) context.CancelFunc {
	subCtx, cancel := context.WithCancel(ctx)
	sub := al.SubscribeEvents(64)
	go func() {
		defer al.UnsubscribeEvents(sub.ID)
		for {
			select {
			case <-subCtx.Done():
				return
			case evt, ok := <-sub.C:
				if !ok {
					return
				}
				al.persistSubTurnSpawnOrEnd(evt)
			}
		}
	}()
	return cancel
}

// persistSubTurnSpawnOrEnd is StartSubagentSpawnPersister's per-event
// handler, individually recovered so one malformed event never kills the
// subscriber for the whole process (mirrors runSessionMessageConsumer's
// discipline, session_messaging_wire.go).
func (al *AgentLoop) persistSubTurnSpawnOrEnd(evt Event) {
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("agent", "steer: subagent spawn/end persister: panic; recovered",
				map[string]any{"panic": fmt.Sprintf("%v", r)})
		}
	}()
	// Launcher-authored events are already persisted synchronously before
	// emission. This subscriber serves only the legacy executor.
	if evt.Meta.Source == "steer" {
		return
	}
	switch p := evt.Payload.(type) {
	case SubTurnSpawnPayload:
		if p.SessionID == "" || p.ParentSpawnCallID == "" {
			return
		}
		id := string(p.ParentSpawnCallID) + ":start"
		frame := generated.SubagentStartFrame{
			Type:         string(generated.WsFrameTypeSubagentStart),
			SessionId:    p.SessionID,
			SpanId:       p.SpanID,
			ParentCallId: string(p.ParentSpawnCallID),
			TaskLabel:    p.TaskLabel,
		}
		if p.AgentID != "" {
			aid := p.AgentID
			frame.AgentId = &aid
		}
		if err := al.persistSubagentEntry(p.SessionID, id, session.SystemSubtypeSubagentStart, func(e *session.TranscriptEntry) {
			e.SubagentStart = &frame
		}); err != nil {
			logger.WarnCF("agent", "steer: persist subagent_start failed",
				map[string]any{"session_id": p.SessionID, "error": err.Error()})
		}
	case SubTurnEndPayload:
		if p.SessionID == "" || p.ParentSpawnCallID == "" {
			return
		}
		id := string(p.ParentSpawnCallID) + ":end"
		frame := generated.SubagentEndFrame{
			Type:      string(generated.WsFrameTypeSubagentEnd),
			SessionId: p.SessionID,
			SpanId:    p.SpanID,
			Status:    string(p.Status),
		}
		if p.AgentID != "" {
			aid := p.AgentID
			frame.AgentId = &aid
		}
		if p.ParentSpawnCallID != "" {
			pc := string(p.ParentSpawnCallID)
			frame.ParentCallId = &pc
		}
		if p.Reason != "" {
			reason := p.Reason
			frame.Reason = &reason
		}
		if err := al.persistSubagentEntry(p.SessionID, id, session.SystemSubtypeSubagentEnd, func(e *session.TranscriptEntry) {
			e.SubagentEnd = &frame
		}); err != nil {
			logger.WarnCF("agent", "steer: persist subagent_end failed",
				map[string]any{"session_id": p.SessionID, "error": err.Error()})
		}
	}
}

// deliverGoalVerdictUpward covers ADR-091 FR-B-017 (I-5, AS-12): when the
// Judge rules on a goal's criteria, the deciding verdict is delivered
// upward through steer.UpwardDeliverer as a goal_status SessionMessage —
// direction session_to_parent, condition not_met, one evidence item per
// judged criterion — the same one upward-delivery operation every other
// child outcome already goes through (I-5), not just the transcript/
// live-event pair every Judge verdict already gets regardless of audience
// (task_executor_judge.go::writeJudgeVerdictTranscript,
// goal_loop.go::writeGoalVerdictTranscript — this function is the shared
// body both call).
//
// sessionID with no steered parent, or with no active /goal record bound to
// it (activeGoalForSession, GOAL-FR-013's one session-bound lookup for both
// owner kinds), both no-op silently and are logged at Warn only for the
// missing-goal-record case — Deliver()'s own edge lookup already covers
// "no steering parent to deliver to" the same way every other Deliver call
// site in this package does (best-effort, never fails the caller).
func (al *AgentLoop) deliverGoalVerdictUpward(ctx context.Context, sessionID string, verdict *task.JudgeVerdict) {
	if verdict == nil || sessionID == "" {
		return
	}
	deliverer := al.getUpwardDeliverer()
	if deliverer == nil {
		return
	}
	rec := activeGoalForSession(sessionID)
	if rec == nil {
		logger.WarnCF("agent", "goal-status upward delivery skipped — no active goal record bound to this session",
			map[string]any{"component": "goal", "session_id": sessionID, "verdict_round": verdict.Round})
		return
	}
	evidence := make([]struct {
		Criterion *string `json:"criterion,omitempty"`
		Met       *bool   `json:"met,omitempty"`
		Note      *string `json:"note,omitempty"`
	}, len(verdict.PerCriterion))
	for i := range verdict.PerCriterion {
		c := verdict.PerCriterion[i]
		evidence[i].Criterion = &c.CriterionID
		evidence[i].Met = &c.Met
		evidence[i].Note = &c.Reason
	}
	var sm generated.SessionMessage
	condition := generated.SessionMessageGoalStatusConditionNotMet
	if verdict.Met {
		condition = generated.SessionMessageGoalStatusConditionMet
	}
	if err := sm.FromSessionMessageGoalStatus(generated.SessionMessageGoalStatus{
		Kind:            generated.SessionMessageGoalStatusKindGoalStatus,
		MessageId:       fmt.Sprintf("%s-verdict-%d", rec.GoalID, verdict.Round),
		SessionId:       sessionID,
		SenderIdentity:  verdict.JudgeAgentID,
		CreatedAt:       time.Now().UTC(),
		Depth:           1,
		GoalId:          rec.GoalID,
		Condition:       condition,
		Direction:       generated.SessionMessageGoalStatusDirectionSessionToParent,
		Evidence:        &evidence,
		UntrustedOrigin: false,
	}); err != nil {
		logger.WarnCF("agent", "goal-status upward delivery skipped — could not encode SessionMessageGoalStatus",
			map[string]any{"component": "goal", "session_id": sessionID, "goal_id": rec.GoalID, "error": err.Error()})
		return
	}
	if _, err := deliverer.Deliver(ctx, steer.UpwardEvent{
		ChildSessionID: sessionID, Outcome: steer.OutcomeGoalVerdict, Message: sm,
	}); err != nil {
		logger.WarnCF("agent", "goal-status upward delivery failed",
			map[string]any{"component": "goal", "session_id": sessionID, "goal_id": rec.GoalID, "error": err.Error()})
	}
}
