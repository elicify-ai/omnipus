// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Skills & Tools C-23 scope and the C-9 per-agent skill permission rows,
// shared by the agent tools and the gateway so every writer and reader uses
// one rule.

import (
	"path/filepath"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// projectShelfSlugs returns the project skill slugs of workspace W, false
// when W has no record.
func projectShelfSlugs(home string) func(string) ([]string, bool) {
	return func(w string) ([]string, bool) {
		mounts, ok := workspace.LoadMounts(home, w)
		if !ok {
			return nil, false
		}
		var out []string
		for _, m := range mounts {
			found, _ := skills.DiscoverProjectSkills(m.Name, m.HostPath)
			for _, ps := range found {
				out = append(out, ps.ID)
			}
		}
		return out, true
	}
}

// GlobalSkillKeyScope is the C-23 scope of the global writer.
func GlobalSkillKeyScope(home string, stored map[string]string) skills.SkillKeyScope {
	keys := make(map[string]bool, len(stored))
	for k := range stored {
		keys[k] = true
	}
	return skills.SkillKeyScope{
		SkillsDir: filepath.Join(home, "skills"), ProjectSlugs: projectShelfSlugs(home), StoredGlobalKeys: keys,
	}
}

// AgentSkillKeyScope is the C-23 scope of a per-agent writer: the global
// rule plus mounted-on-W for project keys.
func AgentSkillKeyScope(home, agentID string, stored map[string]string) skills.SkillKeyScope {
	scope := GlobalSkillKeyScope(home, stored)
	scope.AgentWorkspaces = map[string]bool{}
	ids, _ := workspace.FindAllForAgent(home, agentID)
	for _, id := range ids {
		scope.AgentWorkspaces[id] = true
	}
	return scope
}

// AgentSkillPermissionRow is one C-9 row: skill key, effective permission
// (strictest wins, God Mode floor, trust floor) and the agent override.
type AgentSkillPermissionRow struct {
	Skill     string  `json:"skill"`
	Effective string  `json:"effective"`
	Override  *string `json:"override"`
}

// AgentSkillPermissions lists the granted installed skills and the project
// skills of the agent's mounts with their permissions (C-23 reads). An
// unreadable skill is reported as deny (fail closed).
func AgentSkillPermissions(home string, settings config.PersistedSkillSettings, agentID string, granted []string, agentSkills *config.AgentSkillsCfg) []AgentSkillPermissionRow {
	rows := []AgentSkillPermissionRow{}
	seen := map[string]bool{}
	add := func(key string, d SkillDecision) {
		if seen[key] {
			return
		}
		seen[key] = true
		row := AgentSkillPermissionRow{Skill: key, Effective: effectiveOfDecision(d)}
		if agentSkills != nil {
			if v, ok := agentSkills.Policies[key]; ok {
				s := string(v)
				row.Override = &s
			}
		}
		rows = append(rows, row)
	}
	for _, slug := range granted {
		key := config.NormalizeSkillKey(slug)
		d, err := InstalledSkillDecision(home, settings, agentSkills, slug, true)
		if err != nil {
			if !skills.IsBuiltin(slug) {
				continue // not installed: no row
			}
			d = SkillDecision{Outcome: SkillOutcomeDeny, Reason: SkillDenyPermission}
		}
		add(key, d)
	}
	ids, _ := workspace.FindAllForAgent(home, agentID)
	for _, w := range ids {
		mounts, _ := workspace.LoadMounts(home, w)
		for _, m := range mounts {
			found, _ := skills.DiscoverProjectSkills(m.Name, m.HostPath)
			for _, ps := range found {
				key := config.ProjectSkillKey(w, ps.ID)
				d, err := projectSkillDecision(home, settings, agentSkills, key, filepath.Dir(ps.Path))
				if err != nil {
					d = SkillDecision{Outcome: SkillOutcomeDeny, Reason: SkillDenyPermission}
				}
				add(key, d)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Skill < rows[j].Skill })
	return rows
}

// effectiveOfDecision maps a C-11 outcome to the stored vocabulary.
func effectiveOfDecision(d SkillDecision) string {
	switch d.Outcome {
	case SkillOutcomeAllow:
		return string(config.ToolPolicyAllow)
	case SkillOutcomeAskPending:
		return string(config.ToolPolicyAsk)
	default:
		return string(config.ToolPolicyDeny)
	}
}
