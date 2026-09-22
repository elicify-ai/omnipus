// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// wireTestLoopWithGraphAndMaxDepth is a variant of wireTestLoopWithGraph
// (delegation_wiring_test.go) that additionally seeds
// cfg.Performance.MaxDelegationDepth before building the loop, so tests can
// exercise wireDelegationInjectors' "explicit global depth" advertisement
// path. maxDepth <= 0 leaves the field at its zero value (unset).
func wireTestLoopWithGraphAndMaxDepth(t *testing.T, agentID string, maxDepth int) (*AgentLoop, *ContextBuilder) {
	t.Helper()
	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	if maxDepth > 0 {
		cfg.Performance.MaxDelegationDepth = maxDepth
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
	return al, inst.ContextBuilder
}

// TestWireDelegationInjectors_AdvertisesEffectiveDepthNotRawUncapped is the
// headline #477 regression test: when an operator has set NEITHER a per-edge
// Depth NOR performance.max_delegation_depth, the delegation system-prompt block must
// advertise "max chain depth: 3" (the actual effective cap that will be
// enforced) — never "uncapped", which silently mismatched the spawn-time
// backstop's real default before this fix.
func TestWireDelegationInjectors_AdvertisesEffectiveDepthNotRawUncapped(t *testing.T) {
	const wsID = "01JWDEPTHPROMPT0000000001"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("jim", "worker", nil, nil), // no per-edge Depth (inherit); global unset
	})

	_, cb := wireTestLoopWithGraphAndMaxDepth(t, "jim", 0)

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
// performance.max_delegation_depth is advertised verbatim (no stricter per-edge Depth
// applies).
func TestWireDelegationInjectors_AdvertisesExplicitGlobalDepth(t *testing.T) {
	const wsID = "01JWDEPTHPROMPT0000000002"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("jim", "worker", nil, nil),
	})

	_, cb := wireTestLoopWithGraphAndMaxDepth(t, "jim", 7)

	got := cb.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(got, "max chain depth: 7") {
		t.Fatalf("expected the explicit global cap (7) to be advertised, got:\n%s", got)
	}
	if strings.Contains(got, "max chain depth: uncapped") {
		t.Fatalf("must not advertise 'uncapped' when an explicit global cap is set; got:\n%s", got)
	}
}

// TestDelegationWiring_PerformanceDepthControlsOwnershipWalk proves the third
// former depth reader uses the same Performance value as prompt rendering and
// authorization. A root caller five links above its child must remain an
// authorized steering ancestor when the configured cap is five.
func TestDelegationWiring_PerformanceDepthControlsOwnershipWalk(t *testing.T) {
	const wsID = "01JWDEPTHOWNERSHIP0000001"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("jim", "worker", nil, nil),
	})
	al, _ := wireTestLoopWithGraphAndMaxDepth(t, "jim", 5)

	lifecycle := session.NewLifecycleStore(t.TempDir())
	parent := "root-chat"
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("depth-%d", i)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID:      id,
			AgentID:        "worker",
			WorkspaceID:    wsID,
			OwnerScopeKind: session.OwnerScopeParentSession,
			OwnerScopeID:   parent,
			State:          session.LifecycleRunning,
			SteeredBy:      &session.SteeredBy{SteeringSessionID: parent, RootSessionID: "root-chat"},
		}); err != nil {
			t.Fatalf("persist lifecycle depth %d: %v", i, err)
		}
		parent = id
	}

	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	inst, ok := al.GetRegistry().GetAgent("jim")
	if !ok {
		t.Fatal("jim missing from registry")
	}
	raw, ok := inst.Tools.Get("delegate")
	if !ok {
		t.Fatal("delegate tool missing")
	}
	delegateTool, ok := raw.(*tools.DelegateTool)
	if !ok {
		t.Fatalf("delegate tool type = %T", raw)
	}
	delegateTool.SetSessionMessagingEnabled(func() bool { return true })

	result := delegateTool.Execute(
		tools.WithTranscriptSessionID(context.Background(), "root-chat"),
		map[string]any{"action": "peek", "session_id": "depth-5"},
	)
	if result.IsError {
		t.Fatalf("performance depth cap 5 did not authorize a five-link ownership walk: %s", result.ForLLM)
	}
}
