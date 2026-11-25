// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-19 (key table): a project skill's key
// project:<W>/<slug> is created when Omnipus first reads W's project shelf,
// at turn start or reconcile. The turn start reads the shelf of its
// workspace; a slug with no stored key runs the C-2 heal, the same write as
// the config load, which stores it at Ask with the level
// name_and_description. When the heal fails the key stays missing and the
// skill's decision keeps failing closed.

import (
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skillperm"
)

// storeNewProjectSkillKeys stores the keys of the turn workspace's project
// skills that have none yet.
func (al *AgentLoop) storeNewProjectSkillKeys(ts *turnState) {
	if ts == nil || ts.agent == nil || ts.agent.ContextBuilder == nil || ts.opts.WorkspaceID == "" {
		return
	}
	shelf := ts.agent.ContextBuilder.ProjectShelfForWorkspace(ts.opts.WorkspaceID)
	if len(shelf) == 0 {
		return
	}
	cfg := al.GetConfig()
	if cfg == nil || cfg.Sandbox.SkillPolicies == nil {
		return // not upgraded yet: the C-19 upgrade gives the keys
	}
	missing := []string{}
	for slug := range shelf {
		key := config.ProjectSkillKey(ts.opts.WorkspaceID, slug)
		if _, ok := cfg.Sandbox.SkillPolicies[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return
	}
	err := al.MutateConfig(func(live *config.Config) error {
		policies, visibility, _, err := skillperm.HealIfUpgraded(configFilePath())
		if err != nil {
			return err
		}
		live.Sandbox.SkillPolicies = config.ApplyHealedSkillKeys(live.Sandbox.SkillPolicies, policies)
		live.Sandbox.SkillVisibility = config.ApplyHealedSkillKeys(live.Sandbox.SkillVisibility, visibility)
		return nil
	})
	if err != nil {
		logger.ErrorCF("agent", "project skill keys: could not be stored; those skills stay refused until a heal stores them",
			map[string]any{"agent_id": ts.agentID, "workspace_id": ts.opts.WorkspaceID, "keys": missing, "error": err.Error()})
	}
}

// configFilePath is the config.json the gateway loads: $OMNIPUS_CONFIG, or
// config.json in the Omnipus home.
func configFilePath() string {
	if p := os.Getenv(config.EnvConfig); p != "" {
		return p
	}
	return filepath.Join(omnipusHome(), "config.json")
}
