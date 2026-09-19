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
//
// ADR-037 (Wave 2): AgentConfig.DelegationPolicy no longer exists — the seed
// source of truth is now coreagent.SeedDelegationEdges (an exported wrapper
// around coreAgentDelegation), consulted by id rather than read off a seeded
// AgentConfig field.

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

// seedEdgesFromConfig replays coreagent's per-agent seed delegation policy
// (coreagent.SeedDelegationEdges, keyed by each agent id present in cfg) into
// the equivalent workspace graph edges (the same derivation
// defaultWorkspaceDelegationEdges performs in pkg/gateway). Local, non-wildcard,
// non-self targets only.
func seedEdgesFromConfig(cfg *config.Config) []graphEdge {
	var edges []graphEdge
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		dp := coreagent.SeedDelegationEdges(coreagent.CoreAgentID(ac.ID))
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

// TestSeedConfig_JimTaskTargetsMatchADR090 pins the orchestrator's staff and
// self-delegation targets, including Ava for specialist/skill proposals.
func TestSeedConfig_JimTaskTargetsMatchADR090(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	seededAgent(t, cfg, string(coreagent.IDJim))
	policy := coreagent.SeedDelegationEdges(coreagent.IDJim)
	if policy == nil {
		t.Fatal("Jim must have seeded delegation targets")
	}
	want := map[string]bool{"planner": true, "researcher": true, "worker": true, "jim": true, "ava": true}
	if len(policy.To) != len(want) {
		t.Fatalf("Jim targets=%v, want exactly planner/researcher/worker/jim/ava", policy.To)
	}
	for _, ref := range policy.To {
		if ref.Kind != config.AgentRefKindLocal || !want[ref.ID] {
			t.Fatalf("unexpected or duplicate Jim target: %+v", ref)
		}
		delete(want, ref.ID)
	}
}

// TestSeededGraph_JimToAvaTaskAllowed verifies Jim can request specialist/skill proposals.
func TestSeededGraph_JimToAvaTaskAllowed(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	seedWorkspaceGraph(t, testWS, true, seedEdgesFromConfig(cfg))

	check := buildDelegationDenyCheckerForTaskReassignment(
		string(coreagent.IDJim),
		cfg.Agents.Defaults,
		config.DelegationModeTask,
	)
	if denial := check(ctxWS(testWS, 0), string(coreagent.IDAva)); denial != nil {
		t.Fatalf("Jim → Ava (task) must be allowed for specialist/skill proposals, got: %+v", denial)
	}
}

// TestSeededGraph_JimToWorkerAllowed verifies the ADR-090 orchestrator can
// offload labor to General Purpose in task mode out of the box.
func TestSeededGraph_JimToWorkerAllowed(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	seedWorkspaceGraph(t, testWS, true, seedEdgesFromConfig(cfg))

	check := buildDelegationDenyCheckerForTaskReassignment(
		string(coreagent.IDJim), cfg.Agents.Defaults, config.DelegationModeTask,
	)
	if denial := check(ctxWS(testWS, 0), string(coreagent.IDWorker)); denial != nil {
		t.Fatalf("Jim → worker (task) must be allowed, got deny: %+v", denial)
	}
}

// TestSeededGraph_DisallowedTargetDenied verifies deny-by-default holds for
// targets outside the seeded trust set (Mia → Ava is NOT seeded).
func TestSeededGraph_DisallowedTargetDenied(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	seedWorkspaceGraph(t, testWS, true, seedEdgesFromConfig(cfg))

	check := buildDelegationDenyCheckerForTaskReassignment(
		string(coreagent.IDMia),
		cfg.Agents.Defaults,
		config.DelegationModeTask,
	)
	if denial := check(ctxWS(testWS, 0), string(coreagent.IDAva)); denial == nil {
		t.Fatal("Mia → Ava must be DENIED (no edge in the seeded graph)")
	}
}

// TestSeededGraph_JimAwaitModeAllowed checks Jim's permitted await path and
// Mia's lack of a staff delegation edge. Mode-category rules remain covered
// separately by synthetic graph tests in delegation_enforce_test.go.
func TestSeededGraph_JimAwaitModeAllowed(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	seedWorkspaceGraph(t, testWS, true, seedEdgesFromConfig(cfg))

	jimCheck := buildDelegationDenyCheckerForDelegate(
		string(coreagent.IDJim),
		cfg.Agents.Defaults,
		config.DelegationModeAwait,
	)
	if denial := jimCheck(ctxWS(testWS, 0), string(coreagent.IDWorker)); denial != nil {
		t.Fatalf("Jim → worker (await) must be allowed, got deny: %+v", denial)
	}

	// Mia hands substantial work to Jim rather than assigning staff directly.
	miaCheck := buildDelegationDenyCheckerForDelegate(
		string(coreagent.IDMia),
		cfg.Agents.Defaults,
		config.DelegationModeAwait,
	)
	if denial := miaCheck(ctxWS(testWS, 0), string(coreagent.IDWorker)); denial == nil || denial.Policy != "trust_set" {
		t.Fatalf("Mia → worker must be denied by the trust graph, got: %+v", denial)
	}
}
