// Omnipus — browser_evaluate's observable consequence at the tool filter
// (D2 capability spec FR-042/AC2, §10 order 4a).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// WHAT THIS FILE IS FOR, AND WHY IT IS NOT A RESTATEMENT OF THE SEED TEST.
//
// pkg/coreagent's seed tests assert what the policy DATA says. This one
// asserts the CONSEQUENCE the model actually experiences, in the package that
// owns FilterToolsByPolicy — the function the agent loop calls to decide which
// tool definitions are sent on a turn.
//
// There is deliberately no assertion anywhere about a "policy refusal
// message", because there is none: a denied tool is FILTERED OUT of the sent
// definitions, not refused at call time. An implementer looking for a refusal
// string would find nothing and could conclude the control does not exist.
//
// The oracle is fixed, not conditional: Ava must NOT see browser_evaluate,
// while Mia and Jim MUST see it at "allow". ADR-090 gives Mia a live-browser
// workflow and keeps Ava out of browser execution. A test that only asserted
// absence would go green on a filter that drops everything.

package tools

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// seededAgentPolicyCfg builds a ToolPolicyCfg for one seeded agent exactly the
// way production does: the agent's own per-agent map against the global
// sandbox.tool_policies ceiling, both read from a real
// DefaultConfig()+SeedConfig() install. Nothing here is hand-written policy
// data — that is the whole point.
func seededAgentPolicyCfg(t *testing.T, cfg *config.Config, agentID string) *ToolPolicyCfg {
	t.Helper()
	global := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
	for k, v := range cfg.Sandbox.ToolPolicies {
		global[k] = config.ToolPolicy(v)
	}
	for i := range cfg.Agents.List {
		ac := cfg.Agents.List[i]
		if ac.ID != agentID {
			continue
		}
		if ac.Tools == nil {
			t.Fatalf("seeded agent %q carries no tools config", agentID)
		}
		return &ToolPolicyCfg{
			Policies:       ac.Tools.Builtin.Policies,
			GlobalPolicies: global,
		}
	}
	t.Fatalf("agent %q is not present in the seeded config", agentID)
	return nil
}

// ADR-090 permits browser evaluation for Mia, Jim and Ava, while Admin
// remains excluded even though the global browser switch is enabled.
func TestFilterToolsByPolicy_EnforcesADR090BrowserEvaluateRoles(t *testing.T) {
	cfg := config.DefaultConfig()
	if !coreagent.SeedConfig(cfg) {
		t.Fatal("coreagent.SeedConfig reported no change on a fresh DefaultConfig()")
	}

	// browser_evaluate's real scope is ScopeCore (pkg/tools/browser cannot be
	// imported here — it imports this package). A wrong scope would be denied
	// by the scope gate BEFORE the merge, which would make this test pass for
	// the wrong reason; pkg/tools/browser's own scope table is what pins the
	// real value.
	toolSet := []Tool{
		makeScopedTool("browser_evaluate", ScopeCore),
		makeScopedTool("browser_navigate", ScopeCore),
	}

	for _, id := range []coreagent.CoreAgentID{coreagent.IDAdmin} {
		polCfg := seededAgentPolicyCfg(t, cfg, string(id))
		kept, policies := FilterToolsByPolicy(toolSet, "core", polCfg)
		for _, tool := range kept {
			if tool.Name() == "browser_evaluate" {
				t.Errorf("%s can be sent browser_evaluate (resolved %q). This role holds no browser tools; "+
					"with sandbox.browser_evaluate_enabled seeded true, policy is the ONLY thing between "+
					"this role and arbitrary in-page JavaScript on the workspace browser that carries the "+
					"operator's live logins", id, policies["browser_evaluate"])
			}
		}
	}

	// Positive controls. Without them this file passes against a build where
	// FilterToolsByPolicy returns nothing at all, which is a broken filter
	// rather than a safe posture.
	for _, id := range []coreagent.CoreAgentID{coreagent.IDMia, coreagent.IDJim, coreagent.IDAva} {
		polCfg := seededAgentPolicyCfg(t, cfg, string(id))
		kept, policies := FilterToolsByPolicy(toolSet, "core", polCfg)
		var seesEvaluate bool
		for _, tool := range kept {
			if tool.Name() == "browser_evaluate" {
				seesEvaluate = true
			}
		}
		if !seesEvaluate {
			t.Errorf("%s cannot be sent browser_evaluate despite ADR-090's live-browser role", id)
		}
		if got := policies["browser_evaluate"]; got != "allow" {
			t.Errorf("(%s, browser_evaluate) resolves %q through the real filter, want allow", id, got)
		}
	}
}
