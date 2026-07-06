package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// MIGRATED to the per-workspace graph model.
//
// Previously these tests fed coreagent.SeedConfig's per-agent DelegationPolicy
// straight into buildDelegationDenyChecker. The runtime gate no longer reads the
// config — it reads the per-workspace delegation GRAPH. The seed graph
// (defaultWorkspaceDelegationEdges in pkg/gateway) is DERIVED from those same
// per-agent policies, so here we (1) confirm coreagent still seeds the expected
// per-agent trust edges (the SEED source of truth) and (2) replay the equivalent
// edges into a workspace graph and assert the runtime gate enforces them. The
// gateway's defaultWorkspaceDelegationEdges has its own round-trip test
// (pkg/gateway) proving the seed→graph derivation.

// seededAgent returns the seeded AgentConfig for id after running coreagent.SeedConfig.
func seededAgent(t *testing.T, cfg *config.Config, id string) *config.AgentConfig {
	t.Helper()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			return &cfg.Agents.List[i]
		}
	}
	t.Fatalf("seeded agent %q not found", id)
	return nil
}

// seedEdgesFromConfig replays a config's per-agent DelegationPolicy into the
// equivalent workspace graph edges (the same derivation
// defaultWorkspaceDelegationEdges performs in pkg/gateway). Local, non-wildcard,
// non-self targets only.
func seedEdgesFromConfig(cfg *config.Config) []graphEdge {
	var edges []graphEdge
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		dp := ac.DelegationPolicy
		if dp == nil || len(dp.To) == 0 {
			continue
		}
		modes := make([]string, 0, len(dp.Modes))
		for _, m := range dp.Modes {
			modes = append(modes, string(m))
		}
		var depth *int
		if dp.Depth != nil {
			d := *dp.Depth
			depth = &d
		}
		for _, ref := range dp.To {
			if ref.Kind != config.AgentRefKindLocal || ref.ID == "*" || ref.ID == ac.ID {
				continue
			}
			edges = append(edges, edge(ac.ID, ref.ID, append([]string(nil), modes...), depth))
		}
	}
	return edges
}

// TestSeedConfig_JimToAvaTaskEdgePresent confirms coreagent still seeds the
// Jim→Ava trust edge (the SEED source of truth that derives the workspace graph).
func TestSeedConfig_JimToAvaTaskEdgePresent(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	jim := seededAgent(t, cfg, string(coreagent.IDJim))
	if jim.DelegationPolicy == nil {
		t.Fatal("Jim must have a seeded DelegationPolicy")
	}
	found := false
	for _, ref := range jim.DelegationPolicy.To {
		if ref.ID == string(coreagent.IDAva) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Jim's seeded trust set must include Ava, got: %+v", jim.DelegationPolicy.To)
	}
}

// TestSeededGraph_JimToAvaTaskAllowed verifies the seeded trust graph ALLOWS
// Jim → Ava in task mode once replayed into a workspace graph.
func TestSeededGraph_JimToAvaTaskAllowed(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	seedWorkspaceGraph(t, testWS, true, seedEdgesFromConfig(cfg))

	check := buildDelegationDenyChecker(string(coreagent.IDJim), cfg.Agents.Defaults, config.DelegationModeTask)
	if denial := check(ctxWS(testWS, 0), string(coreagent.IDAva)); denial != nil {
		t.Fatalf("Jim → Ava (task) must be allowed, got deny: %+v", denial)
	}
}

// TestSeededGraph_BaseToWorkerAllowed verifies every base agent can offload labor
// to the general-purpose worker in task mode out of the box.
func TestSeededGraph_BaseToWorkerAllowed(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	seedWorkspaceGraph(t, testWS, true, seedEdgesFromConfig(cfg))

	for _, id := range []coreagent.CoreAgentID{
		coreagent.IDJim, coreagent.IDMia, coreagent.IDRay, coreagent.IDAva,
	} {
		check := buildDelegationDenyChecker(string(id), cfg.Agents.Defaults, config.DelegationModeTask)
		if denial := check(ctxWS(testWS, 0), string(coreagent.IDWorker)); denial != nil {
			t.Fatalf("%s → worker (task) must be allowed, got deny: %+v", id, denial)
		}
	}
}

// TestSeededGraph_DisallowedTargetDenied verifies deny-by-default holds for
// targets outside the seeded trust set (Mia → Ava is NOT seeded).
func TestSeededGraph_DisallowedTargetDenied(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	seedWorkspaceGraph(t, testWS, true, seedEdgesFromConfig(cfg))

	check := buildDelegationDenyChecker(string(coreagent.IDMia), cfg.Agents.Defaults, config.DelegationModeTask)
	if denial := check(ctxWS(testWS, 0), string(coreagent.IDAva)); denial == nil {
		t.Fatal("Mia → Ava must be DENIED (no edge in the seeded graph)")
	}
}

// TestSeededGraph_JimAwaitModeAllowed verifies Jim's seeded policy permits the
// synchronous (await) subagent mode, while a base worker-offloader (Mia) does NOT
// permit await (only task/background seeded).
func TestSeededGraph_JimAwaitModeAllowed(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	seedWorkspaceGraph(t, testWS, true, seedEdgesFromConfig(cfg))

	jimCheck := buildDelegationDenyChecker(string(coreagent.IDJim), cfg.Agents.Defaults, config.DelegationModeAwait)
	if denial := jimCheck(ctxWS(testWS, 0), string(coreagent.IDWorker)); denial != nil {
		t.Fatalf("Jim → worker (await) must be allowed, got deny: %+v", denial)
	}

	miaCheck := buildDelegationDenyChecker(string(coreagent.IDMia), cfg.Agents.Defaults, config.DelegationModeAwait)
	if denial := miaCheck(ctxWS(testWS, 0), string(coreagent.IDWorker)); denial == nil {
		t.Fatal("Mia → worker (await) must be DENIED — Mia only has task/background seeded")
	}
}
