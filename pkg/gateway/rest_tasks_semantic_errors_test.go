package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Oracle: SFH RR1 N2 and the explicit fix brief. Semantic type failures name
// the offending field/index, but must not echo private input or parser details.
func assertSafeTodoFieldError(t *testing.T, message string, index int, privateValues ...string) {
	t.Helper()
	assert.Contains(t, message, fmt.Sprintf("todos[%d].active_form", index), "semantic error must identify the exact checklist field")
	for _, forbidden := range append([]string{
		"invalid JSON body", "json:", "cannot unmarshal", "unexpected EOF", "invalid character", "syntax error", "byte offset", "decoder",
	}, privateValues...) {
		assert.NotContains(t, strings.ToLower(message), strings.ToLower(forbidden), "controlled semantic error must not expose parser/request contents")
	}
}

func TestTaskTodoNormalizationRetainsEnabledSchemaDiagnostics(t *testing.T) {
	for _, operation := range []string{"create", "patch"} {
		t.Run(operation, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			wsID := ensureTestWorkspace(t, api)
			cfg, cloneErr := api.agentLoop.GetConfig().Clone()
			require.NoError(t, cloneErr)
			cfg.Gateway.ValidateInbound = true
			api.agentLoop.SwapConfig(cfg)
			require.True(t, api.agentLoop.GetConfig().Gateway.ValidateInbound)
			var id string
			if operation == "patch" {
				id = createTaskViaAPI(t, api, "Schema diagnostic proof", wsID).Id
			}
			before, beforeListErr := api.taskStore.List(task.Filter{})
			require.NoError(t, beforeListErr)
			response := writeActiveFormTodosHTTP(t, api, operation, id, wsID, `[{"text":"Valid title","status":"not-a-status","active_form":"Valid form"}]`)
			require.Equal(t, http.StatusBadRequest, response.Code)
			var failure gen.ErrorResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
			schemaName := "TaskCreateRequest"
			if operation == "patch" {
				schemaName = "TaskUpdateRequest"
			}
			require.Contains(t, failure.Error, "request body does not match schema "+schemaName, "normalization must retain the actual optional schema-validation path")
			after, afterListErr := api.taskStore.List(task.Filter{})
			require.NoError(t, afterListErr)
			require.Equal(t, before, after)
		})
	}
}

func TestTaskTodosSemanticTypeErrorsAreActionableAndDoNotWrite(t *testing.T) {
	for _, operation := range []string{"create", "patch", "put", "create_schema_enabled", "patch_schema_enabled"} {
		t.Run(operation, func(t *testing.T) {
			baseOperation := strings.TrimSuffix(operation, "_schema_enabled")
			schemaEnabled := strings.HasSuffix(operation, "_schema_enabled")
			for _, tc := range []struct {
				name      string
				value     any
				malformed bool
			}{
				{name: "second_item_null", value: nil},
				{name: "second_item_number", value: 918273645},
				{name: "second_item_object", value: map[string]any{"private": "PRIVATE_OBJECT_CONTENT"}},
				{name: "malformed_json_remains_generic", malformed: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					api := newTestRestAPIWithHome(t)
					wsID := ensureTestWorkspace(t, api)
					if schemaEnabled {
						cfg, cloneErr := api.agentLoop.GetConfig().Clone()
						require.NoError(t, cloneErr)
						cfg.Gateway.ValidateInbound = true
						api.agentLoop.SwapConfig(cfg)
						require.True(t, api.agentLoop.GetConfig().Gateway.ValidateInbound)
					}
					var id string
					const previous = `[{"text":"Preserve this task","status":"in_progress","active_form":"Preserving this task"}]`
					if baseOperation != "create" {
						id = createTaskViaAPI(t, api, "Semantic error proof", wsID).Id
						seed := putTaskTodos(t, api, id, previous)
						require.Equal(t, http.StatusOK, seed.Code, "positive control: %s", seed.Body.String())
					}
					before, beforeListErr := api.taskStore.List(task.Filter{})
					require.NoError(t, beforeListErr)
					items := []any{
						map[string]any{"text": "FIRST_PRIVATE_TODO", "status": "pending", "active_form": "Valid first form"},
						map[string]any{"text": "SECOND_PRIVATE_TODO", "status": "in_progress", "active_form": tc.value},
					}
					body, encodeErr := json.Marshal(items)
					require.NoError(t, encodeErr)
					if tc.malformed {
						body = []byte(`[{"text":"FIRST_PRIVATE_TODO","status":"pending"},{"active_form":`)
					} else {
						require.True(t, json.Valid(body), "a semantic failure must not be accidentally malformed JSON")
					}
					response := writeActiveFormTodosHTTP(t, api, baseOperation, id, wsID, string(body))
					assert.Equal(t, http.StatusBadRequest, response.Code, "invalid replacement remains rejected: %s", response.Body.String())
					var failure gen.ErrorResponse
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
					if tc.malformed {
						assert.Equal(t, "invalid JSON body", failure.Error, "genuine syntax failure must keep the generic public message")
					} else {
						assertSafeTodoFieldError(t, failure.Error, 1, "FIRST_PRIVATE_TODO", "SECOND_PRIVATE_TODO", "918273645", "PRIVATE_OBJECT_CONTENT")
					}
					after, afterListErr := api.taskStore.List(task.Filter{})
					require.NoError(t, afterListErr)
					require.Equal(t, before, after, "400 must not create or alter any task")
					if id != "" {
						assertTaskActiveFormReadbacks(t, api, id, previous)
					}
				})
			}
		})
	}
}
