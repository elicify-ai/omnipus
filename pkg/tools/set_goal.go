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
	"fmt"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// SetGoalToolName is the catalog name (allStaticToolNames member).
const SetGoalToolName = "set_goal"

// setGoalMode is the tool's mode enum (ADR-081 D2).
type setGoalMode string

const (
	// setGoalModeRegister is the default: the agent's first authoring of this
	// goal's record.
	setGoalModeRegister setGoalMode = "register"
	// setGoalModeUpdate re-validates and REPLACES the record (steering); a
	// change summary is computed against the prior record and returned in
	// the tool result.
	setGoalModeUpdate setGoalMode = "update"
)

// setGoalRecord mirrors pkg/agent.CompiledGoal's JSON wire shape exactly
// (field names, field types, omitempty behavior) — see the package doc
// comment for why this is a mirror rather than a type alias. Intent/Prompt
// are never SET by this tool (set_goal's own args are definition/criteria/
// dod/assessment/mode only, per ADR-081 D2 — no intent/prompt argument
// exists), but they ARE carried through unchanged from whatever record
// already existed on this session, so a record produced by an
// activation-time compile (marker path or the D7 fallback, both outside this
// tool's scope) never loses those fields to a later set_goal call.
type setGoalRecord struct {
	Intent     string                     `json:"intent"`
	Prompt     string                     `json:"prompt"`
	Definition string                     `json:"definition,omitempty"`
	Criteria   []task.AcceptanceCriterion `json:"criteria"`
	DoD        []task.AcceptanceCriterion `json:"dod,omitempty"`
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
	// ReadGoalState returns sessionID's current goal condition
	// (session.SessionMeta.GoalCondition; "" means no active goal — FR-005's
	// goalless-session refusal reads this) and current goal record JSON
	// (session.SessionMeta.GoalCriteriaJSON; legitimately "" for an active
	// goal with no record registered yet — ADR-081 D1's transient state).
	ReadGoalState(sessionID string) (goalCondition, recordJSON string, err error)
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
				"description": "register (default): first authoring of this goal's record — fails if " +
					"there is nothing to update yet, use register instead. update: steering — " +
					"re-validate and REPLACE the record; a change summary is returned in the result. " +
					"update fails if no record has been registered yet.",
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

	goalCondition, currentRecordJSON, err := access.ReadGoalState(sessionID)
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
	if mode == setGoalModeUpdate && strings.TrimSpace(currentRecordJSON) == "" {
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

	rawCriteria, _ := args["criteria"].([]any)
	criteria, cErr := parseSetGoalCriteria(rawCriteria, authorID)
	if cErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal rejected: %v", cErr))
	}
	normCriteria, nErr := task.NormalizeCriteria(criteria)
	if nErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal rejected: %v", nErr))
	}

	rawDoD, _ := args["dod"].([]any)
	dod, dErr := parseSetGoalDoD(rawDoD, authorID)
	if dErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal rejected: %v", dErr))
	}
	var normDoD []task.AcceptanceCriterion
	if len(dod) == 0 {
		// ADR-081 D2 — the DoD floor: a validated dod set that comes back
		// empty is backfilled with the same built-in floor
		// pkg/agent.newFloorDoD guarantees (ADR-080 D-DOD layer 3). Already
		// normalized (fixed sentinel IDs, valid shape) — no NormalizeCriteria
		// pass needed.
		normDoD = setGoalFloorDoD()
	} else {
		normDoD, nErr = task.NormalizeCriteria(dod)
		if nErr != nil {
			return ErrorResult(fmt.Sprintf("set_goal rejected: %v", nErr))
		}
	}

	assessment, aErr := parseSetGoalAssessment(args)
	if aErr != nil {
		return ErrorResult(fmt.Sprintf("set_goal rejected: %v", aErr))
	}

	if t.feasibilityFn != nil {
		union := make([]task.AcceptanceCriterion, 0, len(normCriteria)+len(normDoD))
		union = append(union, normCriteria...)
		union = append(union, normDoD...)
		if fErr := t.feasibilityFn(ctx, union); fErr != nil {
			return ErrorResult(fmt.Sprintf("set_goal rejected: %v", fErr))
		}
	}

	// Carry Intent/Prompt through from whatever record already existed (see
	// setGoalRecord's doc comment) — this tool only ever sets
	// Definition/Criteria/DoD.
	var oldRec setGoalRecord
	if strings.TrimSpace(currentRecordJSON) != "" {
		if uErr := json.Unmarshal([]byte(currentRecordJSON), &oldRec); uErr != nil {
			slog.Warn("set_goal: current record JSON failed to parse; proceeding without its intent/prompt carry-over",
				"component", "goal", "session_id", sessionID, "error", uErr)
			oldRec = setGoalRecord{}
		}
	}
	newRec := setGoalRecord{
		Intent:     oldRec.Intent,
		Prompt:     oldRec.Prompt,
		Definition: definition,
		Criteria:   normCriteria,
		DoD:        normDoD,
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

	slog.Info("goal: set_goal write",
		"component", "goal", "session_id", sessionID, "mode", string(mode),
		"criteria_count", len(normCriteria), "dod_count", len(normDoD))
	if assessment != nil {
		slog.Info("goal: set_goal assessment",
			"component", "goal", "session_id", sessionID,
			"clarity", assessment.Clarity, "assumption_count", len(assessment.Assumptions))
	}

	payload := map[string]any{
		"mode":           string(mode),
		"definition":     definition,
		"criteria_count": len(normCriteria),
		"dod_count":      len(normDoD),
	}
	if assessment != nil {
		payload["assessment"] = assessment
	}
	if mode == setGoalModeUpdate {
		payload["diff"] = diffSummary
	}
	encoded, payloadErr := json.Marshal(payload)
	if payloadErr != nil {
		slog.Error("set_goal: could not encode result payload", "session_id", sessionID, "error", payloadErr)
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

// parseSetGoalAssessment decodes and validates the optional `assessment`
// arg. Never persisted (see setGoalAssessment's doc) — this is its only
// consumer besides Execute's own result-payload/log emission.
func parseSetGoalAssessment(args map[string]any) (*setGoalAssessment, error) {
	raw, present := args["assessment"]
	if !present || raw == nil {
		return nil, nil
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
			slog.Warn("set_goal: could not parse the prior record JSON for diffing; showing an additive-only summary",
				"component", "goal", "error", err)
		}
	}
	if err := json.Unmarshal([]byte(newJSON), &newRec); err != nil {
		slog.Error("set_goal: could not parse its own freshly-encoded record JSON for diffing", "component", "goal", "error", err)
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
