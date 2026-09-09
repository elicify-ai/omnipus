// Omnipus — set_goal Agent Tool
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — set_goal (ADR-081 D2, work-first-goal-flow-spec FR-004/
// FR-005/FR-006). This is the validated write-path over the goal record: the
// working agent's own first move on a freshly activated goal (D3's forcing
// predicate) or its steering move on an already-registered one. It applies,
// on the way IN, every deterministic guard the old compile-time path applied
// (task.NormalizeCriteria, task.IsValidJudgment, the DoD floor), so nothing
// invalid is ever persisted — a rejected submission leaves the record exactly
// as it was.
//
// pkg/tools cannot import pkg/agent (pkg/agent already imports pkg/tools —
// pkg/agent/goal_compile.go pulls in tools.EffectiveToolPolicy — so the
// reverse import would cycle). Everything this tool needs from pkg/agent's
// goal machinery is therefore either (a) reached through a narrow, late-bound
// seam a wave-2 wiring layer implements (GoalRecordAccess, DiffFn,
// FeasibilityFn — mirroring the AskUserQuestionRegistry / AppendCorrectionFunc
// precedent already established in this package by ask_user_question.go and
// plan_correct.go), or (b) a local, byte-identical mirror of the JSON shape
// pkg/agent's CompiledGoal already defines (setGoalRecord) so a record this
// tool writes deserializes correctly under pkg/agent's own type on load.
// task.AcceptanceCriterion itself needs no mirroring — pkg/task imports
// neither pkg/tools nor pkg/agent, so it is the one shared, non-cyclic type.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// SetGoalToolName is the catalog name (allStaticToolNames member).
const SetGoalToolName = "set_goal"

// SetGoalModeRegister / SetGoalModeUpdate are the tool's `mode` enum values
// (ADR-081 D2), exported so pkg/agent's engine-authored record writes (the
// marker-path activation/restate and the D7 keeper fallback compile) can
// anchor themselves in the transcript as a set_goal call carrying the SAME
// mode vocabulary a real call carries (ADR-082 D9, review CR8).
const (
	SetGoalModeRegister = "register"
	SetGoalModeUpdate   = "update"
)

// setGoalMode is the tool's mode enum (ADR-081 D2).
type setGoalMode string

const (
	// setGoalModeRegister is the default: the agent's first authoring of this
	// goal's record.
	setGoalModeRegister setGoalMode = SetGoalModeRegister
	// setGoalModeUpdate re-validates and REPLACES the record (steering); a
	// change summary is computed against the prior record and returned in
	// the tool result.
	setGoalModeUpdate setGoalMode = SetGoalModeUpdate
)

// SetGoalResultCore is the input to SetGoalResultPayload — everything the
// set_goal success result carries. Assessment is any JSON-marshalable value
// (Execute passes its parsed *setGoalAssessment; pkg/agent's synthetic
// anchors pass a plain map) and is omitted from the payload when nil. Diff
// is emitted only when DiffSet is true (mode:update's change summary — a
// register result never carries a `diff` key, even an empty one).
type SetGoalResultCore struct {
	Mode       string
	GoalID     string
	Definition string
	Criteria   []task.AcceptanceCriterion
	DoD        []task.AcceptanceCriterion
	Assessment any
	Diff       string
	DiffSet    bool
}

// SetGoalResultPayload builds the JSON-shaped success result of a set_goal
// call (ADR-082 D9/FR-016) — the ONE place that shape is defined. Execute
// below returns exactly this (marshaled) on a successful write, and
// pkg/agent's goal_record_wiring.go reuses it verbatim for the synthetic
// set_goal transcript call it appends whenever a goal record is written by
// an engine path that never ran the tool (marker activation, marker restate,
// the D7 fallback compile — review CR8), so the SPA's dedicated set_goal card
// (SetGoalToolUI.tsx's parseSetGoalResult) parses an engine-anchored record
// and a tool-authored one identically.
//
// Shape: mode, goal_id, definition, criteria_count, dod_count, criteria, dod
// [, assessment] [, diff] [, unchanged]. `unchanged` (bool) is added by
// Execute itself, NOT by this function — it is set true only on the
// fix-wave GX-B no-op path (a mode:update, real or normalised from a
// re-register, whose content is semantically identical to the record
// already on disk: see duplicateSubmission). Its absence means false; the
// SPA wave consuming this field should treat a missing key the same as
// `false`. criteria/dod are marshaled as the same
// task.AcceptanceCriterion shape the goal_status frame's own arrays use
// (id/kind/judgment/provenance/text/check/behavior/author/status), so the
// SPA's existing GoalStatusFrame-shaped rendering needs no new parsing
// logic. criteria_count/dod_count are kept alongside the full arrays — the
// calling model reads the counts, the SPA reads the arrays. A nil Criteria/
// DoD slice is emitted as an empty array (never JSON null) so the SPA's
// Array.isArray checks hold.
func SetGoalResultPayload(c SetGoalResultCore) map[string]any {
	criteria := c.Criteria
	if criteria == nil {
		criteria = []task.AcceptanceCriterion{}
	}
	dod := c.DoD
	if dod == nil {
		dod = []task.AcceptanceCriterion{}
	}
	payload := map[string]any{
		"mode":           c.Mode,
		"goal_id":        c.GoalID,
		"definition":     c.Definition,
		"criteria_count": len(criteria),
		"dod_count":      len(dod),
		"criteria":       criteria,
		"dod":            dod,
	}
	if c.Assessment != nil {
		payload["assessment"] = c.Assessment
	}
	if c.DiffSet {
		payload["diff"] = c.Diff
	}
	return payload
}

// setGoalRecord mirrors pkg/agent.CompiledGoal's JSON wire shape (field
// names, field types, omitempty behavior) — see the package doc comment for
// why this is a mirror rather than a type alias. Intent/Prompt are never SET
// by this tool (set_goal's own args are definition/criteria/dod/assessment/
// mode only, per ADR-081 D2 — no intent/prompt argument exists), but they
// ARE carried through unchanged from whatever record already existed on
// this session, so a record produced by an activation-time compile (marker
// path or the D7 fallback, both outside this tool's scope) never loses
// those fields to a later set_goal call.
//
// SupersededCriteria is the ONE field that deliberately does NOT mirror
// pkg/agent.CompiledGoal (fix-wave GX-B "fix 5b" — operator evidence
// 2026-09-08: a judge verdict was rendered against one criteria set, then a
// later set_goal(mode:register) call REPLACED that set 55 seconds later,
// leaving the verdict referencing criteria that exist nowhere). It is
// durable provenance this tool alone manages — never a new WS frame, never
// a contract field, never surfaced in the UI. This is safe left unmirrored
// on CompiledGoal: pkg/agent's own compile paths (activation, the D7
// fallback) always START a fresh record with no history to carry, and
// never re-serialize an EXISTING set_goal-authored record without going
// back through this tool — so this tool carrying the field forward on every
// call (see Execute) is the only place round-tripping needs to happen.
type setGoalRecord struct {
	Intent     string                     `json:"intent"`
	Prompt     string                     `json:"prompt"`
	Definition string                     `json:"definition,omitempty"`
	Criteria   []task.AcceptanceCriterion `json:"criteria"`
	DoD        []task.AcceptanceCriterion `json:"dod,omitempty"`
	// SupersededCriteria is a bounded (maxSupersededCriteriaHistory) history
	// of outgoing (criteria, dod) sets a real mode:update REPLACED, oldest
	// dropped first. Never touched by a no-op duplicate (duplicateSubmission)
	// — only a content-changing update appends to it.
	SupersededCriteria []supersededCriteriaEntry `json:"superseded_criteria,omitempty"`
}

// supersededCriteriaEntry is one entry in setGoalRecord.SupersededCriteria:
// the full outgoing criteria+dod set a mode:update just replaced, and when.
type supersededCriteriaEntry struct {
	Criteria     []task.AcceptanceCriterion `json:"criteria"`
	DoD          []task.AcceptanceCriterion `json:"dod,omitempty"`
	SupersededAt time.Time                  `json:"superseded_at"`
}

// maxSupersededCriteriaHistory bounds setGoalRecord.SupersededCriteria —
// the last N outgoing sets a real update replaced, oldest dropped first.
const maxSupersededCriteriaHistory = 5

// appendSupersededCriteria appends one outgoing (criteria, dod) set to
// history (a fresh copy — the caller's slice is never mutated), capped at
// maxSupersededCriteriaHistory with the oldest entries dropped first.
func appendSupersededCriteria(history []supersededCriteriaEntry, outgoingCriteria, outgoingDoD []task.AcceptanceCriterion) []supersededCriteriaEntry {
	out := make([]supersededCriteriaEntry, 0, len(history)+1)
	out = append(out, history...)
	out = append(out, supersededCriteriaEntry{
		Criteria:     outgoingCriteria,
		DoD:          outgoingDoD,
		SupersededAt: time.Now().UTC(),
	})
	if len(out) > maxSupersededCriteriaHistory {
		out = out[len(out)-maxSupersededCriteriaHistory:]
	}
	return out
}

// setGoalAssessment is the tool's `assessment` arg shape (ADR-081 D2): the
// agent's own confidence read on this first move. It is VALIDATED but never
// persisted into setGoalRecord — it is surfaced in the tool result and
// logged, so "why did it proceed instead of asking" stays answerable from
// the log, without widening the persisted CompiledGoal shape (round-2 B-2 of
// the ADR's grill: persisting it would ripple into loadCompiledGoal, the
// judge feed, and the pre-upgrade path).
type setGoalAssessment struct {
	Clarity     string   `json:"clarity,omitempty"`
	Assumptions []string `json:"assumptions,omitempty"`
}

// GoalRecordAccess is the narrow, late-bound session-store seam set_goal
// needs (ADR-081 D2): read the calling session's active-goal condition and
// current goal record JSON, and durably write a new record. Implemented by
// the wave-2 wiring layer over the real session store (session.MetaPatch's
// GoalCondition / GoalCriteriaJSON fields) — mirroring the
// AskUserQuestionRegistry / AppendCorrectionFunc precedent elsewhere in this
// package: a narrow contract this tool depends on, not the store itself.
type GoalRecordAccess interface {
	// ReadGoalState returns sessionID's current goal id
	// (session.SessionMeta.GoalID; minted server-side at goal activation,
	// the SAME moment GoalCondition is first set — ADR-082 D9/FR-016 reads
	// this so set_goal's result can carry it, letting the SPA key a
	// goal_status live-progress overlay onto the record card it renders
	// from this tool's own result), current goal condition
	// (session.SessionMeta.GoalCondition; "" means no active goal — FR-005's
	// goalless-session refusal reads this) and current goal record JSON
	// (session.SessionMeta.GoalCriteriaJSON; legitimately "" for an active
	// goal with no record registered yet — ADR-081 D1's transient state).
	ReadGoalState(sessionID string) (goalID, goalCondition, recordJSON string, err error)
	// WriteRecord durably persists recordJSON as sessionID's goal record —
	// the same field the compile-time path writes today.
	WriteRecord(sessionID, recordJSON string) error
}

// DiffFn computes a human-readable change summary between the session's
// PRIOR goal record and the NEW one mode:update is about to write, both as
// this tool's own marshaled JSON shape (setGoalRecord). Wave 2 injects the
// real implementation — pkg/agent's diffGoalAmendment (re-homed per
// ADR-081 D2, its one surviving caller), unmarshaling both sides into
// CompiledGoal and rendering the added/changed/dropped diff. A nil seam
// falls back to this tool's own minimal built-in summary (localDiffSummary)
// so mode:update never fails purely because the diff renderer is unwired.
type DiffFn func(oldRecordJSON, newRecordJSON string) (summary string)

// FeasibilityFn vets a criterion ladder for runtime verifiability the same
// way ADR-053's compile-time feasibility gate does (pkg/agent's
// feasibilityGate, over the calling agent's own tool policy — FR-112 never a
// privileged bypass). pkg/tools cannot import pkg/agent, so this is the seam
// wave 2 injects. Returns a non-nil error naming the first infeasible
// criterion (criteria ∪ dod), or nil when every item is feasible.
//
// A nil seam SKIPS vetting rather than failing closed — deliberately unlike
// plan_correct.go's fail-closed unwired-hook precedent: feasibility is one
// guard among several ADR-081 D2 lists (alongside NormalizeCriteria/
// IsValidJudgment/the DoD floor, which this tool enforces unconditionally,
// with no seam and no way to skip them); it is the one guard that depends on
// agent-instance state (tool policy, sandbox) this package cannot reach on
// its own, so it degrades gracefully until wave 2 wires it rather than
// refusing every set_goal call on a fresh install.
type FeasibilityFn func(ctx context.Context, criteria []task.AcceptanceCriterion) error

// SetGoalTool implements the set_goal tool (ADR-081 D2).
type SetGoalTool struct {
	BaseTool
	// accessFn resolves the live GoalRecordAccess per call (late-bound, the
	// AskUserQuestionTool.registryFn precedent: registerSharedTools registers
	// this tool before the gateway constructs the session-store-backed
	// implementation). A nil fn or nil result fails CLOSED — this tool's
	// entire job is writing a record, so an unwired store means it can do
	// nothing at all, exactly like AskUserQuestion's unwired registry.
	accessFn func() GoalRecordAccess
	// diffFn and feasibilityFn are set post-construction (the
	// PlanCorrectTool.SetAppendCorrection precedent) since they are plain
	// funcs, not late-bound registries.
	diffFn        DiffFn
	feasibilityFn FeasibilityFn
}

// NewSetGoalTool constructs a SetGoalTool. accessFn may be nil for the
// metadata catalog (never Execute()d there); Execute fails closed on a nil/
// unwired access seam.
func NewSetGoalTool(accessFn func() GoalRecordAccess) *SetGoalTool {
	return &SetGoalTool{accessFn: accessFn}
}

// SetDiffFn installs the mode:update diff seam (see DiffFn's doc).
func (t *SetGoalTool) SetDiffFn(fn DiffFn) { t.diffFn = fn }

// SetFeasibilityFn installs the feasibility-vetting seam (see FeasibilityFn's
// doc).
func (t *SetGoalTool) SetFeasibilityFn(fn FeasibilityFn) { t.feasibilityFn = fn }

func (t *SetGoalTool) access() GoalRecordAccess {
	if t.accessFn == nil {
		return nil
	}
	return t.accessFn()
}

// Name implements Tool.
func (t *SetGoalTool) Name() string { return SetGoalToolName }

// Scope implements Tool. ScopeGeneral — every human-facing agent (core
// roster, subagent tier that runs owner sessions, custom agents) can author
// its own goal record; the scope preconditions below (delegation depth,
// active-goal requirement) are the real per-call gate, not agent type.
func (t *SetGoalTool) Scope() ToolScope { return ScopeGeneral }

// Category implements Tool.
func (t *SetGoalTool) Category() ToolCategory { return CategoryTasks }

// Description implements Tool.
func (t *SetGoalTool) Description() string {
	return "Register or update THIS session's goal record: the restated goal statement (definition), " +
		"its acceptance criteria, and its Definition of Done (ADR-081). Use mode:register (the default) " +
		"the first time you author this goal's record — your confident first move on a freshly activated " +
		"goal, or after the operator has answered your clarifying questions. Use mode:update to steer an " +
		"already-registered record when the operator's message or your own judgment changes it: the whole " +
		"record is re-validated and REPLACED, and a change summary (what was added/changed/dropped) is " +
		"returned in the result — there is no approval step to wait for. Every criterion and DoD item " +
		"needs an explicit judgment (\"boolean\", \"quantitative\", or \"artifact\") — never omit it and " +
		"expect a default. Leave dod empty to get a built-in floor DoD automatically; anything you do " +
		"supply must also carry an explicit provenance (\"stated\", \"workspace\", \"floor\", or " +
		"\"inferred\"). This tool refuses on a delegated sub-turn (it writes the OWNER session's record " +
		"only) and on a session with no active goal. An invalid submission is rejected with the specific " +
		"violation and nothing is written — fix it and retry."
}

// Parameters implements Tool.
func (t *SetGoalTool) Parameters() map[string]any {
	judgmentEnum := []string{"boolean", "quantitative", "artifact"}
	judgmentDesc := "boolean: a yes/no fact. quantitative: a value against a threshold. " +
		"artifact: a named produced/changed/sent thing whose existence is checkable. Required — " +
		"never omit this expecting a default."
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mode": map[string]any{
				"type": "string",
				"enum": []string{string(setGoalModeRegister), string(setGoalModeUpdate)},
				"description": "register (default): your first authoring of this goal's record — " +
					"the normal choice on a freshly activated goal, or right after the operator answers " +
					"your clarifying questions. update: steering an ALREADY-registered record — " +
					"re-validates and REPLACES it, and a change summary (what was added/changed/dropped) " +
					"is returned in the result. update fails if no record has been registered yet — " +
					"call register first.",
			},
			"definition": map[string]any{
				"type": "string",
				"description": "One clear sentence restating the goal in your own words — the SMART " +
					"restatement shown to the operator as your working assumption. Required.",
			},
			"criteria": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "This goal's acceptance criteria: at least one, required.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text":     map[string]any{"type": "string", "description": "The criterion statement (1-1000 characters)."},
						"judgment": map[string]any{"type": "string", "enum": judgmentEnum, "description": judgmentDesc},
					},
					"required": []string{"text", "judgment"},
				},
			},
			"dod": map[string]any{
				"type": "array",
				"description": "Definition-of-Done standing quality gates. Optional — when omitted or " +
					"empty, a built-in floor DoD is applied automatically. When you DO supply items, " +
					"every one needs text, judgment, AND provenance.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text":     map[string]any{"type": "string", "description": "The DoD statement (1-1000 characters)."},
						"judgment": map[string]any{"type": "string", "enum": judgmentEnum, "description": judgmentDesc},
						"provenance": map[string]any{
							"type": "string",
							"enum": []string{"stated", "workspace", "floor", "inferred"},
							"description": "stated: you are naming this quality gate explicitly. workspace: derived " +
								"from workspace/project instructions. floor: a built-in universal gate — do not use " +
								"this value yourself, the tool applies floor items automatically when dod is empty. " +
								"inferred: bounded inference you are proposing, shown for approval. Required.",
						},
					},
					"required": []string{"text", "judgment", "provenance"},
				},
			},
			"assessment": map[string]any{
				"type": "object",
				"description": "Your confidence assessment for this move. Validated and logged for " +
					"observability, but NOT stored in the goal record itself.",
				"properties": map[string]any{
					"clarity": map[string]any{
						"type":        "string",
						"enum":        []string{"clear", "ambiguous"},
						"description": "Whether the goal was clear enough to register directly.",
					},
					"assumptions": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Assumptions you are making to proceed despite any ambiguity.",
					},
				},
			},
		},
		"required": []string{"definition", "criteria"},
	}
}

// Execute implements Tool. See the file doc comment for the seam contract.
func (t *SetGoalTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	access := t.access()
	if access == nil {
		return ErrorResult("set_goal: no goal-record store is wired on this deployment")
	}

	sessionID := ToolTranscriptSessionID(ctx)
	if sessionID == "" {
		return ErrorResult("set_goal: no session context — this tool needs a real, store-backed session to write a goal record")
	}

	// --- Scope preconditions (ADR-081 D2 / FR-005) — checked BEFORE any
	// payload parsing, so a delegated or goalless caller gets the scope
	// refusal rather than a validation error that might read as "try again
	// with a corrected payload" when no payload could ever succeed here. ---
	if depth := ToolDelegationDepth(ctx); depth > 0 {
		return ErrorResult("set_goal is owner-session-only: a delegated sub-turn cannot author or amend " +
			"the parent session's goal record (ADR-081 FR-005) — report your findings back to the parent instead")
	}

	goalID, goalCondition, currentRecordJSON, err := access.ReadGoalState(sessionID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("set_goal: could not read this session's goal state: %v", err)).WithError(err)
	}
	if strings.TrimSpace(goalCondition) == "" {
		return ErrorResult("set_goal refuses: this session has no active goal — there is nothing to " +
			"register a record against (ADR-081 FR-005)")
	}

	mode, mErr := parseSetGoalMode(args)
	if mErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal rejected: %v", mErr))
	}
	hadExistingRecord := strings.TrimSpace(currentRecordJSON) != ""
	if mode == setGoalModeRegister && hadExistingRecord {
		// fix-wave GX-B (operator-ratified, 2026-09-08 evidence): a SECOND
		// mode:register on a goal that already has a record is never a new
		// goal — it is the agent steering the one it already registered
		// (the operator saw THREE goal cards from exactly this: two
		// byte-identical register calls, then a third register call
		// carrying a real revision). Rejecting the call is what derailed
		// the turn the first time; silently overwriting under a "new goal"
		// label is just as wrong. Normalise to update and let the update
		// path (diff seam, mergeCriterionKindFromOld) run unchanged — the
		// result reports mode:update so the card renders as a revision.
		logger.InfoCF("goal", "set_goal: register normalised to update — a record already exists for this goal",
			map[string]any{"session_id": sessionID})
		mode = setGoalModeUpdate
	}
	if mode == setGoalModeUpdate && !hadExistingRecord {
		return ErrorResult("set_goal(mode:update) refuses: no record has been registered on this goal yet " +
			"— call set_goal(mode:register) first")
	}

	authorID := ToolAgentID(ctx)
	if authorID == "" {
		return ErrorResult("set_goal: no calling-agent identity in context — cannot attribute the record's authorship")
	}

	definition := strings.TrimSpace(argString(args, "definition"))
	if definition == "" {
		return ErrorResult("set_goal rejected: definition is required — one clear sentence restating the goal")
	}

	// Parse whatever record already existed BEFORE building the new one —
	// mode:update's merge logic below (review-round-1 finding #7) needs it
	// for both the criteria/dod Kind carry-over and the omitted-dod
	// carry-forward. Also still used, as before, to carry Intent/Prompt
	// through unchanged (this tool only ever sets Definition/Criteria/DoD).
	var oldRec setGoalRecord
	if strings.TrimSpace(currentRecordJSON) != "" {
		if uErr := json.Unmarshal([]byte(currentRecordJSON), &oldRec); uErr != nil {
			logger.WarnCF("goal", "set_goal: current record JSON failed to parse; proceeding without its prior-record carry-over",
				map[string]any{"session_id": sessionID, "error": uErr.Error()})
			oldRec = setGoalRecord{}
		}
	}

	rawCriteria, _ := args["criteria"].([]any)
	criteria, cErr := parseSetGoalCriteria(rawCriteria, authorID)
	if cErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal rejected: %v", cErr))
	}
	if mode == setGoalModeUpdate {
		// finding #7(a): a criterion whose normalized text matches one in the
		// PRIOR record keeps that prior criterion's Kind and any
		// check/behavior payload — this tool's schema only ever authors
		// KindProse, so without this merge, an agent simply re-submitting a
		// marker-authored machine-verifiable criterion by text (as part of an
		// otherwise-unrelated steering update) would silently downgrade it.
		for i := range criteria {
			criteria[i] = mergeCriterionKindFromOld(criteria[i], oldRec.Criteria)
		}
	}
	normCriteria, nErr := task.NormalizeCriteria(criteria)
	if nErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal rejected: %v", nErr))
	}

	// finding #7(b): distinguish an OMITTED/null `dod` arg from an
	// explicitly EMPTY one ([]) — args["dod"] absent or JSON null means the
	// caller never touched dod at all; args["dod"]:[] means the caller is
	// deliberately clearing it. Only the second case (or register mode,
	// where there is no prior record to carry forward) means "apply the
	// floor here".
	rawDoDVal, dodKeyPresent := args["dod"]
	dodTouched := dodKeyPresent && rawDoDVal != nil
	var rawDoD []any
	if dodTouched {
		rawDoD, _ = rawDoDVal.([]any)
	}
	dod, dErr := parseSetGoalDoD(rawDoD, authorID)
	if dErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal rejected: %v", dErr))
	}

	var normDoD []task.AcceptanceCriterion
	switch {
	case len(dod) > 0:
		if mode == setGoalModeUpdate {
			// Same Kind/check/behavior carry-over as criteria, above.
			for i := range dod {
				dod[i] = mergeCriterionKindFromOld(dod[i], oldRec.DoD)
			}
		}
		normDoD, nErr = task.NormalizeCriteria(dod)
		if nErr != nil {
			return ErrorResult(fmt.Sprintf("set_goal rejected: %v", nErr))
		}
	case mode == setGoalModeUpdate && !dodTouched:
		// finding #7(b): an omitted dod on update means "leave my existing
		// DoD alone" — NOT "replace it with the generic floor". Already
		// normalized/validated from when it was originally written; no
		// NormalizeCriteria pass needed. Only when the carried-forward
		// result is ITSELF empty (a pre-ADR-080 legacy record with no DoD
		// field at all) does the floor backfill apply — the same
		// "result empty → floor" rule mode:register always followed.
		normDoD = oldRec.DoD
		if len(normDoD) == 0 {
			normDoD = setGoalFloorDoD()
		}
	default:
		// register mode with no dod, OR update mode with an EXPLICIT empty
		// dod ([]) — the caller is deliberately (re)applying the floor.
		// ADR-081 D2 — the DoD floor: pkg/agent.newFloorDoD's built-in floor
		// (ADR-080 D-DOD layer 3). Already normalized (fixed sentinel IDs,
		// valid shape) — no NormalizeCriteria pass needed.
		normDoD = setGoalFloorDoD()
	}

	assessment, aErr := parseSetGoalAssessment(args)
	if aErr != nil && !errors.Is(aErr, errAssessmentNotProvided) {
		return ErrorResult(fmt.Sprintf("set_goal rejected: %v", aErr))
	}

	// fix-wave GX-B, rule (b): an update (real or normalised-from-register,
	// above) whose content is SEMANTICALLY IDENTICAL to the record already
	// on disk is a no-op — no write, no diff, no revision bump. Comparison
	// is order-insensitive and ignores criterion IDs (see
	// duplicateSubmission's doc: the model mints a fresh id every call, so
	// two genuinely-identical submissions never share one). Checked only in
	// mode:update — mode:register with no existing record always creates.
	if mode == setGoalModeUpdate && duplicateSubmission(oldRec, definition, normCriteria, normDoD) {
		logger.InfoCF("goal", "set_goal: duplicate submission ignored — record already matches, no write",
			map[string]any{"session_id": sessionID})
		core := SetGoalResultCore{
			Mode:       string(mode),
			GoalID:     goalID,
			Definition: definition,
			Criteria:   normCriteria,
			DoD:        normDoD,
		}
		if assessment != nil {
			core.Assessment = assessment
		}
		payload := SetGoalResultPayload(core)
		// unchanged (documented alongside SetGoalResultPayload's own shape
		// comment): true only on this no-op path, so the SPA can suppress a
		// second card for a call that changed nothing.
		payload["unchanged"] = true
		encoded, payloadErr := json.Marshal(payload)
		if payloadErr != nil {
			logger.ErrorCF("goal", "set_goal: could not encode unchanged result payload",
				map[string]any{"session_id": sessionID, "error": payloadErr.Error()})
			return NewToolResult(fmt.Sprintf("goal record %s: unchanged (%d criteria, %d DoD items)", mode, len(normCriteria), len(normDoD)))
		}
		return NewToolResult(string(encoded))
	}

	if t.feasibilityFn != nil {
		union := make([]task.AcceptanceCriterion, 0, len(normCriteria)+len(normDoD))
		union = append(union, normCriteria...)
		union = append(union, normDoD...)
		if fErr := t.feasibilityFn(ctx, union); fErr != nil {
			return ErrorResult(fmt.Sprintf("set_goal rejected: %v", fErr))
		}
	}

	newRec := setGoalRecord{
		Intent:             oldRec.Intent,
		Prompt:             oldRec.Prompt,
		Definition:         definition,
		Criteria:           normCriteria,
		DoD:                normDoD,
		SupersededCriteria: oldRec.SupersededCriteria,
	}
	if mode == setGoalModeUpdate && (len(oldRec.Criteria) > 0 || len(oldRec.DoD) > 0) {
		// fix 5b (operator-ratified, 2026-09-08 evidence): this is a REAL,
		// content-changing update (the duplicateSubmission no-op already
		// returned above) — the outgoing criteria/dod set oldRec carries is
		// about to be overwritten and, if a judge already rendered a verdict
		// against it, that verdict would otherwise reference criteria that
		// exist nowhere. Preserve it in the bounded history before it's gone.
		newRec.SupersededCriteria = appendSupersededCriteria(oldRec.SupersededCriteria, oldRec.Criteria, oldRec.DoD)
	}
	newJSON, encErr := json.Marshal(newRec)
	if encErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal: failed to encode the goal record: %v", encErr)).WithError(encErr)
	}

	var diffSummary string
	if mode == setGoalModeUpdate {
		if t.diffFn != nil {
			diffSummary = t.diffFn(currentRecordJSON, string(newJSON))
		} else {
			diffSummary = localDiffSummary(currentRecordJSON, string(newJSON))
		}
	}

	if wErr := access.WriteRecord(sessionID, string(newJSON)); wErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal: failed to persist the goal record: %v", wErr)).WithError(wErr)
	}

	logger.InfoCF("goal", "set_goal write",
		map[string]any{"session_id": sessionID, "mode": string(mode),
			"criteria_count": len(normCriteria), "dod_count": len(normDoD)})
	if assessment != nil {
		logger.InfoCF("goal", "set_goal assessment",
			map[string]any{"session_id": sessionID,
				"clarity": assessment.Clarity, "assumption_count": len(assessment.Assumptions)})
	}

	// ADR-082 D9/FR-016: the result carries goal_id and the full registered
	// record (definition/criteria/dod), not just the counts — the SPA's
	// dedicated set_goal tool UI (SetGoalToolUI, live path) and its
	// VirtualAssistantMessageRow replay branch render the goal card
	// directly from THIS result, anchored at this call's own position,
	// instead of the old thread-tail GoalThreadTailCards mount that read
	// only goalPills (populated solely by the goal_status frame). The shape
	// is built by SetGoalResultPayload (the single definition — pkg/agent's
	// engine-anchored synthetic calls reuse it, review CR8).
	core := SetGoalResultCore{
		Mode:       string(mode),
		GoalID:     goalID,
		Definition: definition,
		Criteria:   normCriteria,
		DoD:        normDoD,
		Diff:       diffSummary,
		DiffSet:    mode == setGoalModeUpdate,
	}
	if assessment != nil {
		// Only a non-nil pointer is boxed: a typed-nil *setGoalAssessment
		// inside an `any` would read as non-nil to SetGoalResultPayload and
		// emit `"assessment": null`.
		core.Assessment = assessment
	}
	payload := SetGoalResultPayload(core)
	encoded, payloadErr := json.Marshal(payload)
	if payloadErr != nil {
		logger.ErrorCF("goal", "set_goal: could not encode result payload",
			map[string]any{"session_id": sessionID, "error": payloadErr.Error()})
		return NewToolResult(fmt.Sprintf("goal record %s: %d criteria, %d DoD items", mode, len(normCriteria), len(normDoD)))
	}
	return NewToolResult(string(encoded))
}

// parseSetGoalMode reads and validates the `mode` arg, defaulting to
// register when absent.
func parseSetGoalMode(args map[string]any) (setGoalMode, error) {
	raw, present := stringArg(args, "mode")
	if !present || strings.TrimSpace(raw) == "" {
		return setGoalModeRegister, nil
	}
	switch setGoalMode(strings.TrimSpace(raw)) {
	case setGoalModeRegister:
		return setGoalModeRegister, nil
	case setGoalModeUpdate:
		return setGoalModeUpdate, nil
	default:
		return "", fmt.Errorf("mode %q is not \"register\" or \"update\"", raw)
	}
}

// mergeCriterionKindFromOld is review-round-1 finding #7(a)'s fix: this
// tool's own Parameters() schema only ever authors KindProse — criteria
// items offer only text+judgment, dod items only text+judgment+provenance,
// with no check/behavior input shape at all. On mode:update, hardcoding
// KindProse on every submitted item (parseSetGoalCriteria/parseSetGoalDoD,
// both above) would silently DOWNGRADE a marker-authored machine-verifiable
// item (Kind check or behavior, carrying a Check or Behavior payload) the
// instant the agent merely re-submits it by text as part of an otherwise-
// unrelated steering update — losing its verifiability with no error, no
// warning, and nothing in the diff summary to flag it.
//
// When newCrit's normalized text matches an item in oldPool, the OLD Kind
// and Check/Behavior payload are carried onto the new item; every other
// field (Judgment, Text, Author, Provenance, Status) stays exactly what the
// caller submitted — task.NormalizeCriteria still validates the merged
// result afterward, so a caller-submitted Judgment genuinely incompatible
// with the carried-over Kind (e.g. "quantitative" for a check item, which
// is always boolean) is still rejected as a validation error, not silently
// coerced.
//
// Matching is TEXT-only, never ID: this tool's schema has no id input field
// for criteria or dod items (see Parameters()), so the caller never has an
// old ID to submit in the first place — there is nothing to match on but
// the restated text.
func mergeCriterionKindFromOld(newCrit task.AcceptanceCriterion, oldPool []task.AcceptanceCriterion) task.AcceptanceCriterion {
	normNew := strings.ToLower(strings.TrimSpace(newCrit.Text))
	for _, old := range oldPool {
		if strings.ToLower(strings.TrimSpace(old.Text)) != normNew {
			continue
		}
		if old.Kind == task.KindCheck || old.Kind == task.KindBehavior {
			newCrit.Kind = old.Kind
			newCrit.Check = old.Check
			newCrit.Behavior = old.Behavior
		}
		break
	}
	return newCrit
}

// parseSetGoalCriteria decodes and shape-checks the `criteria` arg. Judgment
// is REQUIRED and explicit on every item — this tool never lets
// task.InferJudgment's silent "empty defaults to boolean" apply; an omitted
// or empty judgment is rejected here, before task.NormalizeCriteria ever
// sees it.
func parseSetGoalCriteria(raw []any, authorID string) ([]task.AcceptanceCriterion, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("criteria is required, at least one — a goal record with no acceptance criteria cannot be judged")
	}
	out := make([]task.AcceptanceCriterion, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("criteria[%d]: must be an object", i)
		}
		text := strings.TrimSpace(argString(m, "text"))
		if text == "" {
			return nil, fmt.Errorf("criteria[%d]: text is required", i)
		}
		judgment, jErr := requireExplicitJudgment(m, fmt.Sprintf("criteria[%d]", i))
		if jErr != nil {
			return nil, jErr
		}
		out = append(out, task.AcceptanceCriterion{
			Kind:     task.KindProse,
			Judgment: judgment,
			Text:     text,
			Author:   task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: authorID},
		})
	}
	return out, nil
}

// parseSetGoalDoD decodes and shape-checks the `dod` arg. An empty/absent
// dod is legal (the caller backfills the floor); a NON-empty dod requires
// text, judgment, AND provenance on every item — provenance is authority-
// layer metadata (ADR-080 D-DOD) and, like judgment, this tool requires it
// explicit rather than silently defaulting to the empty/unlabeled state.
func parseSetGoalDoD(raw []any, authorID string) ([]task.AcceptanceCriterion, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]task.AcceptanceCriterion, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("dod[%d]: must be an object", i)
		}
		text := strings.TrimSpace(argString(m, "text"))
		if text == "" {
			return nil, fmt.Errorf("dod[%d]: text is required", i)
		}
		judgment, jErr := requireExplicitJudgment(m, fmt.Sprintf("dod[%d]", i))
		if jErr != nil {
			return nil, jErr
		}
		provRaw, hasProv := stringArg(m, "provenance")
		provenance := task.CriterionProvenance(strings.TrimSpace(provRaw))
		if !hasProv || provenance == "" {
			return nil, fmt.Errorf("dod[%d]: provenance is required (must be \"stated\", \"workspace\", \"floor\", or \"inferred\")", i)
		}
		if !task.IsValidCriterionProvenance(provenance) {
			return nil, fmt.Errorf("dod[%d]: invalid provenance %q (must be \"stated\", \"workspace\", \"floor\", or \"inferred\")", i, provenance)
		}
		out = append(out, task.AcceptanceCriterion{
			Kind:       task.KindProse,
			Judgment:   judgment,
			Provenance: provenance,
			Text:       text,
			Author:     task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: authorID},
		})
	}
	return out, nil
}

// requireExplicitJudgment reads the `judgment` field of m and requires it to
// be present, non-empty, and a member of task's valid judgment enum. field
// names the enclosing item for the error message (e.g. "criteria[2]").
func requireExplicitJudgment(m map[string]any, field string) (task.JudgmentKind, error) {
	raw, present := stringArg(m, "judgment")
	judgment := task.JudgmentKind(strings.TrimSpace(raw))
	if !present || judgment == "" {
		return "", fmt.Errorf("%s: judgment is required (must be \"boolean\", \"quantitative\", or \"artifact\")", field)
	}
	if !task.IsValidJudgment(judgment) {
		return "", fmt.Errorf("%s: invalid judgment %q (must be \"boolean\", \"quantitative\", or \"artifact\")", field, judgment)
	}
	return judgment, nil
}

// errAssessmentNotProvided is parseSetGoalAssessment's sentinel for "the
// caller simply did not supply an assessment argument" (lint: nilnil —
// distinguishes an intentionally-absent OPTIONAL arg from a genuine parse
// failure, instead of overloading a bare (nil, nil) return that a `nilnil`
// linter — rightly — cannot tell apart from "forgot to handle an error
// case"). Every caller of parseSetGoalAssessment must treat this sentinel as
// non-fatal (errors.Is check), exactly as the old nil-error case was.
var errAssessmentNotProvided = errors.New("set_goal: no assessment argument provided")

// parseSetGoalAssessment decodes and validates the optional `assessment`
// arg. Never persisted (see setGoalAssessment's doc) — this is its only
// consumer besides Execute's own result-payload/log emission.
func parseSetGoalAssessment(args map[string]any) (*setGoalAssessment, error) {
	raw, present := args["assessment"]
	if !present || raw == nil {
		return nil, errAssessmentNotProvided
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("assessment must be an object")
	}
	a := &setGoalAssessment{}
	if clarityRaw, hasClarity := stringArg(m, "clarity"); hasClarity && strings.TrimSpace(clarityRaw) != "" {
		clarity := strings.TrimSpace(clarityRaw)
		if clarity != "clear" && clarity != "ambiguous" {
			return nil, fmt.Errorf("assessment.clarity must be \"clear\" or \"ambiguous\", got %q", clarity)
		}
		a.Clarity = clarity
	}
	assumptions, saErr := stringSliceArg(m, "assumptions")
	if saErr != nil {
		return nil, fmt.Errorf("assessment.%w", saErr)
	}
	a.Assumptions = assumptions
	return a, nil
}

// setGoalDoDFloorAuthorID mirrors pkg/agent's goalDoDFloorAuthorID sentinel
// exactly.
const setGoalDoDFloorAuthorID = "system"

// setGoalFloorDoD constructs ADR-080 D-DOD's built-in floor Definition of
// Done (layer 3), re-implemented here per ADR-081 D2 because pkg/tools
// cannot import pkg/agent (see the package doc comment). Its two entries —
// ids, kind, judgment, provenance, and text — MUST stay byte-identical to
// pkg/agent/goal_compile.go's newFloorDoD, so a record floor-backfilled by
// this tool and one floor-backfilled by pkg/agent's own load-time backfill
// (loadCompiledGoal) are indistinguishable on disk.
func setGoalFloorDoD() []task.AcceptanceCriterion {
	author := task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: setGoalDoDFloorAuthorID}
	return []task.AcceptanceCriterion{
		{
			ID: "goal-dod-floor-no-secrets", Kind: task.KindProse,
			Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
			Text:   "No secrets or credentials appear in the output.",
			Author: author, Status: task.CritPending,
		},
		{
			ID: "goal-dod-floor-grounded-claims", Kind: task.KindProse,
			Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
			Text:   "Every factual claim is grounded, not assumed.",
			Author: author, Status: task.CritPending,
		},
	}
}

// localDiffSummary is the built-in mode:update change summary used whenever
// no DiffFn seam is wired (see DiffFn's doc). It compares the Criteria and
// DoD ladders of the two JSON-encoded setGoalRecord values by normalized
// text, reporting added/changed(judgment or provenance differs)/dropped
// counts for each ladder. oldJSON may legitimately be "" only when called
// from a context that already guarantees a prior record exists (mode:update
// refuses otherwise) — an unparseable non-empty oldJSON degrades to an
// additive-only summary rather than failing the write, which has already
// succeeded by the time this is rendered.
func localDiffSummary(oldJSON, newJSON string) string {
	var oldRec, newRec setGoalRecord
	if strings.TrimSpace(oldJSON) != "" {
		if err := json.Unmarshal([]byte(oldJSON), &oldRec); err != nil {
			logger.WarnCF("goal", "set_goal: could not parse the prior record JSON for diffing; showing an additive-only summary",
				map[string]any{"error": err.Error()})
		}
	}
	if err := json.Unmarshal([]byte(newJSON), &newRec); err != nil {
		logger.ErrorCF("goal", "set_goal: could not parse its own freshly-encoded record JSON for diffing",
			map[string]any{"error": err.Error()})
	}
	critAdded, critChanged, critDropped := diffCriteriaByText(oldRec.Criteria, newRec.Criteria)
	dodAdded, dodChanged, dodDropped := diffCriteriaByText(oldRec.DoD, newRec.DoD)
	return fmt.Sprintf(
		"criteria: +%d added, ~%d changed, -%d dropped; dod: +%d added, ~%d changed, -%d dropped",
		len(critAdded), len(critChanged), len(critDropped),
		len(dodAdded), len(dodChanged), len(dodDropped),
	)
}

// diffCriteriaByText is localDiffSummary's set-diff: matched by normalized
// text, "changed" when judgment or provenance differs on a text match.
func diffCriteriaByText(oldCriteria, newCriteria []task.AcceptanceCriterion) (added, changed, dropped []task.AcceptanceCriterion) {
	oldByText := make(map[string]task.AcceptanceCriterion, len(oldCriteria))
	for _, c := range oldCriteria {
		oldByText[strings.ToLower(strings.TrimSpace(c.Text))] = c
	}
	seen := make(map[string]bool, len(newCriteria))
	for _, c := range newCriteria {
		key := strings.ToLower(strings.TrimSpace(c.Text))
		seen[key] = true
		existing, ok := oldByText[key]
		if !ok {
			added = append(added, c)
			continue
		}
		if existing.Judgment != c.Judgment || existing.Provenance != c.Provenance {
			changed = append(changed, c)
		}
	}
	for _, c := range oldCriteria {
		key := strings.ToLower(strings.TrimSpace(c.Text))
		if !seen[key] {
			dropped = append(dropped, c)
		}
	}
	return added, changed, dropped
}

// duplicateSubmission reports whether the record Execute is about to write
// (definition/newCriteria/newDoD, already normalized) is semantically
// identical to old — fix-wave GX-B rule (b), evidence: two set_goal(mode:
// register) calls in the SAME model response with byte-identical params but
// DIFFERENT criterion ids (the model mints a fresh one every call) produced
// two goal cards for no real change.
//
// Comparison is deliberately narrow and deliberately excludes three fields:
//   - ID: never submitted by the caller (this tool's schema has no id
//     input for criteria/dod items — see Parameters()) and freshly minted
//     by task.NormalizeCriteria on every call, so it can never match across
//     two calls even for genuinely identical content.
//   - Author: reconstructed from ToolAgentID(ctx) on every call; comparing
//     it would only ever compare the calling agent to itself.
//   - Status: reset to pending on every fresh submission (this tool never
//     accepts a status input) regardless of whatever a judge has since done
//     to the item ON DISK. Comparing it would treat a criterion the judge
//     has already progressed as "different" purely because of that
//     progress, forcing a real rewrite that resets it back to pending —
//     the opposite of what "a no-op leaves the record alone" means.
func duplicateSubmission(old setGoalRecord, newDefinition string, newCriteria, newDoD []task.AcceptanceCriterion) bool {
	if strings.TrimSpace(old.Definition) != strings.TrimSpace(newDefinition) {
		return false
	}
	return criteriaSetEqual(old.Criteria, newCriteria) && criteriaSetEqual(old.DoD, newDoD)
}

// criteriaSetEqual reports whether a and b carry the same MULTISET of
// criterion content (see duplicateSubmission for what "content" excludes),
// independent of order — the operator's two identical register calls had
// their criteria in matching order, but nothing about the tool's contract
// guarantees that in general.
func criteriaSetEqual(a, b []task.AcceptanceCriterion) bool {
	if len(a) != len(b) {
		return false
	}
	aSigs, bSigs := criteriaSignatures(a), criteriaSignatures(b)
	sort.Strings(aSigs)
	sort.Strings(bSigs)
	for i := range aSigs {
		if aSigs[i] != bSigs[i] {
			return false
		}
	}
	return true
}

// criteriaContentSignature is the comparable content of a single criterion
// for duplicateSubmission — everything EXCEPT ID/Author/Status (see its
// doc). Its own type (rather than reusing task.AcceptanceCriterion
// directly) is what makes that exclusion explicit and impossible to widen
// by accident when AcceptanceCriterion itself gains a field.
type criteriaContentSignature struct {
	Kind       task.CriterionKind       `json:"kind"`
	Judgment   task.JudgmentKind        `json:"judgment"`
	Provenance task.CriterionProvenance `json:"provenance,omitempty"`
	Text       string                   `json:"text"`
	Check      *task.CriterionCheck     `json:"check,omitempty"`
	Behavior   *task.CriterionBehavior  `json:"behavior,omitempty"`
}

// criteriaSignatures renders each item's comparable content (see
// criteriaContentSignature) as a stable, order-independent-comparable
// string.
func criteriaSignatures(items []task.AcceptanceCriterion) []string {
	out := make([]string, len(items))
	for i, c := range items {
		sig := criteriaContentSignature{
			Kind:       c.Kind,
			Judgment:   c.Judgment,
			Provenance: c.Provenance,
			Text:       strings.TrimSpace(c.Text),
			Check:      c.Check,
			Behavior:   c.Behavior,
		}
		encoded, err := json.Marshal(sig)
		if err != nil {
			// criteriaContentSignature is a plain data struct (no funcs,
			// channels, or cyclic pointers) — Marshal cannot fail on it in
			// practice. If it somehow does, fail the comparison TOWARD a
			// real write rather than a false no-op: a signature keyed off
			// this item's own address can never equal another item's
			// (including an item at the same index on the OTHER side of
			// the comparison).
			out[i] = fmt.Sprintf("set_goal:unencodable-signature:%p", &items[i])
			continue
		}
		out[i] = string(encoded)
	}
	return out
}
