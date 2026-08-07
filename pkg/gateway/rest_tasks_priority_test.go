// rest_tasks_priority_test.go — regression coverage for the M2(a) REST
// create-path priority validation fix.
//
// Root cause: gen.TaskCreateRequest.Priority is a wire *int, so
// handleTaskCreate KNOWS when the caller explicitly sent priority:0 (a
// non-nil pointer to 0) versus leaving it absent (a nil pointer) — but the
// handler previously discarded that presence information by assigning
// `t.Priority = *req.Priority` straight into task.Task.Priority (a plain int,
// where 0 means "unset" per its own contract) BEFORE any range check, then
// relied on task.Store.Create's own validation — which deliberately skips the
// check when Priority==0 for exactly the same "0 = unset" reason. An explicit
// priority:0 therefore sailed through uncaught, persisted as unset, and read
// back as 3 via EffectivePriority. priority:6 was already correctly rejected
// (6 != 0, so the store's check does fire), which is why only the zero case
// was silently broken.
//
// Traces to: docs/internal/uat/uat-report-adr057-CONSOLIDATED-2026-08-03.md M2(a).
package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// postTaskCreate posts the given raw JSON body to POST /api/v1/tasks and
// returns the response recorder.
func postTaskCreate(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)
	return w
}

// TestTaskCreate_PriorityBoundaryMatrix covers the full REST create-path
// priority matrix: 0 (explicit-zero, must now be REJECTED — this is the RED
// case before the fix), 1 and 5 (valid bounds), 6 and a negative value
// (invalid), and absent (must still default to 3 on read, via
// EffectivePriority). Binding Rule 4: every rejection is paired with an
// acceptance at the same boundary.
func TestTaskCreate_PriorityBoundaryMatrix(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	cases := []struct {
		name         string
		priorityJSON string // literal JSON fragment, or "" to omit the field
		wantStatus   int
		wantPriority int // only checked when wantStatus == 201
	}{
		{"explicit_zero_rejected", `"priority":0,`, http.StatusBadRequest, 0},
		{"one_lower_bound_valid", `"priority":1,`, http.StatusCreated, 1},
		{"five_upper_bound_valid", `"priority":5,`, http.StatusCreated, 5},
		{"six_rejected", `"priority":6,`, http.StatusBadRequest, 0},
		{"negative_rejected", `"priority":-1,`, http.StatusBadRequest, 0},
		{"absent_defaults_to_three", ``, http.StatusCreated, 3},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := fmt.Sprintf(
				`{%s"title":"PriorityMatrix-%s","action":"llm","workspace_id":%q}`,
				c.priorityJSON, c.name, wsID,
			)
			w := postTaskCreate(t, api, body)
			require.Equal(t, c.wantStatus, w.Code,
				"POST /tasks with %s: unexpected status; body=%s", c.name, w.Body.String())

			if c.wantStatus != http.StatusCreated {
				// Explicit error status text check (error proof): the
				// validation message must actually name the rule, not just
				// return a bare 400.
				assert.Contains(t, w.Body.String(), "priority must be between 1 and 5",
					"expected the shared priority validation message in the error body")
				return
			}

			var tsk gen.Task
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tsk))
			require.NotNil(t, tsk.Priority, "created task must report a priority")
			assert.Equal(t, c.wantPriority, *tsk.Priority,
				"created task priority mismatch")

			// Round-trip: read back via GET and confirm the persisted value
			// survived (not just the create response).
			wGet := httptest.NewRecorder()
			rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tsk.Id, nil)
			rGet.URL.Path = "/api/v1/tasks/" + tsk.Id
			api.HandleTasks(wGet, rGet)
			require.Equal(t, http.StatusOK, wGet.Code, "GET readback must succeed; body=%s", wGet.Body.String())
			var got gen.Task
			require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &got))
			require.NotNil(t, got.Priority)
			assert.Equal(t, c.wantPriority, *got.Priority,
				"GET readback priority mismatch (round-trip)")
		})
	}
}

// TestTaskCreate_PriorityZero_NoTaskPersisted proves the rejected
// priority:0 create does not leave a partial task behind — a 400 must mean
// nothing was written, not "written with the wrong value".
func TestTaskCreate_PriorityZero_NoTaskPersisted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	body := fmt.Sprintf(`{"priority":0,"title":"ShouldNotExist","action":"llm","workspace_id":%q}`, wsID)
	w := postTaskCreate(t, api, body)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())

	wList := httptest.NewRecorder()
	rList := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?workspace_id="+wsID, nil)
	rList.URL.Path = "/api/v1/tasks"
	rList.URL.RawQuery = "workspace_id=" + wsID
	api.HandleTasks(wList, rList)
	require.Equal(t, http.StatusOK, wList.Code)
	assert.NotContains(t, wList.Body.String(), "ShouldNotExist",
		"a rejected priority:0 create must not persist a task")
}
