// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// Skills & Tools redesign (#1963), the config half: the skill-trust
// vocabulary (C-3), the model-visibility levels (C-1, C-6), the skill-map
// self-heal (C-2), and the pure planners of the one-time upgrade (C-18,
// C-19 steps 2-4, Dataset D7). Everything here is a pure function of its
// inputs; the durable, replay-safe upgrade that applies the plans lives in
// pkg/skillperm.

import (
	"sort"
	"strings"
)

// The skill-trust vocabulary after the C-3 upgrade. Trust applies to
// unverified skills only; it is a resolution-time floor, not a stored
// permission layer (C-0).
const (
	SkillTrustAllow SkillTrustLevel = "allow"
	SkillTrustAsk   SkillTrustLevel = "ask"
	SkillTrustDeny  SkillTrustLevel = "deny"
)

// MigrateLegacySkillTrust maps a stored sandbox.skill_trust value onto the
// allow / ask / deny vocabulary (C-3): block_unverified -> deny,
// warn_unverified -> ask, allow_all -> allow, and an empty value -> ask
// (today's empty value meant warn_unverified). rewrote reports whether the
// stored value must be rewritten; it gates the one audit entry. A value
// already in the new vocabulary is returned unchanged with rewrote false.
func MigrateLegacySkillTrust(stored string) (string, bool) {
	switch SkillTrustLevel(stored) {
	case SkillTrustAllow, SkillTrustAsk, SkillTrustDeny:
		return stored, false
	case SkillTrustBlockUnverified:
		return string(SkillTrustDeny), true
	case SkillTrustWarnUnverified, "":
		return string(SkillTrustAsk), true
	case SkillTrustAllowAll:
		return string(SkillTrustAllow), true
	default:
		// An unknown value is refused at decode time (UnmarshalJSON); a raw
		// caller gets the safe middle value and a rewrite.
		return string(SkillTrustAsk), true
	}
}

// EffectiveSkillTrust returns the trust value to enforce for a loaded config:
// the migrated value of whatever is stored.
func (c *OmnipusSandboxConfig) EffectiveSkillTrust() SkillTrustLevel {
	v, _ := MigrateLegacySkillTrust(string(c.SkillTrust))
	return SkillTrustLevel(v)
}

// Model visibility levels (C-1).
const (
	VisibilityHidden             = "hidden"
	VisibilityNameOnly           = "name_only"
	VisibilityNameAndDescription = "name_and_description"
	VisibilityFull               = "full"
)

// IsModelVisibility reports whether v is one of the four levels.
func IsModelVisibility(v string) bool {
	switch v {
	case VisibilityHidden, VisibilityNameOnly, VisibilityNameAndDescription, VisibilityFull:
		return true
	}
	return false
}

// visibilityLockedTools carries R31's list: their level is fixed at full
// (C-6). switch_agent from R31 does not exist in the catalog.
var visibilityLockedTools = map[string]struct{}{
	"ToolSearch": {}, "Skill": {}, "AskUserQuestion": {}, "goal_claim": {}, "set_goal": {},
}

// LockedToolVisibility returns the fixed level of a locked tool.
func LockedToolVisibility(name string) (string, bool) {
	if _, ok := visibilityLockedTools[name]; ok {
		return VisibilityFull, true
	}
	return "", false
}

// permissionFixedTools have no Allow / Ask / Deny of their own (C-6): their
// stored global value is allow and is not editable.
var permissionFixedTools = map[string]struct{}{"ToolSearch": {}, "Skill": {}}

// IsPermissionFixedTool reports whether name is ToolSearch or Skill.
func IsPermissionFixedTool(name string) bool {
	_, ok := permissionFixedTools[name]
	return ok
}

// ReconcileSkillPolicyMaps is the C-2 self-heal for the two skill maps, the
// skills twin of ReconcileToolPolicyCeiling: additive, never overwriting.
// A skill with no stored permission gets ask (ST-Q3 B), or allow when it is
// a built-in skill of a newer release (ST-Q10 A); a skill with no stored
// level gets name_and_description. The inputs are not mutated; the returned
// maps are fresh copies holding the stored entries plus the added ones.
// installed maps each installed skill's C-23 key to whether it is a built-in
// skill shipped by a newer release.
func ReconcileSkillPolicyMaps(
	policies, visibility map[string]string,
	installed map[string]bool,
) (map[string]string, map[string]string) {
	outPolicies := make(map[string]string, len(policies)+len(installed))
	for k, v := range policies {
		outPolicies[k] = v
	}
	outVisibility := make(map[string]string, len(visibility)+len(installed))
	for k, v := range visibility {
		outVisibility[k] = v
	}
	for rawKey, builtinNewRelease := range installed {
		key := NormalizeSkillKey(rawKey)
		if key == "" {
			continue
		}
		if _, ok := outPolicies[key]; !ok {
			if builtinNewRelease {
				outPolicies[key] = string(ToolPolicyAllow)
			} else {
				outPolicies[key] = string(ToolPolicyAsk)
			}
		}
		if _, ok := outVisibility[key]; !ok {
			outVisibility[key] = VisibilityNameAndDescription
		}
	}
	return outPolicies, outVisibility
}

// ProjectSkillKeyPrefix starts every project-shelf skill key (C-19).
const ProjectSkillKeyPrefix = "project:"

// ProjectSkillKey returns project:<workspace>/<slug>.
func ProjectSkillKey(workspaceID, slug string) string {
	return ProjectSkillKeyPrefix + workspaceID + "/" + strings.ToLower(slug)
}

// ParseProjectSkillKey splits project:<W>/<slug>; ok is false for any other
// form (including an installed skill id).
func ParseProjectSkillKey(key string) (workspaceID, slug string, ok bool) {
	if !strings.HasPrefix(key, ProjectSkillKeyPrefix) {
		return "", "", false
	}
	rest := key[len(ProjectSkillKeyPrefix):]
	i := strings.Index(rest, "/")
	if i <= 0 || i == len(rest)-1 || strings.Contains(rest[i+1:], "/") {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// NormalizeSkillKey lower-cases an installed skill id and the slug part of a
// project key (installed ids are case-insensitive, C-23). Workspace ids keep
// their spelling.
func NormalizeSkillKey(key string) string {
	key = strings.TrimSpace(key)
	if w, slug, ok := ParseProjectSkillKey(key); ok {
		return ProjectSkillKey(w, slug)
	}
	return strings.ToLower(key)
}

// SetGlobalSkillPolicy stores one entry of the live global skill map (C-2,
// sandbox.skill_policies) under its normalized key. It changes the loaded
// config only; persisting it is the caller's write.
func SetGlobalSkillPolicy(cfg *Config, key, value string) {
	if cfg == nil {
		return
	}
	if cfg.Sandbox.SkillPolicies == nil {
		cfg.Sandbox.SkillPolicies = map[string]string{}
	}
	cfg.Sandbox.SkillPolicies[NormalizeSkillKey(key)] = value
}

// SkillUpgradeAgent is one agent's snapshot row for the C-19 plan.
type SkillUpgradeAgent struct {
	ID string `json:"id"`
	// OldSkill is the agent's stored per-agent value for the Skill tool,
	// "" when it had none.
	OldSkill string   `json:"old_skill"`
	Granted  []string `json:"granted"`
	// ProjectSkills are the project-skill keys of the agent's mounts at
	// snapshot time.
	ProjectSkills []string `json:"project_skills"`
}

// PlanSkillGlobalPolicies is C-19 step 3's global half: every installed and
// project skill gets the old global Skill value. An absent old value plans
// nothing (the C-2 self-heal then gives ask).
func PlanSkillGlobalPolicies(oldGlobalSkill string, skillKeys []string) map[string]string {
	out := map[string]string{}
	if !isToolPolicyValue(oldGlobalSkill) {
		return out
	}
	for _, k := range skillKeys {
		if k = NormalizeSkillKey(k); k != "" {
			out[k] = oldGlobalSkill
		}
	}
	return out
}

// PlanAgentSkillOverrides is C-19 step 4: the agent's old Skill value, as
// given (C-4 "stored as given, floored at run time"), for each skill granted
// to it and each project skill of its mounts. An agent with no old value gets
// nil: translation, never backfill (Hard Constraint 6).
func PlanAgentSkillOverrides(a SkillUpgradeAgent, installed []string) map[string]string {
	if !isToolPolicyValue(a.OldSkill) {
		return nil
	}
	have := make(map[string]struct{}, len(installed))
	for _, k := range installed {
		have[NormalizeSkillKey(k)] = struct{}{}
	}
	out := map[string]string{}
	for _, g := range a.Granted {
		k := NormalizeSkillKey(g)
		if _, ok := have[k]; ok {
			out[k] = a.OldSkill
		}
	}
	for _, p := range a.ProjectSkills {
		if k := NormalizeSkillKey(p); k != "" {
			out[k] = a.OldSkill
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isToolPolicyValue(v string) bool {
	switch ToolPolicy(v) {
	case ToolPolicyAllow, ToolPolicyAsk, ToolPolicyDeny:
		return true
	}
	return false
}

// MCPServerProbe is the name C-18 resolves to decide whether service s is
// covered by the global map: it matches only wildcard keys, never an exact
// tool key.
func MCPServerProbe(server string) string { return "mcp_" + server + "_\x00probe" }

// MCPServerWildcardKey is the per-server global key mcp_<server>_*.
func MCPServerWildcardKey(server string) string { return "mcp_" + server + "_*" }

// PlanMCPServiceCoverage is C-18: for each service whose probe has no match in
// the global map, the migration (and a newly added service, C-7) writes
// mcp_<S>_* = ask. A covered service writes nothing. Pure and replay-safe: a
// second run over the written map plans nothing.
func PlanMCPServiceCoverage(global map[string]string, services []string) map[string]string {
	writes := map[string]string{}
	for _, s := range services {
		if s == "" {
			continue
		}
		if ResolvePolicyFromMap(MCPServerProbe(s), global) != "" {
			continue
		}
		writes[MCPServerWildcardKey(s)] = string(ToolPolicyAsk)
	}
	return writes
}

// PolicyWildcard is one parsed wildcard key of a policy map.
type PolicyWildcard struct {
	Prefix    string
	Segments  int
	Delimiter byte
	Policy    ToolPolicy
}

// ParsePolicyWildcard parses a trailing ".*" or "_*" key. The bare forms are
// ignored. This is the one definition of wildcard-key syntax; pkg/tools'
// resolver builds its index through it.
func ParsePolicyWildcard(key string) (prefix string, segments int, delim byte, ok bool) {
	switch {
	case strings.HasSuffix(key, ".*"):
		prefix = key[:len(key)-2]
		if prefix == "" {
			return "", 0, 0, false
		}
		return prefix, strings.Count(prefix, ".") + 1, '.', true
	case strings.HasSuffix(key, "_*"):
		prefix = key[:len(key)-2]
		if prefix == "" {
			return "", 0, 0, false
		}
		return prefix, 1, '_', true
	}
	return "", 0, 0, false
}

// PolicyWildcardMatches reports whether a parsed wildcard covers name.
func PolicyWildcardMatches(name, prefix string, delim byte) bool {
	return strings.HasPrefix(name, prefix+string(delim)) || name == prefix
}

// BuildPolicyWildcardIndex parses and orders a map's wildcard keys, most
// specific first (segments, then prefix length, then lexicographic).
func BuildPolicyWildcardIndex(policies map[string]string) []PolicyWildcard {
	var out []PolicyWildcard
	for k, v := range policies {
		if prefix, seg, delim, ok := ParsePolicyWildcard(k); ok {
			out = append(out, PolicyWildcard{Prefix: prefix, Segments: seg, Delimiter: delim, Policy: ToolPolicy(v)})
		}
	}
	SortPolicyWildcards(out, func(i int) (int, string) { return out[i].Segments, out[i].Prefix })
	return out
}

// SortPolicyWildcards orders any wildcard slice by the resolver's specificity
// rule, reading each element's segment count and prefix through key.
func SortPolicyWildcards[T any](entries []T, key func(i int) (int, string)) {
	sort.SliceStable(entries, func(i, j int) bool {
		si, pi := key(i)
		sj, pj := key(j)
		if si != sj {
			return si > sj
		}
		if len(pi) != len(pj) {
			return len(pi) > len(pj)
		}
		return pi < pj
	})
}

// ResolvePolicyFromMap resolves name against one policy map: exact key
// first, then the most specific wildcard. "" means no entry matches.
func ResolvePolicyFromMap(name string, policies map[string]string) ToolPolicy {
	if p, ok := policies[name]; ok {
		return ToolPolicy(p)
	}
	for _, w := range BuildPolicyWildcardIndex(policies) {
		if PolicyWildcardMatches(name, w.Prefix, w.Delimiter) {
			return w.Policy
		}
	}
	return ""
}

// MergeStoredPolicyLayers is the strictest-wins global x agent merge
// (deny > ask > allow). One side empty: the other decides. Both empty: deny
// (callers log the coverage gap).
func MergeStoredPolicyLayers(g, a ToolPolicy) ToolPolicy {
	switch {
	case g == "" && a == "":
		return ToolPolicyDeny
	case g == "":
		return a
	case a == "":
		return g
	case g == ToolPolicyDeny || a == ToolPolicyDeny:
		return ToolPolicyDeny
	case g == ToolPolicyAsk || a == ToolPolicyAsk:
		return ToolPolicyAsk
	default:
		return ToolPolicyAllow
	}
}
