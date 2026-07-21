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
		id := id
		assert.Equalf(t, "deny", resolveFor(t, cfg, string(id), "inspect_session", nil),
			"(%s, inspect_session) must resolve deny through the real compositor merge", id)
	}
}

// TestEffectiveResolution_ExecutePlan_DefaultCeiling_EveryoneAsk verifies
// DS-6's default resolved posture (ADR-052 §6.1): under the DEFAULT ceiling
// (execute_plan seeded "ask"), strictest-wins means EVERY agent — including
// Jim, whose own per-agent policy is "allow" — resolves "ask", because ask
// beats allow. Full autonomy is a deliberate one-seed-edit operator opt-in
// (the companion test below), not the fresh-install default.
func TestEffectiveResolution_ExecutePlan_DefaultCeiling_EveryoneAsk(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	assert.Equal(t, "ask", resolveFor(t, cfg, string(coreagent.IDJim), "execute_plan", nil),
		"(Jim, execute_plan) must resolve ask under the default ceiling (ceiling ask beats Jim's own allow)")
	assert.Equal(t, "ask", resolveFor(t, cfg, string(coreagent.IDRay), "execute_plan", nil),
		"(Ray, execute_plan) must resolve ask under the default ceiling")
}

// TestEffectiveResolution_ExecutePlan_CeilingRaised_JimResolvesAllow proves
// the "one seed-edit away" autonomy claim (ADR-052 §6.1, grill F5): when an
// operator raises the GLOBAL CEILING entry for execute_plan to "allow", Jim
// (already seeded "allow" per-agent) resolves "allow" — full autonomy —
// while every other seeded agent (explicit per-agent "ask") still resolves
// "ask" and therefore still prompts.
func TestEffectiveResolution_ExecutePlan_CeilingRaised_JimResolvesAllow(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	raised := globalPolicyMap(cfg)
	raised["execute_plan"] = config.ToolPolicyAllow

	assert.Equal(t, "allow", resolveFor(t, cfg, string(coreagent.IDJim), "execute_plan", raised),
		"(Jim, execute_plan) must resolve allow once the ceiling is raised to allow")
	assert.Equal(t, "ask", resolveFor(t, cfg, string(coreagent.IDRay), "execute_plan", raised),
		"(Ray, execute_plan) must still resolve ask even after the ceiling is raised — Ray's own per-agent entry stays explicit ask")
}
