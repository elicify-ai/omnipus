package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// SetTodosTool is the agent-facing scratchpad tool: a task-ID-opaque checklist
// for the goal the agent is currently working on. It uses full-replace semantics
// (no item IDs) and creates a board-visible tracking card on first use per goal.
//
// The agent never sees a task_id — this is a facade. The internal card is found
// or created by (agentID, goal-title) matching. Only tasks with Scratchpad==true
// are managed by this facade; real create_task cards are never touched.
type SetTodosTool struct {
	BaseTool
	store *task.Store
	home  string
}

// NewSetTodosTool constructs a SetTodosTool. store may be nil for metadata-only
// catalog instances (GeneralBuiltinMetadata). home is needed for workspace
// resolution; SetHome must be called before Execute when home is empty.
func NewSetTodosTool(store *task.Store) *SetTodosTool {
	return &SetTodosTool{store: store}
}

// SetHome configures the OMNIPUS_HOME path so that set_todos can resolve the
// default workspace ID when no workspace is bound to the turn context.
func (t *SetTodosTool) SetHome(home string) {
	t.home = home
}

func (t *SetTodosTool) Name() string           { return "set_todos" }
func (t *SetTodosTool) Scope() ToolScope       { return ScopeCore }
func (t *SetTodosTool) Category() ToolCategory { return CategoryTasks }

func (t *SetTodosTool) Description() string {
	return "Set your working scratchpad: a flat checklist of steps for the goal you're currently focused on. " +
		"Pass the FULL list every call (replace-semantics — no item IDs). " +
		"Use this for the throwaway checklist of what you're doing this turn; " +
		"use create_task + dependencies for a durable multi-wave plan. " +
		"Your checklist is shown on the board and re-shown to you each turn. " +
		"You have ONE active checklist at a time: calling this with a different `goal` closes the " +
		"previous checklist card as done and starts a fresh one, so reuse the exact same `goal` string " +
		"for every update to the same unit of work."
}

func (t *SetTodosTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"goal": map[string]any{
				"type":        "string",
				"description": "The title of the unit of work these todos serve",
			},
			"todos": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":        "string",
							"description": "Checklist item text (1-500 chars)",
						},
						"status": map[string]any{
							"type":        "string",
							"enum":        []string{"pending", "in_progress", "completed"},
							"description": "Status of this item",
						},
					},
					"required": []string{"text"},
				},
				"description": "Full todo list (replace-semantics). Empty array clears the list.",
			},
		},
		"required": []string{"goal", "todos"},
	}
}

func (t *SetTodosTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("set_todos: no task store configured")
	}

	goal, _ := args["goal"].(string)
	if goal == "" {
		return ErrorResult("goal is required")
	}

	agentID := ToolAgentID(ctx)
	if agentID == "" {
		return ErrorResult("could not resolve acting agent")
	}

	// Parse the todos array from the raw args (arrives as []any of map[string]any).
	rawTodos, _ := args["todos"].([]any)
	todos, parseErr := parseTodosArg(rawTodos)
	if parseErr != nil {
		return ErrorResult(parseErr.Error())
	}

	// Resolve workspace ID for creating a new tracking card if needed.
	wsID, wsErr := t.resolveWorkspaceID(ctx)
	if wsErr != nil {
		return ErrorResult(fmt.Sprintf("could not resolve workspace: %v", wsErr))
	}

	// Find this agent's current active SCRATCHPAD task whose Title == goal.
	existing, findErr := t.findActiveGoalTask(agentID, goal)
	if findErr != nil {
		return ErrorResult(fmt.Sprintf("set_todos: list tasks: %v", findErr))
	}

	var updated *task.Task
	if existing != nil {
		// Replace the checklist on the existing scratchpad card.
		var err error
		updated, err = t.store.SetTodos(existing.ID, todos)
		if err != nil {
			return ErrorResult(fmt.Sprintf("set_todos: update todos: %v", err))
		}
	} else {
		// No matching scratchpad card: archive any OTHER active scratchpad cards
		// for this agent (different goal) so only one active scratchpad card exists
		// per agent at a time (board-pollution guard). Real create_task cards
		// (Scratchpad==false) are never touched.
		if archErr := t.archiveOtherScratchpadCards(agentID, goal); archErr != nil {
			slog.Warn("set_todos: could not archive prior scratchpad cards",
				"agent_id", agentID,
				"new_goal", goal,
				"error", archErr,
			)
			// Non-fatal: proceed to create the new card even if archival partially fails.
		}

		// Atomic create: build the card with Scratchpad==true AND Todos set inline
		// in one store.Create call. This eliminates the partial-failure orphan window
		// that would occur with a separate SetTodos second write.
		card := &task.Task{
			Title:       goal,
			Prompt:      goal,
			Action:      task.ActionLLM,
			AgentID:     agentID,
			CreatedBy:   agentID,
			WorkspaceID: wsID,
			Status:      task.StatusInProgress,
			Priority:    3,
			Scratchpad:  true,
			Todos:       todos,
		}
		// CreateByAgent, not Create: a scratchpad card is created BY this agent
		// FOR this agent, so it carries FR-037 provenance
		// (Task.CreatedByAgentID) in the agent-id namespace — CreatedBy above
		// is the mixed-namespace display field and cannot serve as an
		// ownership predicate. agentID is guaranteed non-empty by the
		// "could not resolve acting agent" guard at the top of Execute.
		if err := t.store.CreateByAgent(card, agentID); err != nil {
			return ErrorResult(fmt.Sprintf("set_todos: create goal task: %v", err))
		}
		updated = card
	}

	return NewToolResult(renderChecklist(updated.Title, updated.Todos))
}

// findActiveGoalTask returns the most-recent active SCRATCHPAD task for agentID
// whose Title matches goal, or nil when no such task exists. Real create_task
// cards (Scratchpad==false) are intentionally excluded — this prevents the hijack
// bug where set_todos could overwrite a user task's checklist if the titles match.
func (t *SetTodosTool) findActiveGoalTask(agentID, goal string) (*task.Task, error) {
	tasks, err := t.store.List(task.Filter{AgentID: agentID})
	if err != nil {
		return nil, err
	}
	var found *task.Task
	for i := range tasks {
		tk := &tasks[i]
		if !tk.Scratchpad {
			continue
		}
		if tk.Title != goal {
			continue
		}
		if task.IsTerminal(tk.Status) {
			continue
		}
		// List is sorted priority ASC then created_at ASC; last match is
		// most-recently-created among equal-priority active scratchpad cards.
		found = tk
	}
	return found, nil
}

// archiveOtherScratchpadCards sets Status=done on every active scratchpad card
// owned by agentID whose Title differs from the new goal. This enforces the
// "at most one active scratchpad card per agent" invariant without touching real
// create_task cards (Scratchpad==false).
func (t *SetTodosTool) archiveOtherScratchpadCards(agentID, newGoal string) error {
	tasks, err := t.store.List(task.Filter{AgentID: agentID})
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	done := task.StatusDone
	for i := range tasks {
		tk := &tasks[i]
		if !tk.Scratchpad {
			continue
		}
		if tk.Title == newGoal {
			continue
		}
		if task.IsTerminal(tk.Status) {
			continue
		}
		if _, updateErr := t.store.Update(tk.ID, task.Patch{Status: &done}); updateErr != nil {
			slog.Warn("set_todos: could not archive scratchpad card",
				"task_id", tk.ID,
				"title", tk.Title,
				"agent_id", agentID,
				"error", updateErr,
			)
			// Return first error so callers can react, but we log each one.
			return fmt.Errorf("archive %q: %w", tk.ID, updateErr)
		}
	}
	return nil
}

// resolveWorkspaceID returns the workspace ID from the turn context or the
// disk default, mirroring TaskCreateTool.resolveWorkspaceID.
func (t *SetTodosTool) resolveWorkspaceID(ctx context.Context) (string, error) {
	if ws := ToolWorkspaceID(ctx); ws != "" {
		if t.home == "" || workspace.Exists(t.home, ws) {
			return ws, nil
		}
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

// parseTodosArg converts the raw []any slice from LLM args into []task.Todo.
// Each element must be a map with "text" (string, non-empty) and optional
// "status" (one of pending/in_progress/completed; default "pending").
func parseTodosArg(raw []any) ([]task.Todo, error) {
	todos := make([]task.Todo, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("todos[%d]: expected an object, got %T", i, item)
		}
		text, _ := m["text"].(string)
		if text == "" {
			return nil, fmt.Errorf("todos[%d]: text must not be empty", i)
		}
		statusRaw, _ := m["status"].(string)
		if statusRaw == "" {
			statusRaw = string(task.TodoPending)
		}
		st := task.TodoStatus(statusRaw)
		if !task.IsValidTodoStatus(st) {
			return nil, fmt.Errorf(
				"todos[%d]: invalid status %q (must be pending, in_progress, or completed)",
				i,
				statusRaw,
			)
		}
		todos = append(todos, task.Todo{Text: text, Status: st})
	}
	return todos, nil
}

// renderChecklist formats the updated checklist for the agent's read-on-write
// response. The task_id is intentionally omitted (facade).
func renderChecklist(goal string, todos []task.Todo) string {
	var sb strings.Builder
	sb.WriteString("Goal: ")
	sb.WriteString(goal)
	sb.WriteByte('\n')
	for _, td := range todos {
		sb.WriteString("- [")
		sb.WriteString(string(td.Status))
		sb.WriteString("] ")
		sb.WriteString(td.Text)
		sb.WriteByte('\n')
	}
	if len(todos) == 0 {
		sb.WriteString("(checklist cleared)\n")
	}
	return sb.String()
}
