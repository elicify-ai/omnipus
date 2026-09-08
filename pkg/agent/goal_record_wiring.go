// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_record_wiring.go wires pkg/tools.SetGoalTool's three late-bound seams
// (GoalRecordAccess, DiffFn, FeasibilityFn — set_goal.go's own package doc
// comment) over this package's real goal machinery, and implements the
// ADR-081 D5 write-side effect every successful record write triggers: the
// goal_status frame (FR-019) and, on a channel-routed goal, the formatted
// record echo (FR-020). pkg/tools cannot import pkg/agent (import cycle —
// pkg/agent already imports pkg/tools), so this file is the wave-2 wiring
// layer set_goal.go's own doc comment names — mirroring the
// AskUserQuestionRegistry / PlanCorrectTool.SetAppendCorrection precedent
// already established elsewhere in this package (pkg/agent/loop.go's
// registerSharedTools, pkg/agent/ask_user_wire.go).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// wireGoalToolsForAgent registers set_goal for one agent, wired over the
// real session-store-backed GoalRecordAccess plus the diff and feasibility
// seams (ADR-081 D2). Called from registerSharedTools' per-agent loop
// (loop.go), the SAME site AskUserQuestion registers from, so it re-runs on
// every hot reload — safe: every closure below is stateless with respect to
// cfg, resolving live state (the session store, the calling agent's own
// tool policy) per call, exactly like AskUserQuestion's own registry
// closure.
func wireGoalToolsForAgent(al *AgentLoop, agent *AgentInstance) {
	setGoalTool := tools.NewSetGoalTool(func() tools.GoalRecordAccess {
		return agentLoopGoalRecordAccess{al: al}
	})
	setGoalTool.SetDiffFn(goalRecordDiffAdapter)
	setGoalTool.SetFeasibilityFn(al.goalRecordFeasibilityFn)
	agent.Tools.RegisterReplacing(setGoalTool)
}

// agentLoopGoalRecordAccess implements tools.GoalRecordAccess over the real
// session store (ADR-081 D2): ReadGoalState/WriteRecord read and write the
// SAME session.MetaPatch.GoalCondition/GoalCriteriaJSON fields the
// compile-time path (goal_compile.go/goal_loop.go) already uses — set_goal
// is a second WRITER of that field, never a second store (DoD-11: one
// implementation).
type agentLoopGoalRecordAccess struct{ al *AgentLoop }

// ReadGoalState implements tools.GoalRecordAccess. GoalID is minted at goal
// activation (goal_loop.go's newGoalID call), the SAME MetaPatch write that
// first sets GoalCondition — so whenever GoalCondition is non-empty (the
// only case set_goal proceeds past the goalless-session refusal), GoalID is
// already present too; this is a read-only lookup, never a mint (ADR-082
// D9/FR-016 — set_goal's result surfaces the id, it does not assign one).
func (a agentLoopGoalRecordAccess) ReadGoalState(sessionID string) (goalID, goalCondition, recordJSON string, err error) {
	store := a.al.ResolveSessionStore(sessionID)
	if store == nil {
		return "", "", "", fmt.Errorf("goal record access: session %q is not known to any session store", sessionID)
	}
	meta, gerr := store.GetMeta(sessionID)
	if gerr != nil {
		return "", "", "", fmt.Errorf("goal record access: reading session meta: %w", gerr)
	}
	if meta.GoalCondition != "" && meta.GoalID == "" {
		// ADR-082 D9 review F8: an active goal with no id is a pre-ADR-053
		// goal meta (activated before goal ids were minted) or a meta
		// written by a path that skipped newGoalID. set_goal would silently
		// emit `"goal_id": ""` — the SPA then renders the record card with
		// no live-progress overlay and nothing in the log explains why.
		// Surface it loudly here, once per read, instead of letting the
		// empty id propagate unremarked.
		logger.WarnCF("goal", "goal record access: active goal has no goal_id — set_goal's result will carry an empty id and the SPA card cannot overlay live progress (pre-ADR-053 goal meta?)",
			map[string]any{"session_id": sessionID, "goal_condition": meta.GoalCondition})
	}
	return meta.GoalID, meta.GoalCondition, meta.GoalCriteriaJSON, nil
}

// goalRecordAnchor describes one ENGINE-authored goal-record write that must
// be anchored in the session transcript as a `set_goal` tool call (ADR-082
// D9, review CR8). Three paths write a record without ever running the tool
// — the marker-path activation and marker-path restate (goal_loop.go's
// applyGoalCommandPrompt / applyGoalMarkerRestate) and the D7 keeper
// fallback compile (goal_triggers.go's dispatchGoalFallbackCompile). Under
// D9 the record card renders ONLY from a `set_goal` call's own result at
// the call's own position; afterGoalRecordWrite's goal_status frame feeds the
// header pill and the per-criterion overlay, never the card's content. So
// each of those writes produces NO card at all unless the transcript carries
// a `set_goal` call for it — this is that call.
type goalRecordAnchor struct {
	store     *session.UnifiedStore
	sessionID string
	// agentID is the agent the record is attributed to (the goal-bearing
	// agent) — stamped on the transcript entry and the live frames exactly
	// as a real set_goal call stamps its calling agent.
	agentID string
	// chatID is the routing chat id for the live frames (the WS forwarder
	// matches on chat id OR session id; session id is the key that matters
	// under ADR-082 D6, chat id is advisory).
	chatID string
	// mode is tools.SetGoalModeRegister or tools.SetGoalModeUpdate.
	mode string
	// narration is the short assistant-role content line the entry carries
	// ahead of the call — non-empty on purpose: replay emits a replay_message
	// only for non-empty content, and the SPA anchors the following
	// tool_call_start to that just-emitted assistant bubble; an empty-content
	// entry would leave the card riding a placeholder bubble minted mid-
	// replay instead.
	narration string
	record    *CompiledGoal
	// assumptions is surfaced as the call's `assessment.assumptions` so the
	// card (and the log) say plainly that the ENGINE, not the agent, authored
	// this record and why.
	assumptions []string
}

// goalAnchorTraceSource / goalAnchorTracePath label the synthetic frames'
// EventMeta so the event log distinguishes an engine-anchored set_goal call
// from a tool-executed one.
const (
	goalAnchorTraceSource = "goal_loop"
	goalAnchorTracePath   = "goal.record.anchor"
)

// anchorGoalRecordInTranscript appends one assistant transcript entry
// carrying a single successful `set_goal` tool call for a.record — params
// {mode, definition, criteria, dod, assessment} in the tool's own schema
// shape, result = the byte-identical payload a real set_goal success returns
// (tools.SetGoalResultPayload, goal_id + the full record), persisted under
// the SAME {"text": <json>} Result shape pkg/agent/loop.go's tcRecord
// construction persists for every plain-text tool result — so replay
// (pkg/gateway/replay.go) and hydration (attach_hydrate.go, which turns an
// assistant entry's ToolCalls into a balanced tool_use/tool_result pair)
// treat it exactly like a tool-executed call. Then it emits the SAME
// EventKindToolExecStart/End pair loop.go's runTurn emits around a real
// call, keyed by session id, so a bound webchat connection sees the card
// appear live at that position rather than only after a reload.
//
// Returns the minted tool-call id. A transcript write failure is returned
// (and counted on taskGoalTranscriptWriteFailures) after logging; the live
// frames are NOT emitted in that case — a card the transcript cannot replay
// would be a one-time apparition, worse than none.
func (al *AgentLoop) anchorGoalRecordInTranscript(a goalRecordAnchor) (session.ToolCallID, error) {
	if a.store == nil || a.sessionID == "" {
		return "", errors.New("goal anchor: no session store or session id")
	}
	if a.record == nil {
		return "", errors.New("goal anchor: nil compiled record")
	}
	meta, err := a.store.GetMeta(a.sessionID)
	if err != nil || meta == nil {
		return "", fmt.Errorf("goal anchor: reading session meta: %w", err)
	}
	// A marker-shaped record legitimately carries no restated statement
	// (ADR-081 round-2 B-3); the card's lead line then falls back to the
	// goal condition, the same fallback the echo and the frame already use.
	definition := a.record.Definition
	if definition == "" {
		definition = a.record.Prompt
	}
	if definition == "" {
		definition = a.record.Intent
	}
	assessment := map[string]any{"clarity": "clear"}
	if len(a.assumptions) > 0 {
		assessment["assumptions"] = a.assumptions
	}
	params := map[string]any{
		"mode":       a.mode,
		"definition": definition,
		"criteria":   setGoalCriteriaArgs(a.record.Criteria, false),
		"dod":        setGoalCriteriaArgs(a.record.DoD, true),
		"assessment": assessment,
	}
	resultJSON, merr := json.Marshal(tools.SetGoalResultPayload(tools.SetGoalResultCore{
		Mode:       a.mode,
		GoalID:     meta.GoalID,
		Definition: definition,
		Criteria:   a.record.Criteria,
		DoD:        a.record.DoD,
		Assessment: assessment,
	}))
	if merr != nil {
		return "", fmt.Errorf("goal anchor: encoding set_goal result: %w", merr)
	}
	now := time.Now().UTC()
	callID := session.ToolCallID(fmt.Sprintf("tc_goal_%s_%d", a.mode, now.UnixNano()))
	tc := session.ToolCall{
		ID:         callID,
		Tool:       tools.SetGoalToolName,
		Status:     "success",
		Parameters: params,
		Result:     map[string]any{"text": string(resultJSON)},
	}
	entry := session.TranscriptEntry{
		ID:        fmt.Sprintf("goal-%s-anchor-%d", a.sessionID, now.UnixNano()),
		Role:      "assistant",
		Content:   a.narration,
		AgentID:   a.agentID,
		Timestamp: now,
		ToolCalls: []session.ToolCall{tc},
	}
	if werr := a.store.AppendTranscriptStrict(a.sessionID, entry); werr != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("agent", "goal: could not anchor the record as a set_goal transcript call; the card will not render for this write",
			map[string]any{"component": "goal", "session_id": a.sessionID, "goal_id": meta.GoalID, "mode": a.mode, "error": werr.Error()})
		return "", fmt.Errorf("goal anchor: transcript write: %w", werr)
	}

	evtMeta := EventMeta{AgentID: a.agentID, Source: goalAnchorTraceSource, TracePath: goalAnchorTracePath}
	al.emitEvent(EventKindToolExecStart, evtMeta, ToolExecStartPayload{
		ToolCallID: callID,
		ChatID:     a.chatID,
		SessionID:  a.sessionID,
		Tool:       tools.SetGoalToolName,
		Arguments:  cloneEventArguments(params),
		AgentID:    a.agentID,
	})
	al.emitEvent(EventKindToolExecEnd, evtMeta, ToolExecEndPayload{
		ToolCallID: callID,
		ChatID:     a.chatID,
		SessionID:  a.sessionID,
		Tool:       tools.SetGoalToolName,
		ForLLMLen:  len(resultJSON),
		Result:     string(resultJSON),
		AgentID:    a.agentID,
	})
	logger.InfoCF("agent", "goal: record anchored as a set_goal transcript call",
		map[string]any{"component": "goal", "session_id": a.sessionID, "goal_id": meta.GoalID, "mode": a.mode, "tool_call_id": string(callID)})
	return callID, nil
}

// setGoalCriteriaArgs renders a criterion ladder in set_goal's OWN argument
// schema (Parameters(): criteria items are {text, judgment}; dod items add
// {provenance}) so the synthetic call's params read exactly like a call the
// agent would have made — a reader expanding the raw call (verbose chat)
// sees the tool's documented input shape, not an internal record dump.
func setGoalCriteriaArgs(items []task.AcceptanceCriterion, withProvenance bool) []any {
	out := make([]any, 0, len(items))
	for _, c := range items {
		item := map[string]any{"text": c.Text, "judgment": string(c.Judgment)}
		if withProvenance && c.Provenance != "" {
			item["provenance"] = string(c.Provenance)
		}
		out = append(out, item)
	}
	return out
}

// WriteRecord implements tools.GoalRecordAccess: persists recordJSON as
// sessionID's compiled goal record, bumps the activity clock, and RESETS
// the FR-014b zero-output push streak (a fresh registration/update is
// unambiguous forward progress, so a prior "recordless-idle" streak no
// longer applies against it) — then triggers the D5 write-side effect
// (frame emission + channel echo, afterGoalRecordWrite below).
func (a agentLoopGoalRecordAccess) WriteRecord(sessionID, recordJSON string) error {
	store := a.al.ResolveSessionStore(sessionID)
	if store == nil {
		return fmt.Errorf("goal record access: session %q is not known to any session store", sessionID)
	}
	var priorRecordJSON string
	if priorMeta, gerr := store.GetMeta(sessionID); gerr == nil && priorMeta != nil {
		priorRecordJSON = priorMeta.GoalCriteriaJSON
	}
	now := time.Now().UTC().Format(time.RFC3339)
	zero := 0
	if err := store.SetMeta(sessionID, session.MetaPatch{
		GoalCriteriaJSON:     &recordJSON,
		GoalLastActivityAt:   &now,
		GoalZeroOutputPushes: &zero,
	}); err != nil {
		return fmt.Errorf("goal record access: writing session meta: %w", err)
	}
	a.al.afterGoalRecordWrite(sessionID, recordJSON, goalRecordDiffAdapter(priorRecordJSON, recordJSON))
	return nil
}

// goalRecordDiffAdapter is the tools.DiffFn seam (ADR-081 D2 mode:update):
// diffGoalAmendment's one surviving production caller (goal_compile.go's own
// guard comment, removed below). Both sides are unmarshaled via
// loadCompiledGoal so the comparison runs against the SAME normalized shape
// the judge/frame/echo all read — never a raw-JSON string diff.
func goalRecordDiffAdapter(oldRecordJSON, newRecordJSON string) string {
	amd := diffGoalAmendment(loadCompiledGoal(oldRecordJSON), loadCompiledGoal(newRecordJSON))
	return formatGoalAmendmentSummary(amd)
}

// formatGoalAmendmentSummary renders a GoalAmendment as the SAME plain-text
// shape set_goal.go's own unwired-seam fallback (localDiffSummary) produces,
// so a caller sees byte-identical wording whether the real differ or the
// fallback rendered it.
func formatGoalAmendmentSummary(amd *GoalAmendment) string {
	if amd == nil {
		amd = &GoalAmendment{}
	}
	return fmt.Sprintf(
		"criteria: +%d added, ~%d changed, -%d dropped; dod: +%d added, ~%d changed, -%d dropped",
		len(amd.Added), len(amd.Changed), len(amd.Dropped),
		len(amd.DoDAdded), len(amd.DoDChanged), len(amd.DoDDropped),
	)
}

// goalRecordFeasibilityFn is the tools.FeasibilityFn seam (ADR-081 D2):
// vets set_goal's submitted criteria∪dod union through the SAME compile-time
// feasibility gate (feasibilityGate, goal_compile.go) the deterministic and
// LLM compile paths already run, over the CALLING agent's own tool policy
// (agentFeasibilityContext — FR-112, never a privileged bypass). An
// unresolvable calling agent fails CLOSED: feasibilityGate's KindBehavior/
// KindCheck branches read fc.EffectiveToolPolicy/fc.BashReachable, which
// agentFeasibilityContext's nil-agentInst branch answers "deny"/false —
// matching the compile path's own fail-closed default.
func (al *AgentLoop) goalRecordFeasibilityFn(ctx context.Context, criteria []task.AcceptanceCriterion) error {
	var fc FeasibilityContext = agentFeasibilityContext{}
	if agentID := tools.ToolAgentID(ctx); agentID != "" {
		if inst, ok := al.GetRegistry().GetAgent(agentID); ok && inst != nil {
			fc = agentFeasibilityContext{agentInst: inst}
		}
	}
	if rej := feasibilityGate(criteria, fc); rej != nil {
		return errors.New(rej.Reason)
	}
	return nil
}

// afterGoalRecordWrite is ADR-081 D5's write-side effect, run after EVERY
// successful goal-record write (register or update) that lands through
// WriteRecord above:
//
//   - FR-019: emits the goal_status frame in state ACTIVE with
//     definition/criteria/dod populated from the freshly-written record
//     (definition legitimately absent on a marker-shaped record — the
//     existing Prompt/Intent fallback, round-2 B-3). `queued` is never
//     emitted from this call site.
//   - FR-020: when the goal's RECORDED routing channel (goalTriggers().
//     routeFor — never the current turn's own transient Channel, so a
//     keeper/nudge turn on a Telegram-origin goal still echoes to
//     Telegram) is non-web, sends the formatted record (the re-scoped
//     formatGoalEcho) as one channel text message via the outbound bus —
//     exactly once per write. A web-routed (or not-yet-routed) goal
//     receives no text echo; the frame is the surface there.
func (al *AgentLoop) afterGoalRecordWrite(sessionID, recordJSON, diffSummary string) {
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		logger.WarnCF("agent", "goal: could not re-read session meta after a record write; frame/echo skipped",
			map[string]any{"component": "goal", "session_id": sessionID})
		return
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		logger.WarnCF("agent", "goal: could not re-read session meta after a record write; frame/echo skipped",
			map[string]any{"component": "goal", "session_id": sessionID, "error": errString(err)})
		return
	}

	g := loadCompiledGoal(recordJSON)
	var definition string
	var criteria, dod []task.AcceptanceCriterion
	if g != nil {
		definition = g.Definition
		criteria = g.Criteria
		dod = g.DoD
	}
	al.emitGoalStatusFrameWithCriteriaAndDoD(
		sessionID, meta.GoalID, meta.GoalCondition, meta.GoalRoundsUsed, meta.GoalMaxRounds,
		meta.GoalLatestReason, goalPillActive, definition, criteria, dod,
	)

	if route := goalTriggers().routeFor(sessionID); route.channel != "" && route.channel != goalForcingWebChannel {
		switch al.bus {
		case nil:
			logger.WarnCF("agent", "goal: channel record echo skipped — no message bus wired",
				map[string]any{"component": "goal", "session_id": sessionID, "channel": route.channel})
		default:
			if perr := al.bus.PublishOutbound(context.Background(), bus.OutboundMessage{
				Channel: route.channel,
				ChatID:  route.chatID,
				Content: formatGoalEcho(g),
				AgentID: route.agentID,
			}); perr != nil {
				logger.WarnCF("agent", "goal: channel record echo failed to publish",
					map[string]any{
						"component": "goal", "session_id": sessionID, "channel": route.channel, "error": perr.Error(),
					})
			}
		}
	}

	logger.InfoCF("agent", "goal: record write applied",
		map[string]any{"component": "goal", "session_id": sessionID, "goal_id": meta.GoalID, "diff": diffSummary})
}

// EmitGoalStatusRehydrate re-emits ONE goal_status event for sessionID
// carrying its CURRENT persisted definition/criteria/dod — reusing the SAME
// emission call afterGoalRecordWrite (above) uses — when (and only when)
// the session carries an active, non-terminal goal with a registered
// record. No-op (returns false, no event emitted) when there is no active
// goal, no record yet (D1's legal transient empty-record state), the
// record fails to parse, or the session/store cannot be resolved.
//
// Item 14 (review-round-1, ADR-081): a WS reattach (SPA reload/reconnect)
// has no rehydration path for a goal's already-registered record —
// goal_status is a pure live push (EventKindGoalStatusChanged), never a
// persisted, replayable transcript entry, so the record card the SPA
// renders from it never reappeared after a reload. The gateway's
// handleAttachSession (pkg/gateway/websocket.go) calls this once, right
// after replay + hydration complete, so the reattaching connection (already
// registered for live-event forwarding earlier in that same function) sees
// exactly the event it would have seen had it never disconnected. Cheap:
// one GetMeta read, only on attach, only when a record genuinely exists.
func (al *AgentLoop) EmitGoalStatusRehydrate(sessionID string) bool {
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return false
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return false
	}
	if meta.GoalCondition == "" || meta.GoalCriteriaJSON == "" {
		// No active goal, or the D1 legal-transient empty-record state
		// (nothing registered yet to rehydrate) — a genuinely terminal goal
		// also reads GoalCondition == "" (clearGoal empties it), so this
		// same check excludes terminal goals for free.
		return false
	}
	g := loadCompiledGoal(meta.GoalCriteriaJSON)
	if g == nil {
		// Non-empty but unparseable/corrupt/zero-criteria — loadCompiledGoal
		// already WARN-logged the specifics; nothing safe to re-emit.
		return false
	}
	al.emitGoalStatusFrameWithCriteriaAndDoD(
		sessionID, meta.GoalID, meta.GoalCondition, meta.GoalRoundsUsed, meta.GoalMaxRounds,
		meta.GoalLatestReason, goalPillActive, g.Definition, g.Criteria, g.DoD,
	)
	return true
}
