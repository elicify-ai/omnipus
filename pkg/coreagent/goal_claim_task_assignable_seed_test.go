// Omnipus — goal_claim default for seeded agents, and the one-time Worker update.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

// A native task run completes only when its goal_claim is upheld by the Judge
// (ADR-043 §8, ADR-084 §11, issue #710), and update_task refuses a status
// write on the caller's own running task. An agent that resolves goal_claim to
// anything other than allow can therefore never finish a task assigned to it.
// Founder decision 2026-09-15: goal_claim is allowed by default for every
// agent. The Judge and PlanSupervisor are the documented exception: their
// whole tool policy is a role invariant re-applied every boot, and neither
// can own a goal or be assigned a task (validateCoreTeamMembers rejects a
// System Agent from a workspace team).
//
// Every agent-creation path is enumerated end to end in
// pkg/gateway/goal_claim_default_allow_test.go; this file covers the seed
// itself and the one-time update for installs seeded before the change.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSeed_GoalClaim_EveryNonSystemSeededAgentResolvesAllow_FreshInstall
// resolves goal_claim through the real compositor merge
// (tools.ResolveEffectivePolicy, strictest-wins over the shipped global
// ceiling and each agent's own seeded map).
func TestSeed_GoalClaim_EveryNonSystemSeededAgentResolvesAllow_FreshInstall(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	// Named explicitly, independent of the seed, so an agent the seed stops
	// producing fails here instead of silently shrinking the loop.
	wantNonSystem := []string{
		string(coreagent.IDMia), string(coreagent.IDJim), string(coreagent.IDAva), string(coreagent.IDRay),
		string(coreagent.IDWorker), string(coreagent.IDPlanner), string(coreagent.IDExplorer),
		string(coreagent.IDResearcher),
	}
	var gotNonSystem []string
	for i := range cfg.Agents.List {
		if !cfg.Agents.List[i].IsSystem() {
			gotNonSystem = append(gotNonSystem, cfg.Agents.List[i].ID)
		}
	}
	require.ElementsMatch(t, wantNonSystem, gotNonSystem,
		"the seeded non-System roster changed; extend this test for the new agent")

	for _, id := range wantNonSystem {
		assert.Equalf(t, "allow", resolveFor(t, cfg, id, tools.GoalClaimToolName, nil),
			"seeded agent %q must resolve goal_claim to allow by default, or a task assigned to it can never finish", id)
	}

	// The documented exception: System Agents keep their exact seeded set.
	for _, id := range []string{string(coreagent.IDJudge), string(coreagent.IDPlanSupervisor)} {
		assert.Equalf(t, "deny", resolveFor(t, cfg, id, tools.GoalClaimToolName, nil),
			"System Agent %q keeps its exact seeded tool set (goal_claim deny)", id)
	}
}

// TestSeed_WorkerGoalClaimIsAnExplicitAllow pins the Worker's entry as
// explicit data. The Worker's map is sparse (tightenGlobalCeiling), so an
// absent key would also resolve allow from today's ceiling; the seed names it
// so the Worker's default is readable in its own stored map and matches what
// the one-time update writes on an upgraded install.
func TestSeed_WorkerGoalClaimIsAnExplicitAllow(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	worker := findSeeded(t, cfg, string(coreagent.IDWorker))
	require.NotNil(t, worker.Tools)

	p, present := worker.Tools.Builtin.Policies[tools.GoalClaimToolName]
	require.True(t, present, "the Worker's seed must name goal_claim explicitly")
	assert.Equal(t, config.ToolPolicyAllow, p)

	// set_goal stays refused on task sessions (ADR-084 §11) and the Worker is
	// never a chat target, so its explicit deny is unchanged.
	sg, sgPresent := worker.Tools.Builtin.Policies["set_goal"]
	require.True(t, sgPresent, "the Worker's seed must still name set_goal explicitly")
	assert.Equal(t, config.ToolPolicyDeny, sg)
}

// upgradedInstallWithSeededWorkerDeny returns a config shaped like an install
// seeded by build f4e482561 or earlier on this branch: the full seeded roster,
// the Worker storing the old seeded goal_claim deny, and no update marker.
func upgradedInstallWithSeededWorkerDeny(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	cfg.SeededToolPolicyUpdates = nil
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(coreagent.IDWorker) {
			cfg.Agents.List[i].Tools.Builtin.Policies[tools.GoalClaimToolName] = config.ToolPolicyDeny
		}
	}
	return cfg
}

func storedGoalClaim(t *testing.T, cfg *config.Config, agentID string) (config.ToolPolicy, bool) {
	t.Helper()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != agentID {
			continue
		}
		if cfg.Agents.List[i].Tools == nil {
			return "", false
		}
		p, ok := cfg.Agents.List[i].Tools.Builtin.Policies[tools.GoalClaimToolName]
		return p, ok
	}
	require.FailNowf(t, "agent missing", "agent %q not in config", agentID)
	return "", false
}

func hasMarker(cfg *config.Config) int {
	n := 0
	for _, m := range cfg.SeededToolPolicyUpdates {
		if m == coreagent.ToolPolicyUpdateWorkerGoalClaimAllow {
			n++
		}
	}
	return n
}

// TestWorkerGoalClaimUpdate_UpgradedInstall_FlipsSeededDenyOnce: an install
// that stored the Worker's old seeded goal_claim deny reaches the fresh-install
// default on its next boot, and the marker is recorded.
func TestWorkerGoalClaimUpdate_UpgradedInstall_FlipsSeededDenyOnce(t *testing.T) {
	cfg := upgradedInstallWithSeededWorkerDeny(t)

	require.True(t, coreagent.SeedConfig(cfg), "recording the update marker is a modification")

	p, ok := storedGoalClaim(t, cfg, string(coreagent.IDWorker))
	require.True(t, ok)
	assert.Equal(t, config.ToolPolicyAllow, p, "the Worker's stored seeded deny must become allow")
	assert.Equal(t, "allow", resolveFor(t, cfg, string(coreagent.IDWorker), tools.GoalClaimToolName, nil))
	assert.Equal(t, 1, hasMarker(cfg), "the update marker must be recorded exactly once")

	// A second boot changes nothing.
	assert.False(t, coreagent.SeedConfig(cfg), "a second boot must be a no-op")
	assert.Equal(t, 1, hasMarker(cfg))
}

// TestWorkerGoalClaimUpdate_OperatorDenyAfterUpdate_IsKept: once the marker is
// recorded, a deny the operator sets on the Worker survives every later boot.
func TestWorkerGoalClaimUpdate_OperatorDenyAfterUpdate_IsKept(t *testing.T) {
	cfg := upgradedInstallWithSeededWorkerDeny(t)
	require.True(t, coreagent.SeedConfig(cfg))

	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(coreagent.IDWorker) {
			cfg.Agents.List[i].Tools.Builtin.Policies[tools.GoalClaimToolName] = config.ToolPolicyDeny
		}
	}
	coreagent.SeedConfig(cfg)
	coreagent.SeedConfig(cfg)

	p, _ := storedGoalClaim(t, cfg, string(coreagent.IDWorker))
	assert.Equal(t, config.ToolPolicyDeny, p, "an operator deny set after the update must never be flipped")
}

// TestWorkerGoalClaimUpdate_LeavesEveryOtherAgentAndValueAlone: the update
// touches one value on one agent. Another agent's explicit deny (operator
// data), the System Agents' seeded deny, the Worker's other entries, and a
// Worker value that is not exactly "deny" are all left as they are.
func TestWorkerGoalClaimUpdate_LeavesEveryOtherAgentAndValueAlone(t *testing.T) {
	cfg := upgradedInstallWithSeededWorkerDeny(t)
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:    "ops-bot",
		Name:  "Ops Bot",
		Type:  config.AgentTypeCustom,
		Tools: coreagent.NewCustomAgentToolsCfg(),
	})
	for i := range cfg.Agents.List {
		switch cfg.Agents.List[i].ID {
		case "ops-bot", string(coreagent.IDMia):
			cfg.Agents.List[i].Tools.Builtin.Policies[tools.GoalClaimToolName] = config.ToolPolicyDeny
		}
	}
	workerBefore := make(map[string]config.ToolPolicy)
	for k, v := range findSeeded(t, cfg, string(coreagent.IDWorker)).Tools.Builtin.Policies {
		workerBefore[k] = v
	}

	coreagent.SeedConfig(cfg)

	for _, id := range []string{"ops-bot", string(coreagent.IDMia), string(coreagent.IDJudge), string(coreagent.IDPlanSupervisor)} {
		p, _ := storedGoalClaim(t, cfg, id)
		assert.Equalf(t, config.ToolPolicyDeny, p, "agent %q's goal_claim deny must be left alone", id)
	}
	workerAfter := findSeeded(t, cfg, string(coreagent.IDWorker)).Tools.Builtin.Policies
	for k, v := range workerBefore {
		if k == tools.GoalClaimToolName {
			continue
		}
		assert.Equalf(t, v, workerAfter[k], "the update must not touch the Worker's %q entry", k)
	}
	assert.Len(t, workerAfter, len(workerBefore), "the update must not add or remove Worker entries")

	for _, tc := range []struct {
		name   string
		mutate func(map[string]config.ToolPolicy)
		want   config.ToolPolicy
		wantOK bool
	}{
		{"ask stays ask", func(p map[string]config.ToolPolicy) { p[tools.GoalClaimToolName] = config.ToolPolicyAsk }, config.ToolPolicyAsk, true},
		{"absent stays absent", func(p map[string]config.ToolPolicy) { delete(p, tools.GoalClaimToolName) }, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := upgradedInstallWithSeededWorkerDeny(t)
			for i := range c.Agents.List {
				if c.Agents.List[i].ID == string(coreagent.IDWorker) {
					tc.mutate(c.Agents.List[i].Tools.Builtin.Policies)
				}
			}
			coreagent.SeedConfig(c)
			got, ok := storedGoalClaim(t, c, string(coreagent.IDWorker))
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, 1, hasMarker(c), "the marker is recorded even when nothing flipped")
		})
	}
}

// TestWorkerGoalClaimUpdate_MarkerAlreadyPresent_NoFlip: an install that already
// recorded the marker is never updated again, whatever the Worker stores.
func TestWorkerGoalClaimUpdate_MarkerAlreadyPresent_NoFlip(t *testing.T) {
	cfg := upgradedInstallWithSeededWorkerDeny(t)
	cfg.SeededToolPolicyUpdates = []string{coreagent.ToolPolicyUpdateWorkerGoalClaimAllow}

	coreagent.SeedConfig(cfg)

	p, _ := storedGoalClaim(t, cfg, string(coreagent.IDWorker))
	assert.Equal(t, config.ToolPolicyDeny, p)
	assert.Equal(t, 1, hasMarker(cfg))
}

// TestWorkerGoalClaimUpdate_FreshInstall_RecordsMarkerOnly: a fresh install's
// Worker is already allow; the first boot records the marker and nothing else.
func TestWorkerGoalClaimUpdate_FreshInstall_RecordsMarkerOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	p, _ := storedGoalClaim(t, cfg, string(coreagent.IDWorker))
	assert.Equal(t, config.ToolPolicyAllow, p)
	assert.Equal(t, 1, hasMarker(cfg))
}
