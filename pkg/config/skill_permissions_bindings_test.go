//go:build goolm && stdjson

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// GREEN bindings for the Skills & Tools RED pack (#1963): thin, input-only
// adapters from the pack's seams to the production functions. No adapter
// reads an expected value; each passes only the scenario's inputs.

import "strings"

func init() {
	migrateLegacySkillTrustOf = MigrateLegacySkillTrust

	reconcileSkillMapsOf = func(storedPolicies, storedVisibility map[string]string, installed []installedSkillInfo) (map[string]string, map[string]string) {
		infos := make([]SkillKeyInfo, 0, len(installed))
		for _, s := range installed {
			infos = append(infos, SkillKeyInfo{Key: s.key, BuiltinNewRelease: s.builtinNewRelease})
		}
		return ReconcileSkillPolicyMaps(storedPolicies, storedVisibility, infos)
	}

	skillPermissionsUpgradePlanOf = func(in upgradePlanInput) (map[string]string, map[string]string) {
		global := PlanSkillGlobalPolicies(in.oldGlobalSkillValue, in.installedSkills)
		agent := PlanAgentSkillOverrides(SkillUpgradeAgent{
			ID: "mia", OldSkill: in.oldAgentSkillValue, Granted: in.grantedSkills,
		}, in.installedSkills)
		return global, agent
	}

	visibilityConversionPlanOf = func(old conversionOldState) map[string]string {
		in := VisibilityConversionInput{
			Compressed:       old.compressed,
			PreviewAllLazy:   old.previewAllLazy,
			DiscoveryEnabled: old.discoverySet && old.discoveryEnabled,
			Skills:           old.skills,
		}
		if old.staticTool != "" {
			in.StaticTools = []string{old.staticTool}
		}
		for _, s := range old.servers {
			// An offline server is converted from its stored config alone.
			in.Servers = append(in.Servers, VisibilityConversionServer{ID: s.id, Deferred: s.deferred})
		}
		return PlanToolVisibilityConversion(in)
	}

	mcpMigrationPlanOf = func(global map[string]string, agentEntry string, godMode bool, phase, service, tool string) (map[string]string, string) {
		_ = phase // first start, repeat start and a new service use the one rule (C-18, C-7)
		writes := PlanMCPServiceCoverage(global, []string{service})
		after := make(map[string]string, len(global)+len(writes))
		for k, v := range global {
			after[k] = v
		}
		for k, v := range writes {
			after[k] = v
		}
		g := ResolvePolicyFromMap(tool, after)
		if godMode {
			g = ToolPolicyAllow
		}
		agent := map[string]string{}
		if agentEntry != "" {
			agent[tool] = agentEntry
		}
		return writes, string(MergeStoredPolicyLayers(g, ResolvePolicyFromMap(tool, agent)))
	}

	projectSkillWinnerOf = func(installedPolicy, projectPolicy string, grantedInstalled bool) (string, string) {
		const slug, workspace = "notes", "w1"
		stored := map[string]string{
			NormalizeSkillKey(slug):          installedPolicy,
			ProjectSkillKey(workspace, slug): projectPolicy,
		}
		key := SkillLoadKey(slug, grantedInstalled, workspace)
		if ToolPolicy(stored[key]) == ToolPolicyDeny {
			w, _, _ := ParseProjectSkillKey(key)
			return "", "refused: " + key + " is Deny in " + strings.ToUpper(w)
		}
		if _, _, isProject := ParseProjectSkillKey(key); isProject {
			return "project:" + slug + " instructions", ""
		}
		return "installed:" + slug + " instructions", ""
	}
}
