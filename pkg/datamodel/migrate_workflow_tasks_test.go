// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package datamodel

// Regression tests for migrateWorkflowTasks — the one-time boot migration that
// moves workflow task files from tasks/ to workflow-tasks/ (#402/#397).
//
// T1 (boot migration): seed tasks/ with one workflow file and one GTD file,
// call Init() (which calls migrateWorkflowTasks), assert:
//   - workflow file moved to workflow-tasks/
//   - GTD file stays in tasks/
//   - calling Init() again is a no-op (idempotent)
//
// Traces to: feat/level1-project-task-mgmt — #402/#397 storage separation fix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegression_MigrateWorkflowTasks_MovesWorkflowAndLeavesGTD verifies the
// boot migration correctly classifies and moves files.
//
// BDD:
// Given tasks/ contains:
//   - workflow.json (title:"Fetch Dependencies", status:"queued")
//   - gtd.json (name:"Review PR", status:"inbox")
//
// When Init() runs (which calls migrateWorkflowTasks),
// Then workflow.json is moved to workflow-tasks/,
//
//	gtd.json stays in tasks/,
//	and both files are byte-for-byte identical to the originals.
//
// Traces to: feat/level1-project-task-mgmt — #402/#397 boot migration
func TestRegression_MigrateWorkflowTasks_MovesWorkflowAndLeavesGTD(t *testing.T) {
	// Traces to: #402/#397 — migrateWorkflowTasks must move workflow files to workflow-tasks/
	home := t.TempDir()

	// Pre-create dirs to simulate an existing install that didn't have workflow-tasks/ yet.
	require.NoError(t, os.MkdirAll(filepath.Join(home, "tasks"), 0o700))
	// workflow-tasks/ is intentionally absent — Init will create it.

	now := time.Now().UTC().Format(time.RFC3339)

	// Workflow task: has "title" field and "queued" status.
	workflowTask := map[string]any{
		"id":         "01JXMIGRATE_WORKFLOW001",
		"title":      "Fetch Dependencies",
		"status":     "queued",
		"created_at": now,
		"updated_at": now,
	}
	workflowBytes, err := json.Marshal(workflowTask)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "tasks", "01JXMIGRATE_WORKFLOW001.json"),
		workflowBytes, 0o600,
	))

	// GTD task: has "name" field and "inbox" status.
	gtdTask := map[string]any{
		"id":         "01JXMIGRATE_GTD0000001",
		"name":       "Review PR",
		"status":     "inbox",
		"created_at": now,
		"updated_at": now,
	}
	gtdBytes, err := json.Marshal(gtdTask)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "tasks", "01JXMIGRATE_GTD0000001.json"),
		gtdBytes, 0o600,
	))

	// Run Init — this calls migrateWorkflowTasks internally.
	require.NoError(t, Init(home), "Init must succeed and run the migration")

	// Workflow file must be in workflow-tasks/ and gone from tasks/.
	wfDst := filepath.Join(home, "workflow-tasks", "01JXMIGRATE_WORKFLOW001.json")
	wfSrc := filepath.Join(home, "tasks", "01JXMIGRATE_WORKFLOW001.json")

	_, statDstErr := os.Stat(wfDst)
	require.NoError(t, statDstErr,
		"workflow task must be present in workflow-tasks/ after migration (#402)")
	_, statSrcErr := os.Stat(wfSrc)
	require.True(t, os.IsNotExist(statSrcErr),
		"workflow task must be REMOVED from tasks/ after migration (#402)")

	// Content of moved file must equal the original bytes (no corruption).
	movedBytes, err := os.ReadFile(wfDst)
	require.NoError(t, err)
	assert.Equal(t, string(workflowBytes), string(movedBytes),
		"moved workflow task must be byte-for-byte identical to the original")

	// GTD file must still be in tasks/ and unchanged.
	gtdPath := filepath.Join(home, "tasks", "01JXMIGRATE_GTD0000001.json")
	_, statGTDErr := os.Stat(gtdPath)
	require.NoError(t, statGTDErr,
		"GTD task must remain in tasks/ after migration (must not be moved)")
	gtdReadBack, err := os.ReadFile(gtdPath)
	require.NoError(t, err)
	assert.Equal(t, string(gtdBytes), string(gtdReadBack),
		"GTD task content must be unchanged by migration")

	// GTD file must NOT appear in workflow-tasks/.
	_, statGTDInWFErr := os.Stat(
		filepath.Join(home, "workflow-tasks", "01JXMIGRATE_GTD0000001.json"),
	)
	require.True(t, os.IsNotExist(statGTDInWFErr),
		"GTD task must NOT be copied to workflow-tasks/")
}

// TestRegression_MigrateWorkflowTasks_Idempotent verifies that running Init()
// a second time after the migration does not move/duplicate the workflow file
// again and does not corrupt anything.
//
// BDD:
// Given the migration has already run (workflow file is in workflow-tasks/),
// When Init() runs a second time,
// Then the workflow-tasks/ file is unchanged, and tasks/ is still missing it.
//
// Traces to: feat/level1-project-task-mgmt — #402/#397 migration idempotency
func TestRegression_MigrateWorkflowTasks_Idempotent(t *testing.T) {
	// Traces to: #402/#397 — migrateWorkflowTasks must be idempotent
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "tasks"), 0o700))

	now := time.Now().UTC().Format(time.RFC3339)
	workflowTask := map[string]any{
		"id":         "01JXMIGRATE_IDEM000001",
		"title":      "Idempotency Check Task",
		"status":     "assigned",
		"created_at": now,
		"updated_at": now,
	}
	workflowBytes, err := json.Marshal(workflowTask)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "tasks", "01JXMIGRATE_IDEM000001.json"),
		workflowBytes, 0o600,
	))

	// First Init — migration happens.
	require.NoError(t, Init(home), "first Init must succeed")

	wfDst := filepath.Join(home, "workflow-tasks", "01JXMIGRATE_IDEM000001.json")
	wfSrc := filepath.Join(home, "tasks", "01JXMIGRATE_IDEM000001.json")

	// Verify first migration worked.
	_, err = os.Stat(wfDst)
	require.NoError(t, err, "workflow file must be in workflow-tasks/ after first Init")
	_, err = os.Stat(wfSrc)
	require.True(t, os.IsNotExist(err), "workflow file must be gone from tasks/ after first Init")

	contentAfterFirst, err := os.ReadFile(wfDst)
	require.NoError(t, err)

	// Second Init — must be a no-op.
	require.NoError(t, Init(home), "second Init must succeed (idempotent)")

	// workflow-tasks/ file must be unchanged.
	contentAfterSecond, err := os.ReadFile(wfDst)
	require.NoError(t, err)
	assert.Equal(t, string(contentAfterFirst), string(contentAfterSecond),
		"workflow-tasks/ file must be byte-for-byte identical after second Init (idempotent migration)")

	// tasks/ must still not have the file.
	_, err = os.Stat(wfSrc)
	require.True(t, os.IsNotExist(err),
		"workflow file must still be absent from tasks/ after second Init")

	// Third Init — belt-and-suspenders idempotency check.
	require.NoError(t, Init(home), "third Init must succeed (idempotent)")
	contentAfterThird, err := os.ReadFile(wfDst)
	require.NoError(t, err)
	assert.Equal(t, string(contentAfterFirst), string(contentAfterThird),
		"workflow-tasks/ file must still be unchanged after third Init")
}

// assertStatusOnlyMigration seeds tasks/ with a single file carrying only the
// given status (no title/name) and asserts whether Init() relocates it to
// workflow-tasks/ (wantWorkflow=true) or leaves it in tasks/ (wantWorkflow=false).
//
// Traces to: feat/level1-project-task-mgmt — #402/#397 status-only classification
func assertStatusOnlyMigration(t *testing.T, id, status string, wantWorkflow bool) {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "tasks"), 0o700))

	now := time.Now().UTC().Format(time.RFC3339)
	taskBytes, err := json.Marshal(map[string]any{
		"id":         id,
		"status":     status, // status only — no title, no name
		"created_at": now,
		"updated_at": now,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "tasks", id+".json"), taskBytes, 0o600))

	require.NoError(t, Init(home))

	inWorkflow := filepath.Join(home, "workflow-tasks", id+".json")
	inTasks := filepath.Join(home, "tasks", id+".json")
	if wantWorkflow {
		_, err = os.Stat(inWorkflow)
		assert.NoError(t, err, "status %q must be moved to workflow-tasks/", status)
		_, err = os.Stat(inTasks)
		assert.True(t, os.IsNotExist(err), "status %q must be removed from tasks/", status)
	} else {
		_, err = os.Stat(inTasks)
		assert.NoError(t, err, "status %q must remain in tasks/", status)
		_, err = os.Stat(inWorkflow)
		assert.True(t, os.IsNotExist(err), "status %q must NOT be copied to workflow-tasks/", status)
	}
}

// TestRegression_MigrateWorkflowTasks_WorkflowStatusOnly: a file with ONLY a
// workflow status (no "title") is still classified as workflow and moved.
func TestRegression_MigrateWorkflowTasks_WorkflowStatusOnly(t *testing.T) {
	assertStatusOnlyMigration(t, "01JXMIGRATE_STATONLY01", "running", true)
}

// TestRegression_MigrateWorkflowTasks_GTDStatusOnly: a file with ONLY a GTD
// status (no "name") is left in tasks/ (GTD classification wins).
func TestRegression_MigrateWorkflowTasks_GTDStatusOnly(t *testing.T) {
	assertStatusOnlyMigration(t, "01JXMIGRATE_GTDSTAT01", "next", false)
}

// TestRegression_MigrateWorkflowTasks_DifferentiationTwoFiles verifies that
// the migration produces the right result for two distinct files in one pass:
// the workflow file goes, the GTD file stays.  This is the differentiation test.
//
// Traces to: feat/level1-project-task-mgmt — #402/#397 two-file differentiation
func TestRegression_MigrateWorkflowTasks_DifferentiationTwoFiles(t *testing.T) {
	// Traces to: #402/#397 — two different inputs produce two different outcomes
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "tasks"), 0o700))

	now := time.Now().UTC().Format(time.RFC3339)

	ids := map[string]map[string]any{
		"01JXMIGRATE_WF_DIFF001": {
			"id": "01JXMIGRATE_WF_DIFF001", "title": "Build Docker Image",
			"status": "queued", "created_at": now, "updated_at": now,
		},
		"01JXMIGRATE_GTD_DIFF01": {
			"id": "01JXMIGRATE_GTD_DIFF01", "name": "Write release notes",
			"status": "done", "created_at": now, "updated_at": now,
		},
	}
	for id, data := range ids {
		b, _ := json.Marshal(data)
		require.NoError(t, os.WriteFile(filepath.Join(home, "tasks", id+".json"), b, 0o600))
	}

	require.NoError(t, Init(home))

	// Workflow diff file: moved.
	_, err := os.Stat(filepath.Join(home, "workflow-tasks", "01JXMIGRATE_WF_DIFF001.json"))
	assert.NoError(t, err, "workflow diff file must be in workflow-tasks/")
	_, err = os.Stat(filepath.Join(home, "tasks", "01JXMIGRATE_WF_DIFF001.json"))
	assert.True(t, os.IsNotExist(err), "workflow diff file must be absent from tasks/")

	// GTD diff file: stays.
	_, err = os.Stat(filepath.Join(home, "tasks", "01JXMIGRATE_GTD_DIFF01.json"))
	assert.NoError(t, err, "GTD diff file must remain in tasks/")
	_, err = os.Stat(filepath.Join(home, "workflow-tasks", "01JXMIGRATE_GTD_DIFF01.json"))
	assert.True(t, os.IsNotExist(err), "GTD diff file must NOT appear in workflow-tasks/")

	// Result differs: one moved, one stayed — proves the classifier is not hardcoded.
	wfCount := countFilesInDir(t, filepath.Join(home, "workflow-tasks"))
	gtdCount := countFilesInDir(t, filepath.Join(home, "tasks"))
	assert.Equal(t, 1, wfCount, "workflow-tasks/ must contain exactly 1 file")
	// gtdCount may include config.json etc. but must include at least the one GTD task.
	assert.GreaterOrEqual(t, gtdCount, 1, "tasks/ must contain at least 1 file (the GTD task)")
}

// countFilesInDir counts *.json files in dir for use in differentiation assertions.
func countFilesInDir(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	n := 0
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 5 && e.Name()[len(e.Name())-5:] == ".json" {
			n++
		}
	}
	return n
}
