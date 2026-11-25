// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skillperm

// Skills & Tools C-19 "When the new rules apply": from the moment the
// snapshot is durable, policy is resolved from the plan of step 2, not from
// the files being rewritten. An agent still pending or in_flight gets exactly
// its planned overrides; a failed agent write keeps the plan applying to it.
// Every read of an agent record through pkg/agentstore applies the plan to
// such an agent: its planned skill overrides are added (a value stored later
// is kept, as the record write does) and its old Skill tool entry is dropped.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func init() {
	agentstore.RegisterReadOverlay(ApplyPlanToWaitingAgent)
}

// ApplyPlanToWaitingAgent applies the C-19 plan to agent when the upgrade of
// home has not written its record yet. A finished upgrade, or no upgrade
// under way, leaves the record as it is. An unreadable ledger or snapshot is
// an error: the agent's values cannot be decided.
func ApplyPlanToWaitingAgent(home string, agent *config.AgentConfig) error {
	if agent == nil {
		return nil
	}
	entities := filepath.Join(home, "entities")
	snap, ok, err := readSnapshotIfPresent(entities)
	if err != nil || !ok {
		return err
	}
	led, haveLedger, err := readLedger(entities)
	if err != nil {
		return err
	}
	waiting := agentIDs(snap) // no ledger yet: every agent of the snapshot waits
	if haveLedger {
		if led.State == stateDone {
			return nil
		}
		waiting = led.Pending
	}
	if !slices.Contains(waiting, agent.ID) {
		return nil
	}
	for _, a := range snap.Agents {
		if a.ID == agent.ID {
			applyAgentPlanInMemory(agent, config.PlanAgentSkillOverrides(a, snap.InstalledSkills))
			return nil
		}
	}
	return nil
}

// applyAgentPlanInMemory is writeAgentPlan's change on a loaded record.
func applyAgentPlanInMemory(agent *config.AgentConfig, plan map[string]string) {
	if agent.Tools == nil {
		if len(plan) == 0 {
			return
		}
		agent.Tools = &config.AgentToolsCfg{}
	}
	if len(plan) > 0 {
		if agent.Tools.Skills == nil {
			agent.Tools.Skills = &config.AgentSkillsCfg{}
		}
		if agent.Tools.Skills.Policies == nil {
			agent.Tools.Skills.Policies = map[string]config.ToolPolicy{}
		}
		for k, v := range plan {
			if _, stored := agent.Tools.Skills.Policies[k]; !stored {
				agent.Tools.Skills.Policies[k] = config.ToolPolicy(v)
			}
		}
	}
	delete(agent.Tools.Builtin.Policies, "Skill")
}

// readSnapshotIfPresent reads the durable snapshot; ok is false when there is
// none (no upgrade under way, or it is retired after done).
func readSnapshotIfPresent(entities string) (Snapshot, bool, error) {
	var snap Snapshot
	raw, err := os.ReadFile(filepath.Join(entities, snapshotName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snap, false, nil
		}
		return snap, false, fmt.Errorf("skillperm: read snapshot: %w", err)
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		return snap, false, fmt.Errorf("skillperm: parse snapshot: %w", err)
	}
	return snap, true, nil
}
