// websocket_forward.go: Forward agent events to the client as frames

package gateway

import (
	"encoding/json"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// ADR-091 UAT defect 2: the orphan watchdog (formerly orphanWatchdogTimeout /
// orphanWatchdogMaxRechecks / openSpanEntry / startOrphanWatchdog /
// synthesizeOrphanEnd / the rootTurnEnded latch — which the #823 catch-up
// redo had moved from this per-connection forwarder into the session hub,
// websocket_forward_hub_spans.go) is retired, not re-aimed. It existed for
// the pre-ADR-091 design, where a nested sub-turn's lifetime was
// structurally scoped to its parent's own turn. Under ADR-091 a delegated
// child is a session of its own (D1): it is DESIGNED to keep running after
// its parent's turn ends, and its real terminal state arrives independently
// via its own EventKindSubTurnEnd (pkg/agent/steer_frames.go's
// deliverSubagentEnd, driven by the child's own steer.Outcome — never
// guessed from the parent's turn).

// ADR-057 FR-089 — W5 audit classification artefact (U11's half).
//
// generated.SESSION_SCOPED_FRAME_TYPES has 19 members. 13 were classified by
// the spec itself (adr-057-session-unification-spec.md, BDD-16/BDD-98/BDD-99):
// class (a) both-ids — token, done, tool_call_start, tool_call_result,
// tool_approval_required, media; class (b) producing_session_id-absent —
// replay_message, session_started, session_close_ack, subagent_start,
// subagent_end; class (c) documented pre-existing gap — rate_limit,
// replay_done. The remaining 6 were left "class not yet assigned by the W5
// audit" on their generated types pending this classification, verified
// 2026-08 against this tree:
//
//   - agent_switched → class (a). Built at this file's ToolExecEnd case
//     (below, evtSID := p.SessionID from agent.ToolExecEndPayload) immediately
//     after a successful switch_agent tool_call_result (ADR-071 D4 merged
//     hand_off/return_to_default into this one tool) — the IDENTICAL payload
//     and session-id source as tool_call_result, which is already verified
//     class (a). A delegated child can invoke switch_agent on its own session
//     exactly as a root turn can, so evtSID is the child's own producing
//     session whenever that happens, distinct from the routing key. Stamped
//     alongside tool_call_result above.
//
//   - task_status_changed → class (b). Its only non-test construction site is
//     `TaskStatusChangedFrame{..., SessionId: p.SessionID, ...}` (this file's
//     EventKindTaskStatusChanged case), fed solely by
//     agent.TaskStatusChangedPayload, whose ONLY constructor is
//     pkg/agent/task_executor.go:1821-1830 (`sessionID := t.SessionID; ...
//     EmitTaskStatusChanged(TaskStatusChangedPayload{SessionID: sessionID,
//     ...})`) — the TaskExecutor (a system-level component) reporting a
//     scheduled task's OWN session lifecycle, not narration produced by a
//     delegated child turn. No stamping added; producing_session_id stays
//     absent.
//
//   - cancel_stage → class (b). Sole constructor is sendCancelStageFrame
//     (this file), called only with the id RequestCancel's CancelScope.SessionID
//     resolved to (pkg/agent/cancel.go:404-409,
//     `hooks.SendStageFrame(sessionID, "graceful")`) — the cancel machinery's
//     own target id, narrating the Stop's progress across the whole subtree it
//     cascades to (FR-032/W10c below), never a specific descendant's own
//     output. No stamping added.
//
//   - goal_status → class (b). Its only non-test construction site is this
//     file's EventKindGoalStatusChanged case (`SessionId: p.SessionID` from
//     agent.GoalStatusChangedPayload), whose constructors —
//     pkg/agent/goal_loop.go:502-514, several sites in
//     pkg/agent/goal_triggers.go, and pkg/agent/session_messaging_wire.go:602-606
//     /:626-630 — all pass the session that OWNS the /goal loop config being
//     reported, i.e. the session reporting on itself. No call site was found
//     where this payload's SessionID is a delegated child distinct from a
//     parent's routing id. No stamping added.
//
//   - loop_status → class (b). Its only non-test construction site is this
//     file's EventKindLoopStatusChanged case (`SessionId: p.SessionID` from
//     agent.LoopStatusChangedPayload), whose sole constructor
//     (pkg/agent/loop_command.go:301-309, `emitLoopStatusFrame(sessionID,
//     ...)`) reports on the session whose OWN /loop state changed — the
//     identical "reporting on itself" shape as goal_status. No stamping added.
//
//   - system_overload → COULD NOT DETERMINE; not guessed (spec line ~1309
//     forbids it). Verified: `rg -rl 'SystemOverloadFrame|system_overload'
//     pkg/` matches only the generated type
//     (pkg/api/generated/asyncapi_types.gen.go), its fixtures
//     (pkg/api/generated/fixtures.go), and the inbound-schema copy
//     (pkg/gateway/inboundschemas/SystemOverloadFrame.yaml) — ZERO
//     non-generated, non-fixture Go call site constructs or sends this frame
//     anywhere in pkg/gateway or pkg/agent. The type is fully specified on the
//     wire and consumed by the SPA (src/store/chat.ts, src/lib/ws.ts) but is
//     never produced by the backend, so there is no real emission site to
//     classify BY EVIDENCE. Do not assume a class for this type until a
//     producer exists and is audited.
//
// TokenFrame/DoneFrame (wsStreamer.Update/Finalize, below) need no stamping
// change: their shared "shadow stream" gate (isShadowStream forced true
// whenever parentSpawnCallID != "", in both Update and Finalize) means these
// two frames are constructed ONLY when parentSpawnCallID == "" — i.e. only
// for a turn that IS the routing session by definition (turn.go:406,
// session.RoutingSessionID's own contract: "for a root turn, RoutingSessionID
// MUST equal that turn's own SessionID"). SessionId already equals the
// routing key in every case either frame is actually emitted, so
// ProducingSessionId is correctly always absent.
//
// eventForwarder listens on the agent EventBus and forwards the frames that
// are NOT session-scoped conversation frames (global rate limits, WhatsApp
// pairing, notifications, task/plan/run status) to this one connection.
//
// #823 catch-up redesign: every session-scoped conversation frame — tool
// calls, errors, goal/loop/judge status, tool-result projections AND (as of
// Lane A step 1) sub-agent spans with their orphan watchdog — is produced
// exactly once per event by the EventBus sync tap and numbered through the
// session hub (websocket_forward_hub.go, websocket_forward_hub_spans.go),
// never once per connected tab here.
type eventForwardState struct {
	h  *WSHandler
	wc *wsConn
}

// chatID is kept in the signature for the connection's log identity at the
// call site (ServeHTTP) and existing callers; per-connection chat matching
// is no longer needed because no session-scoped frame is produced here.
func (h *WSHandler) eventForwarder(wc *wsConn, _ string, sub agent.EventSubscription, done chan<- struct{}) {
	defer close(done)
	f := &eventForwardState{h: h, wc: wc}

	for {
		evt, ok := <-sub.C
		if !ok {
			return
		}

		switch evt.Kind {
		case agent.EventKindRateLimit:
			// #823 catch-up redesign: session-scoped rate_limit now goes
			// through the hub sync tap (websocket_forward_hub.go's
			// hubRateLimit), exactly once per event regardless of tab
			// count. onRateLimit now handles ONLY global-scope events
			// (not tied to a session) — see its own doc comment.
			f.onRateLimit(evt)
		case agent.EventKindWhatsAppPairing:
			f.onWhatsAppPairing(evt)
		case agent.EventKindNotification:
			f.onNotification(evt)
		case agent.EventKindTaskStatusChanged:
			f.onTaskStatusChanged(evt)
		case agent.EventKindPlanStatusChanged:
			f.onPlanStatusChanged(evt)
		case agent.EventKindTaskRunStatus:
			f.onTaskRunStatus(evt)
		case agent.EventKindSubTurnSpawn, agent.EventKindSubTurnEnd,
			agent.EventKindSubagentMessage, agent.EventKindSubagentState,
			agent.EventKindToolExecStart, agent.EventKindToolExecEnd,
			agent.EventKindError, agent.EventKindGoalStatusChanged, agent.EventKindGoalOutcome,
			agent.EventKindJudgeVerdict, agent.EventKindLoopStatusChanged, agent.EventKindToolResultProjection:
			// #823 catch-up redesign (BE-DESIGN.md §1.2): these kinds are
			// now translated and delivered EXACTLY ONCE per event by the
			// EventBus sync tap (websocket_forward_hub.go's hubSyncTap
			// and its hubXxx handlers), not once per connected tab by
			// this per-connection forwarder — that per-connection
			// production was the root flaw the hub redesign closes (a
			// session with N tabs would otherwise translate, and once
			// hub-numbered, NUMBER, the same event N different ways).
			// The old f.onToolExecStart/onToolExecEnd/onError/
			// onGoalStatusChanged/onGoalOutcome/onJudgeVerdict/
			// onLoopStatusChanged/onToolResultProjection methods that used
			// to live in this file are DELETED, not just unwired —
			// golangci-lint's unused-code check flagged them as dead once
			// this case stopped calling them, and keeping unreachable code
			// around was worse than deleting it. Every test that used to
			// drive them through this per-connection path now calls
			// h.hubSyncTap directly instead (see the migrated test files
			// listed in SQUAD-REPORT-BEA.md). This case is explicitly
			// empty (not a silent unmatched-case fallthrough) so the
			// intent reads plainly at the call site.
		case agent.EventKindTurnStart, agent.EventKindTurnEnd,
			agent.EventKindLLMRequest, agent.EventKindLLMDelta, agent.EventKindLLMResponse,
			agent.EventKindLLMRetry, agent.EventKindContextCompress,
			agent.EventKindToolExecSkipped, agent.EventKindSteeringInjected, agent.EventKindFollowUpQueued,
			agent.EventKindInterruptReceived, agent.EventKindSubTurnResultDelivered, agent.EventKindSubTurnOrphan,
			agent.EventKindTurnTimeout, agent.EventKindEmptyResponseRetry, agent.EventKindCompactionRetry,
			agent.EventKindBackgroundProcessKill:
			// Not part of the live WS wire protocol — this forwarder only
			// translates the kinds handled above into browser frames.
			// EventKindTurnStart/TurnEnd joined this ignored list with the
			// ADR-091 UAT defect 2 fix: their only consumer here was the
			// retired orphan watchdog (see this file's top-of-file comment).
			// TurnEnd is consumed once per event by the hub sync tap instead
			// (websocket_forward_hub.go's hubTurnEnd), which emits no frame.
			// Behavior-preserving for every other kind here: previously
			// these fell through the switch unmatched (no default case
			// existed), which is a silent no-op identical to this explicit,
			// empty case.
		}
	}
}

// sessionIDForChat looks up the active session_id for a given chatID so every
// event frame can carry it, enabling per-session routing in the SPA.
func (f *eventForwardState) sessionIDForChat(evtChatID string) string {
	f.h.mu.Lock()
	sid := f.h.sessionIDs[evtChatID]
	if sid == "" {
		// Also check the task alias.
		if tid := f.h.taskChatIDs[evtChatID]; tid != "" {
			sid = f.h.sessionIDs[tid]
		}
	}
	f.h.mu.Unlock()
	return sid
}

// onToolExecStart forwards agent.EventKindToolExecStart to this connection.
// onToolExecEnd forwards agent.EventKindToolExecEnd to this connection.
// onRateLimit forwards agent.EventKindRateLimit to this connection.
func (f *eventForwardState) onRateLimit(evt agent.Event) {
	// SEC-26: forward rate-limit denials to the browser so the chat UI
	// can display an inline indicator. Global-scope events (daily cost
	// cap) are broadcast to every connection since they are not tied
	// to a specific chatID.
	//
	// #823 catch-up redesign: SESSION-scoped rate_limit denials now go
	// through the hub sync tap (websocket_forward_hub.go's hubRateLimit)
	// exactly once per event instead of once per connected tab — this
	// method now handles ONLY the global-scope branch, unchanged from
	// before, per-connection (a global event is not tied to any one
	// session, so it has no session hub to number it through).
	p, ok := evt.Payload.(agent.RateLimitPayload)
	if !ok || p.Scope != "global" {
		return
	}
	rateSID := p.SessionID
	if rateSID == "" {
		rateSID = f.sessionIDForChat(p.ChatID)
	}
	// Use generated.RateLimitFrame (contract-first migration).
	rateF := generated.RateLimitFrame{
		Type:              string(generated.WsFrameTypeRateLimit),
		SessionId:         rateSID,
		Scope:             p.Scope,
		Resource:          p.Resource,
		PolicyRule:        p.PolicyRule,
		RetryAfterSeconds: p.RetryAfterSeconds,
	}
	if p.AgentID != "" {
		aid := p.AgentID
		rateF.AgentId = &aid
	}
	if p.Tool != "" {
		tool := p.Tool
		rateF.Tool = &tool
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeRateLimit), rateF)
}

// onError forwards agent.EventKindError to this connection.
// onWhatsAppPairing forwards agent.EventKindWhatsAppPairing to this connection.
func (f *eventForwardState) onWhatsAppPairing(evt agent.Event) {
	// #283: WhatsApp linked-device pairing (QR + status). Not tied to a
	// chatID. Delivered only to connections that subscribed to this
	// channel's pairing UI (Option B), so the QR pairing secret isn't
	// broadcast to every connected tab.
	p, ok := evt.Payload.(agent.WhatsAppPairingPayload)
	if !ok {
		return
	}
	pairF := generated.WhatsAppPairingFrame{
		Type:      string(generated.WsFrameTypeWhatsappPairing),
		ChannelId: p.ChannelID,
		Status:    string(p.Status),
	}
	if p.QR != "" {
		qr := p.QR
		pairF.Qr = &qr
	}
	if p.Message != "" {
		msg := p.Message
		pairF.Message = &msg
	}
	// #368: maintain the per-channel QR cache so late subscribers (e.g.
	// a tab that opens the pairing UI after the first QR fires) receive
	// the last-seen code immediately on subscribe rather than waiting for
	// the next QR rotation.  Only "code" (QR available) is cached;
	// terminal states are evicted so stale QRs are not re-emitted.
	switch p.Status {
	case channels.PairingStatusCode:
		if frameBytes, merr := json.Marshal(pairF); merr == nil {
			f.h.lastPairingState.Store(p.ChannelID, frameBytes)
		} else {
			slog.Error("ws: failed to marshal whatsapp_pairing frame for cache",
				"channel_id", p.ChannelID, "error", merr)
		}
	case channels.PairingStatusLinked, channels.PairingStatusTimeout, channels.PairingStatusError,
		channels.PairingStatusWaiting:
		// PairingStatusWaiting and any other status that is not
		// "code" must not leave a stale QR in the cache — evict so a
		// late subscriber is not shown an outdated code.
		f.h.lastPairingState.Delete(p.ChannelID)
	default:
		// Any future status not yet in this switch: same fail-safe
		// eviction as above, so an unrecognized status never leaves
		// a stale QR behind.
		f.h.lastPairingState.Delete(p.ChannelID)
	}
	if !f.wc.wantsPairing(p.ChannelID) {
		return
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeWhatsappPairing), pairF)
}

// onNotification forwards agent.EventKindNotification to this connection.
func (f *eventForwardState) onNotification(evt agent.Event) {
	// #264: a user-facing notification (e.g. a scheduled run failed).
	// Delivered ONLY to the recipient user's connections (filtered by
	// wc.userID) so it never leaks to other tabs/sessions. The
	// NotificationAdminBroadcast sentinel fans out to every connected
	// client unconditionally when no specific recipient could be
	// resolved — under the single-user model, "broadcast to admins" and
	// "broadcast to the one account's connections" are the same thing.
	p, ok := evt.Payload.(agent.NotificationPayload)
	if !ok {
		return
	}
	if p.Recipient != agent.NotificationAdminBroadcast && f.wc.userID != p.Recipient {
		return
	}
	notifF := generated.NotificationFrame{
		Type:             string(generated.WsFrameTypeNotification),
		Id:               p.ID,
		NotificationType: p.NotificationType,
		Title:            p.Title,
		Severity:         p.Severity,
		Read:             p.Read,
		CreatedAtMs:      p.CreatedAtMs,
	}
	if p.Body != "" {
		body := p.Body
		notifF.Body = &body
	}
	if p.ScheduleID != "" {
		sid := p.ScheduleID
		notifF.ScheduleId = &sid
	}
	if p.SessionID != "" {
		ses := p.SessionID
		notifF.SessionId = &ses
	}
	if p.AgentID != "" {
		aid := p.AgentID
		notifF.AgentId = &aid
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeNotification), notifF)
}

// onTaskStatusChanged forwards agent.EventKindTaskStatusChanged to this connection.
func (f *eventForwardState) onTaskStatusChanged(evt agent.Event) {
	// A workflow task's status changed (queued→running→completed/failed).
	// Not tied to a specific chatID — broadcast to every connection so
	// anyone viewing the tasks board sees live updates. The SPA
	// invalidates its tasks TanStack Query cache on receipt.
	p, ok := evt.Payload.(agent.TaskStatusChangedPayload)
	if !ok {
		return
	}
	taskF := generated.TaskStatusChangedFrame{
		Type:      string(generated.WsFrameTypeTaskStatusChanged),
		SessionId: p.SessionID,
		TaskId:    p.TaskID,
		Status:    p.Status,
	}
	if p.AgentID != "" {
		aid := p.AgentID
		taskF.AgentId = &aid
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeTaskStatusChanged), taskF)
}

// onPlanStatusChanged forwards agent.EventKindPlanStatusChanged to this connection.
func (f *eventForwardState) onPlanStatusChanged(evt agent.Event) {
	// ADR-049 D4/D7: a Plan's state/phase/progress/paused_reason changed.
	// Not tied to a specific chatID (a Plan is workspace-scoped, not
	// session-scoped) — broadcast to every connection, mirroring
	// EventKindTaskStatusChanged above. The SPA invalidates its plans
	// query cache / updates the plan card on receipt.
	p, ok := evt.Payload.(agent.PlanStatusChangedPayload)
	if !ok {
		return
	}
	planF := generated.PlanStatusFrame{
		Type:      string(generated.WsFrameTypePlanStatus),
		PlanId:    p.PlanID,
		State:     p.State,
		PlanPhase: p.PlanPhase,
		Progress:  p.Progress,
	}
	if p.PausedReason != "" {
		pr := p.PausedReason
		planF.PausedReason = &pr
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypePlanStatus), planF)
}

// onGoalStatusChanged forwards agent.EventKindGoalStatusChanged to this connection.
// onGoalOutcome forwards agent.EventKindGoalOutcome to this connection.
// onJudgeVerdict forwards agent.EventKindJudgeVerdict to this connection.
// onLoopStatusChanged forwards agent.EventKindLoopStatusChanged to this connection.
// onTaskRunStatus forwards agent.EventKindTaskRunStatus to this connection.
func (f *eventForwardState) onTaskRunStatus(evt agent.Event) {
	// A per-execution run opened or closed (ADR-050). Broadcast so the
	// calendar's per-occurrence chip updates live without a full refetch.
	// occurrence_ms is nil for an ad-hoc/once/manual run.
	p, ok := evt.Payload.(agent.TaskRunStatusPayload)
	if !ok {
		return
	}
	runF := generated.TaskRunStatusFrame{
		Type:   string(generated.WsFrameTypeTaskRunStatus),
		TaskId: p.TaskID,
		RunId:  p.RunID,
		Status: p.Status,
	}
	if p.OccurrenceMs != nil {
		ms := *p.OccurrenceMs
		runF.OccurrenceMs = &ms
	}
	sendConnGenFrame(f.wc, string(generated.WsFrameTypeTaskRunStatus), runF)
}

// onToolResultProjection forwards agent.EventKindToolResultProjection to this connection.
