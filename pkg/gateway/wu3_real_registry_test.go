//go:build goolm && stdjson

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func wu3WiredAPI(t *testing.T, provider *testutil.ScenarioProvider) *restAPI {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	api := l1WU2WiredPermissionAPI(t)
	rc := &runContextWithOptions{
		homePath: api.homePath, configPath: api.configPath(), cfg: api.agentLoop.GetConfig(), agentLoop: api.agentLoop,
		runningServices: &services{restAPIRef: api}, centralMCPReg: tools.NewMCPRegistry(),
	}
	rc.wireSystemTools() // actual production wiring, including the shared-lock owner
	if provider != nil {
		clone, err := api.agentLoop.GetConfig().Clone()
		require.NoError(t, err)
		require.NoError(t, api.agentLoop.ReloadProviderAndConfig(context.Background(), provider, clone))
	}
	return api
}

func wu3ToolResultFromTurn(t *testing.T, provider *testutil.ScenarioProvider, callID string) map[string]any {
	t.Helper()
	for _, messages := range provider.AllRequests() {
		for _, message := range messages {
			if message.Role == "tool" && message.ToolCallID == callID {
				var body map[string]any
				if err := json.Unmarshal([]byte(message.Content), &body); err != nil {
					t.Fatalf("WU-3 expected a structured outcome from registered %s; actual registry/schema refusal: %s (decode: %v)", callID, message.Content, err)
				}
				return body
			}
		}
	}
	t.Fatalf("real model edge never received tool result for %s", callID)
	return nil
}

// Reachability oracle / FR-040/041/030: public direct-turn entry, actual boot
// registry + policy + tool dispatch + stores + main provisioning. Only the paid
// model edge is scripted. It never constructs or directly invokes create_agent.
func TestWU3ReachableChangeToolThroughRealOmnipusPolicyAndRegistry(t *testing.T) {
	provider := testutil.NewScenario().WithToolCall("ToolSearch", `{"names":["create_agent"]}`).WithToolCall("create_agent", `{"name":"Sales Partner","description":"Target fixture","soul":"Help with sales.","model":"scripted-model","workspace_id":"sales"}`).WithText("apply completed")
	api := wu3WiredAPI(t, provider)
	require.NoError(t, workspace.SaveRecord(api.homePath, workspace.Workspace{ID: "sales", Name: "Sales", Status: "active"}))
	_, err := api.agentLoop.GetSessionStore().GetOrCreateMainSession("default", "omnipus")
	require.NoError(t, err)
	mainID, err := session.MainSessionID("default", "omnipus")
	require.NoError(t, err)
	policy, ok := api.agentLoop.ResolveRegisteredToolPolicy("omnipus", "create_agent")
	require.True(t, ok)
	require.Equal(t, "allow", policy, "real policy positive control")
	answer, err := api.agentLoop.ProcessDirectAs(context.Background(), "omnipus", "Create the confirmed colleague in Sales.", "agent:omnipus:session:"+mainID)
	require.NoError(t, err, "routing/turn error is not proof of change-tool outcome")
	assert.Equal(t, "apply completed", answer)
	result := wu3ToolResultFromTurn(t, provider, "create_agent-0")
	require.Equal(t, "working", result["outcome"], "must see WU-3 outcome from actual policy/registry dispatch")
	id, ok := result["id"].(string)
	require.True(t, ok)
	target, err := workspace.ReadState(api.homePath, "sales")
	require.NoError(t, err)
	assert.Equal(t, []string{id}, target.Workspace.CoreTeam)
	anchor, err := workspace.ReadState(api.homePath, "default")
	require.NoError(t, err)
	assert.Equal(t, []string{"omnipus"}, anchor.Workspace.CoreTeam)
	targetMain, err := session.MainSessionID("sales", id)
	require.NoError(t, err)
	meta, err := api.agentLoop.GetSessionStore().GetMeta(targetMain)
	require.NoError(t, err)
	require.NotNil(t, meta, "FR-041 target main actually provisioned")
	assert.Equal(t, "sales", meta.WorkspaceID)
	assert.Equal(t, id, meta.AgentID)
	anchorMeta, err := api.agentLoop.GetSessionStore().GetMeta(mainID)
	require.NoError(t, err)
	assert.Equal(t, "default", anchorMeta.WorkspaceID, "Omnipus main's anchor never moves")
}

// FR-030 reachability with a baseline-supported argument schema: the real
// turn reaches update_workspace.Execute, commits the exact requested record,
// and must deliver its WU-3 working outcome to the model edge.
func TestWU3WorkspaceOutcomeThroughRealOmnipusTurn(t *testing.T) {
	provider := testutil.NewScenario()
	api := wu3WiredAPI(t, provider)
	require.NoError(t, workspace.SaveRecord(api.homePath, workspace.Workspace{ID: "sales", Name: "Sales", Status: "active"}))
	before, err := workspace.ReadState(api.homePath, "sales")
	require.NoError(t, err)
	args, err := json.Marshal(map[string]any{"id": "sales", "revision": before.Revision, "description": "real WU3 turn write"})
	require.NoError(t, err)
	provider.WithToolCall("ToolSearch", `{"names":["update_workspace"]}`).WithToolCall("update_workspace", string(args)).WithText("apply completed")
	_, err = api.agentLoop.GetSessionStore().GetOrCreateMainSession("default", "omnipus")
	require.NoError(t, err)
	mainID, err := session.MainSessionID("default", "omnipus")
	require.NoError(t, err)
	policy, registered := api.agentLoop.ResolveRegisteredToolPolicy("omnipus", "update_workspace")
	require.True(t, registered)
	require.Equal(t, "allow", policy)
	answer, err := api.agentLoop.ProcessDirectAs(context.Background(), "omnipus", "Apply this confirmed workspace description.", "agent:omnipus:session:"+mainID)
	require.NoError(t, err)
	assert.Equal(t, "apply completed", answer)
	result := wu3ToolResultFromTurn(t, provider, "update_workspace-0")
	after, err := workspace.ReadState(api.homePath, "sales")
	require.NoError(t, err)
	assert.Equal(t, "real WU3 turn write", after.Workspace.Description, "actual registered Execute must have committed, not returned model prose")
	assert.NotEqual(t, before.Revision, after.Revision)
	t.Log("positive real Execute control: Sales description committed through Omnipus turn")
	assert.Equal(t, "working", result["outcome"], "registered change tool must deliver WU-3 outcome")
}

// #1063 F1: the same real registry's ToolSearch/load path and execution agree
// with the live compositor. add_mcp_server's validation path does no host work.
func TestWU3MCPManagementAllowedPolicyMatchesDiscoveryAndRealExecution(t *testing.T) {
	provider := testutil.NewScenario().WithToolCall("ToolSearch", `{"names":["list_mcp_servers","add_mcp_server"]}`).WithToolCall("list_mcp_servers", `{}`).WithToolCall("add_mcp_server", `{"name":"fixture","transport":"stdio","expected_revision":"absent"}`).WithText("checked")
	api := wu3WiredAPI(t, provider)
	al := api.agentLoop
	clone, err := al.GetConfig().Clone()
	require.NoError(t, err)
	clone.Sandbox.ToolPolicies["add_mcp_server"] = "allow"
	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), provider, clone))
	inst, ok := al.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	search, ok := inst.Tools.Get("ToolSearch")
	require.True(t, ok)
	for _, name := range []string{"list_mcp_servers", "add_mcp_server"} {
		policy, registered := al.ResolveRegisteredToolPolicy("omnipus", name)
		require.True(t, registered)
		require.Equal(t, "allow", policy, "positive live policy control before discovery")
		t.Logf("live policy control: %s=%s", name, policy)
	}
	loaded := search.Execute(tools.WithAgentID(context.Background(), "omnipus"), map[string]any{"names": []any{"list_mcp_servers", "add_mcp_server"}})
	require.False(t, loaded.IsError, "allowed tools must be loadable: %s", loaded.ForLLM)
	for _, name := range []string{"list_mcp_servers", "add_mcp_server"} {
		assert.Contains(t, loaded.ForLLM, name)
	}
	_, err = al.GetSessionStore().GetOrCreateMainSession("default", "omnipus")
	require.NoError(t, err)
	mainID, err := session.MainSessionID("default", "omnipus")
	require.NoError(t, err)
	_, err = al.ProcessDirectAs(context.Background(), "omnipus", "Inspect MCP and validate this incomplete add without saving.", "agent:omnipus:session:"+mainID)
	require.NoError(t, err)
	listed := wu3ToolResultFromTurn(t, provider, "list_mcp_servers-0")
	assert.Equal(t, []any{}, listed["servers"], "real tool invocation returns empty configured list")
	refused := wu3ToolResultFromTurn(t, provider, "add_mcp_server-0")
	errBody, ok := refused["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "INVALID_INPUT", errBody["code"], "real validation reached, not permission_denied/policy_denied")
	assert.Contains(t, errBody["message"], "command")
}

// #1063 F2 / binding Q3: authentic CLI Bearer on the real route wrapper. An
// unauthenticated negative control proves the test did not accidentally bypass.
func TestWU3CLITokenCanReadMCPServersREST(t *testing.T) {
	api := wu3WiredAPI(t, nil)
	token := "omnipus_" + strings.Repeat("c", 64) // synthetic fixture, never a real token
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	require.NoError(t, err)
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Gateway.CLIToken = &config.TokenEntry{Hash: config.BcryptHash(hash)}
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{"fixture": {Type: "stdio", Command: "inert-command", Enabled: false}}
		return nil
	}))
	for _, authenticated := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
		if authenticated {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		api.withAuth(api.HandleMCPServers)(w, request)
		if !authenticated {
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			continue
		}
		require.Equal(t, http.StatusOK, w.Code, "CLI token permitted on MCP GET")
		var servers []gen.McpServer
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &servers))
		require.Len(t, servers, 1)
		assert.Equal(t, "fixture", servers[0].Name)
	}
}

// BDD-11.11: tightening the global ceiling between turns must affect the next
// REAL call. Successful first update is the instrument's positive control.
func TestWU3ReloadedGlobalDenyStopsNextOmnipusChange(t *testing.T) {
	provider := testutil.NewScenario().WithToolCall("update_workspace", `{}`).WithText("placeholder")
	api := wu3WiredAPI(t, provider)
	require.NoError(t, workspace.SaveRecord(api.homePath, workspace.Workspace{ID: "sales", Name: "Sales", Status: "active"}))
	inst, ok := api.agentLoop.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	tool, ok := inst.Tools.Get("update_workspace")
	require.True(t, ok)
	before, err := workspace.ReadState(api.homePath, "sales")
	require.NoError(t, err)
	policy, ok := api.agentLoop.ResolveRegisteredToolPolicy("omnipus", "update_workspace")
	require.True(t, ok)
	require.Equal(t, "allow", policy)
	control := tool.Execute(tools.WithAgentID(context.Background(), "omnipus"), map[string]any{"id": "sales", "revision": before.Revision, "description": "winner"})
	require.False(t, control.IsError, "%s", control.ForLLM)
	clone, err := api.agentLoop.GetConfig().Clone()
	require.NoError(t, err)
	clone.Sandbox.ToolPolicies["update_workspace"] = "deny"
	require.NoError(t, api.agentLoop.ReloadProviderAndConfig(context.Background(), provider, clone))
	policy, ok = api.agentLoop.ResolveRegisteredToolPolicy("omnipus", "update_workspace")
	require.True(t, ok)
	assert.Equal(t, "deny", policy)
	// ToolSearch is the policy-gated load surface; no tool implementation stub.
	inst, ok = api.agentLoop.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	search, ok := inst.Tools.Get("ToolSearch")
	require.True(t, ok)
	refusal := search.Execute(tools.WithAgentID(context.Background(), "omnipus"), map[string]any{"names": []any{"update_workspace"}})
	assert.Contains(t, refusal.ForLLM, "denied", "new policy must gate even a previously available change tool")
	data, err := os.ReadFile(filepath.Join(api.homePath, "workspaces", "sales.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "winner")
}
