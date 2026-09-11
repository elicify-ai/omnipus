// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This is an EXTERNAL test package (tools_test) on purpose: it imports
// pkg/agent, which imports pkg/tools. That direction is legal only from an
// external test package, and it is what lets these tests exercise the REAL
// production wiring path (agent.NewAgentLoop -> registerSharedTools ->
// wireGoalToolsForAgent) instead of a test-supplied registration.
//
// The defect these tests pin: `goal_claim` (ADR-084 rev 9 §O / D12) was
// fully implemented, seeded into pkg/coreagent's allStaticToolNames, seeded
// into the global tool-policy ceiling and into every agent's override map —
// and constructed NOWHERE outside tests. No model was ever offered the tool,
// so the claim-triggered adjudication path (goal_loop.go's claim detection)
// could never fire. Its sibling `browser_handover` (ADR-085 D7) had the
// mirror-image hole: registered at runtime but missing from the metadata
// catalog that builds the tool-policy coverage universe.
package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// newWiringTestLoop builds a real AgentLoop over a minimal one-agent config,
// rooted entirely in per-test temp dirs. Nothing here registers a tool: every
// tool the returned agent carries was put there by the production wiring pass
// (loop.go's registerSharedTools), which is the whole point.
func newWiringTestLoop(t *testing.T) *agent.AgentInstance {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{{
				ID:   "native-agent",
				Name: "Native Agent",
				Type: config.AgentTypeWorker,
				Home: t.TempDir(),
			}},
		},
	}
	al, err := agent.NewAgentLoop(cfg, bus.NewMessageBus(), nil)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(al.Close)
	inst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok || inst == nil {
		t.Fatal("native-agent was not registered by NewAgentLoop")
	}
	return inst
}

// TestGoalClaimRegisteredOnEveryAgentByProductionWiring is the regression
// guard for the ADR-084 headline defect: `goal_claim` must be on a normal
// agent's own tool registry — the set the model is actually offered — put
// there by wireGoalToolsForAgent, not by this test.
//
// It asserts two separate things, because either alone would pass while the
// feature stayed dead:
//
//  1. the tool is PRESENT on the agent's registry; and
//  2. its late-bound GoalRecordAccess seam is LIVE, not the nil-seam
//     metadata instance from the catalog. The discriminator is the tool's
//     own first two refusals: a nil seam refuses with "no goal-record store
//     is wired on this deployment" BEFORE it ever looks at the session, while
//     a live seam gets past that and refuses on the missing session context.
//     A catalog instance accidentally registered per-agent would satisfy (1)
//     and fail (2).
func TestGoalClaimRegisteredOnEveryAgentByProductionWiring(t *testing.T) {
	inst := newWiringTestLoop(t)

	tl, ok := inst.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatalf("%q must be registered on every agent by the production wiring pass "+
			"(pkg/agent/goal_record_wiring.go's wireGoalToolsForAgent) — without it no model is "+
			"ever offered the tool and ADR-084's claim-triggered adjudication can never fire",
			tools.GoalClaimToolName)
	}

	res := tl.Execute(context.Background(), map[string]any{"status": tools.GoalClaimStatusMet, "evidence": "x"})
	if res == nil || !res.IsError {
		t.Fatalf("goal_claim with no session context must refuse; got %+v", res)
	}
	if strings.Contains(res.ForLLM, "no goal-record store is wired") {
		t.Fatalf("the registered goal_claim carries a NIL GoalRecordAccess seam — it is the "+
			"metadata-only catalog instance, not the wired one: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "no session context") {
		t.Fatalf("expected the live-seam refusal on the missing session context, got: %s", res.ForLLM)
	}
}

// TestGoalClaimInGeneralBuiltinMetadata pins the catalog half. The catalog is
// what pkg/gateway's buildKnownBuiltinToolNames walks to build the
// Constraint #6 tool-policy coverage universe (and what GET /api/v1/tools
// exposes), so a tool seeded in coreagent.AllStaticToolNames but absent here
// fails TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog and
// is never reconciled into an upgraded install's ceiling.
func TestGoalClaimInGeneralBuiltinMetadata(t *testing.T) {
	assertCatalogHas(t, "GeneralBuiltinMetadata", tools.GeneralBuiltinMetadata(), tools.GoalClaimToolName)
}

// TestBrowserHandoverInBrowserBuiltinMetadata pins the same property for
// browser_handover (ADR-085 D7), which is registered unconditionally at
// runtime (pkg/tools/browser/register.go) and was missing from this catalog.
func TestBrowserHandoverInBrowserBuiltinMetadata(t *testing.T) {
	assertCatalogHas(t, "BrowserBuiltinMetadata", browser.BrowserBuiltinMetadata(), "browser_handover")
}

func assertCatalogHas(t *testing.T, catalogName string, catalog []tools.Tool, want string) {
	t.Helper()
	for _, tl := range catalog {
		if tl.Name() == want {
			return
		}
	}
	t.Fatalf("%s() must contain %q — the tool-policy coverage universe "+
		"(pkg/gateway's buildKnownBuiltinToolNames) is built from this catalog, so an "+
		"absent entry leaves a seeded, registered tool with no ceiling coverage", catalogName, want)
}
