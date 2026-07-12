package tools

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Schema / basic wiring (ported from the former spawn_test.go / subagent_tool_test.go) ---

func TestDelegateTool_Name(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	if tool.Name() != "delegate" {
		t.Errorf("Expected name 'delegate', got '%s'", tool.Name())
	}
}

func TestDelegateTool_Description(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
	if !strings.Contains(desc, "background") {
		t.Errorf("Description should mention background (default) mode, got: %s", desc)
	}
}

func TestDelegateTool_Parameters(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	params := tool.Parameters()

	if params["type"] != "object" {
		t.Errorf("Expected type 'object', got: %v", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Properties should be a map")
	}
	for _, name := range []string{"task", "label", "agent_id", "async", "action", "task_id"} {
		if _, ok = props[name]; !ok {
			t.Errorf("Expected %q parameter in properties", name)
		}
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatal("action parameter should be a map")
	}
	enumVals, ok := action["enum"].([]string)
	if !ok || len(enumVals) != 2 || enumVals[0] != "run" || enumVals[1] != "status" {
		t.Errorf("action enum should be [run, status], got: %v", action["enum"])
	}
}

func TestDelegateTool_Execute_EmptyTask(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(&mockDelegateSpawner{})

	ctx := context.Background()
	tests := []struct {
		name string
		args map[string]any
	}{
		{"empty string", map[string]any{"task": ""}},
		{"whitespace only", map[string]any{"task": "   "}},
		{"tabs and newlines", map[string]any{"task": "\t\n  "}},
		{"missing task key", map[string]any{"label": "test"}},
		{"wrong type", map[string]any{"task": 123}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.Execute(ctx, tt.args)
			if result == nil {
				t.Fatal("Result should not be nil")
			}
			if !result.IsError {
				t.Error("Expected error for invalid task parameter")
			}
			if !strings.Contains(result.ForLLM, "task is required") {
				t.Errorf("Error message should mention 'task is required', got: %s", result.ForLLM)
			}
		})
	}
}

func TestDelegateTool_Execute_NilSpawner(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	// This test's concern is the nil-spawner guard, not the delegation-policy
	// gate — wire a permissive deny-checker for both modes so the (fail-closed
	// per the 7-reviewer-gate fix) gate doesn't mask the assertion under test.
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })

	ctx := context.Background()
	args := map[string]any{"task": "test task"}

	result := tool.Execute(ctx, args)
	if !result.IsError {
		t.Error("Expected error for nil spawner")
	}
	if !strings.Contains(result.ForLLM, "no sub-turn spawner configured") {
		t.Errorf("Error message should mention spawner not configured, got: %s", result.ForLLM)
	}

	// Sync mode (async=false) must also fail cleanly with no spawner.
	syncResult := tool.Execute(ctx, map[string]any{"task": "test task", "async": false})
	if !syncResult.IsError {
		t.Error("Expected error for nil spawner in sync mode")
	}
}

func TestDelegateTool_Execute_InvalidAction(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	result := tool.Execute(context.Background(), map[string]any{"task": "x", "action": "bogus"})
	if !result.IsError {
		t.Error("Expected error for invalid action")
	}
	if !strings.Contains(result.ForLLM, "invalid action") {
		t.Errorf("Expected 'invalid action' error, got: %s", result.ForLLM)
	}
}

func TestDelegateTool_Execute_AsyncWrongType(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(&mockDelegateSpawner{})
	result := tool.Execute(context.Background(), map[string]any{"task": "x", "async": "yes"})
	if !result.IsError {
		t.Error("Expected error for non-boolean async")
	}
	if !strings.Contains(result.ForLLM, "async must be a boolean") {
		t.Errorf("Expected async type error, got: %s", result.ForLLM)
	}
}

// mockDelegateSpawner implements SubTurnSpawner for testing.
type mockDelegateSpawner struct{}

func (m *mockDelegateSpawner) SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error) {
	task := cfg.SystemPrompt
	if strings.Contains(task, "Task: ") {
		parts := strings.Split(task, "Task: ")
		if len(parts) > 1 {
			task = parts[1]
		}
	}
	return &ToolResult{
		ForLLM:  "Task completed: " + task,
		ForUser: "Task completed",
	}, nil
}

// --- FR-D1 / Test Implementation Order #1-4 (agent-delegation-spec.md) ---

// TestDelegate_DefaultIsAsync proves that omitting `async` entirely defaults
// to true (background): the call returns immediately (Async=true), and the
// spawner is invoked in the background rather than blocking the caller.
func TestDelegate_DefaultIsAsync(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	// This test's concern is the async default, not the delegation-policy gate.
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })

	started := make(chan struct{})
	release := make(chan struct{})
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		close(started)
		<-release // block until the test releases it
		return NewToolResult("done"), nil
	}))

	result := tool.Execute(context.Background(), map[string]any{"task": "research X"})
	if result == nil || result.IsError {
		t.Fatalf("expected success, got: %+v", result)
	}
	if !result.Async {
		t.Error("default (no async arg) must return an Async result — background is the default")
	}
	if !strings.Contains(result.ForLLM, "task_id") {
		t.Errorf("async ack must surface a task_id for later status checks, got: %s", result.ForLLM)
	}

	// The call must have returned WITHOUT waiting for the spawner to finish.
	select {
	case <-started:
		// good — the goroutine did start
	case <-time.After(2 * time.Second):
		t.Fatal("background spawner never started")
	}
	close(release) // let the goroutine finish so it doesn't leak past the test
}

// TestDelegate_AsyncFalseBlocks proves that async=false blocks the caller
// until the delegated turn completes and returns its result inline —
// matching the former run_subagent behavior exactly.
func TestDelegate_AsyncFalseBlocks(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	// This test's concern is the async=false blocking behavior, not the
	// delegation-policy gate.
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		time.Sleep(20 * time.Millisecond) // simulate real work
		return &ToolResult{ForLLM: "Task completed: compute Y", ForUser: "done"}, nil
	}))

	start := time.Now()
	result := tool.Execute(context.Background(), map[string]any{
		"task":  "compute Y",
		"async": false,
	})
	elapsed := time.Since(start)

	if result == nil || result.IsError {
		t.Fatalf("expected success, got: %+v", result)
	}
	if result.Async {
		t.Error("async=false must return a non-async (blocking) result")
	}
	if !strings.Contains(result.ForLLM, "Task completed: compute Y") {
		t.Errorf("expected the delegated result inline, got: %s", result.ForLLM)
	}
	if elapsed < 15*time.Millisecond {
		t.Errorf(
			"async=false must have blocked for the sub-turn's duration; elapsed=%v (too fast, likely didn't block)",
			elapsed,
		)
	}
}

// TestDelegate_StatusReflectsRealState is the critical bug-fix proof for
// FR-D2: checking on a task spawned via delegate's async path must report its
// real, current state — NEVER "no subagents have been spawned yet" (the
// confirmed pre-merge disconnect where spawn wrote nowhere check_spawn_status
// could read from).
func TestDelegate_StatusReflectsRealState(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	// This test's concern is action:"status" tracking, not the delegation-policy gate.
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })

	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		defer wg.Done()
		<-release
		return &ToolResult{ForLLM: "Task completed: the real result"}, nil
	}))

	// 1. Delegate a task (async default) and extract the task_id.
	runResult := tool.Execute(context.Background(), map[string]any{
		"task":  "long research task",
		"label": "research",
	})
	if runResult == nil || runResult.IsError {
		t.Fatalf("expected successful delegation, got: %+v", runResult)
	}
	taskID := extractTaskID(t, runResult.ForLLM)

	// 2. While still running, action:"status" must show "running" — NOT the
	// "no subagents have been spawned yet" false-negative.
	statusWhileRunning := tool.Execute(context.Background(), map[string]any{
		"action":  "status",
		"task_id": taskID,
	})
	if statusWhileRunning == nil || statusWhileRunning.IsError {
		t.Fatalf("expected successful status lookup while running, got: %+v", statusWhileRunning)
	}
	if strings.Contains(statusWhileRunning.ForLLM, "no subagents have been spawned") {
		t.Fatalf("REGRESSION: status check reported the false-negative 'no subagents have been spawned yet', got: %s",
			statusWhileRunning.ForLLM)
	}
	if !strings.Contains(statusWhileRunning.ForLLM, "status=running") {
		t.Errorf("expected status=running while the sub-turn is in flight, got: %s", statusWhileRunning.ForLLM)
	}

	// 3. Let the sub-turn finish and confirm status transitions to completed,
	// carrying the real result.
	close(release)
	wg.Wait()
	// Give the async callback goroutine a moment to record completion.
	var statusAfterDone *ToolResult
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statusAfterDone = tool.Execute(context.Background(), map[string]any{
			"action":  "status",
			"task_id": taskID,
		})
		if strings.Contains(statusAfterDone.ForLLM, "status=completed") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if statusAfterDone == nil || !strings.Contains(statusAfterDone.ForLLM, "status=completed") {
		t.Fatalf("expected status=completed after the sub-turn finished, got: %+v", statusAfterDone)
	}
	if !strings.Contains(statusAfterDone.ForLLM, "the real result") {
		t.Errorf("expected the real completed result in the status output, got: %s", statusAfterDone.ForLLM)
	}
}

// TestDelegate_MainAgentCanDelegate confirms FR-D4: DelegateTool has no
// built-in notion of "the main agent" anywhere in its own code — two
// independently-constructed tool instances (simulating two different agents'
// per-agent registrations, exactly as pkg/agent/loop.go wires one DelegateTool
// per agent) behave IDENTICALLY when their injected policy checker allows the
// call. Access is governed solely by the injected delegation-policy function,
// never by any caller-role branch inside the tool.
func TestDelegate_MainAgentCanDelegate(t *testing.T) {
	newAllowedTool := func() *DelegateTool {
		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
			return NewToolResult("ok"), nil
		}))
		tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
		return tool
	}

	// "main" agent's own DelegateTool instance (as loop.go would construct one
	// per agent, including the main/orchestrating agent).
	mainAgentTool := newAllowedTool()
	mainResult := mainAgentTool.Execute(context.Background(), map[string]any{
		"task": "delegate as main", "agent_id": "ray",
	})
	if mainResult == nil || mainResult.IsError {
		t.Fatalf("main agent's delegate call must succeed when its policy allows it, got: %+v", mainResult)
	}

	// A specialist agent's own DelegateTool instance — identical code path.
	specialistTool := newAllowedTool()
	specialistResult := specialistTool.Execute(context.Background(), map[string]any{
		"task": "delegate as specialist", "agent_id": "ava",
	})
	if specialistResult == nil || specialistResult.IsError {
		t.Fatalf("specialist agent's delegate call must succeed identically, got: %+v", specialistResult)
	}

	if mainResult.Async != specialistResult.Async {
		t.Error("main and specialist delegate calls must behave identically — no role-based branch exists")
	}
}

// extractTaskID pulls the "task_id: X" token out of an async delegate ack
// message so a test can drive a follow-up action:"status" call.
func extractTaskID(t *testing.T, forLLM string) string {
	t.Helper()
	const marker = "task_id: "
	idx := strings.Index(forLLM, marker)
	if idx == -1 {
		t.Fatalf("expected %q in async ack, got: %s", marker, forLLM)
	}
	rest := forLLM[idx+len(marker):]
	end := strings.IndexAny(rest, ")\n")
	if end == -1 {
		end = len(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// --- action:"status" scoping (ported from the former spawn_status_test.go) ---

func TestDelegateStatus_Empty(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	result := tool.Execute(context.Background(), map[string]any{"action": "status"})
	if result.IsError {
		t.Fatalf("Expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "No subagents") {
		t.Errorf("Expected 'No subagents' message, got: %s", result.ForLLM)
	}
}

func TestDelegateStatus_ListAll(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	now := time.Now().UnixMilli()
	tool.mu.Lock()
	tool.tasks["delegate-1"] = &DelegateTaskState{
		ID:      "delegate-1",
		Task:    "Do task A",
		Label:   "task-a",
		Status:  "running",
		Created: now,
	}
	tool.tasks["delegate-2"] = &DelegateTaskState{
		ID:      "delegate-2",
		Task:    "Do task B",
		Label:   "task-b",
		Status:  "completed",
		Result:  "Done successfully",
		Created: now,
	}
	tool.tasks["delegate-3"] = &DelegateTaskState{
		ID:     "delegate-3",
		Task:   "Do task C",
		Status: "failed",
		Result: "Error: something went wrong",
	}
	tool.mu.Unlock()

	result := tool.Execute(context.Background(), map[string]any{"action": "status"})
	if result.IsError {
		t.Fatalf("Expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "3 total") {
		t.Errorf("Expected total count in header, got: %s", result.ForLLM)
	}
	for _, id := range []string{"delegate-1", "delegate-2", "delegate-3"} {
		if !strings.Contains(result.ForLLM, id) {
			t.Errorf("Expected task %s in output, got:\n%s", id, result.ForLLM)
		}
	}
	if !strings.Contains(result.ForLLM, "Done successfully") {
		t.Errorf("Expected result text in output, got:\n%s", result.ForLLM)
	}
}

func TestDelegateStatus_GetByID(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.mu.Lock()
	tool.tasks["delegate-42"] = &DelegateTaskState{
		ID:      "delegate-42",
		Task:    "Specific task",
		Label:   "my-task",
		Status:  "failed",
		Result:  "Something went wrong",
		Created: time.Now().UnixMilli(),
	}
	tool.mu.Unlock()

	result := tool.Execute(context.Background(), map[string]any{"action": "status", "task_id": "delegate-42"})
	if result.IsError {
		t.Fatalf("Expected success, got error: %s", result.ForLLM)
	}
	for _, want := range []string{"delegate-42", "failed", "Something went wrong", "my-task"} {
		if !strings.Contains(result.ForLLM, want) {
			t.Errorf("Expected %q in output, got: %s", want, result.ForLLM)
		}
	}
}

func TestDelegateStatus_GetByID_NotFound(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	result := tool.Execute(context.Background(), map[string]any{"action": "status", "task_id": "nonexistent-999"})
	if !result.IsError {
		t.Errorf("Expected error for nonexistent task, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "nonexistent-999") {
		t.Errorf("Expected task ID in error message, got: %s", result.ForLLM)
	}
}

func TestDelegateStatus_TaskID_NonString(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	for _, badVal := range []any{42, 3.14, true, map[string]any{"x": 1}, []string{"a"}} {
		result := tool.Execute(context.Background(), map[string]any{"action": "status", "task_id": badVal})
		if !result.IsError {
			t.Errorf("Expected error for task_id=%T(%v), got success: %s", badVal, badVal, result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "task_id must be a string") {
			t.Errorf("Expected type-error message, got: %s", result.ForLLM)
		}
	}
}

func TestDelegateStatus_ResultTruncation(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	longResult := strings.Repeat("X", 500)
	tool.mu.Lock()
	tool.tasks["delegate-1"] = &DelegateTaskState{
		ID:     "delegate-1",
		Task:   "Long task",
		Status: "completed",
		Result: longResult,
	}
	tool.mu.Unlock()

	result := tool.Execute(context.Background(), map[string]any{"action": "status", "task_id": "delegate-1"})
	if result.IsError {
		t.Fatalf("Unexpected error: %s", result.ForLLM)
	}
	if len(result.ForLLM) >= len(longResult) {
		t.Errorf("Expected result to be truncated, but ForLLM is %d chars", len(result.ForLLM))
	}
	if !strings.Contains(result.ForLLM, "…") {
		t.Errorf("Expected truncation indicator in output, got: %s", result.ForLLM)
	}
}

func TestDelegateStatus_ChannelFiltering_ListAll(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.mu.Lock()
	tool.tasks["delegate-1"] = &DelegateTaskState{
		ID:            "delegate-1",
		Task:          "mine",
		Status:        "running",
		OriginChannel: "telegram",
		OriginChatID:  "chat-A",
	}
	tool.tasks["delegate-2"] = &DelegateTaskState{
		ID:            "delegate-2",
		Task:          "other user",
		Status:        "running",
		OriginChannel: "telegram",
		OriginChatID:  "chat-B",
	}
	tool.mu.Unlock()

	ctx := WithToolContext(context.Background(), "telegram", "chat-A")
	result := tool.Execute(ctx, map[string]any{"action": "status"})
	if result.IsError {
		t.Fatalf("Unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "delegate-1") {
		t.Errorf("Expected own task in output, got:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "delegate-2") {
		t.Errorf("Should NOT see other chat's task, got:\n%s", result.ForLLM)
	}
}

func TestDelegateStatus_ChannelFiltering_GetByID(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.mu.Lock()
	tool.tasks["delegate-99"] = &DelegateTaskState{
		ID:            "delegate-99",
		Task:          "secret",
		Status:        "completed",
		Result:        "private data",
		OriginChannel: "slack",
		OriginChatID:  "room-Z",
	}
	tool.mu.Unlock()

	ctx := WithToolContext(context.Background(), "slack", "room-OTHER")
	result := tool.Execute(ctx, map[string]any{"action": "status", "task_id": "delegate-99"})
	if !result.IsError {
		t.Errorf("Expected error (cross-chat lookup blocked), got: %s", result.ForLLM)
	}
}

func TestDelegateStatus_ChannelFiltering_NoContext(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.mu.Lock()
	tool.tasks["delegate-1"] = &DelegateTaskState{
		ID:            "delegate-1",
		Task:          "t",
		Status:        "completed",
		OriginChannel: "telegram",
		OriginChatID:  "chat-A",
	}
	tool.mu.Unlock()

	// No ToolContext injected — callerChannel and callerChatID are both "".
	// All tasks are listed only when no channel/chat context is injected.
	result := tool.Execute(context.Background(), map[string]any{"action": "status"})
	if result.IsError {
		t.Fatalf("Unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "delegate-1") {
		t.Errorf("Expected task visible from no-context caller, got:\n%s", result.ForLLM)
	}
}

// TestDelegateStatus_DifferentConversation_NotFound is the boundary-condition
// proof (Behavioral Contract / Boundary conditions): a status check
// referencing a task_id from a different conversation must report "not
// found," never another conversation's data.
func TestDelegateStatus_DifferentConversation_NotFound(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.mu.Lock()
	tool.tasks["delegate-7"] = &DelegateTaskState{
		ID: "delegate-7", Task: "conversation A's task", Status: "completed",
		Result: "conversation A's private result", OriginChannel: "cli", OriginChatID: "conv-A",
	}
	tool.mu.Unlock()

	ctx := WithToolContext(context.Background(), "cli", "conv-B")
	result := tool.Execute(ctx, map[string]any{"action": "status", "task_id": "delegate-7"})
	if !result.IsError {
		t.Fatalf("expected 'not found' for a different conversation, got success: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "conversation A's private result") {
		t.Error("must never leak another conversation's task data")
	}
	if !strings.Contains(result.ForLLM, "No subagent found") {
		t.Errorf("expected a 'not found' style message, got: %s", result.ForLLM)
	}
}

// TestDelegate_AsyncFalse_DoesNotRecordStatusTask proves that the sync (await)
// path does not create an entry in the async task store — it has nothing to
// poll, matching run_subagent's historical behavior of not participating in
// check_spawn_status at all.
func TestDelegate_AsyncFalse_DoesNotRecordStatusTask(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	// This test's concern is executeSync not touching tool.tasks, not the
	// delegation-policy gate — wire a permissive checker so the call actually
	// reaches executeSync (an unwired/denied call would also leave tool.tasks
	// empty, but for the wrong reason, silently testing nothing real).
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		return NewToolResult("done"), nil
	}))

	result := tool.Execute(context.Background(), map[string]any{"task": "sync work", "async": false})
	if result == nil || result.IsError {
		t.Fatalf("expected the sync delegate call to succeed, got: %+v", result)
	}

	tool.mu.Lock()
	count := len(tool.tasks)
	tool.mu.Unlock()
	if count != 0 {
		t.Errorf("expected zero recorded tasks after a sync (async=false) call, got %d", count)
	}
}

// TestDelegate_SyncInterrupted_PropagatesToResult is the regression proof for
// Finding F (A-I4 round 5): a synchronous (await-mode) delegate call whose
// child sub-turn was interrupted by a parent-turn cancellation must surface
// ToolResult.Interrupted=true on executeSync's own return value — not fall
// into the generic "Delegate execution failed" shortcut that used to discard
// spawnSubTurn's Interrupted classification the instant err was non-nil
// (indistinguishable, once folded into that shortcut, from a genuine
// failure). pkg/agent/loop.go's tcStatus derivation reads exactly this field
// to persist tc.Status="interrupted" instead of "error", which is what lets
// a session reload's subagent_end frame match what live already showed.
func TestDelegate_SyncInterrupted_PropagatesToResult(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	// Simulates spawnSubTurn's OWN sync-conversion + cleanup-defer output for
	// a canceled child context (pkg/agent/subturn.go): a non-nil err
	// alongside a non-nil result carrying IsError=true (preserves the outer
	// tool_call_result frame's existing "error" badge — that contract has no
	// "interrupted" value) AND Interrupted=true (the richer, span-specific
	// signal).
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		cancelErr := context.Canceled
		return &ToolResult{
			Err:         cancelErr,
			ForLLM:      "SubTurn failed: context canceled",
			IsError:     true,
			Interrupted: true,
		}, cancelErr
	}))

	result := tool.Execute(context.Background(), map[string]any{"task": "long work", "async": false})
	if result == nil {
		t.Fatal("expected a non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError=true — the outer tool_call_result badge must stay 'error', unchanged by this fix")
	}
	if !result.Interrupted {
		t.Error("expected Interrupted=true to survive onto executeSync's returned result — this is the field " +
			"pkg/agent/loop.go's tcStatus derivation reads to persist status \"interrupted\" instead of \"error\"")
	}
	if strings.Contains(result.ForLLM, "Delegate execution failed") {
		t.Errorf("interrupted sync delegation must NOT take the generic error shortcut, got ForLLM: %s", result.ForLLM)
	}
}

// TestDelegate_SyncGenuineError_StillUsesGenericShortcut is the negative
// counterpart to TestDelegate_SyncInterrupted_PropagatesToResult: a real
// dispatch/execution failure (Interrupted=false) must still take the
// existing generic "Delegate execution failed" path unchanged — the Finding
// F fix narrows the shortcut's bypass to interruptions only, it must not
// widen success/failure classification for every other error.
func TestDelegate_SyncGenuineError_StillUsesGenericShortcut(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		return nil, errors.New("dispatch rejected: depth limit exceeded")
	}))

	result := tool.Execute(context.Background(), map[string]any{"task": "long work", "async": false})
	if result == nil || !result.IsError {
		t.Fatalf("expected an error result, got: %+v", result)
	}
	if result.Interrupted {
		t.Error("a genuine dispatch failure must never be classified as Interrupted")
	}
	if !strings.Contains(result.ForLLM, "Delegate execution failed") {
		t.Errorf("a genuine dispatch failure must still use the generic shortcut message, got: %s", result.ForLLM)
	}
}
