package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// not-wire-format: local independently authored Q10 test fixture, not wire types.
type q10FormCase struct {
	Name     string `json:"name"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Reject   bool   `json:"reject"`
}

func gatewayQ10Cases(t *testing.T) []q10FormCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "q10-active-form-normalization.json"))
	require.NoError(t, err)
	var fixture struct {
		Cases []q10FormCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Len(t, fixture.Cases, 38, "all Q10 classes and exact boundaries must actually run")
	return fixture.Cases
}

func q10SchemaControlCases(t *testing.T, all []q10FormCase) []q10FormCase {
	t.Helper()
	var selected []q10FormCase
	for _, tc := range all {
		switch tc.Name {
		case "499_visible_padded_with_bidi", "500_visible_padded_with_bidi", "501_visible_rejected", "500_visible_code_points", "501_invisible_code_points_omitted", "arabic_preserved", "hebrew_preserved", "emoji_family_ZWJ_preserved":
			selected = append(selected, tc)
		}
	}
	require.Len(t, selected, 8, "enabled-schema control must not silently select zero cases")
	return selected
}

// Oracle: founder Q10=A / security F1. The three real HTTP writers must
// normalize before the 500-code-point bound, and every readback is exact.
func TestTaskTodosActiveFormQ10HTTPNormalization(t *testing.T) {
	for _, operation := range []string{"create", "patch", "put", "create_schema_enabled", "patch_schema_enabled"} {
		t.Run(operation, func(t *testing.T) {
			schemaEnabled := strings.HasSuffix(operation, "_schema_enabled")
			baseOperation := strings.TrimSuffix(operation, "_schema_enabled")
			cases := gatewayQ10Cases(t)
			if schemaEnabled {
				cases = q10SchemaControlCases(t, cases)
			}
			for _, tc := range cases {
				t.Run(tc.Name, func(t *testing.T) {
					api := newTestRestAPIWithHome(t)
					wsID := ensureTestWorkspace(t, api)
					if schemaEnabled {
						cfg, err := api.agentLoop.GetConfig().Clone()
						require.NoError(t, err)
						cfg.Gateway.ValidateInbound = true
						api.agentLoop.SwapConfig(cfg)
						require.True(t, api.agentLoop.GetConfig().Gateway.ValidateInbound, "exercise the real create/PATCH raw-schema gate")
					}
					var id string
					const oldTodos = `[{"text":"Keep the previous item","status":"in_progress","active_form":"Keeping the previous item"}]`
					if baseOperation != "create" {
						id = createTaskViaAPI(t, api, "Normalization proof", wsID).Id
						seed := putTaskTodos(t, api, id, oldTodos)
						require.Equal(t, http.StatusOK, seed.Code, "positive control: %s", seed.Body.String())
					}
					before, err := api.taskStore.List(task.Filter{})
					require.NoError(t, err)
					input, err := json.Marshal(tc.Input)
					require.NoError(t, err)
					todos := fmt.Sprintf(`[{"text":"Draft the summary","status":"in_progress","active_form":%s}]`, input)
					response := writeActiveFormTodosHTTP(t, api, baseOperation, id, wsID, todos)
					if tc.Reject {
						require.Equal(t, http.StatusBadRequest, response.Code, "501 normalized points must be rejected: %s", response.Body.String())
						var failure gen.ErrorResponse
						require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
						if schemaEnabled {
							// Q10 specifies rejection, not which enabled validator
							// reports it. HTTP 400 above is the exact error code.
							require.NotEmpty(t, failure.Error, "generated error response must explain the rejection")
						} else {
							require.Equal(t, "todo[0]: active_form must be 500 characters or fewer", failure.Error)
						}
						after, listErr := api.taskStore.List(task.Filter{})
						require.NoError(t, listErr)
						require.Equal(t, before, after, "rejected write must preserve every saved task")
						if id != "" {
							assertTaskActiveFormReadbacks(t, api, id, oldTodos)
						}
						return
					}
					wantStatus := http.StatusOK
					if baseOperation == "create" {
						wantStatus = http.StatusCreated
					}
					require.Equal(t, wantStatus, response.Code, "normalize before validating: %s", response.Body.String())
					var updated gen.Task
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &updated))
					require.NotEmpty(t, updated.Id)
					if id != "" {
						require.Equal(t, id, updated.Id)
					}
					item := map[string]any{"text": "Draft the summary", "status": "in_progress"}
					if tc.Expected != "" {
						item["active_form"] = tc.Expected
					}
					want, err := json.Marshal([]any{item})
					require.NoError(t, err)
					// Nonfatal JSON response failures still fail RED, but let the
					// independent GET and fresh disk readbacks run on the base too.
					var body map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
					assert.JSONEq(t, string(want), string(body["todos"]), "write response must normalize/omit the optional field exactly")
					read := httptest.NewRecorder()
					api.HandleTasks(read, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+updated.Id, nil))
					require.Equal(t, http.StatusOK, read.Code)
					require.NoError(t, json.Unmarshal(read.Body.Bytes(), &body))
					assert.JSONEq(t, string(want), string(body["todos"]), "GET must return the normalized form without losing legitimate joiners")
					saved, err := task.New(api.taskStore.Dir()).Get(updated.Id)
					require.NoError(t, err)
					data, err := json.Marshal(saved.Todos)
					require.NoError(t, err)
					assert.JSONEq(t, string(want), string(data), "fresh saved JSON must normalize/omit the optional field exactly")
				})
			}
		})
	}
}
