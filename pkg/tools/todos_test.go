package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

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
		"outcome": "implement feature X",
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
		"outcome": "refactor",
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
		"outcome": "refactor",
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
		"outcome": "goal alpha",
		"todos":   []any{map[string]any{"text": "do alpha", "status": "pending"}},
	})
	if r1.IsError {
		t.Fatalf("first set_todos: %s", r1.ForLLM)
	}

	r2 := tool.Execute(ctx, map[string]any{
		"outcome": "goal beta",
		"todos":   []any{map[string]any{"text": "do beta", "status": "pending"}},
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
		"outcome": "bad status goal",
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
		"outcome": "empty text goal",
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
		"outcome": "build the thing",
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
		"outcome": "transient goal",
		"todos":   []any{map[string]any{"text": "do something", "status": "pending"}},
	})
	if r1.IsError {
		t.Fatalf("seed: %s", r1.ForLLM)
	}

	// Clear the checklist.
	r2 := tool.Execute(ctx, map[string]any{
		"outcome": "transient goal",
		"todos":   []any{},
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

// TestSetTodos_OldGoalArgNoLongerAccepted proves the goal -> outcome rename is
// breaking: a caller still sending the old `goal` argument (with no `outcome`)
// gets a clear error naming the new field, never a silent fallback.
func TestSetTodos_OldGoalArgNoLongerAccepted(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctx := WithAgentID(context.Background(), "mia")
	ctx = WithWorkspaceID(ctx, "ws-1")

	result := tool.Execute(ctx, map[string]any{
		"goal":  "implement feature X",
		"todos": []any{map[string]any{"text": "step", "status": "pending"}},
	})
	if !result.IsError {
		t.Fatal("expected error when the old `goal` argument is sent instead of `outcome`")
	}
	if !strings.Contains(result.ForLLM, "outcome") {
		t.Errorf("error must name the new `outcome` field, got: %s", result.ForLLM)
	}
}

// TestSetTodos_NoAgentID proves that a missing acting agent ID returns an error.
func TestSetTodos_NoAgentID(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	result := tool.Execute(context.Background(), map[string]any{
		"outcome": "orphan goal",
		"todos":   []any{map[string]any{"text": "something", "status": "pending"}},
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
		"outcome": "implicit pending goal",
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
		"outcome": "implement feature X",
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
		"outcome": "goal alpha",
		"todos":   []any{map[string]any{"text": "step A", "status": "pending"}},
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
		"outcome": "goal beta",
		"todos":   []any{map[string]any{"text": "step B", "status": "pending"}},
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
		"outcome": "atomic create goal",
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
		"outcome": "scratchpad goal 1",
		"todos":   []any{map[string]any{"text": "x", "status": "pending"}},
	})
	if r1.IsError {
		t.Fatalf("first scratchpad: %s", r1.ForLLM)
	}

	// Switch to a new scratchpad goal — archive-previous must fire.
	r2 := tool.Execute(ctx, map[string]any{
		"outcome": "scratchpad goal 2",
		"todos":   []any{map[string]any{"text": "y", "status": "pending"}},
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

// TestSetTodos_AnyAgentCanUseItsOwnScratchpad guards against reintroducing a
// core-agents-only restriction on set_todos. The tool is every agent's
// personal scratchpad (ScopeCore governs REACHABILITY the same way it does
// for bash/edit_file/write_file — available to core agents by default, and
// to a custom agent once its policy explicitly grants the tool) — it is not
// a project-management surface gated to the core roster. A prior fix
// mistakenly added a hard-coded refusal for any agent outside the core
// roster / seeded subagent tier / system-agent set, which broke the tool for
// every ordinary custom or disposable agent it was explicitly granted to.
func TestSetTodos_AnyAgentCanUseItsOwnScratchpad(t *testing.T) {
	t.Parallel()

	for _, agentID := range []string{"ava", "worker", "a-genuinely-custom-agent"} {
		t.Run(agentID, func(t *testing.T) {
			t.Parallel()
			store := task.New(t.TempDir())
			tool := NewSetTodosTool(store)

			ctx := WithAgentID(context.Background(), agentID)
			ctx = WithWorkspaceID(ctx, "ws-1")

			result := tool.Execute(ctx, map[string]any{
				"outcome": "scratchpad goal for " + agentID,
				"todos":   []any{map[string]any{"text": "x", "status": "pending"}},
			})
			if result.IsError {
				t.Fatalf("expected set_todos to succeed for agent %q, got error: %s", agentID, result.ForLLM)
			}

			tasks, err := store.List(task.Filter{AgentID: agentID})
			if err != nil {
				t.Fatalf("list tasks: %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("expected exactly one scratchpad task for %q, got %d", agentID, len(tasks))
			}
		})
	}
}

// ---- session scoping (UAT B-1 runs 3 and 5) --------------------------------
//
// The checklist is scoped to the calling turn's transcript session: two
// concurrent sessions of the SAME agent each get their own card (even for the
// identical goal title), never overwrite each other's checklist, and never
// archive each other's cards.

func TestSetTodos_SessionScoped_SecondSessionGetsOwnCardForSameGoal(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	ctxA := WithAgentID(context.Background(), "mia")
	ctxA = WithWorkspaceID(ctxA, "ws-1")
	ctxA = WithTranscriptSessionID(ctxA, "session-a")
	ctxB := WithAgentID(context.Background(), "mia")
	ctxB = WithWorkspaceID(ctxB, "ws-1")
	ctxB = WithTranscriptSessionID(ctxB, "session-b")

	if r := tool.Execute(ctxA, map[string]any{
		"outcome": "shared goal title",
		"todos":   []any{map[string]any{"text": "A step", "status": "pending"}},
	}); r.IsError {
		t.Fatalf("session A set_todos: %s", r.ForLLM)
	}
	// Same goal title from session B: must NOT find (and overwrite) A's card —
	// it creates its own.
	if r := tool.Execute(ctxB, map[string]any{
		"outcome": "shared goal title",
		"todos":   []any{map[string]any{"text": "B step", "status": "pending"}},
	}); r.IsError {
		t.Fatalf("session B set_todos: %s", r.ForLLM)
	}

	tasks, err := store.List(task.Filter{AgentID: "mia"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("two sessions with the same goal title must own TWO cards, got %d", len(tasks))
	}
	bySession := map[string]task.Task{}
	for _, tk := range tasks {
		bySession[tk.OriginSessionID] = tk
	}
	if len(bySession) != 2 || bySession["session-a"].ID == bySession["session-b"].ID {
		t.Fatalf("each session must have its own card, got %+v", bySession)
	}
	if got := bySession["session-a"].Todos[0].Text; got != "A step" {
		t.Errorf("session A's checklist must be untouched by session B, first todo %q", got)
	}
	if got := bySession["session-b"].Todos[0].Text; got != "B step" {
		t.Errorf("session B's checklist must be its own, first todo %q", got)
	}

	// A same-goal update from session A still replaces A's own card (exactly
	// two cards remain, A's list replaced).
	if r := tool.Execute(ctxA, map[string]any{
		"outcome": "shared goal title",
		"todos":   []any{map[string]any{"text": "A step 2", "status": "in_progress"}},
	}); r.IsError {
		t.Fatalf("session A update: %s", r.ForLLM)
	}
	tasks, _ = store.List(task.Filter{AgentID: "mia"})
	if len(tasks) != 2 {
		t.Fatalf("a same-session update must not create a third card, got %d", len(tasks))
	}
	for _, tk := range tasks {
		if tk.OriginSessionID == "session-a" && (len(tk.Todos) != 1 || tk.Todos[0].Text != "A step 2") {
			t.Errorf("session A's replace-semantics broken: %+v", tk.Todos)
		}
	}
}

func TestSetTodos_SessionScoped_GoalSwitchDoesNotArchiveOtherSessionsCards(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewSetTodosTool(store)

	mkCtx := func(sessionID string) context.Context {
		ctx := WithAgentID(context.Background(), "mia")
		ctx = WithWorkspaceID(ctx, "ws-1")
		return WithTranscriptSessionID(ctx, sessionID)
	}

	// Session A opens a checklist, then switches to a new goal.
	if r := tool.Execute(mkCtx("session-a"), map[string]any{
		"outcome": "A goal one",
		"todos":   []any{map[string]any{"text": "a1", "status": "pending"}},
	}); r.IsError {
		t.Fatalf("A first: %s", r.ForLLM)
	}
	// Session B opens its own (unrelated) checklist while A is still on goal one.
	if r := tool.Execute(mkCtx("session-b"), map[string]any{
		"outcome": "B goal",
		"todos":   []any{map[string]any{"text": "b1", "status": "pending"}},
	}); r.IsError {
		t.Fatalf("B first: %s", r.ForLLM)
	}
	// A switches goals — the archive pass must close only A's old card.
	if r := tool.Execute(mkCtx("session-a"), map[string]any{
		"outcome": "A goal two",
		"todos":   []any{map[string]any{"text": "a2", "status": "pending"}},
	}); r.IsError {
		t.Fatalf("A switch: %s", r.ForLLM)
	}

	tasks, _ := store.List(task.Filter{AgentID: "mia"})
	if len(tasks) != 3 {
		t.Fatalf("expected 3 cards (A old archived, A new, B open), got %d", len(tasks))
	}
	for _, tk := range tasks {
		if tk.OriginSessionID == "session-b" && task.IsTerminal(tk.Status) {
			t.Errorf("REGRESSION (UAT B-1): session A's goal switch archived session B's card (%q is %q)", tk.Title, tk.Status)
		}
		if tk.OriginSessionID == "session-a" && tk.Title == "A goal one" && !task.IsTerminal(tk.Status) {
			t.Errorf("A's own prior card must still be archived, got %q", tk.Status)
		}
	}
}
