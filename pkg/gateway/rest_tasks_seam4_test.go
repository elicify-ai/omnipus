//go:build !cgo

// rest_tasks_seam4_test.go — SEAM 4 reconciliation test (worktree
// wf/w1-seams): proves POST /api/v1/tasks and PATCH /api/v1/tasks/{id}
// actually read/write the ADR-053 write_set/stream/is_join plan-member
// fields end-to-end through the REST surface, using ONLY the generated wire
// types (Constraint #8).

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

// TestTaskCreate_WriteSetStreamIsJoin_PersistAndRoundTrip proves POST
// /api/v1/tasks reads write_set/stream/is_join from the generated
// TaskCreateRequest, persists them, and toWireTask emits them back on both
// the create response and a subsequent GET.
func TestTaskCreate_WriteSetStreamIsJoin_PersistAndRoundTrip(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	body := fmt.Sprintf(
		`{"title":"shard A","action":"llm","workspace_id":%q,"write_set":["pkg/plan/plan_lint.go","pkg/plan/plan_lint_test.go"],"stream":"stream-schema","is_join":true}`,
		wsID,
	)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/tasks"
	api.HandleTasks(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "POST /tasks must return 201; body=%s", w.Body.String())

	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	require.NotNil(t, created.WriteSet, "create response must echo write_set")
	assert.Equal(t, []string{"pkg/plan/plan_lint.go", "pkg/plan/plan_lint_test.go"}, *created.WriteSet)
	require.NotNil(t, created.Stream, "create response must echo stream")
	assert.Equal(t, "stream-schema", *created.Stream)
	require.NotNil(t, created.IsJoin, "create response must echo is_join")
	assert.True(t, *created.IsJoin)

	// Round-trip via GET: proves the fields were actually persisted to disk,
	// not just reflected from the in-memory create response.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+created.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + created.Id
	api.HandleTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "GET /tasks/{id} must return 200; body=%s", wGet.Body.String())

	var reloaded gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &reloaded))
	require.NotNil(t, reloaded.WriteSet)
	assert.Equal(t, []string{"pkg/plan/plan_lint.go", "pkg/plan/plan_lint_test.go"}, *reloaded.WriteSet)
	require.NotNil(t, reloaded.Stream)
	assert.Equal(t, "stream-schema", *reloaded.Stream)
	require.NotNil(t, reloaded.IsJoin)
	assert.True(t, *reloaded.IsJoin)
}

// TestTaskPatch_WriteSetStreamIsJoin_UpdatesAndClears proves PATCH
// /api/v1/tasks/{id} reads write_set/stream/is_join from the (now-extended)
// generated TaskUpdateRequest and applies them via task.Patch — the gap
// this seam closes: TaskUpdateRequest previously had no such fields at all.
func TestTaskPatch_WriteSetStreamIsJoin_UpdatesAndClears(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	createBody := fmt.Sprintf(`{"title":"member","action":"llm","workspace_id":%q}`, wsID)
	wCreate := httptest.NewRecorder()
	rCreate := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(createBody))
	rCreate.Header.Set("Content-Type", "application/json")
	rCreate.URL.Path = "/api/v1/tasks"
	api.HandleTasks(wCreate, rCreate)
	require.Equal(t, http.StatusCreated, wCreate.Code, "create must return 201; body=%s", wCreate.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(wCreate.Body.Bytes(), &created))
	require.Nil(t, created.WriteSet, "a task created with no write_set must not have one")

	// PATCH: set write_set/stream/is_join on an existing task.
	patchBody := `{"write_set":["a.txt","b.txt"],"stream":"stream-x","is_join":true}`
	wPatch := httptest.NewRecorder()
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+created.Id, strings.NewReader(patchBody))
	rPatch.Header.Set("Content-Type", "application/json")
	rPatch.URL.Path = "/api/v1/tasks/" + created.Id
	api.HandleTasks(wPatch, rPatch)
	require.Equal(t, http.StatusOK, wPatch.Code, "PATCH must return 200; body=%s", wPatch.Body.String())

	var patched gen.Task
	require.NoError(t, json.Unmarshal(wPatch.Body.Bytes(), &patched))
	require.NotNil(t, patched.WriteSet)
	assert.Equal(t, []string{"a.txt", "b.txt"}, *patched.WriteSet)
	require.NotNil(t, patched.Stream)
	assert.Equal(t, "stream-x", *patched.Stream)
	require.NotNil(t, patched.IsJoin)
	assert.True(t, *patched.IsJoin)

	// PATCH again: provided-empty write_set + empty-string stream CLEAR them
	// (mirrors blocked_by's three-way convention); is_join flips to false.
	clearBody := `{"write_set":[],"stream":"","is_join":false}`
	wClear := httptest.NewRecorder()
	rClear := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+created.Id, strings.NewReader(clearBody))
	rClear.Header.Set("Content-Type", "application/json")
	rClear.URL.Path = "/api/v1/tasks/" + created.Id
	api.HandleTasks(wClear, rClear)
	require.Equal(t, http.StatusOK, wClear.Code, "PATCH (clear) must return 200; body=%s", wClear.Body.String())

	var cleared gen.Task
	require.NoError(t, json.Unmarshal(wClear.Body.Bytes(), &cleared))
	assert.Nil(t, cleared.WriteSet, "an empty write_set patch must clear it (omitted on the wire)")
	assert.Nil(t, cleared.Stream, "an empty-string stream patch must clear it (omitted on the wire)")
	assert.Nil(t, cleared.IsJoin, "is_join:false must clear it (omitted on the wire, matching toWireTask's zero-value-omits convention)")
}
