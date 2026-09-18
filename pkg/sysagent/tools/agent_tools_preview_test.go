package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// ADR-090 FR-004: a proposal can inspect native creation defaults without
// creating a temporary agent. Overrides remain bounded by the global ceiling.
func TestAgentToolsCreationPreviewIsReadOnlyAndHonorsCeiling(t *testing.T) {
	for _, kind := range []string{"Main", "Subagent"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			cfg := config.DefaultConfig()
			cfg.Sandbox.ToolPolicies["read_file"] = "ask"
			cfg.Sandbox.ToolPolicies["ToolSearch"] = "deny"
			tool := systools.NewAgentGetToolsTool(&systools.Deps{Home: home, GetCfg: func() *config.Config { return cfg }})
			result := tool.Execute(context.Background(), map[string]any{"new_agent_type": kind})
			if result.IsError {
				t.Fatalf("read-only native preview failed: %s", result.ForLLM)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(result.ForLLM), &got); err != nil {
				t.Fatal(err)
			}
			if got["preview"] != true || got["new_agent_type"] != kind {
				t.Fatalf("preview identity=%v", got)
			}
			if _, exists := got["id"]; exists {
				t.Fatal("preview must not invent a persisted id")
			}
			if _, exists := got["revision"]; exists {
				t.Fatal("preview must not invent a persisted revision")
			}
			policies := got["effective_policies"].(map[string]any)
			for name, want := range map[string]string{"read_file": "ask", "bash": "deny", "write_file": "deny", "ToolSearch": "allow"} {
				if policies[name] != want {
					t.Errorf("%s=%v want %s", name, policies[name], want)
				}
			}
			stored := got["stored_tool_overrides"].(map[string]any)
			for name, want := range map[string]string{"read_file": "allow", "AskUserQuestion": "allow", "browser_handover": "deny", "bash": "deny"} {
				if stored[name] != want {
					t.Errorf("stored creation default %s=%v want %s", name, stored[name], want)
				}
			}
			if _, ok := got["connector_catalog"].(map[string]any); !ok {
				t.Fatalf("preview lacks connector catalog: %v", got["connector_catalog"])
			}
			if bindings, ok := got["mcp_servers"].([]any); !ok || len(bindings) != 0 {
				t.Errorf("preview connector assignments=%v", got["mcp_servers"])
			}
			entries, err := os.ReadDir(home)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("preview wrote filesystem entries: %v", entries)
			}
		})
	}
}

func TestAgentToolsCreationPreviewRejectsAmbiguousOrUnsupportedTargets(t *testing.T) {
	for _, args := range []map[string]any{
		{"id": "writer", "new_agent_type": "Main"},
		{"id": "", "new_agent_type": "Main"},
		{"new_agent_type": "subagent_3p"}, {"new_agent_type": "core"},
		{"new_agent_type": "system"}, {"new_agent_type": ""}, {"new_agent_type": true},
	} {
		result := systools.NewAgentGetToolsTool(&systools.Deps{Home: t.TempDir()}).Execute(context.Background(), args)
		if !result.IsError {
			t.Fatalf("invalid preview target accepted: %v", args)
		}
		code, _ := toolErrorCode(t, result.ForLLM)
		if code != "INVALID_INPUT" {
			t.Fatalf("invalid preview target %v result=%s", args, result.ForLLM)
		}
	}
}

// Claude rejects top-level composition even when the JSON Schema is valid.
// Execute still enforces the mutually exclusive targets on every invocation.
func TestAgentToolsPreviewSchemaSupportsClaude(t *testing.T) {
	schema := systools.NewAgentGetToolsTool(&systools.Deps{}).Parameters()
	if schema["type"] != "object" {
		t.Fatalf("tool input must be an object: %v", schema)
	}
	for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
		if _, present := schema[keyword]; present {
			t.Errorf("Claude rejects top-level %s", keyword)
		}
	}
}
