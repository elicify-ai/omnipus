package agent

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
)

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

// TestSeededDelegation_JimToAvaTaskAllowed verifies the seeded trust graph now
// ALLOWS Jim → Ava in task mode (previously dead: base agents had no policy).
func TestSeededDelegation_JimToAvaTaskAllowed(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	jim := seededAgent(t, cfg, string(coreagent.IDJim))

	check := buildDelegationDenyChecker(string(coreagent.IDJim), jim, cfg.Agents.Defaults,
		config.DelegationModeTask, nil)
	if denial := check(ctxAtDepth(0), string(coreagent.IDAva)); denial != nil {
		t.Fatalf("Jim → Ava (task) must be allowed, got deny: %+v", denial)
	}
}

// TestSeededDelegation_BaseToWorkerAllowed verifies every base agent can offload
// labor to the general-purpose worker in task mode out of the box.
func TestSeededDelegation_BaseToWorkerAllowed(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)

	for _, id := range []coreagent.CoreAgentID{
		coreagent.IDJim, coreagent.IDMia, coreagent.IDRay, coreagent.IDAva,
	} {
		ac := seededAgent(t, cfg, string(id))
		check := buildDelegationDenyChecker(string(id), ac, cfg.Agents.Defaults,
			config.DelegationModeTask, nil)
		if denial := check(ctxAtDepth(0), string(coreagent.IDWorker)); denial != nil {
			t.Fatalf("%s → worker (task) must be allowed, got deny: %+v", id, denial)
		}
	}
}

// TestSeededDelegation_DisallowedTargetDenied verifies deny-by-default holds for
// targets outside the seeded trust set (e.g. Mia → Ava is NOT seeded).
func TestSeededDelegation_DisallowedTargetDenied(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	mia := seededAgent(t, cfg, string(coreagent.IDMia))

	check := buildDelegationDenyChecker(string(coreagent.IDMia), mia, cfg.Agents.Defaults,
		config.DelegationModeTask, nil)
	// Mia's seeded To is only [worker]; Ava is not permitted.
	if denial := check(ctxAtDepth(0), string(coreagent.IDAva)); denial == nil {
		t.Fatal("Mia → Ava must be DENIED (not in Mia's seeded trust set)")
	}
}

// TestSeededDelegation_JimAwaitModeAllowed verifies Jim's seeded policy permits
// the synchronous (await) subagent mode, while a base worker-offloader (Mia)
// does NOT permit await (only task/background seeded).
func TestSeededDelegation_JimAwaitModeAllowed(t *testing.T) {
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)

	jim := seededAgent(t, cfg, string(coreagent.IDJim))
	jimCheck := buildDelegationDenyChecker(string(coreagent.IDJim), jim, cfg.Agents.Defaults,
		config.DelegationModeAwait, nil)
	if denial := jimCheck(ctxAtDepth(0), string(coreagent.IDWorker)); denial != nil {
		t.Fatalf("Jim → worker (await) must be allowed, got deny: %+v", denial)
	}

	mia := seededAgent(t, cfg, string(coreagent.IDMia))
	miaCheck := buildDelegationDenyChecker(string(coreagent.IDMia), mia, cfg.Agents.Defaults,
		config.DelegationModeAwait, nil)
	if denial := miaCheck(ctxAtDepth(0), string(coreagent.IDWorker)); denial == nil {
		t.Fatal("Mia → worker (await) must be DENIED — Mia only has task/background seeded")
	}
}
