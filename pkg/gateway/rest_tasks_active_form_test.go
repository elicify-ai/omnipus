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
	"github.com/elicify-ai/omnipus/pkg/task"
)

// The founder's 2026-10-21 requirement preserves active_form on all todo readbacks.
func TestTaskTodosActiveForm(t *testing.T) {
	const todosJSON = `[{"text":"Draft the Q4 summary","status":"in_progress","active_form":"Drafting the Q4 summary"}]`
	for _, operation := range []string{"create", "patch", "put"} {
		t.Run(operation, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			wsID := ensureTestWorkspace(t, api)
			var response *httptest.ResponseRecorder
			var id string
			if operation == "create" {
				body := fmt.Sprintf(`{"title":"Quarter plan","action":"llm","workspace_id":%q,"criteria":%s,"dod":%s,"todos":%s}`,
					wsID, validCriteriaJSON, validDoDJSON, todosJSON)
				response = httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				api.HandleTasks(response, r)
				require.Equal(t, http.StatusCreated, response.Code, "create: %s", response.Body.String())
			} else {
				card := createTaskViaAPI(t, api, "Quarter plan", wsID)
				id = card.Id
				if operation == "patch" {
					response = patchTask(t, api, id, `{"todos":`+todosJSON+`}`)
				} else {
					response = putTaskTodos(t, api, id, todosJSON)
				}
				require.Equal(t, http.StatusOK, response.Code, "%s: %s", operation, response.Body.String())
			}
			assertTaskTodosResponseJSON(t, response.Body.Bytes(), todosJSON)
			var updated gen.Task
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &updated))
			id = updated.Id
			require.NotEmpty(t, id)
			data, err := json.Marshal(updated.Todos)
			require.NoError(t, err)
			require.JSONEq(t, todosJSON, string(data), "mutation response must preserve active_form")

			read := httptest.NewRecorder()
			api.HandleTasks(read, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil))
			require.Equal(t, http.StatusOK, read.Code, "read: %s", read.Body.String())
			assertTaskTodosResponseJSON(t, read.Body.Bytes(), todosJSON)
			var reloaded gen.Task
			require.NoError(t, json.Unmarshal(read.Body.Bytes(), &reloaded))
			data, err = json.Marshal(reloaded.Todos)
			require.NoError(t, err)
			require.JSONEq(t, todosJSON, string(data), "GET must preserve active_form")

			saved, err := api.taskStore.Get(id)
			require.NoError(t, err)
			data, err = json.Marshal(saved.Todos)
			require.NoError(t, err)
			require.JSONEq(t, todosJSON, string(data), "persisted todos must preserve active_form")

			withoutForm := `[{"text":"Publish the summary","status":"completed"}]`
			replaced := putTaskTodos(t, api, id, withoutForm)
			require.Equal(t, http.StatusOK, replaced.Code, "replace: %s", replaced.Body.String())
			t.Logf("replacement response: %s", replaced.Body.String())
			assertTaskTodosResponseJSON(t, replaced.Body.Bytes(), withoutForm)
			var replacement gen.Task
			require.NoError(t, json.Unmarshal(replaced.Body.Bytes(), &replacement))
			data, err = json.Marshal(replacement.Todos)
			require.NoError(t, err)
			require.JSONEq(t, withoutForm, string(data), "replacement without active_form must omit it")
			saved, err = api.taskStore.Get(id)
			require.NoError(t, err)
			data, err = json.Marshal(saved.Todos)
			require.NoError(t, err)
			require.JSONEq(t, withoutForm, string(data), "saved replacement without active_form must omit it")
		})
	}
}

// Oracle: fix-round-1 dispatch item 2 / CHECK F3 and the canonical Todo
// schema: optional string, at most 500 Unicode code points, never JSON null.
// HTTP conversion and storage remain real on all three write paths.
func TestTaskTodosActiveFormHTTPNormalization(t *testing.T) {
	cases := []struct {
		name      string
		input     any
		wantForm  string
		wantError string
	}{
		{name: "surrounding_whitespace", input: " \tDrafting the Q4 summary\n ", wantForm: "Drafting the Q4 summary"},
		{name: "whitespace_only_omitted", input: " \t\n "},
		{name: "empty_omitted", input: ""},
		{name: "one_code_point", input: "𝑥", wantForm: "𝑥"},
		{name: "two_code_points", input: "𝑥界", wantForm: "𝑥界"},
		{name: "499_code_points", input: strings.Repeat("a", 499), wantForm: strings.Repeat("a", 499)},
		{name: "500_code_points", input: strings.Repeat("界", 500), wantForm: strings.Repeat("界", 500)},
		{name: "500_supplementary_code_points", input: strings.Repeat("𝑥", 500), wantForm: strings.Repeat("𝑥", 500)},
		{name: "trim_before_500_point_limit", input: " \t" + strings.Repeat("𝑥", 500) + "\n ", wantForm: strings.Repeat("𝑥", 500)},
		{name: "501_code_points_rejected", input: strings.Repeat("a", 501), wantError: "todo[0]: active_form must be 500 characters or fewer"},
		{name: "501_supplementary_code_points_rejected", input: strings.Repeat("𝑥", 501), wantError: "todo[0]: active_form must be 500 characters or fewer"},
		{name: "number_rejected", input: 42, wantError: "invalid JSON body"},
		{name: "boolean_rejected", input: true, wantError: "invalid JSON body"},
		{name: "object_rejected", input: map[string]any{"form": "Drafting"}, wantError: "invalid JSON body"},
		{name: "array_rejected", input: []string{"Drafting"}, wantError: "invalid JSON body"},
		{name: "null_rejected", input: nil, wantError: "invalid JSON body"},
	}
	for _, operation := range []string{"create", "patch", "put"} {
		t.Run(operation, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					api := newTestRestAPIWithHome(t)
					wsID := ensureTestWorkspace(t, api)
					var id string
					if operation != "create" {
						id = createTaskViaAPI(t, api, "Existing task", wsID).Id
						seed := putTaskTodos(t, api, id, `[{"text":"Keep this checklist","status":"in_progress","active_form":"Keeping this checklist"}]`)
						require.Equal(t, http.StatusOK, seed.Code, "positive control: %s", seed.Body.String())
					}
					before, err := api.taskStore.List(task.Filter{})
					require.NoError(t, err)
					formJSON, err := json.Marshal(tc.input)
					require.NoError(t, err)
					todos := fmt.Sprintf(`[{"text":"Draft the Q4 summary","status":"in_progress","active_form":%s}]`, formJSON)
					response := writeActiveFormTodosHTTP(t, api, operation, id, wsID, todos)
					if tc.wantError != "" {
						assert.Equal(t, http.StatusBadRequest, response.Code, "invalid active_form must be rejected before writing: %s", response.Body.String())
						var failure gen.ErrorResponse
						require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
						assert.Equal(t, tc.wantError, failure.Error, "specific validation response")
						after, err := api.taskStore.List(task.Filter{})
						require.NoError(t, err)
						require.Equal(t, before, after, "rejected create must add no task; rejected replacement must preserve the entire old task")
						if id != "" {
							assertTaskActiveFormReadbacks(t, api, id, `[{"text":"Keep this checklist","status":"in_progress","active_form":"Keeping this checklist"}]`)
						}
						return
					}
					wantStatus := http.StatusOK
					if operation == "create" {
						wantStatus = http.StatusCreated
					}
					require.Equal(t, wantStatus, response.Code, "valid active_form: %s", response.Body.String())
					var result gen.Task
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
					require.NotEmpty(t, result.Id)
					if id != "" {
						require.Equal(t, id, result.Id)
					}
					wantItem := map[string]any{"text": "Draft the Q4 summary", "status": "in_progress"}
					if tc.wantForm != "" {
						wantItem["active_form"] = tc.wantForm
					}
					want, err := json.Marshal([]any{wantItem})
					require.NoError(t, err)
					assertTaskTodosResponseJSON(t, response.Body.Bytes(), string(want))
					assertTaskActiveFormReadbacks(t, api, result.Id, string(want))
				})
			}
		})
	}
}

func writeActiveFormTodosHTTP(t *testing.T, api *restAPI, operation, id, wsID, todos string) *httptest.ResponseRecorder {
	t.Helper()
	switch operation {
	case "create":
		body := fmt.Sprintf(`{"title":"Quarter plan","action":"llm","workspace_id":%q,"criteria":%s,"dod":%s,"todos":%s}`, wsID, validCriteriaJSON, validDoDJSON, todos)
		response := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		api.HandleTasks(response, r)
		return response
	case "patch":
		return patchTask(t, api, id, `{"todos":`+todos+`}`)
	case "put":
		return putTaskTodos(t, api, id, todos)
	default:
		t.Fatalf("unhandled HTTP operation %q", operation)
		return nil
	}
}

func assertTaskActiveFormReadbacks(t *testing.T, api *restAPI, id, want string) {
	t.Helper()
	read := httptest.NewRecorder()
	api.HandleTasks(read, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil))
	require.Equal(t, http.StatusOK, read.Code, "GET readback: %s", read.Body.String())
	assertTaskTodosResponseJSON(t, read.Body.Bytes(), want)
	saved, err := api.taskStore.Get(id)
	require.NoError(t, err)
	data, err := json.Marshal(saved.Todos)
	require.NoError(t, err)
	require.JSONEq(t, want, string(data), "persisted todos must be normalized exactly, including blank-field omission")
}

func assertTaskTodosResponseJSON(t *testing.T, body []byte, want string) {
	t.Helper()
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &response))
	require.JSONEq(t, want, string(response["todos"]), "raw response todos must preserve the exact shape")
}
