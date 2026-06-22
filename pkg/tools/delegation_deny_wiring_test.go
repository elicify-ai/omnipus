package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/task"
)

// decodeDelegationFailure parses a denied-delegation tool result's ForLLM as the
// structured DelegationFailure payload, failing the test if it is not valid JSON
// with the expected discriminator.
func decodeDelegationFailure(t *testing.T, result *ToolResult) DelegationFailure {
	t.Helper()
	var failure DelegationFailure
	if err := json.Unmarshal([]byte(result.ForLLM), &failure); err != nil {
		t.Fatalf("delegation failure result is not valid JSON: %v (got: %s)", err, result.ForLLM)
	}
	if failure.Error != "delegation_denied" {
		t.Fatalf("expected error discriminator 'delegation_denied', got %q (raw: %s)", failure.Error, result.ForLLM)
	}
	return failure
}

// These tests prove the delegation-deny WIRING inside SpawnTool.Execute and
// TaskCreateTool.Execute — i.e. that a deny-checker installed via
// SetDelegationDenyChecker actually ABORTS a disallowed delegation (the tool
// returns an error and never reaches the spawn/create side effect), and that the
// nil-checker path falls back to the legacy boolean allowlist. The standalone
// checker is exercised elsewhere; here we assert the tool consults it.

// TestSpawnTool_DelegationDenyChecker_Aborts proves that a deny-checker returning
// a non-empty reason aborts the spawn before the spawner is ever invoked.
func TestSpawnTool_DelegationDenyChecker_Aborts(t *testing.T) {
	tool := NewSpawnTool(NewSubagentManager(&MockLLMProvider{}, "test-model", "/tmp/test"))

	// A spawner that records whether it ran — it MUST NOT run on a denied spawn.
	spawned := false
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		spawned = true
		return NewToolResult("ran"), nil
	}))

	var gotTarget string
	tool.SetDelegationDenyChecker(func(_ context.Context, targetAgentID string) *DelegationDenial {
		gotTarget = targetAgentID
		return &DelegationDenial{
			Reason:        "target untrusted (mode forbidden)",
			Policy:        DenyTrustSet,
			TargetAgentID: targetAgentID,
		}
	})

	result := tool.Execute(context.Background(), map[string]any{
		"task":     "do the thing",
		"agent_id": "evil-agent",
	})

	if result == nil || !result.IsError {
		t.Fatalf("expected denied spawn to return an error result, got %+v", result)
	}
	failure := decodeDelegationFailure(t, result)
	if failure.Tool != "spawn" {
		t.Errorf("expected structured failure tool 'spawn', got %q", failure.Tool)
	}
	if failure.Policy != DenyTrustSet {
		t.Errorf("expected policy %q, got %q", DenyTrustSet, failure.Policy)
	}
	if !strings.Contains(failure.Reason, "target untrusted") {
		t.Errorf("expected the checker's reason to surface, got: %s", failure.Reason)
	}
	if failure.TargetAgentID != "evil-agent" {
		t.Errorf("expected target_agent_id 'evil-agent', got %q", failure.TargetAgentID)
	}
	if gotTarget != "evil-agent" {
		t.Errorf("expected checker to receive target 'evil-agent', got %q", gotTarget)
	}
	if spawned {
		t.Error("spawner must NOT run when delegation is denied")
	}
}

// TestSpawnTool_DelegationDenyChecker_Allows proves the allow path (empty reason)
// lets the spawn through to the spawner.
func TestSpawnTool_DelegationDenyChecker_Allows(t *testing.T) {
	tool := NewSpawnTool(NewSubagentManager(&MockLLMProvider{}, "test-model", "/tmp/test"))
	spawned := false
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		spawned = true
		return NewToolResult("ran"), nil
	}))
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	result := tool.Execute(context.Background(), map[string]any{
		"task":     "do the thing",
		"agent_id": "trusted-agent",
	})
	if result == nil || result.IsError {
		t.Fatalf("expected allowed spawn to succeed, got %+v", result)
	}
	// SpawnTool launches the spawner in a goroutine; we only assert the call was
	// accepted (Async) here — the goroutine race on `spawned` is not asserted.
	if !result.Async {
		t.Error("expected an async result for an allowed spawn")
	}
	_ = spawned
}

// TestSpawnTool_NilDenyChecker_FallsBackToAllowlist proves that with NO deny
// checker installed, the legacy boolean allowlist is consulted and a disallowed
// target is rejected.
func TestSpawnTool_NilDenyChecker_FallsBackToAllowlist(t *testing.T) {
	tool := NewSpawnTool(NewSubagentManager(&MockLLMProvider{}, "test-model", "/tmp/test"))
	spawned := false
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		spawned = true
		return NewToolResult("ran"), nil
	}))

	// No SetDelegationDenyChecker — legacy path only.
	var checkedTarget string
	tool.SetAllowlistChecker(func(targetAgentID string) bool {
		checkedTarget = targetAgentID
		return false // deny
	})

	result := tool.Execute(context.Background(), map[string]any{
		"task":     "do the thing",
		"agent_id": "blocked-agent",
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected legacy allowlist denial to return an error, got %+v", result)
	}
	if !strings.Contains(result.ForLLM, "not allowed to spawn agent 'blocked-agent'") {
		t.Errorf("expected legacy allowlist denial message, got: %s", result.ForLLM)
	}
	if checkedTarget != "blocked-agent" {
		t.Errorf("expected legacy allowlist to receive 'blocked-agent', got %q", checkedTarget)
	}
	if spawned {
		t.Error("spawner must NOT run when the legacy allowlist denies the target")
	}
}

// TestTaskCreateTool_DelegationDenyChecker_Aborts proves the deny-checker aborts a
// task_create delegation before any task is persisted.
func TestTaskCreateTool_DelegationDenyChecker_Aborts(t *testing.T) {
	// Sprint 2: taskstore.New → task.New (unified store).
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)

	var gotTarget string
	tool.SetDelegationDenyChecker(func(_ context.Context, targetAgentID string) *DelegationDenial {
		gotTarget = targetAgentID
		return &DelegationDenial{
			Reason:        "delegation mode 'task' forbidden for target",
			Policy:        DenyMode,
			TargetAgentID: targetAgentID,
		}
	})

	ctx := WithAgentID(context.Background(), "caller-agent")
	result := tool.Execute(ctx, map[string]any{
		"title":    "subtask",
		"prompt":   "do work",
		"agent_id": "evil-agent",
	})

	if result == nil || !result.IsError {
		t.Fatalf("expected denied task_create to return an error, got %+v", result)
	}
	failure := decodeDelegationFailure(t, result)
	if failure.Tool != "task_create" {
		t.Errorf("expected structured failure tool 'task_create', got %q", failure.Tool)
	}
	if failure.Policy != DenyMode {
		t.Errorf("expected policy %q, got %q", DenyMode, failure.Policy)
	}
	if !strings.Contains(failure.Reason, "forbidden for target") {
		t.Errorf("expected the checker's reason to surface, got: %s", failure.Reason)
	}
	if gotTarget != "evil-agent" {
		t.Errorf("expected checker to receive 'evil-agent', got %q", gotTarget)
	}

	// Prove no task was persisted (the deny must short-circuit before store.Create).
	// Sprint 2: task.Filter replaces taskstore.TaskFilter.
	tasks, err := store.List(task.Filter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected zero tasks persisted on denied delegation, got %d", len(tasks))
	}
}

// TestTaskCreateTool_NilDenyChecker_FallsBackToAllowlist proves that with no deny
// checker, the legacy boolean delegateCheck is consulted and a disallowed target
// is rejected without persisting a task.
func TestTaskCreateTool_NilDenyChecker_FallsBackToAllowlist(t *testing.T) {
	// Sprint 2: taskstore.New → task.New (unified store).
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)

	var checkedTarget string
	tool.SetDelegateChecker(func(targetAgentID string) bool {
		checkedTarget = targetAgentID
		return false // deny
	})

	ctx := WithAgentID(context.Background(), "caller-agent")
	result := tool.Execute(ctx, map[string]any{
		"title":    "subtask",
		"prompt":   "do work",
		"agent_id": "blocked-agent",
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected legacy delegateCheck denial to return an error, got %+v", result)
	}
	failure := decodeDelegationFailure(t, result)
	if failure.Tool != "task_create" || failure.Policy != DenyTrustSet {
		t.Errorf("expected structured task_create/trust_set failure, got %+v", failure)
	}
	if !strings.Contains(failure.Reason, "delegation to blocked-agent not allowed") {
		t.Errorf("expected legacy delegateCheck denial message, got: %s", failure.Reason)
	}
	if checkedTarget != "blocked-agent" {
		t.Errorf("expected legacy delegateCheck to receive 'blocked-agent', got %q", checkedTarget)
	}

	// Sprint 2: task.Filter replaces taskstore.TaskFilter.
	tasks, err := store.List(task.Filter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected zero tasks persisted on denied delegation, got %d", len(tasks))
	}
}

// spawnerFunc adapts a function to the SubTurnSpawner interface for tests.
type spawnerFunc func(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error)

func (f spawnerFunc) SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error) {
	return f(ctx, cfg)
}
