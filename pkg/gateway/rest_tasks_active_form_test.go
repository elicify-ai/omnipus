package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
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
