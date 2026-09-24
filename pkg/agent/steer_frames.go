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
// All four emitters live in this file; their production CALLERS do not, and
// they are not the same caller for start and end:
//
//   - subagent_start — deliverSubagentStart, called by the launcher
//     (steer_launcher.go::SteerLauncher.publishSteeredLaunch, from Launch).
//   - subagent_state — deliverSubagentState, called by the launcher
//     (publishSteeredLaunch, and dispatchSteeredSessionWithReservation
//     twice), by steer_cancel.go::WriteSteerRevivalState, and by
//     steer_audience.go::SteerUpwardDeliverer.Deliver.
//   - subagent_message — deliverSubagentMessage, called by
//     steer_audience.go::SteerUpwardDeliverer.Deliver.
//   - subagent_end — deliverSubagentEnd, whose ONLY production caller is
//     steer_audience.go::SteerUpwardDeliverer.Deliver, NOT the launcher.
//     Debugging a missing subagent_end starts there, not in
//     steer_launcher.go.
//
// These remain the only production emitters of
// EventKindSubTurnSpawn/EventKindSubTurnEnd.
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
		EventMeta{TracePath: "steer.launch", SessionKey: parentSessionID},
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
		EventMeta{TracePath: "steer.complete", SessionKey: parentSessionID},
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
		Type:      string(generated.WsFrameTypeSubagentMessage),
		MessageId: id,
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
		EventMeta{TracePath: "subagent.message", SessionKey: parentSessionID},
		SubagentMessagePayload{SessionID: parentSessionID, MessageID: id, Frame: frame},
	)
}

// steeringReceipt carries the two facts SubagentStateFrame.yaml's
// steering_receipt requires (correlation_id, applied_at) from the injection
// site (loop_run_turn.go) to deliverSubagentState below — issue #870.
// appliedAt is stamped by the caller at the moment of injection, never at
// enqueue: the field means the steer was APPLIED, and a receipt stamped
// earlier would misreport a steer that is still queued, or one dropped
// because the session stopped before its next round, as already applied.
type steeringReceipt struct {
	correlationID string
	appliedAt     time.Time
}

// deliverSubagentState persists then emits ONE subagent_state frame
// (ADR-091 D7/I-4) reporting childSessionID's lifecycle transition to its
// steering session's side panel. receipt is nil on every ordinary lifecycle
// ping; non-nil only for the ADR-091/issue-#870 steering_receipt case (see
// deliverSteeringReceiptsForInjection below, the function's own SIXTH call
// site and the only one that ever passes a non-nil receipt).
//
// Mid-flight transitions ARE emitted. The four ordinary-lifecycle callers:
//
//   - steer_launcher.go::SteerLauncher.publishSteeredLaunch (from Launch) —
//     `queued` at creation.
//   - steer_launcher.go::dispatchSteeredSessionWithReservation — `running`,
//     at both of its admission sites (the task-origin branch handed to
//     taskExecutor.dispatchLaunchedTask, and the ordinary
//     reconstruct-then-run branch).
//   - steer_cancel.go::WriteSteerRevivalState — `running` on revival.
//   - steer_audience.go::SteerUpwardDeliverer.Deliver — the TERMINAL state,
//     where state follows the wake-eligibility decision rather than every
//     non-terminal report.
//
// subagent_end (the existing EventKindSubTurnEnd mechanism, not this file's)
// follows the terminal state transition, per I-4's ordering: "subagent_end
// follows the terminal subagent_state".
//
// An earlier version of this comment described the mid-flight gap as open
// and said no launcher or canceller existed to hook. Both now exist and both
// call this function; do not re-implement a second emitter for transitions
// this one already reports — the SAME rule is why the steering_receipt
// (issue #870) rides this existing builder via an added parameter rather
// than a second SubagentStateFrame builder of its own.
func (al *AgentLoop) deliverSubagentState(parentSessionID string, childRec *session.LifecycleRecord, state string, receipt *steeringReceipt) {
	if al == nil || parentSessionID == "" || childRec == nil || childRec.Origin == nil || childRec.Origin.CallID == "" {
		return
	}
	originCallID := childRec.Origin.CallID
	id := fmt.Sprintf("%s:%d:state:%s", originCallID, childRec.Generation, state)
	if receipt != nil {
		// A single injected round can carry more than one applied steer
		// (three queued messages -> three receipts, per issue #870's design
		// note): distinguish each frame's persisted transcript id by the
		// correlation id it reports, or the second and third would collide
		// on the SAME id (originCallID+generation+state alone repeats
		// per-message) and silently overwrite one another on replay.
		id = fmt.Sprintf("%s:receipt:%s", id, receipt.correlationID)
	}
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
	if receipt != nil && receipt.correlationID != "" {
		// Absent, never null (SubagentStateFrame.yaml's own comment): this
		// is an omitempty POINTER, left nil on every frame that carries no
		// receipt, so the field is present or absent on the wire — never an
		// explicit JSON null. Marking it nullable in the schema instead
		// broke `tsc -b --noEmit` (ADR-091 UAT defect 1); do not "fix" this
		// by making the pointer field non-optional or by touching the
		// schema.
		frame.SteeringReceipt = &struct {
			AppliedAt     string `json:"applied_at"`
			CorrelationId string `json:"correlation_id"`
		}{
			AppliedAt:     receipt.appliedAt.UTC().Format(time.RFC3339),
			CorrelationId: receipt.correlationID,
		}
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
		EventMeta{TracePath: "subagent.state", SessionKey: parentSessionID},
		SubagentStatePayload{SessionID: parentSessionID, MessageID: id, Frame: frame},
	)
}

// deliverSteeringReceiptsForInjection is loop_run_turn.go's sole hook into
// issue #870's steering_receipt: called immediately after
// EventKindSteeringInjected fires for a round that actually injected
// messages, so it runs once per round, never on enqueue. childSessionKey is
// the CHILD's own session id (steer_reconstruct.go's SessionKey:
// rec.SessionID convention — the same key runTurn registers the turn
// under); correlationIDs is loop_run_turn_types.go's own
// agentLoopRunTurnIteration.pendingSteeringReceipts, parallel to the
// messages that were just injected, "" for any entry with no correlation id
// (SubTurn results, plain chat turns).
//
// A no-op — deliberately, not defensively — for every session that is not
// a steered child: steerParentSessionID(rec) returns "" for a session with
// no SteeredBy edge, and deliverSubagentState's own guard (parentSessionID
// == "") refuses to emit anything for it. This is how a plain chat turn
// that happens to reuse the shared steering queue (DeliverSessionMessage's
// SessionMessage kind=steer/respond path) never manufactures a receipt with
// nowhere to be delivered.
func (al *AgentLoop) deliverSteeringReceiptsForInjection(childSessionKey string, correlationIDs []string) {
	if al == nil || childSessionKey == "" {
		return
	}
	haveAny := false
	for _, id := range correlationIDs {
		if id != "" {
			haveAny = true
			break
		}
	}
	if !haveAny {
		return
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(childSessionKey)
	if err != nil || rec == nil {
		return
	}
	parentSessionID := steerParentSessionID(rec)
	if parentSessionID == "" {
		return
	}
	// One shared instant for the whole batch: every message in this slice
	// was injected together, into the SAME round, by the SAME call to
	// ri.messages = append(...) just above in loop_run_turn.go — they are
	// all genuinely applied at this one moment, not at N slightly different
	// times.
	appliedAt := time.Now().UTC()
	for _, id := range correlationIDs {
		if id == "" {
			continue
		}
		al.deliverSubagentState(parentSessionID, rec, string(session.LifecycleRunning),
			&steeringReceipt{correlationID: id, appliedAt: appliedAt})
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
	event := steer.UpwardEvent{ChildSessionID: sessionID, Outcome: steer.OutcomeGoalVerdict, Message: sm}
	delivery, err := deliverer.Deliver(ctx, event)
	if err != nil {
		logger.WarnCF("agent", "goal-status upward delivery failed",
			map[string]any{"component": "goal", "session_id": sessionID, "goal_id": rec.GoalID, "error": err.Error()})
		return
	}
	// [ADR-091 fix lane RX-OUTCOME, HIGH] A goal verdict is wake-eligible
	// (`goal_status`, I-5), and it is the only way a parent learns the Judge
	// ruled on its child's claim. Stored-not-woken means the parent sits on
	// an un-adjudicated child indefinitely — reported, never discarded.
	parentSessionID, generation := steerDeliveryEdge(al.GetSessionLifecycleStore(), sessionID)
	reportUndeliveredWake("steer: goal verdict", event, parentSessionID, generation, delivery)
}
