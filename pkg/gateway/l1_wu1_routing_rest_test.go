package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B2/K14 key ruling (chief, 2026-10-13): L1 retains default_agent_id as
// the required bound MAIN identity; only the global fallback meaning is
// deleted. K19 renames the key to agent_id with the L2 consumers.
func l1WU1RoutingAPI(t *testing.T, validation bool) *restAPI {
	t.Helper()
	api := buildExecutorTestAPI(t)
	writeTestWorkspaceJSON(t, api, "sales", "active", []string{"test-agent"}, false)
	seedChannelInstance(t, api, "telegram.red")
	api.agentLoop.GetConfig().Gateway.ValidateInbound = validation
	return api
}

func TestL1WU1ChannelRoutingWrite_RequiresNonblankPairValidationOnOff(t *testing.T) {
	for _, validation := range []bool{false, true} {
		mode := "validation_off"
		if validation {
			mode = "validation_on"
		}
		t.Run(mode, func(t *testing.T) {
			for _, row := range []struct {
				name, body string
				semantic   bool
			}{
				{"neither", `{}`, true},
				{"agent_only", `{"default_agent_id":"test-agent"}`, true},
				{"workspace_only", `{"workspace_id":"sales"}`, true},
				{"blank_agent", `{"workspace_id":"sales","default_agent_id":""}`, true},
				{"blank_workspace", `{"workspace_id":"","default_agent_id":"test-agent"}`, true},
				{"whitespace_pair", `{"workspace_id":"  ","default_agent_id":"  "}`, true},
				{"null_agent", `{"workspace_id":"sales","default_agent_id":null}`, true},
				{"null_workspace", `{"workspace_id":null,"default_agent_id":"test-agent"}`, true},
				{"wrong_type", `{"workspace_id":42,"default_agent_id":"test-agent"}`, false},
				{"extra_field", `{"workspace_id":"sales","default_agent_id":"test-agent","unexpected":true}`, false},
				{"malformed", `{"workspace_id":`, false},
			} {
				t.Run(row.name, func(t *testing.T) {
					api := l1WU1RoutingAPI(t, validation)
					before, err := os.ReadFile(api.configPath())
					require.NoError(t, err)
					w := setChannelRoutingReq(t, api, "telegram.red", row.body)
					assert.Equal(t, http.StatusBadRequest, w.Code, "FR-127 / BDD-13.8: %s", w.Body.String())
					if row.semantic && (!validation || row.name == "whitespace_pair") {
						assert.Contains(t, w.Body.String(), "bind this channel to an agent in a workspace", "§6.2 / chief Q2 A: whitespace needs the semantic refusal even with validation ON")
					} else if row.semantic {
						assert.Contains(t, w.Body.String(), "ChannelRoutingUpdateRequest", "schema-invalid ON requests must name their schema")
					}
					after, err := os.ReadFile(api.configPath())
					require.NoError(t, err)
					assert.Equal(t, before, after, "rejected binding must not silently enter legacy unbound mode")
				})
			}
			t.Run("valid_bound_pair_positive_control", func(t *testing.T) {
				api := l1WU1RoutingAPI(t, validation)
				w := setChannelRoutingReq(t, api, "telegram.red", `{"workspace_id":"sales","default_agent_id":"test-agent"}`)
				require.Equal(t, http.StatusOK, w.Code, "valid binding must actually persist: %s", w.Body.String())
				instance := api.agentLoop.GetConfig().Channels["telegram.red"]
				assert.Equal(t, "sales", instance.WorkspaceID)
				require.NotNil(t, instance.Identity)
				assert.Equal(t, config.ChannelIdentity{Kind: "agent", ID: "test-agent"}, *instance.Identity)
			})
		})
	}
}

func TestL1WU1ChannelRoutingGET_UnboundIsTruthfulNeverWildcardDestination(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		label := "empty_unbound"
		if legacy {
			label = "legacy_named_unbound"
		}
		t.Run(label, func(t *testing.T) {
			api := l1WU1RoutingAPI(t, false)
			if legacy {
				require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
					m["bindings"] = []any{map[string]any{"agent_id": "test-agent", "match": map[string]any{"channel": "telegram.red", "account_id": "*"}}}
					return nil
				}))
			}
			w := getChannelRoutingReq(t, api, "telegram.red")
			require.Equal(t, http.StatusOK, w.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, false, body["bound"], "FR-128/K14: unbound must be an explicit truthful state")
			assert.Equal(t, "", body["workspace_id"], "FR-128: no workspace invented")
			assert.Equal(t, "", body["default_agent_id"], "FR-128: a legacy wildcard is not a bound destination")
		})
	}
}

func TestL1WU1AgentWire_B1RemovesDefaultAndRejectsDefaultWrites(t *testing.T) {
	// FR-120 / B1 / BDD-13.5. Inspect JSON field presence, not the generated
	// type: the old generated type itself would force the retired default field.
	api := buildExecutorTestAPI(t)
	for _, url := range []string{"/api/v1/agents", "/api/v1/agents/test-agent"} {
		t.Run(url, func(t *testing.T) {
			w := httptest.NewRecorder()
			api.HandleAgents(w, httptest.NewRequest(http.MethodGet, url, nil))
			require.Equal(t, http.StatusOK, w.Code)
			if url == "/api/v1/agents" {
				var rows []map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
				require.NotEmpty(t, rows, "instrument control: an empty response cannot prove retired field absence")
				for _, ac := range rows {
					assert.NotContains(t, ac, "default", "B1: Agent.default must be removed, not false")
				}
			} else {
				var ac map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ac))
				assert.Equal(t, "test-agent", ac["id"], "response identity positive control")
				assert.NotContains(t, ac, "default", "B1: detail must omit the retired field")
			}
		})
	}
	for _, validation := range []bool{false, true} {
		t.Run(map[bool]string{false: "write_validation_off", true: "write_validation_on"}[validation], func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			api.agentLoop.GetConfig().Gateway.ValidateInbound = validation
			before, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			store := agentstore.New(api.homePath)
			beforeAgent, err := store.ReadState("test-agent")
			require.NoError(t, err)
			w := httptest.NewRecorder()
			current := httptest.NewRecorder()
			api.HandleAgents(current, httptest.NewRequest(http.MethodGet, "/api/v1/agents/test-agent", nil))
			require.Equal(t, http.StatusOK, current.Code)
			var state map[string]any
			require.NoError(t, json.Unmarshal(current.Body.Bytes(), &state))
			body, err := json.Marshal(map[string]any{"revision": state["revision"], "description": "must-not-save", "default": true})
			require.NoError(t, err)
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(string(body)))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code, "B1: removed default write must fail loudly, never silently ignore: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "default")
			after, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			assert.Equal(t, before, after, "retired default write must not change any setting")
			afterAgent, err := store.ReadState("test-agent")
			require.NoError(t, err)
			assert.Equal(t, beforeAgent, afterAgent, "retired default field must refuse before ordinary edits are persisted")
		})
	}
}
