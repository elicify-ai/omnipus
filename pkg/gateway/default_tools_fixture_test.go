package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// The oracle is the founder-approved 2026-10-22 default-tools spec, Table:
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/default-tools/SPEC.md
// These are literal expectations, never copied from seed maps or resolved output.
type defaultToolsPolicyCase struct {
	name   string
	main   string
	worker string
}

func defaultToolsSpecRows(t *testing.T, catalog *tools.BuiltinRegistry) []defaultToolsPolicyCase {
	t.Helper()
	var rows []defaultToolsPolicyCase
	groups := []struct {
		names  string
		main   string
		worker string
	}{
		{
			names: "AskUserQuestion send_message message_parent set_todos goal_claim " +
				"remember recall_memory recall_conversation delegate list_agents get_agent get_agent_tools inspect_session list_jobs " +
				"list_tasks update_task Skill ToolSearch find_skills list_skills search_web fetch_url " +
				"read_file list_directory grep list_mounts library_list library_read write_file edit_file append_file send_file serve_web " +
				"read_inbox search_email read_message create_email_draft list_email_attachments read_email_attachment download_email_attachment " +
				"list_secrets list_workspaces get_workspace",
			main: "allow", worker: "allow",
		},
		{
			names: "answer_request read_conversation set_goal run_retrospective create_plan execute_plan stop_plan plan_correct create_task request_secret",
			main:  "allow", worker: "deny",
		},
		{
			names: "delete_task create_skill edit_skill browser_upload_file request_mount send_email reply environment_setup",
			main:  "ask", worker: "ask",
		},
	}
	for _, group := range groups {
		for _, name := range strings.Fields(group.names) {
			rows = append(rows, defaultToolsPolicyCase{name: name, main: group.main, worker: group.worker})
		}
	}
	// The spec quantifies over ALL browser_* tools, not a hand-picked subset.
	// Union the seed universe and actual metadata catalog so an omission from
	// either cannot silently remove a browser case. Values still come from Table.
	browsers := map[string]bool{"browser_navigate": true, "browser_upload_file": true}
	for _, name := range coreagent.AllStaticToolNames() {
		if strings.HasPrefix(name, "browser_") {
			browsers[name] = true
		}
	}
	for _, tool := range catalog.All() {
		if strings.HasPrefix(tool.Name(), "browser_") {
			browsers[tool.Name()] = true
		}
	}
	for name := range browsers {
		if name != "browser_upload_file" { // The next Table row is the explicit Ask exception.
			rows = append(rows, defaultToolsPolicyCase{name: name, main: "allow", worker: "allow"})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		require.False(t, seen[row.name], "duplicate oracle row for %s", row.name)
		seen[row.name] = true
	}
	return rows
}

// Tool names expand the spec's 'Admin-only' row, including its provider,
// channel and MCP-server families. Read-only workspace/agent/skill tools in
// earlier Table rows are intentionally NOT classified as management tools.
func defaultToolsAdminOnlyNames() []string {
	return strings.Fields("create_agent update_agent delete_agent set_agent_permissions read_agent_metadata " +
		"get_config set_config run_doctor get_usage create_workspace update_workspace delete_workspace " +
		"configure_provider list_providers test_provider list_models " +
		"enable_channel configure_channel disable_channel list_channels test_channel connect_channel " +
		"add_mcp_server remove_mcp_server list_mcp_servers remove_skill install_skill remove_secret select_secret_version connect_mailbox list_mailboxes")
}

func defaultToolsCreateREST(t *testing.T, api *restAPI, kind string) *config.AgentConfig {
	t.Helper()
	const token = "default-tools-test-token"
	t.Setenv("OMNIPUS_BEARER_TOKEN", token)
	api.agentLoop.GetConfig().Gateway.ValidateInbound = true
	description := "Checks default tool policies without running an agent turn."
	var payload any
	switch kind {
	case "Main":
		payload = gen.AgentCreateRequestMain{Type: "Main", Name: "Default Main", Soul: "Help the user."}
	case "Subagent":
		payload = gen.AgentCreateRequestSubagent{Type: "Subagent", Name: "Default Native Worker", Description: &description, Soul: "Complete the delegated task."}
	case "subagent_3p":
		request := gen.AgentCreateRequestSubagent3p{Type: "subagent_3p", Name: "Default External Worker", Description: &description, Soul: "Complete the delegated task."}
		cli, executable := gen.ExternalCliToolCodex, defaultToolsExecutable(t)
		request.Executor.Cli, request.Executor.CliPath = &cli, &executable
		payload = request
	default:
		t.Fatalf("unknown test kind %q", kind)
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/agents", api.withAuth(api.HandleAgents))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	buildProductionMiddlewareChain(api, mux).ServeHTTP(response, req)
	require.Equal(t, http.StatusCreated, response.Code, "real REST creation control: %s", response.Body.String())
	created := decodeAgentResp(t, response.Body.Bytes())
	require.Equal(t, gen.AgentType(kind), created.Type)
	require.NotEmpty(t, created.Id)
	stored, err := agentstore.New(api.homePath).Get(created.Id)
	require.NoError(t, err)
	return stored
}

func defaultToolsCreateTool(t *testing.T, api *restAPI, kind string) *config.AgentConfig {
	t.Helper()
	deps := &systools.Deps{
		Home: api.homePath, ConfigPath: api.configPath(),
		GetCfg: api.agentLoop.GetConfig, MutateConfig: api.agentLoop.MutateConfig,
		SaveConfigLocked: func(cfg *config.Config) error { return config.SaveConfig(api.configPath(), cfg) },
	}
	args := map[string]any{
		"agent_type": kind, "name": "Default Tool Subject", "description": "Checks default policies.", "model": "scripted-model",
	}
	if kind == "subagent_3p" {
		// create_agent's external variant takes cli/cli_path and no native soul.
		// The executable is validated but never launched by this test.
		args["cli"], args["cli_path"] = "codex", defaultToolsExecutable(t)
	} else {
		args["soul"] = "Complete the user's task."
	}
	tool := systools.NewAgentCreateTool(deps)
	require.Equal(t, "create_agent", tool.Name(), "real tool boundary control")
	result := tool.Execute(context.Background(), args)
	require.NotNil(t, result)
	require.False(t, result.IsError, "real create_agent control: %s", result.ForLLM)
	var response map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &response))
	require.Equal(t, string(agentstore.PersistenceComplete), response["persistence_status"], "creation must really persist")
	id, ok := response["id"].(string)
	require.True(t, ok, "tool result id must be a string")
	require.NotEmpty(t, id)
	stored, err := agentstore.New(api.homePath).Get(id)
	require.NoError(t, err)
	return stored
}

func defaultToolsExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	return executable
}

func defaultToolsPolicies(cfg *config.Config, agent *config.AgentConfig) (*tools.ToolPolicyCfg, string) {
	// Use the production config-to-policy adapter, with the actual persisted
	// agent rather than a stand-in seed map. The shipped ceiling is NOT lifted.
	// Copy only the adapter's inputs, never Config's runtime mutex.
	snapshot := &config.Config{Agents: cfg.Agents, Sandbox: cfg.Sandbox}
	snapshot.Agents.List = []config.AgentConfig{*agent}
	return tools.BuildFallbackPolicyCfg(snapshot, agent.ID)
}

// Catalog reachability is separate from the spec's policy-resolution contract.
// Chief's 2026-10-22 ruling: absent metadata must not hide a policy assertion.
func defaultToolsReportCatalogGaps(t *testing.T, catalog *tools.BuiltinRegistry, rows []defaultToolsPolicyCase) {
	t.Helper()
	names := defaultToolsAdminOnlyNames()
	for _, row := range rows {
		names = append(names, row.name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := catalog.Get(name); !ok {
			t.Logf("CATALOG GAP: %s has no central metadata entry; policy is still tested; reachability is separate", name)
		}
	}
}
