// loop_events.go: Event bus and hooks

package agent

import (
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// MountHook registers an in-process hook on the agent loop.
func (al *AgentLoop) MountHook(reg HookRegistration) error {
	if al == nil || al.hooks == nil {
		return fmt.Errorf("hook manager is not initialized")
	}
	return al.hooks.Mount(reg)
}

// UnmountHook removes a previously registered in-process hook.
func (al *AgentLoop) UnmountHook(name string) {
	if al == nil || al.hooks == nil {
		return
	}
	al.hooks.Unmount(name)
}

// SubscribeEvents registers a subscriber for agent-loop events.
func (al *AgentLoop) SubscribeEvents(buffer int) EventSubscription {
	if al == nil || al.eventBus == nil {
		ch := make(chan Event)
		close(ch)
		return EventSubscription{C: ch}
	}
	return al.eventBus.Subscribe(buffer)
}

// UnsubscribeEvents removes a previously registered event subscriber.
func (al *AgentLoop) UnsubscribeEvents(id uint64) {
	if al == nil || al.eventBus == nil {
		return
	}
	al.eventBus.Unsubscribe(id)
}

// SetEventSyncTap installs (or, with nil, clears) a synchronous tap on the
// underlying EventBus — see EventBus.SetSyncTap's doc comment for the full
// #823 catch-up-redesign rationale (SetSyncTap runs once per Emit, before
// the lossy per-subscriber fan-out, so a session-numbering hub observes
// 100% of events regardless of subscriber backpressure). Exposed here,
// mirroring SubscribeEvents/UnsubscribeEvents' exact nil-safety pattern,
// because the unexported eventBus field is unreachable from outside this
// package.
//
// CROSS-LANE NOTE (#823 squad, Lane A / Lane B split): this file is owned
// by Lane B (pkg/agent). Lane A (pkg/gateway) added this one method to call
// the SetSyncTap mechanism Lane B already landed specifically for this
// purpose (see EventBus.SetSyncTap's own doc comment: "so the gateway's
// future per-session hub can observe 100% of events"). Flagged per the
// squad brief's cross-lane-edit rule; see Lane A's SQUAD-REPORT-BEA.md.
func (al *AgentLoop) SetEventSyncTap(tap func(Event)) {
	if al == nil || al.eventBus == nil {
		return
	}
	al.eventBus.SetSyncTap(tap)
}

// EventDrops returns the number of dropped events for the given kind.
func (al *AgentLoop) EventDrops(kind EventKind) int64 {
	if al == nil || al.eventBus == nil {
		return 0
	}
	return al.eventBus.Dropped(kind)
}

type turnEventScope struct {
	agentID    string
	sessionKey string
	turnID     string
}

func (al *AgentLoop) newTurnEventScope(agentID, sessionKey string) turnEventScope {
	seq := al.turnSeq.Add(1)
	return turnEventScope{
		agentID:    agentID,
		sessionKey: sessionKey,
		turnID:     fmt.Sprintf("%s-turn-%d", agentID, seq),
	}
}

func (ts turnEventScope) meta(iteration int, source, tracePath string) EventMeta {
	return EventMeta{
		AgentID:    ts.agentID,
		TurnID:     ts.turnID,
		SessionKey: ts.sessionKey,
		Iteration:  iteration,
		Source:     source,
		TracePath:  tracePath,
	}
}

func (al *AgentLoop) emitEvent(kind EventKind, meta EventMeta, payload any) {
	evt := Event{
		Kind:    kind,
		Meta:    meta,
		Payload: payload,
	}

	if al == nil || al.eventBus == nil {
		return
	}

	al.logEvent(evt)

	al.eventBus.Emit(evt)
}

// EmitWhatsAppPairing publishes a WhatsApp native/QR pairing update (QR code or
// status) onto the event bus so every connected SPA WebSocket client receives a
// whatsapp_pairing frame (#283). Safe to call from a channel's own goroutine —
// the bus drops to a full subscriber rather than blocking. Wired into the
// WhatsApp native channel at gateway boot via SetPairingObserver.
func (al *AgentLoop) EmitWhatsAppPairing(channelID string, status channels.PairingStatus, qr, message string) {
	al.emitEvent(EventKindWhatsAppPairing, EventMeta{Source: "channel"}, WhatsAppPairingPayload{
		ChannelID: channelID,
		Status:    status,
		QR:        qr,
		Message:   message,
	})
	// FR-111 (#358): audit-log device-pairing lifecycle transitions so linking a new
	// WhatsApp device leaves a tamper-evident trail. We deliberately do NOT log the
	// `code`/`connecting` states (high-frequency, and the QR itself is a scannable
	// secret that must never reach the audit file) — only the terminal outcomes.
	// Decision uses the audit vocabulary (allow=linked, error=failed/expired); the
	// exact pairing status rides in Details.
	switch status {
	case channels.PairingStatusLinked, channels.PairingStatusError, channels.PairingStatusTimeout:
		decision := audit.DecisionAllow
		if status != channels.PairingStatusLinked {
			decision = audit.DecisionError
		}
		audit.EmitEntry(al.auditLogger, &audit.Entry{
			Timestamp: time.Now().UTC(),
			Event:     audit.EventChannelPairing,
			Decision:  decision,
			Details: map[string]any{
				"channel": channelID,
				"status":  string(status),
				"message": message,
			},
		})
	}
}

// EmitNotification publishes a user-facing notification onto the event bus so
// the recipient's SPA WebSocket connections receive a notification frame (#264).
// The WS forwarder filters delivery by Recipient (==wsConn.userID), so the
// payload is not broadcast to every authenticated tab. Safe to call from any
// goroutine — the bus drops to a full subscriber rather than blocking.
func (al *AgentLoop) EmitNotification(p NotificationPayload) {
	al.emitEvent(EventKindNotification, EventMeta{Source: "schedule"}, p)
}

// EmitTaskStatusChanged publishes a workflow task status transition onto the
// event bus so every connected SPA WebSocket client receives a
// task_status_changed frame (the SPA invalidates its tasks query cache on
// receipt). Safe to call from any goroutine — the bus drops to a full
// subscriber rather than blocking.
func (al *AgentLoop) EmitTaskStatusChanged(p TaskStatusChangedPayload) {
	al.emitEvent(EventKindTaskStatusChanged, EventMeta{AgentID: p.AgentID, Source: "task_executor"}, p)
}

// EmitPlanStatusChanged publishes a Plan state/phase/progress transition onto
// the event bus so every connected SPA WebSocket client receives a
// plan_status frame (ADR-049 D4/D7, spec Part B R3). Safe to call from any
// goroutine — the bus drops to a full subscriber rather than blocking. The
// production emission path is pkg/plan.Store.OnChange, wired at gateway boot
// (setupAndStartServices) to call this after every successful plan
// Create/Update — see that wiring for why this is the single choke point for
// both the plan engine's and the gateway REST layer's mutations.
func (al *AgentLoop) EmitPlanStatusChanged(p PlanStatusChangedPayload) {
	al.emitEvent(EventKindPlanStatusChanged, EventMeta{Source: "plan_engine"}, p)
}

// EmitGoalStatusChanged publishes a `/goal` loop status transition onto the
// event bus so every connected SPA WebSocket client receives a goal_status
// frame (ADR-049 D6/D7, spec Part B US-8). Safe to call from any goroutine.
func (al *AgentLoop) EmitGoalStatusChanged(p GoalStatusChangedPayload) {
	al.emitEvent(EventKindGoalStatusChanged, EventMeta{Source: "goal_loop"}, p)
}

// EmitLoopStatusChanged publishes a `/loop` status transition onto the event
// bus so every connected SPA WebSocket client receives a loop_status frame
// (ADR-049 D6/D7, spec Part B US-9). Safe to call from any goroutine.
func (al *AgentLoop) EmitLoopStatusChanged(p LoopStatusChangedPayload) {
	al.emitEvent(EventKindLoopStatusChanged, EventMeta{Source: "goal_loop"}, p)
}

// EmitTaskRunStatus publishes a per-execution TaskRun open/close transition
// (ADR-050 §3.8) onto the event bus so every connected SPA WebSocket client
// receives a task_run_status frame — additive alongside EmitTaskStatusChanged
// (see EventKindTaskRunStatus's own doc comment for why a separate event is
// needed: a recurring occurrence's run transitions do not move Task.status
// between distinct values). Safe to call from any goroutine — the bus drops
// to a full subscriber rather than blocking.
func (al *AgentLoop) EmitTaskRunStatus(p TaskRunStatusPayload) {
	al.emitEvent(EventKindTaskRunStatus, EventMeta{Source: "task_executor"}, p)
}

func cloneEventArguments(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(args))
	for k, v := range args {
		cloned[k] = v
	}
	return cloned
}

func (al *AgentLoop) logEvent(evt Event) {
	fields := map[string]any{
		"event_kind":  evt.Kind.String(),
		"agent_id":    evt.Meta.AgentID,
		"turn_id":     evt.Meta.TurnID,
		"session_key": evt.Meta.SessionKey,
		"iteration":   evt.Meta.Iteration,
	}

	if evt.Meta.TracePath != "" {
		fields["trace"] = evt.Meta.TracePath
	}
	if evt.Meta.Source != "" {
		fields["source"] = evt.Meta.Source
	}

	switch payload := evt.Payload.(type) {
	case TurnStartPayload:
		fields["channel"] = payload.Channel
		fields["chat_id"] = payload.ChatID
		fields["user_len"] = len(payload.UserMessage)
		fields["media_count"] = payload.MediaCount
	case TurnEndPayload:
		fields["status"] = payload.Status
		fields["iterations_total"] = payload.Iterations
		fields["duration_ms"] = payload.Duration.Milliseconds()
		fields["final_len"] = payload.FinalContentLen
	case LLMRequestPayload:
		fields["model"] = payload.Model
		fields["messages"] = payload.MessagesCount
		fields["tools"] = payload.ToolsCount
		fields["max_tokens"] = payload.MaxTokens
	case LLMDeltaPayload:
		fields["content_delta_len"] = payload.ContentDeltaLen
		fields["reasoning_delta_len"] = payload.ReasoningDeltaLen
	case LLMResponsePayload:
		fields["content_len"] = payload.ContentLen
		fields["tool_calls"] = payload.ToolCalls
		fields["has_reasoning"] = payload.HasReasoning
	case LLMRetryPayload:
		fields["attempt"] = payload.Attempt
		fields["max_retries"] = payload.MaxRetries
		fields["reason"] = payload.Reason
		fields["error"] = payload.Error
		fields["backoff_ms"] = payload.Backoff.Milliseconds()
	case ContextCompressPayload:
		fields["reason"] = payload.Reason
		fields["dropped_messages"] = payload.DroppedMessages
		fields["remaining_messages"] = payload.RemainingMessages
	case ToolExecStartPayload:
		fields["tool"] = payload.Tool
		fields["args_count"] = len(payload.Arguments)
	case ToolExecEndPayload:
		fields["tool"] = payload.Tool
		fields["duration_ms"] = payload.Duration.Milliseconds()
		fields["for_llm_len"] = payload.ForLLMLen
		fields["for_user_len"] = payload.ForUserLen
		fields["is_error"] = payload.IsError
		fields["async"] = payload.Async
	case ToolExecSkippedPayload:
		fields["tool"] = payload.Tool
		fields["reason"] = payload.Reason
	case SteeringInjectedPayload:
		fields["count"] = payload.Count
		fields["total_content_len"] = payload.TotalContentLen
	case FollowUpQueuedPayload:
		fields["source_tool"] = payload.SourceTool
		fields["channel"] = payload.Channel
		fields["chat_id"] = payload.ChatID
		fields["content_len"] = payload.ContentLen
	case InterruptReceivedPayload:
		fields["interrupt_kind"] = payload.Kind
		fields["role"] = payload.Role
		fields["content_len"] = payload.ContentLen
		fields["queue_depth"] = payload.QueueDepth
		fields["hint_len"] = payload.HintLen
	case SubTurnSpawnPayload:
		fields["child_agent_id"] = payload.AgentID
		fields["label"] = payload.Label
	case SubTurnEndPayload:
		fields["child_agent_id"] = payload.AgentID
		fields["status"] = payload.Status
	case SubTurnResultDeliveredPayload:
		fields["target_channel"] = payload.TargetChannel
		fields["target_chat_id"] = payload.TargetChatID
		fields["content_len"] = payload.ContentLen
	case ErrorPayload:
		fields["stage"] = payload.Stage
		fields["error"] = payload.Message
	}

	logger.InfoCF("eventbus", fmt.Sprintf("Agent event: %s", evt.Kind.String()), fields)
}
