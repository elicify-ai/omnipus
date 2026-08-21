// Omnipus — add_mcp_server seeded-policy regression
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// TestSeededPolicy_AddMCPServerDeniedByDefault is the regression for the third
// privilege escalation: an MCP server definition names a program the gateway
// launches, and that process is not confined by the sandbox. config.json is in
// the ADR-062 secret set precisely so an agent cannot write an MCP server entry
// with write_file — add_mcp_server wrote the same setting through the API, and
// was seeded "allow".
//
// This asserts the SEEDED DATA, which is where the control lives. There is no
// code branch refusing the tool (see the companion test below) — CLAUDE.md hard
// constraint 6 requires the posture of a fresh install to come from the seed an
// operator can edit, never from a hardcoded refusal.
func TestSeededPolicy_AddMCPServerDeniedByDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	policies := cfg.Sandbox.ToolPolicies

	got, ok := policies["add_mcp_server"]
	if !ok {
		t.Fatal("add_mcp_server has no explicit seeded tool policy — constraint 6 requires " +
			"every static builtin tool to resolve from an explicit literal entry")
	}
	if got != "deny" {
		t.Errorf("seeded global policy for add_mcp_server = %q, want \"deny\" — an agent granted "+
			"this tool can launch an unconfined program and escape the sandbox", got)
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
// deny must live in the seed, not in the code. An operator who deliberately
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
