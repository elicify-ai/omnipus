// task_query.go: Read-only task operations: list, get, filter, format for the model

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TaskListTool lists tasks for the calling agent.
type TaskListTool struct {
	BaseTool
	store *task.Store
}

func NewTaskListTool(store *task.Store) *TaskListTool {
	return &TaskListTool{store: store}
}

func (t *TaskListTool) Name() string { return "list_tasks" }

func (t *TaskListTool) Scope() ToolScope { return ScopeGeneral }

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
// Scratchpad, DelegationDepth — and a whole-struct marshal
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

type AgentInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// --- AgentListTool ---

type AgentListTool struct {
	BaseTool
	listAgents func() []AgentInfo
}

func NewAgentListTool(lister func() []AgentInfo) *AgentListTool {
	return &AgentListTool{listAgents: lister}
}

func (t *AgentListTool) Name() string { return "list_agents" }

func (t *AgentListTool) Scope() ToolScope { return ScopeGeneral }

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
