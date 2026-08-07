// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors
//
// SEAM 4 reconciliation tests (worktree wf/w1-seams): create_task/update_task
// wiring of the ADR-053 write_set/stream/is_join plan-member fields
// (W1-lint's Task.WriteSet/Stream/IsJoin + plan-lint) through the tool layer.
//
// Build tags: goolm,stdjson (CGO_ENABLED=0).
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestSeam4' -p 1 ./pkg/tools/

package tools

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestSeam4_CreateTask_PersistsWriteSetStreamIsJoin proves create_task accepts
// and persists write_set/stream/is_join through the store — the data plan-lint
// (pkg/plan) reads at approve time.
func TestSeam4_CreateTask_PersistsWriteSetStreamIsJoin(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "caller")
	ctx = WithWorkspaceID(ctx, "ws-seam4")

	result := tool.Execute(ctx, map[string]any{
		"title":     "shard A",
		"prompt":    "implement shard A",
		"agent_id":  "agent-b",
		"criteria":  validCriteriaArg(),
		"write_set": []any{"pkg/plan/plan_lint.go", "pkg/plan/plan_lint_test.go"},
		"stream":    "stream-schema",
		"is_join":   true,
	})
	if result.IsError {
		t.Fatalf("task_create failed: %s", result.ForLLM)
	}

	tasks, err := store.List(task.Filter{WorkspaceID: "ws-seam4"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]

	if len(got.WriteSet) != 2 || got.WriteSet[0] != "pkg/plan/plan_lint.go" || got.WriteSet[1] != "pkg/plan/plan_lint_test.go" {
		t.Errorf("WriteSet = %v, want [pkg/plan/plan_lint.go pkg/plan/plan_lint_test.go]", got.WriteSet)
	}
	if got.Stream != "stream-schema" {
		t.Errorf("Stream = %q, want %q", got.Stream, "stream-schema")
	}
	if !got.IsJoin {
		t.Errorf("IsJoin = false, want true")
	}

	// Round-trip through the store's own Get (proves it was actually written
	// to disk, not just held in the in-memory Create return value).
	reloaded, err := store.Get(got.ID)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if len(reloaded.WriteSet) != 2 {
		t.Errorf("reloaded WriteSet = %v, want 2 entries (persisted to disk)", reloaded.WriteSet)
	}
	if reloaded.Stream != "stream-schema" {
		t.Errorf("reloaded Stream = %q, want %q (persisted to disk)", reloaded.Stream, "stream-schema")
	}
	if !reloaded.IsJoin {
		t.Errorf("reloaded IsJoin = false, want true (persisted to disk)")
	}
}

// TestSeam4_UpdateTask_ReplacesAndClearsWriteSetStreamIsJoin proves
// update_task's three-way write_set semantics (populated REPLACEs,
// provided-empty CLEARs, absent leaves unchanged) mirroring blocked_by, plus
// plain overwrite for stream/is_join.
func TestSeam4_UpdateTask_ReplacesAndClearsWriteSetStreamIsJoin(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	seeded := seedTask(t, store, "agent-a", "agent-a", "ws-1")

	updateTool := NewTaskUpdateTool(store)
	ctx := WithAgentID(context.Background(), "agent-a")

	// 1. Populate write_set/stream/is_join on a task that started without them.
	res := updateTool.Execute(ctx, map[string]any{
		"task_id":   seeded.ID,
		"write_set": []any{"a.txt", "b.txt"},
		"stream":    "stream-x",
		"is_join":   true,
	})
	if res.IsError {
		t.Fatalf("update_task (populate) failed: %s", res.ForLLM)
	}
	got, err := store.Get(seeded.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.WriteSet) != 2 {
		t.Fatalf("WriteSet after populate = %v, want 2 entries", got.WriteSet)
	}
	if got.Stream != "stream-x" {
		t.Fatalf("Stream after populate = %q, want stream-x", got.Stream)
	}
	if !got.IsJoin {
		t.Fatalf("IsJoin after populate = false, want true")
	}

	// 2. Provided-empty write_set CLEARs it; empty-string stream CLEARs it;
	// is_join flips back to false. All in one PATCH, mirroring blocked_by's
	// three-way CLEAR convention.
	res = updateTool.Execute(ctx, map[string]any{
		"task_id":   seeded.ID,
		"write_set": []any{},
		"stream":    "",
		"is_join":   false,
	})
	if res.IsError {
		t.Fatalf("update_task (clear) failed: %s", res.ForLLM)
	}
	got, err = store.Get(seeded.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.WriteSet) != 0 {
		t.Errorf("WriteSet after clear = %v, want empty", got.WriteSet)
	}
	if got.Stream != "" {
		t.Errorf("Stream after clear = %q, want empty", got.Stream)
	}
	if got.IsJoin {
		t.Errorf("IsJoin after clear = true, want false")
	}

	// 3. Absent fields leave the current values unchanged — bump priority
	// only, and confirm write_set/stream/is_join survive untouched (already
	// cleared to zero values above, so "unchanged" == "still zero" here;
	// re-populate first so "unchanged" is a meaningful assertion).
	res = updateTool.Execute(ctx, map[string]any{
		"task_id":   seeded.ID,
		"write_set": []any{"c.txt"},
		"stream":    "stream-y",
	})
	if res.IsError {
		t.Fatalf("update_task (re-populate): %s", res.ForLLM)
	}
	res = updateTool.Execute(ctx, map[string]any{
		"task_id":  seeded.ID,
		"priority": float64(1),
	})
	if res.IsError {
		t.Fatalf("update_task (priority only) failed: %s", res.ForLLM)
	}
	got, err = store.Get(seeded.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.WriteSet) != 1 || got.WriteSet[0] != "c.txt" {
		t.Errorf("WriteSet after unrelated patch = %v, want unchanged [c.txt]", got.WriteSet)
	}
	if got.Stream != "stream-y" {
		t.Errorf("Stream after unrelated patch = %q, want unchanged stream-y", got.Stream)
	}
	if got.Priority != 1 {
		t.Errorf("Priority = %d, want 1", got.Priority)
	}
}
