package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

func TestSeed_GoalClaim_ADR090ResolvedPosture(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	for _, id := range []coreagent.CoreAgentID{
		coreagent.IDMia, coreagent.IDJim, coreagent.IDAva, coreagent.IDAdmin,
		coreagent.IDPlanner, coreagent.IDResearcher, coreagent.IDWorker,
	} {
		assert.Equalf(t, "allow", resolveFor(t, cfg, string(id), "goal_claim", nil), "%s goal_claim", id)
	}
	for _, id := range []coreagent.CoreAgentID{coreagent.IDJudge, coreagent.IDPlanSupervisor} {
		assert.Equalf(t, "deny", resolveFor(t, cfg, string(id), "goal_claim", nil), "%s goal_claim", id)
	}
}

func TestSeed_GoalClaim_OrdinaryOperatorOverridePersists(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	worker := findSeeded(t, cfg, string(coreagent.IDWorker))
	worker.Tools.Builtin.Policies["goal_claim"] = config.ToolPolicyDeny
	coreagent.SeedConfig(cfg)
	assert.Equal(t, config.ToolPolicyDeny, findSeeded(t, cfg, string(coreagent.IDWorker)).Tools.Builtin.Policies["goal_claim"])
}
