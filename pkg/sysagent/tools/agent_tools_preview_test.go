package systools_test

import (
	"context"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// ADR-090 FR-004: a proposal can inspect native creation defaults without
// creating a temporary agent. Overrides remain bounded by the global ceiling.
func TestAgentToolsCreationPreviewIsReadOnlyAndHonorsCeiling(t *testing.T) {
	// SPEC.md compact output: all three creation kinds use the same compact, read-only shape.
	for _, kind := range []string{"Main", "Subagent", "subagent_3p"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			cfg := config.DefaultConfig()
			cfg.Sandbox.ToolPolicies["read_file"] = "ask"
			cfg.Sandbox.ToolPolicies["ToolSearch"] = "deny"
			deps := &systools.Deps{Home: home, GetCfg: func() *config.Config { return cfg }}
			names := defaultToolsWirePreviewCatalog(t, deps)
			result := systools.NewAgentGetToolsTool(deps).Execute(context.Background(), map[string]any{"new_agent_type": kind})
			if result.IsError {
				t.Fatalf("read-only preview failed: %s", result.ForLLM)
			}
			policies := defaultToolsParsePreview(t, result.ForLLM, kind, names)
			// SPEC.md compact effective-policy contract / CR-03: discovery's runtime floor is Allow; ordinary tools still merge strictest-wins.
			for name, want := range map[string]string{"read_file": "ask", "bash": "deny", "write_file": "allow", "ToolSearch": "allow", "browser_handover": "allow"} {
				if policies[name] != want {
					t.Errorf("%s=%v want %s", name, policies[name], want)
				}
			}
			// SPEC.md compact output drops stored_tool_overrides and connector metadata;
			// exact row count/shape above replaces those now-obsolete response assertions.
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
		// Chief's SPEC.md preview ruling (2026-10-22): subagent_3p moved to the positive table above; hidden kinds remain invalid.
		{"new_agent_type": "core"},
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
