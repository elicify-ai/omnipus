// Omnipus — Plan Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — create_plan / execute_plan (ADR-052 §6.1/§6.2, spec
// FR-001..005/019/030/033, US-1/2/3). These are the agent-facing tool
// surface that lets an authorized agent (seeded: Jim/Orchestrator) author
// and launch a Plan without human approval — holding the tool IS the
// approval (Constraint #6). Authorization itself (per-agent allow/ask/deny
// resolution) happens BEFORE Execute is ever called, at the tool-dispatch
// gate in pkg/agent — these tools only implement their own business-rule
// gates (tiered DoD at create, the member-criteria gate at execute).
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// --- shared plan/task-linkage validation (also used by create_task's
// optional plan_id — ADR-052 FR-002) ---

// validateTaskPlanLinkage enforces the same-workspace FK a Task.plan_id
// reference must satisfy — mirroring plan.Store.ValidatePlanWorkspace, which
// this wraps — AND additionally rejects linking to a TERMINAL (done/failed)
// plan (ADR-052 spec "Edge Cases": "create_task(plan_id) referencing a plan
// already done -> reject (can't add to a terminal plan)"). planID == ""
// (no linkage requested) is always valid (nil). A nil planStore fails
// CLOSED — an unwired store is a configuration error, never a permission
// grant (mirrors every other unwired-checker discipline in this package).
func validateTaskPlanLinkage(planStore *plan.Store, planID, workspaceID string) error {
	if planID == "" {
		return nil
	}
	if planStore == nil {
		return fmt.Errorf("cannot verify plan_id %q: plan store is not configured", planID)
	}
	if err := planStore.ValidatePlanWorkspace(planID, workspaceID); err != nil {
		return err
	}
	p, err := planStore.Get(planID)
	if err != nil {
		return fmt.Errorf("could not load plan %q: %w", planID, err)
	}
	if plan.IsTerminal(p.State) {
		return fmt.Errorf("plan %q is %q (terminal) and cannot accept new member tasks", planID, p.State)
	}
	return nil
}

// --- create_plan ---

// PlanCreateTool implements the create_plan agent tool (ADR-052 FR-001,
// spec US-1). It creates a draft Plan. Unlike the human/UI REST path
// (handleWorkspacePlanCreate / rest_plans.go), EVERY caller of this tool is
// by definition an agent (ToolAgentID(ctx) is always set for a tool
// invocation) — so the tiered DoD gate (ADR-049 SD-A7) collapses to the
// STRICT tier unconditionally here: an agent-authored plan with zero
// Definition-of-Done criteria is rejected outright. This matches spec FR-001
// ("tiered DoD — agent-authored plans REQUIRE non-empty DoD") without
// needing the isAgentID heuristic the REST handler uses (that heuristic
// exists ONLY to distinguish REST's own human-vs-agent callers; every caller
// reaching THIS tool already is one).
type PlanCreateTool struct {
	BaseTool
	store *plan.Store
	// validateOwner, when non-nil, validates that owner_agent_id names a
	// real, addressable (non-System-Agent, non-worker) agent — mirrors
	// validatePlanOwnerAgent (pkg/gateway/rest_plans.go), which pkg/tools
	// cannot call directly: it needs the live agent registry/config, which
	// this package does not hold. Injected by the wiring layer
	// (pkg/agent/loop.go, another wave's job).
	//
	// FAIL CLOSED, not open, when unwired: an unwired validator is a
	// configuration error, never a permission grant — mirrors
	// TaskCreateTool's delegationDeny/bashPolicyChecker discipline
	// (pkg/tools/task.go) exactly. Do NOT default an unwired validator's
	// outcome to "allow".
	validateOwner func(ownerAgentID string) error
	// home is OMNIPUS_HOME, used to resolve the default workspace (mirrors
	// TaskCreateTool.resolveWorkspaceID) when no explicit workspace_id arg
	// is given and none is bound to the turn context. Set via SetHome.
	home string
}

// NewPlanCreateTool constructs a PlanCreateTool. store may be nil for
// metadata-only construction (GeneralBuiltinMetadata) — never Execute()d in
// that mode.
func NewPlanCreateTool(store *plan.Store) *PlanCreateTool {
	return &PlanCreateTool{store: store}
}

// SetHome configures OMNIPUS_HOME so create_plan can resolve the real
// default workspace (via workspace.ResolveDefaultID) when no workspace_id is
// supplied and none is bound to the turn context.
func (t *PlanCreateTool) SetHome(home string) {
	t.home = home
}

// SetOwnerValidator installs the owner_agent_id validity check (see the
// validateOwner field doc).
func (t *PlanCreateTool) SetOwnerValidator(fn func(ownerAgentID string) error) {
	t.validateOwner = fn
}

func (t *PlanCreateTool) Name() string           { return "create_plan" }
func (t *PlanCreateTool) Scope() ToolScope       { return ScopeCore }
func (t *PlanCreateTool) Category() ToolCategory { return CategoryTasks }

func (t *PlanCreateTool) Description() string {
	return "Create a draft Plan to decompose / break down a complex, multi-step goal — a " +
		"Definition-of-Done-driven grouping of tasks. Attach member tasks afterward with " +
		"create_task(plan_id=..., write_set=..., stream=..., is_join=...). Requires at least one " +
		"Definition-of-Done criterion (dod) — an agent-authored plan with none is rejected. Also " +
		"requires rationale: the planning discipline behind the decomposition (e.g. which " +
		"write-set/stream split was chosen and which member is the join). Names an owner_agent_id — " +
		"the real, addressable agent woken at the plan's decision points; a System Agent or worker is " +
		"rejected. Optionally takes goal (plain-prose objective the plan judge weighs alongside the " +
		"Definition of Done) and workspace_id (accepts workspace as an alias) which defaults to the " +
		"current turn's bound workspace, then the default workspace. Call execute_plan once the " +
		"plan's member tasks are attached to start autonomous execution with no further human " +
		"approval — execute_plan runs plan-lint, which rejects overlapping parallel write_sets and " +
		"convergence points with no authored join member (is_join=true + its own acceptance criteria)."
}

func (t *PlanCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Short title for the plan (1-200 characters)",
			},
			"goal": map[string]any{
				"type":        "string",
				"description": "Plain-prose objective, used by the plan judge alongside the Definition of Done",
			},
			"owner_agent_id": map[string]any{
				"type":        "string",
				"description": "Agent woken at plan decision points; must be a real, addressable agent (not a System Agent or worker)",
			},
			"workspace": map[string]any{
				"type":        "string",
				"description": "Workspace this plan belongs to (optional — defaults to the current turn's bound workspace, or the default workspace). Alias: workspace_id.",
			},
			"workspace_id": map[string]any{
				"type":        "string",
				"description": "Alias for workspace (optional — defaults to the current turn's bound workspace, or the default workspace). If both are supplied, workspace wins.",
			},
			"dod": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type": "string",
							"enum": []string{"check", "prose", "behavior"},
							"description": "check: a shell command verified via the plan owner's bash tool; " +
								"prose: a free-text statement judged by the plan verifier; " +
								"behavior: a deterministic count of successful calls of a named tool in the " +
								"session's tool-call log. Optional (ADR-074 D2) — when omitted, inferred " +
								"from the payload: check payload => check, behavior payload => behavior, " +
								"no payload => prose. An explicit kind mismatching its payload is rejected.",
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
					"required": []string{"text"},
				},
				"description": "Plan-level Definition of Done. REQUIRED: at least one criterion — an " +
					"agent-authored plan with zero DoD criteria is rejected.",
			},
			"rationale": map[string]any{
				"type": "string",
				"description": "REQUIRED: the planning discipline behind this plan's decomposition — the " +
					"'why' (e.g. the write-set/stream split chosen and the join points authored). " +
					"1-4000 characters. Plan-lint and the owner-loop correction flow read this " +
					"alongside member write_set/stream/is_join.",
			},
		},
		"required": []string{"title", "owner_agent_id", "dod", "rationale"},
	}
}

// resolvePlanWorkspaceID resolves the plan's workspace: an explicit
// "workspace" arg wins (validated to exist on disk when home is known);
// otherwise falls back to the turn-bound workspace, then the real default
// workspace — mirroring TaskCreateTool.resolveWorkspaceID but adding the
// explicit-arg override create_plan's tool contract exposes.
func (t *PlanCreateTool) resolvePlanWorkspaceID(ctx context.Context, args map[string]any) (string, error) {
	// M14: "workspace" is the canonical arg name here, but the rest of the
	// codebase (ToolWorkspaceID, the wire types, and this function's own slog
	// key) spells it "workspace_id". Without this alias, a caller that
	// (reasonably) sends workspace_id has it silently dropped — the schema
	// does not reject unknown keys, so the call succeeds and the plan lands
	// in the turn-bound or default workspace instead. Accept either; an
	// explicit "workspace" wins if somehow both are supplied.
	explicit, ok := args["workspace"].(string)
	if !ok || explicit == "" {
		explicit, ok = args["workspace_id"].(string)
	}
	if ok && explicit != "" {
		if t.home == "" || workspace.Exists(t.home, explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("workspace %q does not exist", explicit)
	}
	if ws := ToolWorkspaceID(ctx); ws != "" {
		if t.home == "" || workspace.Exists(t.home, ws) {
			return ws, nil
		}
		slog.Warn("create_plan: bound workspace_id does not exist — falling back to default",
			"workspace_id", ws)
	}
	if t.home != "" {
		id, err := workspace.ResolveDefaultID(t.home)
		if err != nil {
			return "", fmt.Errorf("could not resolve default workspace: %w", err)
		}
		return id, nil
	}
	return "", fmt.Errorf("no workspace provided, no active workspace bound, and no default workspace resolver configured")
}

func (t *PlanCreateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("create_plan failed: plan store is not available")
	}

	title, _ := args["title"].(string)
	if title == "" {
		return ErrorResult("title is required")
	}
	ownerAgentID, _ := args["owner_agent_id"].(string)
	if ownerAgentID == "" {
		return ErrorResult("owner_agent_id is required")
	}

	callerID := ToolAgentID(ctx)

	// owner_agent_id validity (mirrors validatePlanOwnerAgent, rest_plans.go).
	// FAIL CLOSED when unwired — same discipline as every other DI checker in
	// this package (see the validateOwner field doc).
	if t.validateOwner != nil {
		if err := t.validateOwner(ownerAgentID); err != nil {
			return ErrorResult(fmt.Sprintf("create_plan failed: %v", err))
		}
	} else {
		slog.Error("create_plan: no owner-agent validator installed — denying by default",
			"caller_id", callerID, "owner_agent_id", ownerAgentID)
		return ErrorResult(
			"create_plan failed: cannot verify owner_agent_id (owner validator not configured) — " +
				"denying by default",
		)
	}

	// FR-001 tiered DoD (collapsed to strict — see type doc): every caller of
	// this tool is an agent, so a create_plan with zero DoD is always rejected.
	rawDoD, _ := args["dod"].([]any)
	if len(rawDoD) == 0 {
		return ErrorResult(
			"dod is required: an agent-authored plan must supply at least one Definition-of-Done " +
				"criterion (ADR-052 FR-001, tiered DoD)",
		)
	}
	dod, dErr := parseCriteriaArgs(rawDoD, callerID)
	if dErr != nil {
		return ErrorResult(fmt.Sprintf("create_plan failed: %v", dErr))
	}

	// ADR-053 §Contract Surface / FR-156: rationale is required from this
	// tool unconditionally — every caller is an agent (same collapse-to-
	// strict-tier reasoning as the dod gate above).
	rationale, _ := args["rationale"].(string)
	if strings.TrimSpace(rationale) == "" {
		return ErrorResult(
			"rationale is required: an agent-authored plan must state the planning discipline " +
				"behind its decomposition (ADR-053 §Contract Surface)",
		)
	}

	wsID, wErr := t.resolvePlanWorkspaceID(ctx, args)
	if wErr != nil {
		return ErrorResult(fmt.Sprintf("could not resolve workspace: %v", wErr))
	}

	p := &plan.Plan{
		WorkspaceID:  wsID,
		Title:        title,
		OwnerAgentID: ownerAgentID,
		Owner:        callerID,
		CreatedBy:    callerID,
		DoD:          dod,
		Rationale:    rationale,
	}
	if goal, ok := args["goal"].(string); ok {
		p.Goal = goal
	}

	// FR-012d: record the plan's CHAT ORIGIN — the conversation this plan was
	// asked for in — so an Owner wake can deliver the plan's outcome back to
	// the requester instead of into a synthetic internal destination the
	// message path drops. Read from the turn context, never from args: a
	// caller-supplied origin would let one principal redirect another's plan
	// outcome into a conversation of its choosing.
	//
	// `webchat` is INCLUDED here, deliberately unlike create_task
	// (task.go's `channel != "webchat"` guard). A task's result surfaces on the
	// board, which the web user is already looking at; a plan's outcome has no
	// such fallback surface, and webchat is the origin most plans actually
	// have — excluding it would leave the common case with no destination.
	// Both fields stay empty for a REST/UI-created plan, which is a legitimate
	// state: the owner turn still runs, only the outbound message is skipped.
	if channel := ToolChannel(ctx); channel != "" {
		p.SourceChannel = channel
		p.SourceChatID = ToolChatID(ctx)
	}

	if err := t.store.Create(p); err != nil {
		return ErrorResult(fmt.Sprintf("create_plan failed: %v", err))
	}

	return NewToolResult(fmt.Sprintf(`{"plan_id":%q,"state":%q,"workspace_id":%q}`, p.ID, p.State, p.WorkspaceID))
}

// --- execute_plan ---

// planTaskError is a single member-task offender in execute_plan's rejection
// payload — the SAME shape (task_id/title/reason) POST /approve's
// PlanApproveError.task_errors carries (rest_plans.go), so the calling
// agent can iterate and fix each offending task programmatically.
type planTaskError struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// PlanExecuteTool implements the execute_plan agent tool (ADR-052
// FR-003/004/030, spec US-3). It routes through the SAME gates
// POST /plans/{id}/approve (handlePlanApprove, rest_plans.go) enforces —
// the SD-A7 tiered-DoD gate (see isAgentID) and the unconditional FR-084
// per-member-criteria gate — then transitions draft->approved via the SAME
// single gated state-transition path (plan.Store.Update through
// ValidateStateTransition) and RETURNS IMMEDIATELY. Promotion of
// approved->running under the global concurrency cap is the engine's job
// (pkg/agent's PlanEngine, out of this package's scope) and happens
// asynchronously; this tool has no visibility into the engine's live cap
// occupancy, so its "queued behind cap" note is reported unconditionally
// for a freshly-approved plan (spec G5/FR-003: "the tool reports queued
// behind cap semantics" — accurate whether or not a slot happens to be
// free at the instant this call returns, since the outcome — wait at
// approved, or promote — is the engine's async decision either way).
//
// Two INTENTIONAL differences remain from handlePlanApprove — not gaps,
// flagged here for the Wave-2 shared-gate extraction that would fold both
// handlers onto one internal gate function:
//  1. FR-030 empty-plan reject (zero member tasks) exists here but not in
//     REST — an agent driving its own plan end-to-end has no other point
//     at which "nothing to run" would ever surface; a human using the UI
//     approve button is assumed to already see the (empty) task list.
//  2. State handling is broader here: REST 400s anything that is not
//     StateDraft; this tool additionally treats StateRunning/StateApproved
//     as an idempotent no-op (spec edge case: "execute_plan on an
//     already-running plan -> no-op") rather than an error, since a
//     tool-calling agent may re-issue execute_plan without first checking
//     state, where a human clicking Approve in the UI would not.
type PlanExecuteTool struct {
	BaseTool
	planStore *plan.Store
	taskStore *task.Store
	// isAgentID reports whether id resolves to a registered agent — mirrors
	// restAPI.isAgentID (pkg/gateway/rest_plans.go) exactly, and drives the
	// SAME SD-A7 tiered-DoD gate handlePlanApprove enforces: a plan whose
	// CreatedBy resolves to an agent (strict tier) must carry >=1 DoD
	// criterion; a human-authored plan (soft tier) may have none. Injected
	// by the wiring layer (pkg/agent/loop.go, another wave's job) — mirrors
	// PlanCreateTool.validateOwner's unwired-until-wired discipline.
	//
	// FAIL CLOSED, not open, when unwired: an unset isAgentID must NOT be
	// read as "CreatedBy is not an agent" (which would silently skip the
	// gate for every plan, agent-authored or not). Treat CreatedBy as
	// agent-authored (require DoD) until the real checker is wired — same
	// discipline as every other DI checker in this package.
	isAgentID func(id string) bool
}

// NewPlanExecuteTool constructs a PlanExecuteTool. Either store may be nil
// for metadata-only construction (GeneralBuiltinMetadata) — never Execute()d
// in that mode.
func NewPlanExecuteTool(planStore *plan.Store, taskStore *task.Store) *PlanExecuteTool {
	return &PlanExecuteTool{planStore: planStore, taskStore: taskStore}
}

// SetIsAgentIDChecker installs the CreatedBy-is-an-agent predicate the
// SD-A7 tiered-DoD gate uses (see the isAgentID field doc).
func (t *PlanExecuteTool) SetIsAgentIDChecker(fn func(id string) bool) {
	t.isAgentID = fn
}

func (t *PlanExecuteTool) Name() string           { return "execute_plan" }
func (t *PlanExecuteTool) Scope() ToolScope       { return ScopeCore }
func (t *PlanExecuteTool) Category() ToolCategory { return CategoryTasks }

func (t *PlanExecuteTool) Description() string {
	return "Start autonomous execution of a draft plan — no human approval required. Runs the same " +
		"gates the human approve path enforces: the plan must have at least one member task, every " +
		"member must carry at least one acceptance criterion, and plan-lint must pass — lint rejects " +
		"overlapping parallel write_sets and convergence points with no authored join member " +
		"(is_join=true plus its own criteria), returning the offenders in `lint_violations`. On " +
		"success the plan is approved and the tool returns immediately; the engine promotes it to " +
		"running under the global concurrency cap, waiting at 'approved' if the cap is full and " +
		"promoting automatically when a slot frees. Calling this on an already-approved or running " +
		"plan is a harmless no-op; a done or failed plan is terminal and is rejected."
}

func (t *PlanExecuteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan_id": map[string]any{
				"type":        "string",
				"description": "ID of the (draft) plan to execute",
			},
		},
		"required": []string{"plan_id"},
	}
}

func (t *PlanExecuteTool) Execute(_ context.Context, args map[string]any) *ToolResult {
	if t.planStore == nil {
		return ErrorResult("execute_plan failed: plan store is not available")
	}
	if t.taskStore == nil {
		return ErrorResult("execute_plan failed: task store is not available")
	}

	planID, _ := args["plan_id"].(string)
	if planID == "" {
		return ErrorResult("plan_id is required")
	}

	p, err := t.planStore.Get(planID)
	if err != nil {
		if errors.Is(err, plan.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("plan %q not found", planID))
		}
		return ErrorResult(fmt.Sprintf("execute_plan failed: could not read plan: %v", err))
	}

	// Spec edge case: "execute_plan on an already-running plan -> no-op /
	// idempotent (not re-approved)." An already-approved plan is likewise a
	// no-op (it is already exactly where a successful execute_plan call would
	// have left it — waiting for the engine's cap admission). A done/failed
	// plan is terminal and cannot be (re-)executed.
	switch p.State {
	case plan.StateRunning:
		return NewToolResult(fmt.Sprintf(
			`{"plan_id":%q,"state":%q,"note":"already running — no action taken"}`, p.ID, p.State))
	case plan.StateApproved:
		return NewToolResult(fmt.Sprintf(
			`{"plan_id":%q,"state":%q,"note":"already approved; queued behind cap — the engine promotes it to running when a slot frees"}`,
			p.ID, p.State))
	case plan.StateDone, plan.StateFailed:
		return ErrorResult(fmt.Sprintf("plan %q is %q; only a draft plan can be executed", planID, p.State))
	case plan.StateDraft:
		// Fall through to the gate below.
	default:
		return ErrorResult(fmt.Sprintf("plan %q is in an unrecognized state %q", planID, p.State))
	}

	// SD-A7 tiered-DoD gate (mirrors handlePlanApprove exactly — see the
	// isAgentID field doc for the fail-closed default when unwired).
	requiresDoD := true
	if t.isAgentID != nil {
		requiresDoD = t.isAgentID(p.CreatedBy)
	}
	if requiresDoD && len(p.DoD) == 0 {
		return ErrorResult(
			"execute_plan failed: plan requires a Definition of Done before execution (agent-authored plan)")
	}

	// FR-030: an empty plan (0 member tasks) is rejected — nothing to run.
	members, lerr := t.taskStore.List(task.Filter{PlanID: planID})
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("execute_plan failed: could not list member tasks: %v", lerr))
	}
	if len(members) == 0 {
		return ErrorResult(fmt.Sprintf("plan %q has zero member tasks; nothing to execute", planID))
	}

	// FR-004/FR-084 (unconditional, mirrors handlePlanApprove exactly): every
	// member task must carry >=1 acceptance criterion.
	var offenders []planTaskError
	for _, mt := range members {
		if len(mt.Criteria) == 0 {
			offenders = append(offenders, planTaskError{
				TaskID: mt.ID, Title: mt.Title, Reason: "task has no acceptance criteria",
			})
		}
	}
	if len(offenders) > 0 {
		payload := map[string]any{
			"error":       "plan has member tasks with no acceptance criteria",
			"task_errors": offenders,
		}
		encoded, mErr := json.Marshal(payload)
		if mErr != nil {
			return ErrorResult(fmt.Sprintf(
				"execute_plan rejected: %d member task(s) have no acceptance criteria", len(offenders)))
		}
		return ErrorResult(string(encoded))
	}

	// FR-156/FR-159 (G-16, US-11): plan-lint — reject overlapping parallel
	// write_sets and join-less convergence points. Runs after the FR-084
	// criteria gate (mirrors this function's existing gate ordering: the
	// more fundamental "does every member even have a DoD" gap surfaces
	// first) and before the single gated state transition below, so a
	// rejected plan never reaches draft->approved.
	if lerr := plan.Lint(p, members); lerr != nil {
		payload := map[string]any{
			"error":           lerr.Error(),
			"lint_violations": lerr.Violations,
		}
		encoded, mErr := json.Marshal(payload)
		if mErr != nil {
			return ErrorResult(fmt.Sprintf("execute_plan failed: %v", lerr))
		}
		return ErrorResult(string(encoded))
	}

	// Single gated transition: draft -> approved (the SAME transition
	// handlePlanApprove performs). The engine (out of this package's scope)
	// promotes approved -> running asynchronously under the global cap.
	approved := plan.StateApproved
	updated, uerr := t.planStore.Update(planID, plan.Patch{State: &approved})
	if uerr != nil {
		return ErrorResult(fmt.Sprintf("execute_plan failed: could not approve plan: %v", uerr))
	}

	return NewToolResult(fmt.Sprintf(
		`{"plan_id":%q,"state":%q,"note":"approved; queued behind cap — the engine promotes it to running under the global concurrency cap when a slot is available"}`,
		updated.ID, updated.State))
}
