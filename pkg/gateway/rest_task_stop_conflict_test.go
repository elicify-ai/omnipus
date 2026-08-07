// rest_task_stop_conflict_test.go — coverage for handleTaskStop's
// partial-success/conflict classification (pkg/gateway/rest_tasks.go).
//
// Bug fixed here: handleTaskStop had no partial-success branch. On ANY error
// from PlanEngine.StopTask it discarded `updated` and re-read the task; if
// that re-read showed anything other than in_progress, it answered 409 —
// even when the re-read showed failed+CancelReasonStoppedByUser, i.e. the
// task WAS successfully stopped (just not by this exact racing request,
// because a concurrent Stop for the same task won). A stop that achieved
// its purpose must not be reported as an error. taskStopConflictOutcome
// (rest_tasks.go) is the fix: it distinguishes "already stopped" (200,
// idempotent success) from a genuine conflict (409, unchanged) using the
// re-read task's own Status/CancelReason.
//
// Traces to: docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md
package gateway

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestTaskStopConflictOutcome_Classification is the deterministic,
// state-by-state proof of the fix: it exercises taskStopConflictOutcome
// directly against every state handleTaskStop can observe on the post-error
// re-read, with no dependence on goroutine scheduling. This is the
// mutation-test load-bearing case — reverting the production fix removes
// taskStopConflictOutcome entirely, which fails this test to even compile.
func TestTaskStopConflictOutcome_Classification(t *testing.T) {
	cases := []struct {
		name               string
		status             task.Status
		cancelReason       task.CancelReason
		wantHandled        bool
		wantAlreadyStopped bool
		wantMessageHas     string
	}{
		{
			name:        "still in_progress -> not handled, caller falls back to 500",
			status:      task.StatusInProgress,
			wantHandled: false,
		},
		{
			name:               "failed by a user Stop (this request lost the race) -> already stopped, 200",
			status:             task.StatusFailed,
			cancelReason:       task.CancelReasonStoppedByUser,
			wantHandled:        true,
			wantAlreadyStopped: true,
		},
		{
			name:           "failed for an unrelated reason -> genuine conflict, 409",
			status:         task.StatusFailed,
			cancelReason:   "",
			wantHandled:    true,
			wantMessageHas: `"failed"`,
		},
		{
			name:           "completed normally before the stop landed -> genuine conflict, 409",
			status:         task.StatusDone,
			wantHandled:    true,
			wantMessageHas: `"done"`,
		},
		{
			name:           "never started -> genuine conflict, 409",
			status:         task.StatusInbox,
			wantHandled:    true,
			wantMessageHas: `"inbox"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := &task.Task{ID: "t1", Status: tc.status, CancelReason: tc.cancelReason}
			outcome, handled := taskStopConflictOutcome(tk)
			require.Equal(t, tc.wantHandled, handled)
			if !tc.wantHandled {
				return
			}
			assert.Equal(t, tc.wantAlreadyStopped, outcome.alreadyStopped)
			if tc.wantMessageHas != "" {
				assert.Contains(t, outcome.message, tc.wantMessageHas)
			}
			if tc.wantAlreadyStopped {
				assert.Empty(t, outcome.message, "no 409 body text is needed for the success path")
			}
		})
	}
}

// TestTaskStop_ConcurrentDoubleStopNeverConflicts is the end-to-end,
// black-box proof through the real HTTP handler + real PlanEngine.StopTask:
// two concurrent stop requests for the SAME in_progress standalone task must
// never produce a 409 between them. One request performs the actual state
// transition (in_progress -> failed[stopped_by_user]); the other loses the
// race inside PlanEngine.StopTask's own planDecisionMu-guarded re-check, but
// since the task IS now stopped, that request must ALSO see success (200),
// not a conflict — this is exactly the false-409 bug. Fired with enough
// concurrent attempts (and a shared start gate) that at least one pair
// genuinely races: both pass handleTaskStop's own unlocked precheck
// (in_progress) before either has completed the full engine call.
func TestTaskStop_ConcurrentDoubleStopNeverConflicts(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Concurrent Task Stop WS")
	taskID := mustCreateTask(t, api, wsID, "racing stop", "")
	inProgress := task.StatusInProgress
	_, err := api.taskStore.Update(taskID, task.Patch{Status: &inProgress})
	require.NoError(t, err)

	const n = 40
	codes := make([]int, n)
	bodies := make([]string, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			w := postTaskAction(t, api, taskID, "stop")
			codes[i] = w.Code
			bodies[i] = w.Body.String()
		}(i)
	}
	close(start)
	wg.Wait()

	successCount := 0
	for i, code := range codes {
		require.NotEqual(t, http.StatusConflict, code,
			"request %d: a stop that achieved its purpose must never be reported as a 409 conflict; body=%s", i, bodies[i])
		if code == http.StatusOK {
			successCount++
			var got gen.Task
			require.NoError(t, json.Unmarshal([]byte(bodies[i]), &got), "body=%s", bodies[i])
			assert.Equal(t, gen.TaskStatusFailed, got.Status, "body=%s", bodies[i])
			require.NotNil(t, got.CancelReason, "body=%s", bodies[i])
			assert.Equal(t, gen.TaskCancelReasonStoppedByUser, *got.CancelReason, "body=%s", bodies[i])
		}
	}
	assert.Greater(t, successCount, 0, "at least one of the %d concurrent stop attempts must succeed", n)

	reloaded, gerr := api.taskStore.Get(taskID)
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusFailed, reloaded.Status)
	assert.Equal(t, task.CancelReasonStoppedByUser, reloaded.CancelReason)
}
