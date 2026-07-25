// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// tool_policy_coverage_regression_test.go pins a known, deliberately-accepted
// limitation of ValidateToolPolicyCoverage (validate.go): the function
// iterates cfg.Agents.List, so an EMPTY roster produces ZERO gaps regardless
// of knownTools — i.e. it reports "full coverage" even though NO agent has
// any policy entry for any known tool anywhere (global or per-agent). Taken
// at face value that defeats CLAUDE.md Hard Constraint #6, whose entire
// purpose is to ABORT boot on any agent×tool policy gap: an empty roster
// sails straight through the gate built specifically to catch this.
//
// This is NOT a live, exploitable bug in production. Both boot paths
// (pkg/agent.LoadAgentRosterFromEntityStore and pkg/gateway's
// populateAgentsListFromEntityStore, see roster_load.go / gateway.go)
// repopulate cfg.Agents.List from the on-disk agent entity store (ADR-054)
// BEFORE ValidateToolPolicyCoverage ever runs — a config with a genuinely
// empty roster only arises on an install with literally zero agents
// recorded, which is not the scenario Constraint #6 exists to guard against.
// This test is defence-in-depth documentation, not a bug report: if a future
// refactor changes ValidateToolPolicyCoverage's iteration source, or someone
// "fixes" the empty-roster case in one direction without checking the other,
// this test forces that to be a deliberate, reviewed decision instead of a
// silent regression discovered later at boot on a real install.
//
// DO NOT delete this test to make a change "pass" — if you close the gap
// intentionally, update this test's expectations (and this comment) to
// match, in the same change.
package config

import "testing"

// TestValidateToolPolicyCoverage_EmptyAgentRoster_VacuousPass pins BOTH halves
// of the reproduction that motivated this file:
//
//   - CONTROL: a roster with one agent and zero policy entries anywhere finds
//     every known tool as a gap (the guard working as designed). This half
//     matters as much as the subject half below — without it, a future
//     change that broke ValidateToolPolicyCoverage entirely (e.g. always
//     returning nil) would still make the subject assertion "pass" for the
//     wrong reason.
//   - SUBJECT: the SAME knownTools against an EMPTY roster finds zero gaps —
//     the vacuous pass. See the file-level comment for why this is
//     defence-in-depth, not a live bug: the roster is repopulated from the
//     entity store before this function is ever called in production.
func TestValidateToolPolicyCoverage_EmptyAgentRoster_VacuousPass(t *testing.T) {
	knownTools := map[string]struct{}{
		"bash":      {},
		"web_serve": {},
	}

	t.Run("control: one agent, no policy entries anywhere -> 2 gaps (guard works)", func(t *testing.T) {
		cfg := &Config{
			Agents: AgentsConfig{
				List: []AgentConfig{
					{ID: "mia"}, // no Tools, no per-agent policies; no global entries either
				},
			},
		}

		gaps := ValidateToolPolicyCoverage(cfg, knownTools)

		if len(gaps) != 2 {
			t.Fatalf("control: expected 2 gaps (bash, web_serve) for a 1-agent roster with zero policy "+
				"coverage, got %d: %v — if this fails, ValidateToolPolicyCoverage itself is broken and "+
				"the subject sub-test below cannot be trusted either", len(gaps), gaps)
		}
		gotTools := map[string]bool{}
		for _, g := range gaps {
			if g.AgentID != "mia" {
				t.Errorf("control: gap reported for unexpected agent %q, want %q", g.AgentID, "mia")
			}
			gotTools[g.ToolName] = true
		}
		if !gotTools["bash"] || !gotTools["web_serve"] {
			t.Errorf("control: expected gaps for both %q and %q, got %v", "bash", "web_serve", gaps)
		}
	})

	t.Run("subject: EMPTY roster, same knownTools -> 0 gaps (known vacuous-pass limitation)", func(t *testing.T) {
		cfg := &Config{
			Agents: AgentsConfig{
				List: nil, // deliberately empty — e.g. a config.LoadConfig pass that stripped agents.list
				// (ADR-054, pkg/config/legacy_agents_list.go) with the entity-store
				// repopulation step (LoadAgentRosterFromEntityStore /
				// populateAgentsListFromEntityStore) not yet having run.
			},
		}

		gaps := ValidateToolPolicyCoverage(cfg, knownTools)

		if len(gaps) != 0 {
			t.Fatalf("subject: expected the KNOWN vacuous-pass limitation (0 gaps) for an empty agent "+
				"roster against 2 known tools, got %d gaps: %v — if ValidateToolPolicyCoverage now "+
				"iterates something other than cfg.Agents.List, or otherwise flags an empty roster, "+
				"update this test's expectations AND the file-level comment describing the limitation; "+
				"do not just delete the test", len(gaps), gaps)
		}
	})
}
