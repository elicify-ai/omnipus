package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// decodeDelegationFailure parses a denied-delegation tool result's ForLLM as the
// generated DelegationFailure wire type, failing the test if it is not valid
// JSON with the expected discriminator.
func decodeDelegationFailure(t *testing.T, result *ToolResult) generated.DelegationFailure {
	t.Helper()
	var failure generated.DelegationFailure
	if err := json.Unmarshal([]byte(result.ForLLM), &failure); err != nil {
		t.Fatalf("delegation failure result is not valid JSON: %v (got: %s)", err, result.ForLLM)
	}
	if failure.Error != "delegation_denied" {
		t.Fatalf("expected error discriminator 'delegation_denied', got %q (raw: %s)", failure.Error, result.ForLLM)
	}
	return failure
}

// targetAgentID returns the failure's target agent id, or "" when unset.
func targetAgentID(f generated.DelegationFailure) string {
	if f.TargetAgentId == nil {
		return ""
	}
	return *f.TargetAgentId
}

// These tests prove the delegation-deny WIRING inside DelegateTool.Execute and
// TaskCreateTool.Execute — i.e. that a deny-checker installed via
// SetDelegationDenyCheckerBackground/Await actually ABORTS a disallowed
// delegation (the tool returns an error and never reaches the spawn/create
// side effect), and that the nil-checker path falls back to the legacy
// boolean allowlist. The standalone checker is exercised elsewhere; here we
// assert the tool consults it.

// TestDelegateTool_DelegationDenyChecker_Aborts proves that a deny-checker
// returning a non-empty reason aborts the (default async/background) delegate
// call before the spawner is ever invoked.
func TestDelegateTool_DelegationDenyChecker_Aborts(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)

	// A spawner that records whether it ran — it MUST NOT run on a denied delegate.
	spawned := false
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		spawned = true
		return NewToolResult("ran"), nil
	}))

	var gotTarget string
	tool.SetDelegationDenyCheckerBackground(func(_ context.Context, targetAgentID string) *DelegationDenial {
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
		t.Fatalf("expected denied delegate call to return an error result, got %+v", result)
	}
	failure := decodeDelegationFailure(t, result)
	if failure.Tool != "delegate" {
		t.Errorf("expected structured failure tool 'delegate', got %q", failure.Tool)
	}
	if failure.Policy != string(DenyTrustSet) {
		t.Errorf("expected policy %q, got %q", DenyTrustSet, failure.Policy)
	}
	if !strings.Contains(failure.Reason, "target untrusted") {
		t.Errorf("expected the checker's reason to surface, got: %s", failure.Reason)
	}
	if targetAgentID(failure) != "evil-agent" {
		t.Errorf("expected target_agent_id 'evil-agent', got %q", targetAgentID(failure))
	}
	if gotTarget != "evil-agent" {
		t.Errorf("expected checker to receive target 'evil-agent', got %q", gotTarget)
	}
	if spawned {
		t.Error("spawner must NOT run when delegation is denied")
	}
}

// TestDelegateTool_DelegationDenyChecker_Allows proves the allow path (empty
// reason) lets the (default async/background) delegate call through to the
// spawner.
func TestDelegateTool_DelegationDenyChecker_Allows(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	spawned := false
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		spawned = true
		return NewToolResult("ran"), nil
	}))
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })

	result := tool.Execute(context.Background(), map[string]any{
		"task":     "do the thing",
		"agent_id": "trusted-agent",
	})
	if result == nil || result.IsError {
		t.Fatalf("expected allowed delegate call to succeed, got %+v", result)
	}
	// DelegateTool launches the spawner in a goroutine for async=true (default);
	// we only assert the call was accepted (Async) here — the goroutine race on
	// `spawned` is not asserted.
	if !result.Async {
		t.Error("expected an async result for an allowed background delegate call")
	}
	_ = spawned
}

// TestDelegateTool_NilDenyChecker_FallsBackToAllowlist proves that with NO
// background deny checker installed, the legacy boolean allowlist is
// consulted and a disallowed target is rejected.
func TestDelegateTool_NilDenyChecker_FallsBackToAllowlist(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	spawned := false
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		spawned = true
		return NewToolResult("ran"), nil
	}))

	// No SetDelegationDenyCheckerBackground — legacy path only.
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
	if failure.Tool != "create_task" {
		t.Errorf("expected structured failure tool 'create_task', got %q", failure.Tool)
	}
	if failure.Policy != string(DenyMode) {
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
	if failure.Tool != "create_task" || failure.Policy != string(DenyTrustSet) {
		t.Errorf("expected structured create_task/trust_set failure, got %+v", failure)
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

// TestDelegateTool_AwaitDelegationDenyChecker_Aborts proves the await-mode
// (async=false) deny-checker aborts a delegate call: the structured failure
// carries Tool=="delegate" and the spawner never runs (no sub-turn).
func TestDelegateTool_AwaitDelegationDenyChecker_Aborts(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)

	spawned := false
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		spawned = true
		return NewToolResult("ran"), nil
	}))

	tool.SetDelegationDenyCheckerAwait(func(_ context.Context, _ string) *DelegationDenial {
		return &DelegationDenial{
			Reason:        "delegation depth cap reached",
			Policy:        DenyDepth,
			TargetAgentID: "deep-agent",
		}
	})

	result := tool.Execute(context.Background(), map[string]any{
		"task":  "do the thing",
		"label": "deep work",
		"async": false,
	})

	if result == nil || !result.IsError {
		t.Fatalf("expected denied delegate (await) call to return an error result, got %+v", result)
	}
	failure := decodeDelegationFailure(t, result)
	if failure.Tool != "delegate" {
		t.Errorf("expected structured failure tool 'delegate', got %q", failure.Tool)
	}
	if failure.Policy != string(DenyDepth) {
		t.Errorf("expected policy %q, got %q", DenyDepth, failure.Policy)
	}
	if !strings.Contains(failure.Reason, "depth cap reached") {
		t.Errorf("expected the checker's reason to surface, got: %s", failure.Reason)
	}
	if targetAgentID(failure) != "deep-agent" {
		t.Errorf("expected target_agent_id 'deep-agent', got %q", targetAgentID(failure))
	}
	if spawned {
		t.Error("spawner must NOT run when delegation is denied")
	}
}

// TestDelegateTool_AwaitNilDenyChecker_FallsBackToDelegateChecker proves that
// with NO await-mode deny checker installed, the legacy boolean delegate
// checker is consulted and a denial rejects the call.
func TestDelegateTool_AwaitNilDenyChecker_FallsBackToDelegateChecker(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	spawned := false
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		spawned = true
		return NewToolResult("ran"), nil
	}))

	// No SetDelegationDenyCheckerAwait — legacy path only.
	tool.SetDelegateChecker(func() bool { return false }) // deny

	result := tool.Execute(context.Background(), map[string]any{
		"task":  "do the thing",
		"async": false,
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected legacy delegateChecker denial to return an error, got %+v", result)
	}
	if !strings.Contains(result.ForLLM, "delegation not allowed") {
		t.Errorf("expected legacy delegateChecker denial message, got: %s", result.ForLLM)
	}
	if spawned {
		t.Error("spawner must NOT run when the legacy delegateChecker denies")
	}
}

// TestDelegationDeniedResult_DefaultsInvariant proves the contract invariant
// defense: a denial with an empty reason / invalid policy still serializes a
// schema-valid DelegationFailure (non-empty reason, enum policy) the SPA can
// render, rather than a silently-dropped payload.
func TestDelegationDeniedResult_DefaultsInvariant(t *testing.T) {
	result := DelegationDeniedResult("delegate", &DelegationDenial{}) // empty reason + invalid policy
	if result == nil || !result.IsError {
		t.Fatalf("expected an error result, got %+v", result)
	}
	failure := decodeDelegationFailure(t, result)
	if failure.Reason == "" {
		t.Error("reason must be defaulted to a non-empty string (contract minLength:1)")
	}
	switch failure.Policy {
	case string(DenyTrustSet), string(DenyMode), string(DenyDepth):
		// valid enum value
	default:
		t.Errorf("policy must be defaulted to a valid enum value, got %q", failure.Policy)
	}
	if failure.Tool != "delegate" {
		t.Errorf("expected tool 'delegate', got %q", failure.Tool)
	}
}

// spawnerFunc adapts a function to the SubTurnSpawner interface for tests.
type spawnerFunc func(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error)

func (f spawnerFunc) SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error) {
	return f(ctx, cfg)
}
