// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestGlobalToolPolicy_HotUpdate_EnforcedOnRunningAgent is the O7 hot-path
// regression test (BLOCKER from the security-lead review). It exercises the
// LIVE update path on an ALREADY-BUILT agent — not just the construction-time
// agentToolsCfgToPolicy unit — to prove that a global "deny" set via the
// reload path (TriggerReload → ReloadProviderAndConfig → NewAgentRegistry) is
// enforced at EXEC time, not only persisted/shown by GET.
//
// Before the fix, putToolPolicies only persisted + SwapConfig'd; the running
// agent kept its boot-time toolPolicy snapshot whose GlobalPolicies was stale,
// so a global deny was fail-open until a process restart. This test fails if
// that regression returns: exec-time resolution (FilterToolsByPolicy on the
// agent's LoadToolPolicy(), and resolveSingleToolPolicy) must return "deny"
// after the reload.
func TestGlobalToolPolicy_HotUpdate_EnforcedOnRunningAgent(t *testing.T) {
	al, _ := schedTestLoop(t)

	const tool = "dangerous_tool"
	const agentID = "mia"

	// Put a real agent in the CONFIG list so it survives a reload (which rebuilds
	// the registry from cfg.Agents.List — the exact NewAgentRegistry path the
	// live PUT triggers). Build it once with NO global deny.
	al.cfg.Agents.List = []config.AgentConfig{{ID: agentID, Name: agentID}}
	require.NoError(t,
		al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, al.cfg),
		"initial reload to seed the configured agent must succeed",
	)

	mia, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "configured agent must exist after seed reload")
	mia.Tools.Register(&dangerousStubTool{})

	// Sanity: exec-time resolution allows the tool before any global deny.
	requireEffectivePolicy(t, mia, tool, "allow")
	requireSingleToolPolicy(t, al, mia, tool, "allow")

	// Now apply a GLOBAL deny via the exact reload path the live PUT triggers
	// (putToolPolicies → TriggerReload → ReloadProviderAndConfig). Build a fresh
	// config carrying the new sandbox.tool_policies (what safeUpdateConfigJSON
	// would have persisted) and reload — rebuilding every agent instance with the
	// new GlobalPolicies. We construct a fresh Config (not a struct copy of
	// al.cfg, which holds a sync.RWMutex) and re-state only the fields the reload
	// needs: the agent list and the new global deny.
	newCfg := &config.Config{}
	newCfg.Agents.Defaults = al.cfg.Agents.Defaults
	newCfg.Agents.List = []config.AgentConfig{{ID: agentID, Name: agentID}}
	newCfg.Sandbox.ToolPolicies = map[string]string{tool: "deny"}
	require.NoError(t,
		al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, newCfg),
		"reload with global deny must succeed",
	)

	// After the reload the registry holds a freshly-built instance. Fetch it and
	// re-register the stub tool (NewAgentRegistry creates fresh Tools registries).
	rebuilt, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "agent must exist after reload")
	rebuilt.Tools.Register(&dangerousStubTool{})

	// EXEC-time resolution must now return deny on the running agent — this is
	// the assertion that the global deny is enforced live, not just persisted.
	requireEffectivePolicy(t, rebuilt, tool, "deny")
	requireSingleToolPolicy(t, al, rebuilt, tool, "deny")

	// The denied tool must also be removed from the assembled tool list (it is
	// invisible to the LLM), proving the deny is structural, not advisory.
	filtered, _ := tools.FilterToolsByPolicy(
		rebuilt.Tools.GetAll(), rebuilt.AgentType, rebuilt.LoadToolPolicy(),
	)
	for _, ft := range filtered {
		if ft.Name() == tool {
			t.Fatalf("denied tool %q must be removed from the filtered tool list, but it is present", tool)
		}
	}
}

// requireEffectivePolicy asserts the exec-time effective policy for toolName on
// the agent, resolved exactly as the loop does: FilterToolsByPolicy over the
// agent's registered tools using its live LoadToolPolicy() snapshot.
func requireEffectivePolicy(t *testing.T, ag *AgentInstance, toolName, want string) {
	t.Helper()
	_, pmap := tools.FilterToolsByPolicy(ag.Tools.GetAll(), ag.AgentType, ag.LoadToolPolicy())
	got, present := pmap[toolName]
	if want == "deny" {
		// A denied tool is dropped from the filter map entirely.
		require.False(t, present,
			"FilterToolsByPolicy(LoadToolPolicy()) must drop denied tool %q (got policy %q)", toolName, got)
		return
	}
	require.True(t, present, "tool %q must be present in the filter map", toolName)
	require.Equal(t, want, got,
		"FilterToolsByPolicy(LoadToolPolicy()) effective policy for %q", toolName)
}

// requireSingleToolPolicy asserts the loop's per-call resolver
// (resolveSingleToolPolicy) returns the expected policy for the agent — this is
// the exact path the agent loop uses immediately before each tool Execute.
func requireSingleToolPolicy(t *testing.T, al *AgentLoop, ag *AgentInstance, toolName, want string) {
	t.Helper()
	ts := &turnState{agent: ag, agentID: ag.ID}
	got := al.resolveSingleToolPolicy(ts, toolName)
	require.Equal(t, want, got,
		"resolveSingleToolPolicy for %q on agent %q", toolName, ag.ID)
}
