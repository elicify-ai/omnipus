package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ReadPersistedToolCeiling returns the global tool-policy ceiling as the next
// config load would see it: the persisted sandbox.tool_policies of
// <home>/config.json with the shipped default filled in for any key the file
// does not carry (what ReconcileToolPolicyCeiling adds on load). A missing
// config.json yields the shipped defaults. God mode is not applied.
func ReadPersistedToolCeiling(home string) (map[string]ToolPolicy, error) {
	out := map[string]ToolPolicy{}
	for k, v := range DefaultToolPolicyCeiling() {
		out[k] = ToolPolicy(v)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var doc struct {
		Sandbox struct {
			ToolPolicies map[string]string `json:"tool_policies"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	for k, v := range doc.Sandbox.ToolPolicies {
		out[k] = ToolPolicy(v)
	}
	return out, nil
}

// PersistedSkillSettings is the skill part of config.json as the next load
// sees it (C-2, C-3): the global skill values, the trust value (legacy words
// migrated) and God Mode.
type PersistedSkillSettings struct {
	// Policies is nil when config.json carries no sandbox.skill_policies at
	// all (a home the upgrade has not reached yet).
	Policies map[string]string
	Trust    SkillTrustLevel
	GodMode  bool
}

// ReadPersistedSkillSettings reads the skill settings from config.json under
// home. A missing file yields the defaults (no map, trust ask, God Mode off).
func ReadPersistedSkillSettings(home string) (PersistedSkillSettings, error) {
	out := PersistedSkillSettings{Trust: SkillTrustAsk}
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, fmt.Errorf("read config: %w", err)
	}
	var doc struct {
		Sandbox struct {
			SkillPolicies map[string]string `json:"skill_policies"`
			SkillTrust    string            `json:"skill_trust"`
			GodMode       bool              `json:"god_mode"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out, fmt.Errorf("parse config: %w", err)
	}
	trust, _ := MigrateLegacySkillTrust(doc.Sandbox.SkillTrust)
	out.Policies = doc.Sandbox.SkillPolicies
	out.Trust = SkillTrustLevel(trust)
	out.GodMode = doc.Sandbox.GodMode
	return out, nil
}

// PersistedVisibility is the level part of config.json (C-1, C-6, C-15).
// Tools is nil when config.json carries no sandbox.tool_visibility (a home
// the tool-loading conversion has not reached yet); Skills likewise.
type PersistedVisibility struct {
	Tools  map[string]string
	Skills map[string]string
}

// ReadPersistedVisibility reads the stored levels from config.json under
// home: the one visibility authority each turn reads (C-15). A missing file
// yields no maps.
func ReadPersistedVisibility(home string) (PersistedVisibility, error) {
	var out PersistedVisibility
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, fmt.Errorf("read config: %w", err)
	}
	var doc struct {
		Sandbox struct {
			ToolVisibility  map[string]string `json:"tool_visibility"`
			SkillVisibility map[string]string `json:"skill_visibility"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out, fmt.Errorf("parse config: %w", err)
	}
	out.Tools = doc.Sandbox.ToolVisibility
	out.Skills = doc.Sandbox.SkillVisibility
	return out, nil
}
