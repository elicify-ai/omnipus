// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"strings"
	"testing"
)

// TestDefaultConfig_SeedsDestructiveToolPoliciesAsAsk verifies that a fresh
// install's default config seeds sandbox.tool_policies as a FULLY-ENUMERATED
// global ceiling: every static builtin tool defaults to "allow" except
// irreversible delete_*/remove_* actions, which ask for confirmation.
// disable_channel is deliberately NOT in the destructive set (reversible, not
// a delete). This ceiling can only be tightened per-agent, never loosened —
// the runtime filter resolves global x agent as most-restrictive-wins
// (pkg/agent/instance.go:agentToolsCfgToPolicy) — so this map does not, by
// itself, change what any agent (all deny-by-default least-privilege,
// pkg/coreagent/core.go) can actually do; it only raises the ceiling an
// operator could grant up to.
//
// This is a genuine configuration value the operator can see/edit at any
// time (Settings -> Security -> Tool Policies, or by hand-editing
// config.json) — not resolution-time code logic.
func TestDefaultConfig_SeedsDestructiveToolPoliciesAsAsk(t *testing.T) {
	cfg := DefaultConfig()

	destructive := map[string]bool{
		"delete_agent":             true,
		"delete_workspace":         true,
		"delete_task":              true,
		"delete_task_in_workspace": true,
		"remove_mcp_server":        true,
		"remove_skill":             true,
	}

	for name := range destructive {
		got, ok := cfg.Sandbox.ToolPolicies[name]
		if !ok {
			t.Errorf("expected sandbox.tool_policies to seed an entry for %q, found none", name)
			continue
		}
		if got != "ask" {
			t.Errorf("expected seeded policy 'ask' for destructive tool %q, got %q", name, got)
		}
	}

	// ADR-052 (autonomous agent plan execution, FR-005/FR-027) — a third
	// ceiling category alongside "destructive" (ask) and the allow-by-default
	// rest: the three plan-execution tools are explicit "ask" (never absent,
	// never deny — only Jim's own seeded policy is "allow").
	planningAsk := map[string]bool{
		"create_plan":  true,
		"execute_plan": true,
		"run_task":     true,
	}
	for name := range planningAsk {
		got, ok := cfg.Sandbox.ToolPolicies[name]
		if !ok {
			t.Errorf("expected sandbox.tool_policies to seed an entry for %q, found none", name)
			continue
		}
		if got != "ask" {
			t.Errorf("expected seeded ceiling policy 'ask' for plan-execution tool %q, got %q", name, got)
		}
	}
	// inspect_session (fix-wave finding #2, architect F2 half 1): the ceiling
	// seeds "allow" — the strictest-wins global x agent merge means a ceiling
	// "deny" would OVERRULE the Judge's own seeded "allow" and resolve the
	// Judge to deny (the landed defect this inverts). Every seeded non-Judge
	// agent instead carries an explicit per-agent "deny" (asserted by
	// pkg/coreagent's TestToolPolicy_InspectSession_JudgeOnly), so the
	// resolved posture for everyone but the Judge is unchanged.
	if got := cfg.Sandbox.ToolPolicies["inspect_session"]; got != "allow" {
		t.Errorf("expected seeded ceiling policy 'allow' for verifier-only tool \"inspect_session\", got %q", got)
	}

	// ADR-055 (PlanSupervisor) — the supervision/containment pair. BOTH
	// ceilings are "allow" and neither mirrors execute_plan's "ask": an "ask"
	// or "deny" ceiling on plan_correct would overrule PlanSupervisor's own
	// seeded "allow" under strictest-wins (the inspect_session defect,
	// repeated on the next tool), and an "ask" ceiling on stop_plan would
	// merge Jim's seeded "allow" down to "ask", making a plan owner stopping
	// their own plan depend on a human answering a prompt. Asserted
	// explicitly rather than left to the allow-by-default sweep below, so
	// someone "restoring symmetry" with execute_plan has to delete a named
	// assertion carrying the reason.
	if got := cfg.Sandbox.ToolPolicies["plan_correct"]; got != "allow" {
		t.Errorf("expected seeded ceiling policy 'allow' for \"plan_correct\", got %q", got)
	}
	if got := cfg.Sandbox.ToolPolicies["stop_plan"]; got != "allow" {
		t.Errorf("expected seeded ceiling policy 'allow' for \"stop_plan\", got %q", got)
	}

	// ADR-056 (list_jobs) — the read-only background-job roster. "allow" for
	// the same third reason stop_plan's ceiling is: stop_plan takes a plan id,
	// list_jobs is where an agent gets one, and an "ask" ceiling would drag
	// every per-agent "allow" (Jim's included) down to "ask" under
	// strictest-wins — re-introducing exactly the human-in-the-loop dependency
	// stop_plan's asymmetric ceiling exists to remove. Asserted by name for the
	// same reason as the pair above: "restoring symmetry" with execute_plan
	// must require deleting a named assertion that carries the reason.
	if got := cfg.Sandbox.ToolPolicies["list_jobs"]; got != "allow" {
		t.Errorf("expected seeded ceiling policy 'allow' for \"list_jobs\", got %q", got)
	}

	// The global map must be a full, wildcard-free enumeration (CLAUDE.md hard
	// constraint 6): it must enumerate EXACTLY the names in pkg/coreagent's
	// allStaticToolNames, one for one.
	//
	// That identity is NOT asserted here, and deliberately no longer has a
	// hardcoded count standing in for it. Package config cannot import
	// pkg/coreagent (pkg/coreagent already imports pkg/config — that direction
	// is a cycle), so the only thing this package could ever express was a
	// magic number, which is a strictly weaker control: it says "expected 85,
	// got 86" while the real question is WHICH tool is missing from WHICH
	// surface, and it has to be hand-grown (with a paragraph of changelog
	// prose) every time a tool lands.
	//
	// The real, mechanical, by-name guard runs from the other side, where the
	// import direction is legal: pkg/coreagent's
	// TestCatalog_MatchesGlobalCeilingEntryForEntry checks BOTH directions
	// (every catalog name has a ceiling entry; every ceiling entry is in the
	// catalog) plus length equality, and names the offending tool when it
	// fails. pkg/gateway's TestBuildKnownBuiltinToolNames_MatchesCoreagentStatic
	// ToolCatalog closes the third side (the live tool registry). Do not
	// reintroduce a count literal here — add the tool to both surfaces and let
	// those two tests speak.
	//
	// What IS still asserted locally is that the map is non-empty and that
	// every entry carries a legal, explicit policy value — the sweep below —
	// so a corrupted or wildcard-bearing ceiling still fails in this package.
	if len(cfg.Sandbox.ToolPolicies) == 0 {
		t.Fatal("sandbox.tool_policies must be a fully-enumerated global ceiling, got no entries")
	}
	for name, policy := range cfg.Sandbox.ToolPolicies {
		switch policy {
		case "allow", "ask", "deny":
		default:
			t.Errorf("tool %q has illegal seeded policy %q — must be allow, ask or deny", name, policy)
		}
		if strings.ContainsAny(name, "*?") {
			t.Errorf(
				"tool %q is a wildcard entry — the static builtin ceiling must be literal and "+
					"wildcard-free (CLAUDE.md hard constraint 6); wildcards are the MCP exception only",
				name,
			)
		}
	}

	// Every non-destructive, non-ADR-052-planning-ask entry must be "allow" —
	// this is an allow-by-default ceiling, not a narrow ask-list.
	// disable_channel is explicitly checked as the canonical
	// "reversible, not destructive" example. inspect_session is no longer
	// excluded (fix-wave finding #2): it is now seeded "allow" at the
	// ceiling too, same as any other non-destructive tool.
	for name, policy := range cfg.Sandbox.ToolPolicies {
		if destructive[name] || planningAsk[name] {
			continue
		}
		if policy != "allow" {
			t.Errorf("expected non-destructive tool %q to be seeded 'allow', got %q", name, policy)
		}
	}
	if got := cfg.Sandbox.ToolPolicies["disable_channel"]; got != "allow" {
		t.Errorf("disable_channel is reversible, not a delete — expected 'allow', got %q", got)
	}
}
