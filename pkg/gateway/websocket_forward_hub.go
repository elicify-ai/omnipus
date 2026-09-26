// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// websocket_forward_hub.go: #823 catch-up redesign — the EventBus sync tap
// (BE-DESIGN.md §1.3) that replaces websocket_forward.go's per-connection
// eventForwarder for every SESSION-SCOPED conversation frame. This is the
// fix for the "per-connection production" root flaw the pre-#823 forwarder
// had: eventForwarder subscribes to the EventBus once PER CONNECTION, so a
// session with N attached tabs independently translated and (if it also
// numbered) would have numbered the SAME event N different ways. The sync
// tap runs exactly once per Emit call, regardless of how many tabs are
// attached, translates the event into a frame exactly once, numbers it
// exactly once through the session's hub, and delivers the identical
// seq-stamped bytes to every connection currently resolved for that
// session.
package gateway

import (
	"encoding/json"
	"strconv"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// hubSyncTap is the function installed via AgentLoop.SetEventSyncTap
// (pkg/agent/loop_events.go). It runs synchronously on the emitting
// goroutine for EVERY event, before the lossy per-subscriber fan-out
// (EventBus.Emit's own doc comment) — see this file's header for why that
// matters. Only event kinds that produce a session-scoped CONVERSATION
// frame are handled here; every other kind (whatsapp_pairing, notification,
// task_status_changed, plan_status_changed, task_run_status — the "not
// sequenced" list, BE-DESIGN.md §1.2) is untouched and stays on the
// per-connection eventForwarder path.
func (h *WSHandler) hubSyncTap(evt agent.Event) {
	switch evt.Kind {
	case agent.EventKindTurnEnd:
		h.hubTurnEnd(evt)
	case agent.EventKindSubTurnSpawn:
		h.hubSubTurnSpawn(evt)
	case agent.EventKindSubTurnEnd:
		h.hubSubTurnEnd(evt)
	case agent.EventKindSubagentMessage:
		h.hubSubagentMessage(evt)
	case agent.EventKindSubagentState:
		h.hubSubagentState(evt)
	case agent.EventKindToolExecStart:
		h.hubToolExecStart(evt)
	case agent.EventKindToolExecEnd:
		h.hubToolExecEnd(evt)
	case agent.EventKindRateLimit:
		h.hubRateLimit(evt)
	case agent.EventKindError:
		h.hubError(evt)
	case agent.EventKindToolResultProjection:
		h.hubToolResultProjection(evt)
	case agent.EventKindGoalStatusChanged:
		h.hubGoalStatusChanged(evt)
	case agent.EventKindGoalOutcome:
		h.hubGoalOutcome(evt)
	case agent.EventKindJudgeVerdict:
		h.hubJudgeVerdict(evt)
	case agent.EventKindLoopStatusChanged:
		h.hubLoopStatusChanged(evt)
	}
}

// hubResolveSessionIDForChat mirrors eventForwardState.sessionIDForChat
// (websocket_forward.go) exactly, as a WSHandler-level (not
// connection-level) helper: the durable session_id for a given chatID,
// following the task-chat alias when the direct lookup misses.
func (h *WSHandler) hubResolveSessionIDForChat(chatID string) string {
	h.mu.Lock()
	sid := h.sessionIDs[chatID]
	if sid == "" {
		if tid := h.taskChatIDs[chatID]; tid != "" {
			sid = h.sessionIDs[tid]
		}
	}
	h.mu.Unlock()
	return sid
}

// hubPublishAndDeliver numbers frame through sessionID's hub, which appends
// the seq-stamped bytes to the ordered queue of every connection bound to
// that session (BE-DESIGN.md §1.1/§1.2) — one publish regardless of how many
// tabs are attached, and the only way a session-scoped frame reaches a
// connection. A no-op when sessionID is empty (nothing to number against)
// or the registry is unset (a bare test fixture).
func (h *WSHandler) hubPublishAndDeliver(sessionID, frameType string, frame []byte) {
	h.hubPublishMetaAlsoTo(sessionID, frameType, hubFrameMeta{}, frame, nil)
}

// hubPublishAndDeliverAlsoTo is hubPublishAndDeliver plus BE-DESIGN.md
// §1.4's alsoUnsequencedTo=conn rule: when alsoTo is non-nil and is NOT
// bound to sessionID, it additionally receives the frame WITHOUT a seq.
// This is for the one connection that must see a frame even while it is not
// (yet, or any more) bound to the frame's session — the tab that sent a
// message or pressed Stop. An unsequenced frame never moves a client's
// cursor (§6.2), so the extra copy can never create a gap or a duplicate
// position.
func (h *WSHandler) hubPublishAndDeliverAlsoTo(sessionID, frameType string, frame []byte, alsoTo *wsConn) {
	h.hubPublishMetaAlsoTo(sessionID, frameType, hubFrameMeta{}, frame, alsoTo)
}

// hubPublishMetaAlsoTo is the one publish entry point every producer in
// this package goes through; meta tells the hub how the frame affects the
// active-turn projection (ws_hub_projection.go).
func (h *WSHandler) hubPublishMetaAlsoTo(sessionID, frameType string, meta hubFrameMeta, frame []byte, alsoTo *wsConn) {
	if sessionID == "" || h.hubs == nil {
		if alsoTo != nil {
			sendRawFrameBytes(alsoTo, frameType, frame)
		}
		return
	}
	h.hubs.getOrCreate(sessionID).publishMeta(meta, frame)
	if meta.turnID != "" && meta.kind != hubKindToken {
		// Token items come only from a web streamer, whose own done (or
		// abandoned-turn release) clears them; the rest may belong to a turn
		// with no streamer at all — see hubTurnEnd.
		h.hubs.noteTurnSession(meta.turnID, sessionID)
	}
	if alsoTo != nil && !h.connBoundToSession(alsoTo, sessionID) {
		sendRawFrameBytes(alsoTo, frameType, frame)
	}
}

// hubPublishFrame marshals a generated frame struct and publishes it through
// sessionID's hub via hubPublishAndDeliverAlsoTo. Marshal failures are
// logged, never panicked on — a frame that cannot be encoded is a
// programming error, and the turn that produced it must keep running.
func (h *WSHandler) hubPublishFrame(sessionID, frameType string, frame any, alsoTo *wsConn) {
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal frame for hub failed", "type", frameType, "session_id", sessionID, "error", err)
		return
	}
	h.hubPublishAndDeliverAlsoTo(sessionID, frameType, data, alsoTo)
}

// hubPublishFrameMeta marshals a generated frame and publishes it through
// sessionID's hub with its projection meta.
func (h *WSHandler) hubPublishFrameMeta(sessionID, frameType string, meta hubFrameMeta, frame any) {
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal frame for hub failed", "type", frameType, "session_id", sessionID, "error", err)
		return
	}
	h.hubPublishMetaAlsoTo(sessionID, frameType, meta, data, nil)
}

// hubBroadcastWithSequencedCopy implements BE-DESIGN.md §1.4's
// alsoUnsequencedTo=broadcastOthers rule for goal/loop status, goal outcome,
// and judge verdict frames: every tab sees them, as before, but only the
// tabs bound to the frame's own session get the numbered, journaled copy;
// every other connection gets the identical bytes WITHOUT a seq (§6.2: "no
// seq -> apply as state, cursor untouched"), so a tab on another session
// never moves this session's cursor and still receives the numbered
// original through catch-up when it attaches later.
func (h *WSHandler) hubBroadcastWithSequencedCopy(sessionID, frameType string, frame []byte) {
	if sessionID != "" && h.hubs != nil {
		h.hubs.getOrCreate(sessionID).publishBytes(frame)
	}
	h.mu.Lock()
	others := make([]*wsConn, 0, len(h.sessions))
	for _, wc := range h.sessions {
		if sessionID != "" && wc.boundHub != nil && wc.boundHub.id == sessionID {
			continue
		}
		others = append(others, wc)
	}
	h.mu.Unlock()
	for _, conn := range others {
		sendRawFrameBytes(conn, frameType, frame)
	}
}

// ---------------------------------------------------------------------
// Tool exec start/end + agent_switched
// ---------------------------------------------------------------------

func (h *WSHandler) hubToolExecStart(evt agent.Event) {
	p, ok := evt.Payload.(agent.ToolExecStartPayload)
	if !ok {
		return
	}
	startSID := p.SessionID
	if startSID == "" {
		startSID = h.hubResolveSessionIDForChat(p.ChatID)
	}
	if startSID == "" {
		return
	}
	startArgs := p.Arguments
	if startArgs == nil {
		startArgs = map[string]any{}
	}
	startF := generated.ToolCallStartFrame{
		Type:      string(generated.WsFrameTypeToolCallStart),
		SessionId: startSID,
		CallId:    string(p.ToolCallID),
		Tool:      p.Tool,
		Params:    startArgs,
	}
	if p.AgentID != "" {
		aid := p.AgentID
		startF.AgentId = &aid
	}
	if p.ParentSpawnCallID != "" {
		pc := string(p.ParentSpawnCallID)
		startF.ParentCallId = &pc
	}
	// ADR-091 D7: the payload's session ID is the tool-producing session's
	// own identity (producing_session_id is retired).
	data, err := json.Marshal(startF)
	if err != nil {
		logsafeError("ws: marshal tool_call_start for hub failed", "session_id", startSID, "error", err)
		return
	}
	h.hubPublishMetaAlsoTo(startSID, string(generated.WsFrameTypeToolCallStart),
		hubFrameMeta{kind: hubKindToolStart, key: string(p.ToolCallID), turnID: evt.Meta.TurnID}, data, nil)
}

func (h *WSHandler) hubToolExecEnd(evt agent.Event) {
	p, ok := evt.Payload.(agent.ToolExecEndPayload)
	if !ok {
		return
	}
	status := "success"
	if p.IsError {
		status = "error"
	}
	evtSID := p.SessionID
	if evtSID == "" {
		evtSID = h.hubResolveSessionIDForChat(p.ChatID)
	}
	if evtSID == "" {
		return
	}
	var liveResult any = p.Result
	var structuredErr string
	if status == "error" {
		if obj, reason, isStructured := parseStructuredToolFailure(p.Result); isStructured {
			liveResult = obj
			structuredErr = reason
		}
	}
	if liveResult == any(p.Result) && len(p.Result) > InlineToolResultMaxBytes {
		if encoded, merr := json.Marshal(p.Result); merr == nil {
			if sentinel, offloaded := maybeOffloadResult(h.toolStore, evtSID, encoded); offloaded {
				liveResult = sentinel
			}
		}
	}
	resultF := generated.ToolCallResultFrame{
		Type:      string(generated.WsFrameTypeToolCallResult),
		SessionId: evtSID,
		CallId:    string(p.ToolCallID),
		Tool:      p.Tool,
		Result:    liveResult,
		Status:    status,
	}
	if p.Duration != 0 {
		dm := int(p.Duration.Milliseconds())
		resultF.DurationMs = &dm
	}
	if p.AgentID != "" {
		aid := p.AgentID
		resultF.AgentId = &aid
	}
	if p.ParentSpawnCallID != "" {
		pc := string(p.ParentSpawnCallID)
		resultF.ParentCallId = &pc
	}
	switch {
	case structuredErr != "":
		se := truncateRunesForFrame(structuredErr, maxLiveErrorChars)
		resultF.Error = &se
	case status == "error" && p.Result != "" && liveResult != any(p.Result):
		liveErr := truncateRunesForFrame(p.Result, maxLiveErrorChars)
		resultF.Error = &liveErr
	}
	// ADR-091 D7: the payload's session ID is the tool-producing session's
	// own identity (producing_session_id is retired).
	data, err := json.Marshal(resultF)
	if err != nil {
		logsafeError("ws: marshal tool_call_result for hub failed", "session_id", evtSID, "error", err)
		return
	}
	h.hubPublishMetaAlsoTo(evtSID, string(generated.WsFrameTypeToolCallResult),
		hubFrameMeta{kind: hubKindToolResult, key: string(p.ToolCallID), turnID: evt.Meta.TurnID}, data, nil)

	if p.Tool == "switch_agent" && status == "success" {
		h.hubEmitAgentSwitched(evtSID)
	}
}

// hubEmitAgentSwitched mirrors the agent_switched construction embedded in
// the old per-connection onToolExecEnd (websocket_forward.go) verbatim —
// see that function's history for the ADR-071 §5.2.1/§5.2.2 rationale this
// reproduces unchanged.
func (h *WSHandler) hubEmitAgentSwitched(evtSID string) {
	defaultAgent := h.agentLoop.GetRegistry().GetDefaultAgent()
	var defaultName string
	if defaultAgent != nil {
		defaultName = defaultAgent.Name
	}
	activeAgent, activeOk := h.agentLoop.GetSessionActiveAgent(evtSID)
	toDefault, sawToDefault := h.agentLoop.GetLastSwitchToDefault(evtSID)
	if !activeOk {
		logsafeWarn("websocket: switch_agent succeeded but no active agent found for session",
			"session_id", evtSID)
	}
	if !sawToDefault {
		logsafeWarn("websocket: switch_agent succeeded but no toDefault record found for session; falling back to id comparison",
			"session_id", evtSID)
		toDefault = !activeOk || activeAgent == "" || (defaultAgent != nil && activeAgent == defaultAgent.ID)
	}
	switchF := generated.AgentSwitchedFrame{
		Type:      string(generated.WsFrameTypeAgentSwitched),
		SessionId: evtSID,
	}
	if activeOk && activeAgent != "" && !toDefault {
		agentName, _ := h.agentLoop.GetRegistry().GetAgentName(activeAgent)
		switchF.AgentId = &activeAgent
		if agentName != "" {
			switchF.Message = &agentName
		}
	} else if defaultName != "" {
		switchF.Message = &defaultName
	}
	data, err := json.Marshal(switchF)
	if err != nil {
		logsafeError("ws: marshal agent_switched for hub failed", "session_id", evtSID, "error", err)
		return
	}
	h.hubPublishAndDeliver(evtSID, string(generated.WsFrameTypeAgentSwitched), data)
}

// ---------------------------------------------------------------------
// Rate limit (session-scoped only — global scope stays on the legacy
// per-connection broadcast path, matching BE-DESIGN.md's "not sequenced"
// list) and error
// ---------------------------------------------------------------------

func (h *WSHandler) hubRateLimit(evt agent.Event) {
	p, ok := evt.Payload.(agent.RateLimitPayload)
	if !ok || p.Scope == "global" {
		// Global-scope rate_limit (daily cost cap etc.) is not tied to a
		// session — it stays on the legacy eventForwarder broadcast path,
		// unchanged (BE-DESIGN.md §1.2's "not sequenced" list).
		return
	}
	rateSID := p.SessionID
	if rateSID == "" {
		rateSID = h.hubResolveSessionIDForChat(p.ChatID)
	}
	if rateSID == "" {
		return
	}
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
	data, err := json.Marshal(rateF)
	if err != nil {
		logsafeError("ws: marshal rate_limit for hub failed", "session_id", rateSID, "error", err)
		return
	}
	h.hubPublishAndDeliver(rateSID, string(generated.WsFrameTypeRateLimit), data)
}

func (h *WSHandler) hubError(evt agent.Event) {
	p, ok := evt.Payload.(agent.ErrorPayload)
	if !ok {
		return
	}
	errSID := p.SessionID
	if errSID == "" {
		errSID = h.hubResolveSessionIDForChat(p.ChatID)
	}
	if errSID == "" {
		return
	}
	translated := agent.TranslateLLMError(p.ProviderError, p.Message)
	code := translated.Code
	message := translated.Message
	retryable := translated.Retryable
	detail := translated.Detail
	if p.Code != "" {
		code = agent.LLMErrorCode(p.Code)
		message = p.Message
		retryable = agent.IsRetryableCode(code)
		detail = agent.BuildDetail(p.ProviderError, message)
	}
	errF := generated.ErrorFrame{
		Type:      string(generated.WsFrameTypeError),
		SessionId: &errSID,
		Message:   message,
	}
	errF.Payload = &generated.ErrorPayload{
		LlmError: generated.LLMError{
			Code:      string(code),
			Message:   message,
			Retryable: retryable,
			Detail:    &detail,
		},
	}
	data, err := json.Marshal(errF)
	if err != nil {
		logsafeError("ws: marshal error frame for hub failed", "session_id", errSID, "error", err)
		return
	}
	// A turn-level error stays in the active-turn projection until the
	// turn's done, so a snapshot taken meanwhile still shows it.
	h.hubPublishMetaAlsoTo(errSID, string(generated.WsFrameTypeError),
		hubFrameMeta{
			kind: hubKindItem, key: "error:" + evt.Meta.TurnID + ":" + strconv.FormatInt(evt.Time.UnixNano(), 10),
			turnID: evt.Meta.TurnID,
		},
		data, nil)
}

// ---------------------------------------------------------------------
// Tool result projection
// ---------------------------------------------------------------------

func (h *WSHandler) hubToolResultProjection(evt agent.Event) {
	p, ok := evt.Payload.(agent.ToolResultProjectionPayload)
	if !ok {
		return
	}
	projSID := p.SessionID
	if projSID == "" {
		projSID = h.hubResolveSessionIDForChat(p.ChatID)
	}
	if projSID == "" {
		return
	}
	projF := generated.ToolResultProjectionFrame{
		Type:         string(generated.WsFrameTypeToolResultProjection),
		SessionId:    projSID,
		ToolCallId:   string(p.ToolCallID),
		ArchiveLine:  p.ArchiveLine,
		ContentState: p.ContentState,
	}
	if p.Mark != "" {
		mark := p.Mark
		projF.Mark = &mark
	}
	// ADR-091 D7: the payload's session ID is the projection-producing
	// session's own identity (producing_session_id is retired).
	data, err := json.Marshal(projF)
	if err != nil {
		logsafeError("ws: marshal tool_result_projection for hub failed", "session_id", projSID, "error", err)
		return
	}
	h.hubPublishAndDeliver(projSID, string(generated.WsFrameTypeToolResultProjection), data)
}

// ---------------------------------------------------------------------
// goal_status, goal_outcome, judge_verdict, loop_status: sequenced for the
// session's own connections, unsequenced broadcast to every other
// connection (BE-DESIGN.md §1.4).
// ---------------------------------------------------------------------

func (h *WSHandler) hubGoalStatusChanged(evt agent.Event) {
	p, ok := evt.Payload.(agent.GoalStatusChangedPayload)
	if !ok {
		return
	}
	goalF := generated.GoalStatusFrame{
		Type:         string(generated.WsFrameTypeGoalStatus),
		SessionId:    p.SessionID,
		Condition:    p.Condition,
		Round:        p.Round,
		MaxRounds:    p.MaxRounds,
		LatestReason: p.LatestReason,
		ActiveLoops:  p.ActiveLoops,
		Cap:          p.Cap,
		State:        p.State,
	}
	if p.GoalID != "" {
		gid := p.GoalID
		goalF.GoalId = &gid
	}
	setGoalStatusCriteria(&goalF, p.Criteria)
	if p.Definition != "" {
		def := p.Definition
		goalF.Definition = &def
	}
	setGoalStatusDoD(&goalF, p.DoD)
	data, err := json.Marshal(goalF)
	if err != nil {
		logsafeError("ws: marshal goal_status for hub failed", "session_id", p.SessionID, "error", err)
		return
	}
	h.hubBroadcastWithSequencedCopy(p.SessionID, string(generated.WsFrameTypeGoalStatus), data)
}

func (h *WSHandler) hubGoalOutcome(evt agent.Event) {
	p, ok := evt.Payload.(agent.GoalOutcomePayload)
	if !ok {
		return
	}
	frame := goalOutcomeFrame(p.SessionID, p.MessageID, p.Outcome)
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal goal_outcome for hub failed", "session_id", p.SessionID, "error", err)
		return
	}
	h.hubBroadcastWithSequencedCopy(p.SessionID, string(generated.WsFrameTypeGoalOutcome), data)
}

func (h *WSHandler) hubJudgeVerdict(evt agent.Event) {
	p, ok := evt.Payload.(agent.JudgeVerdictPayload)
	if !ok {
		return
	}
	frame := toJudgeVerdictFrame(p.SessionID, p.Verdict)
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal judge_verdict for hub failed", "session_id", p.SessionID, "error", err)
		return
	}
	// p.SessionID may be "" for a scope=plan verdict (never emitted today) —
	// hubBroadcastWithSequencedCopy's own empty-sessionID handling degrades
	// to "everyone gets the plain frame", matching pre-#823 behavior.
	h.hubBroadcastWithSequencedCopy(p.SessionID, string(generated.WsFrameTypeJudgeVerdict), data)
}

func (h *WSHandler) hubLoopStatusChanged(evt agent.Event) {
	p, ok := evt.Payload.(agent.LoopStatusChangedPayload)
	if !ok {
		return
	}
	loopF := generated.LoopStatusFrame{
		Type:      string(generated.WsFrameTypeLoopStatus),
		SessionId: p.SessionID,
		Mode:      p.Mode,
		Run:       p.Run,
		MaxRuns:   p.MaxRuns,
		State:     p.State,
	}
	if p.NextDelay != nil {
		nd := int64(*p.NextDelay)
		loopF.NextDelay = &nd
	}
	data, err := json.Marshal(loopF)
	if err != nil {
		logsafeError("ws: marshal loop_status for hub failed", "session_id", p.SessionID, "error", err)
		return
	}
	h.hubBroadcastWithSequencedCopy(p.SessionID, string(generated.WsFrameTypeLoopStatus), data)
}

// hubTurnEnd forgets a finished turn's projection items in every hub it left
// them in, unless a web streamer holds that session's live-stream claim for
// the turn — that turn is closed by its own `done`, published after TurnEnd
// once its answer is persisted (merge-review F1). A turn with no web
// streamer (an ADR-091 steered child — the launcher gives it no channel — or
// a Telegram/system task session) never publishes a done, so without this
// its tool cards stayed in the projection forever and pinned the hub against
// idle eviction. Everything it published is already persisted by the time
// the turn ends (tool calls on completion, errors and media as transcript
// entries), so a later snapshot's transcript read covers it. Emits no frame
// and never touches spans (ADR-091: a child outlives its parent's turn).
func (h *WSHandler) hubTurnEnd(evt agent.Event) {
	turnID := evt.Meta.TurnID
	if turnID == "" || h.hubs == nil {
		return
	}
	for _, sid := range h.hubs.takeTurnSessions(turnID) {
		if v, ok := h.streamOwners.Load(sid); ok {
			if claim, ok := v.(streamOwnerClaim); ok && claim.turnID == turnID {
				continue
			}
		}
		if hub := h.hubs.lookup(sid); hub != nil {
			hub.forgetTurn(turnID)
		}
	}
}
