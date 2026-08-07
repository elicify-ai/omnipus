// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeMilestoneFixture writes a legacy milestone JSON file directly (the
// on-disk shape the now-deleted pkg/gateway `milestone` struct used).
func writeMilestoneFixture(t *testing.T, milestonesDir, id, name, dueDate string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(milestonesDir, 0o700))
	m := map[string]any{
		"id":         id,
		"name":       name,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
	}
	if dueDate != "" {
		m["due_date"] = dueDate
	}
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(milestonesDir, id+".json"), data, 0o600))
}

// writeLegacyTaskFixture writes a RAW task JSON literal (bypassing task.Task
// entirely) carrying a legacy "milestone_id" key — proving the migration
// reads that key from raw JSON even though Task.MilestoneID no longer exists
// as a struct field (FR-032).
func writeLegacyTaskFixture(t *testing.T, tasksDir, id, workspaceID, milestoneID, due string, tags []string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	raw := map[string]any{
		"id":           id,
		"title":        "Task " + id,
		"action":       "llm",
		"status":       "inbox",
		"workspace_id": workspaceID,
		"milestone_id": milestoneID,
		"created_at":   "2026-01-01T00:00:00Z",
		"updated_at":   "2026-01-01T00:00:00Z",
	}
	if due != "" {
		raw["due"] = due
	}
	if tags != nil {
		raw["tags"] = tags
	}
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, id+".json"), data, 0o600))
}

// readTaskFixture reads a task file back into a live task.Task.
func readTaskFixture(t *testing.T, tasksDir, id string) Task {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tasksDir, id+".json"))
	require.NoError(t, err)
	var tk Task
	require.NoError(t, json.Unmarshal(data, &tk))
	return tk
}

// rawTaskKeys reads a task file as a bare map, for asserting a key's
// presence/absence independent of the (milestone_id-less) Task struct.
func rawTaskKeys(t *testing.T, tasksDir, id string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tasksDir, id+".json"))
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	return raw
}

// TestMigrateMilestones_LegacyJSONAfterFieldRemoved proves the migration
// reads the legacy milestone_id key from raw JSON — the live task.Task struct
// has no MilestoneID field at all (FR-032), so a plain json.Unmarshal into
// Task would silently drop it; the migration must parse the raw document
// instead (spec's R2/Phase-2 critical-ordering note).
func TestMigrateMilestones_LegacyJSONAfterFieldRemoved(t *testing.T) {
	home := t.TempDir()
	milestonesDir := filepath.Join(home, "milestones")
	tasksDir := filepath.Join(home, "tasks")

	writeMilestoneFixture(t, milestonesDir, "m1", "Q3 Release", "2026-09-30")
	writeLegacyTaskFixture(t, tasksDir, "t1", "ws-1", "m1", "", nil)

	require.NoError(t, MigrateMilestonesToTags(home))

	raw := rawTaskKeys(t, tasksDir, "t1")
	_, hasLegacyKey := raw["milestone_id"]
	assert.False(t, hasLegacyKey, "milestone_id key must be dropped from the rewritten task JSON")

	tk := readTaskFixture(t, tasksDir, "t1")
	assert.Contains(t, tk.Tags, "milestone:q3 release", "task must gain the normalized milestone tag")
	assert.Equal(t, "2026-09-30T00:00:00Z", tk.Due, "due_date must be copied onto empty Due (SD-A12)")

	_, err := os.Stat(filepath.Join(home, milestoneMigrationSentinelFile))
	assert.NoError(t, err, "completion sentinel must be written")
	_, err = os.Stat(milestonesDir)
	assert.True(t, os.IsNotExist(err), "milestones dir must be removed after a successful migration")
}

// TestMigrateMilestones_DueDateCopyOnlyIfEmpty verifies a milestone's
// due_date is copied onto a member task's Due only when Due was empty; a
// task with its own Due keeps it (spec: "Scenario: due_date copied only onto
// empty Due").
func TestMigrateMilestones_DueDateCopyOnlyIfEmpty(t *testing.T) {
	home := t.TempDir()
	milestonesDir := filepath.Join(home, "milestones")
	tasksDir := filepath.Join(home, "tasks")

	writeMilestoneFixture(t, milestonesDir, "m1", "Q3", "2026-09-30")
	writeLegacyTaskFixture(t, tasksDir, "task-empty-due", "ws-1", "m1", "", nil)
	writeLegacyTaskFixture(t, tasksDir, "task-own-due", "ws-1", "m1", "2026-08-01T00:00:00Z", nil)

	require.NoError(t, MigrateMilestonesToTags(home))

	a := readTaskFixture(t, tasksDir, "task-empty-due")
	assert.Equal(t, "2026-09-30T00:00:00Z", a.Due, "empty Due must be filled from due_date")

	b := readTaskFixture(t, tasksDir, "task-own-due")
	assert.Equal(t, "2026-08-01T00:00:00Z", b.Due, "task's own Due must be preserved, not overwritten")
}

// TestMigrateMilestones_CollisionDisambiguation verifies two milestones that
// normalize to the same tag get "-2"/"-3" suffixes keyed by ascending
// milestone-ID order, re-checked after suffixing (ADR D1 r2/r3).
func TestMigrateMilestones_CollisionDisambiguation(t *testing.T) {
	home := t.TempDir()
	milestonesDir := filepath.Join(home, "milestones")
	tasksDir := filepath.Join(home, "tasks")

	// Ascending ID order: milestone-1, milestone-2, milestone-3.
	writeMilestoneFixture(t, milestonesDir, "milestone-1", "Q3", "")
	writeMilestoneFixture(t, milestonesDir, "milestone-2", "Q3", "")
	writeMilestoneFixture(t, milestonesDir, "milestone-3", "q3", "")

	writeLegacyTaskFixture(t, tasksDir, "task-1", "ws-1", "milestone-1", "", nil)
	writeLegacyTaskFixture(t, tasksDir, "task-2", "ws-1", "milestone-2", "", nil)
	writeLegacyTaskFixture(t, tasksDir, "task-3", "ws-1", "milestone-3", "", nil)

	require.NoError(t, MigrateMilestonesToTags(home))

	t1 := readTaskFixture(t, tasksDir, "task-1")
	t2 := readTaskFixture(t, tasksDir, "task-2")
	t3 := readTaskFixture(t, tasksDir, "task-3")

	assert.Contains(t, t1.Tags, "milestone:q3")
	assert.Contains(t, t2.Tags, "milestone:q3-2")
	assert.Contains(t, t3.Tags, "milestone:q3-3")
}

// TestMigrateMilestones_Idempotent verifies a second boot after a completed
// migration is a total no-op: sentinel present → 0 task files modified, 0 tag
// duplication.
func TestMigrateMilestones_Idempotent(t *testing.T) {
	home := t.TempDir()
	milestonesDir := filepath.Join(home, "milestones")
	tasksDir := filepath.Join(home, "tasks")

	writeMilestoneFixture(t, milestonesDir, "m1", "Q3", "2026-09-30")
	writeLegacyTaskFixture(t, tasksDir, "t1", "ws-1", "m1", "", nil)

	require.NoError(t, MigrateMilestonesToTags(home))
	firstRun, err := os.ReadFile(filepath.Join(tasksDir, "t1.json"))
	require.NoError(t, err)

	require.NoError(t, MigrateMilestonesToTags(home)) // second boot — sentinel present
	secondRun, err := os.ReadFile(filepath.Join(tasksDir, "t1.json"))
	require.NoError(t, err)

	assert.Equal(t, string(firstRun), string(secondRun),
		"re-running migration after completion must not modify any task file")

	tk := readTaskFixture(t, tasksDir, "t1")
	assert.Len(t, tk.Tags, 1, "tag must not be duplicated across repeated runs")
}

// TestMigrateMilestones_CrashSafe simulates a mid-migration crash: milestone
// files still present (deletion only happens in Phase 3, after the sentinel),
// one task already migrated, one task not yet migrated, and no sentinel
// written yet. Running the migration against this partial state must produce
// the IDENTICAL final tag assignment a single clean run would (deterministic
// suffix recomputation from all surviving milestone files, not from
// per-task processing state).
func TestMigrateMilestones_CrashSafe(t *testing.T) {
	home := t.TempDir()
	milestonesDir := filepath.Join(home, "milestones")
	tasksDir := filepath.Join(home, "tasks")

	// Both milestones still present — a crash never deletes files individually.
	writeMilestoneFixture(t, milestonesDir, "milestone-1", "Q3", "")
	writeMilestoneFixture(t, milestonesDir, "milestone-2", "q3", "")

	// task-a was ALREADY migrated before the simulated crash: tag applied,
	// milestone_id key already dropped.
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	alreadyMigrated := map[string]any{
		"id": "task-a", "title": "Task task-a", "action": "llm", "status": "inbox",
		"workspace_id": "ws-1", "tags": []string{"milestone:q3"},
		"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
	}
	dataA, err := json.Marshal(alreadyMigrated)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, "task-a.json"), dataA, 0o600))

	// task-b was NOT yet migrated when the crash happened.
	writeLegacyTaskFixture(t, tasksDir, "task-b", "ws-1", "milestone-2", "", nil)

	// No sentinel present — mirrors "crashed before Phase 3".
	_, statErr := os.Stat(filepath.Join(home, milestoneMigrationSentinelFile))
	require.True(t, os.IsNotExist(statErr), "test setup must not have a sentinel yet")

	require.NoError(t, MigrateMilestonesToTags(home))

	a := readTaskFixture(t, tasksDir, "task-a")
	assert.Equal(t, []string{"milestone:q3"}, a.Tags, "already-migrated task must not gain a duplicate tag")

	b := readTaskFixture(t, tasksDir, "task-b")
	// Deterministic: milestone-2 ("q3") still resolves to "milestone:q3-2"
	// even though task-a (referencing milestone-1) was already processed —
	// the suffix ledger is rebuilt from ALL surviving milestone files, not
	// from which tasks happen to already carry a tag.
	assert.Equal(t, []string{"milestone:q3-2"}, b.Tags,
		"re-run from a crashed partial state must produce the identical deterministic suffix as a clean run")

	rawB := rawTaskKeys(t, tasksDir, "task-b")
	_, hasLegacyKey := rawB["milestone_id"]
	assert.False(t, hasLegacyKey)

	_, err = os.Stat(filepath.Join(home, milestoneMigrationSentinelFile))
	assert.NoError(t, err)
}

// TestMigrateMilestones_EmptyLogged verifies a milestone with no member
// tasks is preserved only as an Info log entry (name + due_date) — a tag
// cannot exist unattached — and no task file is touched.
func TestMigrateMilestones_EmptyLogged(t *testing.T) {
	home := t.TempDir()
	milestonesDir := filepath.Join(home, "milestones")
	writeMilestoneFixture(t, milestonesDir, "m-empty", "Retired", "2026-01-01")

	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	require.NoError(t, MigrateMilestonesToTags(home))

	logged := buf.String()
	assert.Contains(t, logged, "empty milestone preserved as log entry")
	assert.Contains(t, logged, "Retired")
	assert.Contains(t, logged, "2026-01-01")

	_, err := os.Stat(filepath.Join(home, milestoneMigrationSentinelFile))
	assert.NoError(t, err, "sentinel must still be written even when every milestone is empty")
}

// TestMigrateMilestones_NoMilestonesDir verifies a fresh install (no
// milestones dir at all) is a clean, immediate no-op that still writes the
// sentinel so future boots skip the ReadDir entirely.
func TestMigrateMilestones_NoMilestonesDir(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, MigrateMilestonesToTags(home))
	_, err := os.Stat(filepath.Join(home, milestoneMigrationSentinelFile))
	assert.NoError(t, err)
}

// TestMigrateMilestones_PartialFailure_DoesNotFinalize is review r1
// silent-failure HIGH 2: when at least one task file fails to migrate (here,
// a task already at the 16-tag cap — SD-A9/spec Part A §B — for which
// appending the milestone tag would push it to 17, rejected by
// normalizeTags), the run must NOT write the completion sentinel and must
// NOT remove the milestones dir. Writing the sentinel unconditionally (the
// pre-fix behavior) would have made task-bad's milestone_id/tag mapping
// PERMANENTLY unrecoverable: the sentinel short-circuits every future call,
// and the milestone source files driving Phase 1's mapping are only safe to
// delete once every task file has actually incorporated that mapping.
func TestMigrateMilestones_PartialFailure_DoesNotFinalize(t *testing.T) {
	home := t.TempDir()
	milestonesDir := filepath.Join(home, "milestones")
	tasksDir := filepath.Join(home, "tasks")

	writeMilestoneFixture(t, milestonesDir, "m1", "Q3", "2026-09-30")

	// review r2 MED-1: a task with EXACTLY 16 pre-existing tags is no longer
	// a poison pill — appending the migration's 17th (milestone) tag now
	// gracefully drops the oldest pre-existing tag to make room and
	// finalizes (see TestMigrateMilestones_AtTagCap_TrimsOldestAndFinalizes
	// below). To keep testing the genuine "a single bad task file blocks
	// the whole migration" contract, this fixture now uses a task ALREADY
	// over the cap (17 tags) BEFORE the migration's append — a real,
	// pre-existing data-integrity problem this migration cannot and must
	// not silently paper over.
	seventeenTags := make([]string, 17)
	for i := range seventeenTags {
		seventeenTags[i] = "existing-tag-" + string(rune('a'+i))
	}
	writeLegacyTaskFixture(t, tasksDir, "task-good", "ws-1", "m1", "", nil)
	writeLegacyTaskFixture(t, tasksDir, "task-bad", "ws-1", "m1", "", seventeenTags)

	err := MigrateMilestonesToTags(home)
	require.Error(t, err, "a partial per-file failure must surface as an error, not be silently swallowed")
	assert.Contains(t, err.Error(), "failed to migrate")

	_, statErr := os.Stat(filepath.Join(home, milestoneMigrationSentinelFile))
	assert.True(t, os.IsNotExist(statErr),
		"sentinel must NOT be written when any task file failed to migrate")
	_, statErr = os.Stat(milestonesDir)
	assert.NoError(t, statErr, "milestones dir must NOT be removed when any task file failed to migrate")

	// task-bad's file must be untouched — still carries the raw milestone_id
	// key, so a retry finds the SAME mapping and fails the SAME deterministic
	// way (not silently drifting into a different, undocumented state).
	rawBad := rawTaskKeys(t, tasksDir, "task-bad")
	_, hasLegacyKey := rawBad["milestone_id"]
	assert.True(t, hasLegacyKey, "a failed migration must leave the task file's milestone_id key intact")

	// task-good's own migration succeeded independently (per-file, not
	// transactional across the whole run) — it already carries its tag.
	good := readTaskFixture(t, tasksDir, "task-good")
	assert.Contains(t, good.Tags, "milestone:q3")

	// Retry (e.g. next boot): task-good's migration is a no-op (already
	// dropped its milestone_id key, idempotent — no duplicate tag), task-bad
	// fails identically again, and the sentinel/dir removal is still
	// withheld.
	err2 := MigrateMilestonesToTags(home)
	require.Error(t, err2)
	goodAfterRetry := readTaskFixture(t, tasksDir, "task-good")
	assert.Len(t, goodAfterRetry.Tags, 1, "already-migrated task-good must not gain a duplicate tag on retry")
	_, statErr = os.Stat(filepath.Join(home, milestoneMigrationSentinelFile))
	assert.True(t, os.IsNotExist(statErr), "sentinel must still be withheld on a repeated failing retry")
}

// TestMigrateMilestones_AtTagCap_TrimsOldestAndFinalizes is the review r2
// MED-1 regression test: a milestone-member task already at EXACTLY
// maxTagsPerTask (16) pre-existing tags must not poison-pill the whole
// migration. Appending the 17th (milestone) tag would otherwise make
// normalizeTags error (not truncate), setting migrationFailures>0 and
// permanently withholding the sentinel — re-running the WHOLE migration on
// every future boot, blocking finalization for every task, not just this
// one. The fix drops the oldest (front-of-list) pre-existing tag to make
// room, logs a WARN, and finalizes normally.
func TestMigrateMilestones_AtTagCap_TrimsOldestAndFinalizes(t *testing.T) {
	home := t.TempDir()
	milestonesDir := filepath.Join(home, "milestones")
	tasksDir := filepath.Join(home, "tasks")

	writeMilestoneFixture(t, milestonesDir, "m1", "Q3", "2026-09-30")

	sixteenTags := make([]string, 16)
	for i := range sixteenTags {
		sixteenTags[i] = "existing-tag-" + string(rune('a'+i))
	}
	writeLegacyTaskFixture(t, tasksDir, "task-at-cap", "ws-1", "m1", "", sixteenTags)

	err := MigrateMilestonesToTags(home)
	require.NoError(t, err, "a task exactly at the tag cap must not block the whole migration")

	tk := readTaskFixture(t, tasksDir, "task-at-cap")
	assert.LessOrEqual(t, len(tk.Tags), maxTagsPerTask, "tags must be trimmed to fit the cap")
	assert.Contains(t, tk.Tags, "milestone:q3", "the migrated milestone tag must be present")
	assert.NotContains(t, tk.Tags, "existing-tag-a",
		"the oldest (front-of-list) pre-existing tag must have been dropped to make room")
	assert.Contains(t, tk.Tags, "existing-tag-p",
		"the newest pre-existing tag (index 15, the 16th) must survive the trim")

	_, statErr := os.Stat(filepath.Join(home, milestoneMigrationSentinelFile))
	assert.NoError(t, statErr, "completion sentinel must be written — migration must finalize")
	_, statErr = os.Stat(milestonesDir)
	assert.True(t, os.IsNotExist(statErr), "milestones dir must be removed after finalization")
}

// TestMigrateMilestones_AlreadyOverCap_StillFails proves the fix is scoped
// correctly: a task ALREADY over maxTagsPerTask BEFORE the migration's own
// append (a genuine pre-existing data-integrity problem, unrelated to this
// migration) is NOT silently rescued — it must still surface as a migration
// failure needing operator attention, exactly as before this fix.
func TestMigrateMilestones_AlreadyOverCap_StillFails(t *testing.T) {
	home := t.TempDir()
	milestonesDir := filepath.Join(home, "milestones")
	tasksDir := filepath.Join(home, "tasks")

	writeMilestoneFixture(t, milestonesDir, "m1", "Q3", "2026-09-30")

	seventeenTags := make([]string, 17)
	for i := range seventeenTags {
		seventeenTags[i] = "existing-tag-" + string(rune('a'+i))
	}
	writeLegacyTaskFixture(t, tasksDir, "task-already-over", "ws-1", "m1", "", seventeenTags)

	err := MigrateMilestonesToTags(home)
	require.Error(t, err, "a task already over the tag cap before this migration's own append must still fail")

	_, statErr := os.Stat(filepath.Join(home, milestoneMigrationSentinelFile))
	assert.True(t, os.IsNotExist(statErr), "sentinel must not be written when a genuine data-integrity issue exists")
}
