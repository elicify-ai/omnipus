// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestBuildDelegationContext_AdvertisedSubsetOfEnforceable (#459) drives the
// real enforcement gate (enforceEdgeModeAndDepth) and the block renderer over
// a grid of (edge depth, global cap, current chain depth) and asserts the
// invariant advertisement ⊆ enforcement on the runtime-depth axis: a target is
// advertised exactly when the gate would not deny it for depth.
func TestBuildDelegationContext_AdvertisedSubsetOfEnforceable(t *testing.T) {
	edgeDepths := []*int{nil, ptr(1), ptr(2), ptr(5)}
	globalCaps := []int{0, 2, 4}
	for _, ed := range edgeDepths {
		for _, gc := range globalCaps {
			for depth := 0; depth <= 6; depth++ {
				name := fmt.Sprintf("edge=%v/global=%d/depth=%d", ed, gc, depth)
				t.Run(name, func(t *testing.T) {
					effectiveGlobal := resolveEffectiveDelegationDepth(nil, gc)
					tgt := makeTarget("ava", nil, ed)
					// Mirror wireDelegationInjectors: per-edge cap from the RAW
					// configured ceiling, footer from the backstop-resolved one.
					tgt.DepthCap = resolveEffectiveDelegationDepth(ed, gc)
					block := buildDelegationContext([]delegationTarget{tgt}, effectiveGlobal, depth)
					advertised := strings.Contains(block, `delegate(agent_id="ava"`)

					edge := &workspace.DelegationEdge{
						FromAgent: "mia", ToAgent: "ava", Depth: ed,
						Modes: []workspace.DelegationMode{workspace.ModeDirect},
					}
					denial := enforceEdgeModeAndDepth(
						ctxAtDepth(depth), edge, "mia", "ava", config.DelegationModeBackground, gc)
					enforced := denial == nil
					if denial != nil && denial.Policy != tools.DenyDepth {
						t.Fatalf("unexpected non-depth denial: %+v", denial)
					}
					if advertised != enforced {
						t.Fatalf("advertised=%v but gate allows=%v\nblock:\n%s", advertised, enforced, block)
					}
				})
			}
		}
	}
}

// TestBuildDelegationContext_AtCeilingSaysSo (#459): when every target is cut
// by the depth ceiling, the block must say plainly that no further
// delegation is possible — not the "no targets configured" text, which would
// misdescribe the cause.
func TestBuildDelegationContext_AtCeilingSaysSo(t *testing.T) {
	got := buildDelegationContext([]delegationTarget{makeTarget("ava", nil, nil)}, 3, 3)
	if !strings.Contains(got, "maximum delegation depth") {
		t.Fatalf("expected an at-max-depth note, got:\n%s", got)
	}
	if strings.Contains(got, `delegate(agent_id="ava"`) {
		t.Fatalf("must not advertise a target the gate will deny:\n%s", got)
	}
	if strings.Contains(got, "none configured for you here") {
		t.Fatalf("at-ceiling note must not be conflated with 'no targets configured':\n%s", got)
	}
}

// TestWireDelegationInjectors_BlockReflectsTurnDepth (#459) proves the depth
// reaches the injector through the real prompt-assembly path
// (buildDynamicContext → delegationInjector), not just the renderer.
func TestWireDelegationInjectors_BlockReflectsTurnDepth(t *testing.T) {
	const wsID = "01JWDEPTHPROMPT0000000459"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("jim", "worker", nil, nil),
	})
	_, cb := wireTestLoopWithGraphAndMaxDepth(t, "jim", 2)

	root := cb.buildDynamicContext(0, "", "", "", "", "")
	if !strings.Contains(root, `delegate(agent_id="worker"`) {
		t.Fatalf("depth 0 must advertise the target, got:\n%s", root)
	}
	atCeiling := cb.buildDynamicContext(2, "", "", "", "", "")
	if strings.Contains(atCeiling, `delegate(agent_id="worker"`) {
		t.Fatalf("depth 2 (== cap 2) must not advertise the target, got:\n%s", atCeiling)
	}
	if !strings.Contains(atCeiling, "maximum delegation depth") {
		t.Fatalf("depth 2 must carry the at-max-depth note, got:\n%s", atCeiling)
	}
}

// TestWireDelegationInjectors_LargerEdgeCapNotClampedToBackstop (#459): with no
// global ceiling configured and a per-edge cap of 5, the gate allows chain
// depth 3, so the block must still advertise the target there.
func TestWireDelegationInjectors_LargerEdgeCapNotClampedToBackstop(t *testing.T) {
	const wsID = "01JWDEPTHPROMPT0000000460"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("jim", "worker", nil, ptr(5)),
	})
	_, cb := wireTestLoopWithGraphAndMaxDepth(t, "jim", 0)

	got := cb.buildDynamicContext(3, "", "", "", "", "")
	if !strings.Contains(got, `delegate(agent_id="worker"`) {
		t.Fatalf("edge cap 5 allows chain depth 3; block must advertise, got:\n%s", got)
	}
}
