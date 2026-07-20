// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// migrate_planning_status.go implements the one-way `planning` -> `next` task
// status backfill (ADR-051 D5, docs/internal/architecture/ADR-051-tasks-screen-
// plans-as-filter.md). With Plans now first-class, the `planning` task status
// is redundant and removed from the live vocabulary (task.go's validStatuses).
// This migration runs once at task-store load (boot), guarded by a completion
// sentinel file, mirroring MigrateMilestonesToTags's discipline
// (migrate_milestones.go): idempotent and crash-safe — a per-file I/O error is
// logged at Warn and that file is skipped, and the sentinel is only written
// after every file that DID need remapping was rewritten successfully, so a
// partial-failure run retries deterministically on the next boot.
package task

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// planningStatusMigrationSentinelFile is the completion marker (ADR-051 D5),
// $OMNIPUS_HOME/.planning_status_migrated.
const planningStatusMigrationSentinelFile = ".planning_status_migrated"

// legacyPlanningStatus is the removed `planning` task status value. Kept as an
// unexported string literal — deliberately NOT a Status constant — because the
// whole point of this migration is that `planning` is no longer a member of
// the live vocabulary: it is absent from validStatuses and IsValidStatus
// rejects it.
const legacyPlanningStatus = "planning"

// MigratePlanningStatusToNext runs the one-time `planning` -> `next` task
// status backfill (ADR-051 D5). home is $OMNIPUS_HOME. Safe to call on every
// boot: once the sentinel exists it returns immediately after a single stat
// call, doing zero directory/file I/O.
//
// Any per-file I/O error is logged at Warn and that file is skipped — a
// single corrupt task file does not abort the whole migration.
func MigratePlanningStatusToNext(home string) error {
	sentinelPath := filepath.Join(home, planningStatusMigrationSentinelFile)
	if _, err := os.Stat(sentinelPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("task: planning-status migration: stat sentinel: %w", err)
	}

	tasksDir := filepath.Join(home, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return writeMigrationSentinel(sentinelPath)
		}
		return fmt.Errorf("task: planning-status migration: read tasks dir: %w", err)
	}

	var migrationFailures, migratedCount int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		migrated, mErr := migrateOneTaskPlanningStatus(filepath.Join(tasksDir, e.Name()))
		if mErr != nil {
			migrationFailures++
			slog.Warn("task: planning-status migration: could not migrate task file",
				"file", e.Name(), "error", mErr)
			continue
		}
		if migrated {
			migratedCount++
		}
	}

	// Only finalize (write the sentinel) once every file that needed remapping
	// was rewritten successfully — mirrors MigrateMilestonesToTags's Phase-3
	// discipline (migrate_milestones.go): a partial-failure run must NOT write
	// the sentinel, so the next boot retries deterministically rather than
	// permanently stranding a task on the removed `planning` status.
	if migrationFailures > 0 {
		return fmt.Errorf(
			"task: planning-status migration: %d task file(s) failed to migrate — sentinel NOT written, "+
				"will retry on next boot",
			migrationFailures,
		)
	}
	if err := writeMigrationSentinel(sentinelPath); err != nil {
		return err
	}
	slog.Info("task: planning-status migration: completed", "tasks_remapped", migratedCount)
	return nil
}

// migrateOneTaskPlanningStatus rewrites a single task file's status from the
// removed `planning` value to `next`, in place. Reads/writes raw JSON
// (map[string]json.RawMessage) rather than the Task struct so the migration
// only ever touches the one field it's responsible for, mirroring
// migrateOneTaskFile's discipline in migrate_milestones.go. Returns
// migrated=true only when the file's on-disk status WAS `planning`; a task
// with no status key, or any status other than `planning`, is left untouched
// (returns false without writing).
func migrateOneTaskPlanningStatus(path string) (migrated bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, fmt.Errorf("parse: %w", err)
	}
	statusRaw, ok := raw["status"]
	if !ok {
		return false, nil
	}
	var status string
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		return false, fmt.Errorf("parse status: %w", err)
	}
	if status != legacyPlanningStatus {
		return false, nil
	}
	nextJSON, err := json.Marshal(string(StatusNext))
	if err != nil {
		return false, fmt.Errorf("marshal status: %w", err)
	}
	raw["status"] = nextJSON
	if err := rewriteRawTaskFile(path, raw); err != nil {
		return false, fmt.Errorf("write: %w", err)
	}
	return true, nil
}
