package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/task"
)

// TestSetTodos_NewGoalCreatesTask proves that calling set_todos with a goal that
// does not yet exist creates a board-visible task with that goal title, agentID,
// and the supplied todos.
func TestSetTodos_NewGoalCreatesTask(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithWorkspaceID(ctx, "ws-1")

	result := tool.Execute(ctx, map[string]any{
		"goal": "implement feature X",
		"todos": []any{
			map[string]any{"text": "write tests", "status": "pending"},
			map[string]any{"text": "write code", "status": "in_progress"},
		},
	})
	if result.IsError {
		t.Fatalf("set_todos failed: %s", result.ForLLM)
	}

	// A board task must have been created.
	tasks, err := store.List(task.Filter{AgentID: "agent-a"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	tk := tasks[0]
	if tk.Title != "implement feature X" {
		t.Errorf("expected title 'implement feature X', got %q", tk.Title)
	}
	if tk.AgentID != "agent-a" {
		t.Errorf("expected agent_id 'agent-a', got %q", tk.AgentID)
	}
	if len(tk.Todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(tk.Todos))
	}
	if tk.Todos[0].Text != "write tests" || tk.Todos[0].Status != task.TodoPending {
		t.Errorf("todo[0] wrong: %+v", tk.Todos[0])
	}
	if tk.Todos[1].Text != "write code" || tk.Todos[1].Status != task.TodoInProgress {
		t.Errorf("todo[1] wrong: %+v", tk.Todos[1])
	}
}

// TestSetTodos_SameGoalReplacesChecklist proves that calling set_todos twice for
// the same goal REPLACES the list (no duplicates, same task).
func TestSetTodos_SameGoalReplacesChecklist(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithWorkspaceID(ctx, "ws-1")

	// First call: 3 todos.
	r1 := tool.Execute(ctx, map[string]any{
		"goal": "refactor",
		"todos": []any{
			map[string]any{"text": "step A", "status": "pending"},
			map[string]any{"text": "step B", "status": "pending"},
			map[string]any{"text": "step C", "status": "pending"},
		},
	})
	if r1.IsError {
		t.Fatalf("first set_todos: %s", r1.ForLLM)
	}

	// Second call: 2 todos (replace).
	r2 := tool.Execute(ctx, map[string]any{
		"goal": "refactor",
		"todos": []any{
			map[string]any{"text": "step A", "status": "completed"},
			map[string]any{"text": "step B", "status": "in_progress"},
		},
	})
	if r2.IsError {
		t.Fatalf("second set_todos: %s", r2.ForLLM)
	}

	// Exactly one task must exist (no duplicate).
	tasks, err := store.List(task.Filter{AgentID: "agent-a"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task after two calls with the same goal, got %d", len(tasks))
	}
	// Two todos remain (replace-semantics, not append).
	if len(tasks[0].Todos) != 2 {
		t.Errorf("expected 2 todos after replace, got %d", len(tasks[0].Todos))
	}
}

// TestSetTodos_DifferentGoalCreatesSeparateTask proves that a different goal
// title creates a second board task rather than reusing the first.
func TestSetTodos_DifferentGoalCreatesSeparateTask(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithWorkspaceID(ctx, "ws-1")

	r1 := tool.Execute(ctx, map[string]any{
		"goal":  "goal alpha",
		"todos": []any{map[string]any{"text": "do alpha", "status": "pending"}},
	})
	if r1.IsError {
		t.Fatalf("first set_todos: %s", r1.ForLLM)
	}

	r2 := tool.Execute(ctx, map[string]any{
		"goal":  "goal beta",
		"todos": []any{map[string]any{"text": "do beta", "status": "pending"}},
	})
	if r2.IsError {
		t.Fatalf("second set_todos: %s", r2.ForLLM)
	}

	tasks, err := store.List(task.Filter{AgentID: "agent-a"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks for 2 different goals, got %d", len(tasks))
	}
	titles := map[string]bool{}
	for _, tk := range tasks {
		titles[tk.Title] = true
	}
	if !titles["goal alpha"] || !titles["goal beta"] {
		t.Errorf("unexpected task titles: %v", titles)
	}
}

// TestSetTodos_InvalidStatusReturnsError proves that an invalid status string
// returns an ErrorResult and does not create any task.
func TestSetTodos_InvalidStatusReturnsError(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithWorkspaceID(ctx, "ws-1")

	result := tool.Execute(ctx, map[string]any{
		"goal": "bad status goal",
		"todos": []any{
			map[string]any{"text": "something", "status": "invalid-value"},
		},
	})
	if !result.IsError {
		t.Fatal("expected error for invalid todo status")
	}
	if !strings.Contains(result.ForLLM, "invalid status") {
		t.Errorf("expected 'invalid status' in error, got: %s", result.ForLLM)
	}

	// No task must have been persisted.
	tasks, _ := store.List(task.Filter{AgentID: "agent-a"})
	if len(tasks) != 0 {
		t.Errorf("expected no task created after validation error, got %d", len(tasks))
	}
}

// TestSetTodos_EmptyTextReturnsError proves that a todo with empty text is rejected.
func TestSetTodos_EmptyTextReturnsError(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithWorkspaceID(ctx, "ws-1")

	result := tool.Execute(ctx, map[string]any{
		"goal": "empty text goal",
		"todos": []any{
			map[string]any{"text": "", "status": "pending"},
		},
	})
	if !result.IsError {
		t.Fatal("expected error for empty todo text")
	}
	if !strings.Contains(result.ForLLM, "text must not be empty") {
		t.Errorf("expected 'text must not be empty' in error, got: %s", result.ForLLM)
	}
}

// TestSetTodos_ReadOnWrite proves the result body contains the full checklist
// (goal + each todo's text + status), not just a bare success ack.
func TestSetTodos_ReadOnWrite(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithWorkspaceID(ctx, "ws-1")

	result := tool.Execute(ctx, map[string]any{
		"goal": "build the thing",
		"todos": []any{
			map[string]any{"text": "scaffold", "status": "completed"},
			map[string]any{"text": "wire API", "status": "in_progress"},
		},
	})
	if result.IsError {
		t.Fatalf("set_todos: %s", result.ForLLM)
	}

	if !strings.Contains(result.ForLLM, "build the thing") {
		t.Errorf("result must contain goal name, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "scaffold") {
		t.Errorf("result must contain 'scaffold' todo text, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "wire API") {
		t.Errorf("result must contain 'wire API' todo text, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "completed") {
		t.Errorf("result must contain 'completed' status, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "in_progress") {
		t.Errorf("result must contain 'in_progress' status, got: %s", result.ForLLM)
	}
	// task_id must NOT appear (facade: agent must not see the underlying ID).
	if strings.Contains(result.ForLLM, "task_id") {
		t.Errorf("result must not expose task_id, got: %s", result.ForLLM)
	}
}

// TestSetTodos_EmptyTodosClearsChecklist proves that passing an empty todos array
// clears the list and returns a "(checklist cleared)" confirmation.
func TestSetTodos_EmptyTodosClearsChecklist(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithWorkspaceID(ctx, "ws-1")

	// Seed a goal with one todo.
	r1 := tool.Execute(ctx, map[string]any{
		"goal":  "transient goal",
		"todos": []any{map[string]any{"text": "do something", "status": "pending"}},
	})
	if r1.IsError {
		t.Fatalf("seed: %s", r1.ForLLM)
	}

	// Clear the checklist.
	r2 := tool.Execute(ctx, map[string]any{
		"goal":  "transient goal",
		"todos": []any{},
	})
	if r2.IsError {
		t.Fatalf("clear: %s", r2.ForLLM)
	}
	if !strings.Contains(r2.ForLLM, "checklist cleared") {
		t.Errorf("expected 'checklist cleared' in response, got: %s", r2.ForLLM)
	}

	tasks, _ := store.List(task.Filter{AgentID: "agent-a"})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task (goal still exists), got %d", len(tasks))
	}
	if len(tasks[0].Todos) != 0 {
		t.Errorf("expected 0 todos after clear, got %d", len(tasks[0].Todos))
	}
}

// TestSetTodos_NoAgentID proves that a missing acting agent ID returns an error.
func TestSetTodos_NoAgentID(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	result := tool.Execute(context.Background(), map[string]any{
		"goal":  "orphan goal",
		"todos": []any{map[string]any{"text": "something", "status": "pending"}},
	})
	if !result.IsError {
		t.Fatal("expected error when no agent ID in context")
	}
	if !strings.Contains(result.ForLLM, "could not resolve acting agent") {
		t.Errorf("unexpected error: %s", result.ForLLM)
	}
}

// TestSetTodos_DefaultStatusPending proves that a todo item without an explicit
// status field defaults to "pending".
func TestSetTodos_DefaultStatusPending(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "agent-a")
	ctx = WithWorkspaceID(ctx, "ws-1")

	result := tool.Execute(ctx, map[string]any{
		"goal": "implicit pending goal",
		"todos": []any{
			map[string]any{"text": "no status field"},
		},
	})
	if result.IsError {
		t.Fatalf("set_todos with missing status: %s", result.ForLLM)
	}

	tasks, _ := store.List(task.Filter{AgentID: "agent-a"})
	if len(tasks) != 1 || len(tasks[0].Todos) != 1 {
		t.Fatalf("expected 1 task with 1 todo, got %+v", tasks)
	}
	if tasks[0].Todos[0].Status != task.TodoPending {
		t.Errorf("expected default status 'pending', got %q", tasks[0].Todos[0].Status)
	}
}
