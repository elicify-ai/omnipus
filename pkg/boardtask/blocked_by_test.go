// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package boardtask_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/boardtask"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// ---- helpers ----------------------------------------------------------------

// taskMap is a simple in-memory task graph for validator tests.
type taskMap map[string]boardtask.Task

func (m taskMap) load(id string) (boardtask.Task, error) {
	t, ok := m[id]
	if !ok {
		return boardtask.Task{}, os.ErrNotExist
	}
	return t, nil
}

// writeTasks writes a set of tasks to a temp directory for disk-level tests.
func writeTasks(t *testing.T, tasks []boardtask.Task) string {
	t.Helper()
	dir := t.TempDir()
	for _, task := range tasks {
		data, err := json.MarshalIndent(task, "", "  ")
		if err != nil {
			t.Fatalf("writeTasks: marshal %s: %v", task.ID, err)
		}
		path := filepath.Join(dir, task.ID+".json")
		if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
			t.Fatalf("writeTasks: write %s: %v", task.ID, err)
		}
	}
	return dir
}

// readTask reads a task from the temp dir.
func readTask(t *testing.T, dir, id string) boardtask.Task {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatalf("readTask %s: %v", id, err)
	}
	var task boardtask.Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatalf("readTask %s: unmarshal: %v", id, err)
	}
	return task
}

// makeTask returns a minimal GTD task with the given ID and blocked_by list.
func makeTask(id string, blockedBy ...string) boardtask.Task {
	return boardtask.Task{
		ID:        id,
		Name:      "Task " + id,
		Status:    boardtask.StatusInbox,
		CreatedAt: "2026-07-01T00:00:00Z",
		UpdatedAt: "2026-07-01T00:00:00Z",
		BlockedBy: blockedBy,
	}
}

// ---- ValidateBlockedBy tests -------------------------------------------------

// TestBlockedBy_SelfEdge verifies that a task cannot list itself in blocked_by.
func TestBlockedBy_SelfEdge(t *testing.T) {
	graph := taskMap{}
	err := boardtask.ValidateBlockedBy("A", []string{"A"}, graph.load)
	if err == nil {
		t.Fatal("expected error for self-edge, got nil")
	}
	if err != boardtask.ErrBlockedBySelfEdge {
		t.Fatalf("expected ErrBlockedBySelfEdge, got: %v", err)
	}
}

// TestBlockedBy_TwoNodeCycle verifies that a direct 2-node cycle A→B, B→A is rejected.
func TestBlockedBy_TwoNodeCycle(t *testing.T) {
	// Existing graph: B is already blocked_by A.
	graph := taskMap{
		"A": makeTask("A"),
		"B": makeTask("B", "A"), // B is blocked_by A
	}
	// Now propose A blocked_by B — creates A↔B cycle.
	err := boardtask.ValidateBlockedBy("A", []string{"B"}, graph.load)
	if err == nil {
		t.Fatal("expected cycle error for A←→B, got nil")
	}
	t.Logf("correctly rejected 2-node cycle: %v", err)
}

// TestBlockedBy_NNodeCycle verifies that an N-node cycle A→B→C→A is rejected.
func TestBlockedBy_NNodeCycle(t *testing.T) {
	// Existing graph: B is blocked_by C; C is blocked_by A.
	graph := taskMap{
		"A": makeTask("A"),
		"B": makeTask("B", "C"), // B is blocked_by C
		"C": makeTask("C", "A"), // C is blocked_by A
	}
	// Propose A blocked_by B — creates A→B→C→A cycle (3-node).
	err := boardtask.ValidateBlockedBy("A", []string{"B"}, graph.load)
	if err == nil {
		t.Fatal("expected cycle error for A→B→C→A, got nil")
	}
	t.Logf("correctly rejected 3-node cycle: %v", err)
}

// TestBlockedBy_FourNodeCycle verifies N-node detection for N=4.
func TestBlockedBy_FourNodeCycle(t *testing.T) {
	// A→B→C→D→A cycle
	graph := taskMap{
		"A": makeTask("A"),
		"B": makeTask("B", "A"),
		"C": makeTask("C", "B"),
		"D": makeTask("D", "C"),
	}
	// Propose A blocked_by D — would create cycle A→B→C→D→A.
	err := boardtask.ValidateBlockedBy("A", []string{"D"}, graph.load)
	if err == nil {
		t.Fatal("expected cycle error for A→B→C→D→A, got nil")
	}
	t.Logf("correctly rejected 4-node cycle: %v", err)
}

// TestBlockedBy_ValidDAG verifies that a valid (acyclic) blocked_by is accepted.
func TestBlockedBy_ValidDAG(t *testing.T) {
	// A is blocked_by B; B is blocked_by C. We add A blocked_by C (diamond) — still acyclic.
	graph := taskMap{
		"A": makeTask("A", "B"),
		"B": makeTask("B", "C"),
		"C": makeTask("C"),
	}
	// Propose A blocked_by [B, C] — both valid, no cycle.
	err := boardtask.ValidateBlockedBy("A", []string{"B", "C"}, graph.load)
	if err != nil {
		t.Fatalf("expected nil for valid DAG, got: %v", err)
	}
}

// TestBlockedBy_EmptyList verifies that an empty blocked_by is accepted without calling the loader.
func TestBlockedBy_EmptyList(t *testing.T) {
	called := false
	loader := func(_ string) (boardtask.Task, error) {
		called = true
		return boardtask.Task{}, nil
	}
	err := boardtask.ValidateBlockedBy("A", nil, loader)
	if err != nil {
		t.Fatalf("expected nil for empty list, got: %v", err)
	}
	if called {
		t.Fatal("loader should not be called for empty blocked_by")
	}
}

// TestBlockedBy_OrphanReference verifies that a blocked_by entry referencing a
// non-existent task is not treated as a cycle (the reference is simply a dangling
// edge that DropOrphanEdges will later clean up).
func TestBlockedBy_OrphanReference(t *testing.T) {
	graph := taskMap{
		"A": makeTask("A"),
	}
	// B doesn't exist; referencing it should not be a cycle.
	err := boardtask.ValidateBlockedBy("A", []string{"B"}, graph.load)
	if err != nil {
		t.Fatalf("expected nil for orphan ref (non-existent dep), got: %v", err)
	}
}

// TestBlockedBy_DepthExceeded verifies that a chain exceeding maxBlockedByDepth is rejected.
func TestBlockedBy_DepthExceeded(t *testing.T) {
	// Build a chain of 52 tasks: Z50→Z49→...→Z1→Z0.
	// Then propose task "ROOT" blocked_by ["Z50"] which creates a chain of 51 hops.
	graph := taskMap{}
	const chainLen = 51 // depth 51 > maxBlockedByDepth(50)
	prev := ""
	for i := 0; i <= chainLen; i++ {
		id := fmt.Sprintf("Z%02d", i)
		var deps []string
		if prev != "" {
			deps = []string{prev}
		}
		graph[id] = makeTask(id, deps...)
		prev = id
	}
	// Propose ROOT blocked_by the tail of the chain — total depth = chainLen+1 > 50.
	err := boardtask.ValidateBlockedBy("ROOT", []string{prev}, graph.load)
	if err == nil {
		t.Fatal("expected ErrBlockedByDepthExceeded, got nil")
	}
	if err != boardtask.ErrBlockedByDepthExceeded {
		t.Fatalf("expected ErrBlockedByDepthExceeded, got: %v", err)
	}
}

// TestBlockedBy_DepthExactLimit verifies that a chain exactly at the limit is accepted.
func TestBlockedBy_DepthExactLimit(t *testing.T) {
	// Build a chain of exactly 50 tasks (depth = 50 = maxBlockedByDepth).
	graph := taskMap{}
	const chainLen = 49 // depth 49 ≤ 50
	prev := ""
	for i := 0; i <= chainLen; i++ {
		id := fmt.Sprintf("Y%02d", i)
		var deps []string
		if prev != "" {
			deps = []string{prev}
		}
		graph[id] = makeTask(id, deps...)
		prev = id
	}
	// Propose ROOT blocked_by the tail — total depth ≤ 50.
	err := boardtask.ValidateBlockedBy("ROOT", []string{prev}, graph.load)
	if err != nil {
		t.Fatalf("expected nil for chain at limit, got: %v", err)
	}
}

// ---- DropOrphanEdges tests ---------------------------------------------------

// TestDropOrphanEdges_RemovesOrphans verifies that blocked_by entries pointing to
// non-existent task files are removed.
func TestDropOrphanEdges_RemovesOrphans(t *testing.T) {
	// Task A is blocked_by B and C. B exists, C does not.
	tasks := []boardtask.Task{
		makeTask("A", "B", "C"),
		makeTask("B"),
	}
	dir := writeTasks(t, tasks)

	removed, err := boardtask.DropOrphanEdges(dir)
	if err != nil {
		t.Fatalf("DropOrphanEdges: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 orphan removed, got %d", removed)
	}

	// A should now have only B in blocked_by.
	a := readTask(t, dir, "A")
	if len(a.BlockedBy) != 1 || a.BlockedBy[0] != "B" {
		t.Fatalf("expected blocked_by=[B], got %v", a.BlockedBy)
	}
}

// TestDropOrphanEdges_NoOrphans verifies that tasks with only valid refs are unchanged.
func TestDropOrphanEdges_NoOrphans(t *testing.T) {
	tasks := []boardtask.Task{
		makeTask("A", "B"),
		makeTask("B"),
	}
	dir := writeTasks(t, tasks)

	removed, err := boardtask.DropOrphanEdges(dir)
	if err != nil {
		t.Fatalf("DropOrphanEdges: %v", err)
	}
	if removed != 0 {
		t.Fatalf("expected 0 orphans removed, got %d", removed)
	}
}

// TestDropOrphanEdges_AllOrphans verifies that when all blocked_by targets are gone,
// blocked_by becomes nil/empty.
func TestDropOrphanEdges_AllOrphans(t *testing.T) {
	tasks := []boardtask.Task{
		makeTask("A", "B", "C"), // B and C both absent
	}
	dir := writeTasks(t, tasks)

	removed, err := boardtask.DropOrphanEdges(dir)
	if err != nil {
		t.Fatalf("DropOrphanEdges: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected 2 orphans removed, got %d", removed)
	}
	a := readTask(t, dir, "A")
	if len(a.BlockedBy) != 0 {
		t.Fatalf("expected blocked_by empty, got %v", a.BlockedBy)
	}
}

// TestDropOrphanEdges_NonGTDSkipped verifies that workflow tasks (non-GTD status) are not modified.
func TestDropOrphanEdges_NonGTDSkipped(t *testing.T) {
	tasks := []boardtask.Task{
		// Workflow task with blocked_by (should not be touched).
		{
			ID:        "W1",
			Name:      "workflow task",
			Status:    "queued", // not a GTD status
			BlockedBy: []string{"MISSING"},
			CreatedAt: "2026-07-01T00:00:00Z",
			UpdatedAt: "2026-07-01T00:00:00Z",
		},
	}
	dir := writeTasks(t, tasks)

	removed, err := boardtask.DropOrphanEdges(dir)
	if err != nil {
		t.Fatalf("DropOrphanEdges: %v", err)
	}
	if removed != 0 {
		t.Fatalf("expected 0 removed (workflow tasks skipped), got %d", removed)
	}
	// File should be unchanged.
	w1 := readTask(t, dir, "W1")
	if len(w1.BlockedBy) != 1 || w1.BlockedBy[0] != "MISSING" {
		t.Fatalf("expected workflow task unchanged, got blocked_by=%v", w1.BlockedBy)
	}
}

// TestDropOrphanEdges_EmptyDir verifies graceful handling of a non-existent directory.
func TestDropOrphanEdges_EmptyDir(t *testing.T) {
	removed, err := boardtask.DropOrphanEdges("/non-existent-path-xyz-abc")
	if err != nil {
		t.Fatalf("expected nil for absent dir, got: %v", err)
	}
	if removed != 0 {
		t.Fatalf("expected 0 removed, got %d", removed)
	}
}

// ---- CascadeDeleteEdges tests ------------------------------------------------

// TestCascadeDeleteEdges_RemovesEdge verifies that the deleted task ID is removed
// from other tasks' blocked_by lists.
func TestCascadeDeleteEdges_RemovesEdge(t *testing.T) {
	// A is blocked_by B; C is blocked_by B and D.
	// Delete B: A and C should lose B from their blocked_by lists.
	tasks := []boardtask.Task{
		makeTask("A", "B"),
		makeTask("B"),
		makeTask("C", "B", "D"),
		makeTask("D"),
	}
	dir := writeTasks(t, tasks)

	// Delete B (remove the file to simulate the delete path).
	if err := os.Remove(filepath.Join(dir, "B.json")); err != nil {
		t.Fatal(err)
	}

	unblocked, err := boardtask.CascadeDeleteEdges(dir, "B")
	if err != nil {
		t.Fatalf("CascadeDeleteEdges: %v", err)
	}

	// A should be fully unblocked (blocked_by was [B], now empty).
	// C should still be blocked by D (blocked_by=[D] after removal of B).
	sort.Strings(unblocked)
	if len(unblocked) != 1 || unblocked[0] != "A" {
		t.Fatalf("expected unblocked=[A], got %v", unblocked)
	}

	a := readTask(t, dir, "A")
	if len(a.BlockedBy) != 0 {
		t.Fatalf("expected A.blocked_by empty after cascade, got %v", a.BlockedBy)
	}

	c := readTask(t, dir, "C")
	if len(c.BlockedBy) != 1 || c.BlockedBy[0] != "D" {
		t.Fatalf("expected C.blocked_by=[D], got %v", c.BlockedBy)
	}
}

// TestCascadeDeleteEdges_NoRefs verifies that cascade is a no-op when no other task
// references the deleted ID.
func TestCascadeDeleteEdges_NoRefs(t *testing.T) {
	tasks := []boardtask.Task{
		makeTask("A", "B"),
		makeTask("B"),
	}
	dir := writeTasks(t, tasks)

	// Simulate deleting A (which no other task references).
	if err := os.Remove(filepath.Join(dir, "A.json")); err != nil {
		t.Fatal(err)
	}

	unblocked, err := boardtask.CascadeDeleteEdges(dir, "A")
	if err != nil {
		t.Fatalf("CascadeDeleteEdges: %v", err)
	}
	if len(unblocked) != 0 {
		t.Fatalf("expected no unblocked tasks, got %v", unblocked)
	}
}

// TestCascadeDeleteEdges_MultipleUnblocked verifies that when the deleted task
// was the sole blocker for multiple tasks, all are returned as unblocked.
func TestCascadeDeleteEdges_MultipleUnblocked(t *testing.T) {
	// X, Y, Z are all solely blocked by "PIVOT".
	tasks := []boardtask.Task{
		makeTask("X", "PIVOT"),
		makeTask("Y", "PIVOT"),
		makeTask("Z", "PIVOT"),
		makeTask("PIVOT"),
	}
	dir := writeTasks(t, tasks)

	if err := os.Remove(filepath.Join(dir, "PIVOT.json")); err != nil {
		t.Fatal(err)
	}

	unblocked, err := boardtask.CascadeDeleteEdges(dir, "PIVOT")
	if err != nil {
		t.Fatalf("CascadeDeleteEdges: %v", err)
	}
	sort.Strings(unblocked)
	if len(unblocked) != 3 {
		t.Fatalf("expected 3 unblocked tasks, got %v", unblocked)
	}
	for i, want := range []string{"X", "Y", "Z"} {
		if unblocked[i] != want {
			t.Fatalf("unblocked[%d]: want %s, got %s", i, want, unblocked[i])
		}
	}
}

// TestCascadeDeleteEdges_EmptyDir verifies graceful handling of absent directory.
func TestCascadeDeleteEdges_EmptyDir(t *testing.T) {
	unblocked, err := boardtask.CascadeDeleteEdges("/non-existent-path-xyz-abc", "X")
	if err != nil {
		t.Fatalf("expected nil for absent dir, got: %v", err)
	}
	if len(unblocked) != 0 {
		t.Fatalf("expected empty, got %v", unblocked)
	}
}

// ---- Spec-5 field persistence test ------------------------------------------

// TestTask_Spec5FieldsPersist verifies that the 4 new Spec-5 fields (start, due,
// recurrence, blocked_by) survive a JSON round-trip (marshal → unmarshal).
func TestTask_Spec5FieldsPersist(t *testing.T) {
	original := boardtask.Task{
		ID:         "SPEC5TASK",
		Name:       "Spec-5 test task",
		Status:     boardtask.StatusNext,
		CreatedAt:  "2026-07-01T00:00:00Z",
		UpdatedAt:  "2026-07-01T00:00:00Z",
		Start:      "2026-07-01T09:00:00Z",
		Due:        "2026-07-31T17:00:00Z",
		Recurrence: "FREQ=WEEKLY;BYDAY=MO",
		BlockedBy:  []string{"A", "B"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var loaded boardtask.Task
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if loaded.Start != original.Start {
		t.Errorf("start: got %q, want %q", loaded.Start, original.Start)
	}
	if loaded.Due != original.Due {
		t.Errorf("due: got %q, want %q", loaded.Due, original.Due)
	}
	if loaded.Recurrence != original.Recurrence {
		t.Errorf("recurrence: got %q, want %q", loaded.Recurrence, original.Recurrence)
	}
	if len(loaded.BlockedBy) != 2 || loaded.BlockedBy[0] != "A" || loaded.BlockedBy[1] != "B" {
		t.Errorf("blocked_by: got %v, want [A B]", loaded.BlockedBy)
	}
}
