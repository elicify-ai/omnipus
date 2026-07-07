// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// blocked_by DAG validator + auto-advance, carried over from pkg/boardtask and
// re-cast onto the unified store and 7-state vocabulary.
//
//   - Write-time validation rejects self-edges, 2-node and N-node cycles, and
//     references to missing tasks; bounds the chain at maxBlockedByDepth (50).
//   - Auto-advance (AdvanceBlockedDependents) transitions a dependent from
//     `blocked` → `next` once every task in its blocked_by reaches `done`.
//   - CascadeDeleteEdges drops a deleted task's ID from every other task's
//     blocked_by and reports the tasks that became fully unblocked (every
//     remaining blocker done, not merely an emptied list).
//   - DropOrphanEdges removes blocked_by entries whose target file is gone. It
//     is run explicitly on boot via the gateway's reconcileOrphanBlockedByEdges
//     (NOT implicitly on every load).

package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// maxBlockedByDepth bounds the longest blocked_by chain so the auto-advance
// traversal on every status change is bounded.
const maxBlockedByDepth = 50

// ErrBlockedByCycle is returned when a blocked_by update would create a cycle.
// It wraps ErrValidation so the REST seam maps it to HTTP 400 via errors.Is.
var ErrBlockedByCycle = fmt.Errorf("%w: blocked_by cycle detected", ErrValidation)

// ErrBlockedByDepthExceeded is returned when the chain exceeds maxBlockedByDepth.
var ErrBlockedByDepthExceeded = fmt.Errorf("%w: blocked_by dependency chain too deep (max 50)", ErrValidation)

// ErrBlockedBySelfEdge is returned when a task lists itself in blocked_by.
var ErrBlockedBySelfEdge = fmt.Errorf("%w: task cannot be blocked by itself", ErrValidation)

// ErrParentCycle is returned when a parent_task_id edge would create a cycle.
var ErrParentCycle = fmt.Errorf("%w: parent_task_id edge would create a cycle", ErrValidation)

// maxParentDepth bounds the parent chain walk.
const maxParentDepth = 50

// validateBlockedByLocked validates a proposed blocked_by set for taskID. The
// caller must hold the per-task lock for taskID. It rejects self-edges, missing
// dependencies, cycles of any length, and over-deep chains.
func (s *Store) validateBlockedByLocked(taskID string, newBlockedBy []string) error {
	if len(newBlockedBy) == 0 {
		return nil
	}
	for _, dep := range newBlockedBy {
		if dep == taskID {
			return ErrBlockedBySelfEdge
		}
		if err := validateID(dep); err != nil {
			return fmt.Errorf("%w: blocked_by contains invalid ID %q: %v", ErrValidation, dep, err)
		}
		if _, err := s.load(dep); err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: blocked_by task %q not found", ErrValidation, dep)
			}
			return fmt.Errorf("blocked_by: could not verify task %q: %w", dep, err)
		}
	}

	// Cycle detection: if taskID is reachable from any proposed dep through the
	// existing blocked_by graph, the new edge closes a cycle.
	visited := make(map[string]bool)
	for _, dep := range newBlockedBy {
		if err := s.detectCycleDFS(dep, taskID, visited, 0); err != nil {
			return err
		}
	}

	// Depth bound: longest path from taskID forward through the new graph.
	depth := s.forwardDepth(newBlockedBy, 0, make(map[string]bool))
	if depth > maxBlockedByDepth {
		return ErrBlockedByDepthExceeded
	}
	return nil
}

// detectCycleDFS searches the blocked_by graph from startID for targetID.
func (s *Store) detectCycleDFS(startID, targetID string, visited map[string]bool, depth int) error {
	if depth > maxBlockedByDepth+1 {
		return nil
	}
	if visited[startID] {
		return nil
	}
	visited[startID] = true

	t, err := s.load(startID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // orphan reference — not a cycle
		}
		return fmt.Errorf("blocked_by: cycle check: loading task %q: %w", startID, err)
	}
	for _, dep := range t.BlockedBy {
		if dep == targetID {
			return fmt.Errorf("%w: %q is reachable from %q through blocked_by", ErrBlockedByCycle, targetID, startID)
		}
		if err := s.detectCycleDFS(dep, targetID, visited, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// forwardDepth returns the depth of the longest dependency chain reachable from
// the given predecessor IDs, bounded by maxBlockedByDepth.
func (s *Store) forwardDepth(ids []string, depth int, seen map[string]bool) int {
	if depth > maxBlockedByDepth {
		return depth
	}
	maxD := depth
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		t, err := s.load(id)
		if err != nil {
			continue
		}
		if d := s.forwardDepth(t.BlockedBy, depth+1, seen); d > maxD {
			maxD = d
		}
	}
	return maxD
}

// checkParentAcyclicLocked walks the parent chain from parentID and rejects the
// edge newID → parentID if it would form a cycle or exceed maxParentDepth, or
// if the parent does not exist. The caller must hold the per-task lock for newID.
func (s *Store) checkParentAcyclicLocked(newID, parentID string) error {
	if parentID == newID {
		return fmt.Errorf("%w: task %q cannot be its own parent", ErrParentCycle, newID)
	}
	if err := validateID(parentID); err != nil {
		return fmt.Errorf("parent_task_id invalid: %w", err)
	}
	visited := map[string]bool{newID: true}
	cur := parentID
	for depth := 0; cur != ""; depth++ {
		if depth >= maxParentDepth {
			return fmt.Errorf("%w: parent chain exceeds max depth %d", ErrParentCycle, maxParentDepth)
		}
		if visited[cur] {
			return fmt.Errorf("%w: parent edge %q → %q closes a loop at %q", ErrParentCycle, newID, parentID, cur)
		}
		visited[cur] = true
		parent, err := s.load(cur)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: parent task %q does not exist", ErrNotFound, cur)
			}
			return fmt.Errorf("task: walk parent chain at %q: %w", cur, err)
		}
		cur = parent.ParentTaskID
	}
	return nil
}

// AdvanceBlockedDependents transitions every task that (a) is currently
// `blocked`, (b) directly depends on completedID, and (c) has ALL of its
// blocked_by dependencies now `done`, from `blocked` → `next`. It returns the
// IDs advanced.
//
// The caller MUST NOT hold the per-task lock of any dependent. Each rewritten
// dependent is guarded by its own striped lock. Idempotent and cycle-safe.
func (s *Store) AdvanceBlockedDependents(completedID string) (advancedIDs []string, err error) {
	ids, err := s.scanTaskIDs()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("task: AdvanceBlockedDependents: ReadDir: %w", err)
	}

	statusCache := make(map[string]Status)
	var failedIDs []string // per-dependent write failures (surfaced as a non-nil err)
	readStatus := func(depID string) (Status, bool) {
		if st, ok := statusCache[depID]; ok {
			return st, true
		}
		dt, lerr := s.load(depID)
		if lerr != nil {
			return "", false
		}
		statusCache[depID] = dt.Status
		return dt.Status, true
	}

	for _, id := range ids {
		if id == completedID {
			continue
		}
		t, lerr := s.load(id)
		if lerr != nil {
			slog.Warn("task: AdvanceBlockedDependents: skipping unreadable file", "id", id, "error", lerr)
			continue
		}
		if t.Status != StatusBlocked || !containsString(t.BlockedBy, completedID) {
			continue
		}
		// Every dependency — including completedID — must actually be `done`.
		// We re-read completedID rather than assuming it, so a speculative or
		// out-of-order call cannot wrongly advance a still-blocked task.
		allDone := true
		for _, dep := range t.BlockedBy {
			st, ok := readStatus(dep)
			if !ok || st != StatusDone {
				allDone = false
				break
			}
		}
		if !allDone {
			continue
		}

		// Advance under the per-task lock; re-read to avoid clobbering a
		// concurrent mutation.
		mu := s.lock.Get(id)
		mu.Lock()
		fresh, reErr := s.load(id)
		if reErr != nil || fresh.Status != StatusBlocked {
			mu.Unlock()
			continue
		}
		fresh.Status = StatusNext
		fresh.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		writeErr := s.write(fresh)
		mu.Unlock()
		if writeErr != nil {
			// A per-dependent write fault orphans this dependent in `blocked`
			// with no auto-retry. Surface it (not just log) so callers can warn
			// the agent/operator — the advancedIDs still holds the ones that DID
			// advance. All callers already handle a non-nil error.
			slog.Warn("task: AdvanceBlockedDependents: write failed", "id", id, "error", writeErr)
			failedIDs = append(failedIDs, id)
			continue
		}
		slog.Info("task: advanced blocked dependent blocked→next", "task_id", id, "completed_dep", completedID)
		advancedIDs = append(advancedIDs, id)
	}
	if len(failedIDs) > 0 {
		return advancedIDs, fmt.Errorf(
			"task: AdvanceBlockedDependents: write failed for %d dependent(s): %v",
			len(failedIDs),
			failedIDs,
		)
	}
	return advancedIDs, nil
}

// cascadeDeleteEdges removes deletedID from the blocked_by list of every other
// task. It returns the IDs that became fully unblocked — meaning every REMAINING
// blocker is satisfied (the list is now empty OR every surviving blocker is
// already `done`), so the dependent is now dispatchable. Caller must already
// have removed the deleted task's own file.
func (s *Store) cascadeDeleteEdges(deletedID string) (unblockedIDs []string, err error) {
	ids, err := s.scanTaskIDs()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("task: cascadeDeleteEdges: ReadDir: %w", err)
	}
	// Cache blocker statuses so we don't re-read the same file for every dependent.
	statusCache := make(map[string]Status)
	depDone := func(depID string) bool {
		if st, ok := statusCache[depID]; ok {
			return st == StatusDone
		}
		dt, lerr := s.load(depID)
		if lerr != nil {
			statusCache[depID] = "" // missing/unreadable → treated as not-done
			return false
		}
		statusCache[depID] = dt.Status
		return dt.Status == StatusDone
	}
	for _, id := range ids {
		if id == deletedID {
			continue
		}
		t, lerr := s.load(id)
		if lerr != nil {
			slog.Warn("task: cascadeDeleteEdges: skipping unreadable file", "id", id, "error", lerr)
			continue
		}
		if !containsString(t.BlockedBy, deletedID) {
			continue
		}
		newDeps := make([]string, 0, len(t.BlockedBy)-1)
		for _, dep := range t.BlockedBy {
			if dep != deletedID {
				newDeps = append(newDeps, dep)
			}
		}
		mu := s.lock.Get(id)
		mu.Lock()
		fresh, reErr := s.load(id)
		if reErr != nil {
			mu.Unlock()
			continue
		}
		fresh.BlockedBy = newDeps
		if len(fresh.BlockedBy) == 0 {
			fresh.BlockedBy = nil
		}
		fresh.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		writeErr := s.write(fresh)
		mu.Unlock()
		if writeErr != nil {
			slog.Warn("task: cascadeDeleteEdges: write failed", "id", id, "error", writeErr)
			continue
		}
		slog.Info("task: cascade-cleaned blocked_by edge", "task_id", id, "deleted_dep", deletedID)
		// A dependent is fully unblocked when every REMAINING blocker is done
		// (vacuously true for an empty set). Reporting only on emptiness stranded
		// a multi-dep task whose other deps were already done (the bug).
		allRemainingDone := true
		for _, dep := range newDeps {
			if !depDone(dep) {
				allRemainingDone = false
				break
			}
		}
		if allRemainingDone {
			unblockedIDs = append(unblockedIDs, id)
		}
	}
	return unblockedIDs, nil
}

// DropOrphanEdges scans every task file and removes blocked_by entries whose
// target file no longer exists. Returns the number of orphan edges removed.
// Safe to call on startup.
func (s *Store) DropOrphanEdges() (int, error) {
	ids, err := s.scanTaskIDs()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("task: DropOrphanEdges: ReadDir: %w", err)
	}
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}

	removed := 0
	for _, id := range ids {
		fileName := id + ".json"
		path := s.path(id)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			slog.Warn("task: DropOrphanEdges: skipping unreadable file", "file", fileName, "error", rerr)
			continue
		}
		var t Task
		if jerr := json.Unmarshal(data, &t); jerr != nil {
			slog.Warn("task: DropOrphanEdges: skipping corrupt file", "file", fileName, "error", jerr)
			continue
		}
		clean := t.BlockedBy[:0]
		for _, dep := range t.BlockedBy {
			if known[dep] {
				clean = append(clean, dep)
			} else {
				removed++
				slog.Info("task: dropping orphan blocked_by edge", "task_id", id, "missing_dep", dep)
			}
		}
		if len(clean) == len(t.BlockedBy) {
			continue
		}
		t.BlockedBy = clean
		if len(t.BlockedBy) == 0 {
			t.BlockedBy = nil
		}
		newData, merr := json.MarshalIndent(&t, "", "  ")
		if merr != nil {
			slog.Warn("task: DropOrphanEdges: marshal failed", "file", fileName, "error", merr)
			continue
		}
		mu := s.lock.Get(id)
		mu.Lock()
		werr := fileutil.WithFlock(path, func() error {
			return fileutil.WriteFileAtomic(path, newData, 0o600)
		})
		mu.Unlock()
		if werr != nil {
			slog.Warn("task: DropOrphanEdges: write failed", "file", fileName, "error", werr)
		}
	}
	return removed, nil
}
