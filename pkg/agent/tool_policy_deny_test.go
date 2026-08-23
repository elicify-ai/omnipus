// Package agent — ToolPolicyCfg governance tests.
//
// Verifies that the central tool registry governance contract holds: when
// ToolPolicyCfg maps bash to "deny", FilterToolsByPolicy excludes it from the
// set presented to the LLM. Prior to ADR-036 this covered two separate tools
// (workspace_shell / workspace_shell_bg, gated behind the now-retired
// experimental.workspace_shell_enabled flag); both are now the single
// universally-registered "bash" tool, so the two tests collapse into one.
//
// Note on architecture: tools are always registered in ag.Tools regardless of
// policy — the registry is a storage layer. FilterToolsByPolicy is the
// governance gate applied at LLM-call assembly time (loop.go:3212). This test
// verifies that gate directly, not the storage layer.
//
// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-3 —
// "ToolPolicyCfg deny means tool not registered".

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestToolPolicyCfg_DenyBash_ExcludedFromLLMView verifies that when an
// agent's ToolPolicyCfg maps bash to "deny", FilterToolsByPolicy removes it
// from the set of tools visible to the LLM.
//
// BDD: Given an agent with ToolPolicyCfg{"bash": "deny"},
//
//	When FilterToolsByPolicy is applied,
//	Then bash is absent from the filtered tool list.
func TestToolPolicyCfg_DenyBash_ExcludedFromLLMView(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{
					ID:   "deny-shell-agent",
					Name: "Deny Shell Agent",
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	al.WireTier13Deps(Tier13Deps{})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	ag, ok := reg.GetAgent("deny-shell-agent")
	if !ok || ag == nil {
		t.Fatal("deny-shell-agent not found in registry")
	}

	// Confirm bash IS in the raw registry (governance is at filter time,
	// registration is unconditional — ADR-036 FR-B8).
	if _, found := ag.Tools.Get("bash"); !found {
		t.Fatal("bash must be registered in ag.Tools — policy applies at filter time, not register time")
	}

	// Apply a deny policy for bash and verify FilterToolsByPolicy excludes it.
	policyCfg := &tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			"bash": "deny",
		},
	}

	allTools := ag.Tools.GetAll()
	filtered, _ := tools.FilterToolsByPolicy(allTools, "custom", policyCfg)

	for _, tool := range filtered {
		if tool.Name() == "bash" {
			t.Errorf("bash must be excluded from filtered tools when policy=deny; found in filtered list")
		}
	}
}
