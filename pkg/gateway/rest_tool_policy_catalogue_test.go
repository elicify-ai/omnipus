package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Chief ruling for CI round 1 (2026-10-14): full replacement maps require
// implemented central-catalogue names, not future reservations. Reserved keys
// are accepted when supplied. An omitted, already-stored reservation survives
// replacement; a reservation never stored must not be backfilled.
func TestRESTBuiltinPolicyMap_CurrentCatalogue(t *testing.T) {
	cases := []struct {
		name         string
		storedFuture bool
		supplied     string
		omit         string
		invalid      string
		wantError    string
	}{
		{name: "current catalogue preserves stored reservation", storedFuture: true},
		{name: "no stored reservation gains none"},
		{name: "supplied reserved deny accepted", storedFuture: true, supplied: "deny"},
		{name: "supplied reserved allow follows override intent", storedFuture: true, supplied: "allow"},
		{name: "missing implemented key refused", storedFuture: true, omit: "bash",
			wantError: "is incomplete — 1 static builtin tool(s) have no explicit policy entry: bash"},
		{name: "unknown key refused", storedFuture: true, invalid: "not_a_builtin_tool",
			wantError: "contains 1 key(s) that are neither a known static builtin tool name nor an mcp_-namespaced MCP key (wildcards are not valid for the static builtin catalog): not_a_builtin_tool"},
		{name: "wildcard refused", storedFuture: true, invalid: "*",
			wantError: "contains 1 key(s) that are neither a known static builtin tool name nor an mcp_-namespaced MCP key (wildcards are not valid for the static builtin catalog): *"},
	}
	for _, validateInbound := range []bool{false, true} {
		for _, route := range []string{"create", "agent", "tools"} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("strict=%t/%s/%s", validateInbound, route, tc.name), func(t *testing.T) {
					api, chain := policyCatalogueTestAPI(t, validateInbound)
					policies := policyCatalogueFromGET(t, chain)
					_, futureImplemented := policies["connect_mailbox"]
					require.False(t, futureImplemented,
						"the reservation fixture must name a reserved, not-yet-implemented tool; connect_mailbox is a landing-2 tool (shipped ceiling ask, no central-catalogue implementation). WU-6 implemented the old example remove_secret, so the example moved — update it again only when connect_mailbox lands")
					before := preparePolicyReservationFixture(t, api, policies, tc.storedFuture)
					api.agentLoop.GetConfig().Gateway.ValidateInbound = validateInbound
					rosterBefore := createBoundaryRoster(t, api)
					if tc.supplied != "" {
						policies["connect_mailbox"] = tc.supplied
					}
					delete(policies, tc.omit)
					if tc.invalid != "" {
						policies[tc.invalid] = "deny"
					}
					method, target, raw := policyCatalogueWriteBody(t, route, before.Revision, policies)
					rec := policyCatalogueRequest(chain, method, target, raw)
					t.Logf("POLICY_CATALOGUE_CASE route=%s status=%d body=%s", route, rec.Code, rec.Body.String())
					if tc.wantError != "" {
						require.Equal(t, http.StatusBadRequest, rec.Code, "%s", rec.Body.String())
						var refusal gen.ErrorResponse
						require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refusal))
						prefix := "tools_cfg.builtin.policies "
						if route == "tools" {
							prefix = "builtin.policies "
						}
						assert.Equal(t, prefix+tc.wantError, refusal.Error)
						after, err := agentstore.New(api.homePath).ReadState("test-agent")
						require.NoError(t, err)
						assert.Equal(t, before, after, "refusal must not change the stored record or revision")
						assert.Equal(t, rosterBefore, createBoundaryRoster(t, api), "refusal must not create a partial agent")
						return
					}
					wantStatus, id := http.StatusOK, "test-agent"
					if route == "create" {
						wantStatus = http.StatusCreated
					}
					assert.Equal(t, wantStatus, rec.Code, "%s", rec.Body.String())
					if rec.Code != wantStatus {
						return
					}
					if route == "create" {
						id = decodeAgentResp(t, rec.Body.Bytes()).Id
					}
					state, err := agentstore.New(api.homePath).ReadState(id)
					require.NoError(t, err)
					for name, policy := range policies {
						assert.Equal(t, config.ToolPolicy(policy), state.Agent.Tools.Builtin.Policies[name], "submitted %s must persist", name)
					}
					future, present := state.Agent.Tools.Builtin.Policies["connect_mailbox"]
					switch {
					case tc.supplied != "":
						assert.True(t, present)
						assert.Equal(t, config.ToolPolicy(tc.supplied), future)
					case route == "create" || tc.storedFuture:
						assert.True(t, present, "existing/constructor reservation must survive")
						assert.Equal(t, config.ToolPolicyDeny, future)
					default:
						assert.False(t, present, "a never-stored reservation must not be backfilled")
					}
					if route != "create" {
						expected := make(map[string]config.ToolPolicy, len(policies)+1)
						for name, policy := range policies {
							expected[name] = config.ToolPolicy(policy)
						}
						if tc.storedFuture && tc.supplied == "" {
							expected["connect_mailbox"] = config.ToolPolicyDeny
						}
						assert.Equal(t, expected, state.Agent.Tools.Builtin.Policies, "only supplied and previously stored reserved entries may persist")
					}
				})
			}
		}
	}
}

func policyCatalogueTestAPI(t *testing.T, strict bool) (*restAPI, http.Handler) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	api := buildExecutorTestAPI(t)
	api.agentLoop.GetConfig().Gateway.ValidateInbound = strict
	api.agentLoop.GetConfig().Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	api.builtinRegistry, _ = buildCentralBuiltinRegistry(nil)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "policy-catalogue-test-token")
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	mux.Handle("/api/v1/agents", api.withAuth(api.HandleAgents))
	return api, buildProductionMiddlewareChain(api, mux)
}

func policyCatalogueRequest(chain http.Handler, method, target string, raw []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer policy-catalogue-test-token")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)
	return rec
}

func policyCatalogueFromGET(t *testing.T, chain http.Handler) map[string]string {
	t.Helper()
	rec := policyCatalogueRequest(chain, http.MethodGet, "/api/v1/tools", nil)
	require.Equal(t, http.StatusOK, rec.Code, "%s", rec.Body.String())
	var entries []gen.ToolRegistryEntry
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entries))
	require.NotEmpty(t, entries)
	policies := make(map[string]string, len(entries))
	for _, entry := range entries {
		policies[entry.Name] = "deny"
	}
	require.Equal(t, "deny", policies["bash"], "implemented-key positive control")
	return policies
}

func preparePolicyReservationFixture(t *testing.T, api *restAPI, implemented map[string]string, stored bool) *agentstore.State {
	t.Helper()
	store := agentstore.New(api.homePath)
	state, err := store.ReadState("test-agent")
	require.NoError(t, err)
	_, err = store.MutateState("test-agent", state.Revision, func(record *config.AgentConfig) error {
		for name := range record.Tools.Builtin.Policies {
			if _, live := implemented[name]; !live {
				delete(record.Tools.Builtin.Policies, name)
			}
		}
		if stored {
			record.Tools.Builtin.Policies["connect_mailbox"] = config.ToolPolicyDeny
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.Empty(t, api.fastAgentUpsert("test-agent"))
	state, err = store.ReadState("test-agent")
	require.NoError(t, err)
	return state
}

func policyCatalogueWriteBody(t *testing.T, route, revision string, policies map[string]string) (string, string, []byte) {
	t.Helper()
	policyJSON, err := json.Marshal(policies)
	require.NoError(t, err)
	var payload any
	method, target := http.MethodPut, "/api/v1/agents/test-agent"
	switch route {
	case "create":
		method, target = http.MethodPost, "/api/v1/agents"
		var create gen.AgentCreateRequestMain
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"type":"Main","name":"Catalogue create","soul":"Test instructions","tools_cfg":{"builtin":{"policies":%s}}}`, policyJSON)), &create))
		payload = create
	case "agent":
		var update gen.AgentUpdateRequest
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"revision":%q,"tools_cfg":{"builtin":{"policies":%s}}}`, revision, policyJSON)), &update))
		payload = update
	case "tools":
		target += "/tools"
		names := make([]string, 0, len(policies))
		for name := range policies {
			names = append(names, name)
		}
		sort.Strings(names)
		namesJSON, marshalErr := json.Marshal(names)
		require.NoError(t, marshalErr)
		var update gen.AgentToolsUpdateRequest
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"revision":%q,"override_names":%s,"builtin":{"policies":%s}}`, revision, namesJSON, policyJSON)), &update))
		payload = update
	default:
		t.Fatalf("unknown fixture route %q", route)
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return method, target, raw
}
