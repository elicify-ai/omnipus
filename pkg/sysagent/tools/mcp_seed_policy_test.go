// Omnipus — add_mcp_server seeded-policy regression
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ADR-090 section 5 gives Admin connector setup while every other seeded role
// remains denied. The global ceiling permits that explicit role assignment;
// the compositor must still enforce an operator's global Deny for Admin.
func TestSeededPolicy_AddMCPServerAdminOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = nil
	coreagent.SeedConfig(cfg)
	policies := cfg.Sandbox.ToolPolicies
	if got := policies["add_mcp_server"]; got != "allow" {
		t.Fatalf("ADR-090 Admin setup requires global allow ceiling, got %q", got)
	}
	for _, agent := range cfg.Agents.List {
		policy, agentType := tools.BuildFallbackPolicyCfg(cfg, agent.ID)
		want := "deny"
		if agent.ID == "admin" {
			want = "allow"
		}
		if got := tools.EffectiveToolPolicy(policy, tools.ScopeCore, agentType, "add_mcp_server"); got != want {
			t.Errorf("%s add_mcp_server = %q, want %q", agent.ID, got, want)
		}
	}
	cfg.Sandbox.ToolPolicies["add_mcp_server"] = "deny"
	policy, agentType := tools.BuildFallbackPolicyCfg(cfg, "admin")
	if got := tools.EffectiveToolPolicy(policy, tools.ScopeCore, agentType, "add_mcp_server"); got != "deny" {
		t.Errorf("operator global denial must block Admin, got %q", got)
	}

	// remove_mcp_server stays "ask", deliberately: it narrows capability rather
	// than widening it, destroys no data, and is recoverable by re-adding. If
	// someone escalates it to deny they should have to change this line and
	// think about the legitimate cleanup flow they are breaking.
	if got, ok := policies["remove_mcp_server"]; !ok || got != "ask" {
		t.Errorf("seeded global policy for remove_mcp_server = %q (present=%v), want \"ask\"", got, ok)
	}

	// list_mcp_servers is read-only and reports no args/env, so it stays allow.
	if got, ok := policies["list_mcp_servers"]; !ok || got != "allow" {
		t.Errorf("seeded global policy for list_mcp_servers = %q (present=%v), want \"allow\"", got, ok)
	}
}

// TestMCPAddTool_HasNoHardcodedRefusal is the other half of the constraint-6
// contract, and the reason the test above is not sufficient on its own: the
// restriction must live in the seed, not in the code. An operator who deliberately
// grants add_mcp_server on their own install must still get a working tool.
//
// Without this test, the suite above would also pass against a build that
// hardcoded a refusal inside Execute — which would look secure and would
// silently take the choice away from the operator.
func TestMCPAddTool_HasNoHardcodedRefusal(t *testing.T) {
	deps, cfg := newTestDeps()

	result := systools.NewMCPAddTool(deps).Execute(context.Background(), map[string]any{
		"name":      "operator-granted",
		"transport": "stdio",
		"command":   "/usr/bin/true",
	})
	if result.IsError {
		t.Fatalf("add_mcp_server refused an operator-granted call: %s\n"+
			"The default must be seeded DATA (sandbox.tool_policies), never a code branch "+
			"— CLAUDE.md hard constraint 6.", result.ForLLM)
	}

	srv, ok := cfg.Tools.MCP.Servers["operator-granted"]
	if !ok {
		t.Fatal("add_mcp_server reported success but wrote no server entry")
	}
	if srv.Command != "/usr/bin/true" {
		t.Errorf("server command = %q, want %q", srv.Command, "/usr/bin/true")
	}
}
