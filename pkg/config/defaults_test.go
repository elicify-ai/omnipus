// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

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

	// The global map must be a full, wildcard-free enumeration (CLAUDE.md hard
	// constraint 6) — 85 static builtin tools (33 general + 11 browser + 35
	// sysagent + 4 ADR-052 planning/verifier + 2 ADR-055 supervision tools),
	// matching pkg/coreagent's allStaticToolNames literal-for-literal.
	// Browser gained browser_list_tabs / browser_switch_tab / browser_close_tab
	// (ADR-041 multi-tab), taking browser from 7 to 10, then browser_open_tab
	// (live-UAT finding, Alex — "no agent tool to open a new tab"), taking it to 11.
	// General gained recall_conversation — the 4th memory tool (remember /
	// recall_memory / run_retrospective / recall_conversation) that was
	// registered but omitted from the seed + catalog, so it was denied-by-default
	// (fail-closed) on every install — taking general from 31 to 32.
	// ADR-052 added create_plan/execute_plan/run_task/inspect_session, taking
	// the total from 78 to 82. ADR-053 added message_parent (the child→parent
	// messaging tool, seeded allow everywhere delegate is), taking it to 83.
	// ADR-055 added plan_correct + stop_plan, taking it to 85.
	//
	// This literal is a BACKSTOP, not the real guard: package config cannot
	// import pkg/coreagent (that direction is a cycle), so the one-for-one
	// identity with allStaticToolNames is asserted from the other side, in
	// pkg/coreagent's TestCatalog_MatchesGlobalCeilingEntryForEntry. Prefer
	// replacing this number with that mechanical assertion the next time a
	// tool is added, rather than growing the literal again.
	const wantToolCount = 85
	if got := len(cfg.Sandbox.ToolPolicies); got != wantToolCount {
		t.Errorf(
			"expected sandbox.tool_policies to enumerate all %d static builtin tools, got %d entries",
			wantToolCount,
			got,
		)
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
