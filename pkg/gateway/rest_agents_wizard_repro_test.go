package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateAgent_WizardRealMuxRoundTrip reproduces the untouched Main wizard
// in create-agent.spec.ts. The request comes from the actual central catalogue,
// exactly like CreateAgentModal::payloadToCreateRequest + resolveToolsCfg's
// Balanced preset. Never pad it with the validation universe: that would hide
// the request which returned 400 in stage-1 candidate CI at 45f2659a0.
// REST create remains supported by system-agent FR-108 / ADR OSA-10 U4.
func TestCreateAgent_WizardRealMuxRoundTrip(t *testing.T) {
	for _, validateInbound := range []bool{false, true} {
		t.Run(fmt.Sprintf("validate_inbound=%t", validateInbound), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			api := l1WU2WiredPermissionAPI(t)
			api.agentLoop.GetConfig().Gateway.ValidateInbound = validateInbound
			api.builtinRegistry, _ = buildCentralBuiltinRegistry(nil)
			t.Setenv("OMNIPUS_BEARER_TOKEN", "wizard-reproduction-token")

			mux := http.NewServeMux()
			api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
			// The collection route is registered separately in gateway_boot.go.
			mux.Handle("/api/v1/agents", api.withAuth(api.HandleAgents))
			chain := buildProductionMiddlewareChain(api, mux)
			request := func(method, path string, body []byte) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path, bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer wizard-reproduction-token")
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				chain.ServeHTTP(rec, req)
				return rec
			}

			catalogue := request(http.MethodGet, "/api/v1/tools", nil)
			require.Equal(t, http.StatusOK, catalogue.Code, "%s", catalogue.Body.String())
			var entries []gen.ToolRegistryEntry
			require.NoError(t, json.Unmarshal(catalogue.Body.Bytes(), &entries))
			require.NotEmpty(t, entries, "the request must use a real, nonempty catalogue")

			// Borrow only the generated nested request shape; discard every
			// populated fixture value before authoring the wizard's policy map.
			cfg := gen.FixtureAgentCreateRequestMain_Populated().ToolsCfg
			cfg.Mcp = nil
			cfg.Builtin.Policies = make(map[string]gen.AgentCreateRequestMainToolsCfgBuiltinPolicies, len(entries))
			for _, entry := range entries {
				policy := gen.AgentCreateRequestMainToolsCfgBuiltinPoliciesAllow
				switch entry.Name {
				case "bash", "browser_navigate", "browser_click", "browser_type", "write_file":
					policy = gen.AgentCreateRequestMainToolsCfgBuiltinPoliciesAsk
				case "browser_evaluate":
					policy = gen.AgentCreateRequestMainToolsCfgBuiltinPoliciesDeny
				}
				cfg.Builtin.Policies[entry.Name] = policy
			}
			assert.Equal(t, gen.AgentCreateRequestMainToolsCfgBuiltinPoliciesAsk, cfg.Builtin.Policies["bash"])
			assert.Equal(t, gen.AgentCreateRequestMainToolsCfgBuiltinPoliciesAllow, cfg.Builtin.Policies["get_agent"])

			color := gen.AgentColor("#8E9196")
			model, provider := "deepseek/deepseek-v4.1-flash", "openrouter"
			body := gen.AgentCreateRequestMain{
				Type: gen.AgentCreateRequestMainTypeMain, Name: "E2E Test Agent 1791985800000",
				Soul: "You are a helpful E2E test agent.", Color: &color,
				Model: &model, Provider: &provider, ToolsCfg: cfg,
			}
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			t.Logf("WIZARD_REQUEST=%s", raw)
			createdResponse := request(http.MethodPost, "/api/v1/agents", raw)
			t.Logf("WIZARD_RESPONSE status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
			assert.Equal(t, http.StatusCreated, createdResponse.Code, "%s", createdResponse.Body.String())
			if createdResponse.Code == http.StatusCreated {
				created := decodeAgentResp(t, createdResponse.Body.Bytes())
				state, readErr := agentstore.New(api.homePath).ReadState(created.Id)
				require.NoError(t, readErr)
				assert.Equal(t, body.Name, state.Agent.Name)
				assert.Equal(t, body.Soul, state.Soul)
				assert.Equal(t, string(color), state.Agent.Color)
			}

			// Same identity, model and instructions; only optional tools_cfg
			// is absent. This discriminates policy-map failure from name/default,
			// palette and native-runtime checks without bypassing the real mux.
			body.Name = "Wizard no-tools control"
			body.ToolsCfg = nil
			controlRaw, marshalErr := json.Marshal(body)
			require.NoError(t, marshalErr)
			control := request(http.MethodPost, "/api/v1/agents", controlRaw)
			t.Logf("WIZARD_CONTROL status=%d body=%s", control.Code, control.Body.String())
			require.Equal(t, http.StatusCreated, control.Code, "%s", control.Body.String())
			controlAgent := decodeAgentResp(t, control.Body.Bytes())
			state, readErr := agentstore.New(api.homePath).ReadState(controlAgent.Id)
			require.NoError(t, readErr)
			assert.Equal(t, body.Name, state.Agent.Name)
			assert.Equal(t, body.Soul, state.Soul)
			assert.Equal(t, string(color), state.Agent.Color)
		})
	}
}
