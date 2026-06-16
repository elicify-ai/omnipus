// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package datamodel

// Regression test: failed-status workflow task must not be stranded in tasks/.
//
// "failed" appears in both the GTD status vocabulary (inbox, next, active,
// waiting, done, failed) and the workflow status vocabulary (queued, assigned,
// running, completed, failed). The migration uses "title non-empty" as the
// decisive workflow signal, so a file with title="X" and status="failed" must
// be moved to workflow-tasks/ even though "failed" is also a valid GTD status.
//
// Without the title-wins rule, such a file could be misclassified as GTD and
// left stranded in tasks/, making it invisible to the taskstore UI and
// permanently unreachable by /start (it would 409 — wrong status).
//
// Traces to: feat/level1-project-task-mgmt — #402/#397 (migration regression)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegression_MigrateWorkflowTasks_FailedWorkflowNotStranded verifies that a
// workflow task with status="failed" (the ambiguous overlap case) is moved by the
// migration because the title field is non-empty.
//
// BDD:
//
//	Given tasks/ contains {"id":"...","title":"Build Docker Image","status":"failed"},
//	When Init() runs (which calls migrateWorkflowTasks),
//	Then the file is present in workflow-tasks/ (title wins — classified as workflow),
//	And the file is absent from tasks/ (not stranded).
//
// Also verifies a real GTD task with status="failed" (which HAS a "name" field)
// is NOT moved — it stays in tasks/ (name+GTD-status → GTD classification).
//
// Traces to: project-task-management-level1-spec.md — #402/#397 migration regression
func TestRegression_MigrateWorkflowTasks_FailedWorkflowNotStranded(t *testing.T) {
	// Traces to: #402/#397 — failed-status workflow task (title non-empty) must
	// be moved to workflow-tasks/, not left stranded by the "failed is also GTD" ambiguity.
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "tasks"), 0o700))

	now := time.Now().UTC().Format(time.RFC3339)

	// Workflow task: title + status="failed" (ambiguous — "failed" is in both vocabularies).
	// Classification must be workflow because title != "".
	const wfID = "01JXSTRANDEDWF_FAILED001"
	wfData, err := json.Marshal(map[string]any{
		"id":         wfID,
		"title":      "Build Docker Image",
		"status":     "failed",
		"created_at": now,
		"updated_at": now,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "tasks", wfID+".json"),
		wfData, 0o600,
	))

	// GTD task: name + status="failed" (clearly GTD — has "name", status in GTD set).
	// Must stay in tasks/ — it is a real failed GTD task, not a workflow task.
	const gtdID = "01JXSTRANDEDGTD_FAILED01"
	gtdData, err := json.Marshal(map[string]any{
		"id":         gtdID,
		"name":       "Fix Tests",
		"status":     "failed",
		"created_at": now,
		"updated_at": now,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "tasks", gtdID+".json"),
		gtdData, 0o600,
	))

	// Run Init — calls migrateWorkflowTasks internally.
	require.NoError(t, Init(home), "Init must succeed")

	// The workflow file must be in workflow-tasks/ (moved — title wins).
	wfDst := filepath.Join(home, "workflow-tasks", wfID+".json")
	wfSrc := filepath.Join(home, "tasks", wfID+".json")

	_, statDstErr := os.Stat(wfDst)
	assert.NoError(t, statDstErr,
		"workflow task (title='Build Docker Image', status='failed') must be in workflow-tasks/ after migration — "+
			"the title field is the decisive workflow discriminator")

	_, statSrcErr := os.Stat(wfSrc)
	assert.True(t, os.IsNotExist(statSrcErr),
		"workflow task (title='Build Docker Image', status='failed') must NOT be left stranded in tasks/ — "+
			"title-wins rule must classify it as workflow regardless of status")

	// The moved file content must be intact.
	if _, err := os.Stat(wfDst); err == nil {
		moved, readErr := os.ReadFile(wfDst)
		require.NoError(t, readErr)
		assert.Equal(t, string(wfData), string(moved),
			"moved workflow task must be byte-for-byte identical to the original")
	}

	// The GTD task must remain in tasks/ (name+failed → GTD, not workflow).
	gtdPath := filepath.Join(home, "tasks", gtdID+".json")
	_, statGTDErr := os.Stat(gtdPath)
	assert.NoError(t, statGTDErr,
		"GTD task (name='Fix Tests', status='failed') must remain in tasks/ — name field classifies it as GTD")

	_, statGTDInWF := os.Stat(filepath.Join(home, "workflow-tasks", gtdID+".json"))
	assert.True(t, os.IsNotExist(statGTDInWF),
		"GTD task must NOT appear in workflow-tasks/ — name field overrides failed-status ambiguity")

	// Differentiation: the two "failed"-status files produce different outcomes.
	// The workflow file (title non-empty) was moved to workflow-tasks/.
	// The GTD file (name non-empty) stayed in tasks/.
	// This proves the classifier is not hardcoded to move or leave all "failed" files.
	assert.NoError(t, statDstErr,
		"workflow file (title='Build Docker Image') must be in workflow-tasks/ after migration")
	assert.True(t, os.IsNotExist(statSrcErr),
		"workflow file must NOT remain in tasks/ after migration — it was moved")
	assert.NoError(t, statGTDErr,
		"GTD file (name='Fix Tests') must still be in tasks/ — not moved")
	assert.True(t, os.IsNotExist(statGTDInWF),
		"GTD file must NOT appear in workflow-tasks/")

	// Count-based differentiation: workflow-tasks/ has 1 file, tasks/ has 1 file.
	// Both counts are 1 but the files are different — one migrated, one stayed.
	wfTasksEntries, _ := os.ReadDir(filepath.Join(home, "workflow-tasks"))
	srcTasksEntries, _ := os.ReadDir(filepath.Join(home, "tasks"))
	wfJSON := 0
	for _, e := range wfTasksEntries {
		if strings.HasSuffix(e.Name(), ".json") {
			wfJSON++
		}
	}
	srcJSON := 0
	for _, e := range srcTasksEntries {
		if strings.HasSuffix(e.Name(), ".json") {
			srcJSON++
		}
	}
	assert.Equal(t, 1, wfJSON,
		"workflow-tasks/ must contain exactly 1 JSON file after migration (the workflow task)")
	assert.Equal(t, 1, srcJSON,
		"tasks/ must contain exactly 1 JSON file after migration (the GTD task)")
}
