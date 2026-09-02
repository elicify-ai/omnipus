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

	// operatorOnly is a SECOND, distinct exception to the allow-by-default
	// ceiling, and it is not about destruction — it is about an agent widening
	// its own boundary.
	//
	// add_mcp_server writes a program definition that the gateway then LAUNCHES,
	// and the launched process is not confined by the sandbox. config.json is in
	// the ADR-062 secret set precisely so an agent cannot write such an entry
	// with write_file; this tool wrote the same setting through the API, which
	// made the file-level protection moot. It is denied at the CEILING (not just
	// per-agent) on purpose: under most-restrictive-wins, a ceiling deny means an
	// operator has to make two deliberate edits — raise the ceiling AND grant the
	// agent — before any agent can install an MCP server. That is the intended
	// cost for a control whose whole job is to not happen by accident.
	//
	// This is seeded DATA an operator can edit on their own install (Settings ->
	// Security -> Tool Policies, or config.json), never a code branch — CLAUDE.md
	// hard constraint 6.
	operatorOnly := map[string]string{
		"add_mcp_server": "deny",
		// request_mount (ADR-063 FR-7.2) belongs to the same class as
		// add_mcp_server — an agent widening its OWN boundary — but is seeded
		// "ask" rather than "deny" because the widening is exactly what the
		// operator is being asked to approve, and the approval modal is where
		// they do it. "allow" would be wrong for the obvious reason (an agent
		// silently granting itself write access to any folder it names); "deny"
		// would be wrong because it makes the tool inert and pushes the request
		// back into prose the operator has to act on manually.
		"request_mount": "ask",
		// browser_upload_file (ADR-072 D2 FR-021) is the third member of this
		// class and the only browser verb in it. Every other browser tool
		// reads the page or drives it; this one hands a HOST FILE to a page on
		// the operator's signed-in session, which is the one browser action
		// that moves their data outward. "allow" would let any agent holding
		// the browser surface attach anything inside its confinement to any
		// site it can reach; "deny" was proposed for the unattended
		// delegation tier and OVERRULED by the operator, and the concern it
		// answered is met by FR-029 instead — the tool is not registered at
		// all until issue #659 lands, so an unattended ask cannot occur.
		//
		// Listing it here rather than under `destructive` is deliberate: it
		// destroys nothing. It is consent-gated on the direction of travel.
		"browser_upload_file": "ask",
	}

	for name, want := range operatorOnly {
		got, ok := cfg.Sandbox.ToolPolicies[name]
		if !ok {
			t.Errorf("expected sandbox.tool_policies to seed an entry for %q, found none", name)
			continue
		}
		if got != want {
			t.Errorf("expected seeded policy %q for operator-only tool %q, got %q", want, name, got)
		}
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

	// ADR-052 (autonomous agent plan execution, FR-005/FR-027) — the three
	// plan-execution tools seed an explicit "allow" CEILING (never absent,
	// never deny).
	//
	// This asserted "ask" until 2026-07-28. That value matched the spec's
	// literal seed matrix but produced the wrong RESOLVED posture: under the
	// strictest-wins global x agent merge, an "ask" ceiling overruled Jim's own
	// seeded "allow" and resolved "ask" for him too, making FR-005/R2-06's
	// "Jim is the ONLY seeded agent granted unprompted plan-execution" dead on
	// every install — and costing a 300 s approval-timeout stall per call.
	//
	// A ceiling grants nothing by itself; it only bounds what a per-agent
	// policy may be granted up to. The real resolved posture — Jim allow,
	// every other seeded agent ask, Judge deny — is asserted from the side
	// where the import direction is legal and the REAL resolver can be called:
	// pkg/coreagent's TestEffectiveResolution_PlanExecution_DefaultCeiling_
	// JimAllowOthersAsk. This assertion only pins the ceiling literal so that
	// tightening it back to "ask" has to delete a named reason first.
	planningAllowCeiling := map[string]bool{
		"create_plan":  true,
		"execute_plan": true,
		"run_task":     true,
	}
	for name := range planningAllowCeiling {
		got, ok := cfg.Sandbox.ToolPolicies[name]
		if !ok {
			t.Errorf("expected sandbox.tool_policies to seed an entry for %q, found none", name)
			continue
		}
		if got != "allow" {
			t.Errorf("expected seeded ceiling policy 'allow' for plan-execution tool %q, got %q "+
				"(an 'ask' ceiling silently overrules Jim's seeded 'allow' under strictest-wins)", name, got)
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
	// ceilings are "allow": an "ask" or "deny" ceiling on plan_correct would
	// overrule PlanSupervisor's own seeded "allow" under strictest-wins (the
	// inspect_session defect, repeated on the next tool), and an "ask" ceiling
	// on stop_plan would merge Jim's seeded "allow" down to "ask", making a
	// plan owner stopping their own plan depend on a human answering a prompt.
	// Asserted explicitly rather than left to the allow-by-default sweep
	// below, so someone tightening these has to delete a named assertion
	// carrying the reason. (Until 2026-07-28 this note contrasted the pair
	// with execute_plan's "ask" ceiling; that ceiling was the same defect and
	// is now "allow" too — the reasoning here is unchanged, only the contrast
	// is gone.)
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
	// stop_plan's ceiling exists to remove. Asserted by name for the same
	// reason as the pair above: tightening it must require deleting a named
	// assertion that carries the reason.
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

	// Every non-destructive entry must be "allow" — this is an allow-by-default
	// ceiling, not a narrow ask-list. disable_channel is explicitly checked as
	// the canonical "reversible, not destructive" example. inspect_session is
	// no longer excluded (fix-wave finding #2), and neither are the three
	// ADR-052 plan-execution tools (2026-07-28): all four are now seeded
	// "allow" at the ceiling, same as any other non-destructive tool, with the
	// real gating done per-agent.
	for name, policy := range cfg.Sandbox.ToolPolicies {
		if destructive[name] {
			continue
		}
		if _, isOperatorOnly := operatorOnly[name]; isOperatorOnly {
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
