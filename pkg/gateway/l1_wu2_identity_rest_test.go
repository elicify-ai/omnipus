package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-001 / BDD-1.1. This is the WU-2 reachability oracle: a real HTTP
// request reaches the production REST dispatcher, not just a seed helper.
func TestL1WU2Reachability_RealRESTExposesLockedOmnipusAndNoRetiredAgents(t *testing.T) {
	api := newSeededJudgeAPI(t)
	ts := httptest.NewServer(http.HandlerFunc(api.HandleAgents))
	defer ts.Close()
	t.Run("mia_positive_control", func(t *testing.T) {
		response, err := ts.Client().Get(ts.URL + "/api/v1/agents/mia")
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		var ac gen.Agent
		require.NoError(t, json.NewDecoder(response.Body).Decode(&ac))
		assert.Equal(t, "mia", ac.Id)
		assert.Equal(t, "Mia", ac.Name)
	})
	for _, id := range []string{"ava", "admin", "omnipus"} {
		t.Run(id, func(t *testing.T) {
			response, err := ts.Client().Get(ts.URL + "/api/v1/agents/" + id)
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			if id != "omnipus" {
				assert.Equal(t, http.StatusNotFound, response.StatusCode, "FR-008: retired agent cannot remain reachable: %s", body)
				return
			}
			require.Equal(t, http.StatusOK, response.StatusCode, "FR-001: Omnipus must be reachable: %s", body)
			var ac gen.Agent
			require.NoError(t, json.Unmarshal(body, &ac))
			assert.Equal(t, "omnipus", ac.Id)
			assert.Equal(t, "Omnipus", ac.Name)
			assert.Equal(t, gen.AgentTypeCore, ac.Type)
			assert.True(t, ac.Locked)
		})
	}
}

func TestL1WU2RESTNameWrites_ReserveOnlyTrimmedCaseInsensitiveOmnipus(t *testing.T) {
	// FR-002 / §6.2 / BDD-1.2: all public name-write paths, plus a similar-name control.
	for _, name := range []string{"Omnipus", "omnipus", " OMNIPUS ", "Omnipus Helper"} {
		for _, path := range []string{"create", "rename"} {
			t.Run(path+"_"+name, func(t *testing.T) {
				api := buildExecutorTestAPI(t)
				store := agentstore.New(api.homePath)
				before, err := store.ReadState("test-agent")
				require.NoError(t, err)
				payload := map[string]any{"name": name, "type": "Main", "soul": "Independent test persona"}
				if path == "rename" {
					payload = map[string]any{"revision": before.Revision, "name": name}
				}
				raw, err := json.Marshal(payload)
				require.NoError(t, err)
				w := httptest.NewRecorder()
				if path == "create" {
					r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(raw))
					r.Header.Set("Content-Type", "application/json")
					api.HandleAgents(w, r)
				} else {
					r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(raw))
					r.Header.Set("Content-Type", "application/json")
					api.HandleAgents(w, r)
				}
				if name == "Omnipus Helper" {
					want := http.StatusOK
					if path == "create" {
						want = http.StatusCreated
					}
					require.Equal(t, want, w.Code, "BDD-1.2 similar-name positive control: %s", w.Body.String())
					var ac gen.Agent
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ac))
					assert.Equal(t, name, ac.Name)
					return
				}
				assert.Equal(t, http.StatusConflict, w.Code, "§6.2 reserved-name refusal: %s", w.Body.String())
				assert.Contains(t, w.Body.String(), "the name Omnipus is reserved")
				after, err := store.ReadState("test-agent")
				require.NoError(t, err)
				assert.Equal(t, before, after)
				all, skipped, err := store.List()
				require.NoError(t, err)
				require.Empty(t, skipped, "unreadable entities must not hide a reserved-name write")
				assert.Len(t, all, 1, "reserved create cannot publish an entity")
			})
		}
	}
}

func TestL1WU2DefaultWorkspace_RealSeedKeepsHiddenMemberAndDepthOneSelfRow(t *testing.T) {
	// FR-003/004, OSA-8 depth 1: membership is ordinary, not an off-team bypass.
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	require.NoError(t, ensureDefaultWorkspace(home, "test-operator", cfg))
	id, err := workspace.ResolveDefaultID(home)
	require.NoError(t, err)
	ws, err := readWorkspaceFile(home, id)
	require.NoError(t, err)
	assert.Contains(t, ws.CoreTeam, "mia", "ordinary default membership positive control")
	assert.Contains(t, ws.CoreTeam, "omnipus", "FR-003: Omnipus is an ordinary hidden default-workspace member")
	assert.NotContains(t, ws.CoreTeam, "ava")
	assert.NotContains(t, ws.CoreTeam, "admin")
	edges, ok := workspace.LoadDelegation(home, id)
	require.True(t, ok, "real seed must persist its graph")
	self := []workspace.DelegationEdge{}
	for _, edge := range edges {
		if edge.FromAgent == "omnipus" && edge.ToAgent == "omnipus" {
			self = append(self, edge)
		}
	}
	require.Len(t, self, 1, "FR-004 / OSA-8: exactly one ordinary self-row must remain in the graph")
	require.NotNil(t, self[0].Depth)
	assert.Equal(t, 1, *self[0].Depth, "§6.2: Omnipus self-row depth is exactly 1, not generic fresh-self depth 10")
}

func l1WU2OmnipusWriteAPI(t *testing.T) *restAPI {
	t.Helper()
	// Explicit identity fixture permits exercising the guard even before the
	// real seed exists; the separate reachability test checks the real seed.
	api := buildExecutorTestAPI(t)
	ac := config.AgentConfig{ID: "omnipus", Name: "Omnipus", Type: config.AgentTypeCore, Locked: true, Tools: &config.AgentToolsCfg{}}
	require.NoError(t, agentstore.New(api.homePath).Create(ac.ID, &ac))
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()), "publish the fixture config through the real reload path")
	_, err := api.agentLoop.UpsertAgentFast(api.agentLoop.GetConfig(), ac.ID)
	require.NoError(t, err, "register the fixture identity through the real activation path")
	return api
}

func TestL1WU2RESTOmnipusPolicyWrites_Ask422DenyAcceptedAndZeroWrite(t *testing.T) {
	// FR-022 / BDD-3.5: direct tools and both agent-update capability forms.
	for _, endpoint := range []string{"tools", "tools_roundtrip", "agent_sparse", "agent_tools_cfg"} {
		for _, policy := range []string{"ask", "deny"} {
			t.Run(endpoint+"_"+policy, func(t *testing.T) {
				api := l1WU2OmnipusWriteAPI(t)
				store := agentstore.New(api.homePath)
				before, err := store.ReadState("omnipus")
				require.NoError(t, err)
				full := map[string]string{}
				for name := range buildKnownBuiltinToolNames() {
					// Request scaffolding, not an oracle: inherited echoes must
					// match this fixture's current ceiling (ADR-090).
					full[name] = api.agentLoop.GetConfig().Sandbox.ToolPolicies[name]
				}
				full["bash"] = policy
				payload := map[string]any{"revision": before.Revision, "override_names": []string{"bash"}, "builtin": map[string]any{"policies": full}}
				url := "/api/v1/agents/omnipus/tools"
				switch endpoint {
				case "tools_roundtrip":
					payload = map[string]any{"revision": before.Revision, "override_names": []string{"bash"}, "config": map[string]any{"builtin": map[string]any{"policies": full}}}
				case "agent_sparse":
					url = "/api/v1/agents/omnipus"
					payload = map[string]any{"revision": before.Revision, "tool_policy_changes": map[string]any{"set": map[string]any{"bash": policy}}}
				case "agent_tools_cfg":
					for name := range full {
						full[name] = "deny"
					}
					full["bash"] = policy
					url = "/api/v1/agents/omnipus"
					payload = map[string]any{"revision": before.Revision, "tools_cfg": map[string]any{"builtin": map[string]any{"policies": full}}}
				}
				raw, err := json.Marshal(payload)
				require.NoError(t, err)
				r := withAdminRole(httptest.NewRequest(http.MethodPut, url, bytes.NewReader(raw)))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				api.HandleAgents(w, r)
				after, err := store.ReadState("omnipus")
				require.NoError(t, err)
				if policy == "ask" {
					assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "FR-022: %s", w.Body.String())
					assert.Contains(t, w.Body.String(), "Omnipus takes no per-agent Ask; set Ask globally or use Deny")
					assert.Equal(t, before, after, "Ask refusal must not write any agent data")
				} else {
					require.Equal(t, http.StatusOK, w.Code, "BDD-3.5 Deny positive control: %s", w.Body.String())
					assert.Equal(t, config.ToolPolicyDeny, after.Agent.Tools.Builtin.Policies["bash"])
				}
			})
		}
	}
}

func TestL1WU2RESTMembership_RejectsOmnipusOutsideDefaultAndRemovalFromDefault(t *testing.T) {
	for _, testCase := range []string{"outside_default", "remove_from_default", "ordinary_remove_positive_control"} {
		t.Run(testCase, func(t *testing.T) {
			api := l1WU2OmnipusWriteAPI(t)
			ws := storedWorkspace{ID: "sales", Name: "Sales", Status: "active", CoreTeam: []string{"test-agent"}}
			team := []string{"test-agent", "omnipus"}
			if testCase == "ordinary_remove_positive_control" {
				team = []string{}
			}
			if testCase == "remove_from_default" {
				ws.ID, ws.Name, ws.IsDefault, ws.CoreTeam = "default", "Default", true, []string{"test-agent", "omnipus"}
				team = []string{"test-agent"}
			}
			require.NoError(t, writeWorkspaceFile(api.homePath, ws))
			before, err := os.ReadFile(filepath.Join(api.homePath, "workspaces", ws.ID+".json"))
			require.NoError(t, err)
			state, err := workspace.ReadStateLocked(api.homePath, ws.ID)
			require.NoError(t, err)
			raw, err := json.Marshal(map[string]any{"revision": state.Revision, "core_team": team})
			require.NoError(t, err)
			r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+ws.ID, bytes.NewReader(raw))
			w := httptest.NewRecorder()
			api.HandleWorkspaces(w, r)
			if testCase == "ordinary_remove_positive_control" {
				require.Equal(t, http.StatusOK, w.Code, "ordinary member removal remains reachable: %s", w.Body.String())
				updated, readErr := readWorkspaceFile(api.homePath, ws.ID)
				require.NoError(t, readErr)
				assert.Empty(t, updated.CoreTeam)
				return
			}
			assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "FR-003 / §6.2: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "default workspace")
			after, err := os.ReadFile(filepath.Join(api.homePath, "workspaces", ws.ID+".json"))
			require.NoError(t, err)
			assert.Equal(t, before, after)
			assert.Equal(t, testCase == "remove_from_default", slices.Contains(ws.CoreTeam, "omnipus"), "fixture must exercise the intended membership direction")
		})
	}
}
