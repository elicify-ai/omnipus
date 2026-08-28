// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// wireTestLoopWithGraphAndMaxDepth is a variant of wireTestLoopWithGraph
// (delegation_wiring_test.go) that additionally seeds
// cfg.Agents.Defaults.SubTurn.MaxDepth before building the loop, so tests can
// exercise wireDelegationInjectors' "explicit global depth" advertisement
// path. maxDepth <= 0 leaves the field at its zero value (unset).
func wireTestLoopWithGraphAndMaxDepth(t *testing.T, agentID string, maxDepth int) *ContextBuilder {
	t.Helper()
	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	if maxDepth > 0 {
		cfg.Agents.Defaults.SubTurn.MaxDepth = maxDepth
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("agent %q not in registry after loop init", agentID)
	}
	return inst.ContextBuilder
}

// TestWireDelegationInjectors_AdvertisesEffectiveDepthNotRawUncapped is the
// headline #477 regression test: when an operator has set NEITHER a per-edge
// Depth NOR SubTurn.MaxDepth, the delegation system-prompt block must
// advertise "max chain depth: 3" (the actual effective cap that will be
// enforced) — never "uncapped", which silently mismatched the spawn-time
// backstop's real default before this fix.
func TestWireDelegationInjectors_AdvertisesEffectiveDepthNotRawUncapped(t *testing.T) {
	const wsID = "01JWDEPTHPROMPT0000000001"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("ava", "ray", nil, nil), // no per-edge Depth (inherit); global unset
	})

	cb := wireTestLoopWithGraphAndMaxDepth(t, "ava", 0)

	got := cb.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(got, "max chain depth: 3") {
		t.Fatalf("expected the effective backstop default (3) to be advertised, got:\n%s", got)
	}
	if strings.Contains(got, "max chain depth: uncapped") {
		t.Fatalf(
			"must NOT advertise 'uncapped' when a 4th hop will actually be rejected at the effective cap; got:\n%s",
			got,
		)
	}
}

// TestWireDelegationInjectors_AdvertisesExplicitGlobalDepth confirms the case
// that already worked correctly before the fix continues to: an explicit
// SubTurn.MaxDepth is advertised verbatim (no stricter per-edge Depth
// applies).
func TestWireDelegationInjectors_AdvertisesExplicitGlobalDepth(t *testing.T) {
	const wsID = "01JWDEPTHPROMPT0000000002"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("ava", "ray", nil, nil),
	})

	cb := wireTestLoopWithGraphAndMaxDepth(t, "ava", 7)

	got := cb.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(got, "max chain depth: 7") {
		t.Fatalf("expected the explicit global cap (7) to be advertised, got:\n%s", got)
	}
	if strings.Contains(got, "max chain depth: uncapped") {
		t.Fatalf("must not advertise 'uncapped' when an explicit global cap is set; got:\n%s", got)
	}
}
