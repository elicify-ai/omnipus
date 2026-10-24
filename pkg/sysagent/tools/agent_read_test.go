package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGetAgentReturnsSanitizedConfigSoulSparseAssignmentsAndRevision(t *testing.T) {
	home := t.TempDir()
	empty := []string{}
	agent := config.AgentConfig{ID: "writer", Name: "Writer", Type: config.AgentTypeWorker, Skills: []string{"draft"}, Tools: &config.AgentToolsCfg{
		Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}},
		MCP:     config.AgentMCPToolsCfg{Servers: []config.AgentMCPServerBinding{{ID: "mail", Tools: empty, ToolsSpecified: true}}},
	}}
	if err := agentstore.New(home).Create(agent.ID, &agent); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "agents", agent.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agents", agent.ID, "SOUL.md"), []byte("Write clearly."), 0o600); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentGetTool(&systools.Deps{Home: home}).Execute(context.Background(), map[string]any{"id": agent.ID})
	if result.IsError {
		t.Fatalf("result=%s", result.ForLLM)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "writer" || got["soul"] != "Write clearly." {
		t.Fatalf("response=%v", got)
	}
	revision, ok := got["revision"].(string)
	if !ok {
		t.Fatalf("revision=%T %v, want string", got["revision"], got["revision"])
	}
	if len(revision) != 64 {
		t.Fatalf("revision=%v", got["revision"])
	}
	overrides, overridesOK := got["override_names"].([]any)
	if !overridesOK {
		t.Fatalf("override_names=%T %v, want array", got["override_names"], got["override_names"])
	}
	if len(overrides) != 1 || overrides[0] != "bash" {
		t.Fatalf("override_names=%v", overrides)
	}
	servers, serversOK := got["mcp_servers"].([]any)
	if !serversOK {
		t.Fatalf("mcp_servers=%T %v, want array", got["mcp_servers"], got["mcp_servers"])
	}
	server, serverOK := servers[0].(map[string]any)
	if !serverOK {
		t.Fatalf("mcp_servers[0]=%T %v, want object", servers[0], servers[0])
	}
	if tools, ok := server["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatalf("server=%v want explicit empty tools", server)
	}
	if _, leaked := got["effective_tools"]; leaked {
		t.Fatal("get_agent must not duplicate full effective tool catalog")
	}
}

func TestGetAgentToolsReturnsCatalogEffectivePoliciesAndStoredOverrideNames(t *testing.T) {
	home := t.TempDir()
	agent := config.AgentConfig{ID: "writer", Name: "Writer", Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}}}}
	store := agentstore.New(home)
	if err := store.Create(agent.ID, &agent); err != nil {
		t.Fatal(err)
	}
	state, err := store.ReadState(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": "allow", "read_file": "allow"}
	deps := &systools.Deps{Home: home, GetCfg: func() *config.Config { return cfg }}
	// SPEC.md compact output: only assigned live MCP tools may join the builtin rows.
	mail := tools.NewMCPTool(nil, "mail", &mcp.Tool{Name: "send", Description: "Send mail."})
	defaultToolsWireCatalog(t, deps, []tools.Tool{mail, defaultToolsPreviewMetadata{name: "read_file"}, defaultToolsPreviewMetadata{name: "bash"}})
	result := systools.NewAgentGetToolsTool(deps).Execute(context.Background(), map[string]any{"id": agent.ID})
	if result.IsError {
		t.Fatalf("result=%s", result.ForLLM)
	}
	// SPEC.md compact output: revision remains in the header; verbose override/connector blocks are dropped.
	header := "agent: writer | name: Writer | kind: main | revision: " + state.Revision
	policies := defaultToolsCompactPolicies(t, result.ForLLM, header)
	if !reflect.DeepEqual(map[string]string{"bash": "deny", "read_file": "allow"}, policies) {
		t.Fatalf("compact effective policies=%v, want exactly bash deny and read_file allow; no unassigned MCP tools", policies)
	}
	stored, err := store.Get(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	// SPEC.md read-only inventory: preserve the old stored-override check against the real protected record.
	if !reflect.DeepEqual(map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}, stored.Tools.Builtin.Policies) {
		t.Fatalf("read changed stored override map: %v", stored.Tools.Builtin.Policies)
	}
}

func TestGetAgentActivationRequiresPublishedRevisionMatch(t *testing.T) {
	home := t.TempDir()
	agent := config.AgentConfig{ID: "writer", Name: "Writer"}
	if err := agentstore.New(home).Create(agent.ID, &agent); err != nil {
		t.Fatal(err)
	}
	state, err := agentstore.New(home).ReadState(agent.ID)
	if err != nil {
		t.Fatal(err)
	}

	deps := &systools.Deps{Home: home, AgentIsLive: func(string) bool { return true }}
	field := reflect.ValueOf(deps).Elem().FieldByName("AgentActiveRevision")
	if !field.IsValid() {
		t.Fatal("Deps.AgentActiveRevision is missing; registry membership alone cannot prove the persisted revision is active")
	}
	setRevision := func(revision string) {
		field.Set(reflect.ValueOf(func(id string) string {
			if id == agent.ID {
				return revision
			}
			return ""
		}))
	}
	readStatus := func() any {
		result := systools.NewAgentGetTool(deps).Execute(context.Background(), map[string]any{"id": agent.ID})
		if result.IsError {
			t.Fatalf("get_agent failed: %s", result.ForLLM)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(result.ForLLM), &got); err != nil {
			t.Fatal(err)
		}
		return got["activation_status"]
	}

	setRevision(strings.Repeat("0", 64))
	if got := readStatus(); got != string(agentstore.ActivationNotAttempted) {
		t.Fatalf("stale live instance status=%v, want not_attempted", got)
	}
	setRevision(state.Revision)
	if got := readStatus(); got != string(agentstore.ActivationActive) {
		t.Fatalf("matching published revision status=%v, want active", got)
	}
}
