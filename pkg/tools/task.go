package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TaskListTool lists tasks for the calling agent.
type TaskListTool struct {
	BaseTool
	store *task.Store
}

func NewTaskListTool(store *task.Store) *TaskListTool {
	return &TaskListTool{store: store}
}

func (t *TaskListTool) Name() string           { return "list_tasks" }
func (t *TaskListTool) Scope() ToolScope       { return ScopeGeneral }
func (t *TaskListTool) Category() ToolCategory { return CategoryTasks }

func (t *TaskListTool) Description() string {
	return "List tasks. Use role='assignee' for tasks assigned to you, role='delegator' for tasks you " +
		"created for other agents. Scoped to your current workspace when this turn has one " +
		"(workspace_scoped says which). Bounded to the 100 most recently updated matches; " +
		"`truncated` and `matched` say when there were more."
}

func (t *TaskListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role": map[string]any{
				"type":        "string",
				"enum":        []string{"assignee", "delegator"},
				"description": "assignee: tasks assigned to you; delegator: tasks you created for others",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"inbox", "next", "in_progress", "blocked", "done", "failed"},
				"description": "Filter by status (optional)",
			},
		},
		"required": []string{"role"},
	}
}

func (t *TaskListTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("list_tasks failed: task store is not available")
	}
	role, _ := args["role"].(string)
	if role != "assignee" && role != "delegator" {
		return ErrorResult("role must be 'assignee' or 'delegator'")
	}
	status, _ := args["status"].(string)

	// FAIL CLOSED on an unresolvable caller, exactly as list_jobs does.
	// task.Filter treats every empty field as "filter off" (Filter.matches,
	// pkg/task/store.go), so an empty agent id here does not narrow anything —
	// it returns EVERY task in the store, across every workspace and every
	// agent, straight into the model's context. list_tasks is seeded `allow`
	// globally, so that is a cross-agent/cross-workspace disclosure reachable
	// from any turn whose agent id failed to resolve.
	//
	// Never relax this into "return an empty list": a silent empty success is
	// indistinguishable from genuinely having no tasks and hides the
	// misconfiguration that produced it.
	agentID := strings.TrimSpace(ToolAgentID(ctx))
	if agentID == "" {
		return ErrorResult("list_tasks: cannot resolve the calling agent; refusing to list tasks")
	}

	// ToolWorkspaceID is conditionally injected and is empty for any turn whose
	// channel binding carries no workspace. That is a legitimate state, so —
	// exactly as list_jobs documents — this is a deliberate exception to the
	// fail-closed posture above: the list widens to every workspace FOR THIS
	// PRINCIPAL ONLY, and says so through workspace_scoped rather than
	// presenting a cross-workspace list as a scoped one.
	workspaceID := strings.TrimSpace(ToolWorkspaceID(ctx))

	filter := task.Filter{Status: task.Status(status), WorkspaceID: workspaceID}
	switch role {
	case "assignee":
		filter.AgentID = agentID
	case "delegator":
		// CreatedByAgentID, not CreatedBy: CreatedBy is MIXED-NAMESPACE (a
		// username on the REST path, an agent id on the tool path — see
		// task.Task.CreatedBy) and must never be used as an ownership
		// predicate, because a human user whose username happens to equal an
		// agent id would have their tasks disclosed to that agent. The
		// CreatedByAgentID filter routes through task.Task.CreatedByAgent,
		// which fails closed on BOTH sides — an unattributed (REST-created)
		// task never matches any agent.
		filter.CreatedByAgentID = agentID
	}

	tasks, err := t.store.List(filter)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_list failed: %v", err))
	}

	// Deterministic order BEFORE the bound, so which rows survive truncation is
	// a stated rule (most recently updated first) rather than directory order.
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].UpdatedAt != tasks[j].UpdatedAt {
			return tasks[i].UpdatedAt > tasks[j].UpdatedAt
		}
		return tasks[i].ID > tasks[j].ID
	})

	matched := len(tasks)
	truncated := matched > maxTaskListRows
	if truncated {
		tasks = tasks[:maxTaskListRows]
	}

	rows := make([]taskListRow, 0, len(tasks))
	for i := range tasks {
		rows = append(rows, projectTaskListRow(&tasks[i]))
	}

	resp := taskListResponse{
		Tasks:           rows,
		WorkspaceScoped: workspaceID != "",
		Matched:         matched,
		Returned:        len(rows),
	}
	if truncated {
		resp.Truncated = true
		resp.Note = fmt.Sprintf(
			"showing the %d most recently updated of %d matching tasks — narrow with status",
			len(rows), matched)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(fmt.Sprintf("list_tasks: marshal: %v", err))
	}
	return NewToolResult(string(data))
}

// maxTaskListRows bounds one list_tasks response. The task store grows
// monotonically and nothing sweeps it, so exceeding this on a long-lived
// install is the steady state rather than the exception — which is why crossing
// it is REPORTED (truncated + matched) rather than silently absorbed.
const maxTaskListRows = 100

// taskListRow is the ALLOWLIST projection list_tasks returns in place of the
// on-disk task.Task struct.
//
// Allowlist, never denylist, and task.Task is never marshalled whole. task.Task
// carries fields whose own doc comments declare them DISK-ONLY and forbid them
// from crossing any boundary — CreatedByAgentID ("the REST mapper does NOT copy
// it to the wire type, and it MUST NOT be added to any schema in contracts/"),
// Scratchpad, PendingJudgeClaim, DelegationDepth — and a whole-struct marshal
// shipped every one of them into an LLM's context. Because these rows are
// already scoped to the caller this was never a cross-principal disclosure, but
// the disk-only contract is a contract regardless of audience, and an allowlist
// means a field added to task.Task tomorrow is not disclosed by default.
//
// Prompt and Result ARE carried: on a caller's own task they are the two fields
// that make a row actionable ("what was I asked to do / what did I report"),
// and the scoping above is what makes carrying them safe.
type taskListRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	PlanID      string `json:"plan_id,omitempty"`
	// Priority, WriteSet, and Stream (M7 fix): create_task accepts all three,
	// but before this fix nothing on this read surface ever showed them back
	// to the calling agent — the exact gap that let the M2 priority-validation
	// bug go unnoticed (a caller had no way to verify what was actually
	// persisted). Priority uses EffectivePriority() (never 0) so a task
	// created without an explicit priority still reads back a meaningful, real
	// value (3), matching the REST read surface (toWireTask).
	Priority    int      `json:"priority,omitempty"`
	Description string   `json:"description,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	Result      string   `json:"result,omitempty"`
	Due         string   `json:"due,omitempty"`
	BlockedBy   []string `json:"blocked_by,omitempty"`
	WriteSet    []string `json:"write_set,omitempty"`
	Stream      string   `json:"stream,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	CompletedAt string   `json:"completed_at,omitempty"`
}

// taskListResponse is the list_tasks envelope. `matched` and `truncated` exist
// so a bounded list is never mistaken for a complete one, and workspace_scoped
// so a narrowed list is never mistaken for the whole picture.
//
// Plan-member tasks are deliberately NOT excluded here, unlike list_jobs'
// task collector. That exclusion is sound there and unsound here: list_jobs
// drops plan members because the plan itself appears as its own row in the SAME
// response, so nothing is hidden. list_tasks has no plan rows, so excluding
// members would make an agent blind to its own plan work with no compensating
// view in the same tool.
type taskListResponse struct {
	Tasks           []taskListRow `json:"tasks"`
	WorkspaceScoped bool          `json:"workspace_scoped"`
	Matched         int           `json:"matched"`
	Returned        int           `json:"returned"`
	Truncated       bool          `json:"truncated,omitempty"`
	Note            string        `json:"note,omitempty"`
}

func projectTaskListRow(tk *task.Task) taskListRow {
	return taskListRow{
		ID:          tk.ID,
		Title:       tk.Title,
		Status:      string(tk.Status),
		WorkspaceID: tk.WorkspaceID,
		AgentID:     tk.AgentID,
		PlanID:      tk.PlanID,
		Priority:    tk.EffectivePriority(),
		Description: tk.Description,
		Prompt:      tk.Prompt,
		Result:      tk.Result,
		Due:         tk.Due,
		BlockedBy:   tk.BlockedBy,
		WriteSet:    tk.WriteSet,
		Stream:      tk.Stream,
		CreatedAt:   tk.CreatedAt,
		UpdatedAt:   tk.UpdatedAt,
		CompletedAt: tk.CompletedAt,
	}
}

// TaskCreateTool creates a task and delegates it to another agent.
type TaskCreateTool struct {
	BaseTool
	store *task.Store
	// delegationDeny, when non-nil, applies the full delegation policy (trust
	// set + modes ("task") + depth — FR-6.2). Returns a non-nil *DelegationDenial
	// to DENY (carrying the structured reason + policy axis) or nil to ALLOW.
	// This is the ONLY delegation gate (ADR-037 retired the legacy boolean
	// delegateCheck fallback, which was only ever consulted when this was nil —
	// never happened in production wiring).
	delegationDeny func(ctx context.Context, targetAgentID string) *DelegationDenial
	// onCreate, when non-nil, is invoked after a task is successfully created so
	// the caller can emit a task_status_changed event.
	onCreate func(*task.Task)
	// home is the OMNIPUS_HOME path used to resolve the default workspace ID
	// when no workspace is bound to the current turn context. Set via SetHome.
	home string
	// maxDelegationDepth is the hard ceiling on the task-mode delegation
	// generation counter. A task_create issued from within a task run whose
	// generation already equals (or exceeds) this bound is rejected, so an
	// A→B→A task→task chain cannot recurse unboundedly. Set via
	// SetMaxDelegationDepth; 0 disables the bound (no caller should leave it 0).
	maxDelegationDepth int
	// bashPolicyChecker resolves an assignee agent's effective "bash" tool
	// policy (ADR-049 D2 rule 5, FR-017/052). Set via SetBashPolicyChecker.
	bashPolicyChecker func(assigneeAgentID string) (policy string, ok bool)
	// planStore, when set, backs the optional plan_id linkage arg (ADR-052
	// FR-002): validates the same-workspace FK and rejects linking to a
	// terminal plan (validateTaskPlanLinkage, plan.go). A nil planStore with
	// a non-empty plan_id arg fails closed (see SetPlanStore).
	planStore *plan.Store
}

func NewTaskCreateTool(store *task.Store) *TaskCreateTool {
	return &TaskCreateTool{store: store}
}

// SetHome configures the OMNIPUS_HOME path so that task_create can resolve the
// real default workspace ID (via workspace.ResolveDefaultID) when no workspace
// is bound to the turn context. The agent loop calls this after constructing the
// tool (pkg/agent/loop.go ~line 1608).
func (t *TaskCreateTool) SetHome(home string) {
	t.home = home
}

// SetMaxDelegationDepth installs the hard task-mode recursion bound. A
// task_create issued from within a task run whose stored DelegationDepth is
// already >= the bound is rejected. The agent loop passes agent.maxTaskDepth (10).
func (t *TaskCreateTool) SetMaxDelegationDepth(bound int) {
	t.maxDelegationDepth = bound
}

// SetDelegationDenyChecker installs the full delegation-policy gate (FR-6.2).
func (t *TaskCreateTool) SetDelegationDenyChecker(
	fn func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDeny = fn
}

// SetOnCreate sets the callback invoked after a task is successfully created.
func (t *TaskCreateTool) SetOnCreate(fn func(*task.Task)) {
	t.onCreate = fn
}

// SetBashPolicyChecker installs the D2 rule 5 checker (ADR-049, FR-017/052):
// resolves the assignee agent's effective "bash" tool policy so a create
// whose criteria are ALL kind=check can be rejected as structurally
// unsatisfiable when that policy is deny or ask (ask resolves to deny
// unattended at judge time, D2 rule 2 — a machine check that can never even
// run can never adjudicate MET). fn should return ok=false when the assignee
// agent cannot be resolved at all.
//
// Mirrors the fail-closed-when-unwired discipline SetDelegationDenyChecker
// documents above — an unwired checker is a configuration error, never a
// permission grant. Do NOT default an unwired checker's outcome to "allow".
func (t *TaskCreateTool) SetBashPolicyChecker(fn func(assigneeAgentID string) (policy string, ok bool)) {
	t.bashPolicyChecker = fn
}

// SetPlanStore installs the plan store backing the optional plan_id linkage
// arg (ADR-052 FR-002). Wired by the agent loop alongside the task store; a
// nil (unwired) store makes any create_task(plan_id=...) call fail closed —
// see validateTaskPlanLinkage (plan.go). create_task calls with no plan_id
// are entirely unaffected by whether this is wired.
func (t *TaskCreateTool) SetPlanStore(store *plan.Store) {
	t.planStore = store
}

// parseCriteriaArgs converts the create_task tool's raw "criteria" argument
// (a []any of map[string]any — the shape LLM tool-call arguments always
// decode into) into []task.AcceptanceCriterion. Every criterion is
// server-authored as the CALLING agent — agent-created criteria are, by
// definition, agent-authored (SD-A7); author is never accepted from args.
// Shape/length validation (kind enum, text bounds, check-shape-iff-kind,
// ID/status defaulting) is left to the store's own normalizeCriteria,
// invoked from Store.Create — this only handles the untyped-map decode.
//
// Behavior payloads (ADR-052 FR-034 / ADR-074 D3a) decode via the shared
// task.DecodeBehaviorPayload, which honors the pointer semantics
// pkg/task/criterion.go documents (absent min_count/max_count stay nil; an
// explicit 0 decodes to a pointer at 0).
func parseCriteriaArgs(raw []any, authorAgentID string) ([]task.AcceptanceCriterion, error) {
	out := make([]task.AcceptanceCriterion, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("criteria[%d]: must be an object", i)
		}
		kind, _ := m["kind"].(string)
		text, _ := m["text"].(string)
		c := task.AcceptanceCriterion{
			Kind:   task.CriterionKind(kind),
			Text:   text,
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: authorAgentID},
		}
		if chk, ok := m["check"].(map[string]any); ok {
			command, _ := chk["command"].(string)
			var expectedExitCode int
			if v, ok := chk["expected_exit_code"].(float64); ok {
				expectedExitCode = int(v)
			}
			c.Check = &task.CriterionCheck{Command: command, ExpectedExitCode: expectedExitCode}
		}
		if beh, ok := m["behavior"].(map[string]any); ok {
			c.Behavior = task.DecodeBehaviorPayload(beh)
		}
		// ADR-074 D2: kind is optional at authoring time — resolve it from the
		// payload shape HERE, before the caller's ADR-049 D2-rule-5 all-check
		// bash-policy gate runs, so the gate fires on inferred kinds too. An
		// explicit kind passes through unchanged; kind-less with BOTH payloads
		// is rejected as ambiguous.
		k, kErr := task.InferCriterionKind(&c)
		if kErr != nil {
			return nil, fmt.Errorf("criteria[%d]: %w", i, kErr)
		}
		c.Kind = k
		out = append(out, c)
	}
	return out, nil
}

// allCheckCriteria reports whether criteria is non-empty and EVERY entry is
// kind=check (ADR-049 D2 rule 5 gate condition).
func allCheckCriteria(criteria []task.AcceptanceCriterion) bool {
	if len(criteria) == 0 {
		return false
	}
	for _, c := range criteria {
		if c.Kind != task.KindCheck {
			return false
		}
	}
	return true
}

// describeBashPolicy renders a bashPolicyChecker result for an error message:
// the resolved policy string, or "unresolvable" when the assignee agent
// itself could not be found.
func describeBashPolicy(policy string, ok bool) string {
	if !ok {
		return "unresolvable"
	}
	return policy
}

func (t *TaskCreateTool) Name() string           { return "create_task" }
func (t *TaskCreateTool) Scope() ToolScope       { return ScopeGeneral }
func (t *TaskCreateTool) Category() ToolCategory { return CategoryTasks }

func (t *TaskCreateTool) Description() string {
	return "Create a task and assign it to an agent for execution.\n" +
		"This is a DELEGATION: it passes the same delegation-policy gate (trust set + modes + depth) as " +
		"any other delegation, and is refused if you are not authorized to delegate to the assignee. " +
		"criteria is REQUIRED: at least one acceptance criterion (Definition of Done) — a task created " +
		"with none is rejected. Before authoring acceptance criteria, load the define-done skill " +
		"(via the Skill tool) and follow its quality bar. " +
		"If every criterion is kind=check, the assignee's effective bash policy " +
		"must be allow, or the create is rejected as structurally unsatisfiable (a machine check that can " +
		"never run can never adjudicate MET). The task lands as a visible card on the workspace board in " +
		"status `next` (triaged and dispatchable) — never `inbox`."
}

func (t *TaskCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Short title for the task",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Full instructions for the agent",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to assign the task to",
			},
			"priority": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     5,
				"description": "Priority 1 (highest) to 5 (lowest); default 3",
			},
			"due": map[string]any{
				"type":        "string",
				"description": "Due date/time in RFC 3339 format (optional)",
			},
			"parent_task_id": map[string]any{
				"type":        "string",
				"description": "ID of the parent task (optional) — set when this is a subtask of another task",
			},
			"plan_id": map[string]any{
				"type":        "string",
				"description": "ID of the Plan this task is a member of (optional). Must exist in the same workspace and must not be a terminal (done/failed) plan.",
			},
			"write_set": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Concrete paths this plan member creates/edits (optional). Meaningful only alongside plan_id; plan-lint reads this at approve to reject overlapping parallel streams. Empty/omitted for an exploratory member whose write footprint is unknowable up front.",
			},
			"stream": map[string]any{
				"type":        "string",
				"description": "The parallel-group id this plan member belongs to (optional). Members sharing a stream run serially within it; different streams may run concurrently provided their write_sets are disjoint.",
			},
			"is_join": map[string]any{
				"type":        "boolean",
				"description": "True marks this plan member as an authored join/assemble member with its own criteria, converging one or more parallel streams into a single artifact. Defaults to false.",
			},
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs this task is blocked by (optional). Each blocker must exist and be in the same workspace; a cycle is rejected.",
			},
			"criteria": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type": "string",
							"enum": []string{"check", "prose", "behavior"},
							"description": "check: a shell command verified via the assignee's own bash tool; " +
								"prose: a free-text statement judged by the Judge System Agent; " +
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
						"behavior": task.BehaviorCriterionParamSchema(),
					},
					"required": []string{"text"},
				},
				"description": "Acceptance criteria (Definition of Done) for this task. REQUIRED: at least " +
					"one criterion — an agent-created task with zero criteria is rejected.",
			},
		},
		"required": []string{"title", "prompt", "agent_id", "criteria"},
	}
}

// resolveBlockedBy parses the optional "blocked_by" array arg into a slice of
// non-empty task IDs. It distinguishes three cases so the caller can tell a
// CLEAR (provided empty) from an unchanged (absent) request:
//   - absent   → deps == nil, provided == false  (leave the field unchanged)
//   - provided → deps == nil/empty, provided == true (CLEAR the list)
//   - provided → deps non-empty, provided == true (REPLACE with deps)
//
// Empty-string and non-string entries are dropped (they are not valid task IDs).
func resolveBlockedBy(args map[string]any) (deps []string, provided bool) {
	rawDeps, ok := args["blocked_by"].([]any)
	if !ok {
		return nil, false
	}
	deps = make([]string, 0, len(rawDeps))
	for _, d := range rawDeps {
		if s, ok := d.(string); ok && s != "" {
			deps = append(deps, s)
		} else {
			slog.Debug("task: dropped invalid blocked_by entry", "entry", d)
		}
	}
	if len(deps) == 0 {
		return nil, true // provided but empty → caller treats as CLEAR
	}
	return deps, true
}

// validateBlockersWorkspace loads each blocker task and verifies it is in the
// same workspace as the dependent. This mirrors TaskAddDependencyTool's
// cross-workspace guard; the store's validateBlockedByLocked handles
// cycle/self-edge/missing/depth, but NOT the same-workspace constraint, so the
// tool layer enforces it.
//
// TOCTOU note: WorkspaceID is immutable (set at create, never in task.Patch), so
// the same-workspace check is race-free — a blocker's workspace cannot change
// between this pre-check and the locked write. The store re-validates the DAG
// invariants (cycle/self-edge/missing/depth) atomically under the per-task lock;
// this tool-layer guard adds the same-workspace rule the store does not enforce.
func validateBlockersWorkspace(store *task.Store, dependentWorkspaceID string, blockers []string) error {
	for _, b := range blockers {
		bt, err := store.Get(b)
		if err != nil {
			if errors.Is(err, task.ErrNotFound) {
				return fmt.Errorf("blocker task %q not found", b)
			}
			return fmt.Errorf("could not load blocker task %q: %w", b, err)
		}
		if bt.WorkspaceID != dependentWorkspaceID {
			return fmt.Errorf("blocker task %q is in a different workspace", b)
		}
	}
	return nil
}

// resolveWorkspaceID returns the workspace ID for the current turn. It first
// checks the turn context (explicitly bound workspace), then falls back to the
// real default workspace on disk (via workspace.ResolveDefaultID). It returns
// an error rather than inventing a literal ID, so chat-delegated tasks never
// land in an invisible workspace.
func (t *TaskCreateTool) resolveWorkspaceID(ctx context.Context) (string, error) {
	if ws := ToolWorkspaceID(ctx); ws != "" {
		// Belt-and-suspenders (M4): a bound id that no longer exists on disk
		// (stale/typo'd) would land the task on an invisible board. Treat a
		// non-existent ctx id as unbound and fall through to the default.
		if t.home == "" || workspace.Exists(t.home, ws) {
			return ws, nil
		}
		slog.Warn("create_task: bound workspace_id does not exist — falling back to default",
			"workspace_id", ws)
	}
	if t.home != "" {
		id, err := workspace.ResolveDefaultID(t.home)
		if err != nil {
			return "", fmt.Errorf("could not resolve default workspace: %w", err)
		}
		return id, nil
	}
	return "", fmt.Errorf("no active workspace bound and no default workspace resolver configured")
}

func (t *TaskCreateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("create_task failed: task store is not available")
	}
	title, _ := args["title"].(string)
	prompt, _ := args["prompt"].(string)
	agentID, _ := args["agent_id"].(string)
	callerID := strings.TrimSpace(ToolAgentID(ctx))

	if title == "" {
		return ErrorResult("title is required")
	}
	if prompt == "" {
		return ErrorResult("prompt is required")
	}
	if agentID == "" {
		return ErrorResult("agent_id is required")
	}
	// FAIL CLOSED on an unresolvable caller. create_task is a delegation: it
	// assigns work to ANOTHER agent, and both halves of that record — the
	// delegation-policy decision and the FR-037 provenance stamp
	// (Store.CreateByAgent, which itself rejects an empty agent id) — are
	// meaningless without a principal. Refusing here keeps the two consistent
	// rather than letting the policy gate run against an empty caller and then
	// discovering the missing principal at write time.
	if callerID == "" {
		return ErrorResult("create_task: cannot resolve the calling agent; refusing to create a delegated task")
	}

	// Delegation policy gate (FR-6.2): trust set + modes ("task") + depth.
	// ADR-037: the legacy boolean delegateCheck fallback is retired — this is
	// now the only gate. Checked BEFORE the criteria validation below so an
	// unauthorized caller always gets a consistent delegation-denied response
	// regardless of what else they did or didn't supply (authorization first,
	// business-rule validation second).
	//
	// FAIL CLOSED, not open, when no checker is wired: an unwired deny-checker
	// is a configuration error, never a permission grant. Unreachable in
	// today's production wiring (pkg/agent/loop.go always calls
	// SetDelegationDenyChecker), but the legacy fallback this replaced was
	// itself deny-by-default — removing it must not silently flip the
	// unwired-checker case from deny to allow for the NEXT wiring bug (a new
	// agent-construction path, a v0.3 plugin-system entry point, a refactor
	// slip). Do NOT "simplify" this back to fail-open — CLAUDE.md Hard
	// Constraint #6 forbids a silent runtime default here.
	if t.delegationDeny != nil {
		if denial := t.delegationDeny(ctx, agentID); denial != nil {
			return DelegationDeniedResult("create_task", denial)
		}
	} else {
		slog.Error("create_task: no delegation-deny checker installed — denying by default",
			"caller_id", callerID, "target_agent_id", agentID)
		return DelegationDeniedResult("create_task", &DelegationDenial{
			Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
			Policy:        DenyTrustSet,
			TargetAgentID: agentID,
		})
	}

	// FR-6/D5 strict criteria enforcement (ADR-049, SD-A7, review r1 major
	// M5): an agent-created task requires at least one acceptance criterion —
	// human/UI creation (which never calls this tool) may still leave
	// Criteria empty (the soft tier, judged against Prompt/title/description
	// at judge time instead, ADR-049 D5). rawCriteria absent or an empty
	// array both fail this check identically.
	rawCriteria, _ := args["criteria"].([]any)
	if len(rawCriteria) == 0 {
		return ErrorResult(
			"criteria is required: an agent-created task must supply at least one acceptance " +
				"criterion (Definition of Done) — ADR-049 D5/SD-A7",
		)
	}
	criteria, cErr := parseCriteriaArgs(rawCriteria, callerID)
	if cErr != nil {
		return ErrorResult(fmt.Sprintf("task_create failed: %v", cErr))
	}

	// D2 rule 5 (FR-017/052): an all-check criteria set can never be
	// adjudicated MET if the assignee's effective bash policy is deny or ask
	// (ask resolves to deny unattended at judge time, D2 rule 2) — the
	// machine check could never even run. Reject at write time rather than
	// let the task loop forever against a structurally unsatisfiable DoD.
	if allCheckCriteria(criteria) {
		if t.bashPolicyChecker == nil {
			// FAIL CLOSED, not open, when no checker is wired — same rationale
			// as the delegation gate above: an unwired checker is a
			// configuration error, never a permission grant.
			slog.Error("create_task: no bash-policy checker installed — denying an "+
				"all-check criteria set by default",
				"caller_id", callerID, "target_agent_id", agentID)
			return ErrorResult(
				"task_create failed: cannot verify the assignee's bash policy (D2 rule 5 checker not " +
					"configured) — denying an all-machine-criteria create by default",
			)
		}
		policy, ok := t.bashPolicyChecker(agentID)
		if !ok || policy != string(config.ToolPolicyAllow) {
			return ErrorResult(fmt.Sprintf(
				"task_create failed: all criteria are machine-checkable (kind=check) but agent %q's "+
					"effective bash policy is %q — this criteria set could never be satisfied "+
					"(structurally unsatisfiable, ADR-049 D2 rule 5)",
				agentID, describeBashPolicy(policy, ok),
			))
		}
	}

	// Task-mode recursion bound (SEC): a task_create issued from *within* a task
	// run carries that run's delegation generation on the context. Each task→task
	// hop increments the generation; reject once it would exceed the hard ceiling.
	// This is the runtime bound the per-agent depth gate cannot enforce on its own
	// because every task run starts a FRESH turn at turnState depth 0 — without
	// this counter, an A→B→A task-mode chain would recurse unboundedly.
	parentDepth := ToolDelegationDepth(ctx)
	childDepth := parentDepth + 1
	if t.maxDelegationDepth > 0 && childDepth > t.maxDelegationDepth {
		return DelegationDeniedResult("create_task", &DelegationDenial{
			Reason: fmt.Sprintf(
				"maximum task delegation depth (%d) reached — cannot create a further delegated task",
				t.maxDelegationDepth,
			),
			Policy:        DenyDepth,
			TargetAgentID: agentID,
		})
	}

	// M2(a)/(b) fix: args["priority"] being PRESENT (ok==true) means the caller
	// supplied an explicit value — including an explicit 0 — which must be
	// validated and, if invalid, REJECTED rather than silently replaced with
	// the default. The previous `ok && p >= 1 && p <= 5` guard let any
	// out-of-range value (0, 6, negative...) simply fail the condition and
	// fall through to priority=3 with no error at all: the caller received a
	// success response with their input silently discarded. task.ValidatePriority
	// is the same shared range-check update_task, create_task_in_workspace, and
	// the REST create/update handlers all use, so this can't drift out of sync
	// with them again.
	priority := 3
	if p, ok := args["priority"].(float64); ok {
		pr := int(p)
		if err := task.ValidatePriority(pr); err != nil {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", err))
		}
		priority = pr
	}

	// Optional due date (RFC 3339), mirroring update_task's own validation:
	// due is a general task attribute, not gated behind plan_id the way
	// write_set/stream/is_join are, so its absence here (while
	// create_task_in_workspace and update_task both accept it) was a genuine
	// schema-consistency gap rather than a deliberate restriction.
	var due string
	if d, ok := args["due"].(string); ok && d != "" {
		if _, pErr := time.Parse(time.RFC3339, d); pErr != nil {
			return ErrorResult(fmt.Sprintf("invalid due date %q (must be RFC 3339): %v", d, pErr))
		}
		due = d
	}

	parentTaskID, _ := args["parent_task_id"].(string)

	wsID, err := t.resolveWorkspaceID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not resolve workspace: %v", err))
	}

	// A delegated task is ready to be picked up by the executor: it lands in
	// `next` (triaged & dispatchable) rather than `inbox`. Detail #6: it carries
	// a parent link and the originating channel for result delivery.
	entity := &task.Task{
		Title:           title,
		Prompt:          prompt,
		Action:          task.ActionLLM,
		AgentID:         agentID,
		CreatedBy:       callerID,
		Priority:        priority,
		Due:             due,
		ParentTaskID:    parentTaskID,
		WorkspaceID:     wsID,
		Status:          task.StatusNext,
		DelegationDepth: childDepth,
		Criteria:        criteria,
	}

	// Propagate the originating channel so completed tasks can route results back.
	if channel := ToolChannel(ctx); channel != "" && channel != "webchat" {
		entity.SourceChannel = channel
		entity.SourceChatID = ToolChatID(ctx)
	}

	// Optional blocked_by: mirror admin create. The store's Create validates the
	// blocked_by DAG (cycle/self-edge/missing/depth); the tool layer additionally
	// enforces the same-workspace constraint (the store does not), mirroring
	// TaskAddDependencyTool's cross-workspace guard.
	//
	// For create, provided-empty (CLEAR) and absent are equivalent — a brand-new
	// task starts with no deps either way — so only the populated path sets deps.
	deps, depsProvided := resolveBlockedBy(args)
	if depsProvided && len(deps) > 0 {
		if wErr := validateBlockersWorkspace(t.store, wsID, deps); wErr != nil {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", wErr))
		}
		entity.BlockedBy = deps
	}

	// Optional plan_id (ADR-052 FR-002): same-workspace FK + not-terminal
	// (validateTaskPlanLinkage, plan.go). A create_task call with no plan_id
	// is entirely unaffected — the check is a no-op for planID == "".
	if planID, _ := args["plan_id"].(string); planID != "" {
		if pErr := validateTaskPlanLinkage(t.planStore, planID, wsID); pErr != nil {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", pErr))
		}
		entity.PlanID = planID
	}

	// Optional write_set/stream/is_join (ADR-053 §Contract Surface, US-11
	// G-16): meaningful only alongside plan_id, but accepted unconditionally
	// (ignored by plan-lint on a standalone task) — matching the wire
	// contract's own "meaningful only when plan_id is set" convention rather
	// than rejecting a caller who supplies them without a plan_id.
	if rawWriteSet, ok := args["write_set"].([]any); ok {
		writeSet := make([]string, 0, len(rawWriteSet))
		for _, p := range rawWriteSet {
			if s, ok := p.(string); ok && s != "" {
				writeSet = append(writeSet, s)
			}
		}
		entity.WriteSet = writeSet
	}
	if stream, ok := args["stream"].(string); ok {
		entity.Stream = stream
	}
	if isJoin, ok := args["is_join"].(bool); ok {
		entity.IsJoin = isJoin
	}

	// CreateByAgent, not Create: this is the AGENT creation path, so the task
	// carries FR-037 provenance (Task.CreatedByAgentID = the calling agent) in
	// the agent-id namespace. That stamp is what makes the created task
	// findable by its author — list_jobs' dispatched half and list_tasks
	// role="delegator" both filter on it, and neither can use the
	// mixed-namespace CreatedBy. callerID is guaranteed non-empty by the
	// fail-closed guard at the top of Execute, so CreateByAgent's own
	// empty-agent-id rejection is belt-and-suspenders here, not the primary
	// gate.
	if err := t.store.CreateByAgent(entity, callerID); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", err))
		}
		return ErrorResult(fmt.Sprintf("task_create failed: %v", err))
	}

	if t.onCreate != nil {
		t.onCreate(entity)
	}

	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"status":%q}`, entity.ID, entity.Status))
}

// TaskUpdateTool allows an agent to update status of its own task.
type TaskUpdateTool struct {
	BaseTool
	store *task.Store
	// delegationDeny, when non-nil, applies the full delegation policy (trust
	// set + modes ("task") + depth — FR-6.2) to a reassignment. Returns a
	// non-nil *DelegationDenial to DENY or nil to ALLOW. This is the ONLY
	// delegation gate (ADR-037 retired the legacy boolean delegateCheck
	// fallback). Reassignment is re-delegation, so it routes through the
	// SAME gate task_create uses.
	delegationDeny func(ctx context.Context, targetAgentID string) *DelegationDenial
	onComplete     func(*task.Task)
}

func NewTaskUpdateTool(store *task.Store) *TaskUpdateTool {
	return &TaskUpdateTool{store: store}
}

// SetOnComplete sets the callback invoked when a task reaches a terminal status.
func (t *TaskUpdateTool) SetOnComplete(fn func(*task.Task)) {
	t.onComplete = fn
}

// SetDelegationDenyChecker installs the full delegation-policy gate (FR-6.2)
// for reassignment. Reassignment is re-delegation, so it routes through the
// SAME gate task_create uses.
func (t *TaskUpdateTool) SetDelegationDenyChecker(
	fn func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDeny = fn
}

// deferDoneClaimToJudge reports whether an explicit update_task(status:"done")
// call on t must be staged as a pending judge claim rather than written as a
// terminal `done` immediately (ADR-049 C1/SD-B2, review r1 blocker). This is
// the FR-041 evidence-ladder judge closing the #1 self-certification bypass:
// a worker could previously call update_task(done) directly and skip the
// judge entirely, even though the SAME claim arriving via the TASK_STATUS
// completion marker (task_executor.go finishTaskRun/adjudicateClaim) was
// always judged.
//
//   - A task with explicit acceptance criteria (hard tier, len(Criteria)>0)
//     is ALWAYS judged — this returns true.
//   - A criteria-less task (soft tier — ADR-049 D5 synthesizes an implicit
//     prose criterion from Prompt/title/description at judge time) keeps
//     today's exact behavior: an explicit done write is trusted immediately,
//     unchanged by this fix (explicitly accepted scope per review r1 C1).
//   - A Scratchpad task (FR-048, set_todos-created checklist tracking) is
//     exempt from the goal loop entirely, mirroring finishTaskRun's own
//     Scratchpad exemption — trusted immediately regardless of criteria.
func deferDoneClaimToJudge(t *task.Task, newStatus task.Status) bool {
	return newStatus == task.StatusDone && !t.Scratchpad && len(t.Criteria) > 0
}

func (t *TaskUpdateTool) Name() string           { return "update_task" }
func (t *TaskUpdateTool) Scope() ToolScope       { return ScopeGeneral }
func (t *TaskUpdateTool) Category() ToolCategory { return CategoryTasks }

func (t *TaskUpdateTool) Description() string {
	return "Update a task assigned to you or that you created: status, title, priority, due date, agent_id, or blocked_by.\n" +
		"Mark status (done/failed — in_progress is reached only through real dispatch via run_task, " +
		"never written directly here). If the task has acceptance criteria, a done claim is NOT applied " +
		"directly: during that task's own run it is recorded as a claim for the evidence-ladder judge " +
		"(the task stays non-terminal and the response says so), and outside that run it is refused. " +
		"Tasks with no criteria are marked done immediately. Only provided fields are updated."
}

func (t *TaskUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID of the task to update",
			},
			"status": map[string]any{
				"type": "string",
				"enum": []string{"done", "failed"},
				"description": "New status for the task. in_progress is NOT settable here (issue #593) — " +
					"it is only ever reached through real dispatch; use run_task to actually start this task.",
			},
			"result": map[string]any{
				"type":        "string",
				"description": "Summary of what was accomplished (for done/failed)",
			},
			"artifacts": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "File paths or URLs produced as artifacts",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "New title for the task (1-200 chars)",
			},
			"priority": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     5,
				"description": "Priority 1 (highest) to 5 (lowest)",
			},
			"due": map[string]any{
				"type":        "string",
				"description": "Due date/time in RFC 3339 format",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to reassign the task to",
			},
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of task IDs this task is blocked by. Pass the full list to REPLACE the existing deps; pass [] to CLEAR; omit to leave unchanged. Each blocker must exist + be same-workspace; cycles rejected.",
			},
			"write_set": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Replacement set of concrete paths this plan member creates/edits. Pass the full list to REPLACE; pass [] to CLEAR; omit to leave unchanged. Meaningful only alongside plan_id.",
			},
			"stream": map[string]any{
				"type":        "string",
				"description": "New parallel-group id this plan member belongs to. Pass \"\" to CLEAR; omit to leave unchanged.",
			},
			"is_join": map[string]any{
				"type":        "boolean",
				"description": "Set/clear whether this plan member is an authored join/assemble member.",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskUpdateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("update_task failed: task store is not available")
	}
	taskID, _ := args["task_id"].(string)
	callerID := ToolAgentID(ctx)
	if callerID == "" {
		return ErrorResult("agent ID not set in context; cannot verify task ownership")
	}

	if taskID == "" {
		return ErrorResult("task_id is required")
	}

	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}

	// CreatedByAgent, never CreatedBy. Task.CreatedBy is MIXED-NAMESPACE
	// (a human username on the REST path, an agent id on the tool path) and
	// its own doc comment on Task.CreatedBy / Task.CreatedByAgent forbids
	// using CreatedBy as an ownership predicate. CreatedByAgent reads the
	// agent-id-namespaced CreatedByAgentID and fails closed on both sides,
	// so a task's creator (the delegator) can update it even when it is
	// assigned to a different agent — the same union check delete_task's
	// gate applies. Reassignment of the assignee itself still routes
	// through the separate delegationDeny gate below.
	if existing.AgentID != callerID && !existing.CreatedByAgent(callerID) {
		return ErrorResult("you can only update tasks you own or are assigned")
	}

	patch := task.Patch{}
	updatedFields := []string{}

	// Status (optional — was the only field historically; still the common path).
	var newStatus task.Status
	statusStr, _ := args["status"].(string)
	if statusStr != "" {
		st := task.Status(statusStr)
		if !task.IsValidStatus(st) {
			return ErrorResult(fmt.Sprintf("invalid status %q", statusStr))
		}
		// Issue #593 (Option A): in_progress is a DISPATCH state, not a
		// caller-settable status the way done/failed are. The only legitimate
		// writers are the executor's ClaimForRun, REST's handleTaskPatch,
		// run_task, and set_todos-via-Create — none of which call this tool.
		// Before this guard, ANY permitted caller — including the task's own
		// creator, who passes the ownership union check above — could force
		// next/inbox -> in_progress here with no session, no goroutine, and
		// nothing that will ever revisit it: the task then reads "running"
		// forever. Mirror the existing done-with-criteria guard's shape below
		// (reject outright, name the real path) rather than silently accept a
		// forged state. A resend on a task that is ALREADY in_progress is a
		// harmless no-op and is let through unchanged — only a transition INTO
		// in_progress from a different status is rejected.
		if st == task.StatusInProgress && existing.Status != task.StatusInProgress {
			return ErrorResult("in_progress cannot be set directly — it is only ever reached through " +
				"real dispatch; call run_task to actually start this task")
		}
		newStatus = st
		updatedFields = append(updatedFields, "status")
		if deferDoneClaimToJudge(existing, st) {
			// review r2 Chunk 1: a hard-tier done claim is ONLY ever
			// adjudicated inside THAT task's own executor run — finishTaskRun
			// (task_executor.go) is the sole reader of Task.PendingJudgeClaim.
			// An out-of-band call (this task is not the caller's
			// currently-running task, e.g. a stale/idle criteria task nobody
			// is executing, or a different agent poking at it) must be
			// rejected outright rather than staged: nothing would ever
			// adjudicate that claim, stranding the task non-terminal forever
			// (bounded only by boot's reconcileStuckTasks). Status is
			// deliberately left unpatched in the genuine in-run case too —
			// the PendingJudgeClaim block after the result/artifacts fields
			// stages the claim instead.
			if ToolRunningTaskID(ctx) != taskID {
				return ErrorResult("this task has acceptance criteria — completion is adjudicated by " +
					"the judge during a task run; it cannot be force-completed here")
			}
		} else {
			patch.Status = &st
		}
	}

	// Result / artifacts — accepted with or without a status.
	deferToJudge := deferDoneClaimToJudge(existing, newStatus)
	var claimText string
	if result, ok := args["result"].(string); ok && result != "" {
		claimText = result
		if deferToJudge {
			// Captured below as the judge's claim text (adjudicateClaim),
			// NOT written to Task.Result directly — Task.Result stays the
			// goal loop's own in-flight steering carrier between attempts
			// (task_executor.go writeSteeringPrompt's doc comment) until the
			// judge decides; completeTaskWithResult overwrites it with the
			// real final result once terminal.
		} else {
			patch.Result = &result
			updatedFields = append(updatedFields, "result")
		}
	}
	if deferToJudge {
		patch.PendingJudgeClaim = &claimText
		updatedFields = append(updatedFields, "pending_judge_claim")
	}
	if rawArtifacts, ok := args["artifacts"].([]any); ok {
		artifacts := make([]string, 0, len(rawArtifacts))
		for _, a := range rawArtifacts {
			if s, ok := a.(string); ok {
				artifacts = append(artifacts, s)
			}
		}
		patch.Artifacts = &artifacts
		updatedFields = append(updatedFields, "artifacts")
	}

	// Title.
	if title, ok := args["title"].(string); ok && title != "" {
		patch.Title = &title
		updatedFields = append(updatedFields, "title")
	}

	// Priority (1-5). Shared range-check with create_task/create_task_in_
	// workspace/REST (task.ValidatePriority) — see its doc comment.
	if p, ok := args["priority"].(float64); ok {
		pr := int(p)
		if pErr := task.ValidatePriority(pr); pErr != nil {
			return ErrorResult(pErr.Error())
		}
		patch.Priority = &pr
		updatedFields = append(updatedFields, "priority")
	}

	// Due (RFC 3339 string).
	if due, ok := args["due"].(string); ok && due != "" {
		if _, pErr := time.Parse(time.RFC3339, due); pErr != nil {
			return ErrorResult(fmt.Sprintf("invalid due date %q (must be RFC 3339): %v", due, pErr))
		}
		patch.Due = &due
		updatedFields = append(updatedFields, "due")
	}

	// agent_id (reassign). Reassignment is re-delegation: when the new agent
	// differs from the current assignee, route it through the SAME delegation-
	// policy gate task_create uses (FR-6.2). A no-op reassign (same agent) needs
	// no gate. Mirrors TaskCreateTool.Execute's denial shape.
	if agentID, ok := args["agent_id"].(string); ok && agentID != "" && agentID != existing.AgentID {
		// FAIL CLOSED, not open, when no checker is wired — same rationale as
		// TaskCreateTool.Execute above: an unwired deny-checker is a
		// configuration error, never a permission grant. Do NOT "simplify"
		// this back to fail-open.
		if t.delegationDeny != nil {
			if denial := t.delegationDeny(ctx, agentID); denial != nil {
				return DelegationDeniedResult("update_task", denial)
			}
		} else {
			slog.Error("update_task: no delegation-deny checker installed — denying by default",
				"caller_id", callerID, "target_agent_id", agentID)
			return DelegationDeniedResult("update_task", &DelegationDenial{
				Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
				Policy:        DenyTrustSet,
				TargetAgentID: agentID,
			})
		}
		patch.AgentID = &agentID
		updatedFields = append(updatedFields, "agent_id")
	}

	// blocked_by (replaces the list). Cross-workspace guard at the tool layer
	// (mirrors TaskAddDependencyTool's cross-workspace guard); the store's
	// validateBlockedByLocked handles cycle/self-edge/missing/depth atomically
	// under the per-task lock.
	//
	// Three-way: provided-empty CLEARs the list, populated REPLACEs it, absent
	// leaves it unchanged.
	deps, depsProvided := resolveBlockedBy(args)
	if depsProvided {
		if len(deps) == 0 {
			// CLEAR — empty list trivially passes the cross-workspace guard.
			patch.BlockedBy = &[]string{}
			updatedFields = append(updatedFields, "blocked_by")
		} else {
			if wErr := validateBlockersWorkspace(t.store, existing.WorkspaceID, deps); wErr != nil {
				return ErrorResult(fmt.Sprintf("task_update failed: %v", wErr))
			}
			patch.BlockedBy = &deps
			updatedFields = append(updatedFields, "blocked_by")
		}
	}

	// write_set (ADR-053 §Contract Surface, US-11 G-16): three-way, mirroring
	// blocked_by — provided-empty CLEARs the declared write-set (reverts to
	// an exploratory member, D10), populated REPLACEs it, absent leaves it
	// unchanged.
	if rawWriteSet, ok := args["write_set"].([]any); ok {
		writeSet := make([]string, 0, len(rawWriteSet))
		for _, p := range rawWriteSet {
			if s, ok := p.(string); ok && s != "" {
				writeSet = append(writeSet, s)
			}
		}
		patch.WriteSet = &writeSet
		updatedFields = append(updatedFields, "write_set")
	}

	// stream (empty string CLEARs the label; absent leaves it unchanged).
	if stream, ok := args["stream"].(string); ok {
		patch.Stream = &stream
		updatedFields = append(updatedFields, "stream")
	}

	// is_join (plain overwrite; absent leaves it unchanged).
	if isJoin, ok := args["is_join"].(bool); ok {
		patch.IsJoin = &isJoin
		updatedFields = append(updatedFields, "is_join")
	}

	if len(updatedFields) == 0 {
		return ErrorResult(
			"no updatable fields provided (supply at least one of status, result, artifacts, title, priority, due, agent_id, blocked_by, write_set, stream, is_join)",
		)
	}

	// Timestamps keyed off status (unchanged behavior for the status path). A
	// deferred done-claim is NOT actually terminal yet (review r1 C1) — the
	// judge decides — so CompletedAt is not stamped until adjudication lands
	// (task_executor.go completeTaskWithResult stamps it then, via the normal
	// Update path).
	now := time.Now().UTC().Format(time.RFC3339)
	switch newStatus {
	case task.StatusInProgress:
		patch.StartedAt = &now
	case task.StatusDone, task.StatusFailed:
		if !deferToJudge {
			patch.CompletedAt = &now
		}
	}

	updated, err := t.store.Update(taskID, patch)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_update failed: %v", err))
	}

	// FR-6.5: when the task newly reaches "done", advance dependents (mirror
	// admin: pkg/sysagent/tools/task.go:252-259). The primary update already
	// persisted, so this is best-effort: a storage fault here is surfaced to the
	// caller as an advance_warning rather than turning a successful update into a
	// failure (which would orphan dependents with no signal either way).
	//
	// A deferred done-claim (review r1 C1/SD-B2) must NOT advance dependents
	// or fire onComplete here — the task has not actually reached `done`
	// (patch.Status was deliberately left unset above), so newStatus=="done"
	// alone is no longer sufficient to gate these; the judge
	// (task_executor.go adjudicateClaim -> completeTaskWithResult) is the
	// only path that may do so, once it actually adjudicates the claim MET.
	var advanceWarning string
	if newStatus == task.StatusDone && !deferToJudge {
		advanced, advErr := t.store.AdvanceBlockedDependents(taskID)
		if advErr != nil {
			// Storage fault advancing dependents — the update itself succeeded,
			// so this stays a success, but the warning must reach the LLM/user.
			slog.Error("update_task: advance dependents failed",
				"id", taskID, "error", advErr)
			advanceWarning = advErr.Error()
		} else if len(advanced) > 0 {
			slog.Info("update_task: completed task advanced dependents",
				"completed_id", taskID, "advanced_ids", advanced)
		}
	}

	if task.IsTerminal(newStatus) && !deferToJudge && t.onComplete != nil {
		t.onComplete(updated)
	}

	// Marshal cannot fail on a []string (updatedFields is always a concrete
	// slice of strings), so the error is impossible in practice — discard it.
	updatedFieldsJSON, _ := json.Marshal(updatedFields)
	result := fmt.Sprintf(`{"task_id":%q,"status":%q,"updated_fields":%s}`,
		updated.ID, updated.Status, string(updatedFieldsJSON))
	if deferToJudge {
		// FR-041/SD-B2 (review r1 C1): tell the calling agent its done claim
		// was received but is NOT yet final — this task has explicit
		// acceptance criteria, so the evidence-ladder judge must adjudicate
		// the claim (task_executor.go adjudicateClaim) before the task can
		// actually reach `done`. Built with json.Marshal for safe escaping.
		const pendingNote = "completion claim recorded — this task has acceptance criteria, so it is " +
			"NOT yet done; the evidence-ladder judge will adjudicate your claim against the criteria " +
			"before the task can reach a terminal status"
		noteJSON, _ := json.Marshal(pendingNote)
		result = fmt.Sprintf(`{"task_id":%q,"status":%q,"updated_fields":%s,"pending_judge_note":%s}`,
			updated.ID, updated.Status, string(updatedFieldsJSON), string(noteJSON))
	} else if advanceWarning != "" {
		// Append the warning as an escaped string field so the LLM/user can see
		// the dependents were not advanced. Built with json.Marshal so the error
		// message is properly escaped into a JSON string literal.
		warnJSON, _ := json.Marshal(advanceWarning)
		result = fmt.Sprintf(`{"task_id":%q,"status":%q,"updated_fields":%s,"advance_warning":%s}`,
			updated.ID, updated.Status, string(updatedFieldsJSON), string(warnJSON))
	}
	return NewToolResult(result)
}

// --- TaskDeleteTool ---

type TaskDeleteTool struct {
	BaseTool
	store *task.Store
}

func NewTaskDeleteTool(store *task.Store) *TaskDeleteTool {
	return &TaskDeleteTool{store: store}
}

func (t *TaskDeleteTool) Name() string           { return "delete_task" }
func (t *TaskDeleteTool) Scope() ToolScope       { return ScopeGeneral }
func (t *TaskDeleteTool) Category() ToolCategory { return CategoryTasks }
func (t *TaskDeleteTool) Description() string {
	return "Permanently delete a to-do/task item by task_id. Only use when explicitly asked to remove a " +
		"task. You may only delete a task you own — one you created or are assigned to; a task created " +
		"or assigned to someone else is refused, with no delegation override on this path."
}

func (t *TaskDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "ID of the task to delete"},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskDeleteTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("delete_task failed: task store is not available")
	}
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return ErrorResult("task_id is required")
	}

	callerID := strings.TrimSpace(ToolAgentID(ctx))
	if callerID == "" {
		return ErrorResult("agent ID not set in context; cannot verify task ownership")
	}

	// Ownership gate: load the task first to verify the caller owns it.
	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}
	// CreatedByAgent, never CreatedBy. Task.CreatedBy is MIXED-NAMESPACE and its
	// own doc comment states the rule this predicate used to break: it "MUST
	// NEVER be used as an ownership or authorization predicate", because the
	// REST path writes a human USERNAME into it (pkg/gateway/rest_tasks.go)
	// while callerID is an agent id. A human who registers the username `jim`
	// would otherwise have every task they created become deletable by the
	// agent `jim` — and the base roster ids (mia/jim/ava/ray) are all plausible
	// usernames. CreatedByAgent reads the agent-id-namespaced CreatedByAgentID
	// and fails closed on BOTH sides, so "" is never a wildcard in either
	// direction.
	//
	// This does NOT narrow the legitimate case: this file's own create_task
	// persists through Store.CreateByAgent, which stamps CreatedByAgentID with
	// the same callerID, so an agent can still delete what it created. What it
	// drops is deletion of REST/human-created tasks, which carry no agent
	// attribution at all and were never this caller's to delete.
	if existing.AgentID != callerID && !existing.CreatedByAgent(callerID) {
		return ErrorResult("you can only modify/delete tasks you own or are assigned")
	}

	unblocked, err := t.store.Delete(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		if !errors.Is(err, task.ErrCascadeEdgeCleanupFailed) {
			return ErrorResult(fmt.Sprintf("could not delete task: %v", err))
		}
		// The task itself was deleted; only cleaning up OTHER tasks' dangling
		// blocked_by edges partially failed. Non-fatal — log and continue
		// reporting success for the primary delete, matching how this file's
		// update_task path already treats AdvanceBlockedDependents's
		// write-failure error as a logged, non-fatal side effect.
		slog.Warn("delete_task: cascade edge cleanup partially failed", "deleted_id", taskID, "error", err)
	}

	// Advance any dependents that became fully unblocked by this delete.
	// The cascade only rewrote the blocked_by edges; a task that was `blocked`
	// with this task as its only blocker must be moved to `next`. AdvanceUnblocked
	// is a no-op when the dependent is not `blocked` and uses the internal hatch
	// so the transition guard does not reject blocked→next.
	for _, depID := range unblocked {
		if _, uErr := t.store.AdvanceUnblocked(depID); uErr != nil {
			slog.Warn(
				"delete_task: advance unblocked dependent failed",
				"deleted_id",
				taskID,
				"dependent_id",
				depID,
				"error",
				uErr,
			)
			continue
		}
		slog.Info("delete_task: advanced unblocked dependent blocked→next", "deleted_id", taskID, "advanced_id", depID)
	}

	return NewToolResult(fmt.Sprintf(`{"deleted":%q}`, taskID))
}

// --- AgentListTool ---

type AgentListTool struct {
	BaseTool
	listAgents func() []AgentInfo
}

type AgentInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func NewAgentListTool(lister func() []AgentInfo) *AgentListTool {
	return &AgentListTool{listAgents: lister}
}

func (t *AgentListTool) Name() string           { return "list_agents" }
func (t *AgentListTool) Scope() ToolScope       { return ScopeGeneral }
func (t *AgentListTool) Category() ToolCategory { return CategoryAgents }
func (t *AgentListTool) Description() string {
	return "List all available agents with their IDs, names, and type.\n" +
		"type is one of core/Main/Subagent/subagent_3p — you cannot chat-delegate to a Subagent or " +
		"subagent_3p worker. Use this to resolve agent names to IDs before delegating tasks. Being " +
		"listed here does not mean you may delegate to that agent — delegation trust is scoped per " +
		"workspace and is checked when you actually call."
}

func (t *AgentListTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *AgentListTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.listAgents == nil {
		return ErrorResult("list_agents failed: agent lister is not configured")
	}
	agents := t.listAgents()
	data, err := json.Marshal(agents)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not serialize agent list: %v", err))
	}
	return NewToolResult(string(data))
}
