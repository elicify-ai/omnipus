// rest_tasks_rollup_perf_test.go — regression coverage for fix-wave finding
// #1a (14-reviewer sign-off, release/v0.1.1): toWireTask's read-time rollup
// (computeRollup) used to issue one additional task.Store.List call PER
// RETURNED TASK, making GET /api/v1/tasks and GET /tasks/{id}/subtasks O(n^2)
// in the number of task files on disk (200 returned tasks against a
// 2000-task store meant ~400k file reads for one request). The fix
// (rollupIndex / buildRollupIndex in rest_tasks.go) computes ONE shared
// task.Store.List snapshot per request and reuses it for every task's
// rollup. These tests assert the bounded call count directly via
// task.Store.ListCallCount() (a test-only atomic counter, see its doc
// comment in pkg/task/store.go) rather than merely asserting correctness —
// a regression that reintroduces the per-task List call would still produce
// correct JSON, so only a call-count assertion actually catches it.
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
)

// listTasks issues GET /api/v1/tasks with the given raw query string (may be
// empty) and returns the recorder.
func listTasks(t *testing.T, api *restAPI, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	path := "/api/v1/tasks"
	if rawQuery != "" {
		path += "?" + rawQuery
	}
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.URL.Path = "/api/v1/tasks"
	r.URL.RawQuery = rawQuery
	api.HandleTasks(w, r)
	return w
}

// listTaskSubtasks issues GET /api/v1/tasks/{id}/subtasks and returns the recorder.
func listTaskSubtasks(t *testing.T, api *restAPI, parentID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+parentID+"/subtasks", nil)
	r.URL.Path = "/api/v1/tasks/" + parentID + "/subtasks"
	api.HandleTasks(w, r)
	return w
}

// createChildTask creates a task with the given parent via POST /api/v1/tasks
// and returns its wire representation.
func createChildTask(t *testing.T, api *restAPI, wsID, title, parentID string) gen.Task {
	t.Helper()
	body := fmt.Sprintf(`{"workspace_id":%q,"title":%q,"parent_task_id":%q}`, wsID, title, parentID)
	w := postTask(t, api, body)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	return created
}

// TestHandleTaskList_RollupIndexBoundsListCalls seeds N parent tasks, each
// with one live (non-terminal) child, and asserts:
//  1. Each parent's rollup correctly reflects its child (functional
//     correctness of the shared rollupIndex vs. the old per-task List call).
//  2. task.Store.List is invoked a CONSTANT number of times for the whole
//     request — not proportional to N — proving the O(n^2) pattern is gone.
func TestHandleTaskList_RollupIndexBoundsListCalls(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	wsID := createTestWorkspace(t, api, "Rollup Perf WS")

	const numParents = 12
	parentIDs := make([]string, 0, numParents)
	for i := 0; i < numParents; i++ {
		parentBody := fmt.Sprintf(`{"workspace_id":%q,"title":"parent-%d"}`, wsID, i)
		wCreate := postTask(t, api, parentBody)
		require.Equal(t, http.StatusCreated, wCreate.Code, "body=%s", wCreate.Body.String())
		var parent gen.Task
		require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &parent))
		parentIDs = append(parentIDs, parent.Id)
		// One live child per parent — lands in `inbox` (non-terminal), so it
		// must appear in that parent's rollup.
		createChildTask(t, api, wsID, fmt.Sprintf("child-of-%d", i), parent.Id)
	}

	before := api.taskStore.ListCallCount()
	w := listTasks(t, api, "workspace_id="+wsID)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	after := api.taskStore.ListCallCount()

	var tasks []gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	require.Len(t, tasks, numParents*2, "expected every parent and child task back (no parent_task_id filter)")

	// Functional correctness: every parent must show its one live child in
	// Rollup; children (no children of their own) must show a nil Rollup.
	byID := make(map[string]gen.Task, len(tasks))
	for _, tk := range tasks {
		byID[tk.Id] = tk
	}
	for _, pid := range parentIDs {
		parent, ok := byID[pid]
		require.True(t, ok, "parent %s missing from list response", pid)
		require.NotNil(t, parent.Rollup, "parent %s should have a rollup entry for its live child", pid)
		assert.Len(t, *parent.Rollup, 1, "parent %s rollup should have exactly one live child", pid)
	}

	// The regression assertion: regardless of numParents, toWireTask's
	// rollup computation must not perform its own List call per task.
	// handleTaskList issues exactly two List calls total: the primary
	// filtered listing, and the one buildRollupIndex snapshot shared by
	// every task in the response.
	delta := after - before
	assert.Equal(t, int64(2), delta,
		"handleTaskList must issue a CONSTANT number of task.Store.List calls "+
			"regardless of how many tasks are returned (fix-wave finding #1a); "+
			"got %d calls for %d returned tasks — a delta scaling with the "+
			"result size means the O(n^2) per-task rollup List call regressed",
		delta, len(tasks))
}

// TestHandleTaskSubtasks_RollupIndexBoundsListCalls mirrors the list test for
// GET /tasks/{id}/subtasks: each child's OWN rollup (its grandchildren) must
// come from the shared index, not a fresh List call per child.
func TestHandleTaskSubtasks_RollupIndexBoundsListCalls(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	wsID := createTestWorkspace(t, api, "Subtasks Rollup Perf WS")

	parentBody := fmt.Sprintf(`{"workspace_id":%q,"title":"root"}`, wsID)
	wParent := postTask(t, api, parentBody)
	require.Equal(t, http.StatusCreated, wParent.Code, "body=%s", wParent.Body.String())
	var parent gen.Task
	require.NoError(t, json.Unmarshal(wParent.Body.Bytes(), &parent))

	const numChildren = 12
	childIDs := make([]string, 0, numChildren)
	for i := 0; i < numChildren; i++ {
		child := createChildTask(t, api, wsID, fmt.Sprintf("child-%d", i), parent.Id)
		childIDs = append(childIDs, child.Id)
	}
	// Give exactly one child a live grandchild so its own rollup is non-nil —
	// exercises the per-child rollupIndex lookup, not just the "no rollup" path.
	grandchildParent := childIDs[3]
	createChildTask(t, api, wsID, "grandchild", grandchildParent)

	before := api.taskStore.ListCallCount()
	w := listTaskSubtasks(t, api, parent.Id)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	after := api.taskStore.ListCallCount()

	var children []gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &children))
	require.Len(t, children, numChildren)

	for _, c := range children {
		if c.Id == grandchildParent {
			require.NotNil(t, c.Rollup, "child %s has a live grandchild and should show a rollup", c.Id)
			assert.Len(t, *c.Rollup, 1)
		} else {
			assert.Nil(t, c.Rollup, "child %s has no children and should show a nil rollup", c.Id)
		}
	}

	delta := after - before
	assert.Equal(t, int64(2), delta,
		"handleTaskSubtasks must issue a CONSTANT number of task.Store.List calls "+
			"regardless of how many children are returned (fix-wave finding #1a); got %d calls for %d children",
		delta, len(children))
}
