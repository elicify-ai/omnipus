package gateway

// FR-037 / T-10 R15/R16. Actual registered/authenticated routes, current
// revisions and real entity/config persistence. No generic unknown-name 400
// may stand in for the specified always-ask refusal.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func q7PolicyFixture(t *testing.T) *wu7LiveFixture {
	t.Helper()
	f := newWU7LiveFixture(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", t14Bearer)
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Gateway.DevModeBypass = false
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	control := t14Request(t, f.wu6Fixture, http.MethodGet, "/api/v1/security/tool-policies", "", true)
	require.Equal(t, http.StatusOK, control.Code, "instrument: actual global policy route and bearer authentication")
	return f
}

func q7PolicyPayload(t *testing.T, f *wu6Fixture, endpoint, id, policy string, clearOverride bool) (string, string) {
	t.Helper()
	state, err := agentstore.New(f.f1277.home).ReadState(id)
	require.NoError(t, err)
	full := map[string]string{}
	for name := range buildKnownBuiltinToolNames() {
		full[name] = f.al.GetConfig().Sandbox.ToolPolicies[name] // inherited request scaffolding, not an oracle
	}
	full["set_channel_outbound"] = policy
	url := "/api/v1/agents/" + id + "/tools"
	overrides := []string{"set_channel_outbound"}
	if clearOverride {
		overrides = []string{}
		full["set_channel_outbound"] = f.al.GetConfig().Sandbox.ToolPolicies["set_channel_outbound"]
	}
	var payload any = map[string]any{"revision": state.Revision, "override_names": overrides, "builtin": map[string]any{"policies": full}}
	switch endpoint {
	case "tools_roundtrip":
		payload = map[string]any{"revision": state.Revision, "override_names": overrides, "config": map[string]any{"builtin": map[string]any{"policies": full}}}
	case "agent_sparse":
		url = "/api/v1/agents/" + id
		patch := map[string]any{"set": map[string]any{"set_channel_outbound": policy}}
		if clearOverride {
			patch = map[string]any{"remove": []string{"set_channel_outbound"}}
		}
		payload = map[string]any{"revision": state.Revision, "tool_policy_changes": patch}
	case "agent_tools_cfg":
		url = "/api/v1/agents/" + id
		for name := range full {
			full[name] = "deny"
		}
		full["set_channel_outbound"] = policy
		if clearOverride {
			delete(full, "set_channel_outbound")
		}
		payload = map[string]any{"revision": state.Revision, "tools_cfg": map[string]any{"builtin": map[string]any{"policies": full}}}
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return url, string(raw)
}

func TestQ7T35_GlobalRegisteredWriterRejectsAllowWithoutSaving(t *testing.T) {
	f := q7PolicyFixture(t)
	before := q7ConfigBytes(t, f.wu6Fixture)
	live := f.al.GetConfig().Sandbox.ToolPolicies
	policies := map[string]string{}
	for name, value := range live {
		policies[name] = value
	}
	policies["set_channel_outbound"] = "allow"
	raw, err := json.Marshal(map[string]any{"policies": policies})
	require.NoError(t, err)
	rec := t14Request(t, f.wu6Fixture, http.MethodPut, "/api/v1/security/tool-policies", string(raw), true)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"`+q7AlwaysAskText+`"}`, rec.Body.String(), "FR-037: not a generic unrecognized-name refusal")
	require.Equal(t, before, q7ConfigBytes(t, f.wu6Fixture))
	require.Equal(t, live, f.al.GetConfig().Sandbox.ToolPolicies)
}

func TestQ7T35_GlobalRegisteredWriterStoresAskAndDeny(t *testing.T) {
	for _, policy := range []string{"ask", "deny"} {
		t.Run(policy, func(t *testing.T) {
			f := q7PolicyFixture(t)
			policies := map[string]string{}
			for name, value := range f.al.GetConfig().Sandbox.ToolPolicies {
				policies[name] = value
			}
			policies["set_channel_outbound"] = policy
			raw, err := json.Marshal(map[string]any{"policies": policies})
			require.NoError(t, err)
			rec := t14Request(t, f.wu6Fixture, http.MethodPut, "/api/v1/security/tool-policies", string(raw), true)
			require.Equal(t, http.StatusOK, rec.Code, "FR-037: applicable stored value survives real reload: %s", rec.Body.String())
			var saved map[string]any
			require.NoError(t, json.Unmarshal(q7ConfigBytes(t, f.wu6Fixture), &saved))
			sandbox, ok := saved["sandbox"].(map[string]any)
			require.True(t, ok)
			stored, ok := sandbox["tool_policies"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, policy, stored["set_channel_outbound"])
			require.Equal(t, policy, f.al.GetConfig().Sandbox.ToolPolicies["set_channel_outbound"])
		})
	}
}

func TestQ7T35_PerAgentWriterMatrixUsesTheAlwaysAskAndExistingOmnipusRules(t *testing.T) {
	for _, endpoint := range []string{"tools", "tools_roundtrip", "agent_sparse", "agent_tools_cfg"} {
		for _, id := range []string{"mia", "omnipus"} {
			for _, policy := range []string{"allow", "ask", "deny"} {
				t.Run(endpoint+"_"+id+"_"+policy, func(t *testing.T) {
					f := q7PolicyFixture(t)
					store := agentstore.New(f.f1277.home)
					before, err := store.ReadState(id)
					require.NoError(t, err)
					configBefore := q7ConfigBytes(t, f.wu6Fixture)
					url, raw := q7PolicyPayload(t, f.wu6Fixture, endpoint, id, policy, false)
					rec := t14Request(t, f.wu6Fixture, http.MethodPut, url, raw, true)
					after, err := store.ReadState(id)
					require.NoError(t, err)
					if policy == "allow" {
						require.Equal(t, http.StatusBadRequest, rec.Code)
						require.JSONEq(t, `{"error":"`+q7AlwaysAskText+`"}`, rec.Body.String(), "T-10 R15/R16: exact refusal, not stale token or unknown-name 400")
						require.Equal(t, before, after)
						require.Equal(t, configBefore, q7ConfigBytes(t, f.wu6Fixture))
						return
					}
					if id == "omnipus" && policy == "ask" {
						require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "T-10 R16: existing Omnipus guard remains: %s", rec.Body.String())
						require.Contains(t, rec.Body.String(), "Omnipus takes no per-agent Ask")
						require.Equal(t, before, after)
						require.Equal(t, configBefore, q7ConfigBytes(t, f.wu6Fixture))
						return
					}
					require.Equal(t, http.StatusOK, rec.Code, "T-10 R15/R16: applicable Ask/Deny accepted: %s", rec.Body.String())
					require.Equal(t, config.ToolPolicy(policy), after.Agent.Tools.Builtin.Policies["set_channel_outbound"])
				})
			}
		}
	}
}

func TestQ7T35_ClearingOmnipusOverrideRestoresOnlyGlobalInheritance(t *testing.T) {
	for _, endpoint := range []string{"tools", "tools_roundtrip", "agent_sparse", "agent_tools_cfg"} {
		for _, global := range []string{"ask", "deny"} {
			t.Run(endpoint+"_global_"+global, func(t *testing.T) {
				f := q7PolicyFixture(t)
				require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
					cfg.Sandbox.ToolPolicies["set_channel_outbound"] = global
					return config.SaveConfig(f.f1277.configPath, cfg)
				}))
				url, raw := q7PolicyPayload(t, f.wu6Fixture, endpoint, "omnipus", "deny", false)
				rec := t14Request(t, f.wu6Fixture, http.MethodPut, url, raw, true)
				require.Equal(t, http.StatusOK, rec.Code, "instrument: explicit Deny stored first: %s", rec.Body.String())
				url, raw = q7PolicyPayload(t, f.wu6Fixture, endpoint, "omnipus", "", true)
				rec = t14Request(t, f.wu6Fixture, http.MethodPut, url, raw, true)
				require.Equal(t, http.StatusOK, rec.Code, "FR-037: clear is a removal, never a stored Ask: %s", rec.Body.String())
				state, err := agentstore.New(f.f1277.home).ReadState("omnipus")
				require.NoError(t, err)
				_, present := state.Agent.Tools.Builtin.Policies["set_channel_outbound"]
				require.False(t, present, "T-10 R8/R9: clearing leaves no override")
				require.Equal(t, global, f.al.GetConfig().Sandbox.ToolPolicies["set_channel_outbound"], "clear must not lift global Deny")
			})
		}
	}
}

func TestQ7T35_KnownBuiltinNamesIncludeThePolicyWriterTarget(t *testing.T) {
	names := buildKnownBuiltinToolNames()
	_, control := names["connect_channel"]
	require.True(t, control, "instrument: known-name source includes existing connection tools")
	_, present := names["set_channel_outbound"]
	require.True(t, present, "FR-032: known-name registration is required, not an unknown-policy fallback")
}
