package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/task"
)

// seedTask creates a task in the store and returns it. Fails the test on error.
func seedTask(t *testing.T, store *task.Store, agentID, createdBy, wsID string) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title:       "test task",
		Prompt:      "do something",
		Action:      task.ActionLLM,
		AgentID:     agentID,
		CreatedBy:   createdBy,
		WorkspaceID: wsID,
		Status:      task.StatusNext,
	}
	if err := store.Create(tk); err != nil {
		t.Fatalf("seedTask: create: %v", err)
	}
	return tk
}

// seedWorkspaceDefault writes a minimal workspace JSON with is_default:true under
// <home>/workspaces/<id>.json so workspace.ResolveDefaultID finds it.
func seedWorkspaceDefault(t *testing.T, home, id string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("seedWorkspaceDefault: mkdir: %v", err)
	}
	data, err := json.Marshal(map[string]any{"id": id, "is_default": true, "name": "My Workspace"})
	if err != nil {
		t.Fatalf("seedWorkspaceDefault: marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600); err != nil {
		t.Fatalf("seedWorkspaceDefault: write: %v", err)
	}
}

// TestTaskAddTodo_NoDeadlock verifies that task_add_todo completes without
// hanging (the old Lock()+Update() deadlock) and actually appends the todo.
func TestTaskAddTodo_NoDeadlock(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskAddTodoTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	result := tool.Execute(ctx, map[string]any{
		"task_id": tk.ID,
		"text":    "write tests",
	})

	if result.IsError {
		t.Fatalf("task_add_todo returned error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, `"todos":1`) {
		t.Errorf("expected todos count 1 in response, got: %s", result.ForLLM)
	}

	// Confirm the todo actually persisted.
	updated, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task after add_todo: %v", err)
	}
	if len(updated.Todos) != 1 {
		t.Errorf("expected 1 todo persisted, got %d", len(updated.Todos))
	}
	if updated.Todos[0].Text != "write tests" {
		t.Errorf("expected todo text 'write tests', got %q", updated.Todos[0].Text)
	}
}

// TestTaskAddTodo_SecondItem verifies that a second todo is appended, not replacing.
func TestTaskAddTodo_SecondItem(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskAddTodoTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	tool.Execute(ctx, map[string]any{"task_id": tk.ID, "text": "first"})
	result := tool.Execute(ctx, map[string]any{"task_id": tk.ID, "text": "second"})

	if result.IsError {
		t.Fatalf("second add_todo: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, `"todos":2`) {
		t.Errorf("expected todos:2, got: %s", result.ForLLM)
	}
}

// TestTaskAddTodo_OwnershipRejection proves that a caller who is neither AgentID
// nor CreatedBy is rejected.
func TestTaskAddTodo_OwnershipRejection(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskAddTodoTool(store)

	// Unrelated caller — must be rejected.
	ctx := WithAgentID(context.Background(), "intruder")
	result := tool.Execute(ctx, map[string]any{
		"task_id": tk.ID,
		"text":    "malicious todo",
	})
	if !result.IsError {
		t.Fatal("expected ownership rejection but got success")
	}
	if !strings.Contains(result.ForLLM, "you can only modify") {
		t.Errorf("unexpected rejection message: %s", result.ForLLM)
	}

	// Confirm no todo was written.
	stored, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(stored.Todos) != 0 {
		t.Errorf("expected 0 todos after rejected add, got %d", len(stored.Todos))
	}
}

// TestTaskAddTodo_OwnerAllowed proves AgentID is allowed to add a todo.
func TestTaskAddTodo_OwnerAllowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskAddTodoTool(store)
	ctx := WithAgentID(context.Background(), "agent-owner")
	result := tool.Execute(ctx, map[string]any{"task_id": tk.ID, "text": "valid"})
	if result.IsError {
		t.Fatalf("owner must be allowed: %s", result.ForLLM)
	}
}

// TestTaskAddTodo_CreatorAllowed proves CreatedBy is allowed to add a todo even
// when they are not the assigned agent.
func TestTaskAddTodo_CreatorAllowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskAddTodoTool(store)
	ctx := WithAgentID(context.Background(), "creator-a")
	result := tool.Execute(ctx, map[string]any{"task_id": tk.ID, "text": "valid"})
	if result.IsError {
		t.Fatalf("creator must be allowed: %s", result.ForLLM)
	}
}

// TestTaskAddTodo_MissingAgentID proves that a missing caller ID returns an error.
func TestTaskAddTodo_MissingAgentID(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskAddTodoTool(store)
	// No WithAgentID — callerID will be "".
	result := tool.Execute(context.Background(), map[string]any{
		"task_id": tk.ID,
		"text":    "something",
	})
	if !result.IsError {
		t.Fatal("expected error when no agent ID in context")
	}
	if !strings.Contains(result.ForLLM, "agent ID not set") {
		t.Errorf("unexpected message: %s", result.ForLLM)
	}
}

// TestTaskAddDependency_OwnershipRejection proves a non-owner cannot add a dependency.
func TestTaskAddDependency_OwnershipRejection(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	dep := seedTask(t, store, "agent-owner", "creator-a", "ws-1")
	blocker := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskAddDependencyTool(store)
	ctx := WithAgentID(context.Background(), "intruder")
	result := tool.Execute(ctx, map[string]any{
		"task_id":    dep.ID,
		"blocked_by": blocker.ID,
	})
	if !result.IsError {
		t.Fatal("expected ownership rejection")
	}
	if !strings.Contains(result.ForLLM, "you can only modify") {
		t.Errorf("unexpected rejection: %s", result.ForLLM)
	}
}

// TestTaskAddDependency_OwnerAllowed proves AgentID can add a dependency.
func TestTaskAddDependency_OwnerAllowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	dep := seedTask(t, store, "agent-owner", "creator-a", "ws-1")
	blocker := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskAddDependencyTool(store)
	ctx := WithAgentID(context.Background(), "agent-owner")
	result := tool.Execute(ctx, map[string]any{
		"task_id":    dep.ID,
		"blocked_by": blocker.ID,
	})
	if result.IsError {
		t.Fatalf("owner must be allowed: %s", result.ForLLM)
	}
}

// TestTaskAddDependency_CreatorAllowed proves CreatedBy can add a dependency even
// when they are not the assigned agent.
func TestTaskAddDependency_CreatorAllowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	dep := seedTask(t, store, "agent-owner", "creator-a", "ws-1")
	blocker := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskAddDependencyTool(store)
	ctx := WithAgentID(context.Background(), "creator-a")
	result := tool.Execute(ctx, map[string]any{
		"task_id":    dep.ID,
		"blocked_by": blocker.ID,
	})
	if result.IsError {
		t.Fatalf("creator must be allowed: %s", result.ForLLM)
	}
}

// TestTaskAddDependency_CrossWorkspace proves a cross-workspace blocker is rejected.
func TestTaskAddDependency_CrossWorkspace(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	dep := seedTask(t, store, "agent-a", "agent-a", "ws-alpha")
	// blocker lives in a different workspace
	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-beta")

	tool := NewTaskAddDependencyTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")
	result := tool.Execute(ctx, map[string]any{
		"task_id":    dep.ID,
		"blocked_by": blocker.ID,
	})
	if !result.IsError {
		t.Fatal("expected cross-workspace rejection")
	}
	if !strings.Contains(result.ForLLM, "different workspace") {
		t.Errorf("unexpected message: %s", result.ForLLM)
	}
}

// TestTaskAddDependency_Idempotent proves adding the same edge twice returns
// already:true on the second call without an error.
func TestTaskAddDependency_Idempotent(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	dep := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskAddDependencyTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	r1 := tool.Execute(ctx, map[string]any{"task_id": dep.ID, "blocked_by": blocker.ID})
	if r1.IsError {
		t.Fatalf("first add: %s", r1.ForLLM)
	}
	r2 := tool.Execute(ctx, map[string]any{"task_id": dep.ID, "blocked_by": blocker.ID})
	if r2.IsError {
		t.Fatalf("second add: %s", r2.ForLLM)
	}
	if !strings.Contains(r2.ForLLM, `"already":true`) {
		t.Errorf("expected already:true on second add, got: %s", r2.ForLLM)
	}
}

// TestTaskDelete_OwnershipRejection proves a non-owner cannot delete a task.
func TestTaskDelete_OwnershipRejection(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "intruder")
	result := tool.Execute(ctx, map[string]any{"task_id": tk.ID})
	if !result.IsError {
		t.Fatal("expected ownership rejection on delete")
	}
	if !strings.Contains(result.ForLLM, "you can only modify/delete") {
		t.Errorf("unexpected rejection message: %s", result.ForLLM)
	}

	// Task must still exist.
	if _, err := store.Get(tk.ID); err != nil {
		t.Errorf("task should still exist after rejected delete, got: %v", err)
	}
}

// TestTaskDelete_OwnerAllowed proves AgentID (owner) can delete.
func TestTaskDelete_OwnerAllowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "agent-owner")
	result := tool.Execute(ctx, map[string]any{"task_id": tk.ID})
	if result.IsError {
		t.Fatalf("owner delete failed: %s", result.ForLLM)
	}
}

// TestTaskDelete_CreatorAllowed proves CreatedBy can also delete.
func TestTaskDelete_CreatorAllowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "creator-a")
	result := tool.Execute(ctx, map[string]any{"task_id": tk.ID})
	if result.IsError {
		t.Fatalf("creator delete failed: %s", result.ForLLM)
	}
}

// TestTaskDelete_AdvancesBlockedDependent proves that deleting a blocker task
// advances a still-blocked dependent to `next`.
func TestTaskDelete_AdvancesBlockedDependent(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	blocker := seedTask(t, store, "agent-a", "agent-a", "ws-1")
	dependent := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	// Wire the dependency via the store directly (bypasses the tool ownership
	// check so we can set up the state without needing matching callerID for
	// the setup step).
	if _, _, err := store.AddDependency(dependent.ID, blocker.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// After adding the dependency the dependent should be `blocked`.
	dep, err := store.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent after AddDependency: %v", err)
	}
	if dep.Status != task.StatusBlocked {
		t.Fatalf("expected dependent to be blocked, got %q", dep.Status)
	}

	// Now delete the blocker via the tool.
	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")
	result := tool.Execute(ctx, map[string]any{"task_id": blocker.ID})
	if result.IsError {
		t.Fatalf("delete blocker failed: %s", result.ForLLM)
	}

	// The dependent must now be `next`.
	dep, err = store.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent after blocker delete: %v", err)
	}
	if dep.Status != task.StatusNext {
		t.Errorf("expected dependent to advance to next, got %q", dep.Status)
	}
}

// TestResolveWorkspaceID_FromContext proves that a workspace ID bound in the
// context is returned directly without hitting disk.
func TestResolveWorkspaceID_FromContext(t *testing.T) {
	t.Parallel()
	tool := &TaskCreateTool{}
	ctx := WithWorkspaceID(context.Background(), "ws-from-ctx")
	id, err := tool.resolveWorkspaceID(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "ws-from-ctx" {
		t.Errorf("expected 'ws-from-ctx', got %q", id)
	}
}

// TestResolveWorkspaceID_FromHome proves that when ctx has no workspace ID but
// home is set, the real default workspace ULID is returned.
func TestResolveWorkspaceID_FromHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const wantID = "01JXDEFAULT000000000000001"
	seedWorkspaceDefault(t, home, wantID)

	tool := &TaskCreateTool{home: home}
	id, err := tool.resolveWorkspaceID(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != wantID {
		t.Errorf("expected %q, got %q", wantID, id)
	}
}

// TestResolveWorkspaceID_NoDefault proves an error when no default workspace exists.
func TestResolveWorkspaceID_NoDefault(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// workspaces/ dir exists but has no is_default workspace
	if err := os.MkdirAll(filepath.Join(home, "workspaces"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tool := &TaskCreateTool{home: home}
	_, err := tool.resolveWorkspaceID(context.Background())
	if err == nil {
		t.Fatal("expected error when no default workspace exists")
	}
}

// TestResolveWorkspaceID_NoHomeNoCtx proves an error when neither ctx workspace
// nor home is configured.
func TestResolveWorkspaceID_NoHomeNoCtx(t *testing.T) {
	t.Parallel()
	tool := &TaskCreateTool{} // home == ""
	_, err := tool.resolveWorkspaceID(context.Background())
	if err == nil {
		t.Fatal("expected error when home is not set and ctx has no workspace")
	}
	if !strings.Contains(err.Error(), "no active workspace") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestTaskCreateTool_WorkspaceFromCtx proves task_create uses the ctx workspace.
func TestTaskCreateTool_WorkspaceFromCtx(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws-explicit")

	result := tool.Execute(ctx, map[string]any{
		"title":    "test",
		"prompt":   "do it",
		"agent_id": "agent-b",
	})
	if result.IsError {
		t.Fatalf("task_create failed: %s", result.ForLLM)
	}

	tasks, err := store.List(task.Filter{WorkspaceID: "ws-explicit"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task in ws-explicit, got %d", len(tasks))
	}
}

// TestTaskCreateTool_WorkspaceFromHome proves task_create resolves the default
// workspace when the context has no bound workspace but SetHome is called.
func TestTaskCreateTool_WorkspaceFromHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const defaultWS = "01JXDEFAULT000000000000002"
	seedWorkspaceDefault(t, home, defaultWS)

	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetHome(home)

	ctx := WithAgentID(context.Background(), "caller")
	result := tool.Execute(ctx, map[string]any{
		"title":    "test",
		"prompt":   "do it",
		"agent_id": "agent-b",
	})
	if result.IsError {
		t.Fatalf("task_create failed: %s", result.ForLLM)
	}

	tasks, err := store.List(task.Filter{WorkspaceID: defaultWS})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task in default workspace %q, got %d", defaultWS, len(tasks))
	}
}

// TestTaskCreateTool_NoWorkspaceError proves task_create returns an error when no
// workspace can be resolved (no ctx, no home).
func TestTaskCreateTool_NoWorkspaceError(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store) // home not set

	ctx := WithAgentID(context.Background(), "caller")
	result := tool.Execute(ctx, map[string]any{
		"title":    "test",
		"prompt":   "do it",
		"agent_id": "agent-b",
	})
	if !result.IsError {
		t.Fatal("expected error when no workspace can be resolved")
	}
	if !strings.Contains(result.ForLLM, "could not resolve workspace") {
		t.Errorf("unexpected error message: %s", result.ForLLM)
	}
}

// TestTaskDelete_NotFound proves a clear error on unknown task ID.
func TestTaskDelete_NotFound(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskDeleteTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")
	result := tool.Execute(ctx, map[string]any{"task_id": "nonexistent-id"})
	if !result.IsError {
		t.Fatal("expected error for non-existent task")
	}
	if !strings.Contains(result.ForLLM, "not found") {
		t.Errorf("unexpected message: %s", result.ForLLM)
	}
}

// TestTaskAddTodo_NotFound proves a clear error when the task does not exist.
func TestTaskAddTodo_NotFound(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskAddTodoTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")
	result := tool.Execute(ctx, map[string]any{"task_id": "no-such-task", "text": "hi"})
	if !result.IsError {
		t.Fatal("expected error for non-existent task")
	}
	if !strings.Contains(result.ForLLM, "not found") {
		t.Errorf("unexpected message: %s", result.ForLLM)
	}
}

// TestSetHome_Setter verifies SetHome sets the home field so the resolver can
// use it. This documents the public API the agent loop must call.
func TestSetHome_Setter(t *testing.T) {
	t.Parallel()
	tool := NewTaskCreateTool(nil)
	if tool.home != "" {
		t.Error("home must be empty before SetHome")
	}
	tool.SetHome("/some/path")
	if tool.home != "/some/path" {
		t.Errorf("home not set, got %q", tool.home)
	}
}

// TestTaskAddDependency_MissingAgentID proves that a missing caller ID returns
// an error before any store mutation occurs.
func TestTaskAddDependency_MissingAgentID(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	dep := seedTask(t, store, "agent-owner", "creator-a", "ws-1")
	blocker := seedTask(t, store, "agent-owner", "creator-a", "ws-1")

	tool := NewTaskAddDependencyTool(store)
	// No WithAgentID.
	result := tool.Execute(context.Background(), map[string]any{
		"task_id":    dep.ID,
		"blocked_by": blocker.ID,
	})
	if !result.IsError {
		t.Fatal("expected error when no agent ID in context")
	}
	if !strings.Contains(result.ForLLM, "agent ID not set") {
		t.Errorf("unexpected message: %s", result.ForLLM)
	}
}

// TestTaskDelete_MissingAgentID proves that a missing caller ID returns an error.
func TestTaskDelete_MissingAgentID(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-owner", "agent-owner", "ws-1")

	tool := NewTaskDeleteTool(store)
	result := tool.Execute(context.Background(), map[string]any{"task_id": tk.ID})
	if !result.IsError {
		t.Fatal("expected error when no agent ID in context")
	}
	if !strings.Contains(result.ForLLM, "agent ID not set") {
		t.Errorf("unexpected message: %s", result.ForLLM)
	}
}

// Compile-time check: ensure SetHome is the documented wiring point.
var _ = fmt.Sprintf // suppress import if no other fmt use
