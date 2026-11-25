// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// Skills & Tools C-2: the skill-map self-heal runs on the config load path,
// like the tool-ceiling heal. It needs the installed skills of the home and
// the built-in skill names, which live in pkg/skills; pkg/skills imports
// this package, so the heal itself lives in pkg/skillperm and registers
// here when that package is linked in.

// SkillMapHeal heals the skill maps of the config.json at configPath on
// disk. It returns the stored skill_policies and skill_visibility maps
// after the heal and the bytes it wrote to config.json (nil when it wrote
// nothing). Both maps are nil when the heal does not run yet.
type SkillMapHeal func(configPath string) (policies, visibility map[string]string, written []byte, err error)

var skillMapHeal SkillMapHeal

// RegisterSkillMapHeal installs the skill-map self-heal run by every config
// load. It is called from a package init, before any load.
func RegisterSkillMapHeal(fn SkillMapHeal) {
	skillMapHeal = fn
}

// healSkillMapsOnLoad runs the registered skill-map heal and applies the
// keys it stored and removed to the loaded config. A failed heal fails the load, like the
// other load-time rewrites: the stored maps would otherwise miss keys.
func healSkillMapsOnLoad(cfg *Config, path string, onSelfHeal SelfHealWriteHook) error {
	if skillMapHeal == nil {
		return nil
	}
	policies, visibility, written, err := skillMapHeal(path)
	if err != nil {
		return err
	}
	cfg.Sandbox.SkillPolicies = ApplyHealedSkillKeys(cfg.Sandbox.SkillPolicies, policies)
	cfg.Sandbox.SkillVisibility = ApplyHealedSkillKeys(cfg.Sandbox.SkillVisibility, visibility)
	if written != nil && onSelfHeal != nil {
		onSelfHeal(written)
	}
	return nil
}

// ApplyHealedSkillKeys brings an in-memory skill map in line with the map
// the heal left on disk: it adds every key of healed that dst does not hold
// and drops every project key (project:<W>/<slug>) of dst that healed no
// longer holds, because the heal removed it with its workspace or folder
// (FR-080). Stored values are never overwritten. A nil healed (the heal did
// not run) changes nothing.
func ApplyHealedSkillKeys(dst, healed map[string]string) map[string]string {
	if healed == nil {
		return dst
	}
	for k := range dst {
		if _, _, project := ParseProjectSkillKey(k); !project {
			continue
		}
		if _, ok := healed[k]; !ok {
			delete(dst, k)
		}
	}
	for k, v := range healed {
		if _, ok := dst[k]; ok {
			continue
		}
		if dst == nil {
			dst = make(map[string]string, len(healed))
		}
		dst[k] = v
	}
	return dst
}
