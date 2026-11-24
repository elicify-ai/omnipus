// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skillperm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// loadOrTakeSnapshot returns the durable snapshot, writing it once from the
// live files when none exists. Once written it is never rewritten.
func loadOrTakeSnapshot(home, entities string) (Snapshot, error) {
	var snap Snapshot
	path := filepath.Join(entities, snapshotName)
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(raw, &snap); err != nil {
			return snap, fmt.Errorf("skillperm: parse snapshot: %w", err)
		}
		return snap, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return snap, fmt.Errorf("skillperm: read snapshot: %w", err)
	}
	snap, err = takeSnapshot(home)
	if err != nil {
		return snap, err
	}
	return snap, writeJSON(path, snap)
}

func takeSnapshot(home string) (Snapshot, error) {
	cfg, err := readRawConfig(home)
	if err != nil {
		return Snapshot{}, err
	}
	sandbox := childMap(cfg, "sandbox")
	policies := childMap(sandbox, "tool_policies")
	tools := childMap(cfg, "tools")
	manifest := childMap(tools, "manifest")
	mcp := childMap(tools, "mcp")
	snap := Snapshot{
		OldGlobalSkill:   stringAt(policies, "Skill"),
		OldTrust:         stringAt(sandbox, "skill_trust"),
		Compressed:       boolAt(manifest, "compressed", config.DefaultConfig().Tools.Manifest.Compressed),
		PreviewAllLazy:   boolAt(manifest, "preview_all_lazy", false),
		DiscoveryEnabled: boolAt(childMap(mcp, "discovery"), "enabled", false),
		GlobalMCP:        map[string]string{},
		GodMode:          boolAt(sandbox, "god_mode", false),
		InstalledSkills:  installedSkills(home),
		ProjectSkills:    []string{},
		StaticTools:      sortedKeys(config.DefaultToolPolicyCeiling()),
	}
	for k, v := range policies {
		if s, ok := v.(string); ok && strings.HasPrefix(k, "mcp_") {
			snap.GlobalMCP[k] = s
		}
	}
	for _, id := range sortedKeys(childMap(mcp, "servers")) {
		server := childMap(childMap(mcp, "servers"), id)
		var deferred *bool
		if d, ok := server["deferred"].(bool); ok {
			deferred = &d
		}
		snap.Servers = append(snap.Servers, config.VisibilityConversionServer{ID: id, Deferred: deferred})
	}
	byWorkspace := projectSkillsByWorkspace(home)
	for _, w := range sortedKeys(byWorkspace) {
		snap.ProjectSkills = append(snap.ProjectSkills, byWorkspace[w]...)
	}
	snap.Agents, snap.SkippedAgents = snapshotAgents(home, byWorkspace)
	return snap, nil
}

func snapshotAgents(home string, byWorkspace map[string][]string) ([]config.SkillUpgradeAgent, []string) {
	dir := filepath.Join(home, "entities", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []config.SkillUpgradeAgent{}, nil
	}
	agents := []config.SkillUpgradeAgent{}
	var skipped []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		rec, err := readJSONMap(filepath.Join(dir, e.Name()))
		if err != nil {
			logger.WarnCF(logComponent, "skill permissions upgrade: agent record unreadable; skipped, its old skill limits still apply",
				map[string]any{"agent": id, "error": err.Error()})
			skipped = append(skipped, id)
			continue
		}
		a := config.SkillUpgradeAgent{
			ID:            id,
			OldSkill:      stringAt(childMap(childMap(childMap(rec, "tools"), "builtin"), "policies"), "Skill"),
			Granted:       stringList(rec["skills"]),
			ProjectSkills: []string{},
		}
		ws, _ := workspace.FindAllForAgent(home, id)
		for _, w := range ws {
			a.ProjectSkills = append(a.ProjectSkills, byWorkspace[w]...)
		}
		agents = append(agents, a)
	}
	return agents, skipped
}

func installedSkills(home string) []string {
	out := []string{}
	entries, err := os.ReadDir(filepath.Join(home, "skills"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(home, "skills", e.Name(), "SKILL.md")); err == nil {
			out = append(out, config.NormalizeSkillKey(e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// projectSkillsByWorkspace lists the project-skill keys found on every
// workspace's mounts.
func projectSkillsByWorkspace(home string) map[string][]string {
	out := map[string][]string{}
	ids, err := workspace.ListWorkspaceIDs(home)
	if err != nil {
		return out
	}
	for _, w := range ids {
		mounts, _ := workspace.LoadMounts(home, w)
		seen := map[string]struct{}{}
		for _, m := range mounts {
			found, _ := skills.DiscoverProjectSkills(m.Name, m.HostPath)
			for _, ps := range found {
				seen[config.ProjectSkillKey(w, ps.Name)] = struct{}{}
			}
		}
		if len(seen) > 0 {
			out[w] = sortedKeys(seen)
		}
	}
	return out
}

// applyConfigPlan is step 3: one atomic config.json write of every planned
// value. A key whose stored value differs from both its old value and its
// planned value was changed by a person in between and is left as it is.
func applyConfigPlan(home string, snap Snapshot) error {
	cfg, err := readRawConfig(home)
	if err != nil {
		return err
	}
	sandbox := ensureMap(cfg, "sandbox")
	changed := false
	newTrust, _ := config.MigrateLegacySkillTrust(snap.OldTrust)
	changed = replaySet(sandbox, "skill_trust", snap.OldTrust, newTrust) || changed
	skillPolicies := ensureMap(sandbox, "skill_policies")
	allSkills := append(append([]string{}, snap.InstalledSkills...), snap.ProjectSkills...)
	for k, v := range config.PlanSkillGlobalPolicies(snap.OldGlobalSkill, allSkills) {
		changed = replaySet(skillPolicies, k, "", v) || changed
	}
	visibility := ensureMap(sandbox, "tool_visibility")
	skillVisibility := ensureMap(sandbox, "skill_visibility")
	levels := config.PlanToolVisibilityConversion(config.VisibilityConversionInput{
		Compressed: snap.Compressed, PreviewAllLazy: snap.PreviewAllLazy,
		DiscoveryEnabled: snap.DiscoveryEnabled, StaticTools: snap.StaticTools,
		Servers: snap.Servers, Skills: allSkills,
	})
	skillSet := map[string]struct{}{}
	for _, k := range allSkills {
		skillSet[config.NormalizeSkillKey(k)] = struct{}{}
	}
	for k, v := range levels {
		target := visibility
		if _, isSkill := skillSet[k]; isSkill {
			target = skillVisibility
		}
		changed = replaySet(target, k, "", v) || changed
	}
	toolPolicies := ensureMap(sandbox, "tool_policies")
	var servers []string
	for _, s := range snap.Servers {
		servers = append(servers, s.ID)
	}
	for k, v := range config.PlanMCPServiceCoverage(snap.GlobalMCP, servers) {
		changed = replaySet(toolPolicies, k, "", v) || changed
	}
	changed = replaySet(toolPolicies, "Skill", snap.OldGlobalSkill, string(config.ToolPolicyAllow)) || changed
	if !changed {
		return nil
	}
	return writeRawConfig(home, cfg)
}

// writeAgentPlan is step 4 for one agent.
func writeAgentPlan(home string, a config.SkillUpgradeAgent, snap Snapshot, opts Options) error {
	if opts.BeforeAgentWrite != nil {
		if err := opts.BeforeAgentWrite(a.ID); err != nil {
			return err
		}
	}
	plan := config.PlanAgentSkillOverrides(a, snap.InstalledSkills)
	path := filepath.Join(home, "entities", "agents", a.ID+".json")
	rec, err := readJSONMap(path)
	if err != nil {
		return err
	}
	tools := ensureMap(rec, "tools")
	changed := false
	if len(plan) > 0 {
		policies := ensureMap(ensureMap(tools, "skills"), "policies")
		for _, k := range sortedKeys(plan) {
			cur, present := policies[k]
			switch {
			case !present:
				policies[k] = plan[k]
				changed = true
			case cur != plan[k]:
				logger.WarnCF(logComponent, "skill permissions upgrade: kept a later per-agent skill value",
					map[string]any{"agent": a.ID, "skill": k, "stored": cur, "planned": plan[k]})
			}
		}
	}
	builtin := childMap(childMap(tools, "builtin"), "policies")
	if _, ok := builtin["Skill"]; ok {
		delete(builtin, "Skill")
		changed = true
	}
	if !changed {
		return nil
	}
	return writeJSONMap(path, rec)
}

func writeMarker(home string) error {
	cfg, err := readRawConfig(home)
	if err != nil {
		return err
	}
	migrations := ensureMap(ensureMap(cfg, "sandbox"), "migrations")
	if migrations[MigrationName] == stateDone {
		return nil
	}
	migrations[MigrationName] = stateDone
	return writeRawConfig(home, cfg)
}

// replaySet writes planned at key when the stored value is absent or still
// the old value; it reports whether it wrote.
func replaySet(m map[string]any, key, old, planned string) bool {
	cur, present := m[key]
	if present && cur == planned {
		return false
	}
	if present && cur != old {
		return false
	}
	m[key] = planned
	return true
}

func readRawConfig(home string) (map[string]any, error) {
	cfg, err := readJSONMap(filepath.Join(home, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	return cfg, err
}

func writeRawConfig(home string, cfg map[string]any) error {
	return writeJSONMap(filepath.Join(home, "config.json"), cfg)
}

func readJSONMap(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("skillperm: parse %s: %w", filepath.Base(path), err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func writeJSONMap(path string, m map[string]any) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("skillperm: encode %s: %w", filepath.Base(path), err)
	}
	if err := fileutil.WriteFileAtomic(path, raw, 0o600); err != nil {
		return fmt.Errorf("skillperm: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

func childMap(m map[string]any, key string) map[string]any {
	if c, ok := m[key].(map[string]any); ok {
		return c
	}
	return map[string]any{}
}

func ensureMap(m map[string]any, key string) map[string]any {
	if c, ok := m[key].(map[string]any); ok {
		return c
	}
	c := map[string]any{}
	m[key] = c
	return c
}

func stringAt(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func boolAt(m map[string]any, key string, def bool) bool {
	if b, ok := m[key].(bool); ok {
		return b
	}
	return def
}

func stringList(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
