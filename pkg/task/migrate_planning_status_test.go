// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTaskFixtureWithStatus writes a raw task JSON literal with the given
// status string (bypassing task.Task/Status entirely, so a value like
// "planning" — no longer a member of the live Status vocabulary — can still
// be written to disk exactly as a pre-ADR-051 install would have it).
func writeTaskFixtureWithStatus(t *testing.T, tasksDir, id, status string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	raw := map[string]any{
		"id":           id,
		"title":        "Task " + id,
		"action":       "llm",
		"status":       status,
		"workspace_id": "ws-1",
		"created_at":   "2026-01-01T00:00:00Z",
		"updated_at":   "2026-01-01T00:00:00Z",
	}
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, id+".json"), data, 0o600))
}

// TestMigratePlanningStatus_RemapsToNext verifies the core ADR-051 D5
// backfill: a task on disk with status="planning" (from a pre-migration
// install) is rewritten to status="next" after MigratePlanningStatusToNext
// runs, and the completion sentinel is written.
func TestMigratePlanningStatus_RemapsToNext(t *testing.T) {
	home := t.TempDir()
	tasksDir := filepath.Join(home, "tasks")
	writeTaskFixtureWithStatus(t, tasksDir, "t1", "planning")

	require.NoError(t, MigratePlanningStatusToNext(home))

	tk := readTaskFixture(t, tasksDir, "t1")
	assert.Equal(t, StatusNext, tk.Status, "planning must be remapped to next")

	_, err := os.Stat(filepath.Join(home, planningStatusMigrationSentinelFile))
	assert.NoError(t, err, "completion sentinel must be written")
}

// TestMigratePlanningStatus_OtherStatusesUntouched verifies the migration
// only rewrites tasks whose status is exactly "planning" — every other
// canonical status (and the file's other fields) survive unchanged.
func TestMigratePlanningStatus_OtherStatusesUntouched(t *testing.T) {
	home := t.TempDir()
	tasksDir := filepath.Join(home, "tasks")
	statuses := []string{"inbox", "next", "in_progress", "blocked", "done", "failed"}
	for _, s := range statuses {
		writeTaskFixtureWithStatus(t, tasksDir, "t-"+s, s)
	}

	require.NoError(t, MigratePlanningStatusToNext(home))

	for _, s := range statuses {
		tk := readTaskFixture(t, tasksDir, "t-"+s)
		assert.Equal(t, Status(s), tk.Status, "status %q must be left untouched", s)
	}
}

// TestMigratePlanningStatus_IdempotentSecondRun verifies re-running the
// migration after the sentinel exists is a no-op: it does not error and does
// not re-touch already-migrated files (a task manually reset to "planning"
// after the first run — simulating operator data, not a real re-migration —
// is left alone once the sentinel is present).
func TestMigratePlanningStatus_IdempotentSecondRun(t *testing.T) {
	home := t.TempDir()
	tasksDir := filepath.Join(home, "tasks")
	writeTaskFixtureWithStatus(t, tasksDir, "t1", "planning")

	require.NoError(t, MigratePlanningStatusToNext(home))
	tk := readTaskFixture(t, tasksDir, "t1")
	require.Equal(t, StatusNext, tk.Status)

	// Simulate an operator/tool writing "planning" back onto the file after
	// the migration completed — the sentinel means a second run must be a
	// pure no-op (proves the short-circuit, not a re-scan).
	writeTaskFixtureWithStatus(t, tasksDir, "t1", "planning")
	require.NoError(t, MigratePlanningStatusToNext(home))

	tk = readTaskFixture(t, tasksDir, "t1")
	assert.Equal(t, Status("planning"), tk.Status,
		"second run must be a short-circuit no-op once the sentinel exists")
}

// TestMigratePlanningStatus_NoTasksDir verifies a fresh install (no tasks/
// directory yet) is handled gracefully: the migration writes the sentinel and
// returns no error rather than failing on ENOENT.
func TestMigratePlanningStatus_NoTasksDir(t *testing.T) {
	home := t.TempDir()

	require.NoError(t, MigratePlanningStatusToNext(home))

	_, err := os.Stat(filepath.Join(home, planningStatusMigrationSentinelFile))
	assert.NoError(t, err, "completion sentinel must be written even with no tasks dir")
}

// TestMigratePlanningStatus_CorruptFileSkippedNotFatal verifies a single
// corrupt/unreadable task file does not abort the whole migration run, and
// (mirroring MigrateMilestonesToTags's Phase-3 discipline) the sentinel is
// NOT written when a file fails, so the run retries on the next boot.
func TestMigratePlanningStatus_CorruptFileSkippedNotFatal(t *testing.T) {
	home := t.TempDir()
	tasksDir := filepath.Join(home, "tasks")
	writeTaskFixtureWithStatus(t, tasksDir, "good", "planning")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, "corrupt.json"), []byte("{not json"), 0o600))

	err := MigratePlanningStatusToNext(home)
	require.Error(t, err, "a corrupt file must surface as an error (blocking sentinel finalization)")

	_, statErr := os.Stat(filepath.Join(home, planningStatusMigrationSentinelFile))
	assert.True(t, os.IsNotExist(statErr), "sentinel must NOT be written when a file failed to migrate")

	// The well-formed file must still have been migrated even though the
	// corrupt one failed (per-file isolation).
	tk := readTaskFixture(t, tasksDir, "good")
	assert.Equal(t, StatusNext, tk.Status, "well-formed task must still be remapped despite a sibling failure")
}
