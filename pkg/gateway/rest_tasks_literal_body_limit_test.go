package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Oracle: ARCH-RR1-1 / explicit mid-round founder addition. The cap is on
// incoming bytes, not the HTML-escaped representation produced internally.
func TestTaskLiteralTodoBodyLimitAppliesOnlyToIncomingBytes(t *testing.T) {
	const incomingLimit = 1 << 20 // Existing 1,048,576-byte wire limit; do not raise it.
	const todoCount = 400
	const lessThanCount = 450
	activeForm := "Checking " + strings.Repeat("<", lessThanCount)
	require.Len(t, []rune(activeForm), 459, "architect's valid active_form stays below the 500-code-point bound")
	todos := make([]task.Todo, todoCount)
	for index := range todos {
		todos[index] = task.Todo{Text: "Compare markup", Status: task.TodoInProgress, ActiveForm: activeForm}
	}
	for _, operation := range []string{"create", "patch"} {
		for _, schemaEnabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/schema_%t", operation, schemaEnabled), func(t *testing.T) {
				for _, overLimit := range []bool{false, true} {
					t.Run(fmt.Sprintf("incoming_over_limit_%t", overLimit), func(t *testing.T) {
						api := newTestRestAPIWithHome(t)
						wsID := ensureTestWorkspace(t, api)
						cfg, cloneErr := api.agentLoop.GetConfig().Clone()
						require.NoError(t, cloneErr)
						cfg.Gateway.ValidateInbound = schemaEnabled
						api.agentLoop.SwapConfig(cfg)
						require.Equal(t, schemaEnabled, api.agentLoop.GetConfig().Gateway.ValidateInbound)
						var id string
						method, path := http.MethodPost, "/api/v1/tasks"
						if operation == "patch" {
							id = createTaskViaAPI(t, api, "Literal body limit", wsID).Id
							method, path = http.MethodPatch, "/api/v1/tasks/"+id
						}
						before, beforeListErr := api.taskStore.List(task.Filter{})
						require.NoError(t, beforeListErr)
						body := literalTodoRequestBody(t, operation, wsID, todos)
						require.True(t, json.Valid(body))
						require.Less(t, len(body), incomingLimit, "original request must fit the incoming cap")
						require.Equal(t, todoCount*lessThanCount, bytes.Count(body, []byte("<")), "test instrument must send literal '<' bytes, not HTML-safe escapes")
						escapedLessThan := string([]byte{0x5c, 'u', '0', '0', '3', 'c'})
						require.NotContains(t, string(body), escapedLessThan, "ordinary json.Marshal would not reproduce this boundary")
						t.Logf("original_incoming_bytes=%d cap=%d literal_less_than_bytes=%d", len(body), incomingLimit, bytes.Count(body, []byte("<")))
						if overLimit {
							// JSON whitespace changes only transport size, not fields,
							// item count or decoded values. It cannot fail a field cap.
							body = append(append([]byte{'{'}, bytes.Repeat([]byte(" "), incomingLimit)...), body[1:]...)
							require.Greater(t, len(body), incomingLimit)
							require.True(t, json.Valid(body), "over-cap control is still syntactically valid JSON")
							t.Logf("over_limit_incoming_bytes=%d", len(body))
						}
						request := httptest.NewRequest(method, path, bytes.NewReader(body))
						request.Header.Set("Content-Type", "application/json")
						response := httptest.NewRecorder()
						api.HandleTasks(response, request)
						if overLimit {
							require.Contains(t, []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge}, response.Code, "over-cap incoming bytes must still be rejected: %s", response.Body.String())
							after, afterListErr := api.taskStore.List(task.Filter{})
							require.NoError(t, afterListErr)
							require.Equal(t, before, after, "over-cap request must not write anything")
							return
						}
						wantStatus := http.StatusCreated
						if operation == "patch" {
							wantStatus = http.StatusOK
						}
						require.Equal(t, wantStatus, response.Code, "valid incoming bytes must not be truncated after normalization: %s", response.Body.String())
						var result gen.Task
						require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
						require.NotEmpty(t, result.Id)
						if id != "" {
							require.Equal(t, id, result.Id)
						}
						saved, readErr := task.New(api.taskStore.Dir()).Get(result.Id)
						require.NoError(t, readErr)
						require.Equal(t, todos, saved.Todos, "all 400 stored values must remain byte-identical")
						want, encodeErr := json.Marshal(todos) // Expected-value comparison only, never the incoming body.
						require.NoError(t, encodeErr)
						assertTaskTodosResponseJSON(t, response.Body.Bytes(), string(want))
						assertTaskActiveFormReadbacks(t, api, result.Id, string(want))
					})
				}
			})
		}
	}
}

func literalTodoRequestBody(t *testing.T, operation, workspaceID string, todos []task.Todo) []byte {
	t.Helper()
	body := map[string]any{"todos": todos}
	if operation == "create" {
		body["title"], body["action"], body["workspace_id"] = "Literal body limit", "llm", workspaceID
		body["criteria"], body["dod"] = json.RawMessage(validCriteriaJSON), json.RawMessage(validDoDJSON)
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false) // Crucial: the wire body must contain literal '<'.
	require.NoError(t, encoder.Encode(body))
	return output.Bytes()
}
