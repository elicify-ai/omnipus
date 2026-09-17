package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
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
	if len(got["revision"].(string)) != 64 {
		t.Fatalf("revision=%v", got["revision"])
	}
	overrides := got["override_names"].([]any)
	if len(overrides) != 1 || overrides[0] != "bash" {
		t.Fatalf("override_names=%v", overrides)
	}
	servers := got["mcp_servers"].([]any)
	server := servers[0].(map[string]any)
	if tools, ok := server["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatalf("server=%v want explicit empty tools", server)
	}
	if _, leaked := got["effective_tools"]; leaked {
		t.Fatal("get_agent must not duplicate full effective tool catalog")
	}
}

func TestGetAgentToolsReturnsCatalogEffectivePoliciesAndStoredOverrideNames(t *testing.T) {
	home := t.TempDir()
	agent := config.AgentConfig{ID: "writer", Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}}}}
	if err := agentstore.New(home).Create(agent.ID, &agent); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": "allow", "read_file": "allow"}
	result := systools.NewAgentGetToolsTool(&systools.Deps{Home: home, GetCfg: func() *config.Config { return cfg }}).Execute(context.Background(), map[string]any{"id": agent.ID})
	if result.IsError {
		t.Fatalf("result=%s", result.ForLLM)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &got); err != nil {
		t.Fatal(err)
	}
	if names := got["override_names"].([]any); len(names) != 1 || names[0] != "bash" {
		t.Fatalf("response=%v", got)
	}
	policies := got["effective_policies"].(map[string]any)
	if policies["bash"] != "deny" || policies["read_file"] != "allow" {
		t.Fatalf("effective=%v", policies)
	}
}
