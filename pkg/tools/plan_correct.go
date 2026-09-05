// Omnipus — plan_correct Agent Tool
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — plan_correct (ADR-055, plan-supervisor-spec FR-004/FR-046,
// US-1/US-2/US-3). This is the agent-facing surface over the engine's
// AppendCorrection, which has been fully implemented but had ZERO non-test
// callers — the entire reason this tool exists.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/google/uuid"
)

// PlanSupervisorAgentID is the exact agent identity that holds correction
// authority (FR-009). The gate is on this literal, NOT on the agent's
// `Type == system`: a future System Agent must not silently inherit
// correction rights by being a System Agent. Its soundness rests on the id
// being unclaimable, which it is — agent ids are server-minted UUIDs and a
// `{"type":"system"}` create body is rejected 400 (FR-049 / N12).
//
// The seeding layer (pkg/coreagent) declares the same id as its own System
// Agent constant; the two MUST be equal, and the equality belongs in a
// pkg/coreagent test (that package already imports pkg/tools in tests, while
// pkg/tools must not import pkg/coreagent).
const PlanSupervisorAgentID = "plansupervisor"

// correctionDeniedMessage is the SINGLE opaque denial every non-PlanSupervisor
// caller receives (FR-010). It deliberately:
//   - does not name the plan id, so responses for different plan ids are
//     byte-identical (FR-010 clause 2);
//   - does not distinguish "not permitted" from "no such plan", because the
//     identity precheck runs BEFORE any store read, closing the existence
//     oracle that would otherwise sit in front of the gate (FR-010 clause 3);
//   - does not name the authorised holder, so a denied caller learns nothing
//     about who may correct (the same opaque-denial property the engine's
//     requireOwner gate has, sec-MAJOR-2).
const correctionDeniedMessage = "plan_correct denied: this caller is not permitted to correct plans"

// Payload caps (FR-046 / D-06). The authoritative constants live in pkg/plan
// so that this boundary and the engine's own validateCorrection enforce one
// number, not two that happen to agree today; these are local spellings of
// them, not copies. Changing pkg/plan's value changes both validators.
const (
	maxTailMembers      = plan.MaxTailMembers
	maxTailEdges        = plan.MaxTailEdges
	maxMemberTitleBytes = plan.MaxMemberTitleBytes
	maxTextBytes        = plan.MaxTextBytes
)

// Task-store limits a tail member must also satisfy. A payload that passes
// FR-046's caps but violates these would be rejected by task.Store.Create
// INSIDE the engine's transactional intent-log commit — i.e. a mid-commit
// abort — so the tool enforces them up front, before any mutation.
const (
	taskStoreMaxTitleRunes       = 200
	taskStoreMaxDescriptionBytes = 2000
)

// CorrectionCaller and CorrectionRequest are the engine's own correction
// types, re-exported here as aliases (FR-004). They are declared in pkg/plan
// and aliased identically from pkg/agent — the plan.IntentEdge precedent —
// because pkg/tools cannot import pkg/agent (pkg/agent already imports
// pkg/tools, a cycle) yet this tool must hand the engine the very type it
// consumes. Aliases, not copies: a value built here IS the engine's value,
// with no conversion and no shape that can drift.
//
// not-wire-format: tool/engine-internal; never crosses the gateway boundary.
type (
	CorrectionCaller  = plan.CorrectionCaller
	CorrectionRequest = plan.CorrectionRequest
)

// AppendCorrectionFunc applies a validated correction to a running plan. It
// mirrors *pkg/agent.PlanEngine.AppendCorrection, injected as a func value
// rather than called directly because pkg/tools must not import pkg/agent.
// The wiring layer supplies it.
//
// It returns the minted revision id and whether the correction took the
// honest-exit path (the plan could still make no progress afterwards and was
// failed rather than left to livelock).
type AppendCorrectionFunc func(ctx context.Context, planID string, caller CorrectionCaller, req CorrectionRequest) (revisionID string, honestExit bool, err error)

// PlanCorrectTool implements the plan_correct agent tool (FR-004, US-1/2/3).
//
// Authority is PlanSupervisor and only PlanSupervisor — the exact inverse of
// stop_plan, whose authority is the plan's owner. The adjudicator corrects;
// the owner contains. The identity precheck runs before ANY store access, so
// a non-holder cannot use this tool as a plan-existence oracle (FR-010).
//
// The tool validates the whole payload before calling the engine (FR-046):
// verb enum, the verb/field compatibility matrix, both collection caps, the
// text/title byte caps, edge endpoint resolution, edge acyclicity, and the
// supersede pairing + criteria-inheritance rules. Nothing mutates until every
// rule passes, so a rejected correction leaves the plan in exactly the state
// that produced the supervision wake.
type PlanCorrectTool struct {
	BaseTool
	planStore *plan.Store
	taskStore *task.Store
	// appendCorrection is the engine hook. FAIL CLOSED when unwired: an
	// unwired hook is a configuration error, never a silent success — the
	// discipline TaskRunTool.startTaskNow and PlanCreateTool.validateOwner
	// both document verbatim.
	appendCorrection AppendCorrectionFunc
}

// NewPlanCorrectTool constructs a PlanCorrectTool. Either store may be nil
// for metadata-only construction (GeneralBuiltinMetadata) — never Execute()d
// in that mode.
func NewPlanCorrectTool(planStore *plan.Store, taskStore *task.Store) *PlanCorrectTool {
	return &PlanCorrectTool{planStore: planStore, taskStore: taskStore}
}

// SetAppendCorrection installs the engine hook (see the field doc).
func (t *PlanCorrectTool) SetAppendCorrection(fn AppendCorrectionFunc) {
	t.appendCorrection = fn
}

func (t *PlanCorrectTool) Name() string           { return "plan_correct" }
func (t *PlanCorrectTool) Scope() ToolScope       { return ScopeCore }
func (t *PlanCorrectTool) Category() ToolCategory { return CategoryTasks }

func (t *PlanCorrectTool) Description() string {
	return "Correct a running plan that has parked for adjudication (phase awaiting_supervision) or " +
		"stalled with no dispatchable member — use this when a plan looks stuck and needs to be " +
		"unstuck. Only the plan supervisor may call this; every other caller is refused. Order new " +
		"work with optional tail_edges (from/to naming an existing member id or a tail member's ref " +
		"from this same call) — the graph must stay acyclic and no edge may touch the member being " +
		"superseded. Each verb accepts only its own fields: passing a field a verb does not take is " +
		"rejected outright, not ignored. Four verbs: append (add tail work), supersede (mark a " +
		"done member's outcome ignored by the judge — REQUIRES at least one replacement tail member; " +
		"the system automatically carries every acceptance criterion of the superseded member onto " +
		"your replacement work, so you do not need to know or restate its exact criteria — just " +
		"describe the real replacement work, and add any further criteria you want it held to), " +
		"targeted_retry (reset one failed member — REQUIRES retried_member_id), and abandon (the " +
		"honest exit — the Definition of Done is unreachable from here; the plan terminates " +
		"dod_unreachable with your falsified assumption on the record). The Definition of Done is " +
		"immutable and cannot be edited by any verb. NEW tail_members' ids are minted by the system, " +
		"never supplied by you — but superseded_member_id and retried_member_id name an EXISTING " +
		"member and MUST be that member's real id from the plan's member list, never a label like " +
		"\"m2\" or a title. Corrections consume the plan's existing judge-round budget; they do not " +
		"get a separate one."
}

func (t *PlanCorrectTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan_id": map[string]any{
				"type":        "string",
				"description": "ID of the running plan to correct",
			},
			"verb": map[string]any{
				"type": "string",
				"enum": []string{
					string(plan.RevisionAppend),
					string(plan.RevisionSupersede),
					string(plan.RevisionTargetedRetry),
					string(plan.RevisionAbandon),
				},
				"description": "append: add tail work. supersede: mark a done member's outcome " +
					"ignored by the judge (requires replacement tail work). targeted_retry: reset " +
					"one failed member. abandon: the Definition of Done is unreachable — terminate " +
					"the plan honestly instead of burning the remaining round budget.",
			},
			"falsified_assumption": map[string]any{
				"type": "string",
				"description": "REQUIRED: the assumption this plan made that turned out to be wrong. " +
					"This is the diagnosis the correction rests on and it is recorded on the revision entry.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Optional free-text detail supporting the correction.",
			},
			"superseded_member_id": map[string]any{
				"type": "string",
				"description": "Required for supersede: a done member OF THIS PLAN whose outcome the " +
					"judge should ignore. Rejected for every other verb.",
			},
			"retried_member_id": map[string]any{
				"type": "string",
				"description": "Required for targeted_retry: a failed member OF THIS PLAN to reset. " +
					"Rejected for every other verb.",
			},
			"tail_members": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ref": map[string]any{
							"type": "string",
							"description": "Optional label used ONLY to name this member from tail_edges " +
								"in this same call. It is not an id and is not stored — the system mints " +
								"the real member id. Must be unique in this call and must not equal an " +
								"existing member's id.",
						},
						"title": map[string]any{
							"type":        "string",
							"description": "Short title for the new member task (1-200 characters).",
						},
						"description": map[string]any{
							"type":        "string",
							"description": "What this member must do (up to 2000 characters).",
						},
						"criteria": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"kind": map[string]any{
										"type": "string",
										"enum": []string{"check", "prose", "behavior"},
										"description": "check: a shell command verified via the assignee's bash tool; " +
											"prose: a free-text statement judged by the verifier; " +
											"behavior: a deterministic count of successful calls of a named tool " +
											"in the session's tool-call log",
									},
									"text": map[string]any{
										"type":        "string",
										"description": "The criterion statement (1-1000 characters)",
									},
									"check": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"command":            map[string]any{"type": "string", "description": "Shell command to run"},
											"expected_exit_code": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
										},
										"description": "Required when kind is \"check\"; must be omitted for other kinds",
									},
									"behavior": behaviorCriterionSchema(),
								},
								"required": []string{"kind", "text"},
							},
							"description": "REQUIRED, at least one: this member's acceptance criteria describing " +
								"the REPLACEMENT work itself. For a supersede, you do not need to know or " +
								"restate the superseded member's own criteria — the system carries those onto " +
								"the replacement automatically; add criteria here for whatever ADDITIONAL " +
								"standard you want the real replacement work held to.",
						},
					},
					"required": []string{"title", "criteria"},
				},
				"description": fmt.Sprintf(
					"New member tasks to add. Required (at least one) for append AND for supersede. "+
						"Rejected outright for targeted_retry and abandon. At most %d.", maxTailMembers),
			},
			"tail_edges": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from": map[string]any{
							"type":        "string",
							"description": "Member that must complete first: an existing member id of this plan, or a tail member's ref from this call.",
						},
						"to": map[string]any{
							"type":        "string",
							"description": "Member that waits: an existing member id of this plan, or a tail member's ref from this call.",
						},
					},
					"required": []string{"from", "to"},
				},
				"description": fmt.Sprintf(
					"Dependency edges to wire. Optional on append and supersede, rejected for "+
						"targeted_retry and abandon. The resulting graph must be acyclic. At most %d.", maxTailEdges),
			},
		},
		"required": []string{"plan_id", "verb", "falsified_assumption"},
	}
}

func (t *PlanCorrectTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	planID, _ := args["plan_id"].(string)
	callerID := ToolAgentID(ctx)

	// --- Identity precheck (FR-009 / FR-010 clause 3) --------------------
	// This runs BEFORE the plan store is touched, on purpose. AppendCorrection
	// loads the plan before its own owner gate, so a non-holder naming a
	// nonexistent plan gets a store error while a non-holder naming a real one
	// gets a permission error — a perfect existence oracle sitting in front of
	// the gate. Prechecking identity here needs no plan state at all (it is a
	// comparison against a constant), so the oracle closes with no reordering
	// of anything transactional. The plan id goes to the server-side log only.
	if callerID != PlanSupervisorAgentID {
		slog.Warn("plan_correct: denied — caller is not the plan supervisor",
			"caller_id", callerID, "plan_id", planID)
		return ErrorResult(correctionDeniedMessage)
	}

	if t.planStore == nil || t.taskStore == nil {
		return ErrorResult("plan_correct failed: plan/task stores are not available")
	}
	// FAIL CLOSED when unwired (FR-004).
	if t.appendCorrection == nil {
		slog.Error("plan_correct: no correction hook installed — denying by default",
			"caller_id", callerID, "plan_id", planID)
		return ErrorResult(
			"plan_correct failed: the correction engine is not wired (configuration error) — denying by default")
	}
	if planID == "" {
		return ErrorResult("plan_id is required")
	}

	// From here on the caller IS PlanSupervisor, so real errors are returned
	// rather than normalised (FR-010 clause 4): the adjudicator must be able
	// to tell "I named the wrong plan" from "I am not permitted", because its
	// own honest-exit accounting depends on the difference.
	p, err := t.planStore.Get(planID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("plan_correct failed: could not read plan %q: %v", planID, err))
	}
	if p.State != plan.StateRunning {
		return ErrorResult(fmt.Sprintf(
			"plan %q is %q, not running — only a running plan can be corrected", planID, p.State))
	}
	// D-01 / FR-029: the supervision-eligible phase set is
	// {awaiting_supervision, stalled}, not awaiting_supervision alone. A
	// stalled plan's supervision wake exists precisely so the adjudicator can
	// fix the structure that stalled it; gating on the parked phase alone
	// rejected every correction a stall wake provoked.
	if !plan.IsSupervisionEligiblePhase(p.EffectivePlanPhase()) {
		return ErrorResult(fmt.Sprintf(
			"plan %q is in phase %q; corrections are accepted only while a plan is awaiting supervision or stalled",
			planID, p.EffectivePlanPhase()))
	}

	req, vErr := t.buildCorrection(p, callerID, args)
	if vErr != nil {
		return ErrorResult(fmt.Sprintf("plan_correct rejected: %v", vErr))
	}

	caller := CorrectionCaller{AgentID: callerID, SessionID: ToolTranscriptSessionID(ctx)}
	revisionID, honestExit, cErr := t.appendCorrection(ctx, planID, caller, req)
	if cErr != nil {
		return ErrorResult(fmt.Sprintf("plan_correct failed: %v", cErr))
	}

	// D-03: the correction's read surface. The verb, the falsified assumption
	// and the target member id are all reported back so the adjudicator's own
	// transcript records what it did, matching the widened audit entry.
	createdIDs := make([]string, 0, len(req.TailMembers))
	for i := range req.TailMembers {
		createdIDs = append(createdIDs, req.TailMembers[i].ID)
	}
	payload := map[string]any{
		"plan_id":              planID,
		"revision_id":          revisionID,
		"verb":                 string(req.Verb),
		"falsified_assumption": req.FalsifiedAssumption,
		"created_member_ids":   createdIDs,
		"honest_exit":          honestExit,
	}
	if req.SupersededMemberID != "" {
		payload["superseded_member_id"] = req.SupersededMemberID
	}
	if req.RetriedMemberID != "" {
		payload["retried_member_id"] = req.RetriedMemberID
	}
	switch {
	case req.Verb == plan.RevisionAbandon:
		payload["note"] = "plan abandoned: the Definition of Done was judged unreachable and the plan is terminating"
	case honestExit:
		payload["note"] = "correction applied, but the plan still has no way to make progress and has been failed honestly"
	default:
		payload["note"] = "correction applied; the plan returns to dispatching and will be re-judged"
	}
	encoded, mErr := json.Marshal(payload)
	if mErr != nil {
		// The correction is durably committed at this point — report success
		// with the identifiers rather than an error the adjudicator would
		// (correctly) read as "nothing happened".
		slog.Error("plan_correct: could not encode result payload",
			"plan_id", planID, "revision_id", revisionID, "error", mErr)
		return NewToolResult(fmt.Sprintf("correction applied: revision %s on plan %s", revisionID, planID))
	}
	return NewToolResult(string(encoded))
}

// --- payload validation (FR-046) ------------------------------------------

// buildCorrection validates the whole tool payload and returns the engine
// request. EVERY rule is checked before it returns, and the caller does not
// mutate anything until it returns nil — so a rejected correction leaves the
// plan in exactly the state that produced the wake (E26).
func (t *PlanCorrectTool) buildCorrection(p *plan.Plan, callerID string, args map[string]any) (CorrectionRequest, error) {
	var req CorrectionRequest

	verbRaw, _ := args["verb"].(string)
	verb := plan.RevisionVerb(verbRaw)
	switch verb {
	case plan.RevisionAppend, plan.RevisionSupersede, plan.RevisionTargetedRetry, plan.RevisionAbandon:
	default:
		return req, fmt.Errorf("verb %q is not one of append, supersede, targeted_retry, abandon", verbRaw)
	}
	req.Verb = verb

	falsified := strings.TrimSpace(argString(args, "falsified_assumption"))
	if falsified == "" {
		return req, fmt.Errorf(
			"falsified_assumption is required: state the assumption this plan made that turned out to be wrong")
	}
	if err := checkTextBytes("falsified_assumption", falsified, maxTextBytes); err != nil {
		return req, err
	}
	req.FalsifiedAssumption = falsified

	reason := strings.TrimSpace(argString(args, "reason"))
	if err := checkTextBytes("reason", reason, maxTextBytes); err != nil {
		return req, err
	}
	req.Reason = reason

	supersededID := strings.TrimSpace(argString(args, "superseded_member_id"))
	retriedID := strings.TrimSpace(argString(args, "retried_member_id"))
	rawMembers, _ := args["tail_members"].([]any)
	rawEdges, _ := args["tail_edges"].([]any)

	// --- verb/field compatibility matrix (FR-046) ------------------------
	// Stated as "which fields this verb accepts", so a field that is merely
	// meaningless for a verb is REJECTED rather than silently ignored. The
	// engine sets Members/Edges from the request unconditionally for every
	// verb, so a targeted_retry carrying 50 tail members would create all 50.
	switch verb {
	case plan.RevisionAppend:
		if err := rejectFields(map[string]bool{
			"superseded_member_id": supersededID != "",
			"retried_member_id":    retriedID != "",
		}, "append"); err != nil {
			return req, err
		}
		if len(rawMembers) == 0 {
			return req, fmt.Errorf("append requires at least one tail_members entry — an append that adds no work is not a correction")
		}
	case plan.RevisionSupersede:
		if err := rejectFields(map[string]bool{
			"retried_member_id": retriedID != "",
		}, "supersede"); err != nil {
			return req, err
		}
		if supersededID == "" {
			return req, fmt.Errorf("supersede requires superseded_member_id")
		}
		// FR-030 — the pairing rule, and the reason this whole verb is safe.
		// supersede marks a member's outcome ignored by the judge, so an
		// adjudicator could otherwise satisfy an unmet criterion by
		// discounting the evidence that failed it instead of fixing the work.
		// Pairing composes atomically inside this one request: the engine
		// creates tail members verb-independently, so the discounting and the
		// replacement work land in the same transactional commit or neither
		// does.
		if len(rawMembers) == 0 {
			return req, fmt.Errorf(
				"supersede requires at least one tail_members entry: discounting a member's outcome is only " +
					"a correction when it is paired with replacement work that addresses the same criteria")
		}
	case plan.RevisionTargetedRetry:
		if err := rejectFields(map[string]bool{
			"superseded_member_id": supersededID != "",
			"tail_members":         len(rawMembers) > 0,
			"tail_edges":           len(rawEdges) > 0,
		}, "targeted_retry"); err != nil {
			return req, err
		}
		if retriedID == "" {
			return req, fmt.Errorf("targeted_retry requires retried_member_id")
		}
	case plan.RevisionAbandon:
		// The honest exit carries verb, falsified_assumption and reason only:
		// it mutates no member and adds no work.
		if err := rejectFields(map[string]bool{
			"superseded_member_id": supersededID != "",
			"retried_member_id":    retriedID != "",
			"tail_members":         len(rawMembers) > 0,
			"tail_edges":           len(rawEdges) > 0,
		}, "abandon"); err != nil {
			return req, err
		}
		return req, nil
	}

	if len(rawMembers) > maxTailMembers {
		return req, fmt.Errorf("tail_members has %d entries; the maximum is %d", len(rawMembers), maxTailMembers)
	}
	if len(rawEdges) > maxTailEdges {
		return req, fmt.Errorf("tail_edges has %d entries; the maximum is %d", len(rawEdges), maxTailEdges)
	}

	members, err := t.listPlanMembers(p.ID)
	if err != nil {
		return req, err
	}

	// --- member-targeted verbs: resolve the named member -----------------
	// Plan ownership is checked BEFORE status (FR-047), and the rejection
	// never names the other plan's id: a status error for a member of a
	// different plan would put another plan's member status into the
	// adjudicator's context.
	var supersededMember *task.Task
	switch verb {
	case plan.RevisionSupersede:
		m, mErr := t.resolvePlanMember(members, supersededID, task.StatusDone, "supersede")
		if mErr != nil {
			return req, mErr
		}
		supersededMember = m
		req.SupersededMemberID = supersededID
	case plan.RevisionTargetedRetry:
		if _, mErr := t.resolvePlanMember(members, retriedID, task.StatusFailed, "targeted_retry"); mErr != nil {
			return req, mErr
		}
		req.RetriedMemberID = retriedID
		return req, nil
	}

	// --- tail members ----------------------------------------------------
	existingIDs := make(map[string]bool, len(members))
	for i := range members {
		existingIDs[members[i].ID] = true
	}
	tailMembers, refToID, tErr := t.parseTailMembers(p, callerID, supersededMember, rawMembers, existingIDs)
	if tErr != nil {
		return req, tErr
	}
	req.TailMembers = tailMembers

	// --- FR-030b: the replacement inherits the superseded member's criteria
	//
	// Backfill first (InheritSupersededCriteria), THEN check
	// (RequireCriteriaInheritance): the adjudicator is never shown the
	// superseded member's criteria detail, so requiring it to submit them
	// exactly was unsatisfiable — see InheritSupersededCriteria's doc. The
	// check is kept as a belt-and-suspenders assertion that the backfill
	// actually closed the gap; it must never fire in practice.
	if verb == plan.RevisionSupersede {
		InheritSupersededCriteria(supersededMember.Criteria, tailMembers)
		if err := RequireCriteriaInheritance(supersededMember.Criteria, tailMembers); err != nil {
			return req, err
		}
	}

	// --- tail edges ------------------------------------------------------
	edges, eErr := parseTailEdges(rawEdges, existingIDs, refToID, req.SupersededMemberID)
	if eErr != nil {
		return req, eErr
	}
	if err := RequireAcyclic(members, tailMembers, edges); err != nil {
		return req, err
	}
	req.TailEdges = edges

	return req, nil
}

// listPlanMembers reads this plan's current member set.
func (t *PlanCorrectTool) listPlanMembers(planID string) ([]task.Task, error) {
	members, err := t.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		return nil, fmt.Errorf("could not list member tasks: %w", err)
	}
	return members, nil
}

// resolvePlanMember finds memberID among this plan's members and requires the
// verb's expected status. Ownership is decided by membership of the plan's own
// member list, so a member of another plan is indistinguishable from one that
// does not exist and neither response names the other plan (FR-047).
func (t *PlanCorrectTool) resolvePlanMember(members []task.Task, memberID string, want task.Status, verb string) (*task.Task, error) {
	for i := range members {
		if members[i].ID != memberID {
			continue
		}
		m := &members[i]
		if m.Status != want {
			return nil, fmt.Errorf("%s requires a %s member; member %q is %s", verb, want, memberID, m.Status)
		}
		return m, nil
	}
	return nil, fmt.Errorf("%s names %q, which is not a member of this plan", verb, memberID)
}

// parseTailMembers decodes, validates and MINTS an id for each new member.
//
// Member ids are minted here, never accepted from the caller (FR-046). The
// engine's apply func skips a tail member whose id already exists — correct
// for intent-log replay, and silent data loss for a caller reusing an id it
// just read off the plan: the member is never created, the correction reports
// success, and the plan proceeds believing the work was added, which can flip
// a DoD verdict to MET. Minting retires the whole class rather than validating
// around it, and leaves the replay skip reachable only on replay, where it is
// correct. The tool schema therefore exposes no id field at all — only an
// optional request-local `ref` for naming a new member from tail_edges.
func (t *PlanCorrectTool) parseTailMembers(
	p *plan.Plan, callerID string, supersededMember *task.Task, raw []any, existingIDs map[string]bool,
) ([]task.Task, map[string]string, error) {
	out := make([]task.Task, 0, len(raw))
	refToID := make(map[string]string, len(raw))

	// The tail member's assignee. FR-046's tail-member shape carries no
	// assignee field, and a member with no AgentID fails dispatch outright
	// ("agent %q not found" in the task executor), so it is derived: a
	// supersede's replacement inherits the superseded member's assignee —
	// it redoes that member's work and is held to that member's criteria —
	// and everything else falls to the plan's owner agent, which create_plan
	// already validated as a real, addressable agent.
	assignee := p.OwnerAgentID
	if supersededMember != nil && supersededMember.AgentID != "" {
		assignee = supersededMember.AgentID
	}
	if assignee == "" {
		return nil, nil, fmt.Errorf(
			"cannot assign tail members: this plan has no owner agent to inherit an assignee from")
	}

	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("tail_members[%d]: must be an object", i)
		}

		title := strings.TrimSpace(argString(m, "title"))
		if title == "" {
			return nil, nil, fmt.Errorf("tail_members[%d]: title is required", i)
		}
		if len(title) > maxMemberTitleBytes {
			return nil, nil, fmt.Errorf("tail_members[%d]: title is %d bytes; the maximum is %d",
				i, len(title), maxMemberTitleBytes)
		}
		if utf8.RuneCountInString(title) > taskStoreMaxTitleRunes {
			return nil, nil, fmt.Errorf("tail_members[%d]: title is %d characters; the maximum is %d",
				i, utf8.RuneCountInString(title), taskStoreMaxTitleRunes)
		}

		description := strings.TrimSpace(argString(m, "description"))
		if err := checkTextBytes(fmt.Sprintf("tail_members[%d].description", i), description, maxTextBytes); err != nil {
			return nil, nil, err
		}
		if len(description) > taskStoreMaxDescriptionBytes {
			return nil, nil, fmt.Errorf("tail_members[%d]: description is %d bytes; the maximum is %d",
				i, len(description), taskStoreMaxDescriptionBytes)
		}

		if _, hasID := m["id"]; hasID {
			return nil, nil, fmt.Errorf(
				"tail_members[%d]: id is not accepted — member ids are minted by the system; use `ref` to name this member from tail_edges", i)
		}

		rawCriteria, _ := m["criteria"].([]any)
		if len(rawCriteria) == 0 {
			return nil, nil, fmt.Errorf(
				"tail_members[%d]: criteria is required, at least one — a member with no acceptance criteria cannot be judged", i)
		}
		criteria, cErr := parseCriteriaArgs(rawCriteria, callerID)
		if cErr != nil {
			return nil, nil, fmt.Errorf("tail_members[%d]: %w", i, cErr)
		}

		id := uuid.New().String()

		if ref := strings.TrimSpace(argString(m, "ref")); ref != "" {
			if existingIDs[ref] {
				return nil, nil, fmt.Errorf(
					"tail_members[%d]: ref %q collides with an existing member id of this plan; pick a different label", i, ref)
			}
			if _, dup := refToID[ref]; dup {
				return nil, nil, fmt.Errorf("tail_members[%d]: ref %q is used by more than one tail member", i, ref)
			}
			refToID[ref] = id
		}

		out = append(out, task.Task{
			ID:          id,
			Title:       title,
			Description: description,
			Action:      task.ActionLLM,
			Status:      task.StatusNext,
			AgentID:     assignee,
			PlanID:      p.ID,
			WorkspaceID: p.WorkspaceID,
			Criteria:    criteria,
			CreatedBy:   callerID,
		})
	}
	return out, refToID, nil
}

// RequireCriteriaInheritance enforces FR-030b: every acceptance criterion of
// the superseded member must be carried by the union of the replacement tail
// members' criteria.
//
// FR-030's "at least one tail member" rule alone makes a BARE discount
// impossible, and nothing more. The content of tail_members is entirely
// caller-authored, so without this an adjudicator could supersede the member
// whose output failed a criterion and attach one trivial, instantly-satisfiable
// replacement: the Definition of Done is unchanged, but the evidence set the
// judge weighs is — which is exactly the move supersede must not enable. The
// bypass would cost one throwaway member.
//
// Presence is decided by criterion id when BOTH sides carry one, otherwise by
// exact equality of the (kind, expression) pair — never by rendered or free
// text. A superset is fine; a strict subset is rejected and the rejection
// names what is missing. An empty superseded criteria set is vacuously
// satisfied (FR-030's pairing rule still applies, so the bare form of that
// case is still rejected).
//
// EXPORTED for the same reason as RequireAcyclic below: the engine enforces
// this rule too, on the typed request, and the two must be one function.
//
// In practice this check is now BACKED by InheritSupersededCriteria (called
// first at both call sites — this tool's buildCorrection and the engine's
// validateCorrection): a caller can no longer under-supply and get rejected
// for it, because whatever it omits is backfilled before this runs. This
// function is kept, unrelaxed, as the belt-and-suspenders assertion that the
// backfill actually worked — see InheritSupersededCriteria's doc for why the
// caller-must-reproduce-it-exactly design this function still encodes was
// unsatisfiable in the first place.
func RequireCriteriaInheritance(superseded []task.AcceptanceCriterion, replacements []task.Task) error {
	if len(superseded) == 0 {
		return nil
	}
	byID, byKey := criteriaPresenceSets(replacements)
	var missing []string
	for i := range superseded {
		c := &superseded[i]
		if c.ID != "" && byID[c.ID] {
			continue
		}
		if byKey[criterionKey(c)] {
			continue
		}
		missing = append(missing, fmt.Sprintf("%q", c.Text))
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"supersede rejected: the replacement tail members do not carry %d of the superseded member's "+
				"acceptance criteria (%s). Replacement work must be held to the same standard as the work it "+
				"replaces — otherwise superseding is just discounting the evidence that failed",
			len(missing), strings.Join(missing, ", "))
	}
	return nil
}

// criteriaPresenceSets renders the identity sets RequireCriteriaInheritance
// and InheritSupersededCriteria both need: every criterion id already present
// (non-empty ids only) and every criterion's (kind, expression) key, across
// the WHOLE replacement set (presence is a union property, not a per-member
// one — FR-030b is satisfied by the tail members TOGETHER).
func criteriaPresenceSets(replacements []task.Task) (byID, byKey map[string]bool) {
	byID = make(map[string]bool)
	byKey = make(map[string]bool)
	for i := range replacements {
		for j := range replacements[i].Criteria {
			c := &replacements[i].Criteria[j]
			if c.ID != "" {
				byID[c.ID] = true
			}
			byKey[criterionKey(c)] = true
		}
	}
	return byID, byKey
}

// InheritSupersededCriteria makes FR-030b's identity rule SATISFIABLE instead
// of merely enforced. It backfills onto replacements[0] whatever acceptance
// criterion of superseded is not already carried by the union of the
// replacements' own criteria (same identity rule as RequireCriteriaInheritance:
// by id when both sides have one, otherwise by exact (kind, expression)).
//
// Why this has to be automatic rather than left to the caller: the ONLY
// caller of plan_correct's supersede verb is the PlanSupervisor System Agent,
// and its supervision wake prompt deliberately omits criteria detail
// (buildSupervisionTargetsText, pkg/agent/plan_engine.go, renders only
// member id | status | title — "No descriptions, no results" by design).
// The tail-member criteria schema also never accepts a caller-supplied id
// (parseTailMembers mints every id server-side, matching FR-046's id-minting
// rule generally). Put together: an LLM adjudicator has no way to learn a
// check criterion's exact command and expected exit code, and no id-based
// shortcut either — so requiring it to reproduce that check byte-for-byte
// was, for the one caller that exists, unconditionally impossible, not
// merely hard. Observed live: four varied supersede attempts against a
// check-carrying member, every one rejected, including one that echoed the
// exact criterion TEXT (learned from a prior rejection's error message) with
// a plausible-but-wrong command ("true"/exit 0 for an original "exit 0"/exit
// 0) — the model had no channel to learn the real command short of guessing.
//
// The engine already holds the real criteria (it just read them off the
// superseded member to build the check this replaces); there is no reason to
// make the caller echo them at all. This only ADDS what's missing — it never
// removes or rewrites a criterion the caller wrote, including one that was a
// near-miss attempt at the same criterion (a caller-authored prose criterion
// with the SAME TEXT as a superseded check is a DIFFERENT (kind, expression)
// key and is therefore not treated as covering it — both end up present,
// which is what keeps a check from being quietly downgradable to prose by a
// caller's guess). The replacement can still exceed the floor with its own,
// stricter criteria; it can never fall short of it.
//
// Each backfilled criterion is a value copy with a FRESH identity: id cleared
// (Store.Create mints one, same as any newly authored criterion) and status
// reset to pending (it is a new, unjudged instance of the same
// Definition-of-Done item on a new task — not the old judgement carried
// forward), and Check/Behavior are deep-copied so the new task's criterion
// never aliases the frozen, immutable superseded member's own criterion
// pointer (D4: the superseded member's record must stay untouched).
//
// No-op when there is nothing to inherit (superseded carries no criteria) or
// nowhere to put it (replacements is empty — FR-030's pairing rule rejects
// that case before this ever runs).
func InheritSupersededCriteria(superseded []task.AcceptanceCriterion, replacements []task.Task) {
	if len(superseded) == 0 || len(replacements) == 0 {
		return
	}
	byID, byKey := criteriaPresenceSets(replacements)
	var missing []task.AcceptanceCriterion
	for i := range superseded {
		c := &superseded[i]
		if c.ID != "" && byID[c.ID] {
			continue
		}
		if byKey[criterionKey(c)] {
			continue
		}
		missing = append(missing, cloneCriterionForInheritance(*c))
	}
	if len(missing) == 0 {
		return
	}
	replacements[0].Criteria = append(replacements[0].Criteria, missing...)
}

// cloneCriterionForInheritance deep-copies c for InheritSupersededCriteria: id
// cleared (server mints a fresh one at create time), status reset to pending,
// and the Check/Behavior payloads copied rather than aliased so the new
// task's criterion can never mutate the superseded (frozen, immutable)
// member's own criterion through a shared pointer.
func cloneCriterionForInheritance(c task.AcceptanceCriterion) task.AcceptanceCriterion {
	c.ID = ""
	c.Status = task.CritPending
	if c.Check != nil {
		chk := *c.Check
		c.Check = &chk
	}
	if c.Behavior != nil {
		b := *c.Behavior
		if b.MinCount != nil {
			mc := *b.MinCount
			b.MinCount = &mc
		}
		if b.MaxCount != nil {
			mc := *b.MaxCount
			b.MaxCount = &mc
		}
		c.Behavior = &b
	}
	return c
}

// criterionKey renders the (kind, expression) identity of a criterion for
// FR-030b's comparison. The "expression" is the criterion's machine-meaningful
// payload: the command and expected exit code for a check, the tool/count/scope
// triple for a behavior, and the statement itself for prose. Rendered display
// text is never the comparison basis for check/behavior criteria.
func criterionKey(c *task.AcceptanceCriterion) string {
	switch c.Kind {
	case task.KindCheck:
		if c.Check == nil {
			return "check|<missing>"
		}
		return "check|" + c.Check.Command + "|" + strconv.Itoa(c.Check.ExpectedExitCode)
	case task.KindBehavior:
		if c.Behavior == nil {
			return "behavior|<missing>"
		}
		maxCount := "unbounded"
		if c.Behavior.MaxCount != nil {
			maxCount = strconv.Itoa(*c.Behavior.MaxCount)
		}
		scope := string(c.Behavior.Scope)
		if scope == "" {
			scope = "task_session"
		}
		return "behavior|" + c.Behavior.Tool + "|" +
			strconv.Itoa(c.Behavior.EffectiveMinCount()) + "|" + maxCount + "|" + scope
	default:
		return string(c.Kind) + "|" + strings.TrimSpace(c.Text)
	}
}

// parseTailEdges decodes and validates the edge list: both endpoints must
// resolve (to an existing member of this plan, or to a tail member created in
// this same request via its ref), self-edges are rejected, and neither
// endpoint may name the member being superseded in this request — wiring new
// work behind a member whose outcome is being discounted cannot make progress.
func parseTailEdges(raw []any, existingIDs map[string]bool, refToID map[string]string, supersededID string) ([]plan.IntentEdge, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	resolve := func(field string, idx int, value string) (string, error) {
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("tail_edges[%d]: %s is required", idx, field)
		}
		if id, ok := refToID[value]; ok {
			return id, nil
		}
		if existingIDs[value] {
			return value, nil
		}
		return "", fmt.Errorf(
			"tail_edges[%d]: %s %q resolves to neither an existing member of this plan nor a tail member ref in this call",
			idx, field, value)
	}
	out := make([]plan.IntentEdge, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tail_edges[%d]: must be an object", i)
		}
		from, err := resolve("from", i, argString(m, "from"))
		if err != nil {
			return nil, err
		}
		to, err := resolve("to", i, argString(m, "to"))
		if err != nil {
			return nil, err
		}
		if from == to {
			return nil, fmt.Errorf("tail_edges[%d]: from and to name the same member (self-edge)", i)
		}
		if supersededID != "" && (from == supersededID || to == supersededID) {
			return nil, fmt.Errorf(
				"tail_edges[%d]: names member %q, whose outcome this correction is superseding — new work must not depend on it",
				i, supersededID)
		}
		out = append(out, plan.IntentEdge{FromTaskID: from, ToTaskID: to})
	}
	return out, nil
}

// RequireAcyclic rejects a correction whose resulting dependency graph
// contains a cycle. The graph is the plan's existing member DAG (each
// member's blocked_by edges, restricted to blockers that are themselves
// members of this plan) plus the new tail members and the new edges.
//
// The engine wires edges INSIDE its transactional intent-log commit, so a
// cycle discovered there aborts mid-commit; and an un-wired cycle is
// unresolvable by the dispatcher, which — combined with a once-per-park
// supervision wake — strands the plan permanently.
//
// EXPORTED because PlanEngine.validateCorrection calls this same function on
// the typed request it is handed. AppendCorrection is an exported engine
// entrypoint that this tool is only one caller of, so the engine re-enforces
// the rule independently — and it must be the SAME rule, not a second
// implementation that can drift.
func RequireAcyclic(existing []task.Task, tail []task.Task, edges []plan.IntentEdge) error {
	adj := make(map[string][]string)
	nodes := make(map[string]bool, len(existing)+len(tail))
	for i := range existing {
		nodes[existing[i].ID] = true
	}
	for i := range tail {
		nodes[tail[i].ID] = true
	}
	addEdge := func(from, to string) {
		adj[from] = append(adj[from], to)
	}
	for i := range existing {
		t := &existing[i]
		for _, blocker := range t.BlockedBy {
			if nodes[blocker] {
				addEdge(blocker, t.ID)
			}
		}
	}
	for _, e := range edges {
		addEdge(e.FromTaskID, e.ToTaskID)
	}

	const (
		white = 0 // unvisited
		grey  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(nodes))
	var stack []string
	var visit func(string) []string
	visit = func(n string) []string {
		color[n] = grey
		stack = append(stack, n)
		for _, next := range adj[n] {
			switch color[next] {
			case grey:
				// Found the back edge — report the cycle from where it closes.
				for i := range stack {
					if stack[i] == next {
						return append(append([]string{}, stack[i:]...), next)
					}
				}
				return []string{next, next}
			case white:
				if cycle := visit(next); cycle != nil {
					return cycle
				}
			}
		}
		color[n] = black
		stack = stack[:len(stack)-1]
		return nil
	}
	for n := range nodes {
		if color[n] != white {
			continue
		}
		stack = stack[:0]
		if cycle := visit(n); cycle != nil {
			return fmt.Errorf(
				"tail_edges would create a dependency cycle (%s) — a cyclic plan graph has no dispatchable member and can never make progress",
				strings.Join(cycle, " -> "))
		}
	}
	return nil
}

// --- small shared helpers -------------------------------------------------

// argString reads a string argument as a plain value, tolerating an absent or
// non-string entry (the shape LLM tool-call arguments decode into is untyped).
// Thin wrapper over stringArg for the many call sites here that treat "absent",
// "null" and "not a string" identically — each is then rejected by the
// field's own required/enum check with a message the caller can act on.
func argString(args map[string]any, key string) string {
	s, _ := stringArg(args, key)
	return s
}

// checkTextBytes enforces a BYTE cap (not runes) on a free-text field.
func checkTextBytes(field, value string, maxBytes int) error {
	if len(value) > maxBytes {
		return fmt.Errorf("%s is %d bytes; the maximum is %d", field, len(value), maxBytes)
	}
	return nil
}

// rejectFields rejects any field that is populated but not accepted for this
// verb, naming every offender at once so the caller can fix the payload in a
// single retry rather than discovering the rules one round trip at a time.
func rejectFields(populated map[string]bool, verb string) error {
	var offenders []string
	for _, field := range []string{"superseded_member_id", "retried_member_id", "tail_members", "tail_edges"} {
		if populated[field] {
			offenders = append(offenders, field)
		}
	}
	if len(offenders) == 0 {
		return nil
	}
	return fmt.Errorf("verb %q does not accept %s", verb, strings.Join(offenders, ", "))
}
