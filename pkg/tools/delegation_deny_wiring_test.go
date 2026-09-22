package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
// SetDelegationDenyCheckerBackground/Await/SetDelegationDenyChecker actually
// ABORTS a disallowed delegation (the tool returns an error and never reaches
// the spawn/create side effect). The standalone checker is exercised
// elsewhere; here we assert the tool consults it.
//
// ADR-037 retired the legacy boolean allowlistCheck/delegateChecker fallbacks
// these tools used to fall back to when the deny-checker above was nil (that
// fallback was only ever reachable in the never-happens-in-production case of
// an unwired deny-checker). The legacy fallback was ITSELF deny-by-default
// (config.IsDelegationAllowed / registry.CanSpawnSubagent both returned false
// on an unset policy) — so deleting it must not also delete that safety net.
// The *_NilDenyChecker_FailsClosed tests below (7-reviewer-gate follow-up,
// silent-failure-hunter finding) prove the corrected behavior: an unwired
// deny-checker now DENIES, not allows, replacing an earlier *_FailsOpen
// version of these same tests that characterized (and thereby normalized) the
// unsafe fall-through as if it were intentional.

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
	launcher := &recordingSessionLauncher{}
	tool.SetSessionLauncher(launcher)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-session"), "test-caller")
	result := tool.Execute(ctx, map[string]any{
		"task":     "do the thing",
		"agent_id": "trusted-agent",
	})
	if result == nil || result.IsError {
		t.Fatalf("expected allowed delegate call to succeed, got %+v", result)
	}
	if launcher.dispatchSessionID == "" {
		t.Error("the allowed delegate call never reached the session launcher")
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

// TestDelegateTool_BackgroundNilDenyChecker_FailsClosed mirrors the await
// test above for the background (async=true, default) mode.
func TestDelegateTool_BackgroundNilDenyChecker_FailsClosed(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	spawned := make(chan struct{}, 1)
	tool.SetSpawner(spawnerFunc(func(context.Context, SubTurnConfig) (*ToolResult, error) {
		spawned <- struct{}{}
		return NewToolResult("ran"), nil
	}))

	// No SetDelegationDenyCheckerBackground installed at all.
	result := tool.Execute(context.Background(), map[string]any{
		"task":     "do the thing",
		"agent_id": "some-agent",
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected an unwired background deny-checker to fail CLOSED (deny), got %+v", result)
	}
	failure := decodeDelegationFailure(t, result)
	if failure.Tool != "delegate" {
		t.Errorf("expected structured failure tool 'delegate', got %q", failure.Tool)
	}
	if failure.Policy != string(DenyTrustSet) {
		t.Errorf("expected policy %q, got %q", DenyTrustSet, failure.Policy)
	}
	select {
	case <-spawned:
		t.Error("spawner must NOT run when no deny-checker is installed (fail-closed-when-unwired)")
	case <-time.After(200 * time.Millisecond):
		// spawner did not run within the window, as expected for fail-closed.
	}
}

// TestTaskCreateTool_NilDenyChecker_FailsClosed proves that with no
// SetDelegationDenyChecker installed, create_task is DENIED, not silently
// allowed — same 7-reviewer-gate follow-up as the DelegateTool tests above.
func TestTaskCreateTool_NilDenyChecker_FailsClosed(t *testing.T) {
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)

	ctx := WithAgentID(context.Background(), "caller-agent")
	ctx = WithWorkspaceID(ctx, "ws-1")
	result := tool.Execute(ctx, map[string]any{
		"title":    "subtask",
		"prompt":   "do work",
		"agent_id": "any-agent",
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected an unwired delegation-deny checker to fail CLOSED (deny), got %+v", result)
	}
	failure := decodeDelegationFailure(t, result)
	if failure.Tool != "create_task" {
		t.Errorf("expected structured failure tool 'create_task', got %q", failure.Tool)
	}
	if failure.Policy != string(DenyTrustSet) {
		t.Errorf("expected policy %q, got %q", DenyTrustSet, failure.Policy)
	}

	tasks, err := store.List(task.Filter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected zero tasks persisted (fail-closed-when-unwired), got %d", len(tasks))
	}
}

// TestTaskUpdateTool_NilDenyChecker_FailsClosed mirrors
// TestTaskCreateTool_NilDenyChecker_FailsClosed for update_task's
// reassignment path (agent_id changed from the task's current assignee),
// which routes through the same delegationDeny gate.
func TestTaskUpdateTool_NilDenyChecker_FailsClosed(t *testing.T) {
	store := task.New(t.TempDir())
	tk := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	tool := NewTaskUpdateTool(store)
	// No SetDelegationDenyChecker installed at all.

	ctx := WithAgentID(context.Background(), "agent-a")
	// Issue #593: status "failed", not "in_progress" — the seeded task starts
	// at StatusNext, so "in_progress" would now be rejected by the
	// in_progress-forge guard before the reassignment's delegation-deny gate
	// (this test's actual concern) is ever reached.
	result := tool.Execute(ctx, map[string]any{
		"task_id":  tk.ID,
		"status":   "failed",
		"agent_id": "agent-b",
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected an unwired delegation-deny checker to fail CLOSED (deny), got %+v", result)
	}
	failure := decodeDelegationFailure(t, result)
	if failure.Tool != "update_task" {
		t.Errorf("expected structured failure tool 'update_task', got %q", failure.Tool)
	}
	if failure.Policy != string(DenyTrustSet) {
		t.Errorf("expected policy %q, got %q", DenyTrustSet, failure.Policy)
	}

	// No side effect: the task's assignee must be unchanged.
	reloaded, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if reloaded.AgentID != "agent-a" {
		t.Errorf("expected agent_id unchanged at 'agent-a' after a rejected reassignment, got %q", reloaded.AgentID)
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
