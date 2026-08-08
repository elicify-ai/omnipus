// rest_plans_progress_perf_test.go — regression coverage for fix-wave
// finding #1b (14-reviewer sign-off, release/v0.1.1): toWirePlan's read-time
// progress (plan.ComputeProgress) used to issue a full task.Store.List scan
// PER PLAN inside handleWorkspacePlansList's list loop. The fix
// (taskSnapshotLister in rest_plans.go) fetches the workspace's tasks ONCE
// per request and reuses that snapshot as the plan.TaskLister for every
// plan's ComputeProgress call. Mirrors rest_tasks_rollup_perf_test.go's
// approach: assert the bounded task.Store.List call count directly via
// task.Store.ListCallCount(), since a regression reintroducing the per-plan
// List call would still produce correct JSON.
package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// listWorkspacePlans issues GET /api/v1/workspaces/{id}/plans and returns the
// decoded response.
func listWorkspacePlans(t *testing.T, api *restAPI, wsID string) (*httptest.ResponseRecorder, gen.PlanListResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+wsID+"/plans", nil)
	r.URL.Path = "/api/v1/workspaces/" + wsID + "/plans"
	api.HandleWorkspaces(w, r)
	var resp gen.PlanListResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w, resp
}

// TestHandleWorkspacePlansList_TaskSnapshotBoundsListCalls seeds N plans in
// one workspace, each with one done and one not-done member task (progress
// should read 0.5 for every plan), and asserts:
//  1. Every plan's server-computed progress is correct (0.5) — functional
//     correctness of the shared taskSnapshotLister vs. the old per-plan
//     ComputeProgress(planID, a.taskStore) call.
//  2. task.Store.List is invoked a CONSTANT number of times for the whole
//     request — not proportional to N — proving the O(n) per-plan
//     task-store scan is gone.
func TestHandleWorkspacePlansList_TaskSnapshotBoundsListCalls(t *testing.T) {
	api := newTestRestAPIWithPlans(t)
	wsID := createTestWorkspace(t, api, "Plans Progress Perf WS")

	const numPlans = 10
	planIDs := make([]string, 0, numPlans)
	for i := 0; i < numPlans; i++ {
		planBody := fmt.Sprintf(`{"workspace_id":%q,"title":"plan-%d","owner_agent_id":%q}`, wsID, i, testPlansAgentID)
		wPlan := postPlan(t, api, wsID, planBody)
		require.Equal(t, http.StatusCreated, wPlan.Code, "body=%s", wPlan.Body.String())
		var created gen.Plan
		require.NoError(t, json.Unmarshal(wPlan.Body.Bytes(), &created))
		planIDs = append(planIDs, created.Id)

		// One done member, one not-done member -> progress == 0.5.
		doneBody := fmt.Sprintf(`{"workspace_id":%q,"title":"done-member-%d","plan_id":%q}`, wsID, i, created.Id)
		wDone := postTask(t, api, doneBody)
		require.Equal(t, http.StatusCreated, wDone.Code, "body=%s", wDone.Body.String())
		var doneTask gen.Task
		require.NoError(t, json.Unmarshal(wDone.Body.Bytes(), &doneTask))
		_, uerr := api.taskStore.Update(doneTask.Id, task.Patch{Status: taskStatusPtr(task.StatusDone)})
		require.NoError(t, uerr)

		pendingBody := fmt.Sprintf(`{"workspace_id":%q,"title":"pending-member-%d","plan_id":%q}`, wsID, i, created.Id)
		wPending := postTask(t, api, pendingBody)
		require.Equal(t, http.StatusCreated, wPending.Code, "body=%s", wPending.Body.String())
	}

	before := api.taskStore.ListCallCount()
	w, listResp := listWorkspacePlans(t, api, wsID)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	after := api.taskStore.ListCallCount()

	require.Len(t, listResp.Plans, numPlans)
	// listResp.Plans is oapi-codegen's own anonymous struct type (distinct
	// from gen.Plan, per this codebase's per-schema-context generation
	// convention) — pull out just the two fields this test needs rather
	// than trying to name that type.
	progressByID := make(map[string]*float32, len(listResp.Plans))
	for _, p := range listResp.Plans {
		progressByID[p.Id] = p.Progress
	}
	for _, pid := range planIDs {
		progress, ok := progressByID[pid]
		require.True(t, ok, "plan %s missing from list response", pid)
		require.NotNil(t, progress, "plan %s should have a computed progress", pid)
		assert.InDelta(t, 0.5, *progress, 0.0001, "plan %s: one done + one pending member should be 0.5", pid)
	}

	// The regression assertion: regardless of numPlans, toWirePlan's progress
	// computation must not perform its own task.Store.List call per plan.
	// handleWorkspacePlansList issues exactly one task.Store.List call total —
	// the shared taskSnapshotLister snapshot reused by every plan.
	delta := after - before
	assert.Equal(t, int64(1), delta,
		"handleWorkspacePlansList must issue a CONSTANT number of task.Store.List "+
			"calls regardless of how many plans are returned (fix-wave finding #1b); "+
			"got %d calls for %d returned plans — a delta scaling with the result "+
			"size means the O(n) per-plan progress List call regressed",
		delta, len(listResp.Plans))
}
