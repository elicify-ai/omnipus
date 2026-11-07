package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t10MarkStoppedByUser rewrites the task file into the one restartable state
// (failed, stopped_by_user) without going through the store, so the scheduler
// registration is not touched by the setup itself.
func t10MarkStoppedByUser(t *testing.T, f *t10TaskEntryFixture) string {
	t.Helper()
	path := filepath.Join(f.api.taskStore.Dir(), f.id+".json")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(b, &stored))
	stored["status"] = string(task.StatusFailed)
	stored["cancel_reason"] = string(task.CancelReasonStoppedByUser)
	b, err = json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))
	return path
}

// Traces to: chief decision C2 (2026-11-07) under schedules-t10-spec-r6.md
// FR-16 / S-H.16: the restart (Play) route would move a stopped task to
// `next`, where the ordinary drain runs it outside the run-now contract and
// its armed occurrence still fires later. A task with a schedule is refused
// with 409 and a pointer at Run now BEFORE any write; an ordinary task in the
// same stopped state still restarts (control).
func TestT10_ScheduledTaskRestartRefusedBeforeWrite(t *testing.T) {
	for _, kind := range []string{"once", "recurring", "manual-control"} {
		t.Run(kind, func(t *testing.T) {
			fixtureKind := kind
			if kind == "manual-control" {
				fixtureKind = "manual"
			}
			f := t10TaskEntry(t, fixtureKind)
			path := t10MarkStoppedByUser(t, f)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			jobsBefore := t10ReadTaskJobs(t, f.path, f.id)

			w := httptest.NewRecorder()
			f.api.HandleTasks(w, httptest.NewRequest("POST", "/api/v1/tasks/"+f.id+"/restart", nil))

			if kind == "manual-control" {
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				var restarted map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &restarted))
				assert.Equal(t, string(task.StatusNext), restarted["status"], "ordinary restart is unchanged")
				return
			}
			assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
			var refusal gen.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refusal))
			assert.Regexp(t, `(?i)run now`, refusal.Error, "refusal names the one route that starts it")
			assert.Contains(t, refusal.Error, "/api/v1/tasks/"+f.id+"/runs")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "refused before any task write")
			assert.Equal(t, jobsBefore, t10ReadTaskJobs(t, f.path, f.id), "armed registration unchanged")
			rows, err := f.api.taskStore.ListRuns(f.id)
			require.NoError(t, err)
			assert.Empty(t, rows, "nothing ran")
		})
	}
}
