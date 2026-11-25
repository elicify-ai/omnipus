package gateway

// FR-037 / T-10 R15/R16. Actual registered/authenticated routes, current
// revisions and real entity/config persistence. No generic unknown-name 400
// may stand in for the specified always-ask refusal.
//
// Fix round 4 (Part B row 6, spec C-22 row 6 and C-2): the global writer is no
// longer PUT /security/tool-policies. C-2 names PATCH /security/permissions "the
// only writer of these maps" and C-22 row 6 retires the old route, so the two
// global-writer tests below send the same refusals and stored values through the
// replacement; every assertion they carried is kept. The fixture's authentication
// probe reads GET /status (a plain withAuth route that exists before and after the
// redesign) instead of the retired GET, so the thirty per-agent tests that share
// this fixture do not turn red for an instrument reason.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	control := t14Request(t, f.wu6Fixture, http.MethodGet, "/api/v1/status", "", true)
	require.Equal(t, http.StatusOK, control.Code, "instrument: actual authenticated route and bearer authentication")
	anonymous := t14Request(t, f.wu6Fixture, http.MethodGet, "/api/v1/status", "", false)
	require.Equal(t, http.StatusUnauthorized, anonymous.Code, "instrument: the same route refuses a caller without the bearer, so the 200 above proves the bearer is checked")
	return f
}

// q7PatchPermissions sends PATCH /api/v1/security/permissions through the real mux and bearer
// middleware. No re-auth token is sent, on purpose: team-lead ruled (fix round 4, part B row 6,
// option B) that this route does NOT require X-Reauth-Token — spec line 60 records the retired
// writer as "no re-auth", C-2 says the replacement uses the same authentication, the UAT note in
// reauth_gate_test.go records the step-up gate as removed, and C-16 autosave cannot carry a
// token. A test that quietly supplied one would rely on a gate that must not exist.
func q7PatchPermissions(t *testing.T, f *wu6Fixture, body string) *httptest.ResponseRecorder {
	t.Helper()
	return t14Request(t, f, http.MethodPatch, "/api/v1/security/permissions", body, true)
}

// q7RequirePermissionsRoute proves the replacement route is registered and
// authenticated before a test leans on it: a 404 here would make every refusal
// below fail for the wrong reason (instrument rule).
func q7RequirePermissionsRoute(t *testing.T, f *wu6Fixture) {
	t.Helper()
	rec := t14Request(t, f, http.MethodGet, "/api/v1/security/permissions", "", true)
	require.Equal(t, http.StatusOK, rec.Code, "instrument: GET /security/permissions (C-2) is registered and authenticated: %s", rec.Body.String())
	var read map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &read), "instrument: the read model is JSON: %s", rec.Body.String())
	require.Contains(t, read, "policies", "instrument: C-2 PermissionSettings carries the tool permission map")
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

// q7StoredGlobalPolicy reads one tool's stored global permission straight from
// config.json (not through the loop), so a test sees what survives on disk.
func q7StoredGlobalPolicy(t *testing.T, f *wu6Fixture, tool string) any {
	t.Helper()
	var saved map[string]any
	require.NoError(t, json.Unmarshal(q7ConfigBytes(t, f), &saved))
	sandbox, ok := saved["sandbox"].(map[string]any)
	require.True(t, ok)
	stored, ok := sandbox["tool_policies"].(map[string]any)
	require.True(t, ok)
	return stored[tool]
}

// C-2 / C-22 row 6: the global writer is PATCH /security/permissions. A key the request does not
// name is never written, so the request names only set_channel_outbound (the old PUT had to
// carry the complete map to satisfy its coverage rule).
func TestQ7T35_GlobalRegisteredWriterRejectsAllowWithoutSaving(t *testing.T) {
	f := q7PolicyFixture(t)
	q7RequirePermissionsRoute(t, f.wu6Fixture)
	before := q7ConfigBytes(t, f.wu6Fixture)
	// A copy, not the loop's own map: a handler that wrote through a shared map must not be able
	// to make the comparison below vacuous.
	live := map[string]string{}
	for name, value := range f.al.GetConfig().Sandbox.ToolPolicies {
		live[name] = value
	}
	rec := q7PatchPermissions(t, f.wu6Fixture, `{"policies":{"set_channel_outbound":"allow"}}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"`+q7AlwaysAskText+`"}`, rec.Body.String(), "FR-037: not a generic unrecognized-name refusal")
	require.Equal(t, before, q7ConfigBytes(t, f.wu6Fixture))
	require.Equal(t, live, f.al.GetConfig().Sandbox.ToolPolicies)
}

func TestQ7T35_GlobalRegisteredWriterStoresAskAndDeny(t *testing.T) {
	for _, policy := range []string{"ask", "deny"} {
		t.Run(policy, func(t *testing.T) {
			f := q7PolicyFixture(t)
			q7RequirePermissionsRoute(t, f.wu6Fixture)
			if policy == "ask" {
				// ask is the shipped value of set_channel_outbound, and C-2 stores nothing for a
				// value that equals the stored one, so move the stored value away first: the write
				// under test below must be a real change, not an equal-value no-op.
				first := q7PatchPermissions(t, f.wu6Fixture, `{"policies":{"set_channel_outbound":"deny"}}`)
				require.Equal(t, http.StatusOK, first.Code, "instrument: the first move to deny stores: %s", first.Body.String())
				require.Equal(t, "deny", q7StoredGlobalPolicy(t, f.wu6Fixture, "set_channel_outbound"), "instrument: the stored value left ask before the write under test")
			}
			rec := q7PatchPermissions(t, f.wu6Fixture, `{"policies":{"set_channel_outbound":"`+policy+`"}}`)
			require.Equal(t, http.StatusOK, rec.Code, "FR-037: applicable stored value survives real reload: %s", rec.Body.String())
			// The contract answers a 200 with the complete stored settings.
			var answered struct {
				Policies map[string]string `json:"policies"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answered), "the 200 body is the stored settings: %s", rec.Body.String())
			require.Equal(t, policy, answered.Policies["set_channel_outbound"])
			require.Equal(t, policy, q7StoredGlobalPolicy(t, f.wu6Fixture, "set_channel_outbound"))
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
						var refusal struct {
							Error string `json:"error"`
						}
						require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refusal))
						q7AssertClearEntryGuidance(t, refusal.Error)
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

// R-CHK, S-39 row 16 / T-10 row 9: the refusal must explain the recovery
// path without promising that clearing a private entry can lift global Deny.
func q7AssertClearEntryGuidance(t *testing.T, message string) {
	t.Helper()
	message = strings.ToLower(message)
	require.Contains(t, message, "clear the entry", "S-39: refusal names the supported action")
	require.Contains(t, message, "inherit", "S-39: clearing restores inheritance, not stored Ask")
	require.Regexp(t, `global[^.\n]*(not\s+[^a-z]*deny|non-deny)`, message, "S-39: always-ask recovery requires a non-Deny global layer")
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
