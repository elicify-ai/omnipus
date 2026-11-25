// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools C-19 / FR-080: GET /skills is a reconcile entry. It runs
// the C-2 skill-map self-heal, the same heal the gateway start runs after
// the one-time upgrade: project-skill keys seen for the first time are
// stored, and the keys of a deleted workspace or a removed project folder
// leave sandbox.skill_policies, sandbox.skill_visibility and every agent's
// skills.policies. The live config takes the healed global maps.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skillperm"
	"github.com/elicify-ai/omnipus/pkg/skills"
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
		policies, visibility, _, err := skillperm.SelfHeal(a.homePath, configPath, skills.DefaultSkillNames())
		if err != nil {
			return err
		}
		if live != nil {
			live.Sandbox.SkillPolicies = config.ApplyHealedSkillKeys(live.Sandbox.SkillPolicies, policies)
			live.Sandbox.SkillVisibility = config.ApplyHealedSkillKeys(live.Sandbox.SkillVisibility, visibility)
		}
		return nil
	}
	if a.agentLoop == nil {
		return heal(nil)
	}
	return a.agentLoop.MutateConfig(heal)
}
