// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools C-19 / FR-080: GET /skills is a reconcile entry. Once the
// one-time C-19 upgrade is done it runs the C-2 skill-map self-heal, the
// heal of the config load and the turn start (skillperm.HealIfUpgraded):
// project-skill keys seen for the first time are stored, and the keys of a
// deleted workspace or a removed project folder leave
// sandbox.skill_policies, sandbox.skill_visibility and every agent record's
// skills.policies. Before the upgrade is done it changes nothing: the
// upgrade gives the keys their planned values first (spec line 154).
//
// The live config takes the healed global maps, and the gone keys leave the
// live agent copies in the same step, so GET /agents/{id}/tools (which reads
// the live config) never shows a removed key.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skillperm"
)

// reconcileSkillKeys runs the FR-080 reconcile for GET /skills. A home with
// no config.json stores no skill maps, so there is nothing to reconcile.
func (a *restAPI) reconcileSkillKeys() error {
	configPath := a.configPath()
	if _, err := os.Stat(configPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	heal := func(live *config.Config) error {
		policies, visibility, _, err := skillperm.HealIfUpgraded(configPath)
		if err != nil {
			return err
		}
		if policies == nil && visibility == nil {
			return nil // the C-19 upgrade is not done: it runs first
		}
		if live == nil {
			return nil
		}
		live.Sandbox.SkillPolicies = config.ApplyHealedSkillKeys(live.Sandbox.SkillPolicies, policies)
		live.Sandbox.SkillVisibility = config.ApplyHealedSkillKeys(live.Sandbox.SkillVisibility, visibility)
		return a.dropGoneKeysFromLiveAgents(live)
	}
	if a.agentLoop == nil {
		return heal(nil)
	}
	return a.agentLoop.MutateConfig(heal)
}

// dropGoneKeysFromLiveAgents removes the project keys whose workspace or
// folder is gone from every live agent's skills.policies, as the heal does
// in the agent records on disk.
func (a *restAPI) dropGoneKeysFromLiveAgents(live *config.Config) error {
	var keys []string
	for i := range live.Agents.List {
		if p := liveAgentSkillPolicies(&live.Agents.List[i]); p != nil {
			for k := range p {
				if _, _, project := config.ParseProjectSkillKey(k); project {
					keys = append(keys, k)
				}
			}
		}
	}
	if len(keys) == 0 {
		return nil
	}
	gone, err := skillperm.GoneProjectKeys(a.homePath, keys)
	if err != nil {
		return err
	}
	for i := range live.Agents.List {
		p := liveAgentSkillPolicies(&live.Agents.List[i])
		for k := range gone {
			delete(p, k)
		}
	}
	return nil
}

// liveAgentSkillPolicies is the agent's own skill permission map, or nil.
func liveAgentSkillPolicies(ac *config.AgentConfig) map[string]config.ToolPolicy {
	if ac.Tools == nil || ac.Tools.Skills == nil {
		return nil
	}
	return ac.Tools.Skills.Policies
}
