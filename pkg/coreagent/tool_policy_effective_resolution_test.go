// Omnipus — ADR-052 fix-wave finding #2 companion: effective-resolution
// tests for the inspect_session seed inversion + the execute_plan
// ceiling-raise autonomy knob (spec FR-005/DS-6), exercised through the REAL
// compositor merge (pkg/tools.ResolveEffectivePolicy) rather than by
// inspecting the raw seed maps directly. This is exactly the blind spot that
// let the inspect_session inversion land undetected in the first place: each
// seed map individually looked plausible (Judge per-agent allow, ceiling
// deny) but the ACTUAL runtime strictest-wins (deny > ask > allow) merge
// resolved the opposite of what was intended (the Judge resolved deny).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// globalPolicyMap converts cfg.Sandbox.ToolPolicies (map[string]string, the
// raw config-file shape) into the map[string]config.ToolPolicy shape
// tools.ToolPolicyCfg.GlobalPolicies expects — the same conversion
// pkg/tools.BuildFallbackPolicyCfg performs in production.
func globalPolicyMap(cfg *config.Config) map[string]config.ToolPolicy {
	out := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
	for k, v := range cfg.Sandbox.ToolPolicies {
		out[k] = config.ToolPolicy(v)
	}
	return out
}

// resolveFor builds a tools.ToolPolicyCfg for the named seeded agent's OWN
// per-agent policy map (read from a real SeedConfig-populated cfg) against
// the given global ceiling map (globalOverride, or the cfg's own ceiling when
// nil), and resolves toolName through the REAL production compositor
// (tools.ResolveEffectivePolicy) — the same strictest-wins (deny > ask >
// allow) merge the agent loop and gateway approval hook both resolve
// through, NOT a hand-rolled comparison of the two raw maps.
func resolveFor(t *testing.T, cfg *config.Config, agentID, toolName string, globalOverride map[string]config.ToolPolicy) string {
	t.Helper()
	ac := findSeeded(t, cfg, agentID)
	require.NotNil(t, ac.Tools, "agent %q must carry a tools policy", agentID)
	gp := globalOverride
	if gp == nil {
		gp = globalPolicyMap(cfg)
	}
	polCfg := &tools.ToolPolicyCfg{
		Policies:       ac.Tools.Builtin.Policies,
		GlobalPolicies: gp,
	}
	return tools.ResolveEffectivePolicy(polCfg, toolName)
}

// TestEffectiveResolution_InspectSession_JudgeAllow_OthersDeny is the
// fix-wave #2 regression lock, run through the REAL compositor merge (not the
// raw seed maps): under the fresh-install default config, inspect_session
// resolves "allow" for the Judge and "deny" for every other named seeded
// agent (Mia/Ray/Ava/Worker, per the task's explicit matrix).
func TestEffectiveResolution_InspectSession_JudgeAllow_OthersDeny(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	assert.Equal(t, "allow", resolveFor(t, cfg, string(coreagent.IDJudge), "inspect_session", nil),
		"(Judge, inspect_session) must resolve allow through the real compositor merge")

	for _, id := range []coreagent.CoreAgentID{
		coreagent.IDMia, coreagent.IDRay, coreagent.IDAva, coreagent.IDWorker,
	} {
		assert.Equalf(t, "deny", resolveFor(t, cfg, string(id), "inspect_session", nil),
			"(%s, inspect_session) must resolve deny through the real compositor merge", id)
	}
}

// TestEffectiveResolution_PlanExecution_DefaultCeiling_JimAllowOthersAsk is
// the ADR-052 FR-005/R2-06 regression lock, resolved END-TO-END through the
// real compositor merge.
//
// It replaces an earlier test that asserted the OPPOSITE ("every agent
// including Jim resolves ask under the default ceiling") and, in doing so,
// locked in the defect it was describing. The seed data always said Jim was
// "the ONLY seeded agent granted unprompted plan-execution", but the global
// ceiling was "ask", and strictest-wins (deny > ask > allow) silently
// overruled his per-agent "allow" to "ask" on every install. The observed
// symptom was a 300 s stall: run_task raised an approval that, on a headless
// or unattended turn, nobody answered, and the tool never ran.
//
// What makes this a real end-to-end assertion rather than a seed-literal
// check: it starts from config.DefaultConfig() + the real coreagent.SeedConfig
// and resolves through tools.ResolveEffectivePolicy — the same function the
// agent loop's ask-gate and the gateway approval hook both call. A change to
// EITHER side of the merge (the ceiling in pkg/config/defaults.go or the
// per-agent seed in pkg/coreagent/core.go) fails this test.
func TestEffectiveResolution_PlanExecution_DefaultCeiling_JimAllowOthersAsk(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	for _, tool := range planExecutionTools {
		assert.Equalf(t, "allow", resolveFor(t, cfg, string(coreagent.IDJim), tool, nil),
			"(Jim, %s) must RESOLVE allow on a fresh install — he is the only seeded agent "+
				"granted unprompted plan-execution (ADR-052 FR-005/R2-06). Resolving ask here means "+
				"the global ceiling in pkg/config/defaults.go has been tightened back to ask and is "+
				"silently overruling his seeded allow again", tool)

		for _, id := range []coreagent.CoreAgentID{
			coreagent.IDMia, coreagent.IDRay, coreagent.IDAva, coreagent.IDWorker,
		} {
			assert.Equalf(t, "ask", resolveFor(t, cfg, string(id), tool, nil),
				"(%s, %s) must still resolve ask — raising the ceiling grants the tool to nobody "+
					"by itself; every seeded agent except Jim carries an explicit per-agent ask "+
					"that still wins under strictest-wins", id, tool)
		}
	}

	// The Judge's explicit per-agent "deny" (systemAgentSeed, DS-6) must also
	// survive the raised ceiling — deny beats allow in the same merge.
	for _, tool := range planExecutionTools {
		assert.Equalf(t, "deny", resolveFor(t, cfg, string(coreagent.IDJudge), tool, nil),
			"(Judge, %s) must resolve deny — plan-execution is verifier-inapplicable (DS-6)", tool)
	}
}

// TestEffectiveResolution_SeededAgentAllow_IsNeverOverruledByCeiling is the
// BUG-CLASS lock, not a single-tool lock.
//
// The same defect has now landed three times in this codebase — inspect_session
// (Judge's allow overruled to deny by a "deny" ceiling), ADR-055's plan_correct
// (caught in review before shipping), and ADR-052's create_plan/execute_plan/
// run_task (shipped, and cost a 300 s per-call stall). Each was found by hand,
// on the specific tool someone happened to look at.
//
// The invariant: for EVERY seeded agent and EVERY tool in its own policy map,
// a per-agent "allow" must RESOLVE to "allow". A seed that says allow while the
// runtime resolves ask (or deny) is a lie in the data — the operator-visible
// posture disagrees with the seeded intent, and nothing in the type system,
// the boot-time coverage validation, or the per-tool tests catches it.
//
// Note this deliberately does NOT assert the converse. A ceiling legitimately
// TIGHTENS a per-agent allow down to ask/deny as a design choice in some cases;
// what it must never do is so SILENTLY. If a future change genuinely needs a
// ceiling that overrules a seeded allow, this test will fail and the fix is to
// change the per-agent seed to match reality (so the data is honest), not to
// weaken this assertion.
func TestEffectiveResolution_SeededAgentAllow_IsNeverOverruledByCeiling(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	global := globalPolicyMap(cfg)
	require.NotEmpty(t, global, "default config must seed a global tool-policy ceiling")

	checked := 0
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if ac.Tools == nil {
			continue
		}
		for tool, perAgent := range ac.Tools.Builtin.Policies {
			if perAgent != config.ToolPolicyAllow {
				continue
			}
			polCfg := &tools.ToolPolicyCfg{
				Policies:       ac.Tools.Builtin.Policies,
				GlobalPolicies: global,
			}
			got := tools.ResolveEffectivePolicy(polCfg, tool)
			checked++
			assert.Equalf(t, "allow", got,
				"(%s, %s): seeded per-agent policy is \"allow\" but the REAL resolver returns %q — "+
					"the global ceiling (%q) is silently overruling the seed under strictest-wins. "+
					"Either raise the ceiling in pkg/config/defaults.go, or change this agent's own "+
					"seed in pkg/coreagent/core.go to say what actually happens",
				ac.ID, tool, got, global[tool])
		}
	}

	// Guard against the assertion loop silently covering nothing (an empty
	// seed, a renamed field) and reporting a vacuous pass.
	require.Greater(t, checked, 100,
		"expected the seeded roster to contain many per-agent allow entries; "+
			"got %d — the seed shape probably changed and this test is no longer covering it", checked)
}
