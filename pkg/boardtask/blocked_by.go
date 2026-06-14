// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package boardtask — blocked_by DAG validator (Spec-5, FR-8.2).
//
// Write-time DAG validation for the blocked_by field:
//   - Rejects self-edges (a task cannot be blocked by itself).
//   - Rejects 2-node and N-node cycles (DFS/topo over the full task graph).
//   - Enforces a depth bound of maxBlockedByDepth (50) on the longest path
//     from the updated task through the dependency chain, so the Orchestrator
//     (Spec-3) traversal on every status-change is bounded.
//
// Load-time orphan cleanup:
//   - DropOrphanEdges scans all task files in a directory, removes any
//     blocked_by entries whose target task file no longer exists, and
//     atomically rewrites only the tasks that changed.
//
// Delete-time cascade:
//   - CascadeDeleteEdges removes the deleted task ID from the blocked_by
//     list of every other task (both inbound and outbound edges), atomically
//     rewrites changed task files, and returns the IDs of tasks that are now
//     fully unblocked (their blocked_by list became empty).

package boardtask

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// maxBlockedByDepth is the maximum allowed depth of the blocked_by dependency
// chain. A chain deeper than this is rejected at write time to bound the
// Orchestrator's traversal cost on every task_status_changed event (Spec-3).
const maxBlockedByDepth = 50

// ErrBlockedByCycle is returned when a blocked_by update would create a cycle
// in the dependency DAG (self-edge, 2-node, or N-node cycle).
var ErrBlockedByCycle = errors.New("blocked_by: cycle detected")

// ErrBlockedByDepthExceeded is returned when the dependency chain exceeds
// maxBlockedByDepth hops.
var ErrBlockedByDepthExceeded = errors.New("blocked_by: dependency chain too deep (max 50)")

// ErrBlockedBySelfEdge is returned when a task lists itself in blocked_by.
var ErrBlockedBySelfEdge = errors.New("blocked_by: task cannot be blocked by itself")

// TaskLoader is a function that loads a single task by ID from disk.
// Returns os.ErrNotExist when the task file is absent or the file is not a
// GTD task. Used by the DAG validator so it can traverse the full graph
// without importing pkg/gateway (avoiding circular imports).
type TaskLoader func(id string) (Task, error)

// ValidateBlockedBy validates a proposed blocked_by update for the task
// identified by taskID.
//
// It checks:
//  1. No self-edge (taskID must not appear in newBlockedBy).
//  2. No cycle of any length (DFS from each entry in newBlockedBy back
//     through the dependency graph — if taskID is reachable, there is a cycle).
//  3. Depth does not exceed maxBlockedByDepth (longest path from taskID
//     forward through the new graph).
//
// loadTask is called for each task encountered during graph traversal; it may
// return os.ErrNotExist for orphaned references (those are skipped, not
// counted as cycles).
//
// Returns nil when the proposed update is valid.
func ValidateBlockedBy(taskID string, newBlockedBy []string, loadTask TaskLoader) error {
	if len(newBlockedBy) == 0 {
		return nil
	}

	// Step 1: reject self-edge.
	for _, dep := range newBlockedBy {
		if dep == taskID {
			return ErrBlockedBySelfEdge
		}
	}

	// Build the proposed graph: after this update, taskID would have newBlockedBy
	// as its predecessors. We need to detect if any predecessor can reach taskID
	// through the existing graph (which would form a cycle).
	//
	// DFS: for each entry in newBlockedBy, traverse its blocked_by graph to
	// see whether taskID appears anywhere reachable. If it does, the proposed
	// edge creates a cycle.
	visited := make(map[string]bool)
	for _, dep := range newBlockedBy {
		if err := detectCycleDFS(dep, taskID, loadTask, visited, 0); err != nil {
			return err
		}
	}

	// Step 3: check that the depth from taskID through newBlockedBy does not
	// exceed maxBlockedByDepth. We DFS forward through the dependency chain
	// (taskID is blocked by its predecessors, so depth = how many hops back
	// from taskID we can go).
	//
	// Because cycles are already ruled out above, this terminates.
	depth := forwardDepth(newBlockedBy, loadTask, 0, make(map[string]bool))
	if depth > maxBlockedByDepth {
		return ErrBlockedByDepthExceeded
	}

	return nil
}

// detectCycleDFS performs a DFS starting from startID, looking for targetID.
// If targetID is found reachable from startID via the blocked_by graph,
// it means adding an edge from targetID→startID would create a cycle, so
// it returns ErrBlockedByCycle.
//
// visited tracks nodes already explored in this DFS run (across all start
// nodes) to avoid re-visiting. depth is used to enforce maxBlockedByDepth
// even during cycle detection (guards against extremely deep graphs before
// the depth check proper).
func detectCycleDFS(startID, targetID string, loadTask TaskLoader, visited map[string]bool, depth int) error {
	if depth > maxBlockedByDepth+1 {
		// Depth exceeded even without a cycle — the depth check below will
		// catch this properly; skip further traversal.
		return nil
	}
	if visited[startID] {
		return nil
	}
	visited[startID] = true

	task, err := loadTask(startID)
	if err != nil {
		// Orphan reference — skip (not a cycle; DropOrphanEdges cleans these up).
		return nil
	}

	for _, dep := range task.BlockedBy {
		if dep == targetID {
			return fmt.Errorf("%w: %q is reachable from %q through blocked_by", ErrBlockedByCycle, targetID, startID)
		}
		if err := detectCycleDFS(dep, targetID, loadTask, visited, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// forwardDepth returns the depth of the longest dependency chain reachable
// from the given set of predecessor IDs, bounded by maxBlockedByDepth+1 to
// avoid infinite loops (cycles should already be rejected, but we guard anyway).
//
// depth is the current recursion depth; seen tracks visited nodes to avoid
// re-traversal in DAGs with shared ancestors.
func forwardDepth(ids []string, loadTask TaskLoader, depth int, seen map[string]bool) int {
	if depth > maxBlockedByDepth {
		return depth
	}
	max := depth
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		task, err := loadTask(id)
		if err != nil {
			continue
		}
		if d := forwardDepth(task.BlockedBy, loadTask, depth+1, seen); d > max {
			max = d
		}
	}
	return max
}

// DropOrphanEdges scans every GTD task file in tasksDir and removes any
// blocked_by entries whose target task file no longer exists on disk. Only
// GTD tasks (files with a known GTD status and a non-empty name) are
// processed. Tasks whose blocked_by lists change are rewritten atomically.
//
// This is safe to call on startup, after a delete, or during periodic cleanup.
// It acquires the per-task striped lock for each task it rewrites.
//
// Returns the number of orphan edges removed and any I/O error encountered
// (non-fatal orphan removal errors are logged and skipped).
func DropOrphanEdges(tasksDir string) (int, error) {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("boardtask: DropOrphanEdges: ReadDir: %w", err)
	}

	// Build the set of known task IDs first (one pass).
	knownIDs := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		knownIDs[name[:len(name)-5]] = true
	}

	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		id := name[:len(name)-5]
		path := filepath.Join(tasksDir, name)

		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("boardtask: DropOrphanEdges: skipping unreadable file", "file", name, "error", err)
			continue
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			slog.Warn("boardtask: DropOrphanEdges: skipping corrupt file", "file", name, "error", err)
			continue
		}
		// Only process GTD tasks.
		if t.Name == "" || !IsGTDStatus(string(t.Status)) {
			continue
		}

		// Filter blocked_by to only known IDs.
		clean := t.BlockedBy[:0]
		for _, dep := range t.BlockedBy {
			if knownIDs[dep] {
				clean = append(clean, dep)
			} else {
				removed++
				slog.Info("boardtask: dropping orphan blocked_by edge", "task_id", id, "missing_dep", dep)
			}
		}
		if len(clean) == len(t.BlockedBy) {
			continue // nothing changed
		}
		t.BlockedBy = clean
		if len(t.BlockedBy) == 0 {
			t.BlockedBy = nil
		}

		newData, err := json.MarshalIndent(t, "", "  ")
		if err != nil {
			slog.Warn("boardtask: DropOrphanEdges: marshal failed", "file", name, "error", err)
			continue
		}
		mu := TaskFileLock.Get(id)
		mu.Lock()
		writeErr := fileutil.WriteFileAtomic(path, newData, 0o600)
		mu.Unlock()
		if writeErr != nil {
			slog.Warn("boardtask: DropOrphanEdges: write failed", "file", name, "error", writeErr)
		}
	}
	return removed, nil
}

// CascadeDeleteEdges removes deletedID from the blocked_by list of every
// other GTD task in tasksDir. It atomically rewrites each changed task file
// using the per-task striped lock.
//
// Returns the IDs of tasks that were waiting on deletedID and are now fully
// unblocked (their blocked_by list became empty after the removal), and any
// I/O error encountered during the scan. Partial errors (individual task
// rewrite failures) are logged and skipped rather than aborting the cascade.
//
// This MUST be called after the target task file has been deleted so the
// DropOrphanEdges logic would also clean these up, but calling CascadeDeleteEdges
// at delete time is faster and surfaces the newly-unblocked task IDs for
// the caller to act on (e.g. the Orchestrator emitting task_status_changed).
func CascadeDeleteEdges(tasksDir, deletedID string) (unblockedIDs []string, err error) {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("boardtask: CascadeDeleteEdges: ReadDir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		id := name[:len(name)-5]
		if id == deletedID {
			continue // skip the file we just deleted
		}
		path := filepath.Join(tasksDir, name)

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			slog.Warn("boardtask: CascadeDeleteEdges: skipping unreadable file", "file", name, "error", readErr)
			continue
		}
		var t Task
		if jsonErr := json.Unmarshal(data, &t); jsonErr != nil {
			slog.Warn("boardtask: CascadeDeleteEdges: skipping corrupt file", "file", name, "error", jsonErr)
			continue
		}
		// Only process GTD tasks that reference deletedID.
		if t.Name == "" || !IsGTDStatus(string(t.Status)) {
			continue
		}

		found := false
		for _, dep := range t.BlockedBy {
			if dep == deletedID {
				found = true
				break
			}
		}
		if !found {
			continue
		}

		// Remove deletedID from the list.
		newDeps := make([]string, 0, len(t.BlockedBy)-1)
		for _, dep := range t.BlockedBy {
			if dep != deletedID {
				newDeps = append(newDeps, dep)
			}
		}
		t.BlockedBy = newDeps
		if len(t.BlockedBy) == 0 {
			t.BlockedBy = nil
		}

		newData, marshalErr := json.MarshalIndent(t, "", "  ")
		if marshalErr != nil {
			slog.Warn("boardtask: CascadeDeleteEdges: marshal failed", "file", name, "error", marshalErr)
			continue
		}
		mu := TaskFileLock.Get(id)
		mu.Lock()
		writeErr := fileutil.WriteFileAtomic(path, newData, 0o600)
		mu.Unlock()
		if writeErr != nil {
			slog.Warn("boardtask: CascadeDeleteEdges: write failed", "file", name, "error", writeErr)
			continue
		}
		slog.Info("boardtask: cascade-cleaned blocked_by edge", "task_id", id, "deleted_dep", deletedID)
		if len(t.BlockedBy) == 0 {
			unblockedIDs = append(unblockedIDs, id)
		}
	}
	return unblockedIDs, nil
}
