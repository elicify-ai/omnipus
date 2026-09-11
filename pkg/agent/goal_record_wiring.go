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
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// wireGoalToolsForAgent registers BOTH goal tools for one agent — set_goal
// (ADR-081 D2) and goal_claim (ADR-084 revision 9 §O / D12) — each wired
// over the real session-store-backed GoalRecordAccess; set_goal additionally
// gets the diff and feasibility seams. Called from registerSharedTools'
// per-agent loop (loop.go), the SAME site AskUserQuestion registers from, so
// it re-runs on every hot reload — safe: every closure below is stateless
// with respect to cfg, resolving live state (the session store, the calling
// agent's own tool policy) per call, exactly like AskUserQuestion's own
// registry closure.
//
// goal_claim MUST be registered here and nowhere else. It shares set_goal's
// GoalRecordAccess seam verbatim (goal_claim.go's own doc comment: "reusing
// set_goal.go's own GoalRecordAccess (its read half) ... so a single
// implementation wires both tools with no adapter needed") — it reads
// ReadGoalState only, to answer its FR-090 "does this session have an active
// goal at all" precondition, and never writes. Without this registration the
// tool is seeded in every agent's policy map and present in the metadata
// catalog yet NEVER OFFERED TO A MODEL, which silently disables ADR-084's
// claim-triggered adjudication entirely (goal_loop.go's claim detection can
// never fire and the engine falls back to the prose markers D12/D13
// replaced).
func wireGoalToolsForAgent(al *AgentLoop, agent *AgentInstance) {
	goalAccess := func() tools.GoalRecordAccess {
		return agentLoopGoalRecordAccess{al: al}
	}
	setGoalTool := tools.NewSetGoalTool(goalAccess)
	setGoalTool.SetDiffFn(goalRecordDiffAdapter)
	setGoalTool.SetFeasibilityFn(al.goalRecordFeasibilityFn)
	agent.Tools.RegisterReplacing(setGoalTool)
	agent.Tools.RegisterReplacing(tools.NewGoalClaimTool(goalAccess))
}

// agentLoopGoalRecordAccess implements tools.GoalRecordAccess. Its two
// methods, ReadGoalState and WriteRecord, are this wave's (joint delivery
// plan wave E4, GOAL-FR-003's "seam" half + FR-005's "consumer half") own
// re-point of ADR-081 D2's seam onto ADR-086's goal entity store
// (pkg/goal.Store) — see each method's own doc comment for the shape of
// the change. The other functions in this file (wireGoalToolsForAgent,
// afterGoalRecordWrite, anchorGoalRecordInTranscript,
// goalRecordDiffAdapter, goalRecordFeasibilityFn, EmitGoalStatusRehydrate)
// have since been re-pointed onto the same store: wave S6 deleted session
// meta's GoalID/GoalCondition/GoalCriteriaJSON/GoalRoundsUsed/
// GoalMaxRounds/GoalLatestReason fields outright, so NOTHING in this file
// reads goal state off session.UnifiedMeta any more — activeGoalForSession
// (below) is the single entry predicate they all share.
type agentLoopGoalRecordAccess struct{ al *AgentLoop }

// goalSeamRecord is the JSON wire shape ReadGoalState/WriteRecord exchange
// with pkg/tools' own unexported setGoalRecord (set_goal.go): the two are
// independent, field-for-field mirrors of each other by necessity —
// pkg/tools cannot import pkg/agent (import cycle) and this package cannot
// reach set_goal.go's unexported type — exactly the same precedent
// set_goal.go's own doc comment already documents for pkg/agent.
// CompiledGoal. Under ADR-086 GOAL-FR-003, pkg/goal.Store's own
// Goal.Criteria/Goal.DoD are ALWAYS real typed lists, never a serialised
// string — this struct exists ONLY at this seam's own wire boundary
// (set_goal.go's GoalRecordAccess interface still speaks a JSON string,
// and that interface is not this wave's to change — pkg/tools/set_goal.go
// belongs to wave E12), translating to/from the typed record on the way
// in and out.
type goalSeamRecord struct {
	Intent             string                     `json:"intent"`
	Prompt             string                     `json:"prompt"`
	Definition         string                     `json:"definition,omitempty"`
	Criteria           []task.AcceptanceCriterion `json:"criteria"`
	DoD                []task.AcceptanceCriterion `json:"dod,omitempty"`
	SupersededCriteria []goalSeamSupersededEntry  `json:"superseded_criteria,omitempty"`
}

// goalSeamSupersededEntry mirrors goal.SupersededCriteriaEntry's JSON shape
// (the same three fields) at this seam's wire boundary.
type goalSeamSupersededEntry struct {
	Criteria     []task.AcceptanceCriterion `json:"criteria"`
	DoD          []task.AcceptanceCriterion `json:"dod,omitempty"`
	SupersededAt time.Time                  `json:"superseded_at"`
}

// resolveGoalRecordStore constructs a pkg/goal.Store rooted at the current
// $OMNIPUS_HOME. config.OmnipusHomeDir() is deliberately read fresh on
// every call (its own doc comment: "Intentionally not memoised so tests
// can override OMNIPUS_HOME mid-process") and pkg/goal.Store is a thin,
// cheap wrapper over pkg/entity.Store[Goal] with nothing expensive to
// cache — the SAME per-call-site construction pattern pkg/agentstore's own
// callers already use (e.g. pkg/gateway/rest.go's
// agentstore.New(a.homePath)). AgentLoop has no goal-store field of its
// own: AgentLoop is declared in pkg/agent/loop.go, a file this wave's
// write-set (joint delivery plan §3, wave E4) does not include — loop.go
// belongs to the E2 → B123 → E13 chain.
func resolveGoalRecordStore() *goal.Store {
	return goal.NewStore(config.OmnipusHomeDir())
}

// activeGoalForSession returns the ACTIVE goal record currently BOUND to
// sessionID — the record whose ActiveSessionID is this session — for EITHER
// owner kind, or nil when this session carries no active goal.
//
// This is the ADR-086 replacement for the retired
// `meta.GoalCondition != ""` entry predicate that every goal reader in this
// package used to run against session.UnifiedMeta (deleted by wave S6). It
// deliberately keys on ActiveSessionID rather than on the owner reference
// (GetActiveByOwner), because the two are NOT the same lookup for a
// task-owned goal: a task goal's OwnerID is the TASK's id, while the turns
// that must reach the keeper/claim machinery run in the session the task run
// minted (status.go's Activate binds exactly that session into
// ActiveSessionID). GOAL-FR-013 requires one code path for both owner kinds,
// so the session-bound lookup is the one that serves both; ReadGoalState's
// owner-keyed GetActiveByOwner stays as it is — that seam is the `set_goal`
// tool's, and a task goal's criteria are fixed at creation (D-C), never
// authored by a task run's own agent.
//
// Finding more than one active record bound to one session is an upstream
// invariant violation (at most one goal may be active per session). It is
// reported at Warn and the first record in the store's own (created_at, id)
// order is used, rather than silently returning nil — losing the goal loop
// entirely would be a worse failure than continuing against the older of two
// records.
func activeGoalForSession(sessionID string) *goal.Goal {
	if sessionID == "" {
		return nil
	}
	active, err := resolveGoalRecordStore().ListActive()
	if err != nil {
		logger.WarnCF("agent", "goal: could not list active goal records; treating this session as goal-less",
			map[string]any{"component": "goal", "session_id": sessionID, "error": err.Error()})
		return nil
	}
	var found *goal.Goal
	for i := range active {
		if active[i].ActiveSessionID != sessionID {
			continue
		}
		if found != nil {
			logger.WarnCF("agent", "goal: more than one ACTIVE goal record is bound to this session — using the first; this is an upstream invariant violation",
				map[string]any{"component": "goal", "session_id": sessionID, "goal_id": found.GoalID, "other_goal_id": active[i].GoalID})
			break
		}
		g := active[i]
		found = &g
	}
	return found
}

// bumpGoalRecordActivity moves goalID's own LastActivityAt clock forward —
// GOAL-FR-004's relocation of the retired session-meta GoalLastActivityAt
// field onto the goal record, where it drives both the ~60 s quiet-window
// math and the multi-day idle-expiry brake. Best-effort with a Warn on
// failure, exactly like the SetMeta writes it replaces: a missed bump only
// delays idle settlement, it never fails the caller.
func bumpGoalRecordActivity(goalID string, now time.Time) {
	if goalID == "" {
		return
	}
	if _, err := resolveGoalRecordStore().Update(goalID, func(cur *goal.Goal) error {
		cur.LastActivityAt = now
		return nil
	}); err != nil {
		logger.WarnCF("agent", "goal: could not bump the goal record's activity clock",
			map[string]any{"component": "goal", "goal_id": goalID, "error": err.Error()})
	}
}

// compiledGoalFromRecord projects a pkg/goal record onto this package's
// in-memory CompiledGoal shape, so every reader that used to call
// loadCompiledGoal(meta.GoalCriteriaJSON) keeps reading the SAME struct it
// always did — now sourced from the record's typed Criteria/DoD lists
// (GOAL-FR-003) instead of a serialised session-meta string.
//
// It reproduces loadCompiledGoal's own emptiness contract exactly: a record
// with no criteria yet — ADR-081 D1's legal transient window between instant
// activation and the working agent's first `set_goal` — returns nil, the
// same value an empty GoalCriteriaJSON returned, so every caller's existing
// nil fallback behaves unchanged. pkg/goal.Goal has no separate Intent field
// (GOAL-FR-002 keeps ONE raw-intent field, Prompt), so Prompt fills both
// wire positions — the same mapping marshalGoalSeamRecord already makes.
func compiledGoalFromRecord(g *goal.Goal) *CompiledGoal {
	if g == nil || len(g.Criteria) == 0 {
		return nil
	}
	return &CompiledGoal{
		Intent:     g.Prompt,
		Prompt:     g.Prompt,
		Definition: g.Definition,
		Criteria:   g.Criteria,
		DoD:        g.DoD,
	}
}

// goalRecordCompiledJSON renders g in the SAME CompiledGoal JSON encoding
// the retired GoalCriteriaJSON session-meta field carried, for the two
// remaining consumers that still speak that encoding at a seam boundary:
// afterGoalRecordWrite's own recordJSON argument and goalRecordDiffAdapter's
// "prior record" side. Returns "" for a record with no criteria — matching
// the empty-string value those consumers already handle.
func goalRecordCompiledJSON(g *goal.Goal) string {
	compiled := compiledGoalFromRecord(g)
	if compiled == nil {
		return ""
	}
	data, err := marshalCompiledGoal(compiled)
	if err != nil {
		logger.WarnCF("agent", "goal: could not marshal the goal record as a compiled-goal record",
			map[string]any{"component": "goal", "goal_id": g.GoalID, "error": err.Error()})
		return ""
	}
	return data
}

// marshalGoalSeamRecord renders g's typed Criteria/DoD/Definition/
// SupersededCriteria as this seam's goalSeamRecord JSON shape — the same
// field names pkg/tools.setGoalRecord uses, so a round-trip through
// set_goal.go's own json.Unmarshal(&oldRec, ...) carries every field
// forward unchanged (set_goal.go's own doc comment: "Intent/Prompt are
// never SET by this tool ... but ARE carried through unchanged").
// pkg/goal.Goal has no separate Intent/Prompt split the way
// pkg/agent.CompiledGoal does — Goal.Prompt (GOAL-FR-002's one raw-intent
// field) fills both wire positions; set_goal.go only ever carries these
// two fields forward verbatim, never branching on which is which.
func marshalGoalSeamRecord(g *goal.Goal) (string, error) {
	rec := goalSeamRecord{
		Intent:     g.Prompt,
		Prompt:     g.Prompt,
		Definition: g.Definition,
		Criteria:   g.Criteria,
		DoD:        g.DoD,
	}
	for _, s := range g.SupersededCriteria {
		rec.SupersededCriteria = append(rec.SupersededCriteria, goalSeamSupersededEntry{
			Criteria: s.Criteria, DoD: s.DoD, SupersededAt: s.SupersededAt,
		})
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// parseGoalSeamRecord parses recordJSON — WriteRecord's own argument,
// set_goal.go's freshly-marshaled setGoalRecord — into this seam's wire
// shape. An empty/whitespace-only value is refused: WriteRecord always
// carries a real record (set_goal.go's Execute always populates
// definition + criteria before calling access.WriteRecord).
func parseGoalSeamRecord(recordJSON string) (goalSeamRecord, error) {
	var rec goalSeamRecord
	if strings.TrimSpace(recordJSON) == "" {
		return rec, errors.New("empty record")
	}
	if err := json.Unmarshal([]byte(recordJSON), &rec); err != nil {
		return goalSeamRecord{}, err
	}
	return rec, nil
}

// ReadGoalState implements tools.GoalRecordAccess (ADR-086 GOAL-FR-003 seam
// re-point, GOAL-FR-005's consumer half). It looks up sessionID's ACTIVE,
// session-owned pkg/goal.Store record (GOAL-FR-002's owner_kind: session)
// instead of session meta's retired GoalID/GoalCondition/GoalCriteriaJSON
// fields — those fields are not deleted until wave S6 and other functions
// in this same file still read them (see agentLoopGoalRecordAccess's own
// doc comment), but this method no longer does.
//
// goalCondition keeps its EXACT old emptiness-only contract: set_goal.go's
// GoalRecordAccess doc says "" means no active goal, and its Execute()
// checks nothing else about the value
// (strings.TrimSpace(goalCondition) == ""). A pkg/goal.Goal's Prompt is
// always non-empty once persisted (Goal.Validate requires it), so
// returning g.Prompt here satisfies that contract exactly while surfacing
// real text instead of an arbitrary placeholder.
//
// recordJSON is "" for the ADR-081 D1 legal-transient window — an active
// goal with no criteria registered yet (Goal.Validate allows an empty
// Criteria list; only DoD must be non-empty) — and this seam's own
// goalSeamRecord JSON encoding of g otherwise.
//
// FR-005's consumer half: the parked-question set (PendingAskJSON, session-
// owned per wave S2) is never read or returned here — it was never part of
// this seam's contract and it is not part of pkg/goal.Goal either (see
// goal.go: no PendingAsk field exists on the goal entity).
func (a agentLoopGoalRecordAccess) ReadGoalState(sessionID string) (goalID, goalCondition, recordJSON string, err error) {
	if a.al.ResolveSessionStore(sessionID) == nil {
		return "", "", "", fmt.Errorf("goal record access: session %q is not known to any session store", sessionID)
	}
	g, gerr := resolveGoalRecordStore().GetActiveByOwner(generated.GoalOwnerKindSession, sessionID)
	if gerr != nil {
		if errors.Is(gerr, goal.ErrOwnerNotFound) {
			// No active goal record for this session — matches the OLD
			// "GoalCondition == ''" empty-triple contract exactly.
			return "", "", "", nil
		}
		return "", "", "", fmt.Errorf("goal record access: reading goal record: %w", gerr)
	}
	if len(g.Criteria) == 0 {
		return g.GoalID, g.Prompt, "", nil
	}
	rec, merr := marshalGoalSeamRecord(g)
	if merr != nil {
		return "", "", "", fmt.Errorf("goal record access: encoding goal record: %w", merr)
	}
	return g.GoalID, g.Prompt, rec, nil
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
	// goalID is the goal this record belongs to — stamped into the synthetic
	// set_goal result payload and the log lines. Supplied by the caller
	// (ADR-086): it used to be read back off session meta's retired GoalID
	// field inside anchorGoalRecordInTranscript, and every call site already
	// holds the id it is anchoring for.
	goalID string
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
	// The session must exist for the transcript append below to land; the
	// goal identity itself now comes from a.goalID (ADR-086), not from this
	// read.
	if meta, err := a.store.GetMeta(a.sessionID); err != nil || meta == nil {
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
		GoalID:     a.goalID,
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
			map[string]any{"component": "goal", "session_id": a.sessionID, "goal_id": a.goalID, "mode": a.mode, "error": werr.Error()})
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
		map[string]any{"component": "goal", "session_id": a.sessionID, "goal_id": a.goalID, "mode": a.mode, "tool_call_id": string(callID)})
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

// WriteRecord implements tools.GoalRecordAccess (ADR-086 GOAL-FR-003 seam
// re-point). It parses recordJSON — set_goal.go's own freshly-marshaled
// setGoalRecord — into typed criteria/dod and persists them onto
// sessionID's ACTIVE pkg/goal.Store record via Store.Update, never as a
// serialised string (GOAL-FR-003: pkg/goal.Goal.Criteria/DoD are always
// real typed lists). SetCriteria/SetDoD (pkg/goal/criteria.go) bump the
// record's own LastActivityAt as a side effect — GOAL-FR-004's relocated
// idle-expiry clock — and ZeroOutputPushes is explicitly reset to 0 below,
// replacing the old session.MetaPatch.GoalLastActivityAt/
// GoalZeroOutputPushes write this method used to make (the FR-014b reset
// rule: a fresh registration/update is unambiguous forward progress, so a
// prior "recordless-idle" streak no longer applies against it).
//
// This requires an ALREADY-ACTIVE goal record for sessionID.
// set_goal.go's own Execute() already refuses before ever reaching this
// call when ReadGoalState reported no active goal (goalCondition == ""),
// so a missing active record here is a caller-contract violation, not a
// normal-path branch — it is reported as an error rather than silently
// creating one. The activation write itself (Store.Create + Goal.Activate)
// belongs to the engine wave that wires /goal activation into
// goal_loop.go — outside this wave's write-set (joint delivery plan §3:
// this file's WriteRecord/ReadGoalState is wave E4; goal_loop.go's
// activation path is E8/E12, later in the same chain).
//
// The D5 write-side effect (frame emission + channel echo,
// afterGoalRecordWrite below — untouched by this wave, still reading
// session meta's own Goal* fields until wave S6) still receives the
// ORIGINAL recordJSON argument unchanged: goalSeamRecord's wire shape is a
// superset of the fields pkg/agent.CompiledGoal's own json.Unmarshal reads
// (loadCompiledGoal silently ignores the one field it does not know,
// superseded_criteria), so afterGoalRecordWrite's own loadCompiledGoal
// call parses it exactly as it always has. The "prior" side of
// goalRecordDiffAdapter's diff is this seam's own re-marshaling of the
// record as it stood immediately BEFORE this write (the OLD behaviour read
// this from session meta; it now reads it from the store).
func (a agentLoopGoalRecordAccess) WriteRecord(sessionID, recordJSON string) error {
	if a.al.ResolveSessionStore(sessionID) == nil {
		return fmt.Errorf("goal record access: session %q is not known to any session store", sessionID)
	}
	rec, perr := parseGoalSeamRecord(recordJSON)
	if perr != nil {
		return fmt.Errorf("goal record access: parsing incoming record: %w", perr)
	}

	store := resolveGoalRecordStore()
	existing, gerr := store.GetActiveByOwner(generated.GoalOwnerKindSession, sessionID)
	if gerr != nil {
		return fmt.Errorf("goal record access: no active goal to write against: %w", gerr)
	}
	var priorRecordJSON string
	if len(existing.Criteria) > 0 {
		if data, merr := marshalGoalSeamRecord(existing); merr == nil {
			priorRecordJSON = data
		}
	}

	now := time.Now().UTC()
	if _, uerr := store.Update(existing.GoalID, func(cur *goal.Goal) error {
		cur.Definition = rec.Definition
		if err := cur.SetCriteria(rec.Criteria, now); err != nil {
			return err
		}
		if len(rec.DoD) > 0 {
			if err := cur.SetDoD(rec.DoD, now); err != nil {
				return err
			}
		}
		superseded := make([]goal.SupersededCriteriaEntry, 0, len(rec.SupersededCriteria))
		for _, s := range rec.SupersededCriteria {
			superseded = append(superseded, goal.SupersededCriteriaEntry{
				Criteria: s.Criteria, DoD: s.DoD, SupersededAt: s.SupersededAt,
			})
		}
		cur.SupersededCriteria = superseded
		cur.ZeroOutputPushes = 0
		return nil
	}); uerr != nil {
		return fmt.Errorf("goal record access: writing goal record: %w", uerr)
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
	// ADR-086: the goal's own record, not session meta, is where the frame's
	// id / condition / round / budget / reason now come from.
	rec := activeGoalForSession(sessionID)
	if rec == nil {
		logger.WarnCF("agent", "goal: could not re-read the goal record after a record write; frame/echo skipped",
			map[string]any{"component": "goal", "session_id": sessionID})
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
		sessionID, rec.GoalID, rec.Prompt, rec.Round, rec.MaxRounds,
		rec.LatestReason, goalPillActive, definition, criteria, dod,
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
		map[string]any{"component": "goal", "session_id": sessionID, "goal_id": rec.GoalID, "diff": diffSummary})
}

// EmitGoalStatusRehydrate re-emits ONE goal_status event for sessionID
// carrying its CURRENT persisted state — reusing the SAME emission call
// afterGoalRecordWrite (above) uses — whenever the session carries an
// active, non-terminal goal, WITH or WITHOUT a registered record. No-op
// (returns false, no event emitted) when there is no active goal at all, or
// the store/session cannot be resolved.
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
// one goal-record lookup, only on attach.
//
// SPA goal-ack-line fix (2026-09-08, frontend wave GX-C): originally this
// returned false outright for the D1 legal-transient empty-record window
// (activation has happened but the working agent has not called `set_goal`
// yet) — the exact window the operator's reported "17 minutes of a silent
// spinner" bug lives in. A reload during that window used to get NO
// goal_status frame at all on reattach, so the SPA's goal-acknowledgement
// line (chat.ts's `case 'goal_status'` — inserted the first time an
// `active` frame is observed for a goal_id) never reconstructed. Emitting
// the SAME criteria-less frame `activateInstantGoal` (goal_loop.go) emits
// at the moment of activation — never inventing state, just re-publishing
// what is already durably persisted on the goal record — closes
// that gap: every reattach while the goal is active, empty record or not,
// now reproduces exactly the live event stream a connection that never
// dropped would have seen.
// ADR-086: "is there an active goal" is now the existence of an ACTIVE
// pkg/goal record bound to this session (activeGoalForSession), and "has a
// record been registered yet" is that record's own criteria list being
// non-empty — the same two questions the retired GoalCondition /
// GoalCriteriaJSON session-meta fields used to answer. A terminal goal is
// still excluded for free: Terminate moves the record out of the active
// state, so activeGoalForSession stops finding it.
func (al *AgentLoop) EmitGoalStatusRehydrate(sessionID string) bool {
	rec := activeGoalForSession(sessionID)
	if rec == nil {
		return false
	}
	g := compiledGoalFromRecord(rec)
	if g == nil {
		// D1 legal-transient empty-record state: activated, but the working
		// agent has not written a record yet. Re-emit the SAME criteria-less
		// frame activateInstantGoal itself emits at activation — this is
		// what lets the SPA's goal-ack line (and the goal-aware thinking
		// indicator) survive a reload that lands inside this window.
		al.emitGoalStatusFrame(
			sessionID, rec.GoalID, rec.Prompt, rec.Round, rec.MaxRounds,
			rec.LatestReason, goalPillActive,
		)
		return true
	}
	al.emitGoalStatusFrameWithCriteriaAndDoD(
		sessionID, rec.GoalID, rec.Prompt, rec.Round, rec.MaxRounds,
		rec.LatestReason, goalPillActive, g.Definition, g.Criteria, g.DoD,
	)
	return true
}
