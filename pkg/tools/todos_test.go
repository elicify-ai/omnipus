package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// UAT batch2 S43 (docs/internal/qa/uat-report-full-tool-catalog-batch2-2026-09-02.md)
// / UAT batch3 finding #4: set_todos is documented as core-agents-only, but
// that restriction was never enforced in code, only by convention. The fix
// (see Execute's IsCoreAgent/IsSubagentTierID gate in todos.go) refuses the
// call for any agent ID that is neither the core roster nor the seeded
// subagent tier. Every pre-existing test in this file used the fictitious
// id "agent-a" as its acting agent — under the fix that id is correctly
// refused, so every test below was updated to use "mia" (a real core-roster
// id) instead, preserving what each test actually exercises (goal/todo
// mechanics) rather than accidentally re-testing the new restriction by
// omission. TestSetTodos_CoreAgentsOnly (below) is the restriction's own
// dedicated test.

// TestSetTodos_NewGoalCreatesTask proves that calling set_todos with a goal that
// does not yet exist creates a board-visible task with that goal title, agentID,
// and the supplied todos.
func TestSetTodos_NewGoalCreatesTask(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "mia")
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
	tasks, err := store.List(task.Filter{AgentID: "mia"})
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
	if tk.AgentID != "mia" {
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

	ctx := WithAgentID(context.Background(), "mia")
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
	tasks, err := store.List(task.Filter{AgentID: "mia"})
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

	ctx := WithAgentID(context.Background(), "mia")
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

	tasks, err := store.List(task.Filter{AgentID: "mia"})
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

	ctx := WithAgentID(context.Background(), "mia")
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
	tasks, _ := store.List(task.Filter{AgentID: "mia"})
	if len(tasks) != 0 {
		t.Errorf("expected no task created after validation error, got %d", len(tasks))
	}
}

// TestSetTodos_EmptyTextReturnsError proves that a todo with empty text is rejected.
func TestSetTodos_EmptyTextReturnsError(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "mia")
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

	ctx := WithAgentID(context.Background(), "mia")
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

	ctx := WithAgentID(context.Background(), "mia")
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

	tasks, _ := store.List(task.Filter{AgentID: "mia"})
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

	ctx := WithAgentID(context.Background(), "mia")
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

	tasks, _ := store.List(task.Filter{AgentID: "mia"})
	if len(tasks) != 1 || len(tasks[0].Todos) != 1 {
		t.Fatalf("expected 1 task with 1 todo, got %+v", tasks)
	}
	if tasks[0].Todos[0].Status != task.TodoPending {
		t.Errorf("expected default status 'pending', got %q", tasks[0].Todos[0].Status)
	}
}

// TestSetTodos_DoesNotHijackRealTask proves that set_todos NEVER overwrites a
// real create_task card (Scratchpad==false) even when its title matches the goal.
// Fix 2: findActiveGoalTask must filter to Scratchpad==true only.
func TestSetTodos_DoesNotHijackRealTask(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "mia")
	ctx = WithWorkspaceID(ctx, "ws-1")

	// Simulate a real create_task card: Scratchpad=false (default), same title.
	realTask := &task.Task{
		Title:       "implement feature X",
		Action:      task.ActionLLM,
		AgentID:     "mia",
		CreatedBy:   "mia",
		WorkspaceID: "ws-1",
		Status:      task.StatusInProgress,
		Priority:    3,
		// Scratchpad is false by default — this is the discriminator.
	}
	if err := store.Create(realTask); err != nil {
		t.Fatalf("pre-seed real task: %v", err)
	}

	// Now call set_todos with the same goal title.
	result := tool.Execute(ctx, map[string]any{
		"goal": "implement feature X",
		"todos": []any{
			map[string]any{"text": "scratchpad step", "status": "pending"},
		},
	})
	if result.IsError {
		t.Fatalf("set_todos failed: %s", result.ForLLM)
	}

	// Must NOT have modified the real task (it had no todos; it still must have none).
	got, err := store.Get(realTask.ID)
	if err != nil {
		t.Fatalf("get real task: %v", err)
	}
	if len(got.Todos) != 0 {
		t.Errorf("real task todos must be untouched, got %d todos", len(got.Todos))
	}
	if got.Scratchpad {
		t.Errorf("real task Scratchpad flag must remain false")
	}

	// A SEPARATE scratchpad card must have been created.
	tasks, err := store.List(task.Filter{AgentID: "mia"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks (real + scratchpad), got %d", len(tasks))
	}
	var scratchpadCount int
	for _, tk := range tasks {
		if tk.Scratchpad {
			scratchpadCount++
			if len(tk.Todos) != 1 {
				t.Errorf("scratchpad card must have 1 todo, got %d", len(tk.Todos))
			}
		}
	}
	if scratchpadCount != 1 {
		t.Errorf("expected exactly 1 scratchpad card, got %d", scratchpadCount)
	}
}

// TestSetTodos_NewGoalArchivesPriorScratchpad proves that switching to a new goal
// archives (Status=done) the previous scratchpad card, so only one active scratchpad
// exists per agent at a time. Fix 3: archive-previous lifecycle.
func TestSetTodos_NewGoalArchivesPriorScratchpad(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "mia")
	ctx = WithWorkspaceID(ctx, "ws-1")

	// Create first scratchpad card.
	r1 := tool.Execute(ctx, map[string]any{
		"goal":  "goal alpha",
		"todos": []any{map[string]any{"text": "step A", "status": "pending"}},
	})
	if r1.IsError {
		t.Fatalf("first set_todos: %s", r1.ForLLM)
	}

	// Capture the ID of the first card.
	tasks1, _ := store.List(task.Filter{AgentID: "mia"})
	if len(tasks1) != 1 {
		t.Fatalf("expected 1 task after first goal, got %d", len(tasks1))
	}
	firstID := tasks1[0].ID

	// Switch to a new goal — this must archive the first scratchpad card.
	r2 := tool.Execute(ctx, map[string]any{
		"goal":  "goal beta",
		"todos": []any{map[string]any{"text": "step B", "status": "pending"}},
	})
	if r2.IsError {
		t.Fatalf("second set_todos: %s", r2.ForLLM)
	}

	// The prior card must now be Status=done (archived).
	prior, err := store.Get(firstID)
	if err != nil {
		t.Fatalf("get prior card: %v", err)
	}
	if prior.Status != task.StatusDone {
		t.Errorf("prior scratchpad card must be archived (done), got %q", prior.Status)
	}

	// Only one ACTIVE scratchpad card must remain.
	allTasks, _ := store.List(task.Filter{AgentID: "mia"})
	var activeScratchpad int
	for _, tk := range allTasks {
		if tk.Scratchpad && !task.IsTerminal(tk.Status) {
			activeScratchpad++
		}
	}
	if activeScratchpad != 1 {
		t.Errorf("expected exactly 1 active scratchpad card after goal switch, got %d", activeScratchpad)
	}
}

// TestSetTodos_AtomicCreate proves that set_todos creates a card with Todos set
// inline (no orphan window) and that the card's Scratchpad flag is true.
// Fix 2: Scratchpad==true + atomic create (todos in the &task.Task{} literal).
func TestSetTodos_AtomicCreate(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "mia")
	ctx = WithWorkspaceID(ctx, "ws-1")

	result := tool.Execute(ctx, map[string]any{
		"goal": "atomic create goal",
		"todos": []any{
			map[string]any{"text": "step one", "status": "pending"},
			map[string]any{"text": "step two", "status": "in_progress"},
		},
	})
	if result.IsError {
		t.Fatalf("set_todos failed: %s", result.ForLLM)
	}

	tasks, err := store.List(task.Filter{AgentID: "mia"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 scratchpad task, got %d", len(tasks))
	}
	tk := tasks[0]

	// Discriminator flag must be set.
	if !tk.Scratchpad {
		t.Errorf("scratchpad card must have Scratchpad=true")
	}
	// Todos must be present from the initial create (no orphan window).
	if len(tk.Todos) != 2 {
		t.Fatalf("expected 2 todos on newly created card, got %d (atomic create must set todos inline)", len(tk.Todos))
	}
	if tk.Todos[0].Text != "step one" || tk.Todos[0].Status != task.TodoPending {
		t.Errorf("todo[0] wrong: %+v", tk.Todos[0])
	}
	if tk.Todos[1].Text != "step two" || tk.Todos[1].Status != task.TodoInProgress {
		t.Errorf("todo[1] wrong: %+v", tk.Todos[1])
	}
}

// TestSetTodos_ArchiveDoesNotTouchRealTasks proves that the archive-previous pass
// ignores real create_task cards (Scratchpad==false), even when they are active.
// Fix 3: archiveOtherScratchpadCards must never touch Scratchpad==false tasks.
func TestSetTodos_ArchiveDoesNotTouchRealTasks(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "mia")
	ctx = WithWorkspaceID(ctx, "ws-1")

	// Seed a real task (no Scratchpad flag).
	realTask := &task.Task{
		Title:       "real task alpha",
		Action:      task.ActionLLM,
		AgentID:     "mia",
		CreatedBy:   "mia",
		WorkspaceID: "ws-1",
		Status:      task.StatusInProgress,
		Priority:    3,
	}
	if err := store.Create(realTask); err != nil {
		t.Fatalf("pre-seed real task: %v", err)
	}

	// Create first scratchpad goal.
	r1 := tool.Execute(ctx, map[string]any{
		"goal":  "scratchpad goal 1",
		"todos": []any{map[string]any{"text": "x", "status": "pending"}},
	})
	if r1.IsError {
		t.Fatalf("first scratchpad: %s", r1.ForLLM)
	}

	// Switch to a new scratchpad goal — archive-previous must fire.
	r2 := tool.Execute(ctx, map[string]any{
		"goal":  "scratchpad goal 2",
		"todos": []any{map[string]any{"text": "y", "status": "pending"}},
	})
	if r2.IsError {
		t.Fatalf("second scratchpad: %s", r2.ForLLM)
	}

	// The real task must still be in_progress (untouched by archive-previous).
	got, err := store.Get(realTask.ID)
	if err != nil {
		t.Fatalf("get real task: %v", err)
	}
	if got.Status != task.StatusInProgress {
		t.Errorf("real task status must remain in_progress, got %q", got.Status)
	}
}

// TestSetTodos_CoreAgentsOnly is the dedicated regression test for UAT
// batch2 S43 / batch3 finding #4: set_todos must refuse a non-core,
// non-locked (i.e. genuinely custom/disposable) calling agent, and must
// still work for both the core roster and the seeded subagent tier.
func TestSetTodos_CoreAgentsOnly(t *testing.T) {
	t.Parallel()

	t.Run("disposable custom agent is refused", func(t *testing.T) {
		t.Parallel()
		store := task.New(t.TempDir())
		tool := NewSetTodosTool(store)

		ctx := WithAgentID(context.Background(), "uat-s43-disposable-tester")
		ctx = WithWorkspaceID(ctx, "ws-1")

		result := tool.Execute(ctx, map[string]any{
			"goal":  "should never be created",
			"todos": []any{map[string]any{"text": "x", "status": "pending"}},
		})
		if !result.IsError {
			t.Fatal("expected set_todos to be refused for a non-core, non-locked agent")
		}
		if !strings.Contains(result.ForLLM, "uat-s43-disposable-tester") {
			t.Errorf("error should name the refused agent id, got: %s", result.ForLLM)
		}
		if !strings.Contains(strings.ToLower(result.ForLLM), "core") {
			t.Errorf("error should explain the core-agents-only restriction, got: %s", result.ForLLM)
		}

		// No task must have been created — the refusal is a hard stop, not a
		// silent partial write.
		tasks, err := store.List(task.Filter{AgentID: "uat-s43-disposable-tester"})
		if err != nil {
			t.Fatalf("list tasks: %v", err)
		}
		if len(tasks) != 0 {
			t.Errorf("expected no task created for a refused agent, got %d", len(tasks))
		}
	})

	t.Run("core-roster agent is allowed", func(t *testing.T) {
		t.Parallel()
		store := task.New(t.TempDir())
		tool := NewSetTodosTool(store)

		ctx := WithAgentID(context.Background(), "ava")
		ctx = WithWorkspaceID(ctx, "ws-1")

		result := tool.Execute(ctx, map[string]any{
			"goal":  "core roster goal",
			"todos": []any{map[string]any{"text": "x", "status": "pending"}},
		})
		if result.IsError {
			t.Fatalf("expected set_todos to succeed for core-roster agent 'ava': %s", result.ForLLM)
		}
	})

	t.Run("seeded subagent-tier agent is allowed", func(t *testing.T) {
		t.Parallel()
		store := task.New(t.TempDir())
		tool := NewSetTodosTool(store)

		ctx := WithAgentID(context.Background(), "worker")
		ctx = WithWorkspaceID(ctx, "ws-1")

		result := tool.Execute(ctx, map[string]any{
			"goal":  "subagent tier goal",
			"todos": []any{map[string]any{"text": "x", "status": "pending"}},
		})
		if result.IsError {
			t.Fatalf("expected set_todos to succeed for seeded subagent-tier agent 'worker': %s", result.ForLLM)
		}
	})
}
